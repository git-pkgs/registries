// Package swift provides a registry client for Swift package registries
// (SE-0292).
package swift

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/git-pkgs/registries/internal/core"
	"github.com/git-pkgs/registries/internal/urlparser"
	"github.com/git-pkgs/vers"
)

const (
	DefaultURL = ""
	ecosystem  = "swift"

	acceptHeader  = "application/vnd.swift.registry.v1+json"
	sourceArchive = "source-archive"
	maxPages      = 50
	maxScopeLen   = 39
	maxNameLen    = 100
)

// ErrInvalidName is returned for names that are not a registry scope and
// package name.
var ErrInvalidName = errors.New("invalid swift package name")

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
	if client == nil {
		client = core.DefaultClient()
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

// SplitName returns the registry scope and package name from "scope/name",
// the PURL namespace and name, or "scope.name", the registry identifier.
// A PURL namespace that is a source host such as "github.com/apple" has no
// defined mapping to a scope and is rejected.
func SplitName(name string) (scope, pkg string, err error) {
	var ok bool
	if strings.Contains(name, "/") {
		scope, pkg, ok = strings.Cut(name, "/")
		if strings.Contains(pkg, "/") {
			return "", "", fmt.Errorf("%w %q: the namespace must be a registry scope, not a source host path", ErrInvalidName, name)
		}
	} else {
		scope, pkg, ok = strings.Cut(name, ".")
	}
	if !ok || !validIdentifier(scope, maxScopeLen, "-") || !validIdentifier(pkg, maxNameLen, "-_") {
		return "", "", fmt.Errorf("%w %q: expected scope/name or scope.name", ErrInvalidName, name)
	}
	return scope, pkg, nil
}

// validIdentifier applies the registry rules for scopes and names:
// alphanumerics, with separators only between alphanumerics.
func validIdentifier(s string, maxLen int, separators string) bool {
	if s == "" || len(s) > maxLen {
		return false
	}
	prevSeparator := true
	for _, c := range s {
		switch {
		case c < 0x80 && (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'):
			prevSeparator = false
		case strings.ContainsRune(separators, c) && !prevSeparator:
			prevSeparator = true
		default:
			return false
		}
	}
	return !prevSeparator
}

type releasesResponse struct {
	Releases map[string]releaseEntry `json:"releases"`
}

type releaseEntry struct {
	URL     string   `json:"url"`
	Problem *problem `json:"problem"`
}

type problem struct {
	Status int    `json:"status"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

type releaseResponse struct {
	ID          string          `json:"id"`
	Version     string          `json:"version"`
	Resources   []resource      `json:"resources"`
	Metadata    releaseMetadata `json:"metadata"`
	PublishedAt string          `json:"publishedAt"`
}

type resource struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Checksum string `json:"checksum"`
}

type releaseMetadata struct {
	Author                  *author  `json:"author"`
	Description             string   `json:"description"`
	LicenseURL              string   `json:"licenseURL"`
	ReadmeURL               string   `json:"readmeURL"`
	RepositoryURLs          []string `json:"repositoryURLs"`
	OriginalPublicationTime string   `json:"originalPublicationTime"`
}

type author struct {
	Name         string        `json:"name"`
	Email        string        `json:"email"`
	URL          string        `json:"url"`
	Organization *organization `json:"organization"`
}

type organization struct {
	Name string `json:"name"`
}

func (r *Registry) get(ctx context.Context, rawURL string, v any) (http.Header, error) {
	return r.client.GetJSONWithHeaders(ctx, rawURL, http.Header{"Accept": {acceptHeader}}, v)
}

func isNotFound(err error) bool {
	var httpErr *core.HTTPError
	return errors.As(err, &httpErr) && (httpErr.StatusCode == http.StatusNotFound || httpErr.StatusCode == http.StatusGone)
}

// fetchReleases lists every release, following Link rel="next" pages on the
// registry's own host.
func (r *Registry) fetchReleases(ctx context.Context, name string) (map[string]releaseEntry, error) {
	scope, pkg, err := SplitName(name)
	if err != nil {
		return nil, err
	}

	releases := make(map[string]releaseEntry)
	next := fmt.Sprintf("%s/%s/%s", r.baseURL, scope, pkg)
	for page := 0; next != "" && page < maxPages; page++ {
		var resp releasesResponse
		header, err := r.get(ctx, next, &resp)
		if err != nil {
			if isNotFound(err) {
				return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
			}
			return nil, err
		}
		for v, entry := range resp.Releases {
			releases[v] = entry
		}
		next = r.nextPage(next, header)
	}
	if next != "" {
		return nil, fmt.Errorf("swift release listing for %q exceeds %d pages", name, maxPages)
	}
	return releases, nil
}

func (r *Registry) nextPage(current string, header http.Header) string {
	base, err := url.Parse(current)
	if err != nil {
		return ""
	}
	for _, link := range parseLinks(header.Values("Link")) {
		if link.rel != "next" {
			continue
		}
		u, err := base.Parse(link.url)
		if err != nil || u.Scheme != base.Scheme || u.Host != base.Host {
			return ""
		}
		return u.String()
	}
	return ""
}

type link struct {
	url string
	rel string
}

// parseLinks reads RFC 8288 Link header values, keeping the target and rel.
func parseLinks(values []string) []link {
	var links []link
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			target, params, _ := strings.Cut(strings.TrimSpace(part), ";")
			target = strings.TrimSpace(target)
			if !strings.HasPrefix(target, "<") || !strings.HasSuffix(target, ">") {
				continue
			}
			l := link{url: target[1 : len(target)-1]}
			for _, param := range strings.Split(params, ";") {
				key, val, _ := strings.Cut(strings.TrimSpace(param), "=")
				if strings.EqualFold(key, "rel") {
					l.rel = strings.Trim(val, `"`)
				}
			}
			links = append(links, l)
		}
	}
	return links
}

func (r *Registry) fetchRelease(ctx context.Context, name, version string) (*releaseResponse, error) {
	scope, pkg, err := SplitName(name)
	if err != nil {
		return nil, err
	}
	var resp releaseResponse
	if _, err := r.get(ctx, fmt.Sprintf("%s/%s/%s/%s", r.baseURL, scope, pkg, url.PathEscape(version)), &resp); err != nil {
		if isNotFound(err) {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name, Version: version}
		}
		return nil, err
	}
	return &resp, nil
}

// availableVersions returns releases without a problem, highest first.
func availableVersions(releases map[string]releaseEntry) []string {
	versions := make([]string, 0, len(releases))
	for v, entry := range releases {
		if entry.Problem == nil {
			versions = append(versions, v)
		}
	}
	sortVersions(versions)
	return versions
}

func sortVersions(versions []string) {
	sort.Slice(versions, func(i, j int) bool { return vers.Compare(versions[i], versions[j]) > 0 })
}

func (r *Registry) latestRelease(ctx context.Context, name string) (*releaseResponse, error) {
	releases, err := r.fetchReleases(ctx, name)
	if err != nil {
		return nil, err
	}
	versions := availableVersions(releases)
	if len(versions) == 0 {
		return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
	}
	return r.fetchRelease(ctx, name, versions[0])
}

func (r *Registry) FetchPackage(ctx context.Context, name string) (*core.Package, error) {
	release, err := r.latestRelease(ctx, name)
	if err != nil {
		return nil, err
	}
	scope, _, _ := SplitName(name)

	var repository string
	for _, u := range release.Metadata.RepositoryURLs {
		if repository = urlparser.Parse(u); repository != "" {
			break
		}
	}

	return &core.Package{
		Name:          release.ID,
		Description:   release.Metadata.Description,
		Repository:    repository,
		Namespace:     scope,
		LatestVersion: release.Version,
		Metadata: map[string]any{
			"license_url":     release.Metadata.LicenseURL,
			"readme_url":      release.Metadata.ReadmeURL,
			"repository_urls": release.Metadata.RepositoryURLs,
		},
	}, nil
}

// FetchVersions lists releases without fetching each one, so versions carry
// no publication time or checksum; FetchVersion returns those. Releases the
// registry reports a problem for are marked yanked.
func (r *Registry) FetchVersions(ctx context.Context, name string) ([]core.Version, error) {
	releases, err := r.fetchReleases(ctx, name)
	if err != nil {
		return nil, err
	}

	numbers := make([]string, 0, len(releases))
	for v := range releases {
		numbers = append(numbers, v)
	}
	sortVersions(numbers)

	versions := make([]core.Version, 0, len(numbers))
	for _, number := range numbers {
		v := core.Version{Number: number}
		if p := releases[number].Problem; p != nil {
			v.Status = core.StatusYanked
			v.Metadata = map[string]any{"problem_status": p.Status, "problem_detail": p.Detail}
		}
		versions = append(versions, v)
	}
	return versions, nil
}

// FetchVersion returns one release with its source archive checksum.
func (r *Registry) FetchVersion(ctx context.Context, name, version string) (*core.Version, error) {
	release, err := r.fetchRelease(ctx, name, version)
	if err != nil {
		return nil, err
	}

	published := release.Metadata.OriginalPublicationTime
	if published == "" {
		published = release.PublishedAt
	}
	publishedAt, _ := time.Parse(time.RFC3339, published)

	v := &core.Version{
		Number:      release.Version,
		PublishedAt: publishedAt,
		Metadata: map[string]any{
			"license_url": release.Metadata.LicenseURL,
		},
	}
	for _, res := range release.Resources {
		if res.Name != sourceArchive {
			continue
		}
		if res.Checksum != "" {
			v.Integrity = "sha256-" + res.Checksum
		}
		downloadURL := r.urls.Download(name, version)
		v.Artifacts = append(v.Artifacts, core.Artifact{
			URL:       downloadURL,
			Filename:  version + ".zip",
			Integrity: v.Integrity,
			MediaType: res.Type,
		})
	}
	return v, nil
}

// FetchDependencies returns nil: dependencies are declared in Package.swift,
// which the registry serves as Swift source rather than structured data.
func (r *Registry) FetchDependencies(ctx context.Context, name, version string) ([]core.Dependency, error) {
	return nil, nil
}

// FetchMaintainers returns the author of the latest release.
func (r *Registry) FetchMaintainers(ctx context.Context, name string) ([]core.Maintainer, error) {
	release, err := r.latestRelease(ctx, name)
	if err != nil {
		return nil, err
	}
	a := release.Metadata.Author
	if a == nil || a.Name == "" {
		return nil, nil
	}
	m := core.Maintainer{Name: a.Name, Email: a.Email, URL: a.URL}
	if a.Organization != nil {
		m.Role = a.Organization.Name
	}
	return []core.Maintainer{m}, nil
}

type URLs struct {
	baseURL string
}

func (u *URLs) Registry(name, version string) string {
	scope, pkg, err := SplitName(name)
	if err != nil {
		return ""
	}
	if version != "" {
		return fmt.Sprintf("%s/%s/%s/%s", u.baseURL, scope, pkg, url.PathEscape(version))
	}
	return fmt.Sprintf("%s/%s/%s", u.baseURL, scope, pkg)
}

func (u *URLs) Download(name, version string) string {
	scope, pkg, err := SplitName(name)
	if err != nil || version == "" {
		return ""
	}
	return fmt.Sprintf("%s/%s/%s/%s.zip", u.baseURL, scope, pkg, url.PathEscape(version))
}

func (u *URLs) Documentation(name, version string) string {
	return ""
}

func (u *URLs) PURL(name, version string) string {
	scope, pkg, err := SplitName(name)
	if err != nil {
		return ""
	}
	if version != "" {
		return fmt.Sprintf("pkg:swift/%s/%s@%s", scope, pkg, version)
	}
	return fmt.Sprintf("pkg:swift/%s/%s", scope, pkg)
}
