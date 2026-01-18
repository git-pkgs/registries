// Package hex provides a registry client for hex.pm (Elixir/Erlang).
package hex

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/git-pkgs/registries/internal/core"
)

const (
	DefaultURL = "https://hex.pm"
	ecosystem  = "hex"
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
	Name      string           `json:"name"`
	Meta      metaInfo         `json:"meta"`
	Releases  []releaseInfo    `json:"releases"`
	Downloads downloadsInfo    `json:"downloads"`
	Owners    []ownerInfo      `json:"owners"`
}

type metaInfo struct {
	Description string            `json:"description"`
	Licenses    []string          `json:"licenses"`
	Links       map[string]string `json:"links"`
}

type releaseInfo struct {
	Version    string `json:"version"`
	InsertedAt string `json:"inserted_at"`
}

type downloadsInfo struct {
	All int `json:"all"`
}

type ownerInfo struct {
	Username string `json:"username"`
	Email    string `json:"email"`
}

type versionResponse struct {
	Version    string                 `json:"version"`
	Checksum   string                 `json:"checksum"`
	Downloads  int                    `json:"downloads"`
	Retirement map[string]interface{} `json:"retirement"`
	Requirements map[string]requirementInfo `json:"requirements"`
}

type requirementInfo struct {
	Requirement string `json:"requirement"`
	Optional    bool   `json:"optional"`
	App         string `json:"app"`
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

	links := make(map[string]string)
	for k, v := range resp.Meta.Links {
		links[strings.ToLower(k)] = v
	}

	var homepage, repository string
	if gh, ok := links["github"]; ok {
		repository = gh
	}
	for k, v := range links {
		if k != "github" && homepage == "" {
			homepage = v
		}
	}

	return &core.Package{
		Name:        resp.Name,
		Description: resp.Meta.Description,
		Homepage:    homepage,
		Repository:  repository,
		Licenses:    strings.Join(resp.Meta.Licenses, ","),
		Metadata: map[string]any{
			"downloads": resp.Downloads.All,
			"links":     resp.Meta.Links,
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

	versions := make([]core.Version, 0, len(resp.Releases))
	for _, rel := range resp.Releases {
		// Fetch detailed version info for checksum and retirement status
		versionURL := fmt.Sprintf("%s/api/packages/%s/releases/%s", r.baseURL, name, rel.Version)
		var versionResp versionResponse
		if err := r.client.GetJSON(ctx, versionURL, &versionResp); err != nil {
			// If we can't get details, still include basic info
			var publishedAt time.Time
			if rel.InsertedAt != "" {
				publishedAt, _ = time.Parse(time.RFC3339, rel.InsertedAt)
			}
			versions = append(versions, core.Version{
				Number:      rel.Version,
				PublishedAt: publishedAt,
			})
			continue
		}

		var publishedAt time.Time
		if rel.InsertedAt != "" {
			publishedAt, _ = time.Parse(time.RFC3339, rel.InsertedAt)
		}

		var status core.VersionStatus
		if versionResp.Retirement != nil {
			status = core.StatusRetracted
		}

		var integrity string
		if versionResp.Checksum != "" {
			integrity = "sha256-" + versionResp.Checksum
		}

		versions = append(versions, core.Version{
			Number:      versionResp.Version,
			PublishedAt: publishedAt,
			Integrity:   integrity,
			Status:      status,
			Metadata: map[string]any{
				"downloads":  versionResp.Downloads,
				"retirement": versionResp.Retirement,
			},
		})
	}

	return versions, nil
}

func (r *Registry) FetchDependencies(ctx context.Context, name, version string) ([]core.Dependency, error) {
	url := fmt.Sprintf("%s/api/packages/%s/releases/%s", r.baseURL, name, version)

	var resp versionResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name, Version: version}
		}
		return nil, err
	}

	deps := make([]core.Dependency, 0, len(resp.Requirements))
	for depName, req := range resp.Requirements {
		scope := core.Runtime
		if req.Optional {
			scope = core.Optional
		}

		deps = append(deps, core.Dependency{
			Name:         depName,
			Requirements: req.Requirement,
			Scope:        scope,
			Optional:     req.Optional,
		})
	}

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

	maintainers := make([]core.Maintainer, len(resp.Owners))
	for i, owner := range resp.Owners {
		maintainers[i] = core.Maintainer{
			UUID:  owner.Username,
			Login: owner.Username,
			Email: owner.Email,
		}
	}

	return maintainers, nil
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
	return fmt.Sprintf("https://repo.hex.pm/tarballs/%s-%s.tar", name, version)
}

func (u *URLs) Documentation(name, version string) string {
	if version != "" {
		return fmt.Sprintf("https://hexdocs.pm/%s/%s", name, version)
	}
	return fmt.Sprintf("https://hexdocs.pm/%s", name)
}

func (u *URLs) PURL(name, version string) string {
	if version != "" {
		return fmt.Sprintf("pkg:hex/%s@%s", name, version)
	}
	return fmt.Sprintf("pkg:hex/%s", name)
}
