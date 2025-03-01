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
	transportPool = &http.Transport{
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
		ResponseHeaderTimeout: 60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
	}
	cache           = make(map[string]cachedResponse)
	defaultCacheTTL = 5 * time.Minute
	cacheMutex      sync.RWMutex

	// Rate limiter per client IP
	rateLimiters = make(map[string]*rate.Limiter)
	rateMutex    sync.RWMutex
	rateLimit    = rate.Limit(10) // 10 requests per second per client
	rateBurst    = 20             // Allow burst of 20 requests
)

type cachedResponse struct {
	body          []byte
	headers       http.Header
	statusCode    int
	cachedAt      time.Time
	cacheDuration time.Duration
	etag          string
}

func getReverseProxy(target string, skipVerify bool) *httputil.ReverseProxy {
	targetURL, err := url.Parse(target)
	if err != nil {
		log.Printf("Error parsing target URL %s: %v", target, err)
		errorLogger.Printf("Error parsing target URL %s: %v", target, err)
		return nil
	}

	director := func(req *http.Request) {
		req.URL.Scheme = targetURL.Scheme
		req.URL.Host = targetURL.Host
		// Preserve client's Host header for session continuity
		if req.Header.Get("Host") != "" {
			req.Host = req.Header.Get("Host")
		} else {
			req.Host = targetURL.Host
		}

		// Preserve all request headers, including cookies
		for k, v := range req.Header {
			req.Header[k] = v
		}

		// Preserve query parameters and path
		req.URL.RawQuery = req.URL.RawQuery
		if targetURL.Path != "" {
			req.URL.Path = strings.TrimPrefix(req.URL.Path, "/")
			req.URL.Path = singleJoin(targetURL.Path, req.URL.Path)
		}

		// WebSocket support: pass upgrade headers
		if strings.ToLower(req.Header.Get("Upgrade")) == "websocket" {
			req.Header.Set("Connection", "Upgrade")
		}

		trafficLogger.Printf("Request: %s %s -> %s [Host: %s]", req.Method, req.URL.String(), target, req.Host)
	}

	transport := transportPool
	if !skipVerify {
		transport = &http.Transport{
			MaxIdleConns:        transportPool.MaxIdleConns,
			MaxIdleConnsPerHost: transportPool.MaxIdleConnsPerHost,
			IdleConnTimeout:     transportPool.IdleConnTimeout,
			TLSClientConfig:     &tls.Config{InsecureSkipVerify: false},
		}
	}

	return &httputil.ReverseProxy{
		Director:  director,
		Transport: transport,
		ModifyResponse: func(resp *http.Response) error {
			// Preserve all response headers, including Set-Cookie
			for k, v := range resp.Header {
				resp.Header[k] = v
			}

			// Handle WebSocket upgrade
			if strings.ToLower(resp.Header.Get("Upgrade")) == "websocket" {
				return nil // No further modification needed for WebSocket
			}

			// Compression if client supports it and response isn’t already compressed
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
				resp.Header.Del("Content-Length") // Length changes after compression
				trafficLogger.Printf("Compressed response for %s", resp.Request.URL.String())
			}

			// Cache static content with ETag support
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
					etag = generateETag(body) // Simple ETag generation if not provided
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
				trafficLogger.Printf("Cached response for %s [ETag: %s]", resp.Request.URL.String(), etag)
			}

			trafficLogger.Printf("Response: %s %d from %s", resp.Status, resp.StatusCode, target)
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("Proxy error for %s: %v", r.Host, err)
			errorLogger.Printf("Proxy error for %s: %v", r.Host, err)
			http.Error(w, "Proxy error", http.StatusBadGateway)
		},
	}
}

func shouldCache(resp *http.Response) bool {
	if resp.Request.Method != "GET" || resp.StatusCode != http.StatusOK {
		return false
	}
	contentType := resp.Header.Get("Content-Type")
	return strings.HasPrefix(contentType, "text/") || strings.HasPrefix(contentType, "image/") ||
		strings.HasPrefix(contentType, "application/javascript") || strings.HasPrefix(contentType, "application/json")
}

func singleJoin(prefix, suffix string) string {
	prefix = strings.TrimSuffix(prefix, "/")
	suffix = strings.TrimPrefix(suffix, "/")
	return prefix + "/" + suffix
}

func generateETag(body []byte) string {
	return fmt.Sprintf(`"%x"`, md5.Sum(body)) // Simple ETag based on MD5 hash
}

func parseCacheControl(header string) time.Duration {
	if header == "" {
		return 0
	}
	parts := strings.Split(header, ",")
	for _, part := range parts {
		if strings.Contains(part, "max-age=") {
			ageStr := strings.TrimPrefix(part, "max-age=")
			ageStr = strings.TrimSpace(ageStr)
			if age, err := strconv.Atoi(ageStr); err == nil {
				return time.Duration(age) * time.Second
			}
		}
	}
	return 0
}

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

func handler(w http.ResponseWriter, r *http.Request) {
	configMux.RLock()
	defer configMux.RUnlock()

	// Rate limiting based on client IP
	clientIP := r.RemoteAddr[:strings.LastIndex(r.RemoteAddr, ":")] // Strip port
	limiter := getLimiter(clientIP)
	if !limiter.Allow() {
		http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
		trafficLogger.Printf("Rate limited client %s", clientIP)
		return
	}

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

	isHTTPS := target[:5] == "https"
	isHTTPReq := r.TLS == nil

	if isHTTPReq && isHTTPS && !noHTTPSRedirect {
		redirectURL := "https://" + r.Host + r.RequestURI
		http.Redirect(w, r, redirectURL, http.StatusMovedPermanently)
		return
	}

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

	proxy := getReverseProxy(target, skipVerify)
	if proxy == nil {
		http.Error(w, "Invalid target configuration", http.StatusInternalServerError)
		return
	}

	targetURL, err := url.Parse(target)
	if err != nil {
		errorLogger.Printf("Error parsing target URL %s: %v", target, err)
		http.Error(w, "Invalid target configuration", http.StatusInternalServerError)
		return
	}

	// WebSocket upgrade handling
	if strings.ToLower(r.Header.Get("Upgrade")) == "websocket" {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "WebSocket upgrade not supported", http.StatusInternalServerError)
			return
		}
		conn, _, err := hijacker.Hijack()
		if err != nil {
			errorLogger.Printf("Failed to hijack connection for WebSocket: %v", err)
			return
		}
		defer conn.Close()

		targetConn, err := transportPool.Dial("tcp", targetURL.Host)
		if err != nil {
			errorLogger.Printf("Failed to dial target for WebSocket: %v", err)
			return
		}
		defer targetConn.Close()

		// Forward request to target
		if err := r.Write(targetConn); err != nil {
			errorLogger.Printf("Failed to forward WebSocket request: %v", err)
			return
		}

		// Pipe connections
		go io.Copy(conn, targetConn)
		io.Copy(targetConn, conn)
		trafficLogger.Printf("WebSocket connection established: %s -> %s", r.Host, target)
		return
	}

	proxy.ServeHTTP(w, r)
}
