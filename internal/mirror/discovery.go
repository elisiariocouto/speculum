package mirror

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// ServiceDiscovery represents the response from .well-known/terraform.json
type ServiceDiscovery struct {
	Hostname    string    `json:"-"`
	ProvidersV1 string    `json:"providers.v1"`
	CachedAt    time.Time `json:"-"`
}

// DiscoveryCache caches service discovery responses with TTL
type DiscoveryCache struct {
	mu     sync.RWMutex
	cache  map[string]*ServiceDiscovery
	ttl    time.Duration
	client *http.Client
	logger *slog.Logger
}

// NewDiscoveryCache creates a new discovery cache
func NewDiscoveryCache(ttl time.Duration, client *http.Client, logger *slog.Logger) *DiscoveryCache {
	return &DiscoveryCache{
		cache:  make(map[string]*ServiceDiscovery),
		ttl:    ttl,
		client: client,
		logger: logger,
	}
}

// DiscoverServices discovers the service endpoints for a Terraform registry
// It fetches https://{hostname}/.well-known/terraform.json and caches the result
func (dc *DiscoveryCache) DiscoverServices(ctx context.Context, hostname string) (*ServiceDiscovery, error) {
	// Check cache first
	dc.mu.RLock()
	if cached, ok := dc.cache[hostname]; ok {
		// Check if cache is still valid
		if time.Since(cached.CachedAt) < dc.ttl {
			dc.logger.DebugContext(ctx, "using cached service discovery",
				slog.String("hostname", hostname),
				slog.String("providers_v1", cached.ProvidersV1))
			dc.mu.RUnlock()
			return cached, nil
		}
	}
	dc.mu.RUnlock()

	// Cache miss or expired, fetch from upstream
	dc.logger.DebugContext(ctx, "discovering services from .well-known",
		slog.String("hostname", hostname))

	wellKnownURL := fmt.Sprintf("https://%s/.well-known/terraform.json", hostname)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wellKnownURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create discovery request: %w", err)
	}

	resp, err := dc.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch service discovery: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("service discovery returned status %d", resp.StatusCode)
	}

	var discovery ServiceDiscovery
	if err := json.NewDecoder(resp.Body).Decode(&discovery); err != nil {
		return nil, fmt.Errorf("failed to parse service discovery response: %w", err)
	}

	// Set metadata
	discovery.Hostname = hostname
	discovery.CachedAt = time.Now()

	dc.logger.DebugContext(ctx, "discovered service endpoints",
		slog.String("hostname", hostname),
		slog.String("providers_v1", discovery.ProvidersV1))

	// Cache the result
	dc.mu.Lock()
	dc.cache[hostname] = &discovery
	dc.mu.Unlock()

	return &discovery, nil
}

// Clear removes all cached discovery information
func (dc *DiscoveryCache) Clear() {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.cache = make(map[string]*ServiceDiscovery)
}

// ClearHost removes cached discovery information for a specific hostname
func (dc *DiscoveryCache) ClearHost(hostname string) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	delete(dc.cache, hostname)
}
