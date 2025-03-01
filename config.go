package main

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v2"
)

// Config defines the structure of the proxy configuration loaded from config.yaml
type Config struct {
	ListenHTTP      string            `yaml:"listen_http"`       // Port for HTTP server (e.g., ":80")
	ListenHTTPS     string            `yaml:"listen_https"`      // Port for HTTPS server (e.g., ":443")
	CertDir         string            `yaml:"cert_dir"`          // Directory for certificate files
	CertFile        string            `yaml:"cert_file"`         // Certificate filename
	KeyFile         string            `yaml:"key_file"`          // Private key filename
	Routes          map[string]string `yaml:"routes"`            // Mapping of hostnames to target URLs
	TrustTarget     map[string]bool   `yaml:"trust_target"`      // Whether to skip TLS verification for targets
	NoHTTPSRedirect map[string]bool   `yaml:"no_https_redirect"` // Whether to skip HTTP->HTTPS redirect
}

// loadConfig reads and parses the config.yaml file
func loadConfig() (Config, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return Config{}, fmt.Errorf("failed to read config %s: %v", configPath, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("failed to unmarshal config: %v", err)
	}
	return cfg, nil
}

// generateDefaultConfig creates a default configuration if config.yaml doesn’t exist
func generateDefaultConfig() Config {
	return Config{
		ListenHTTP:  ":80",
		ListenHTTPS: ":443",
		CertDir:     "./certificate",
		CertFile:    "certificate.pem",
		KeyFile:     "key.pem",
		Routes: map[string]string{
			"*":                "https://127.0.0.1:3000",      // Wildcard route
			"main.example.com": "https://10.100.111.254:4444", // Specific route
		},
		TrustTarget: map[string]bool{
			"*":                true, // Skip TLS verification by default
			"main.example.com": true,
		},
		NoHTTPSRedirect: map[string]bool{
			"*":                false, // Redirect HTTP to HTTPS by default
			"main.example.com": false,
		},
	}
}

// saveConfig writes the configuration to config.yaml
func saveConfig(cfg Config) error {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return fmt.Errorf("failed to create base directory %s: %v", baseDir, err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %v", err)
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config to %s: %v", configPath, err)
	}
	return nil
}

// monitorConfig watches config.yaml for changes and updates the in-memory config
func monitorConfig() {
	var lastModTime time.Time
	for {
		configInfo, err := os.Stat(configPath)
		if err != nil {
			errorLogger.Printf("Error checking config file: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		if configInfo.ModTime() != lastModTime {
			newConfig, err := loadConfig()
			if err != nil {
				errorLogger.Printf("Error reloading config: %v", err)
			} else {
				configMux.Lock()
				// Update individual fields only if they’ve changed
				if newConfig.ListenHTTP != config.ListenHTTP {
					config.ListenHTTP = newConfig.ListenHTTP
					refreshLogger.Printf("Updated listen_http to %s", config.ListenHTTP)
				}
				if newConfig.ListenHTTPS != config.ListenHTTPS {
					config.ListenHTTPS = newConfig.ListenHTTPS
					refreshLogger.Printf("Updated listen_https to %s", config.ListenHTTPS)
				}
				if newConfig.CertDir != config.CertDir || newConfig.CertFile != config.CertFile || newConfig.KeyFile != config.KeyFile {
					config.CertDir = newConfig.CertDir
					config.CertFile = newConfig.CertFile
					config.KeyFile = newConfig.KeyFile
					updatePaths()
					if err := loadCertificate(); err != nil {
						errorLogger.Printf("Error reloading certificate after path change: %v", err)
					} else {
						refreshLogger.Println("Updated certificate paths and reloaded certificate")
					}
				}
				// Update routes
				for k, v := range newConfig.Routes {
					if oldV, exists := config.Routes[k]; !exists || oldV != v {
						config.Routes[k] = v
						refreshLogger.Printf("Updated route %s to %s", k, v)
					}
				}
				for k := range config.Routes {
					if _, exists := newConfig.Routes[k]; !exists {
						delete(config.Routes, k)
						refreshLogger.Printf("Removed route %s", k)
					}
				}
				// Update trust_target
				for k, v := range newConfig.TrustTarget {
					if oldV, exists := config.TrustTarget[k]; !exists || oldV != v {
						config.TrustTarget[k] = v
						refreshLogger.Printf("Updated trust_target %s to %v", k, v)
					}
				}
				for k := range config.TrustTarget {
					if _, exists := newConfig.TrustTarget[k]; !exists {
						delete(config.TrustTarget, k)
						refreshLogger.Printf("Removed trust_target %s", k)
					}
				}
				// Update no_https_redirect
				for k, v := range newConfig.NoHTTPSRedirect {
					if oldV, exists := config.NoHTTPSRedirect[k]; !exists || oldV != v {
						config.NoHTTPSRedirect[k] = v
						refreshLogger.Printf("Updated no_https_redirect %s to %v", k, v)
					}
				}
				for k := range config.NoHTTPSRedirect {
					if _, exists := newConfig.NoHTTPSRedirect[k]; !exists {
						delete(config.NoHTTPSRedirect, k)
						refreshLogger.Printf("Removed no_https_redirect %s", k)
					}
				}
				configMux.Unlock()
				refreshLogger.Println("Config reloaded successfully")
				lastModTime = configInfo.ModTime()
			}
		}
		time.Sleep(5 * time.Second) // Poll every 5 seconds
	}
}
