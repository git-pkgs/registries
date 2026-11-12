// Package terraform provides a registry client for Terraform modules and providers.
package terraform

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/git-pkgs/registries/internal/core"
	"github.com/git-pkgs/registries/internal/urlparser"
)

const (
	DefaultURL = "https://registry.terraform.io"
	ecosystem  = "terraform"
)

func init() {
	core.Register(ecosystem, DefaultURL, func(baseURL string, client *core.Client) core.Registry {
		return New(baseURL, client)
	})
}

var errInvalidName = errors.New("terraform name must be 'namespace/type' for a provider or 'namespace/name/provider' for a module")

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

// parseModuleName parses "namespace/name/provider" format
func parseModuleName(name string) (namespace, moduleName, provider string, ok bool) {
	parts := strings.Split(name, "/")
	if len(parts) == 3 { //nolint:mnd // namespace/name/provider
		return parts[0], parts[1], parts[2], true
	}
	return "", "", "", false
}

type moduleResponse struct {
	ID          string `json:"id"`
	Namespace   string `json:"namespace"`
	Name        string `json:"name"`
	Provider    string `json:"provider"`
	Description string `json:"description"`
	Source      string `json:"source"`
	Version     string `json:"version"`
	PublishedAt string `json:"published_at"`
	Downloads   int    `json:"downloads"`
	Verified    bool   `json:"verified"`
}

type moduleVersionsResponse struct {
	Modules []moduleVersionsEntry `json:"modules"`
}

type moduleVersionsEntry struct {
	Versions []versionEntry `json:"versions"`
}

type versionEntry struct {
	Version    string           `json:"version"`
	Submodules []submoduleEntry `json:"submodules"`
	Root       rootModule       `json:"root"`
}

type submoduleEntry struct {
	Path         string            `json:"path"`
	Dependencies []dependencyEntry `json:"dependencies"`
}

type rootModule struct {
	Dependencies []dependencyEntry `json:"dependencies"`
	Providers    []providerEntry   `json:"providers"`
}

type dependencyEntry struct {
	Name    string `json:"name"`
	Source  string `json:"source"`
	Version string `json:"version"`
}

type providerEntry struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Source    string `json:"source"`
	Version   string `json:"version"`
}

func (r *Registry) FetchPackage(ctx context.Context, name string) (*core.Package, error) {
	if namespace, typ, ok := parseProviderName(name); ok {
		return r.fetchProviderPackage(ctx, name, namespace, typ)
	}
	namespace, moduleName, provider, ok := parseModuleName(name)
	if !ok {
		return nil, errInvalidName
	}

	url := fmt.Sprintf("%s/v1/modules/%s/%s/%s", r.baseURL, namespace, moduleName, provider)

	var resp moduleResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	// Extract repository from source
	repository := urlparser.Parse(resp.Source)

	return &core.Package{
		Name:        fmt.Sprintf("%s/%s/%s", resp.Namespace, resp.Name, resp.Provider),
		Description: resp.Description,
		Homepage:    fmt.Sprintf("https://registry.terraform.io/modules/%s/%s/%s", namespace, moduleName, provider),
		Repository:  repository,
		Namespace:   resp.Namespace,
		Metadata: map[string]any{
			"provider":  resp.Provider,
			"downloads": resp.Downloads,
			"verified":  resp.Verified,
		},
	}, nil
}

func (r *Registry) FetchVersions(ctx context.Context, name string) ([]core.Version, error) {
	if namespace, typ, ok := parseProviderName(name); ok {
		return r.fetchProviderVersions(ctx, name, namespace, typ)
	}
	namespace, moduleName, provider, ok := parseModuleName(name)
	if !ok {
		return nil, errInvalidName
	}

	url := fmt.Sprintf("%s/v1/modules/%s/%s/%s/versions", r.baseURL, namespace, moduleName, provider)

	var resp moduleVersionsResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	if len(resp.Modules) == 0 {
		return nil, nil
	}

	versions := make([]core.Version, 0, len(resp.Modules[0].Versions))
	for _, v := range resp.Modules[0].Versions {
		versions = append(versions, core.Version{
			Number: v.Version,
		})
	}

	// Sort newest first (versions come oldest first from API)
	sort.Slice(versions, func(i, j int) bool {
		return versions[i].Number > versions[j].Number
	})

	return versions, nil
}

func (r *Registry) FetchDependencies(ctx context.Context, name, version string) ([]core.Dependency, error) {
	// Providers declare no dependencies.
	if _, _, ok := parseProviderName(name); ok {
		return nil, nil
	}
	namespace, moduleName, provider, ok := parseModuleName(name)
	if !ok {
		return nil, errInvalidName
	}

	url := fmt.Sprintf("%s/v1/modules/%s/%s/%s/%s", r.baseURL, namespace, moduleName, provider, version)

	var resp versionEntry
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name, Version: version}
		}
		return nil, err
	}

	var deps []core.Dependency

	// Root module dependencies
	for _, d := range resp.Root.Dependencies {
		deps = append(deps, core.Dependency{
			Name:         d.Name,
			Requirements: d.Version,
			Scope:        core.Runtime,
		})
	}

	// Provider dependencies
	for _, p := range resp.Root.Providers {
		providerName := p.Name
		if p.Namespace != "" {
			providerName = p.Namespace + "/" + p.Name
		}
		deps = append(deps, core.Dependency{
			Name:         providerName,
			Requirements: p.Version,
			Scope:        core.Runtime,
		})
	}

	sort.Slice(deps, func(i, j int) bool {
		return deps[i].Name < deps[j].Name
	})

	return deps, nil
}

func (r *Registry) FetchMaintainers(ctx context.Context, name string) ([]core.Maintainer, error) {
	namespace, _, _, ok := parseModuleName(name)
	if !ok {
		namespace, _, ok = parseProviderName(name)
	}
	if !ok {
		return nil, nil
	}

	// The namespace is the maintainer/organization
	return []core.Maintainer{{
		Login: namespace,
		URL:   fmt.Sprintf("https://registry.terraform.io/namespaces/%s", namespace),
	}}, nil
}

type URLs struct {
	baseURL string
}

func (u *URLs) Registry(name, version string) string {
	if namespace, typ, ok := parseProviderName(name); ok {
		return u.providerPage(namespace, typ, version, "")
	}
	namespace, moduleName, provider, ok := parseModuleName(name)
	if !ok {
		return ""
	}
	if version != "" {
		return fmt.Sprintf("https://registry.terraform.io/modules/%s/%s/%s/%s", namespace, moduleName, provider, version)
	}
	return fmt.Sprintf("https://registry.terraform.io/modules/%s/%s/%s", namespace, moduleName, provider)
}

// Download is the module's download endpoint. A provider has an archive per
// platform, named by the download endpoint for that platform, so it has none.
func (u *URLs) Download(name, version string) string {
	if _, _, ok := parseProviderName(name); ok {
		return ""
	}
	namespace, moduleName, provider, ok := parseModuleName(name)
	if !ok || version == "" {
		return ""
	}
	return fmt.Sprintf("%s/v1/modules/%s/%s/%s/%s/download", u.baseURL, namespace, moduleName, provider, version)
}

func (u *URLs) Documentation(name, version string) string {
	if namespace, typ, ok := parseProviderName(name); ok {
		return u.providerPage(namespace, typ, version, "/docs")
	}
	return u.Registry(name, version)
}

func (u *URLs) PURL(name, version string) string {
	if namespace, typ, ok := parseProviderName(name); ok {
		return u.providerPURL(namespace, typ, version)
	}
	namespace, moduleName, provider, ok := parseModuleName(name)
	if !ok {
		return ""
	}
	if version != "" {
		return fmt.Sprintf("pkg:terraform/%s/%s/%s@%s", namespace, moduleName, provider, version)
	}
	return fmt.Sprintf("pkg:terraform/%s/%s/%s", namespace, moduleName, provider)
}
