package main

import (
	"context"
	"crypto/tls"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"

	"golangproxy/config"
	"golangproxy/logger"
	"golangproxy/proxy"
	"golangproxy/server"
	"golangproxy/ssl"
)

// Global variables for dynamic configuration and certificate updates
var (
	configPath    = "config.yaml"
	routesMutex   sync.RWMutex            // Protects routes and defaultRoute
	certMutex     sync.RWMutex            // Protects currentCert
	currentConfig *config.Config          // Current configuration
	currentCert   *tls.Certificate        // Current SSL certificate
	routes        map[string]*proxy.Route // Host-specific routes
	defaultRoute  *proxy.Route            // Wildcard route
	watcher       *fsnotify.Watcher       // File watcher instance
)

// main initializes and runs the reverse proxy application
func main() {
	// Initialize logging to file and terminal
	logger.InitLogger()
	log := logger.Logger

	// Load initial configuration
	var err error
	currentConfig, err = config.LoadConfig(configPath)
	if err != nil {
		log.Fatalf("Error loading config: %v", err)
	}

	// Ensure SSL certificate and key files exist
	err = ssl.EnsureCertFiles(currentConfig.CertFile, currentConfig.KeyFile)
	if err != nil {
		log.Fatalf("Error ensuring cert files: %v", err)
	}

	// Load initial SSL certificate
	cert, err := tls.LoadX509KeyPair(currentConfig.CertFile, currentConfig.KeyFile)
	if err != nil {
		log.Fatalf("Error loading cert: %v", err)
	}
	certMutex.Lock()
	currentCert = &cert
	certMutex.Unlock()

	// Initialize proxy routes from config
	initializeRoutes(log)

	// Start the simple web server in a goroutine
	go server.StartServer()

	// Configure HTTP server
	httpServer := &http.Server{
		Addr: currentConfig.ListenHTTP,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			routesMutex.RLock()
			route := getRoute(r.Host)
			routesMutex.RUnlock()
			if strings.HasPrefix(route.Target, "https://") && !route.NoHTTPSRedirect {
				httpsURL := "https://" + r.Host + r.URL.Path
				if r.URL.RawQuery != "" {
					httpsURL += "?" + r.URL.RawQuery
				}
				http.Redirect(w, r, httpsURL, http.StatusMovedPermanently)
				return
			}
			route.Handler.ServeHTTP(w, r) // Use Handler instead of Proxy
		}),
		ErrorLog: logger.Logger, // Add this to filter server-level errors (from previous fix)
	}

	// Configure HTTPS server
	httpsServer := &http.Server{
		Addr: currentConfig.ListenHTTPS,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			routesMutex.RLock()
			route := getRoute(r.Host)
			routesMutex.RUnlock()
			route.Handler.ServeHTTP(w, r) // Use Handler instead of Proxy
		}),
		TLSConfig: &tls.Config{
			GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
				certMutex.RLock()
				defer certMutex.RUnlock()
				return currentCert, nil
			},
		},
		ErrorLog: logger.Logger, // Add this to filter server-level errors (from previous fix)
	}

	// Start servers in goroutines
	go func() {
		log.Println("Starting HTTP server on", currentConfig.ListenHTTP)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	go func() {
		log.Println("Starting HTTPS server on", currentConfig.ListenHTTPS)
		if err := httpsServer.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTPS server error: %v", err)
		}
	}()

	// Initialize file watcher
	watcher, err = fsnotify.NewWatcher()
	if err != nil {
		log.Fatalf("Error creating watcher: %v", err)
	}
	defer watcher.Close()

	// Watch initial config and cert files
	err = watcher.Add(configPath)
	if err != nil {
		log.Fatalf("Error watching config file: %v", err)
	}
	err = watcher.Add(currentConfig.CertFile)
	if err != nil {
		log.Println("Error watching cert file:", err)
	}
	err = watcher.Add(currentConfig.KeyFile)
	if err != nil {
		log.Println("Error watching key file:", err)
	}

	// Handle file updates in a goroutine
	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Op&fsnotify.Write == fsnotify.Write {
					switch event.Name {
					case configPath:
						log.Println("Config file changed, reloading...")
						reloadConfig(log)
					case currentConfig.CertFile, currentConfig.KeyFile:
						log.Println("Cert files changed, reloading cert...")
						reloadCert(log)
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Println("Watcher error:", err)
			}
		}
	}()

	// Handle graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Println("HTTP server shutdown error:", err)
	}
	if err := httpsServer.Shutdown(ctx); err != nil {
		log.Println("HTTPS server shutdown error:", err)
	}
}

// getRoute retrieves the appropriate proxy route for a host
func getRoute(host string) *proxy.Route {
	routesMutex.RLock()
	defer routesMutex.RUnlock()
	if route, ok := routes[host]; ok {
		return route
	}
	return defaultRoute
}

// initializeRoutes sets up the routes map and default route from the current config
func initializeRoutes(log *log.Logger) {
	routesMutex.Lock()
	defer routesMutex.Unlock()

	routes = make(map[string]*proxy.Route)
	for host, target := range currentConfig.Routes {
		if host == "*" {
			continue
		}
		trust := getConfigBool(currentConfig.TrustTarget, host)
		noRedirect := getConfigBool(currentConfig.NoHTTPSRedirect, host)
		route := proxy.CreateRoute(target, trust)
		route.NoHTTPSRedirect = noRedirect
		routes[host] = route
	}
	defaultTarget, ok := currentConfig.Routes["*"]
	if !ok {
		log.Fatal("Default route '*' not found in config")
	}
	defaultTrust := currentConfig.TrustTarget["*"]
	defaultNoRedirect := currentConfig.NoHTTPSRedirect["*"]
	defaultRoute = proxy.CreateRoute(defaultTarget, defaultTrust)
	defaultRoute.NoHTTPSRedirect = defaultNoRedirect
}

// getConfigBool retrieves a boolean config value, falling back to '*' if host-specific value is absent
func getConfigBool(m map[string]bool, host string) bool {
	if val, ok := m[host]; ok {
		return val
	}
	return m["*"]
}

// reloadConfig reloads the configuration and updates routes and certs if necessary
func reloadConfig(log *log.Logger) {
	newConfig, err := config.LoadConfig(configPath)
	if err != nil {
		log.Println("Error reloading config:", err)
		return
	}

	// Log differences between old and new config
	log.Println("Config file changed, reloading...")
	logConfigChanges(log, currentConfig, newConfig)

	// Store old cert file paths before updating config
	oldCertFile := currentConfig.CertFile
	oldKeyFile := currentConfig.KeyFile
	certChanged := newConfig.CertFile != oldCertFile || newConfig.KeyFile != oldKeyFile

	currentConfig = newConfig

	// Update routes
	initializeRoutes(log)

	// Update certificates and watcher if paths changed
	if certChanged {
		reloadCert(log)
		updateCertWatchers(log, oldCertFile, oldKeyFile)
	}
}

// logConfigChanges logs the differences between old and new config
func logConfigChanges(log *log.Logger, oldConfig, newConfig *config.Config) {
	if oldConfig.ListenHTTP != newConfig.ListenHTTP {
		log.Printf("listen_http changed from %s to %s", oldConfig.ListenHTTP, newConfig.ListenHTTP)
	}
	if oldConfig.ListenHTTPS != newConfig.ListenHTTPS {
		log.Printf("listen_https changed from %s to %s", oldConfig.ListenHTTPS, newConfig.ListenHTTPS)
	}
	if oldConfig.CertFile != newConfig.CertFile {
		log.Printf("cert_file changed from %s to %s", oldConfig.CertFile, newConfig.CertFile)
	}
	if oldConfig.KeyFile != newConfig.KeyFile {
		log.Printf("key_file changed from %s to %s", oldConfig.KeyFile, newConfig.KeyFile)
	}

	// Compare Routes
	for key := range oldConfig.Routes {
		if newVal, ok := newConfig.Routes[key]; !ok {
			log.Printf("Route %s removed (was %s)", key, oldConfig.Routes[key])
		} else if oldConfig.Routes[key] != newVal {
			log.Printf("Route %s changed from %s to %s", key, oldConfig.Routes[key], newVal)
		}
	}
	for key, newVal := range newConfig.Routes {
		if _, ok := oldConfig.Routes[key]; !ok {
			log.Printf("Route %s added: %s", key, newVal)
		}
	}

	// Compare TrustTarget
	for key := range oldConfig.TrustTarget {
		if newVal, ok := newConfig.TrustTarget[key]; !ok {
			log.Printf("trust_target %s removed (was %t)", key, oldConfig.TrustTarget[key])
		} else if oldConfig.TrustTarget[key] != newVal {
			log.Printf("trust_target %s changed from %t to %t", key, oldConfig.TrustTarget[key], newVal)
		}
	}
	for key, newVal := range newConfig.TrustTarget {
		if _, ok := oldConfig.TrustTarget[key]; !ok {
			log.Printf("trust_target %s added: %t", key, newVal)
		}
	}

	// Compare NoHTTPSRedirect
	for key := range oldConfig.NoHTTPSRedirect {
		if newVal, ok := newConfig.NoHTTPSRedirect[key]; !ok {
			log.Printf("no_https_redirect %s removed (was %t)", key, oldConfig.NoHTTPSRedirect[key])
		} else if oldConfig.NoHTTPSRedirect[key] != newVal {
			log.Printf("no_https_redirect %s changed from %t to %t", key, oldConfig.NoHTTPSRedirect[key], newVal)
		}
	}
	for key, newVal := range newConfig.NoHTTPSRedirect {
		if _, ok := oldConfig.NoHTTPSRedirect[key]; !ok {
			log.Printf("no_https_redirect %s added: %t", key, newVal)
		}
	}
}

// reloadCert reloads the SSL certificate from disk
func reloadCert(log *log.Logger) {
	cert, err := tls.LoadX509KeyPair(currentConfig.CertFile, currentConfig.KeyFile)
	if err != nil {
		log.Println("Error reloading cert:", err)
		return
	}
	certMutex.Lock()
	currentCert = &cert
	certMutex.Unlock()
}

// updateCertWatchers updates the file watcher for new cert file paths
func updateCertWatchers(log *log.Logger, oldCertFile, oldKeyFile string) {
	if oldCertFile != currentConfig.CertFile {
		watcher.Remove(oldCertFile)
		if err := watcher.Add(currentConfig.CertFile); err != nil {
			log.Println("Error watching new cert file:", err)
		}
	}
	if oldKeyFile != currentConfig.KeyFile {
		watcher.Remove(oldKeyFile)
		if err := watcher.Add(currentConfig.KeyFile); err != nil {
			log.Println("Error watching new key file:", err)
		}
	}
}
