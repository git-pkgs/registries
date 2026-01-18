// Package pypi provides a registry client for pypi.org.
package pypi

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/git-pkgs/registries/internal/core"
)

const (
	DefaultURL = "https://pypi.org"
	ecosystem  = "pypi"
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
	Info     infoBlock                  `json:"info"`
	Releases map[string][]releaseFile   `json:"releases"`
}

type infoBlock struct {
	Name              string            `json:"name"`
	Summary           string            `json:"summary"`
	Description       string            `json:"description"`
	HomePage          string            `json:"home_page"`
	License           string            `json:"license"`
	LicenseExpression string            `json:"license_expression"`
	Keywords          string            `json:"keywords"`
	Version           string            `json:"version"`
	Classifiers       []string          `json:"classifiers"`
	ProjectURLs       map[string]string `json:"project_urls"`
	RequiresDist      []string          `json:"requires_dist"`
	RequiresPython    string            `json:"requires_python"`
}

type releaseFile struct {
	Digests         map[string]string `json:"digests"`
	URL             string            `json:"url"`
	UploadTime      string            `json:"upload_time"`
	Yanked          bool              `json:"yanked"`
	YankedReason    string            `json:"yanked_reason"`
	PackageType     string            `json:"packagetype"`
	PythonVersion   string            `json:"python_version"`
	RequiresPython  string            `json:"requires_python"`
	Size            int               `json:"size"`
}

type versionInfoResponse struct {
	Info infoBlock `json:"info"`
}

func (r *Registry) FetchPackage(ctx context.Context, name string) (*core.Package, error) {
	url := fmt.Sprintf("%s/pypi/%s/json", r.baseURL, name)

	var resp packageResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	repoURL := extractRepoURL(resp.Info.ProjectURLs, resp.Info.HomePage)
	homepage := extractHomepage(resp.Info.ProjectURLs, resp.Info.HomePage)

	return &core.Package{
		Name:        strings.ToLower(resp.Info.Name),
		Description: resp.Info.Summary,
		Homepage:    homepage,
		Repository:  repoURL,
		Licenses:    extractLicense(resp.Info),
		Keywords:    parseKeywords(resp.Info.Keywords),
		Metadata: map[string]any{
			"classifiers":      resp.Info.Classifiers,
			"documentation":    resp.Info.ProjectURLs["Documentation"],
			"normalized_name":  normalizeName(resp.Info.Name),
		},
	}, nil
}

func extractRepoURL(projectURLs map[string]string, homePage string) string {
	priorityKeys := []string{"Repository", "Source", "Source Code", "Code"}
	for _, key := range priorityKeys {
		if url, ok := projectURLs[key]; ok && url != "" {
			if isRepoURL(url) {
				return url
			}
		}
	}

	for _, url := range projectURLs {
		if isRepoURL(url) && !strings.Contains(url, "github.com/sponsors") {
			return url
		}
	}

	if isRepoURL(homePage) {
		return homePage
	}

	return ""
}

func extractHomepage(projectURLs map[string]string, homePage string) string {
	if homePage != "" {
		return homePage
	}
	if url, ok := projectURLs["Homepage"]; ok {
		return url
	}
	if url, ok := projectURLs["Home"]; ok {
		return url
	}
	return ""
}

func isRepoURL(url string) bool {
	return strings.Contains(url, "github.com") ||
		strings.Contains(url, "gitlab.com") ||
		strings.Contains(url, "bitbucket.org") ||
		strings.Contains(url, "codeberg.org")
}

func extractLicense(info infoBlock) string {
	if info.LicenseExpression != "" {
		return info.LicenseExpression
	}
	if info.License != "" {
		return info.License
	}

	for _, classifier := range info.Classifiers {
		if strings.HasPrefix(classifier, "License :: ") {
			parts := strings.Split(classifier, " :: ")
			if len(parts) > 0 {
				return parts[len(parts)-1]
			}
		}
	}

	return ""
}

func parseKeywords(keywords string) []string {
	if keywords == "" {
		return nil
	}
	if strings.Contains(keywords, ",") {
		parts := strings.Split(keywords, ",")
		result := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				result = append(result, p)
			}
		}
		return result
	}
	return strings.Fields(keywords)
}

func normalizeName(name string) string {
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, "_", "-")
	name = strings.ReplaceAll(name, ".", "-")
	return name
}

func (r *Registry) FetchVersions(ctx context.Context, name string) ([]core.Version, error) {
	url := fmt.Sprintf("%s/pypi/%s/json", r.baseURL, name)

	var resp packageResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	versions := make([]core.Version, 0, len(resp.Releases))
	for num, files := range resp.Releases {
		if len(files) == 0 {
			versions = append(versions, core.Version{
				Number: num,
			})
			continue
		}

		file := files[0]
		var publishedAt time.Time
		if file.UploadTime != "" {
			publishedAt, _ = time.Parse("2006-01-02T15:04:05", file.UploadTime)
		}

		var status core.VersionStatus
		if file.Yanked {
			status = core.StatusYanked
		}

		var integrity string
		if sha256, ok := file.Digests["sha256"]; ok {
			integrity = "sha256-" + sha256
		}

		versions = append(versions, core.Version{
			Number:      num,
			PublishedAt: publishedAt,
			Integrity:   integrity,
			Status:      status,
			Metadata: map[string]any{
				"download_url":    file.URL,
				"requires_python": file.RequiresPython,
				"yanked_reason":   file.YankedReason,
				"packagetype":     file.PackageType,
				"size":            file.Size,
			},
		})
	}

	return versions, nil
}

var pep508NameRegex = regexp.MustCompile(`^([A-Za-z0-9][-A-Za-z0-9._]*[A-Za-z0-9]|[A-Za-z0-9])(\s*\[.*?\])?`)

func (r *Registry) FetchDependencies(ctx context.Context, name, version string) ([]core.Dependency, error) {
	url := fmt.Sprintf("%s/pypi/%s/%s/json", r.baseURL, name, version)

	var resp versionInfoResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name, Version: version}
		}
		return nil, err
	}

	if len(resp.Info.RequiresDist) == 0 {
		return nil, nil
	}

	deps := make([]core.Dependency, 0, len(resp.Info.RequiresDist))
	for _, req := range resp.Info.RequiresDist {
		depName, requirements, envMarker := parsePEP508(req)

		scope := core.Runtime
		optional := false
		if envMarker != "" {
			scope = core.Scope(envMarker)
			optional = true
		}

		deps = append(deps, core.Dependency{
			Name:         depName,
			Requirements: requirements,
			Scope:        scope,
			Optional:     optional,
		})
	}

	return deps, nil
}

func parsePEP508(dep string) (name, requirements, envMarker string) {
	// Split on ; first to get environment markers
	parts := strings.SplitN(dep, ";", 2)
	nameAndVersion := strings.TrimSpace(parts[0])
	if len(parts) > 1 {
		envMarker = strings.TrimSpace(parts[1])
	}

	// Extract name and version
	match := pep508NameRegex.FindStringSubmatch(nameAndVersion)
	if match != nil {
		name = strings.TrimSpace(match[1])
		requirements = strings.TrimSpace(nameAndVersion[len(match[0]):])
		// Remove parentheses from version spec
		requirements = strings.Trim(requirements, "()")
		requirements = strings.TrimSpace(requirements)
	} else {
		name = nameAndVersion
	}

	// Remove extras brackets from name
	if idx := strings.Index(name, "["); idx != -1 {
		name = name[:idx]
	}

	if requirements == "" {
		requirements = "*"
	}

	return
}

func (r *Registry) FetchMaintainers(ctx context.Context, name string) ([]core.Maintainer, error) {
	// PyPI doesn't expose maintainers through JSON API
	// Would require scraping or XML-RPC
	return nil, nil
}

type URLs struct {
	baseURL string
}

func (u *URLs) Registry(name, version string) string {
	if version != "" {
		return fmt.Sprintf("%s/project/%s/%s/", u.baseURL, name, version)
	}
	return fmt.Sprintf("%s/project/%s/", u.baseURL, name)
}

func (u *URLs) Download(name, version string) string {
	// PyPI downloads are version-specific and stored in metadata
	return ""
}

func (u *URLs) Documentation(name, version string) string {
	if version != "" {
		return fmt.Sprintf("https://%s.readthedocs.io/en/%s/", name, version)
	}
	return fmt.Sprintf("https://%s.readthedocs.io/", name)
}

func (u *URLs) PURL(name, version string) string {
	normalized := normalizeName(name)
	if version != "" {
		return fmt.Sprintf("pkg:pypi/%s@%s", normalized, version)
	}
	return fmt.Sprintf("pkg:pypi/%s", normalized)
}
