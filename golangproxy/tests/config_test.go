package tests

import (
	"os"
	"testing"

	"golangproxy/config"
)

func TestLoadConfig(t *testing.T) {
	// Test loading existing config
	// Test creating default config
	os.Remove("test_config.yaml")
	config, err := config.LoadConfig("test_config.yaml")
	if err != nil {
		t.Fatalf("Error loading config: %v", err)
	}
	if config.ListenHTTP != ":80" {
		t.Errorf("Expected ListenHTTP :80, got %s", config.ListenHTTP)
	}
}
