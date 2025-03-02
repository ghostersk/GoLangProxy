# structure:
```
/golangproxy
├── main.go               # Application entry point
├── config/
│   └── config.go         # Configuration loading and parsing
├── proxy/
│   └── proxy.go          # Reverse proxy logic
├── server/
│   └── server.go         # Simple web server implementation
├── ssl/
│   └── ssl.go            # SSL certificate management
├── logger/
│   └── logger.go         # Logging setup
├── logs/                 # Logs directory (created at runtime)
├── ssl/                  # SSL certificates directory (created at runtime)
├── www/                  # Web server content directory (created at runtime)
└── tests/                # Test files
    ├── config_test.go    # Tests for config package
    ├── proxy_test.go     # Tests for proxy package
    ├── server_test.go    # Tests for server package
    └── ssl_test.go       # Tests for ssl package
```
