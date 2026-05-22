// Package golang provides a registry client for the Go module proxy.
package golang

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/git-pkgs/registries/internal/core"
	"github.com/git-pkgs/registries/internal/urlparser"
)

const (
	DefaultURL = "https://proxy.golang.org"
	pkgsiteAPI = "https://pkg.go.dev/v1beta"
	ecosystem  = "golang"

	pkgsitePageLimit = 500
)

func init() {
	core.Register(ecosystem, DefaultURL, func(baseURL string, client *core.Client) core.Registry {
		return New(baseURL, client)
	})
}

type Registry struct {
	baseURL    string
	pkgsiteURL string
	client     *core.Client
	urls       *URLs
}

func New(baseURL string, client *core.Client) *Registry {
	if baseURL == "" {
		baseURL = DefaultURL
	}
	r := &Registry{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		client:  client,
	}
	// The pkgsite API only covers modules visible on pkg.go.dev. When pointed
	// at a private proxy we fall back to the goproxy protocol exclusively.
	if r.baseURL == DefaultURL {
		r.pkgsiteURL = pkgsiteAPI
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

type versionInfo struct {
	Version string    `json:"Version"`
	Time    time.Time `json:"Time"`
}

type pkgsiteLicense struct {
	Types []string `json:"types"`
}

type pkgsiteModule struct {
	Path     string           `json:"path"`
	Version  string           `json:"version"`
	RepoURL  string           `json:"repoUrl"`
	Licenses []pkgsiteLicense `json:"licenses"`
}

type pkgsiteVersion struct {
	Version    string    `json:"version"`
	CommitTime time.Time `json:"commitTime"`
	Deprecated bool      `json:"deprecated"`
	Retracted  bool      `json:"retracted"`
}

type pkgsiteVersions struct {
	Items         []pkgsiteVersion `json:"items"`
	NextPageToken string           `json:"nextPageToken"`
}

// encodeForProxy encodes a module path according to the goproxy protocol.
// Capital letters are replaced with "!" followed by the lowercase letter.
// https://go.dev/ref/mod#goproxy-protocol
func encodeForProxy(path string) string {
	var b strings.Builder
	for _, r := range path {
		if r >= 'A' && r <= 'Z' {
			b.WriteRune('!')
			b.WriteRune(unicode.ToLower(r))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (r *Registry) FetchPackage(ctx context.Context, name string) (*core.Package, error) {
	if r.pkgsiteURL != "" {
		if pkg, err := r.fetchPackagePkgsite(ctx, name); err == nil {
			return pkg, nil
		}
		// Any pkgsite failure (including 404, since pkg.go.dev can lag the
		// proxy) falls through to the goproxy protocol.
	}
	return r.fetchPackageProxy(ctx, name)
}

func (r *Registry) fetchPackagePkgsite(ctx context.Context, name string) (*core.Package, error) {
	moduleURL := fmt.Sprintf("%s/module/%s?licenses=true", r.pkgsiteURL, name)

	var mod pkgsiteModule
	if err := r.client.GetJSON(ctx, moduleURL, &mod); err != nil {
		return nil, err
	}

	repoURL := urlparser.Parse(mod.RepoURL)
	if repoURL == "" {
		// pkgsite already returns a validated URL; keep it even if urlparser
		// doesn't recognise the host (e.g. go.googlesource.com, gitea instances).
		repoURL = mod.RepoURL
	}
	if repoURL == "" {
		repoURL = urlparser.Parse(deriveRepoURL(name))
	}

	var types []string
	for _, l := range mod.Licenses {
		types = append(types, l.Types...)
	}

	return &core.Package{
		Name:          name,
		Repository:    repoURL,
		Homepage:      repoURL,
		Namespace:     namespaceFor(name),
		Licenses:      strings.Join(types, ", "),
		LatestVersion: mod.Version,
	}, nil
}

func (r *Registry) fetchPackageProxy(ctx context.Context, name string) (*core.Package, error) {
	encoded := encodeForProxy(name)

	listURL := fmt.Sprintf("%s/%s/@v/list", r.baseURL, encoded)
	body, err := r.client.GetText(ctx, listURL)
	if err != nil {
		if isNotFound(err) {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	if strings.TrimSpace(body) == "" {
		return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
	}

	repoURL := urlparser.Parse(deriveRepoURL(name))

	return &core.Package{
		Name:       name,
		Repository: repoURL,
		Homepage:   repoURL,
		Namespace:  namespaceFor(name),
	}, nil
}

func namespaceFor(name string) string {
	if i := strings.LastIndex(name, "/"); i > 0 {
		return name[:i]
	}
	return ""
}

func isNotFound(err error) bool {
	httpErr, ok := err.(*core.HTTPError)
	return ok && httpErr.IsNotFound()
}

func deriveRepoURL(modulePath string) string {
	// Common hosting platforms
	if strings.HasPrefix(modulePath, "github.com/") ||
		strings.HasPrefix(modulePath, "gitlab.com/") ||
		strings.HasPrefix(modulePath, "bitbucket.org/") {
		// Take the first 3 parts as the repo URL
		parts := strings.Split(modulePath, "/")
		if len(parts) >= 3 { //nolint:mnd // host/owner/repo
			return "https://" + strings.Join(parts[:3], "/")
		}
		return "https://" + modulePath
	}
	return "https://" + modulePath
}

func (r *Registry) FetchVersions(ctx context.Context, name string) ([]core.Version, error) {
	if r.pkgsiteURL != "" {
		if versions, err := r.fetchVersionsPkgsite(ctx, name); err == nil {
			return versions, nil
		}
	}
	return r.fetchVersionsProxy(ctx, name)
}

func (r *Registry) fetchVersionsPkgsite(ctx context.Context, name string) ([]core.Version, error) {
	var versions []core.Version
	token := ""

	for {
		q := url.Values{"limit": {fmt.Sprint(pkgsitePageLimit)}}
		if token != "" {
			q.Set("token", token)
		}
		pageURL := fmt.Sprintf("%s/versions/%s?%s", r.pkgsiteURL, name, q.Encode())

		var page pkgsiteVersions
		if err := r.client.GetJSON(ctx, pageURL, &page); err != nil {
			return nil, err
		}

		for _, v := range page.Items {
			status := core.StatusNone
			switch {
			case v.Retracted:
				status = core.StatusRetracted
			case v.Deprecated:
				status = core.StatusDeprecated
			}
			versions = append(versions, core.Version{
				Number:      v.Version,
				PublishedAt: v.CommitTime,
				Status:      status,
			})
		}

		if page.NextPageToken == "" {
			break
		}
		token = page.NextPageToken
	}

	return versions, nil
}

func (r *Registry) fetchVersionsProxy(ctx context.Context, name string) ([]core.Version, error) {
	encoded := encodeForProxy(name)
	listURL := fmt.Sprintf("%s/%s/@v/list", r.baseURL, encoded)

	body, err := r.client.GetText(ctx, listURL)
	if err != nil {
		if isNotFound(err) {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	lines := strings.Split(strings.TrimSpace(body), "\n")
	versions := make([]core.Version, 0, len(lines))

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Get version info for the timestamp
		infoURL := fmt.Sprintf("%s/%s/@v/%s.info", r.baseURL, encoded, line)
		var info versionInfo
		if err := r.client.GetJSON(ctx, infoURL, &info); err == nil {
			versions = append(versions, core.Version{
				Number:      info.Version,
				PublishedAt: info.Time,
			})
		} else {
			// If we can't get the info, just use the version number
			versions = append(versions, core.Version{
				Number: line,
			})
		}
	}

	return versions, nil
}

func (r *Registry) FetchDependencies(ctx context.Context, name, version string) ([]core.Dependency, error) {
	encoded := encodeForProxy(name)
	modURL := fmt.Sprintf("%s/%s/@v/%s.mod", r.baseURL, encoded, version)

	body, err := r.client.GetText(ctx, modURL)
	if err != nil {
		if isNotFound(err) {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name, Version: version}
		}
		return nil, err
	}

	return parseGoMod(body), nil
}

func parseGoMod(content string) []core.Dependency {
	var deps []core.Dependency
	lines := strings.Split(content, "\n")

	inRequire := false
	for _, line := range lines {
		line = strings.TrimSpace(line)

		if strings.HasPrefix(line, "require (") {
			inRequire = true
			continue
		}
		if inRequire && line == ")" {
			inRequire = false
			continue
		}

		if inRequire || strings.HasPrefix(line, "require ") {
			dep := parseRequireLine(line)
			if dep != nil {
				deps = append(deps, *dep)
			}
		}
	}

	return deps
}

func parseRequireLine(line string) *core.Dependency {
	line = strings.TrimPrefix(line, "require ")
	line = strings.TrimSpace(line)

	if line == "" || line == "(" || line == ")" {
		return nil
	}

	// Check for indirect before removing comment
	isIndirect := strings.Contains(line, "// indirect")

	// Remove comments
	if idx := strings.Index(line, "//"); idx != -1 {
		line = line[:idx]
	}
	line = strings.TrimSpace(line)

	if line == "" {
		return nil
	}

	parts := strings.Fields(line)
	if len(parts) < 2 { //nolint:mnd // require needs name + version
		return nil
	}

	name := parts[0]
	version := parts[1]

	scope := core.Runtime
	if isIndirect {
		scope = core.Optional
	}

	return &core.Dependency{
		Name:         name,
		Requirements: version,
		Scope:        scope,
		Optional:     isIndirect,
	}
}

func (r *Registry) FetchMaintainers(ctx context.Context, name string) ([]core.Maintainer, error) {
	// Go modules don't have a maintainer concept in the proxy protocol
	return nil, nil
}

type URLs struct {
	baseURL string
}

func (u *URLs) Registry(name, version string) string {
	if version != "" {
		return fmt.Sprintf("https://pkg.go.dev/%s@%s", name, version)
	}
	return fmt.Sprintf("https://pkg.go.dev/%s", name)
}

func (u *URLs) Download(name, version string) string {
	if version == "" {
		return ""
	}
	encoded := encodeForProxy(name)
	return fmt.Sprintf("%s/%s/@v/%s.zip", u.baseURL, encoded, version)
}

func (u *URLs) Documentation(name, version string) string {
	if version != "" {
		return fmt.Sprintf("https://pkg.go.dev/%s@%s#section-documentation", name, version)
	}
	return fmt.Sprintf("https://pkg.go.dev/%s#section-documentation", name)
}

func (u *URLs) PURL(name, version string) string {
	encoded := encodeForProxy(name)
	parts := strings.Split(name, "/")
	namespace := ""
	var pkgName string

	if len(parts) > 1 {
		namespace = strings.Join(parts[:len(parts)-1], "/")
		pkgName = parts[len(parts)-1]
		// Encode the namespace for PURL
		namespace = encodeForProxy(namespace)
		pkgName = encodeForProxy(pkgName)
	} else {
		pkgName = encoded
	}

	if namespace != "" {
		if version != "" {
			return fmt.Sprintf("pkg:golang/%s/%s@%s", namespace, pkgName, version)
		}
		return fmt.Sprintf("pkg:golang/%s/%s", namespace, pkgName)
	}

	if version != "" {
		return fmt.Sprintf("pkg:golang/%s@%s", pkgName, version)
	}
	return fmt.Sprintf("pkg:golang/%s", pkgName)
}

// LatestVersion fetches the latest version of a module.
func (r *Registry) LatestVersion(ctx context.Context, name string) (string, error) {
	encoded := encodeForProxy(name)
	latestURL := fmt.Sprintf("%s/%s/@latest", r.baseURL, encoded)

	body, err := r.client.GetBody(ctx, latestURL)
	if err != nil {
		return "", err
	}

	var info versionInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return "", err
	}

	return info.Version, nil
}
