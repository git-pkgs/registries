// Package helm provides a registry client for Helm HTTP repositories.
package helm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/git-pkgs/registries/internal/core"
	"github.com/git-pkgs/vers"
	"go.yaml.in/yaml/v3"
)

const (
	DefaultURL = ""
	ecosystem  = "helm"
)

func init() {
	core.Register(ecosystem, DefaultURL, func(baseURL string, client *core.Client) core.Registry {
		return New(baseURL, client)
	})
}

type Registry struct {
	indexURL string
	client   *core.Client
	urls     *URLs
}

func New(baseURL string, client *core.Client) *Registry {
	if client == nil {
		client = core.DefaultClient()
	}

	indexURL := buildIndexURL(baseURL)
	return &Registry{
		indexURL: indexURL,
		client:   client,
		urls:     &URLs{indexURL: indexURL, downloads: make(map[string]map[string]string)},
	}
}

func (r *Registry) Ecosystem() string {
	return ecosystem
}

func (r *Registry) URLs() core.URLBuilder { //nolint:ireturn
	return r.urls
}

type indexFile struct {
	APIVersion  string                    `yaml:"apiVersion"`
	Generated   time.Time                 `yaml:"generated"`
	Entries     map[string][]chartVersion `yaml:"entries"`
	PublicKeys  []string                  `yaml:"publicKeys"`
	Annotations map[string]string         `yaml:"annotations"`
}

type chartVersion struct {
	Name         string            `json:"name,omitempty" yaml:"name"`
	Home         string            `json:"home,omitempty" yaml:"home"`
	Sources      []string          `json:"sources,omitempty" yaml:"sources"`
	Version      string            `json:"version,omitempty" yaml:"version"`
	Description  string            `json:"description,omitempty" yaml:"description"`
	Keywords     []string          `json:"keywords,omitempty" yaml:"keywords"`
	Maintainers  []maintainerInfo  `json:"maintainers,omitempty" yaml:"maintainers"`
	Icon         string            `json:"icon,omitempty" yaml:"icon"`
	APIVersion   string            `json:"apiVersion,omitempty" yaml:"apiVersion"`
	Condition    string            `json:"condition,omitempty" yaml:"condition"`
	Tags         string            `json:"tags,omitempty" yaml:"tags"`
	AppVersion   string            `json:"appVersion,omitempty" yaml:"appVersion"`
	Deprecated   bool              `json:"deprecated,omitempty" yaml:"deprecated"`
	Annotations  map[string]string `json:"annotations,omitempty" yaml:"annotations"`
	KubeVersion  string            `json:"kubeVersion,omitempty" yaml:"kubeVersion"`
	Dependencies []dependencyInfo  `json:"dependencies,omitempty" yaml:"dependencies"`
	Type         string            `json:"type,omitempty" yaml:"type"`
	URLs         []string          `json:"urls,omitempty" yaml:"urls"`
	Created      time.Time         `json:"created,omitempty" yaml:"created"`
	Removed      bool              `json:"removed,omitempty" yaml:"removed"`
	Digest       string            `json:"digest,omitempty" yaml:"digest"`
	Checksum     string            `json:"checksum,omitempty" yaml:"checksum"`
	resolvedURLs []string
}

type maintainerInfo struct {
	Name  string `json:"name,omitempty" yaml:"name"`
	Email string `json:"email,omitempty" yaml:"email"`
	URL   string `json:"url,omitempty" yaml:"url"`
}

type dependencyInfo struct {
	Name         string   `json:"name" yaml:"name"`
	Version      string   `json:"version,omitempty" yaml:"version"`
	Repository   string   `json:"repository,omitempty" yaml:"repository"`
	Condition    string   `json:"condition,omitempty" yaml:"condition"`
	Tags         []string `json:"tags,omitempty" yaml:"tags"`
	Enabled      bool     `json:"enabled,omitempty" yaml:"enabled"`
	ImportValues []any    `json:"import-values,omitempty" yaml:"import-values"`
	Alias        string   `json:"alias,omitempty" yaml:"alias"`
}

func (r *Registry) FetchPackage(ctx context.Context, name string) (*core.Package, error) {
	versions, err := r.fetchChart(ctx, name)
	if err != nil {
		return nil, err
	}

	entry, latestVersion := packageEntry(versions)
	packageName := entry.Name
	if packageName == "" {
		packageName = name
	}

	return &core.Package{
		Name:          packageName,
		Description:   entry.Description,
		Homepage:      entry.Home,
		Repository:    core.ExtractRepoURL(entry.Sources),
		Keywords:      entry.Keywords,
		LatestVersion: latestVersion,
		Metadata:      packageMetadata(entry),
	}, nil
}

func (r *Registry) FetchVersions(ctx context.Context, name string) ([]core.Version, error) {
	entries, err := r.fetchChart(ctx, name)
	if err != nil {
		return nil, err
	}

	versions := make([]core.Version, 0, len(entries))
	for _, entry := range entries {
		metadata := map[string]any{
			"urls": entry.resolvedURLs,
		}
		if len(entry.resolvedURLs) > 0 {
			metadata["download_url"] = entry.resolvedURLs[0]
		}
		if len(entry.Dependencies) > 0 {
			metadata["dependencies"] = entry.Dependencies
		}

		versions = append(versions, core.Version{
			Number:      entry.Version,
			PublishedAt: entry.Created,
			Integrity:   formatIntegrity(entry.Digest),
			Status:      versionStatus(entry),
			Metadata:    metadata,
		})
	}

	return versions, nil
}

func (r *Registry) FetchDependencies(ctx context.Context, name, version string) ([]core.Dependency, error) {
	entries, err := r.fetchChart(ctx, name)
	if err != nil {
		return nil, err
	}

	entry := findVersion(entries, version)
	if entry == nil {
		return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name, Version: version}
	}

	dependencies := make([]core.Dependency, len(entry.Dependencies))
	for i, dependency := range entry.Dependencies {
		dependencies[i] = core.Dependency{
			Name:         dependency.Name,
			Requirements: dependency.Version,
			Scope:        core.Runtime,
		}
	}

	return dependencies, nil
}

func (r *Registry) FetchMaintainers(ctx context.Context, name string) ([]core.Maintainer, error) {
	versions, err := r.fetchChart(ctx, name)
	if err != nil {
		return nil, err
	}

	entry, _ := packageEntry(versions)
	maintainers := make([]core.Maintainer, len(entry.Maintainers))
	for i, maintainer := range entry.Maintainers {
		maintainers[i] = core.Maintainer{
			Name:  maintainer.Name,
			Email: maintainer.Email,
			URL:   maintainer.URL,
		}
	}

	return maintainers, nil
}

func (r *Registry) fetchChart(ctx context.Context, name string) ([]chartVersion, error) {
	index, err := r.fetchIndex(ctx)
	if err != nil {
		return nil, err
	}

	versions, ok := index.Entries[name]
	if !ok || len(versions) == 0 {
		return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
	}
	return versions, nil
}

func (r *Registry) fetchIndex(ctx context.Context) (*indexFile, error) {
	body, err := r.client.GetBody(ctx, r.indexURL)
	if err != nil {
		return nil, err
	}

	var index indexFile
	if err := yaml.Unmarshal(body, &index); err != nil {
		return nil, fmt.Errorf("parsing Helm index: %w", err)
	}
	if index.APIVersion == "" {
		return nil, fmt.Errorf("parsing Helm index: missing apiVersion")
	}
	if index.Entries == nil {
		return nil, fmt.Errorf("parsing Helm index: missing entries")
	}

	downloads := make(map[string]map[string]string, len(index.Entries))
	for name, versions := range index.Entries {
		packageDownloads := make(map[string]string, len(versions))
		for i := range versions {
			resolved, err := resolveChartURLs(r.indexURL, versions[i].URLs)
			if err != nil {
				return nil, fmt.Errorf("resolving Helm chart URLs for %s %s: %w", name, versions[i].Version, err)
			}
			versions[i].resolvedURLs = resolved
			if len(resolved) > 0 {
				if _, exists := packageDownloads[versions[i].Version]; !exists {
					packageDownloads[versions[i].Version] = resolved[0]
				}
			}
		}
		index.Entries[name] = versions
		downloads[name] = packageDownloads
	}
	r.urls.setDownloads(downloads)

	return &index, nil
}

func buildIndexURL(baseURL string) string {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return strings.TrimSuffix(baseURL, "/") + "/index.yaml"
	}
	if path.Base(parsed.Path) != "index.yaml" {
		parsed.Path = strings.TrimSuffix(parsed.Path, "/") + "/index.yaml"
		parsed.RawPath = ""
	}
	return parsed.String()
}

func resolveChartURLs(indexURL string, chartURLs []string) ([]string, error) {
	base, err := url.Parse(indexURL)
	if err != nil {
		return nil, err
	}

	resolved := make([]string, 0, len(chartURLs))
	for _, chartURL := range chartURLs {
		if chartURL == "" {
			continue
		}
		reference, err := url.Parse(chartURL)
		if err != nil {
			continue
		}
		resolved = append(resolved, base.ResolveReference(reference).String())
	}
	return resolved, nil
}

func packageEntry(versions []chartVersion) (*chartVersion, string) {
	var latest *chartVersion
	for i := range versions {
		entry := &versions[i]
		if entry.Deprecated || entry.Removed || entry.Version == "" {
			continue
		}
		if latest == nil || vers.Compare(entry.Version, latest.Version) > 0 {
			latest = entry
		}
	}
	if latest != nil {
		return latest, latest.Version
	}
	return &versions[0], ""
}

func packageMetadata(entry *chartVersion) map[string]any {
	metadata := make(map[string]any)
	if len(entry.Sources) > 0 {
		metadata["sources"] = entry.Sources
	}
	if len(entry.Maintainers) > 0 {
		metadata["maintainers"] = entry.Maintainers
	}
	if entry.Icon != "" {
		metadata["icon"] = entry.Icon
	}
	if entry.APIVersion != "" {
		metadata["apiVersion"] = entry.APIVersion
	}
	if entry.Condition != "" {
		metadata["condition"] = entry.Condition
	}
	if entry.Tags != "" {
		metadata["tags"] = entry.Tags
	}
	if entry.AppVersion != "" {
		metadata["appVersion"] = entry.AppVersion
	}
	if entry.Deprecated {
		metadata["deprecated"] = true
	}
	if len(entry.Annotations) > 0 {
		metadata["annotations"] = entry.Annotations
	}
	if entry.KubeVersion != "" {
		metadata["kubeVersion"] = entry.KubeVersion
	}
	if len(entry.Dependencies) > 0 {
		metadata["dependencies"] = entry.Dependencies
	}
	if entry.Type != "" {
		metadata["type"] = entry.Type
	}
	return metadata
}

func findVersion(versions []chartVersion, version string) *chartVersion {
	for i := range versions {
		if versions[i].Version == version {
			return &versions[i]
		}
	}
	return nil
}

func versionStatus(entry chartVersion) core.VersionStatus {
	if entry.Removed {
		return core.StatusYanked
	}
	if entry.Deprecated {
		return core.StatusDeprecated
	}
	return core.StatusNone
}

func formatIntegrity(digest string) string {
	if value, ok := strings.CutPrefix(digest, "sha256:"); ok {
		return "sha256-" + value
	}
	if decoded, err := hex.DecodeString(digest); err == nil && len(decoded) == sha256.Size {
		return "sha256-" + digest
	}
	return digest
}

type URLs struct {
	indexURL  string
	mu        sync.RWMutex
	downloads map[string]map[string]string
}

func (u *URLs) Registry(name, version string) string {
	return u.indexURL
}

func (u *URLs) Download(name, version string) string {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.downloads[name][version]
}

func (u *URLs) Documentation(name, version string) string {
	return ""
}

func (u *URLs) PURL(name, version string) string {
	if version != "" {
		return fmt.Sprintf("pkg:helm/%s@%s", name, version)
	}
	return fmt.Sprintf("pkg:helm/%s", name)
}

func (u *URLs) setDownloads(downloads map[string]map[string]string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.downloads = downloads
}
