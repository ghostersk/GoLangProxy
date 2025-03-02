package config

import (
	"os"

	"gopkg.in/yaml.v2"
)

// Config represents the application configuration
type Config struct {
	ListenHTTP      string            `yaml:"listen_http"`       // HTTP listen address (e.g., ":80")
	ListenHTTPS     string            `yaml:"listen_https"`      // HTTPS listen address (e.g., ":443")
	CertFile        string            `yaml:"cert_file"`         // Path to SSL certificate
	KeyFile         string            `yaml:"key_file"`          // Path to SSL key
	Routes          map[string]string `yaml:"routes"`            // Host to target URL mappings
	TrustTarget     map[string]bool   `yaml:"trust_target"`      // Whether to trust invalid target certs
	NoHTTPSRedirect map[string]bool   `yaml:"no_https_redirect"` // Disable HTTP to HTTPS redirect
}

// LoadConfig loads the config from file or creates a default one
func LoadConfig(configPath string) (*Config, error) {
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// Create default configuration
		defaultConfig := &Config{
			ListenHTTP:  ":80",
			ListenHTTPS: ":443",
			CertFile:    "./crt/certificate.pem",
			KeyFile:     "./crt/key.pem",
			Routes: map[string]string{
				"*":                "http://127.0.0.1:61147",      // accespt all route
				"main.example.com": "https://10.100.111.254:4444", // Specific route
				"gg.example.com":   "https://example.com:443",
			},
			TrustTarget: map[string]bool{
				"*":                true, // true = trust any certificates on target url
				"main.example.com": true,
				"gg.example.com":   false, // trusting target cetificate disabled
			},
			NoHTTPSRedirect: map[string]bool{
				"*":                false, // false = HTTP redirected to HTTPS automatically
				"main.example.com": false,
				"gg.example.com":   true, // no automatic redirect to HTTPS from HTTP
			},
		}
		data, err := yaml.Marshal(defaultConfig)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(configPath, data, 0644); err != nil {
			return nil, err
		}
		return defaultConfig, nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, err
	}
	return &config, nil
}
