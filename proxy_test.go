package main

import (
	"bufio"
	"bytes"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// Mock globals for testing
var (
	mockConfig = Config{
		Routes: map[string]string{
			"test.local":  "http://mock-target",
			"ws.local":    "ws://mock-target",
			"cache.local": "http://mock-target",
		},
		TrustTarget: map[string]bool{
			"test.local":  true,
			"ws.local":    true,
			"cache.local": true,
		},
		NoHTTPSRedirect: map[string]bool{
			"test.local":  true,
			"ws.local":    true,
			"cache.local": true,
		},
	}
	mockConfigMux = sync.RWMutex{}
	mockLogger    = log.New(io.Discard, "", 0) // Discard logs during testing
)

// TestHandlerRoute tests basic routing to a target
func TestHandlerRoute(t *testing.T) {
	// Mock target server
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Hello from target"))
	}))
	defer targetServer.Close()

	// Setup mock globals
	configMux.Lock()
	config = mockConfig
	config.Routes["test.local"] = targetServer.URL
	trafficLogger = mockLogger
	configMux.Unlock()

	// Create request
	req, _ := http.NewRequest("GET", "http://test.local", nil)
	rr := httptest.NewRecorder()

	// Run handler
	handler(rr, req)

	// Check response
	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}
	if body := rr.Body.String(); body != "Hello from target" {
		t.Errorf("handler returned unexpected body: got %v want %v", body, "Hello from target")
	}
}

// TestHandlerWebSocket tests WebSocket upgrade handling
func TestHandlerWebSocket(t *testing.T) {
	// Mock WebSocket target (simplified)
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") == "websocket" {
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("Server doesn’t support hijacking")
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				t.Fatal(err)
			}
			conn.Write([]byte("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n"))
			conn.Close()
		}
	}))
	defer targetServer.Close()

	// Setup mock globals
	configMux.Lock()
	config = mockConfig
	config.Routes["ws.local"] = targetServer.URL
	trafficLogger = mockLogger
	configMux.Unlock()

	// Create WebSocket request
	req, _ := http.NewRequest("GET", "http://ws.local", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")

	// Use a custom recorder for hijacking
	rr := &hijackRecorder{ResponseRecorder: httptest.NewRecorder()}
	handler(rr, req)

	// Check if hijacking occurred
	if !rr.hijacked {
		t.Errorf("Expected WebSocket hijacking, but it didn’t occur")
	}
}

// TestHandlerCache tests caching functionality
func TestHandlerCache(t *testing.T) {
	// Mock target server
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("Cached content"))
	}))
	defer targetServer.Close()

	// Setup mock globals
	configMux.Lock()
	config = mockConfig
	config.Routes["cache.local"] = targetServer.URL
	trafficLogger = mockLogger
	cache = make(map[string]cachedResponse) // Reset cache for test isolation
	configMux.Unlock()

	// First request to cache
	req, _ := http.NewRequest("GET", "http://cache.local", nil)
	rr1 := httptest.NewRecorder()
	handler(rr1, req)

	// Second request to hit cache
	rr2 := httptest.NewRecorder()
	handler(rr2, req)

	// Check if second response is from cache
	if rr2.Code != http.StatusOK {
		t.Errorf("Expected status OK, got %v", rr2.Code)
	}
	if body := rr2.Body.String(); body != "Cached content" {
		t.Errorf("Expected cached response, got %v", body)
	}
}

// hijackRecorder mocks ResponseRecorder with Hijack support for WebSocket testing
type hijackRecorder struct {
	*httptest.ResponseRecorder
	hijacked bool
}

func (hr *hijackRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hr.hijacked = true
	return &mockConn{Reader: bytes.NewReader([]byte("mock response")), Writer: hr.Body}, nil, nil
}

// mockConn is a simple net.Conn mock for testing
type mockConn struct {
	io.Reader
	io.Writer
}

func (mc *mockConn) Close() error                       { return nil }
func (mc *mockConn) LocalAddr() net.Addr                { return nil }
func (mc *mockConn) RemoteAddr() net.Addr               { return nil }
func (mc *mockConn) SetDeadline(t time.Time) error      { return nil }
func (mc *mockConn) SetReadDeadline(t time.Time) error  { return nil }
func (mc *mockConn) SetWriteDeadline(t time.Time) error { return nil }
