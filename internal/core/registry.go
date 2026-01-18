package core

import (
	"context"
	"fmt"
	"sync"
)

// Registry is the interface implemented by all ecosystem registry clients.
type Registry interface {
	// Ecosystem returns the PURL type for this registry (e.g., "cargo", "npm", "gem").
	Ecosystem() string

	// FetchPackage retrieves package metadata.
	FetchPackage(ctx context.Context, name string) (*Package, error)

	// FetchVersions retrieves all versions of a package.
	FetchVersions(ctx context.Context, name string) ([]Version, error)

	// FetchDependencies retrieves dependencies for a specific version.
	FetchDependencies(ctx context.Context, name, version string) ([]Dependency, error)

	// FetchMaintainers retrieves maintainer information.
	FetchMaintainers(ctx context.Context, name string) ([]Maintainer, error)

	// URLs returns the URL builder for this registry.
	URLs() URLBuilder
}

// Factory creates a registry instance for a given base URL.
type Factory func(baseURL string, client *Client) Registry

var (
	factories = make(map[string]Factory)
	defaults  = make(map[string]string)
	mu        sync.RWMutex
)

// Register adds a registry factory to the global registry.
// ecosystem is the PURL type (e.g., "cargo", "npm", "gem", "pypi", "golang").
// defaultURL is the default registry URL for the ecosystem.
func Register(ecosystem string, defaultURL string, factory Factory) {
	mu.Lock()
	defer mu.Unlock()
	factories[ecosystem] = factory
	defaults[ecosystem] = defaultURL
}

// New creates a new registry for the given ecosystem.
// If baseURL is empty, the default registry URL is used.
func New(ecosystem string, baseURL string, client *Client) (Registry, error) {
	mu.RLock()
	factory, ok := factories[ecosystem]
	defaultURL := defaults[ecosystem]
	mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("unknown ecosystem: %s", ecosystem)
	}

	if baseURL == "" {
		baseURL = defaultURL
	}

	if client == nil {
		client = DefaultClient()
	}

	return factory(baseURL, client), nil
}

// SupportedEcosystems returns all registered ecosystem types.
func SupportedEcosystems() []string {
	mu.RLock()
	defer mu.RUnlock()

	ecosystems := make([]string, 0, len(factories))
	for eco := range factories {
		ecosystems = append(ecosystems, eco)
	}
	return ecosystems
}

// DefaultURL returns the default registry URL for an ecosystem.
func DefaultURL(ecosystem string) string {
	mu.RLock()
	defer mu.RUnlock()
	return defaults[ecosystem]
}
