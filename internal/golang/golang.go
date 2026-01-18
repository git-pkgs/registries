// Package golang provides a registry client for the Go module proxy.
package golang

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/git-pkgs/registries/internal/core"
)

const (
	DefaultURL = "https://proxy.golang.org"
	ecosystem  = "golang"
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

type versionInfo struct {
	Version string    `json:"Version"`
	Time    time.Time `json:"Time"`
}

// encodeForProxy encodes a module path according to the goproxy protocol.
// Capital letters are replaced with "!" followed by the lowercase letter.
// https://go.dev/ref/mod#goproxy-protocol
func encodeForProxy(path string) string {
	var b strings.Builder
	for _, r := range path {
		if r >= 'A' && r <= 'Z' {
			b.WriteRune('!')
			b.WriteRune(r + 32) // lowercase
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (r *Registry) FetchPackage(ctx context.Context, name string) (*core.Package, error) {
	encoded := encodeForProxy(name)

	// Try to get the version list first to verify the module exists
	listURL := fmt.Sprintf("%s/%s/@v/list", r.baseURL, encoded)
	body, err := r.client.GetText(ctx, listURL)
	if err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	if strings.TrimSpace(body) == "" {
		return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
	}

	// Go modules don't have rich metadata in the proxy protocol
	// The repository URL is typically derived from the module path
	repoURL := deriveRepoURL(name)

	parts := strings.Split(name, "/")
	namespace := ""
	if len(parts) > 1 {
		namespace = strings.Join(parts[:len(parts)-1], "/")
	}

	return &core.Package{
		Name:       name,
		Repository: repoURL,
		Homepage:   repoURL,
		Namespace:  namespace,
	}, nil
}

func deriveRepoURL(modulePath string) string {
	// Common hosting platforms
	if strings.HasPrefix(modulePath, "github.com/") ||
		strings.HasPrefix(modulePath, "gitlab.com/") ||
		strings.HasPrefix(modulePath, "bitbucket.org/") {
		// Take the first 3 parts as the repo URL
		parts := strings.Split(modulePath, "/")
		if len(parts) >= 3 {
			return "https://" + strings.Join(parts[:3], "/")
		}
		return "https://" + modulePath
	}
	return "https://" + modulePath
}

func (r *Registry) FetchVersions(ctx context.Context, name string) ([]core.Version, error) {
	encoded := encodeForProxy(name)
	listURL := fmt.Sprintf("%s/%s/@v/list", r.baseURL, encoded)

	body, err := r.client.GetText(ctx, listURL)
	if err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
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
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
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
	if len(parts) < 2 {
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
	pkgName := name

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
