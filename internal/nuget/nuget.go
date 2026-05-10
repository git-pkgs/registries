// Package nuget provides a registry client for nuget.org (.NET).
package nuget

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/git-pkgs/registries/internal/core"
	"github.com/git-pkgs/registries/internal/urlparser"
)

const (
	DefaultURL = "https://api.nuget.org/v3"
	ecosystem  = "nuget"
)

func init() {
	core.Register(ecosystem, DefaultURL, func(baseURL string, client *core.Client) core.Registry {
		return New(baseURL, client)
	})
}

type Registry struct {
	baseURL string
	client  *core.Client
	urls    *URLs
}

func New(baseURL string, client *core.Client) *Registry {
	if baseURL == "" {
		baseURL = DefaultURL
	}
	r := &Registry{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		client:  client,
	}
	r.urls = &URLs{baseURL: r.baseURL}
	return r
}

func (r *Registry) Ecosystem() string {
	return ecosystem
}

func (r *Registry) URLs() core.URLBuilder { //nolint:ireturn
	return r.urls
}

type registrationResponse struct {
	Items []registrationPage `json:"items"`
}

type registrationPage struct {
	Items []registrationLeaf `json:"items"`
}

type registrationLeaf struct {
	CatalogEntry catalogEntry `json:"catalogEntry"`
}

type catalogEntry struct {
	CatalogURL    string   `json:"@id"`
	ID            string   `json:"id"`
	Version       string   `json:"version"`
	Description   string   `json:"description"`
	Summary       string   `json:"summary"`
	Authors       string   `json:"authors"`
	IconURL       string   `json:"iconUrl"`
	LicenseURL    string   `json:"licenseUrl"`
	ProjectURL    string   `json:"projectUrl"`
	Published     string   `json:"published"`
	Tags          []string `json:"tags"`
	Listed        bool     `json:"listed"`
	Deprecation   *deprecationInfo `json:"deprecation"`
	Dependencies  []dependencyGroup `json:"dependencyGroups"`
	LicenseExpression string `json:"licenseExpression"`
}

type deprecationInfo struct {
	Message string   `json:"message"`
	Reasons []string `json:"reasons"`
}

type dependencyGroup struct {
	TargetFramework string       `json:"targetFramework"`
	Dependencies    []dependency `json:"dependencies"`
}

type dependency struct {
	ID    string `json:"id"`
	Range string `json:"range"`
}

type catalogLeaf struct {
	PackageHash          string `json:"packageHash"`
	PackageHashAlgorithm string `json:"packageHashAlgorithm"`
}

func (r *Registry) FetchPackage(ctx context.Context, name string) (*core.Package, error) {
	// NuGet IDs are case-insensitive, lowercase for URL
	lowerName := strings.ToLower(name)
	url := fmt.Sprintf("%s/registration5-semver1/%s/index.json", r.baseURL, lowerName)

	var resp registrationResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	// Get the latest version's catalog entry
	var latest *catalogEntry
	for _, page := range resp.Items {
		for _, leaf := range page.Items {
			if latest == nil || leaf.CatalogEntry.Listed {
				entry := leaf.CatalogEntry
				latest = &entry
			}
		}
	}

	if latest == nil {
		return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
	}

	var keywords []string
	if len(latest.Tags) > 0 {
		keywords = latest.Tags
	}

	description := latest.Description
	if description == "" {
		description = latest.Summary
	}

	licenses := latest.LicenseExpression
	if licenses == "" && latest.LicenseURL != "" {
		licenses = latest.LicenseURL
	}

	return &core.Package{
		Name:        latest.ID,
		Description: description,
		Homepage:    latest.ProjectURL,
		Repository:  extractRepository(latest.ProjectURL),
		Licenses:    licenses,
		Keywords:    keywords,
		Metadata: map[string]any{
			"icon_url":    latest.IconURL,
			"license_url": latest.LicenseURL,
		},
	}, nil
}

func extractRepository(projectURL string) string {
	return urlparser.Parse(projectURL)
}

func (r *Registry) FetchVersions(ctx context.Context, name string) ([]core.Version, error) {
	lowerName := strings.ToLower(name)
	url := fmt.Sprintf("%s/registration5-semver1/%s/index.json", r.baseURL, lowerName)

	var resp registrationResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	var versions []core.Version
	for _, page := range resp.Items {
		for _, leaf := range page.Items {
			entry := leaf.CatalogEntry

			var publishedAt time.Time
			if entry.Published != "" {
				publishedAt, _ = time.Parse(time.RFC3339, entry.Published)
			}

			var status core.VersionStatus
			if !entry.Listed {
				status = core.StatusYanked
			} else if entry.Deprecation != nil {
				status = core.StatusDeprecated
			}

			licenses := entry.LicenseExpression
			if licenses == "" && entry.LicenseURL != "" {
				licenses = entry.LicenseURL
			}

			versions = append(versions, core.Version{
				Number:      entry.Version,
				PublishedAt: publishedAt,
				Licenses:    licenses,
				Integrity:   r.fetchIntegrity(ctx, entry.CatalogURL),
				Status:      status,
				Metadata: map[string]any{
					"listed":      entry.Listed,
					"deprecation": entry.Deprecation,
				},
			})
		}
	}

	return versions, nil
}

// fetchIntegrity fetches the catalog leaf to extract the package hash.
// The registration index does not include packageHash, only the full
// catalog leaf does. Errors are swallowed since integrity is best-effort.
func (r *Registry) fetchIntegrity(ctx context.Context, catalogURL string) string {
	if catalogURL == "" {
		return ""
	}
	var leaf catalogLeaf
	if err := r.client.GetJSON(ctx, catalogURL, &leaf); err != nil {
		return ""
	}
	if leaf.PackageHash == "" {
		return ""
	}
	algo := strings.ToLower(leaf.PackageHashAlgorithm)
	if algo == "" {
		algo = "sha512"
	}
	return algo + "-" + leaf.PackageHash
}

func (r *Registry) FetchDependencies(ctx context.Context, name, version string) ([]core.Dependency, error) {
	lowerName := strings.ToLower(name)
	url := fmt.Sprintf("%s/registration5-semver1/%s/index.json", r.baseURL, lowerName)

	var resp registrationResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name, Version: version}
		}
		return nil, err
	}

	// Find the specific version
	for _, page := range resp.Items {
		for _, leaf := range page.Items {
			if leaf.CatalogEntry.Version == version {
				return extractDependencies(leaf.CatalogEntry.Dependencies), nil
			}
		}
	}

	return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name, Version: version}
}

func extractDependencies(groups []dependencyGroup) []core.Dependency {
	// Use a map to deduplicate dependencies across target frameworks
	seen := make(map[string]core.Dependency)

	for _, group := range groups {
		for _, dep := range group.Dependencies {
			key := dep.ID
			if _, ok := seen[key]; !ok {
				seen[key] = core.Dependency{
					Name:         dep.ID,
					Requirements: dep.Range,
					Scope:        core.Runtime,
				}
			}
		}
	}

	deps := make([]core.Dependency, 0, len(seen))
	for _, d := range seen {
		deps = append(deps, d)
	}
	return deps
}

func (r *Registry) FetchMaintainers(ctx context.Context, name string) ([]core.Maintainer, error) {
	lowerName := strings.ToLower(name)
	url := fmt.Sprintf("%s/registration5-semver1/%s/index.json", r.baseURL, lowerName)

	var resp registrationResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	// Find latest version for authors
	var authors string
	for _, page := range resp.Items {
		for _, leaf := range page.Items {
			if leaf.CatalogEntry.Authors != "" {
				authors = leaf.CatalogEntry.Authors
			}
		}
	}

	if authors == "" {
		return nil, nil
	}

	// Authors is a comma-separated string
	authorList := strings.Split(authors, ",")
	maintainers := make([]core.Maintainer, len(authorList))
	for i, a := range authorList {
		name := strings.TrimSpace(a)
		maintainers[i] = core.Maintainer{
			Name: name,
		}
	}

	return maintainers, nil
}

type URLs struct {
	baseURL string
}

func (u *URLs) Registry(name, version string) string {
	if version != "" {
		return fmt.Sprintf("https://www.nuget.org/packages/%s/%s", name, version)
	}
	return fmt.Sprintf("https://www.nuget.org/packages/%s", name)
}

func (u *URLs) Download(name, version string) string {
	if version == "" {
		return ""
	}
	lowerName := strings.ToLower(name)
	lowerVersion := strings.ToLower(version)
	return fmt.Sprintf("https://api.nuget.org/v3-flatcontainer/%s/%s/%s.%s.nupkg", lowerName, lowerVersion, lowerName, lowerVersion)
}

func (u *URLs) Documentation(name, version string) string {
	// NuGet packages typically don't have a separate documentation URL
	// Documentation is usually on the project URL or within the package
	return ""
}

func (u *URLs) PURL(name, version string) string {
	if version != "" {
		return fmt.Sprintf("pkg:nuget/%s@%s", name, version)
	}
	return fmt.Sprintf("pkg:nuget/%s", name)
}
