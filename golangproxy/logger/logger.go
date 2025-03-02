package logger

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// Logger is the global logger instance
var Logger *log.Logger

// InitLogger initializes logging to file and stdout
func InitLogger() {
	if err := os.MkdirAll("logs", 0755); err != nil {
		log.Fatalf("Error creating logs directory: %v", err)
	}
	logFile, err := os.OpenFile(filepath.Join("logs", "proxy.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Fatalf("Error opening log file: %v", err)
	}
	multiWriter := io.MultiWriter(os.Stdout, logFile)
	Logger = log.New(multiWriter, "", log.LstdFlags)
	// Wrap the logger to filter context canceled errors
	oldOutput := Logger.Writer()
	Logger.SetOutput(&filteredWriter{Writer: oldOutput})
}

// filteredWriter wraps an io.Writer to filter out context canceled errors
type filteredWriter struct {
	Writer io.Writer
}

func (fw *filteredWriter) Write(p []byte) (n int, err error) {
	if strings.Contains(string(p), "context canceled") && strings.Contains(string(p), "http: proxy error") {
		return len(p), nil // Silently discard the message
	}
	return fw.Writer.Write(p)
}
