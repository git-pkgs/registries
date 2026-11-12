package terraform

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/git-pkgs/purl"
	"github.com/git-pkgs/registries/internal/core"
	"github.com/git-pkgs/registries/internal/urlparser"
	"github.com/git-pkgs/vers"
)

// Providers are named namespace/type, as in a provider source address
// without its hostname (hashicorp/random); modules have three segments.
//
// Only the Provider Registry Protocol's version list
// (/v1/providers/{namespace}/{type}/versions) is served by every registry.
// registry.terraform.io also serves the provider's details at
// /v1/providers/{namespace}/{type}; others, such as registry.opentofu.org,
// answer 404 there.

// parseProviderName parses "namespace/type".
func parseProviderName(name string) (namespace, typ string, ok bool) {
	namespace, typ, ok = strings.Cut(name, "/")
	if !ok || namespace == "" || typ == "" || strings.Contains(typ, "/") {
		return "", "", false
	}
	return namespace, typ, true
}

type providerResponse struct {
	Namespace   string `json:"namespace"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Source      string `json:"source"`
	PublishedAt string `json:"published_at"`
	Downloads   int64  `json:"downloads"`
	Tier        string `json:"tier"`
}

type providerVersionsResponse struct {
	Versions []providerVersion `json:"versions"`
}

type providerVersion struct {
	Version   string             `json:"version"`
	Protocols []string           `json:"protocols"`
	Platforms []providerPlatform `json:"platforms"`
}

type providerPlatform struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

func (r *Registry) providerURL(namespace, typ string) string {
	return fmt.Sprintf("%s/v1/providers/%s/%s", r.baseURL, url.PathEscape(namespace), url.PathEscape(typ))
}

func (r *Registry) fetchProviderPackage(ctx context.Context, name, namespace, typ string) (*core.Package, error) {
	var resp providerResponse
	err := r.client.GetJSON(ctx, r.providerURL(namespace, typ), &resp)
	if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
		return r.fetchProtocolProviderPackage(ctx, name, namespace)
	}
	if err != nil {
		return nil, err
	}
	return &core.Package{
		Name:          name,
		Description:   resp.Description,
		Homepage:      r.urls.Registry(name, ""),
		Repository:    urlparser.Parse(resp.Source),
		Namespace:     namespace,
		LatestVersion: resp.Version,
		Metadata: map[string]any{
			"tier":      resp.Tier,
			"downloads": resp.Downloads,
		},
	}, nil
}

// fetchProtocolProviderPackage describes a provider from its version list
// alone, for registries that serve no details.
func (r *Registry) fetchProtocolProviderPackage(ctx context.Context, name, namespace string) (*core.Package, error) {
	typ := strings.TrimPrefix(name, namespace+"/")
	versions, err := r.fetchProviderVersions(ctx, name, namespace, typ)
	if err != nil {
		return nil, err
	}
	pkg := &core.Package{Name: name, Homepage: r.urls.Registry(name, ""), Namespace: namespace}
	if latest := core.SelectLatestVersion(versions, ecosystem, ""); latest != nil {
		pkg.LatestVersion = latest.Number
	}
	return pkg, nil
}

func (r *Registry) fetchProviderVersions(ctx context.Context, name, namespace, typ string) ([]core.Version, error) {
	var resp providerVersionsResponse
	if err := r.client.GetJSON(ctx, r.providerURL(namespace, typ)+"/versions", &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	versions := make([]core.Version, 0, len(resp.Versions))
	for _, v := range resp.Versions {
		if v.Version == "" {
			continue
		}
		platforms := make([]string, 0, len(v.Platforms))
		for _, p := range v.Platforms {
			platforms = append(platforms, p.OS+"_"+p.Arch)
		}
		versions = append(versions, core.Version{
			Number: v.Version,
			Metadata: map[string]any{
				"protocols": v.Protocols,
				"platforms": platforms,
			},
		})
	}
	// The protocol lists versions in no particular order.
	sort.SliceStable(versions, func(i, j int) bool {
		return vers.CompareWithScheme(versions[i].Number, versions[j].Number, ecosystem) > 0
	})
	return versions, nil
}

// providerPage is the provider's page on registry.terraform.io, or "" for
// another registry, which has no pages at these paths.
func (u *URLs) providerPage(namespace, typ, version, suffix string) string {
	if u.baseURL != DefaultURL {
		return ""
	}
	if version == "" {
		version = "latest"
	}
	return fmt.Sprintf("%s/providers/%s/%s/%s%s", DefaultURL, namespace, typ, version, suffix)
}

// providerPURL carries repository_url for any registry but the default one.
func (u *URLs) providerPURL(namespace, typ, version string) string {
	var qualifiers map[string]string
	if u.baseURL != DefaultURL {
		qualifiers = map[string]string{"repository_url": u.baseURL}
	}
	return purl.New(ecosystem, namespace, typ, version, qualifiers).String()
}
