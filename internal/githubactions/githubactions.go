// Package githubactions provides a registry client for GitHub Actions.
//
// Actions are git repositories on GitHub, so package metadata and tags come
// from the GitHub REST API. Unauthenticated requests are limited to 60 per
// hour. Subdirectory actions such as "github/codeql-action/init" share the
// repository of their first two path segments.
package githubactions

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/git-pkgs/registries/internal/core"
	"github.com/git-pkgs/registries/internal/urlparser"
)

const (
	DefaultURL = "https://api.github.com"
	ecosystem  = "githubactions"

	defaultWebURL = "https://github.com"
	enterpriseAPI = "/api/v3"
	mediaType     = "application/vnd.github+json"
	tagsPerPage   = 100
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

// New creates a GitHub Actions registry client. baseURL is the GitHub REST
// API root; a GitHub Enterprise Server root ending in /api/v3 also sets the
// web URL used for repository pages and archive downloads.
func New(baseURL string, client *core.Client) *Registry {
	if baseURL == "" {
		baseURL = DefaultURL
	}
	r := &Registry{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		client:  client,
	}
	r.urls = &URLs{webURL: webURLFor(r.baseURL)}
	return r
}

// webURLFor maps an API root to the matching web root.
func webURLFor(apiURL string) string {
	if apiURL == DefaultURL {
		return defaultWebURL
	}
	return strings.TrimSuffix(apiURL, enterpriseAPI)
}

func (r *Registry) Ecosystem() string {
	return ecosystem
}

func (r *Registry) URLs() core.URLBuilder { //nolint:ireturn
	return r.urls
}

// actionName is an action reference split into its repository and the
// optional subdirectory holding the action.
type actionName struct {
	owner string
	repo  string
	path  string
}

// parseActionName splits "owner/repo" or "owner/repo/sub/dir".
func parseActionName(name string) (actionName, bool) {
	parts := strings.SplitN(strings.Trim(name, "/"), "/", 3) //nolint:mnd // owner/repo/path
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {  //nolint:mnd
		return actionName{}, false
	}
	a := actionName{owner: parts[0], repo: parts[1]}
	if len(parts) == 3 { //nolint:mnd
		a.path = parts[2]
	}
	return a, true
}

func invalidNameError(name string) error {
	return fmt.Errorf("githubactions name must be in format 'owner/repo' or 'owner/repo/path', got %q", name)
}

type repoResponse struct {
	FullName      string       `json:"full_name"`
	Description   string       `json:"description"`
	Homepage      string       `json:"homepage"`
	HTMLURL       string       `json:"html_url"`
	DefaultBranch string       `json:"default_branch"`
	Archived      bool         `json:"archived"`
	Topics        []string     `json:"topics"`
	License       *licenseInfo `json:"license"`
}

type licenseInfo struct {
	SPDXID string `json:"spdx_id"`
}

type tagResponse struct {
	Name   string `json:"name"`
	Commit struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}

func (r *Registry) getJSON(ctx context.Context, endpoint string, v any) error {
	_, err := r.client.GetJSONWithHeaders(ctx, endpoint, http.Header{"Accept": {mediaType}}, v)
	return err
}

func (r *Registry) repoEndpoint(a actionName) string {
	return fmt.Sprintf("%s/repos/%s/%s", r.baseURL, url.PathEscape(a.owner), url.PathEscape(a.repo))
}

func (r *Registry) FetchPackage(ctx context.Context, name string) (*core.Package, error) {
	a, ok := parseActionName(name)
	if !ok {
		return nil, invalidNameError(name)
	}

	var resp repoResponse
	if err := r.getJSON(ctx, r.repoEndpoint(a), &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	repository := urlparser.Parse(resp.HTMLURL)
	if repository == "" {
		repository = resp.HTMLURL
	}
	homepage := resp.Homepage
	if homepage == "" {
		homepage = resp.HTMLURL
	}

	var license string
	if resp.License != nil && resp.License.SPDXID != "NOASSERTION" {
		license = resp.License.SPDXID
	}

	return &core.Package{
		Name:        name,
		Description: resp.Description,
		Homepage:    homepage,
		Repository:  repository,
		Licenses:    license,
		Keywords:    resp.Topics,
		Namespace:   a.owner,
		Metadata: map[string]any{
			"full_name":      resp.FullName,
			"default_branch": resp.DefaultBranch,
			"archived":       resp.Archived,
			"path":           a.path,
		},
	}, nil
}

// FetchVersions lists the repository's tags. Branches and commit SHAs are
// valid refs for an action but are not enumerated. Integrity is left empty
// because GitHub does not guarantee archive tarballs are byte-stable.
func (r *Registry) FetchVersions(ctx context.Context, name string) ([]core.Version, error) {
	a, ok := parseActionName(name)
	if !ok {
		return nil, invalidNameError(name)
	}

	var versions []core.Version
	for page := 1; ; page++ {
		endpoint := fmt.Sprintf("%s/tags?per_page=%d&page=%d", r.repoEndpoint(a), tagsPerPage, page)
		var tags []tagResponse
		if err := r.getJSON(ctx, endpoint, &tags); err != nil {
			if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
				return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
			}
			return nil, err
		}

		for _, tag := range tags {
			versions = append(versions, core.Version{
				Number: tag.Name,
				Metadata: map[string]any{
					"commit": tag.Commit.SHA,
				},
			})
		}

		if len(tags) < tagsPerPage {
			return versions, nil
		}
	}
}

func (r *Registry) FetchDependencies(_ context.Context, _, _ string) ([]core.Dependency, error) {
	return nil, fmt.Errorf("githubactions does not expose structured dependencies: %w", errors.ErrUnsupported)
}

func (r *Registry) FetchMaintainers(_ context.Context, _ string) ([]core.Maintainer, error) {
	return nil, fmt.Errorf("githubactions does not expose maintainers: %w", errors.ErrUnsupported)
}

type URLs struct {
	webURL string
}

// escapeRef escapes each segment of a git ref so refs containing slashes,
// such as "releases/v1", keep their path structure.
func escapeRef(ref string) string {
	segments := strings.Split(ref, "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	return strings.Join(segments, "/")
}

func (u *URLs) repoURL(a actionName) string {
	return fmt.Sprintf("%s/%s/%s", u.webURL, a.owner, a.repo)
}

// Registry returns the repository page, at the given ref and subdirectory
// when either is set.
func (u *URLs) Registry(name, version string) string {
	a, ok := parseActionName(name)
	if !ok {
		return ""
	}
	if version == "" && a.path == "" {
		return u.repoURL(a)
	}
	ref := "HEAD"
	if version != "" {
		ref = escapeRef(version)
	}
	link := fmt.Sprintf("%s/tree/%s", u.repoURL(a), ref)
	if a.path != "" {
		link += "/" + a.path
	}
	return link
}

// Download returns the source tarball for a tag, branch or commit SHA. The
// tarball covers the whole repository, including for subdirectory actions.
func (u *URLs) Download(name, version string) string {
	a, ok := parseActionName(name)
	if !ok || version == "" {
		return ""
	}
	return fmt.Sprintf("%s/archive/%s.tar.gz", u.repoURL(a), escapeRef(version))
}

func (u *URLs) Documentation(name, version string) string {
	return u.Registry(name, version)
}

func (u *URLs) PURL(name, version string) string {
	a, ok := parseActionName(name)
	if !ok {
		return ""
	}
	p := fmt.Sprintf("pkg:%s/%s/%s", ecosystem, a.owner, a.repo)
	if a.path != "" {
		p += "/" + a.path
	}
	if version != "" {
		p += "@" + url.PathEscape(version)
	}
	return p
}
