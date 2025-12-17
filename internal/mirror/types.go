package mirror

import "errors"

var (
	// ErrNotFound is returned when a provider is not found upstream
	ErrNotFound = errors.New("provider not found")
)

// IndexResponse represents the response to a provider index request
// Returned by GET /:hostname/:namespace/:type/index.json
type IndexResponse struct {
	Versions map[string]interface{} `json:"versions"`
}

// VersionResponse represents the response to a provider version request
// Returned by GET /:hostname/:namespace/:type/:version.json
type VersionResponse struct {
	Archives map[string]Archive `json:"archives"`
}

// Archive represents a downloadable provider package
type Archive struct {
	URL    string   `json:"url"`
	Hashes []string `json:"hashes,omitempty"`
}

// RegistryVersionsResponse is the full response from the registry /versions API
type RegistryVersionsResponse struct {
	Versions []RegistryVersion `json:"versions"`
}

// RegistryVersion represents a single version in the registry versions response
type RegistryVersion struct {
	Version   string             `json:"version"`
	Platforms []RegistryPlatform `json:"platforms"`
}

// RegistryPlatform represents a platform in the registry versions response
type RegistryPlatform struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

// DownloadInfo holds the download metadata from registry
type DownloadInfo struct {
	DownloadURL string `json:"download_url"`
	Shasum      string `json:"shasum"`
}

// ProviderAddress represents a provider's network address
type ProviderAddress struct {
	Hostname  string
	Namespace string
	Type      string
}

// ParseProviderAddress parses a provider registry address like "registry.terraform.io/hashicorp/aws"
func ParseProviderAddress(addr string) *ProviderAddress {
	// For now, just store it as-is since this is mainly for demonstration
	// Real parsing would split on "/" and handle defaults
	return &ProviderAddress{}
}
