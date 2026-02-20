// Package registries provides clients for fetching package metadata from registry APIs.
//
// The package supports multiple ecosystems (npm, PyPI, Cargo, RubyGems, Go modules)
// with a unified interface for fetching package information, versions, dependencies,
// and maintainers.
//
// Basic usage:
//
//	import (
//		"context"
//		"github.com/git-pkgs/registries"
//		_ "github.com/git-pkgs/registries/internal/cargo"
//	)
//
//	client := registries.DefaultClient()
//	reg, err := registries.New("cargo", "", client)
//	if err != nil {
//		log.Fatal(err)
//	}
//
//	pkg, err := reg.FetchPackage(context.Background(), "serde")
//	if err != nil {
//		log.Fatal(err)
//	}
//	fmt.Println(pkg.Name, pkg.Repository)
//
// To automatically import all supported ecosystems, use the imports subpackage:
//
//	import (
//		"github.com/git-pkgs/registries"
//		_ "github.com/git-pkgs/registries/all"
//	)
package registries

import (
	"context"

	"github.com/git-pkgs/purl"
	"github.com/git-pkgs/registries/client"
	"github.com/git-pkgs/registries/internal/core"
)

// Re-export types from internal/core
type (
	// Registry is the interface implemented by all ecosystem registry clients.
	Registry = core.Registry

	// Package represents metadata about a package from a registry.
	Package = core.Package

	// Version represents a specific version of a package.
	Version = core.Version

	// Dependency represents a package dependency.
	Dependency = core.Dependency

	// Maintainer represents a package maintainer.
	Maintainer = core.Maintainer

	// Scope indicates when a dependency is required.
	Scope = core.Scope

	// VersionStatus represents the status of a package version.
	VersionStatus = core.VersionStatus
)

// Re-export types from client
type (
	// Client is an HTTP client with retry logic for registry APIs.
	Client = client.Client

	// URLBuilder constructs URLs for a registry.
	URLBuilder = client.URLBuilder

	// RateLimiter controls request pacing.
	RateLimiter = client.RateLimiter
)

// Re-export constants
const (
	Runtime     = core.Runtime
	Development = core.Development
	Test        = core.Test
	Build       = core.Build
	Optional    = core.Optional

	StatusNone       = core.StatusNone
	StatusYanked     = core.StatusYanked
	StatusDeprecated = core.StatusDeprecated
	StatusRetracted  = core.StatusRetracted
)

// Re-export errors
var (
	ErrNotFound = client.ErrNotFound
)

// Error types
type (
	HTTPError      = client.HTTPError
	NotFoundError  = client.NotFoundError
	RateLimitError = client.RateLimitError
)

// New creates a new registry for the given ecosystem.
// If baseURL is empty, the default registry URL is used.
// If client is nil, DefaultClient() is used.
//
// Supported ecosystems: "cargo", "npm", "gem", "pypi", "golang"
func New(ecosystem string, baseURL string, c *Client) (Registry, error) {
	return core.New(ecosystem, baseURL, c)
}

// DefaultClient returns a client with sensible defaults:
// - 30s timeout
// - 5 retries with exponential backoff
// - Retry on 429 and 5xx responses
func DefaultClient() *Client {
	return client.DefaultClient()
}

// NewClient creates a new client with the given options.
func NewClient(opts ...Option) *Client {
	return client.NewClient(opts...)
}

// Option configures a Client.
type Option = client.Option

// WithTimeout sets the HTTP client timeout.
var WithTimeout = client.WithTimeout

// WithMaxRetries sets the maximum number of retries.
var WithMaxRetries = client.WithMaxRetries

// SupportedEcosystems returns all registered ecosystem types.
// Note: ecosystems must be imported to be registered.
func SupportedEcosystems() []string {
	return core.SupportedEcosystems()
}

// BuildURLs returns a map of all non-empty URLs for a package.
// Keys are "registry", "download", "docs", and "purl".
func BuildURLs(urls URLBuilder, name, version string) map[string]string {
	return client.BuildURLs(urls, name, version)
}

// DefaultURL returns the default registry URL for an ecosystem.
func DefaultURL(ecosystem string) string {
	return core.DefaultURL(ecosystem)
}

// PURL represents a parsed Package URL.
type PURL = purl.PURL

// ParsePURL parses a Package URL string into its components.
// Supports both package PURLs (pkg:cargo/serde) and version PURLs (pkg:cargo/serde@1.0.0).
func ParsePURL(purlStr string) (*PURL, error) {
	return purl.Parse(purlStr)
}

// NewFromPURL creates a registry client from a PURL and returns the parsed components.
// Returns the registry, full package name, and version (empty if not in PURL).
func NewFromPURL(purl string, c *Client) (Registry, string, string, error) {
	return core.NewFromPURL(purl, c)
}

// FetchPackageFromPURL fetches package metadata using a PURL.
func FetchPackageFromPURL(ctx context.Context, purl string, c *Client) (*Package, error) {
	return core.FetchPackageFromPURL(ctx, purl, c)
}

// FetchVersionFromPURL fetches a specific version's metadata using a PURL.
// Returns an error if the PURL doesn't include a version.
func FetchVersionFromPURL(ctx context.Context, purl string, c *Client) (*Version, error) {
	return core.FetchVersionFromPURL(ctx, purl, c)
}

// FetchDependenciesFromPURL fetches dependencies for a specific version using a PURL.
// Returns an error if the PURL doesn't include a version.
func FetchDependenciesFromPURL(ctx context.Context, purl string, c *Client) ([]Dependency, error) {
	return core.FetchDependenciesFromPURL(ctx, purl, c)
}

// FetchMaintainersFromPURL fetches maintainer information using a PURL.
func FetchMaintainersFromPURL(ctx context.Context, purl string, c *Client) ([]Maintainer, error) {
	return core.FetchMaintainersFromPURL(ctx, purl, c)
}

// FetchLatestVersion returns the latest non-yanked/retracted/deprecated version.
// Returns nil if no valid versions exist.
func FetchLatestVersion(ctx context.Context, reg Registry, name string) (*Version, error) {
	return core.FetchLatestVersion(ctx, reg, name)
}

// FetchLatestVersionFromPURL returns the latest non-yanked version for a PURL.
func FetchLatestVersionFromPURL(ctx context.Context, purl string, c *Client) (*Version, error) {
	return core.FetchLatestVersionFromPURL(ctx, purl, c)
}

// BulkFetchPackages fetches package metadata for multiple PURLs in parallel.
// Individual fetch errors are silently ignored - those PURLs are omitted from results.
// Returns a map of PURL to Package.
func BulkFetchPackages(ctx context.Context, purls []string, c *Client) map[string]*Package {
	return core.BulkFetchPackages(ctx, purls, c)
}

// BulkFetchPackagesWithConcurrency fetches packages with a custom concurrency limit.
func BulkFetchPackagesWithConcurrency(ctx context.Context, purls []string, c *Client, concurrency int) map[string]*Package {
	return core.BulkFetchPackagesWithConcurrency(ctx, purls, c, concurrency)
}

// BulkFetchVersions fetches version metadata for multiple versioned PURLs in parallel.
// PURLs without versions are silently skipped.
// Individual fetch errors are silently ignored - those PURLs are omitted from results.
// Returns a map of PURL to Version.
func BulkFetchVersions(ctx context.Context, purls []string, c *Client) map[string]*Version {
	return core.BulkFetchVersions(ctx, purls, c)
}

// BulkFetchVersionsWithConcurrency fetches versions with a custom concurrency limit.
func BulkFetchVersionsWithConcurrency(ctx context.Context, purls []string, c *Client, concurrency int) map[string]*Version {
	return core.BulkFetchVersionsWithConcurrency(ctx, purls, c, concurrency)
}

// BulkFetchLatestVersions fetches the latest version for multiple PURLs in parallel.
// Returns a map of PURL to the latest non-yanked Version.
func BulkFetchLatestVersions(ctx context.Context, purls []string, c *Client) map[string]*Version {
	return core.BulkFetchLatestVersions(ctx, purls, c)
}

// BulkFetchLatestVersionsWithConcurrency fetches latest versions with a custom concurrency limit.
func BulkFetchLatestVersionsWithConcurrency(ctx context.Context, purls []string, c *Client, concurrency int) map[string]*Version {
	return core.BulkFetchLatestVersionsWithConcurrency(ctx, purls, c, concurrency)
}
