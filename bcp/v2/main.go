package main

import (
	"crypto/tls"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	config     Config
	configMux  sync.RWMutex
	cert       *tls.Certificate
	baseDir    string
	certDir    string
	certPath   string
	keyPath    string
	configPath string

	// Loggers
	errorLogger   *log.Logger
	refreshLogger *log.Logger
	trafficLogger *log.Logger
)

func init() {
	// Get the absolute path of the running executable (or main.go in `go run`)
	exePath, err := os.Executable()
	if err != nil {
		log.Fatalf("Failed to get executable path: %v", err)
	}
	baseDir = filepath.Dir(exePath)
	/*
		if runtime.GOOS == "windows" && len(os.Args) > 0 && filepath.Ext(os.Args[0]) == ".go" {
			var err error
			baseDir, err = os.Getwd()
			if err != nil {
				log.Fatalf("Failed to get working directory: %v", err)
			}
		} else {
			exePath, err := os.Executable()
			if err != nil {
				log.Fatalf("Failed to get executable path: %v", err)
			}
			baseDir = filepath.Dir(exePath)
		}
	*/
	// Setup logging
	setupLogging()
}

func setupLogging() {
	logsDir := filepath.Join(baseDir, "logs")
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		log.Fatalf("Failed to create logs directory: %v", err)
	}

	// Error log
	errorFile, err := os.OpenFile(filepath.Join(logsDir, "errors.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatalf("Failed to open errors.log: %v", err)
	}
	errorLogger = log.New(io.MultiWriter(os.Stdout, errorFile), "ERROR: ", log.Ldate|log.Ltime|log.Lshortfile)

	// Refresh log
	refreshFile, err := os.OpenFile(filepath.Join(logsDir, "refresh.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatalf("Failed to open refresh.log: %v", err)
	}
	refreshLogger = log.New(io.MultiWriter(os.Stdout, refreshFile), "REFRESH: ", log.Ldate|log.Ltime|log.Lshortfile)

	// Traffic log (daily rotation)
	go manageTrafficLogs(logsDir)
}

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

		// Cleanup old logs
		cleanupOldLogs(logsDir)

		// Wait until next day
		nextDay := time.Now().Truncate(24 * time.Hour).Add(24 * time.Hour)
		time.Sleep(time.Until(nextDay))
		trafficFile.Close()
	}
}

func cleanupOldLogs(logsDir string) {
	files, err := os.ReadDir(logsDir)
	if err != nil {
		errorLogger.Printf("Failed to read logs directory: %v", err)
		return
	}

	cutoff := time.Now().AddDate(0, 0, -7) // 7 days ago
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

func main() {
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

	updatePaths()

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

	go monitorCertificates()
	go monitorConfig()

	httpServer := &http.Server{
		Addr:           config.ListenHTTP,
		Handler:        http.HandlerFunc(handler),
		MaxHeaderBytes: 1 << 20,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
	}

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

	go func() {
		log.Printf("Starting HTTP server on %s", config.ListenHTTP)
		trafficLogger.Printf("Starting HTTP server on %s", config.ListenHTTP)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errorLogger.Fatalf("HTTP server error: %v", err)
		}
	}()

	log.Printf("Starting HTTPS server on %s", config.ListenHTTPS)
	trafficLogger.Printf("Starting HTTPS server on %s", config.ListenHTTPS)
	if err := httpsServer.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
		errorLogger.Fatalf("HTTPS server error: %v", err)
	}
}
