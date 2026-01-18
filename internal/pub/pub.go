// Package pub provides a registry client for pub.dev (Dart/Flutter).
package pub

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/git-pkgs/registries/internal/core"
)

const (
	DefaultURL = "https://pub.dev"
	ecosystem  = "pub"
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

func (r *Registry) URLs() core.URLBuilder {
	return r.urls
}

type packageResponse struct {
	Name     string        `json:"name"`
	Latest   versionInfo   `json:"latest"`
	Versions []versionInfo `json:"versions"`
}

type versionInfo struct {
	Version   string    `json:"version"`
	Published time.Time `json:"published"`
	Pubspec   pubspec   `json:"pubspec"`
}

type pubspec struct {
	Name         string                 `json:"name"`
	Description  string                 `json:"description"`
	Version      string                 `json:"version"`
	Homepage     string                 `json:"homepage"`
	Repository   string                 `json:"repository"`
	License      string                 `json:"license"`
	Dependencies map[string]interface{} `json:"dependencies"`
	DevDeps      map[string]interface{} `json:"dev_dependencies"`
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

	latest := resp.Latest.Pubspec
	repository := latest.Repository
	if repository == "" {
		repository = latest.Homepage
	}

	return &core.Package{
		Name:          resp.Name,
		Description:   latest.Description,
		Homepage:      latest.Homepage,
		Repository:    repository,
		Licenses:      latest.License,
		LatestVersion: resp.Latest.Version,
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

	versions := make([]core.Version, len(resp.Versions))
	for i, v := range resp.Versions {
		versions[i] = core.Version{
			Number:      v.Version,
			PublishedAt: v.Published,
			Licenses:    v.Pubspec.License,
		}
	}

	return versions, nil
}

func (r *Registry) FetchDependencies(ctx context.Context, name, version string) ([]core.Dependency, error) {
	url := fmt.Sprintf("%s/api/packages/%s/versions/%s", r.baseURL, name, version)

	var resp versionInfo
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name, Version: version}
		}
		return nil, err
	}

	var deps []core.Dependency

	for depName, req := range resp.Pubspec.Dependencies {
		deps = append(deps, core.Dependency{
			Name:         depName,
			Requirements: formatRequirement(req),
			Scope:        core.Runtime,
		})
	}

	for depName, req := range resp.Pubspec.DevDeps {
		deps = append(deps, core.Dependency{
			Name:         depName,
			Requirements: formatRequirement(req),
			Scope:        core.Development,
		})
	}

	return deps, nil
}

func formatRequirement(req interface{}) string {
	switch v := req.(type) {
	case string:
		return v
	case map[string]interface{}:
		if ver, ok := v["version"].(string); ok {
			return ver
		}
		if git, ok := v["git"]; ok {
			if gitMap, ok := git.(map[string]interface{}); ok {
				if url, ok := gitMap["url"].(string); ok {
					return "git:" + url
				}
			}
			if gitStr, ok := git.(string); ok {
				return "git:" + gitStr
			}
		}
		if hosted, ok := v["hosted"]; ok {
			if hostedMap, ok := hosted.(map[string]interface{}); ok {
				if name, ok := hostedMap["name"].(string); ok {
					return "hosted:" + name
				}
			}
		}
		if path, ok := v["path"].(string); ok {
			return "path:" + path
		}
	}
	return ""
}

func (r *Registry) FetchMaintainers(ctx context.Context, name string) ([]core.Maintainer, error) {
	// pub.dev API doesn't expose maintainers in the standard package endpoint
	return nil, nil
}

type URLs struct {
	baseURL string
}

func (u *URLs) Registry(name, version string) string {
	if version != "" {
		return fmt.Sprintf("%s/packages/%s/versions/%s", u.baseURL, name, version)
	}
	return fmt.Sprintf("%s/packages/%s", u.baseURL, name)
}

func (u *URLs) Download(name, version string) string {
	if version == "" {
		return ""
	}
	return fmt.Sprintf("https://pub.dev/packages/%s/versions/%s.tar.gz", name, version)
}

func (u *URLs) Documentation(name, version string) string {
	if version != "" {
		return fmt.Sprintf("https://pub.dev/documentation/%s/%s/", name, version)
	}
	return fmt.Sprintf("https://pub.dev/documentation/%s/latest/", name)
}

func (u *URLs) PURL(name, version string) string {
	if version != "" {
		return fmt.Sprintf("pkg:pub/%s@%s", name, version)
	}
	return fmt.Sprintf("pkg:pub/%s", name)
}
