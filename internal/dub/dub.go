// Package dub provides a registry client for D language packages.
package dub

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/git-pkgs/registries/internal/core"
	"github.com/git-pkgs/registries/internal/urlparser"
)

const (
	DefaultURL = "https://code.dlang.org"
	ecosystem  = "dub"
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
	Name           string        `json:"name"`
	Description    string        `json:"description"`
	Homepage       string        `json:"homepage"`
	Repository     string        `json:"repository"`
	DocumentationURL string      `json:"documentationURL"`
	Categories     []string      `json:"categories"`
	Versions       []versionInfo `json:"versions"`
	Owner          string        `json:"owner"`
}

type versionInfo struct {
	Version      string    `json:"version"`
	Date         string    `json:"date"`
	License      string    `json:"license"`
	Dependencies map[string]interface{} `json:"dependencies"`
}

func (r *Registry) FetchPackage(ctx context.Context, name string) (*core.Package, error) {
	url := fmt.Sprintf("%s/api/packages/%s", r.baseURL, name)

	var resp packageResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	// Get license from latest version
	var license string
	if len(resp.Versions) > 0 {
		license = resp.Versions[0].License
	}

	// Extract repository URL
	repository := urlparser.Parse(resp.Repository)
	if repository == "" {
		repository = urlparser.CanonicalURL(resp.Homepage)
	}

	return &core.Package{
		Name:        resp.Name,
		Description: resp.Description,
		Homepage:    resp.Homepage,
		Repository:  repository,
		Licenses:    license,
		Keywords:    resp.Categories,
		Metadata: map[string]any{
			"owner":            resp.Owner,
			"documentation_url": resp.DocumentationURL,
		},
	}, nil
}

func (r *Registry) FetchVersions(ctx context.Context, name string) ([]core.Version, error) {
	url := fmt.Sprintf("%s/api/packages/%s", r.baseURL, name)

	var resp packageResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	versions := make([]core.Version, 0, len(resp.Versions))
	for _, v := range resp.Versions {
		var publishedAt time.Time
		if v.Date != "" {
			// Try parsing ISO format
			publishedAt, _ = time.Parse(time.RFC3339, v.Date)
			if publishedAt.IsZero() {
				publishedAt, _ = time.Parse("2006-01-02T15:04:05", v.Date)
			}
		}

		versions = append(versions, core.Version{
			Number:      v.Version,
			PublishedAt: publishedAt,
			Licenses:    v.License,
		})
	}

	return versions, nil
}

func (r *Registry) FetchDependencies(ctx context.Context, name, version string) ([]core.Dependency, error) {
	url := fmt.Sprintf("%s/api/packages/%s", r.baseURL, name)

	var resp packageResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name, Version: version}
		}
		return nil, err
	}

	// Find the matching version
	var targetVersion *versionInfo
	for i := range resp.Versions {
		if resp.Versions[i].Version == version {
			targetVersion = &resp.Versions[i]
			break
		}
	}

	if targetVersion == nil {
		return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name, Version: version}
	}

	var deps []core.Dependency
	for depName, constraint := range targetVersion.Dependencies {
		var requirements string
		switch c := constraint.(type) {
		case string:
			requirements = c
		case map[string]interface{}:
			// Complex dependency with version field
			if v, ok := c["version"].(string); ok {
				requirements = v
			}
		}

		deps = append(deps, core.Dependency{
			Name:         depName,
			Requirements: requirements,
			Scope:        core.Runtime,
		})
	}

	// Sort for consistent output
	sort.Slice(deps, func(i, j int) bool {
		return deps[i].Name < deps[j].Name
	})

	return deps, nil
}

func (r *Registry) FetchMaintainers(ctx context.Context, name string) ([]core.Maintainer, error) {
	url := fmt.Sprintf("%s/api/packages/%s", r.baseURL, name)

	var resp packageResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	if resp.Owner == "" {
		return nil, nil
	}

	return []core.Maintainer{{
		Login: resp.Owner,
	}}, nil
}

type URLs struct {
	baseURL string
}

func (u *URLs) Registry(name, version string) string {
	if version != "" {
		return fmt.Sprintf("%s/packages/%s/%s", u.baseURL, name, version)
	}
	return fmt.Sprintf("%s/packages/%s", u.baseURL, name)
}

func (u *URLs) Download(name, version string) string {
	if version == "" {
		return ""
	}
	return fmt.Sprintf("%s/packages/%s/%s.zip", u.baseURL, name, version)
}

func (u *URLs) Documentation(name, version string) string {
	if version != "" {
		return fmt.Sprintf("%s/packages/%s/%s", u.baseURL, name, version)
	}
	return fmt.Sprintf("%s/packages/%s", u.baseURL, name)
}

func (u *URLs) PURL(name, version string) string {
	if version != "" {
		return fmt.Sprintf("pkg:dub/%s@%s", name, version)
	}
	return fmt.Sprintf("pkg:dub/%s", name)
}
