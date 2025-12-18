package mirror

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestDiscoveryCache_CacheHit(t *testing.T) {
	callCount := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"providers.v1": "https://registry.terraform.io/v1/providers/",
		})
	}))
	defer server.Close()

	// Create an HTTP client that trusts the test server
	client := server.Client()
	cache := NewDiscoveryCache(1*time.Second, client, newTestLogger())

	// Extract hostname from server URL (removing scheme)
	u, _ := url.Parse(server.URL)
	hostname := u.Host

	// First request should hit upstream
	discovery1, err := cache.DiscoverServices(context.Background(), hostname)
	if err != nil {
		t.Fatalf("first request failed: %v", err)
	}

	// Second request should hit cache
	discovery2, err := cache.DiscoverServices(context.Background(), hostname)
	if err != nil {
		t.Fatalf("second request failed: %v", err)
	}

	if discovery1.ProvidersV1 != discovery2.ProvidersV1 {
		t.Errorf("cached value mismatch: %v != %v", discovery1.ProvidersV1, discovery2.ProvidersV1)
	}

	if callCount != 1 {
		t.Errorf("expected 1 upstream call due to cache hit, got %d", callCount)
	}
}

func TestDiscoveryCache_CacheExpiration(t *testing.T) {
	callCount := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"providers.v1": fmt.Sprintf("https://registry.terraform.io/v1/providers/?call=%d", callCount),
		})
	}))
	defer server.Close()

	client := server.Client()
	cache := NewDiscoveryCache(50*time.Millisecond, client, newTestLogger())

	u, _ := url.Parse(server.URL)
	hostname := u.Host

	// First request
	discovery1, err := cache.DiscoverServices(context.Background(), hostname)
	if err != nil {
		t.Fatalf("first request failed: %v", err)
	}

	// Wait for cache to expire
	time.Sleep(100 * time.Millisecond)

	// Second request should fetch again
	discovery2, err := cache.DiscoverServices(context.Background(), hostname)
	if err != nil {
		t.Fatalf("second request failed: %v", err)
	}

	if discovery1.ProvidersV1 == discovery2.ProvidersV1 {
		t.Errorf("cache should have expired and fetched new data")
	}

	if callCount != 2 {
		t.Errorf("expected 2 upstream calls, got %d", callCount)
	}
}

func TestDiscoveryCache_RequestCoalescing(t *testing.T) {
	callCount := 0
	var mu sync.Mutex
	blocker := make(chan struct{})

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		callCount++
		mu.Unlock()

		// Block first request to allow others to queue up
		<-blocker

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"providers.v1": "https://registry.terraform.io/v1/providers/",
		})
	}))
	defer server.Close()

	client := server.Client()
	cache := NewDiscoveryCache(1*time.Second, client, newTestLogger())

	u, _ := url.Parse(server.URL)
	hostname := u.Host

	numConcurrent := 5
	var wg sync.WaitGroup
	wg.Add(numConcurrent)

	// Launch concurrent requests
	for i := 0; i < numConcurrent; i++ {
		go func() {
			defer wg.Done()
			_, err := cache.DiscoverServices(context.Background(), hostname)
			if err != nil {
				t.Errorf("request failed: %v", err)
			}
		}()
	}

	// Give requests time to queue
	time.Sleep(100 * time.Millisecond)

	// Unblock the server
	close(blocker)

	wg.Wait()

	mu.Lock()
	count := callCount
	mu.Unlock()

	if count != 1 {
		t.Errorf("expected 1 upstream call with coalescing, got %d", count)
	}
}

func TestDiscoveryCache_InvalidProvidersURL(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Return an invalid URL
		json.NewEncoder(w).Encode(map[string]string{
			"providers.v1": "not a valid url at all !!!",
		})
	}))
	defer server.Close()

	client := server.Client()
	cache := NewDiscoveryCache(1*time.Second, client, newTestLogger())

	u, _ := url.Parse(server.URL)
	hostname := u.Host

	_, err := cache.DiscoverServices(context.Background(), hostname)
	if err == nil {
		t.Errorf("expected error for invalid URL, got nil")
	}
	if !strings.Contains(err.Error(), "invalid providers.v1 URL") {
		t.Errorf("expected invalid URL error, got: %v", err)
	}
}

func TestDiscoveryCache_EmptyProvidersURL(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Return empty URL
		json.NewEncoder(w).Encode(map[string]string{
			"providers.v1": "",
		})
	}))
	defer server.Close()

	client := server.Client()
	cache := NewDiscoveryCache(1*time.Second, client, newTestLogger())

	u, _ := url.Parse(server.URL)
	hostname := u.Host

	_, err := cache.DiscoverServices(context.Background(), hostname)
	if err == nil {
		t.Errorf("expected error for empty URL, got nil")
	}
	if !strings.Contains(err.Error(), "invalid providers.v1 URL") {
		t.Errorf("expected invalid URL error, got: %v", err)
	}
}

func TestDiscoveryCache_HTTPError(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := server.Client()
	cache := NewDiscoveryCache(1*time.Second, client, newTestLogger())

	u, _ := url.Parse(server.URL)
	hostname := u.Host

	_, err := cache.DiscoverServices(context.Background(), hostname)
	if err == nil {
		t.Errorf("expected HTTP error, got nil")
	}
	if !strings.Contains(err.Error(), "status 404") {
		t.Errorf("expected status error, got: %v", err)
	}
}

func TestDiscoveryCache_InvalidJSON(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("invalid json {"))
	}))
	defer server.Close()

	client := server.Client()
	cache := NewDiscoveryCache(1*time.Second, client, newTestLogger())

	u, _ := url.Parse(server.URL)
	hostname := u.Host

	_, err := cache.DiscoverServices(context.Background(), hostname)
	if err == nil {
		t.Errorf("expected JSON error, got nil")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Errorf("expected parse error, got: %v", err)
	}
}

func TestDiscoveryCache_Clear(t *testing.T) {
	callCount := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"providers.v1": "https://registry.terraform.io/v1/providers/",
		})
	}))
	defer server.Close()

	client := server.Client()
	cache := NewDiscoveryCache(10*time.Second, client, newTestLogger())

	u, _ := url.Parse(server.URL)
	hostname := u.Host

	// Populate cache
	_, err := cache.DiscoverServices(context.Background(), hostname)
	if err != nil {
		t.Fatalf("first request failed: %v", err)
	}

	if callCount != 1 {
		t.Errorf("expected 1 call, got %d", callCount)
	}

	// Clear cache
	cache.Clear()

	// Request again should fetch
	_, err = cache.DiscoverServices(context.Background(), hostname)
	if err != nil {
		t.Fatalf("second request failed: %v", err)
	}

	if callCount != 2 {
		t.Errorf("expected 2 calls after clear, got %d", callCount)
	}
}

func TestDiscoveryCache_ClearHost(t *testing.T) {
	callCount := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"providers.v1": "https://registry.terraform.io/v1/providers/",
		})
	}))
	defer server.Close()

	client := server.Client()
	cache := NewDiscoveryCache(10*time.Second, client, newTestLogger())

	u, _ := url.Parse(server.URL)
	hostname := u.Host
	hostname1 := hostname + "-1"
	hostname2 := hostname + "-2"

	// Create two different discoveries
	discovery := &ServiceDiscovery{
		Hostname:    hostname1,
		ProvidersV1: "https://registry.terraform.io/v1/providers/",
		CachedAt:    time.Now(),
	}

	cache.mu.Lock()
	cache.cache[hostname1] = discovery
	cache.cache[hostname2] = discovery
	cache.mu.Unlock()

	// Clear only hostname1
	cache.ClearHost(hostname1)

	// Verify hostname1 is cleared and hostname2 is still there
	cache.mu.RLock()
	_, exists1 := cache.cache[hostname1]
	_, exists2 := cache.cache[hostname2]
	cache.mu.RUnlock()

	if exists1 {
		t.Errorf("hostname1 should have been cleared")
	}
	if !exists2 {
		t.Errorf("hostname2 should still be in cache")
	}
}

func TestIsValidProvidersURL(t *testing.T) {
	tests := []struct {
		name  string
		url   string
		valid bool
	}{
		{
			name:  "valid http url",
			url:   "http://registry.terraform.io/v1/providers/",
			valid: true,
		},
		{
			name:  "valid https url",
			url:   "https://registry.terraform.io/v1/providers/",
			valid: true,
		},
		{
			name:  "valid relative path",
			url:   "/v1/providers/",
			valid: true,
		},
		{
			name:  "empty string",
			url:   "",
			valid: false,
		},
		{
			name:  "invalid characters",
			url:   "not a valid url at all !!!",
			valid: false,
		},
		{
			name:  "whitespace",
			url:   "https://registry.terraform.io/v1/providers/ with spaces",
			valid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidProvidersURL(tt.url)
			if result != tt.valid {
				t.Errorf("isValidProvidersURL(%q) = %v, want %v", tt.url, result, tt.valid)
			}
		})
	}
}

func TestDiscoveryCache_ContextCancellation(t *testing.T) {
	blocker := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blocker
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"providers.v1": "https://registry.terraform.io/v1/providers/",
		})
	}))
	defer server.Close()

	client := server.Client()
	cache := NewDiscoveryCache(1*time.Second, client, newTestLogger())

	u, _ := url.Parse(server.URL)
	hostname := u.Host

	ctx, cancel := context.WithCancel(context.Background())

	// Start request in goroutine
	done := make(chan error)
	go func() {
		_, err := cache.DiscoverServices(ctx, hostname)
		done <- err
	}()

	// Give request time to start
	time.Sleep(100 * time.Millisecond)

	// Cancel context
	cancel()

	// Unblock server
	close(blocker)

	// Check that context cancellation is handled
	err := <-done
	if err == nil {
		t.Errorf("expected context cancellation error")
	}
}
