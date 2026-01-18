// Package npm provides a registry client for npmjs.com.
package npm

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/git-pkgs/registries/internal/core"
)

const (
	DefaultURL = "https://registry.npmjs.org"
	ecosystem  = "npm"
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
	ID          string                     `json:"_id"`
	Name        string                     `json:"name"`
	Description string                     `json:"description"`
	Homepage    interface{}                `json:"homepage"`
	Repository  interface{}                `json:"repository"`
	Versions    map[string]versionInfo     `json:"versions"`
	Time        map[string]string          `json:"time"`
	Maintainers []maintainerInfo           `json:"maintainers"`
	DistTags    map[string]string          `json:"dist-tags"`
}

type versionInfo struct {
	Name         string                 `json:"name"`
	Version      string                 `json:"version"`
	Description  string                 `json:"description"`
	Keywords     interface{}            `json:"keywords"`
	License      interface{}            `json:"license"`
	Homepage     interface{}            `json:"homepage"`
	Repository   interface{}            `json:"repository"`
	Dependencies map[string]string      `json:"dependencies"`
	DevDeps      map[string]string      `json:"devDependencies"`
	OptionalDeps map[string]string      `json:"optionalDependencies"`
	Deprecated   string                 `json:"deprecated"`
	Dist         distInfo               `json:"dist"`
	Maintainers  []maintainerInfo       `json:"maintainers"`
	NpmUser      map[string]interface{} `json:"_npmUser"`
	Engines      map[string]string      `json:"engines"`
	Funding      interface{}            `json:"funding"`
}

type distInfo struct {
	Shasum    string `json:"shasum"`
	Tarball   string `json:"tarball"`
	Integrity string `json:"integrity"`
}

type maintainerInfo struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

func (r *Registry) FetchPackage(ctx context.Context, name string) (*core.Package, error) {
	escapedName := url.PathEscape(name)
	url := fmt.Sprintf("%s/%s", r.baseURL, escapedName)

	var resp packageResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	latestVersion := resp.DistTags["latest"]
	var latest versionInfo
	if latestVersion != "" {
		latest = resp.Versions[latestVersion]
	} else if len(resp.Versions) > 0 {
		for _, v := range resp.Versions {
			latest = v
			break
		}
	}

	pkg := &core.Package{
		Name:          resp.ID,
		Description:   coalesceString(latest.Description, resp.Description),
		Homepage:      extractString(resp.Homepage),
		Repository:    extractRepoURL(resp.Repository, latest.Repository),
		Licenses:      extractLicense(latest.License),
		Keywords:      extractKeywords(latest.Keywords),
		Namespace:     extractNamespace(resp.ID),
		LatestVersion: latestVersion,
		Metadata: map[string]any{
			"dist-tags": resp.DistTags,
			"funding":   latest.Funding,
		},
	}

	return pkg, nil
}

func (r *Registry) FetchVersions(ctx context.Context, name string) ([]core.Version, error) {
	escapedName := url.PathEscape(name)
	url := fmt.Sprintf("%s/%s", r.baseURL, escapedName)

	var resp packageResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	versions := make([]core.Version, 0, len(resp.Versions))
	for num, v := range resp.Versions {
		var publishedAt time.Time
		if timeStr, ok := resp.Time[num]; ok {
			publishedAt, _ = time.Parse(time.RFC3339, timeStr)
		}

		var status core.VersionStatus
		if v.Deprecated != "" {
			status = core.StatusDeprecated
		}

		integrity := v.Dist.Integrity
		if integrity == "" && v.Dist.Shasum != "" {
			integrity = "sha1-" + v.Dist.Shasum
		}

		versions = append(versions, core.Version{
			Number:      num,
			PublishedAt: publishedAt,
			Licenses:    extractLicense(v.License),
			Integrity:   integrity,
			Status:      status,
			Metadata: map[string]any{
				"deprecated":   v.Deprecated,
				"dist":         v.Dist,
				"engines":      v.Engines,
				"_npmUser":     v.NpmUser,
				"tarball":      v.Dist.Tarball,
			},
		})
	}

	return versions, nil
}

func (r *Registry) FetchDependencies(ctx context.Context, name, version string) ([]core.Dependency, error) {
	escapedName := url.PathEscape(name)
	url := fmt.Sprintf("%s/%s", r.baseURL, escapedName)

	var resp packageResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	v, ok := resp.Versions[version]
	if !ok {
		return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name, Version: version}
	}

	var deps []core.Dependency

	for depName, req := range v.Dependencies {
		deps = append(deps, core.Dependency{
			Name:         depName,
			Requirements: req,
			Scope:        core.Runtime,
		})
	}

	for depName, req := range v.DevDeps {
		deps = append(deps, core.Dependency{
			Name:         depName,
			Requirements: req,
			Scope:        core.Development,
		})
	}

	for depName, req := range v.OptionalDeps {
		deps = append(deps, core.Dependency{
			Name:         depName,
			Requirements: req,
			Scope:        core.Optional,
			Optional:     true,
		})
	}

	return deps, nil
}

func (r *Registry) FetchMaintainers(ctx context.Context, name string) ([]core.Maintainer, error) {
	escapedName := url.PathEscape(name)
	url := fmt.Sprintf("%s/%s", r.baseURL, escapedName)

	var resp packageResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	maintainers := make([]core.Maintainer, len(resp.Maintainers))
	for i, m := range resp.Maintainers {
		maintainers[i] = core.Maintainer{
			UUID:  m.Name,
			Login: m.Name,
			Email: m.Email,
		}
	}

	return maintainers, nil
}

func extractString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	if arr, ok := v.([]interface{}); ok && len(arr) > 0 {
		if s, ok := arr[0].(string); ok {
			return s
		}
	}
	return ""
}

func extractRepoURL(pkgRepo, versionRepo interface{}) string {
	for _, repo := range []interface{}{versionRepo, pkgRepo} {
		switch r := repo.(type) {
		case string:
			return normalizeGitURL(r)
		case map[string]interface{}:
			if url, ok := r["url"].(string); ok {
				return normalizeGitURL(url)
			}
		case []interface{}:
			if len(r) > 0 {
				if m, ok := r[0].(map[string]interface{}); ok {
					if url, ok := m["url"].(string); ok {
						return normalizeGitURL(url)
					}
				}
			}
		}
	}
	return ""
}

func normalizeGitURL(u string) string {
	u = strings.TrimPrefix(u, "git+")
	u = strings.TrimPrefix(u, "git://")
	u = strings.TrimSuffix(u, ".git")
	if strings.HasPrefix(u, "github.com/") {
		u = "https://" + u
	}
	return u
}

func extractLicense(v interface{}) string {
	switch l := v.(type) {
	case string:
		return l
	case map[string]interface{}:
		if t, ok := l["type"].(string); ok {
			return t
		}
	case []interface{}:
		var licenses []string
		for _, item := range l {
			switch li := item.(type) {
			case string:
				licenses = append(licenses, li)
			case map[string]interface{}:
				if t, ok := li["type"].(string); ok {
					licenses = append(licenses, t)
				}
			}
		}
		return strings.Join(licenses, ",")
	}
	return ""
}

func extractKeywords(v interface{}) []string {
	switch k := v.(type) {
	case []interface{}:
		keywords := make([]string, 0, len(k))
		for _, item := range k {
			if s, ok := item.(string); ok && s != "" {
				keywords = append(keywords, s)
			}
		}
		return keywords
	case []string:
		return k
	}
	return nil
}

func extractNamespace(id string) string {
	if strings.HasPrefix(id, "@") && strings.Contains(id, "/") {
		parts := strings.SplitN(id, "/", 2)
		return strings.TrimPrefix(parts[0], "@")
	}
	return ""
}

func coalesceString(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

type URLs struct {
	baseURL string
}

func (u *URLs) Registry(name, version string) string {
	if version != "" {
		return fmt.Sprintf("https://www.npmjs.com/package/%s/v/%s", name, version)
	}
	return fmt.Sprintf("https://www.npmjs.com/package/%s", name)
}

func (u *URLs) Download(name, version string) string {
	if version == "" {
		return ""
	}
	shortName := name
	if strings.Contains(name, "/") {
		parts := strings.SplitN(name, "/", 2)
		shortName = parts[1]
	}
	return fmt.Sprintf("%s/%s/-/%s-%s.tgz", u.baseURL, name, shortName, version)
}

func (u *URLs) Documentation(name, version string) string {
	if version != "" {
		return fmt.Sprintf("https://www.npmjs.com/package/%s/v/%s", name, version)
	}
	return fmt.Sprintf("https://www.npmjs.com/package/%s", name)
}

func (u *URLs) PURL(name, version string) string {
	namespace := ""
	pkgName := name
	if strings.HasPrefix(name, "@") && strings.Contains(name, "/") {
		parts := strings.SplitN(name, "/", 2)
		namespace = parts[0]
		pkgName = parts[1]
	}

	if namespace != "" {
		if version != "" {
			return fmt.Sprintf("pkg:npm/%s/%s@%s", namespace, pkgName, version)
		}
		return fmt.Sprintf("pkg:npm/%s/%s", namespace, pkgName)
	}

	if version != "" {
		return fmt.Sprintf("pkg:npm/%s@%s", pkgName, version)
	}
	return fmt.Sprintf("pkg:npm/%s", pkgName)
}
