// Package cargo provides a registry client for crates.io.
package cargo

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/git-pkgs/registries/internal/core"
)

const (
	DefaultURL = "https://crates.io"
	ecosystem  = "cargo"
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

type crateResponse struct {
	Crate    crateInfo        `json:"crate"`
	Versions []versionInfo    `json:"versions"`
}

type crateInfo struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Homepage    string   `json:"homepage"`
	Repository  string   `json:"repository"`
	Keywords    []string `json:"keywords"`
	Categories  []string `json:"categories"`
	Downloads   int      `json:"downloads"`
}

type versionInfo struct {
	ID          int                    `json:"id"`
	Num         string                 `json:"num"`
	License     string                 `json:"license"`
	Checksum    string                 `json:"checksum"`
	Yanked      bool                   `json:"yanked"`
	YankMessage string                 `json:"yank_message"`
	CreatedAt   string                 `json:"created_at"`
	Downloads   int                    `json:"downloads"`
	Features    map[string][]string    `json:"features"`
	RustVersion string                 `json:"rust_version"`
	CrateSize   int                    `json:"crate_size"`
	PublishedBy map[string]interface{} `json:"published_by"`
}

type dependenciesResponse struct {
	Dependencies []dependencyInfo `json:"dependencies"`
}

type dependencyInfo struct {
	CrateID  string `json:"crate_id"`
	Req      string `json:"req"`
	Kind     string `json:"kind"`
	Optional bool   `json:"optional"`
}

type ownersResponse struct {
	Users []ownerInfo `json:"users"`
}

type ownerInfo struct {
	ID    int    `json:"id"`
	Login string `json:"login"`
	Name  string `json:"name"`
	URL   string `json:"url"`
}

func (r *Registry) FetchPackage(ctx context.Context, name string) (*core.Package, error) {
	url := fmt.Sprintf("%s/api/v1/crates/%s", r.baseURL, name)

	var resp crateResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	var licenses string
	if len(resp.Versions) > 0 {
		licenses = resp.Versions[0].License
	}

	return &core.Package{
		Name:        resp.Crate.ID,
		Description: resp.Crate.Description,
		Homepage:    resp.Crate.Homepage,
		Repository:  resp.Crate.Repository,
		Licenses:    licenses,
		Keywords:    resp.Crate.Keywords,
		Metadata: map[string]any{
			"categories": resp.Crate.Categories,
			"downloads":  resp.Crate.Downloads,
		},
	}, nil
}

func (r *Registry) FetchVersions(ctx context.Context, name string) ([]core.Version, error) {
	url := fmt.Sprintf("%s/api/v1/crates/%s", r.baseURL, name)

	var resp crateResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	versions := make([]core.Version, len(resp.Versions))
	for i, v := range resp.Versions {
		var publishedAt time.Time
		if v.CreatedAt != "" {
			publishedAt, _ = time.Parse(time.RFC3339, v.CreatedAt)
		}

		var status core.VersionStatus
		if v.Yanked {
			status = core.StatusYanked
		}

		var integrity string
		if v.Checksum != "" {
			integrity = "sha256-" + v.Checksum
		}

		versions[i] = core.Version{
			Number:      v.Num,
			PublishedAt: publishedAt,
			Licenses:    v.License,
			Integrity:   integrity,
			Status:      status,
			Metadata: map[string]any{
				"id":           v.ID,
				"downloads":    v.Downloads,
				"features":     v.Features,
				"rust_version": v.RustVersion,
				"crate_size":   v.CrateSize,
				"published_by": v.PublishedBy,
				"yank_message": v.YankMessage,
			},
		}
	}

	return versions, nil
}

func (r *Registry) FetchDependencies(ctx context.Context, name, version string) ([]core.Dependency, error) {
	url := fmt.Sprintf("%s/api/v1/crates/%s/%s/dependencies", r.baseURL, name, version)

	var resp dependenciesResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name, Version: version}
		}
		return nil, err
	}

	deps := make([]core.Dependency, len(resp.Dependencies))
	for i, d := range resp.Dependencies {
		deps[i] = core.Dependency{
			Name:         d.CrateID,
			Requirements: d.Req,
			Scope:        mapScope(d.Kind),
			Optional:     d.Optional,
		}
	}

	return deps, nil
}

func mapScope(kind string) core.Scope {
	switch kind {
	case "dev":
		return core.Development
	case "build":
		return core.Build
	default:
		return core.Runtime
	}
}

func (r *Registry) FetchMaintainers(ctx context.Context, name string) ([]core.Maintainer, error) {
	url := fmt.Sprintf("%s/api/v1/crates/%s/owner_user", r.baseURL, name)

	var resp ownersResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	maintainers := make([]core.Maintainer, len(resp.Users))
	for i, u := range resp.Users {
		maintainers[i] = core.Maintainer{
			UUID:  fmt.Sprintf("%d", u.ID),
			Login: u.Login,
			Name:  u.Name,
			URL:   u.URL,
		}
	}

	return maintainers, nil
}

type URLs struct {
	baseURL string
}

func (u *URLs) Registry(name, version string) string {
	if version != "" {
		return fmt.Sprintf("%s/crates/%s/%s", u.baseURL, name, version)
	}
	return fmt.Sprintf("%s/crates/%s", u.baseURL, name)
}

func (u *URLs) Download(name, version string) string {
	if version == "" {
		return ""
	}
	return fmt.Sprintf("https://static.crates.io/crates/%s/%s-%s.crate", name, name, version)
}

func (u *URLs) Documentation(name, version string) string {
	if version != "" {
		return fmt.Sprintf("https://docs.rs/%s/%s", name, version)
	}
	return fmt.Sprintf("https://docs.rs/%s", name)
}

func (u *URLs) PURL(name, version string) string {
	if version != "" {
		return fmt.Sprintf("pkg:cargo/%s@%s", name, version)
	}
	return fmt.Sprintf("pkg:cargo/%s", name)
}
