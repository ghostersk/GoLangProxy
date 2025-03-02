package tests

import (
	"testing"

	"golangproxy/proxy"
)

func TestCreateRoute(t *testing.T) {
	// Test HTTP target
	route := proxy.CreateRoute("http://example.com", false)
	if route.Target != "http://example.com" {
		t.Errorf("Expected target http://example.com, got %s", route.Target)
	}
}
