package main

import (
	"crypto/tls"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Shared global variables used across all files
var (
	config     Config           // Holds the proxy configuration loaded from config.yaml
	configMux  sync.RWMutex     // Mutex for thread-safe access to config
	cert       *tls.Certificate // TLS certificate for HTTPS server
	baseDir    string           // Base directory (working dir for go run, executable dir for binary)
	certDir    string           // Directory for certificates
	certPath   string           // Path to certificate file
	keyPath    string           // Path to private key file
	configPath string           // Path to config.yaml

	// Loggers for different types of events
	errorLogger   *log.Logger // Logs errors to errors.log and console
	refreshLogger *log.Logger // Logs config/certificate refreshes to refresh.log and console
	trafficLogger *log.Logger // Logs traffic to traffic-YYYY-MM-DD.log and console
)

// init sets up the base directory and initializes logging
func init() {
	// Determine base directory based on execution context
	if runtime.GOOS == "windows" && len(os.Args) > 0 && filepath.Ext(os.Args[0]) == ".go" {
		// For "go run", use current working directory
		var err error
		baseDir, err = os.Getwd()
		if err != nil {
			log.Fatalf("Failed to get working directory: %v", err)
		}
	} else {
		// For compiled binary, use executable's directory
		exePath, err := os.Executable()
		if err != nil {
			log.Fatalf("Failed to get executable path: %v", err)
		}
		baseDir = filepath.Dir(exePath)
	}

	// Initialize logging to files and console
	setupLogging()
}

// setupLogging configures loggers for errors, refreshes, and traffic
func setupLogging() {
	logsDir := filepath.Join(baseDir, "logs")
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		log.Fatalf("Failed to create logs directory: %v", err)
	}

	// Error log: writes to errors.log and stdout
	errorFile, err := os.OpenFile(filepath.Join(logsDir, "errors.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatalf("Failed to open errors.log: %v", err)
	}
	errorLogger = log.New(io.MultiWriter(os.Stdout, errorFile), "ERROR: ", log.Ldate|log.Ltime|log.Lshortfile)

	// Refresh log: writes to refresh.log and stdout
	refreshFile, err := os.OpenFile(filepath.Join(logsDir, "refresh.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatalf("Failed to open refresh.log: %v", err)
	}
	refreshLogger = log.New(io.MultiWriter(os.Stdout, refreshFile), "REFRESH: ", log.Ldate|log.Ltime|log.Lshortfile)

	// Traffic log: managed in a goroutine for daily rotation
	go manageTrafficLogs(logsDir)
}

// manageTrafficLogs handles daily rotation of traffic logs with 7-day retention
func manageTrafficLogs(logsDir string) {
	for {
		dateStr := time.Now().Format("2006-01-02")
		trafficFile, err := os.OpenFile(filepath.Join(logsDir, "traffic-"+dateStr+".log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			errorLogger.Printf("Failed to open traffic log: %v", err)
			time.Sleep(1 * time.Minute)
			continue
		}
		trafficLogger = log.New(io.MultiWriter(os.Stdout, trafficFile), "TRAFFIC: ", log.Ldate|log.Ltime)

		// Cleanup logs older than 7 days
		cleanupOldLogs(logsDir)

		// Wait until next day
		nextDay := time.Now().Truncate(24 * time.Hour).Add(24 * time.Hour)
		time.Sleep(time.Until(nextDay))
		trafficFile.Close()
	}
}

// cleanupOldLogs removes traffic logs older than 7 days
func cleanupOldLogs(logsDir string) {
	files, err := os.ReadDir(logsDir)
	if err != nil {
		errorLogger.Printf("Failed to read logs directory: %v", err)
		return
	}

	cutoff := time.Now().AddDate(0, 0, -7)
	for _, file := range files {
		if strings.HasPrefix(file.Name(), "traffic-") && file.Name() != "traffic-"+time.Now().Format("2006-01-02")+".log" {
			info, err := file.Info()
			if err != nil {
				continue
			}
			if info.ModTime().Before(cutoff) {
				if err := os.Remove(filepath.Join(logsDir, file.Name())); err != nil {
					errorLogger.Printf("Failed to remove old log %s: %v", file.Name(), err)
				} else {
					refreshLogger.Printf("Removed old traffic log: %s", file.Name())
				}
			}
		}
	}
}

// main is the entry point, setting up and running the HTTP/HTTPS servers
func main() {
	// Set config file path and load or generate initial config
	configPath = filepath.Join(baseDir, "config.yaml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		config = generateDefaultConfig()
		if err := saveConfig(config); err != nil {
			errorLogger.Fatalf("Failed to save default config: %v", err)
		}
		refreshLogger.Println("Generated default config file")
	} else {
		cfg, err := loadConfig()
		if err != nil {
			errorLogger.Fatalf("Failed to load config: %v", err)
		}
		config = cfg
	}

	// Update certificate and config paths based on loaded config
	updatePaths()

	// Generate or load TLS certificate
	_, certErr := os.Stat(certPath)
	_, keyErr := os.Stat(keyPath)
	if os.IsNotExist(certErr) || os.IsNotExist(keyErr) {
		if err := generateSelfSignedCert(); err != nil {
			errorLogger.Fatalf("Failed to generate self-signed certificate: %v", err)
		}
	}
	if err := loadCertificate(); err != nil {
		errorLogger.Fatalf("Failed to load certificate: %v", err)
	}

	// Start background monitoring for config and certificate changes
	go monitorCertificates()
	go monitorConfig()

	// Configure HTTP server with timeouts for robustness
	httpServer := &http.Server{
		Addr:           config.ListenHTTP,
		Handler:        http.HandlerFunc(handler),
		MaxHeaderBytes: 1 << 20, // 1 MB max header size
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
	}

	// Configure HTTPS server with TLS and certificate fetching
	tlsConfig := &tls.Config{
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			configMux.RLock()
			defer configMux.RUnlock()
			return cert, nil
		},
	}
	httpsServer := &http.Server{
		Addr:           config.ListenHTTPS,
		Handler:        http.HandlerFunc(handler),
		TLSConfig:      tlsConfig,
		MaxHeaderBytes: 1 << 20,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
	}

	// Start HTTP server in a goroutine
	go func() {
		refreshLogger.Printf("Starting HTTP server on %s", config.ListenHTTP)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errorLogger.Fatalf("HTTP server error: %v", err)
		}
	}()

	// Start HTTPS server in the main goroutine
	refreshLogger.Printf("Starting HTTPS server on %s", config.ListenHTTPS)
	if err := httpsServer.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
		errorLogger.Fatalf("HTTPS server error: %v", err)
	}
}
