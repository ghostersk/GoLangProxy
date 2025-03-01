package main

// go run main.go config.go certificate.go proxy.go utils.go
// go build -o proxy
import (
	"crypto/tls"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

// Shared global variables (declared only here)
var (
	config     Config
	configMux  sync.RWMutex
	cert       *tls.Certificate
	baseDir    string
	certDir    string
	certPath   string
	keyPath    string
	configPath string
)

func init() {
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
}

func main() {
	configPath = filepath.Join(baseDir, "config.yaml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		config = generateDefaultConfig()
		if err := saveConfig(config); err != nil {
			log.Fatalf("Failed to save default config: %v", err)
		}
		log.Println("Generated default config file")
	} else {
		cfg, err := loadConfig()
		if err != nil {
			log.Fatalf("Failed to load config: %v", err)
		}
		config = cfg
	}

	updatePaths()

	_, certErr := os.Stat(certPath)
	_, keyErr := os.Stat(keyPath)
	if os.IsNotExist(certErr) || os.IsNotExist(keyErr) {
		if err := generateSelfSignedCert(); err != nil {
			log.Fatalf("Failed to generate self-signed certificate: %v", err)
		}
	}

	if err := loadCertificate(); err != nil {
		log.Fatalf("Failed to load certificate: %v", err)
	}

	go monitorCertificates()
	go monitorConfig()

	httpServer := &http.Server{
		Addr:    config.ListenHTTP,
		Handler: http.HandlerFunc(handler),
	}

	tlsConfig := &tls.Config{
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			configMux.RLock()
			defer configMux.RUnlock()
			return cert, nil
		},
	}
	httpsServer := &http.Server{
		Addr:      config.ListenHTTPS,
		Handler:   http.HandlerFunc(handler),
		TLSConfig: tlsConfig,
	}

	go func() {
		log.Printf("Starting HTTP server on %s", config.ListenHTTP)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	log.Printf("Starting HTTPS server on %s", config.ListenHTTPS)
	if err := httpsServer.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
		log.Fatalf("HTTPS server error: %v", err)
	}
}
