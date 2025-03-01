package main

import (
	"crypto/tls"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

func getReverseProxy(target string, skipVerify bool) *httputil.ReverseProxy {
	targetURL, err := url.Parse(target)
	if err != nil {
		log.Printf("Error parsing target URL %s: %v", target, err)
		return nil
	}

	director := func(req *http.Request) {
		req.URL.Scheme = targetURL.Scheme
		req.URL.Host = targetURL.Host
		req.Host = targetURL.Host // Set Host header to match target

		// Preserve original headers
		for k, v := range req.Header {
			if k != "Host" { // Host is set above
				req.Header[k] = v
			}
		}

		// Ensure the full path is preserved
		if targetURL.Path != "" {
			req.URL.Path = strings.TrimPrefix(req.URL.Path, "/") // Avoid double slashes
			req.URL.Path = singleJoin(targetURL.Path, req.URL.Path)
		}
	}

	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: skipVerify},
	}

	return &httputil.ReverseProxy{
		Director:  director,
		Transport: transport,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("Proxy error for %s: %v", r.Host, err)
			http.Error(w, "Proxy error", http.StatusBadGateway)
		},
	}
}

// singleJoin ensures a single slash between path segments
func singleJoin(prefix, suffix string) string {
	prefix = strings.TrimSuffix(prefix, "/")
	suffix = strings.TrimPrefix(suffix, "/")
	return prefix + "/" + suffix
}

func handler(w http.ResponseWriter, r *http.Request) {
	configMux.RLock()
	defer configMux.RUnlock()

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

	// Check if request is HTTP and target is HTTPS
	isHTTPS := target[:5] == "https"
	isHTTPReq := r.TLS == nil // r.TLS is nil for HTTP, non-nil for HTTPS

	if isHTTPReq && isHTTPS && !noHTTPSRedirect {
		// Redirect to HTTPS version of the host
		redirectURL := "https://" + r.Host + r.RequestURI
		http.Redirect(w, r, redirectURL, http.StatusMovedPermanently)
		return
	}

	proxy := getReverseProxy(target, skipVerify)
	if proxy == nil {
		http.Error(w, "Invalid target configuration", http.StatusInternalServerError)
		return
	}
	proxy.ServeHTTP(w, r)
}
