package main

import "path/filepath"

func updatePaths() {
	certDir = filepath.Join(baseDir, config.CertDir)
	certPath = filepath.Join(certDir, config.CertFile)
	keyPath = filepath.Join(certDir, config.KeyFile)
	configPath = filepath.Join(baseDir, "config.yaml")
}
