package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"gopkg.in/yaml.v2"
)

type Config struct {
	ListenHTTP      string            `yaml:"listen_http"`
	ListenHTTPS     string            `yaml:"listen_https"`
	CertDir         string            `yaml:"cert_dir"`
	CertFile        string            `yaml:"cert_file"`
	KeyFile         string            `yaml:"key_file"`
	Routes          map[string]string `yaml:"routes"`
	TrustTarget     map[string]bool   `yaml:"trust_target"`
	NoHTTPSRedirect map[string]bool   `yaml:"no_https_redirect"` // New field
}

func loadConfig() (Config, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return Config{}, fmt.Errorf("failed to read config %s: %v", configPath, err)
	}

	var cfg Config
	err = yaml.Unmarshal(data, &cfg)
	if err != nil {
		return Config{}, fmt.Errorf("failed to unmarshal config: %v", err)
	}
	return cfg, nil
}

func generateDefaultConfig() Config {
	return Config{
		ListenHTTP:  ":80",
		ListenHTTPS: ":443",
		CertDir:     "certificates", // default current directory creates certificates folder
		CertFile:    "certificate.pem",
		KeyFile:     "key.pem",
		Routes: map[string]string{
			"*":                "http://127.0.0.1:80",
			"main.example.com": "http://127.0.0.1:80",
		},
		TrustTarget: map[string]bool{
			"*":                true,  // default trust all certificates
			"main.example.com": false, // use only trusted certificate
		},
		NoHTTPSRedirect: map[string]bool{
			"*":                false, // Default: redirect to HTTPS
			"main.example.com": true,  // set to not redirect HTTP to HTTPS
		},
	}
}

func saveConfig(cfg Config) error {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return fmt.Errorf("failed to create base directory %s: %v", baseDir, err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %v", err)
	}

	err = os.WriteFile(configPath, data, 0644)
	if err != nil {
		return fmt.Errorf("failed to write config to %s: %v", configPath, err)
	}
	return nil
}

func monitorConfig() {
	var lastModTime time.Time
	for {
		configInfo, err := os.Stat(configPath)
		if err != nil {
			log.Printf("Error checking config file: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		if configInfo.ModTime() != lastModTime {
			newConfig, err := loadConfig()
			if err != nil {
				log.Printf("Error reloading config: %v", err)
			} else {
				configMux.Lock()
				if newConfig.ListenHTTP != config.ListenHTTP {
					config.ListenHTTP = newConfig.ListenHTTP
					log.Printf("Updated listen_http to %s", config.ListenHTTP)
				}
				if newConfig.ListenHTTPS != config.ListenHTTPS {
					config.ListenHTTPS = newConfig.ListenHTTPS
					log.Printf("Updated listen_https to %s", config.ListenHTTPS)
				}
				if newConfig.CertDir != config.CertDir || newConfig.CertFile != config.CertFile || newConfig.KeyFile != config.KeyFile {
					config.CertDir = newConfig.CertDir
					config.CertFile = newConfig.CertFile
					config.KeyFile = newConfig.KeyFile
					updatePaths()
					if err := loadCertificate(); err != nil {
						log.Printf("Error reloading certificate after path change: %v", err)
					} else {
						log.Println("Updated certificate paths and reloaded certificate")
					}
				}
				for k, v := range newConfig.Routes {
					if oldV, exists := config.Routes[k]; !exists || oldV != v {
						config.Routes[k] = v
						log.Printf("Updated route %s to %s", k, v)
					}
				}
				for k := range config.Routes {
					if _, exists := newConfig.Routes[k]; !exists {
						delete(config.Routes, k)
						log.Printf("Removed route %s", k)
					}
				}
				for k, v := range newConfig.TrustTarget {
					if oldV, exists := config.TrustTarget[k]; !exists || oldV != v {
						config.TrustTarget[k] = v
						log.Printf("Updated trust_target %s to %v", k, v)
					}
				}
				for k := range config.TrustTarget {
					if _, exists := newConfig.TrustTarget[k]; !exists {
						delete(config.TrustTarget, k)
						log.Printf("Removed trust_target %s", k)
					}
				}
				for k, v := range newConfig.NoHTTPSRedirect {
					if oldV, exists := config.NoHTTPSRedirect[k]; !exists || oldV != v {
						config.NoHTTPSRedirect[k] = v
						log.Printf("Updated no_https_redirect %s to %v", k, v)
					}
				}
				for k := range config.NoHTTPSRedirect {
					if _, exists := newConfig.NoHTTPSRedirect[k]; !exists {
						delete(config.NoHTTPSRedirect, k)
						log.Printf("Removed no_https_redirect %s", k)
					}
				}
				configMux.Unlock()
				log.Println("Config reloaded successfully")
				lastModTime = configInfo.ModTime()
			}
		}
		time.Sleep(5 * time.Second)
	}
}
