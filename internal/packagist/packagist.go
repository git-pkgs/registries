// Package packagist provides a registry client for packagist.org (PHP/Composer).
package packagist

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/git-pkgs/registries/internal/core"
	"github.com/git-pkgs/registries/internal/urlparser"
)

const (
	DefaultURL = "https://packagist.org"
	ecosystem  = "composer"
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

type packageResponse struct {
	Package packageInfo `json:"package"`
}

type packageInfo struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Time        string                 `json:"time"`
	Maintainers []maintainerInfo       `json:"maintainers"`
	Versions    map[string]versionInfo `json:"versions"`
	Type        string                 `json:"type"`
	Repository  string                 `json:"repository"`
	Language    string                 `json:"language"`
	Abandoned   interface{}            `json:"abandoned"`
}

type maintainerInfo struct {
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
}

type versionInfo struct {
	Name             string            `json:"name"`
	Description      string            `json:"description"`
	Version          string            `json:"version"`
	VersionNormalized string           `json:"version_normalized"`
	License          []string          `json:"license"`
	Homepage         string            `json:"homepage"`
	Time             string            `json:"time"`
	Source           sourceInfo        `json:"source"`
	Dist             distInfo          `json:"dist"`
	Require          map[string]string `json:"require"`
	RequireDev       map[string]string `json:"require-dev"`
}

type sourceInfo struct {
	Type      string `json:"type"`
	URL       string `json:"url"`
	Reference string `json:"reference"`
}

type distInfo struct {
	Type      string `json:"type"`
	URL       string `json:"url"`
	Reference string `json:"reference"`
	Shasum    string `json:"shasum"`
}

func (r *Registry) FetchPackage(ctx context.Context, name string) (*core.Package, error) {
	url := fmt.Sprintf("%s/packages/%s.json", r.baseURL, name)

	var resp packageResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	pkg := resp.Package

	// Extract namespace (vendor) from name
	var namespace string
	if parts := strings.SplitN(name, "/", 2); len(parts) == 2 { //nolint:mnd // vendor/package split
		namespace = parts[0]
	}

	// Find the latest stable version for homepage/repository
	var homepage, repository, licenses string
	for _, v := range pkg.Versions {
		if v.Homepage != "" && homepage == "" {
			homepage = v.Homepage
		}
		if v.Source.URL != "" && repository == "" {
			repository = urlparser.Parse(v.Source.URL)
		}
		if len(v.License) > 0 && licenses == "" {
			licenses = strings.Join(v.License, ",")
		}
	}

	// Fallback to package-level repository
	if repository == "" && pkg.Repository != "" {
		repository = urlparser.Parse(pkg.Repository)
	}

	return &core.Package{
		Name:        pkg.Name,
		Description: pkg.Description,
		Homepage:    homepage,
		Repository:  repository,
		Licenses:    licenses,
		Namespace:   namespace,
		Metadata: map[string]any{
			"type":      pkg.Type,
			"abandoned": pkg.Abandoned,
		},
	}, nil
}

func (r *Registry) FetchVersions(ctx context.Context, name string) ([]core.Version, error) {
	url := fmt.Sprintf("%s/packages/%s.json", r.baseURL, name)

	var resp packageResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	versions := make([]core.Version, 0, len(resp.Package.Versions))
	for _, v := range resp.Package.Versions {
		var publishedAt time.Time
		if v.Time != "" {
			publishedAt, _ = time.Parse(time.RFC3339, v.Time)
		}

		var integrity string
		if v.Dist.Shasum != "" {
			integrity = "sha1-" + v.Dist.Shasum
		}

		var status core.VersionStatus
		if resp.Package.Abandoned != nil {
			status = core.StatusDeprecated
		}

		var artifacts []core.Artifact
		if v.Dist.URL != "" {
			artifacts = []core.Artifact{{
				URL:       v.Dist.URL,
				Filename:  distFilename(v.Dist.URL, v.Dist.Type),
				Integrity: integrity,
			}}
		}

		versions = append(versions, core.Version{
			Number:      v.Version,
			PublishedAt: publishedAt,
			Licenses:    strings.Join(v.License, ","),
			Integrity:   integrity,
			Status:      status,
			Artifacts:   artifacts,
			Metadata: map[string]any{
				"dist_url":  v.Dist.URL,
				"dist_type": v.Dist.Type,
			},
		})
	}

	return versions, nil
}

// distFilename is the last path segment of a dist URL. GitHub zipball URLs
// end in a bare commit hash, so zip dists without an extension get ".zip".
func distFilename(rawURL, distType string) string {
	name := rawURL
	if u, err := url.Parse(rawURL); err == nil {
		name = u.Path
	}
	name = path.Base(name)
	if path.Ext(name) == "" && distType == "zip" {
		name += ".zip"
	}
	return name
}

func (r *Registry) FetchDependencies(ctx context.Context, name, version string) ([]core.Dependency, error) {
	url := fmt.Sprintf("%s/packages/%s.json", r.baseURL, name)

	var resp packageResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name, Version: version}
		}
		return nil, err
	}

	versionInfo, ok := resp.Package.Versions[version]
	if !ok {
		// Try with 'v' prefix
		versionInfo, ok = resp.Package.Versions["v"+version]
		if !ok {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name, Version: version}
		}
	}

	var deps []core.Dependency

	for depName, req := range versionInfo.Require {
		// Skip PHP and extension requirements
		if depName == "php" || strings.HasPrefix(depName, "ext-") {
			continue
		}
		deps = append(deps, core.Dependency{
			Name:         depName,
			Requirements: req,
			Scope:        core.Runtime,
		})
	}

	for depName, req := range versionInfo.RequireDev {
		deps = append(deps, core.Dependency{
			Name:         depName,
			Requirements: req,
			Scope:        core.Development,
		})
	}

	return deps, nil
}

func (r *Registry) FetchMaintainers(ctx context.Context, name string) ([]core.Maintainer, error) {
	url := fmt.Sprintf("%s/packages/%s.json", r.baseURL, name)

	var resp packageResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	maintainers := make([]core.Maintainer, len(resp.Package.Maintainers))
	for i, m := range resp.Package.Maintainers {
		maintainers[i] = core.Maintainer{
			Login: m.Name,
			Name:  m.Name,
			URL:   m.AvatarURL,
		}
	}

	return maintainers, nil
}

type URLs struct {
	baseURL string
}

func (u *URLs) Registry(name, version string) string {
	if version != "" {
		return fmt.Sprintf("%s/packages/%s#%s", u.baseURL, name, version)
	}
	return fmt.Sprintf("%s/packages/%s", u.baseURL, name)
}

func (u *URLs) Download(name, version string) string {
	if version == "" {
		return ""
	}
	// Packagist doesn't have a standard download URL pattern
	// Downloads go through the dist URL in the version info
	return ""
}

func (u *URLs) Documentation(name, version string) string {
	// No standard documentation URL for Composer packages
	return ""
}

func (u *URLs) PURL(name, version string) string {
	if version != "" {
		return fmt.Sprintf("pkg:composer/%s@%s", name, version)
	}
	return fmt.Sprintf("pkg:composer/%s", name)
}
