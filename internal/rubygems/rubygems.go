// Package rubygems provides a registry client for rubygems.org.
package rubygems

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/git-pkgs/registries/internal/core"
)

const (
	DefaultURL = "https://rubygems.org"
	ecosystem  = "gem"
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

type gemResponse struct {
	Name           string            `json:"name"`
	Info           string            `json:"info"`
	Version        string            `json:"version"`
	Downloads      int               `json:"downloads"`
	Licenses       []string          `json:"licenses"`
	SHA            string            `json:"sha"`
	HomepageURI    string            `json:"homepage_uri"`
	SourceCodeURI  string            `json:"source_code_uri"`
	WikiURI        string            `json:"wiki_uri"`
	DocumentURI    string            `json:"documentation_uri"`
	BugTrackerURI  string            `json:"bug_tracker_uri"`
	ChangelogURI   string            `json:"changelog_uri"`
	FundingURI     string            `json:"funding_uri"`
	Metadata       map[string]string `json:"metadata"`
	Dependencies   dependenciesBlock `json:"dependencies"`
}

type dependenciesBlock struct {
	Development []gemDep `json:"development"`
	Runtime     []gemDep `json:"runtime"`
}

type gemDep struct {
	Name         string `json:"name"`
	Requirements string `json:"requirements"`
}

type versionResponse struct {
	Number          string            `json:"number"`
	Platform        string            `json:"platform"`
	CreatedAt       string            `json:"created_at"`
	Downloads       int               `json:"downloads_count"`
	Licenses        []string          `json:"licenses"`
	SHA             string            `json:"sha"`
	RubyVersion     string            `json:"ruby_version"`
	RubygemsVersion string            `json:"rubygems_version"`
	Prerelease      bool              `json:"prerelease"`
	Metadata        map[string]string `json:"metadata"`
}

type ownerResponse struct {
	ID     int    `json:"id"`
	Handle string `json:"handle"`
}

type dependencyVersionResponse struct {
	Dependencies dependenciesBlock `json:"dependencies"`
}

func (r *Registry) FetchPackage(ctx context.Context, name string) (*core.Package, error) {
	url := fmt.Sprintf("%s/api/v1/gems/%s.json", r.baseURL, name)

	var resp gemResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	repoURL := extractRepoURL(resp.SourceCodeURI, resp.WikiURI, resp.DocumentURI, resp.BugTrackerURI, resp.ChangelogURI, resp.HomepageURI)

	return &core.Package{
		Name:        resp.Name,
		Description: resp.Info,
		Homepage:    resp.HomepageURI,
		Repository:  repoURL,
		Licenses:    strings.Join(resp.Licenses, ","),
		Metadata: map[string]any{
			"downloads":   resp.Downloads,
			"funding_uri": resp.FundingURI,
		},
	}, nil
}

func extractRepoURL(urls ...string) string {
	for _, u := range urls {
		if u == "" {
			continue
		}
		if strings.Contains(u, "github.com") || strings.Contains(u, "gitlab.com") || strings.Contains(u, "bitbucket.org") {
			return u
		}
	}
	for _, u := range urls {
		if u != "" {
			return u
		}
	}
	return ""
}

func (r *Registry) FetchVersions(ctx context.Context, name string) ([]core.Version, error) {
	url := fmt.Sprintf("%s/api/v1/versions/%s.json", r.baseURL, name)

	var resp []versionResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	versions := make([]core.Version, len(resp))
	for i, v := range resp {
		var publishedAt time.Time
		if v.CreatedAt != "" {
			publishedAt, _ = time.Parse(time.RFC3339, v.CreatedAt)
		}

		number := v.Number
		if v.Platform != "" && v.Platform != "ruby" {
			number = fmt.Sprintf("%s-%s", v.Number, v.Platform)
		}

		var integrity string
		if v.SHA != "" {
			integrity = "sha256-" + v.SHA
		}

		versions[i] = core.Version{
			Number:      number,
			PublishedAt: publishedAt,
			Licenses:    strings.Join(v.Licenses, ","),
			Integrity:   integrity,
			Metadata: map[string]any{
				"platform":         v.Platform,
				"downloads":        v.Downloads,
				"ruby_version":     v.RubyVersion,
				"rubygems_version": v.RubygemsVersion,
				"prerelease":       v.Prerelease,
			},
		}
	}

	return versions, nil
}

func (r *Registry) FetchDependencies(ctx context.Context, name, version string) ([]core.Dependency, error) {
	url := fmt.Sprintf("%s/api/v2/rubygems/%s/versions/%s.json", r.baseURL, name, version)

	var resp dependencyVersionResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name, Version: version}
		}
		return nil, err
	}

	var deps []core.Dependency

	for _, d := range resp.Dependencies.Runtime {
		deps = append(deps, core.Dependency{
			Name:         d.Name,
			Requirements: d.Requirements,
			Scope:        core.Runtime,
		})
	}

	for _, d := range resp.Dependencies.Development {
		deps = append(deps, core.Dependency{
			Name:         d.Name,
			Requirements: d.Requirements,
			Scope:        core.Development,
		})
	}

	return deps, nil
}

func (r *Registry) FetchMaintainers(ctx context.Context, name string) ([]core.Maintainer, error) {
	url := fmt.Sprintf("%s/api/v1/gems/%s/owners.json", r.baseURL, name)

	var resp []ownerResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	maintainers := make([]core.Maintainer, len(resp))
	for i, u := range resp {
		maintainers[i] = core.Maintainer{
			UUID:  fmt.Sprintf("%d", u.ID),
			Login: u.Handle,
		}
	}

	return maintainers, nil
}

type URLs struct {
	baseURL string
}

func (u *URLs) Registry(name, version string) string {
	if version != "" {
		return fmt.Sprintf("%s/gems/%s/versions/%s", u.baseURL, name, version)
	}
	return fmt.Sprintf("%s/gems/%s", u.baseURL, name)
}

func (u *URLs) Download(name, version string) string {
	if version == "" {
		return ""
	}
	return fmt.Sprintf("%s/downloads/%s-%s.gem", u.baseURL, name, version)
}

func (u *URLs) Documentation(name, version string) string {
	if version != "" {
		return fmt.Sprintf("http://www.rubydoc.info/gems/%s/%s", name, version)
	}
	return fmt.Sprintf("http://www.rubydoc.info/gems/%s", name)
}

func (u *URLs) PURL(name, version string) string {
	if version != "" {
		return fmt.Sprintf("pkg:gem/%s@%s", name, version)
	}
	return fmt.Sprintf("pkg:gem/%s", name)
}
