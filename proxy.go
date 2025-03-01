package main

import (
	"bytes"
	"compress/gzip"
	"crypto/md5"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

var (
	// transportPool configures HTTP transport with timeouts matching Nginx defaults
	transportPool = &http.Transport{
		MaxIdleConns:          100,                                                  // Max idle connections
		MaxIdleConnsPerHost:   10,                                                   // Max idle per host
		IdleConnTimeout:       90 * time.Second,                                     // Idle connection timeout
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second}).DialContext, // Dial timeout
		ResponseHeaderTimeout: 60 * time.Second,                                     // Response header timeout (Nginx-like)
		TLSHandshakeTimeout:   10 * time.Second,                                     // TLS handshake timeout
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},                // Default skip TLS verification
	}
	cache      = make(map[string]cachedResponse) // Cache for static responses
	cacheMutex sync.RWMutex                      // Mutex for cache access

	// Rate limiting per client IP
	rateLimiters = make(map[string]*rate.Limiter)
	rateMutex    sync.RWMutex
	rateLimit    = rate.Limit(10) // 10 req/s per client
	rateBurst    = 20             // Burst allowance

	defaultCacheTTL = 5 * time.Minute // Default TTL for cached responses
)

// cachedResponse stores cached response details
type cachedResponse struct {
	body          []byte
	headers       http.Header
	statusCode    int
	cachedAt      time.Time
	cacheDuration time.Duration
	etag          string
}

// getReverseProxy creates a reverse proxy for a target URL
func getReverseProxy(target string, skipVerify bool, originalReq *http.Request) *httputil.ReverseProxy {
	targetURL, err := url.Parse(target)
	if err != nil {
		log.Printf("Error parsing target URL %s: %v", target, err)
		errorLogger.Printf("Error parsing target URL %s: %v", target, err)
		return nil
	}

	// director modifies the request to forward it to the target
	director := func(req *http.Request) {
		req.URL.Scheme = targetURL.Scheme
		req.URL.Host = targetURL.Host
		// Preserve the original client’s Host header for session continuity
		if originalReq.Header.Get("Host") != "" {
			req.Host = originalReq.Header.Get("Host")
		} else {
			req.Host = targetURL.Host
		}

		// Copy all request headers to ensure cookies and session data are preserved
		for k, v := range originalReq.Header {
			req.Header[k] = v
		}

		// Preserve query parameters and full path
		req.URL.RawQuery = originalReq.URL.RawQuery
		if targetURL.Path != "" {
			req.URL.Path = strings.TrimPrefix(originalReq.URL.Path, "/")
			req.URL.Path = singleJoin(targetURL.Path, req.URL.Path)
		}

		// Ensure WebSocket headers are passed correctly
		if strings.ToLower(req.Header.Get("Upgrade")) == "websocket" {
			req.Header.Set("Connection", "Upgrade")
		}

		// Log request details for debugging
		trafficLogger.Printf("Request: %s %s -> %s [Host: %s] [Headers: %v]", req.Method, req.URL.String(), target, req.Host, req.Header)
	}

	transport := transportPool
	if !skipVerify {
		// Use a new transport with TLS verification if skipVerify is false
		transport = &http.Transport{
			MaxIdleConns:          transportPool.MaxIdleConns,
			MaxIdleConnsPerHost:   transportPool.MaxIdleConnsPerHost,
			IdleConnTimeout:       transportPool.IdleConnTimeout,
			DialContext:           transportPool.DialContext,
			ResponseHeaderTimeout: transportPool.ResponseHeaderTimeout,
			TLSHandshakeTimeout:   transportPool.TLSHandshakeTimeout,
			TLSClientConfig:       &tls.Config{InsecureSkipVerify: false},
		}
	}

	return &httputil.ReverseProxy{
		Director:  director,
		Transport: transport,
		// ModifyResponse processes responses, avoiding corruption
		ModifyResponse: func(resp *http.Response) error {
			// Preserve all response headers, including Set-Cookie for sessions
			for k, v := range resp.Header {
				resp.Header[k] = v
			}

			// Skip modification for WebSocket responses
			if strings.ToLower(resp.Header.Get("Upgrade")) == "websocket" {
				return nil
			}

			// Apply compression only if not already compressed and client supports it
			if resp.Header.Get("Content-Encoding") == "" && strings.Contains(resp.Request.Header.Get("Accept-Encoding"), "gzip") {
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					errorLogger.Printf("Error reading response body for compression: %v", err)
					return err
				}
				resp.Body.Close()

				var buf bytes.Buffer
				gw := gzip.NewWriter(&buf)
				if _, err := gw.Write(body); err != nil {
					errorLogger.Printf("Error compressing response: %v", err)
					return err
				}
				gw.Close()

				resp.Body = io.NopCloser(&buf)
				resp.Header.Set("Content-Encoding", "gzip")
				resp.Header.Del("Content-Length")
				trafficLogger.Printf("Compressed response for %s", resp.Request.URL.String())
			}

			// Cache static content if applicable
			if shouldCache(resp) {
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					errorLogger.Printf("Error reading response body for caching: %v", err)
					return err
				}
				resp.Body.Close()
				resp.Body = io.NopCloser(bytes.NewReader(body))

				etag := resp.Header.Get("ETag")
				if etag == "" {
					etag = generateETag(body)
				}

				cacheDuration := parseCacheControl(resp.Header.Get("Cache-Control"))
				if cacheDuration == 0 {
					cacheDuration = defaultCacheTTL
				}

				cacheMutex.Lock()
				cache[resp.Request.URL.String()] = cachedResponse{
					body:          body,
					headers:       resp.Header.Clone(),
					statusCode:    resp.StatusCode,
					cachedAt:      time.Now(),
					cacheDuration: cacheDuration,
					etag:          etag,
				}
				cacheMutex.Unlock()
				resp.Header.Set("ETag", etag)
				trafficLogger.Printf("Cached response for %s [ETag: %s]", resp.Request.URL.String(), etag)
			}

			trafficLogger.Printf("Response: %s %d from %s [Headers: %v]", resp.Status, resp.StatusCode, target, resp.Header)
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if err.Error() == "context canceled" {
				trafficLogger.Printf("Request canceled by client for %s: %v", r.Host, err)
				return
			}
			log.Printf("Proxy error for %s: %v", r.Host, err)
			errorLogger.Printf("Proxy error for %s: %v", r.Host, err)
			http.Error(w, "Proxy error", http.StatusBadGateway)
		},
	}
}

// shouldCache checks if a response should be cached based on method and content type
func shouldCache(resp *http.Response) bool {
	if resp.Request.Method != "GET" || resp.StatusCode != http.StatusOK {
		return false
	}
	contentType := resp.Header.Get("Content-Type")
	return strings.HasPrefix(contentType, "text/") || strings.HasPrefix(contentType, "image/") ||
		strings.HasPrefix(contentType, "application/javascript") || strings.HasPrefix(contentType, "application/json")
}

// singleJoin combines path segments with a single slash
func singleJoin(prefix, suffix string) string {
	prefix = strings.TrimSuffix(prefix, "/")
	suffix = strings.TrimPrefix(suffix, "/")
	return prefix + "/" + suffix
}

// generateETag creates an ETag from the response body using MD5
func generateETag(body []byte) string {
	return fmt.Sprintf(`"%x"`, md5.Sum(body))
}

// parseCacheControl extracts max-age from the Cache-Control header for caching duration
func parseCacheControl(header string) time.Duration {
	if header == "" {
		return 0
	}
	parts := strings.Split(header, ",")
	for _, part := range parts {
		if strings.HasPrefix(part, "max-age=") {
			ageStr := strings.TrimSpace(strings.TrimPrefix(part, "max-age="))
			if age, err := strconv.Atoi(ageStr); err == nil {
				return time.Duration(age) * time.Second
			}
		}
	}
	return 0
}

// getLimiter manages rate limiters per client IP
func getLimiter(ip string) *rate.Limiter {
	rateMutex.Lock()
	defer rateMutex.Unlock()

	if limiter, exists := rateLimiters[ip]; exists {
		return limiter
	}
	limiter := rate.NewLimiter(rateLimit, rateBurst)
	rateLimiters[ip] = limiter
	return limiter
}

// handler processes incoming HTTP/HTTPS requests
func handler(w http.ResponseWriter, r *http.Request) {
	configMux.RLock()
	defer configMux.RUnlock()

	// Rate limiting based on client IP
	clientIP := r.RemoteAddr[:strings.LastIndex(r.RemoteAddr, ":")]
	limiter := getLimiter(clientIP)
	if !limiter.Allow() {
		http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
		trafficLogger.Printf("Rate limited client %s", clientIP)
		return
	}

	// Retrieve target and settings for the requested host
	target, exists := config.Routes[r.Host]
	skipVerify := config.TrustTarget[r.Host]
	noHTTPSRedirect := config.NoHTTPSRedirect[r.Host]

	if !exists {
		if target, exists = config.Routes["*"]; !exists {
			http.Error(w, "Host not configured", http.StatusNotFound)
			return
		}
		skipVerify = config.TrustTarget["*"]
		noHTTPSRedirect = config.NoHTTPSRedirect["*"]
	}

	// Handle HTTP->HTTPS redirect if applicable
	isHTTPS := target[:5] == "https"
	isHTTPReq := r.TLS == nil
	if isHTTPReq && isHTTPS && !noHTTPSRedirect {
		redirectURL := "https://" + r.Host + r.RequestURI
		http.Redirect(w, r, redirectURL, http.StatusMovedPermanently)
		return
	}

	// Check cache for static content
	cacheKey := r.URL.String()
	cacheMutex.RLock()
	if cached, ok := cache[cacheKey]; ok && time.Since(cached.cachedAt) < cached.cacheDuration {
		if etag := r.Header.Get("If-None-Match"); etag != "" && etag == cached.etag {
			w.WriteHeader(http.StatusNotModified)
			trafficLogger.Printf("Served 304 Not Modified from cache: %s [ETag: %s]", cacheKey, cached.etag)
		} else {
			for k, v := range cached.headers {
				w.Header()[k] = v
			}
			w.WriteHeader(cached.statusCode)
			w.Write(cached.body)
			trafficLogger.Printf("Served from cache: %s [ETag: %s]", cacheKey, cached.etag)
		}
		cacheMutex.RUnlock()
		return
	}
	cacheMutex.RUnlock()

	proxy := getReverseProxy(target, skipVerify, r)
	if proxy == nil {
		http.Error(w, "Invalid target configuration", http.StatusInternalServerError)
		return
	}

	// Handle WebSocket connections
	if strings.ToLower(r.Header.Get("Upgrade")) == "websocket" {
		targetURL, err := url.Parse(target)
		if err != nil {
			errorLogger.Printf("Error parsing target URL for WebSocket: %v", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		hijacker, ok := w.(http.Hijacker)
		if !ok {
			errorLogger.Printf("WebSocket upgrade not supported by server")
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		clientConn, _, err := hijacker.Hijack()
		if err != nil {
			errorLogger.Printf("Failed to hijack connection for WebSocket: %v", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		defer clientConn.Close()

		dialer := transportPool
		if !skipVerify {
			dialer = &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: false},
			}
		}
		targetConn, err := dialer.Dial("tcp", targetURL.Host)
		if err != nil {
			errorLogger.Printf("Failed to dial target for WebSocket: %v", err)
			return
		}
		defer targetConn.Close()

		if err := r.Write(targetConn); err != nil {
			errorLogger.Printf("Failed to forward WebSocket request: %v", err)
			return
		}

		errChan := make(chan error, 2)
		go func() {
			_, err := io.Copy(targetConn, clientConn)
			errChan <- err
		}()
		go func() {
			_, err := io.Copy(clientConn, targetConn)
			errChan <- err
		}()
		trafficLogger.Printf("WebSocket connection established: %s -> %s", r.Host, target)
		<-errChan
		trafficLogger.Printf("WebSocket connection closed: %s -> %s", r.Host, target)
		return
	}

	proxy.ServeHTTP(w, r)
}
