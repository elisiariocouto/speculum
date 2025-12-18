package mirror

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
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
	mu       sync.RWMutex
	cache    map[string]*ServiceDiscovery
	inFlight map[string]bool // Track hostnames currently being fetched
	cond     *sync.Cond      // Signal when a fetch completes
	ttl      time.Duration
	client   *http.Client
	logger   *slog.Logger
}

// NewDiscoveryCache creates a new discovery cache
func NewDiscoveryCache(ttl time.Duration, client *http.Client, logger *slog.Logger) *DiscoveryCache {
	dc := &DiscoveryCache{
		cache:    make(map[string]*ServiceDiscovery),
		inFlight: make(map[string]bool),
		ttl:      ttl,
		client:   client,
		logger:   logger,
	}
	dc.cond = sync.NewCond(&dc.mu)
	return dc
}

// isValidProvidersURL validates that the ProvidersV1 URL is well-formed
func isValidProvidersURL(urlStr string) bool {
	if urlStr == "" {
		return false
	}
	// Check for control characters or unencoded spaces
	for _, ch := range urlStr {
		if ch < 32 || ch == 127 {
			return false
		}
		if ch == ' ' {
			return false
		}
	}
	u, err := url.Parse(urlStr)
	if err != nil {
		return false
	}
	// URL must be either a valid absolute URL with a scheme or a valid path
	if u.Scheme != "" {
		// Absolute URL: must have a host
		if u.Host == "" {
			return false
		}
		// Only allow http or https schemes
		if u.Scheme != "http" && u.Scheme != "https" {
			return false
		}
	}
	return true
}

// DiscoverServices discovers the service endpoints for a Terraform registry
// It fetches https://{hostname}/.well-known/terraform.json and caches the result.
// Multiple concurrent requests for the same hostname will coalesce to a single upstream fetch.
func (dc *DiscoveryCache) DiscoverServices(ctx context.Context, hostname string) (*ServiceDiscovery, error) {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	// Check cache first
	if cached, ok := dc.cache[hostname]; ok {
		// Check if cache is still valid
		if time.Since(cached.CachedAt) < dc.ttl {
			dc.logger.DebugContext(ctx, "using cached service discovery",
				slog.String("hostname", hostname),
				slog.String("providers_v1", cached.ProvidersV1))
			return cached, nil
		}
	}

	// Wait for any in-flight request for this hostname to complete
	for dc.inFlight[hostname] {
		dc.cond.Wait()
		// After waiting, check cache again in case the in-flight request succeeded
		if cached, ok := dc.cache[hostname]; ok {
			if time.Since(cached.CachedAt) < dc.ttl {
				dc.logger.DebugContext(ctx, "using service discovery from coalesced request",
					slog.String("hostname", hostname),
					slog.String("providers_v1", cached.ProvidersV1))
				return cached, nil
			}
		}
	}

	// Mark this hostname as in-flight
	dc.inFlight[hostname] = true
	dc.mu.Unlock()

	// Fetch from upstream (outside the lock)
	discovery, err := dc.fetchFromUpstream(ctx, hostname)

	// Update cache and signal waiters
	dc.mu.Lock()
	delete(dc.inFlight, hostname)

	if err != nil {
		dc.cond.Broadcast()
		return nil, err
	}

	// Validate ProvidersV1 URL before caching
	if !isValidProvidersURL(discovery.ProvidersV1) {
		dc.cond.Broadcast()
		return nil, fmt.Errorf("invalid providers.v1 URL in service discovery: %q", discovery.ProvidersV1)
	}

	dc.cache[hostname] = discovery
	dc.cond.Broadcast()
	return discovery, nil
}

// fetchFromUpstream fetches service discovery from the .well-known endpoint
func (dc *DiscoveryCache) fetchFromUpstream(ctx context.Context, hostname string) (*ServiceDiscovery, error) {
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
