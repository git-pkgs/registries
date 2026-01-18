package core

import (
	"context"
	"fmt"

	packageurl "github.com/package-url/packageurl-go"
)

// PURL wraps packageurl.PackageURL with registry-specific helpers.
type PURL struct {
	packageurl.PackageURL
}

// FullName returns the package name in the format expected by the registry.
// For npm: "@babel/core", for maven: "org.apache.commons:commons-lang3"
func (p PURL) FullName() string {
	if p.Namespace == "" {
		return p.Name
	}

	switch p.Type {
	case "npm":
		// packageurl-go keeps @ in namespace, so "@babel" + "/" + "core" = "@babel/core"
		return p.Namespace + "/" + p.Name
	case "maven":
		return p.Namespace + ":" + p.Name
	case "terraform":
		// terraform modules are namespace/name/provider, all parts needed
		return p.Namespace + "/" + p.Name
	default:
		return p.Namespace + "/" + p.Name
	}
}

// ParsePURL parses a Package URL string into its components.
// Supports both package PURLs (pkg:cargo/serde) and version PURLs (pkg:cargo/serde@1.0.0).
func ParsePURL(purl string) (*PURL, error) {
	p, err := packageurl.FromString(purl)
	if err != nil {
		return nil, err
	}
	return &PURL{p}, nil
}

// NewFromPURL creates a registry client from a PURL and returns the parsed components.
// Returns the registry, full package name, and version (empty if not in PURL).
// If the PURL has a repository_url qualifier, it's used as the base URL for private registries.
func NewFromPURL(purl string, client *Client) (Registry, string, string, error) {
	p, err := ParsePURL(purl)
	if err != nil {
		return nil, "", "", err
	}

	// Extract repository_url qualifier for private registry support
	baseURL := p.Qualifiers.Map()["repository_url"]

	reg, err := New(p.Type, baseURL, client)
	if err != nil {
		return nil, "", "", err
	}

	return reg, p.FullName(), p.Version, nil
}

// FetchPackageFromPURL fetches package metadata using a PURL.
func FetchPackageFromPURL(ctx context.Context, purl string, client *Client) (*Package, error) {
	reg, name, _, err := NewFromPURL(purl, client)
	if err != nil {
		return nil, err
	}

	return reg.FetchPackage(ctx, name)
}

// FetchVersionFromPURL fetches a specific version's metadata using a PURL.
// Returns an error if the PURL doesn't include a version.
func FetchVersionFromPURL(ctx context.Context, purl string, client *Client) (*Version, error) {
	p, err := ParsePURL(purl)
	if err != nil {
		return nil, err
	}

	if p.Version == "" {
		return nil, fmt.Errorf("PURL has no version: %s", purl)
	}

	baseURL := p.Qualifiers.Map()["repository_url"]
	reg, err := New(p.Type, baseURL, client)
	if err != nil {
		return nil, err
	}

	versions, err := reg.FetchVersions(ctx, p.FullName())
	if err != nil {
		return nil, err
	}

	for _, v := range versions {
		if v.Number == p.Version {
			return &v, nil
		}
	}

	return nil, &NotFoundError{
		Ecosystem: p.Type,
		Name:      p.FullName(),
		Version:   p.Version,
	}
}

// FetchDependenciesFromPURL fetches dependencies for a specific version using a PURL.
// Returns an error if the PURL doesn't include a version.
func FetchDependenciesFromPURL(ctx context.Context, purl string, client *Client) ([]Dependency, error) {
	p, err := ParsePURL(purl)
	if err != nil {
		return nil, err
	}

	if p.Version == "" {
		return nil, fmt.Errorf("PURL has no version: %s", purl)
	}

	baseURL := p.Qualifiers.Map()["repository_url"]
	reg, err := New(p.Type, baseURL, client)
	if err != nil {
		return nil, err
	}

	return reg.FetchDependencies(ctx, p.FullName(), p.Version)
}

// FetchMaintainersFromPURL fetches maintainer information using a PURL.
func FetchMaintainersFromPURL(ctx context.Context, purl string, client *Client) ([]Maintainer, error) {
	reg, name, _, err := NewFromPURL(purl, client)
	if err != nil {
		return nil, err
	}

	return reg.FetchMaintainers(ctx, name)
}
