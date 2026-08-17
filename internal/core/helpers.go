package core

import (
	"context"
	"fmt"

	"github.com/git-pkgs/purl"
	"github.com/git-pkgs/vers"
)

const defaultConcurrency = 15

// NewFromPURL creates a registry client from a PURL and returns the parsed components.
// Returns the registry, full package name, and version (empty if not in PURL).
// If the PURL has a repository_url qualifier, it's used as the base URL for private registries.
func NewFromPURL(purlStr string, client *Client) (Registry, string, string, error) { //nolint:ireturn
	p, err := purl.Parse(purlStr)
	if err != nil {
		return nil, "", "", err
	}

	// Extract repository_url qualifier for private registry support
	baseURL := p.RepositoryURL()

	reg, err := New(p.Type, baseURL, client)
	if err != nil {
		return nil, "", "", err
	}

	return reg, p.FullName(), p.Version, nil
}

// FetchPackageFromPURL fetches package metadata using a PURL.
func FetchPackageFromPURL(ctx context.Context, purlStr string, client *Client) (*Package, error) {
	reg, name, _, err := NewFromPURL(purlStr, client)
	if err != nil {
		return nil, err
	}

	return reg.FetchPackage(ctx, name)
}

// FetchVersionFromPURL fetches a specific version's metadata using a PURL.
// Returns an error if the PURL doesn't include a version.
func FetchVersionFromPURL(ctx context.Context, purlStr string, client *Client) (*Version, error) {
	p, err := purl.Parse(purlStr)
	if err != nil {
		return nil, err
	}

	if p.Version == "" {
		return nil, fmt.Errorf("PURL has no version: %s", purlStr)
	}

	baseURL := p.RepositoryURL()
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
func FetchDependenciesFromPURL(ctx context.Context, purlStr string, client *Client) ([]Dependency, error) {
	p, err := purl.Parse(purlStr)
	if err != nil {
		return nil, err
	}

	if p.Version == "" {
		return nil, fmt.Errorf("PURL has no version: %s", purlStr)
	}

	baseURL := p.RepositoryURL()
	reg, err := New(p.Type, baseURL, client)
	if err != nil {
		return nil, err
	}

	return reg.FetchDependencies(ctx, p.FullName(), p.Version)
}

// FetchMaintainersFromPURL fetches maintainer information using a PURL.
func FetchMaintainersFromPURL(ctx context.Context, purlStr string, client *Client) ([]Maintainer, error) {
	reg, name, _, err := NewFromPURL(purlStr, client)
	if err != nil {
		return nil, err
	}

	return reg.FetchMaintainers(ctx, name)
}

// FetchLatestVersion returns the registry-advertised latest version when one
// is available. Otherwise it selects the latest active version by publication
// time, falling back to ecosystem version ordering when timestamps are absent.
// It returns nil when the registry reports no active versions.
func FetchLatestVersion(ctx context.Context, reg Registry, name string) (*Version, error) {
	var advertised string
	pkg, err := reg.FetchPackage(ctx, name)
	if err == nil && pkg != nil {
		advertised = pkg.LatestVersion
	}

	versions, err := reg.FetchVersions(ctx, name)
	if err != nil {
		if advertised != "" {
			return &Version{Number: advertised}, nil
		}
		return nil, err
	}
	return SelectLatestVersion(versions, reg.Ecosystem(), advertised), nil
}

// SelectLatestVersion applies the shared latest-release policy to versions.
// An advertised version takes precedence, including when it is a prerelease
// or has a non-empty status.
// Without one, versions with a non-empty status are excluded and the newest
// publication time wins. Ecosystem version ordering breaks timestamp ties and
// is used when every active version lacks a timestamp. Prereleases remain
// eligible in that fallback. The input slice is not modified.
func SelectLatestVersion(versions []Version, ecosystem, advertised string) *Version {
	if advertised != "" {
		for i := range versions {
			if vers.CompareWithScheme(versions[i].Number, advertised, ecosystem) == 0 {
				selected := versions[i]
				return &selected
			}
		}
		return &Version{Number: advertised}
	}

	var latest *Version
	hasTimestamp := false
	for i := range versions {
		candidate := &versions[i]
		if candidate.Number == "" || candidate.Status != StatusNone {
			continue
		}
		if !candidate.PublishedAt.IsZero() {
			if !hasTimestamp || latest == nil || candidate.PublishedAt.After(latest.PublishedAt) ||
				(candidate.PublishedAt.Equal(latest.PublishedAt) &&
					vers.CompareWithScheme(candidate.Number, latest.Number, ecosystem) > 0) {
				selected := *candidate
				latest = &selected
			}
			hasTimestamp = true
			continue
		}
		if hasTimestamp {
			continue
		}
		if latest == nil || vers.CompareWithScheme(candidate.Number, latest.Number, ecosystem) > 0 {
			selected := *candidate
			latest = &selected
		}
	}

	return latest
}

// FetchLatestVersionFromPURL returns the latest version for a PURL using the
// shared latest-release policy.
func FetchLatestVersionFromPURL(ctx context.Context, purl string, client *Client) (*Version, error) {
	reg, name, _, err := NewFromPURL(purl, client)
	if err != nil {
		return nil, err
	}
	return FetchLatestVersion(ctx, reg, name)
}

// BulkFetchPackages fetches package metadata for multiple PURLs in parallel.
// Individual fetch errors are silently ignored - those PURLs are omitted from results.
// Returns a map of PURL to Package.
func BulkFetchPackages(ctx context.Context, purls []string, client *Client) map[string]*Package {
	return BulkFetchPackagesWithConcurrency(ctx, purls, client, defaultConcurrency)
}

// BulkFetchPackagesWithConcurrency fetches packages with a custom concurrency limit.
func BulkFetchPackagesWithConcurrency(ctx context.Context, purls []string, client *Client, concurrency int) map[string]*Package {
	return ParallelMap(ctx, purls, concurrency, func(ctx context.Context, p string) (*Package, error) {
		return FetchPackageFromPURL(ctx, p, client)
	})
}

// BulkFetchVersions fetches version metadata for multiple versioned PURLs in parallel.
// PURLs without versions are silently skipped.
// Individual fetch errors are silently ignored - those PURLs are omitted from results.
// Returns a map of PURL to Version.
func BulkFetchVersions(ctx context.Context, purls []string, client *Client) map[string]*Version {
	return BulkFetchVersionsWithConcurrency(ctx, purls, client, defaultConcurrency)
}

// BulkFetchVersionsWithConcurrency fetches versions with a custom concurrency limit.
func BulkFetchVersionsWithConcurrency(ctx context.Context, purls []string, client *Client, concurrency int) map[string]*Version {
	return ParallelMap(ctx, purls, concurrency, func(ctx context.Context, p string) (*Version, error) {
		return FetchVersionFromPURL(ctx, p, client)
	})
}

// BulkFetchLatestVersions fetches the latest version for multiple PURLs in
// parallel using the shared latest-release policy.
func BulkFetchLatestVersions(ctx context.Context, purls []string, client *Client) map[string]*Version {
	return BulkFetchLatestVersionsWithConcurrency(ctx, purls, client, defaultConcurrency)
}

// BulkFetchLatestVersionsWithConcurrency fetches latest versions using the
// shared policy and a custom concurrency limit.
func BulkFetchLatestVersionsWithConcurrency(ctx context.Context, purls []string, client *Client, concurrency int) map[string]*Version {
	return ParallelMap(ctx, purls, concurrency, func(ctx context.Context, p string) (*Version, error) {
		return FetchLatestVersionFromPURL(ctx, p, client)
	})
}
