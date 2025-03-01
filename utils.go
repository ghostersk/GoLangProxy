package main

import "path/filepath"

// updatePaths sets certificate and config file paths based on the current config
func updatePaths() {
	certDir = filepath.Join(baseDir, config.CertDir)   // Certificate directory path
	certPath = filepath.Join(certDir, config.CertFile) // Full certificate file path
	keyPath = filepath.Join(certDir, config.KeyFile)   // Full private key file path
	configPath = filepath.Join(baseDir, "config.yaml") // Config file path
}
