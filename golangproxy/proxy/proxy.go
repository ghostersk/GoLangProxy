package proxy

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"

	"golangproxy/logger"
)

// Route holds proxy configuration for a specific host
type Route struct {
	Proxy           *httputil.ReverseProxy // The reverse proxy instance
	Handler         http.Handler           // Custom handler wrapping the proxy
	NoHTTPSRedirect bool                   // Disable HTTP to HTTPS redirect
	Target          string                 // Target URL for proxying
}

// CreateRoute initializes a reverse proxy for a target with trust settings
func CreateRoute(target string, trustInvalidCert bool) *Route {
	url, _ := url.Parse(target)
	proxy := httputil.NewSingleHostReverseProxy(url)
	if url.Scheme == "https" {
		proxy.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: trustInvalidCert},
		}
	}

	// Modify the Director based on whether the target is an IP or hostname
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		if isIPTarget(url.Hostname()) {
			// For IP targets, preserve the incoming Host header (e.g., main.example.com)
			// This ensures session cookies match the client's requested domain
		} else {
			// For hostname targets, set Host to the target's hostname (e.g., example.com)
			req.Host = url.Host
		}
		req.Header.Set("X-Forwarded-For", req.RemoteAddr)
		req.Header.Set("X-Forwarded-Host", req.Host)
		req.Header.Set("X-Forwarded-Proto", url.Scheme)
		if req.Header.Get("User-Agent") == "" {
			req.Header.Set("User-Agent", "GoLangProxy")
		}
		//logger.Logger.Printf("Proxying to %s - Headers: %v, Cookies: %v", target, req.Header, req.Cookies())
	}

	// Create a custom handler to wrap the proxy and filter context canceled errors
	handler := http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rwWrapper := &responseWriterWrapper{ResponseWriter: rw}
		proxy.ServeHTTP(rwWrapper, req)
		if err := req.Context().Err(); err != nil && err != context.Canceled {
			logger.Logger.Printf("Proxy error for %s: %v", target, err)
		}
		//logger.Logger.Printf("Response from %s - Headers: %v, Status: %d", target, rwWrapper.Header(), rwWrapper.status)
	})

	return &Route{
		Proxy:   proxy,
		Handler: handler,
		Target:  target,
	}
}

// isIPTarget checks if the target hostname is an IP address
func isIPTarget(host string) bool {
	// Split host and port if a port is present (e.g., "10.100.111.254:4444")
	hostname, _, err := net.SplitHostPort(host)
	if err != nil {
		// If there's no port (or an error splitting), use the original host
		hostname = host
	}
	return net.ParseIP(hostname) != nil
}

// responseWriterWrapper captures response status and headers
type responseWriterWrapper struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriterWrapper) WriteHeader(status int) {
	rw.status = status
	rw.ResponseWriter.WriteHeader(status)
}

func (rw *responseWriterWrapper) Write(b []byte) (int, error) {
	if rw.status == 0 {
		rw.status = http.StatusOK
	}
	return rw.ResponseWriter.Write(b)
}
