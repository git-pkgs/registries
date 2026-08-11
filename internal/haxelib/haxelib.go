// Package haxelib provides a registry client for Haxe packages.
package haxelib

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/git-pkgs/registries/internal/core"
	"github.com/git-pkgs/registries/internal/urlparser"
)

const (
	DefaultURL = "https://lib.haxe.org"
	ecosystem  = "haxelib"
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
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Website      string   `json:"website"`
	License      string   `json:"license"`
	Tags         []string `json:"tags"`
	Owner        string   `json:"owner"`
	Contributors []string `json:"contributors"`
	Versions     []versionInfo `json:"versions"`
	Downloads    int      `json:"downloads"`
}

type versionInfo struct {
	Version      string   `json:"version"`
	Date         string   `json:"date"`
	Comments     string   `json:"comments"`
	Dependencies map[string]string `json:"dependencies"`
}

func (r *Registry) FetchPackage(ctx context.Context, name string) (*core.Package, error) {
	url := fmt.Sprintf("%s/api/3.0/package-info/%s", r.baseURL, name)

	var resp packageResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	// Extract repository URL from website
	repository := urlparser.CanonicalURL(resp.Website)

	return &core.Package{
		Name:        resp.Name,
		Description: resp.Description,
		Homepage:    resp.Website,
		Repository:  repository,
		Licenses:    resp.License,
		Keywords:    resp.Tags,
		Metadata: map[string]any{
			"owner":        resp.Owner,
			"downloads":    resp.Downloads,
			"contributors": resp.Contributors,
		},
	}, nil
}

func (r *Registry) FetchVersions(ctx context.Context, name string) ([]core.Version, error) {
	url := fmt.Sprintf("%s/api/3.0/package-info/%s", r.baseURL, name)

	var resp packageResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	versions := make([]core.Version, 0, len(resp.Versions))
	for _, v := range resp.Versions {
		versions = append(versions, core.Version{
			Number:   v.Version,
			Licenses: resp.License,
			Metadata: map[string]any{
				"comments": v.Comments,
			},
		})
	}

	// Reverse to get newest first (Haxelib returns oldest first)
	for i, j := 0, len(versions)-1; i < j; i, j = i+1, j-1 {
		versions[i], versions[j] = versions[j], versions[i]
	}

	return versions, nil
}

func (r *Registry) FetchDependencies(ctx context.Context, name, version string) ([]core.Dependency, error) {
	url := fmt.Sprintf("%s/api/3.0/package-info/%s", r.baseURL, name)

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
		deps = append(deps, core.Dependency{
			Name:         depName,
			Requirements: constraint,
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
	url := fmt.Sprintf("%s/api/3.0/package-info/%s", r.baseURL, name)

	var resp packageResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	var maintainers []core.Maintainer

	if resp.Owner != "" {
		maintainers = append(maintainers, core.Maintainer{
			Login: resp.Owner,
			Role:  "owner",
		})
	}

	for _, c := range resp.Contributors {
		if c != resp.Owner {
			maintainers = append(maintainers, core.Maintainer{
				Login: c,
				Role:  "contributor",
			})
		}
	}

	return maintainers, nil
}

type URLs struct {
	baseURL string
}

func (u *URLs) Registry(name, version string) string {
	if version != "" {
		return fmt.Sprintf("%s/p/%s/%s", u.baseURL, name, version)
	}
	return fmt.Sprintf("%s/p/%s", u.baseURL, name)
}

func (u *URLs) Download(name, version string) string {
	if version == "" {
		return ""
	}
	return fmt.Sprintf("%s/files/%s-%s.zip", u.baseURL, name, version)
}

func (u *URLs) Documentation(name, version string) string {
	return fmt.Sprintf("%s/p/%s", u.baseURL, name)
}

func (u *URLs) PURL(name, version string) string {
	if version != "" {
		return fmt.Sprintf("pkg:haxelib/%s@%s", name, version)
	}
	return fmt.Sprintf("pkg:haxelib/%s", name)
}
