// Package homebrew provides a registry client for Homebrew packages.
package homebrew

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/git-pkgs/registries/internal/core"
	"github.com/git-pkgs/registries/internal/urlparser"
)

const (
	DefaultURL = "https://formulae.brew.sh"
	ecosystem  = "brew"
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

type formulaResponse struct {
	Name             string          `json:"name"`
	FullName         string          `json:"full_name"`
	Tap              string          `json:"tap"`
	Desc             string          `json:"desc"`
	License          string          `json:"license"`
	Homepage         string          `json:"homepage"`
	Versions         versionsInfo    `json:"versions"`
	URLs             urlsInfo        `json:"urls"`
	Dependencies     []string        `json:"dependencies"`
	BuildDependencies []string       `json:"build_dependencies"`
	TestDependencies []string        `json:"test_dependencies"`
	OptionalDependencies []string    `json:"optional_dependencies"`
	VersionedFormulae []string       `json:"versioned_formulae"`
	Deprecated       bool            `json:"deprecated"`
	DeprecationDate  string          `json:"deprecation_date"`
	DeprecationReason string         `json:"deprecation_reason"`
	Disabled         bool            `json:"disabled"`
	Analytics        analyticsInfo   `json:"analytics"`
}

type versionsInfo struct {
	Stable string `json:"stable"`
	Head   string `json:"head"`
	Bottle bool   `json:"bottle"`
}

type urlsInfo struct {
	Stable urlInfo `json:"stable"`
}

type urlInfo struct {
	URL      string `json:"url"`
	Revision int    `json:"revision"`
	Checksum string `json:"checksum"`
}

type analyticsInfo struct {
	Install install30d `json:"install"`
}

type install30d struct {
	Days30 map[string]int `json:"30d"`
}

func (r *Registry) FetchPackage(ctx context.Context, name string) (*core.Package, error) {
	url := fmt.Sprintf("%s/api/formula/%s.json", r.baseURL, name)

	var resp formulaResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	// Extract repository URL from homepage
	repository := urlparser.CanonicalURL(resp.Homepage)

	var status string
	if resp.Deprecated {
		status = "deprecated"
	} else if resp.Disabled {
		status = "disabled"
	}

	return &core.Package{
		Name:        resp.Name,
		Description: resp.Desc,
		Homepage:    resp.Homepage,
		Repository:  repository,
		Licenses:    resp.License,
		Metadata: map[string]any{
			"tap":               resp.Tap,
			"full_name":         resp.FullName,
			"status":            status,
			"deprecation_reason": resp.DeprecationReason,
		},
	}, nil
}

func (r *Registry) FetchVersions(ctx context.Context, name string) ([]core.Version, error) {
	url := fmt.Sprintf("%s/api/formula/%s.json", r.baseURL, name)

	var resp formulaResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	var versions []core.Version

	// Add stable version
	if resp.Versions.Stable != "" {
		var status core.VersionStatus
		if resp.Deprecated {
			status = core.StatusDeprecated
		}

		versions = append(versions, core.Version{
			Number:    resp.Versions.Stable,
			Licenses:  resp.License,
			Integrity: formatIntegrity(resp.URLs.Stable.Checksum),
			Status:    status,
			Metadata: map[string]any{
				"bottle": resp.Versions.Bottle,
			},
		})
	}

	// Add versioned formulae (e.g., python@3.11, node@18)
	for _, vf := range resp.VersionedFormulae {
		// Extract version from formula name like "python@3.11"
		parts := strings.SplitN(vf, "@", 2) //nolint:mnd // name@version split
		if len(parts) == 2 {                //nolint:mnd
			versions = append(versions, core.Version{
				Number: parts[1],
				Metadata: map[string]any{
					"formula": vf,
				},
			})
		}
	}

	return versions, nil
}

func formatIntegrity(checksum string) string {
	if checksum == "" {
		return ""
	}
	// Homebrew uses SHA256
	return "sha256-" + checksum
}

func (r *Registry) FetchDependencies(ctx context.Context, name, version string) ([]core.Dependency, error) {
	url := fmt.Sprintf("%s/api/formula/%s.json", r.baseURL, name)

	var resp formulaResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name, Version: version}
		}
		return nil, err
	}

	var deps []core.Dependency

	for _, d := range resp.Dependencies {
		deps = append(deps, core.Dependency{
			Name:  d,
			Scope: core.Runtime,
		})
	}

	for _, d := range resp.BuildDependencies {
		deps = append(deps, core.Dependency{
			Name:  d,
			Scope: core.Build,
		})
	}

	for _, d := range resp.TestDependencies {
		deps = append(deps, core.Dependency{
			Name:  d,
			Scope: core.Test,
		})
	}

	for _, d := range resp.OptionalDependencies {
		deps = append(deps, core.Dependency{
			Name:     d,
			Scope:    core.Optional,
			Optional: true,
		})
	}

	sort.Slice(deps, func(i, j int) bool {
		return deps[i].Name < deps[j].Name
	})

	return deps, nil
}

func (r *Registry) FetchMaintainers(ctx context.Context, name string) ([]core.Maintainer, error) {
	// Homebrew formulae don't expose maintainer info via API
	// Maintainers are tracked in the tap repository
	return nil, nil
}

type URLs struct {
	baseURL string
}

func (u *URLs) Registry(name, version string) string {
	return fmt.Sprintf("%s/formula/%s", u.baseURL, name)
}

func (u *URLs) Download(name, version string) string {
	// Download URLs are in the formula response, not predictable
	return ""
}

func (u *URLs) Documentation(name, version string) string {
	return fmt.Sprintf("%s/formula/%s", u.baseURL, name)
}

func (u *URLs) PURL(name, version string) string {
	if version != "" {
		return fmt.Sprintf("pkg:brew/%s@%s", name, version)
	}
	return fmt.Sprintf("pkg:brew/%s", name)
}
