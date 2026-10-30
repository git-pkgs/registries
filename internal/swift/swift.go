package swift

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"

	"github.com/git-pkgs/registries/internal/core"
	"github.com/git-pkgs/registries/internal/urlparser"
	"github.com/git-pkgs/vers"
)

const (
	ecosystem = "swift"
	mediaType = "application/vnd.swift.registry.v1+json"
)

func init() {
	core.Register(ecosystem, "", func(baseURL string, client *core.Client) core.Registry {
		return &Registry{client: client, urls: &URLs{baseURL: baseURL}}
	})
}

type Registry struct {
	client *core.Client
	urls   *URLs
}

func (r *Registry) Ecosystem() string { return ecosystem }

func (r *Registry) URLs() core.URLBuilder { //nolint:ireturn
	return r.urls
}

type listedRelease struct {
	URL     string         `json:"url"`
	Problem map[string]any `json:"problem"`
}

type listing struct {
	releases map[string]listedRelease
	links    map[string][]string
}

func (r *Registry) getJSON(ctx context.Context, endpoint, name, version string, dest any) (http.Header, error) {
	headers, err := r.client.GetJSONWithHeaders(ctx, endpoint, http.Header{"Accept": {mediaType}}, dest)
	var httpErr *core.HTTPError
	if errors.As(err, &httpErr) && httpErr.IsNotFound() {
		return headers, &core.NotFoundError{Ecosystem: ecosystem, Name: name, Version: version}
	}
	if err == nil && headers.Get("Content-Version") != "" && headers.Get("Content-Version") != "1" {
		return headers, fmt.Errorf("unsupported Swift registry Content-Version %q", headers.Get("Content-Version"))
	}
	return headers, err
}

func (r *Registry) list(ctx context.Context, name string) (*listing, error) {
	page, err := r.urls.endpoint(name, "")
	if err != nil {
		return nil, err
	}
	result := &listing{releases: make(map[string]listedRelease), links: make(map[string][]string)}
	seen := make(map[string]bool)
	for page != "" {
		if seen[page] {
			return nil, fmt.Errorf("swift registry pagination loop at %s", page)
		}
		if len(seen) == core.MaxPages {
			return nil, fmt.Errorf("%w: swift registry listing for %s", core.ErrTooManyPages, name)
		}
		seen[page] = true
		var response struct {
			Releases map[string]listedRelease `json:"releases"`
		}
		headers, err := r.getJSON(ctx, page, name, "", &response)
		if err != nil {
			return nil, err
		}
		if response.Releases == nil {
			return nil, fmt.Errorf("swift registry response has no releases object")
		}
		for number, release := range response.Releases {
			if number == "" {
				return nil, fmt.Errorf("swift registry returned an empty release version")
			}
			release.URL, err = r.releaseEndpoint(page, name, number, release.URL)
			if err != nil {
				return nil, err
			}
			result.releases[number] = release
		}
		links := responseLinks(headers)
		for rel, values := range links {
			result.links[rel] = append(result.links[rel], values...)
		}
		page, err = nextPage(page, links)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (r *Registry) releaseEndpoint(page, name, number, link string) (string, error) {
	endpoint, err := r.urls.endpoint(name, number)
	if err != nil || link == "" {
		return endpoint, err
	}
	endpoint, err = resolveLink(page, link)
	if err != nil {
		return "", err
	}
	if !sameOrigin(page, endpoint) {
		return "", fmt.Errorf("swift registry release %s is on another origin", number)
	}
	return endpoint, nil
}

func (l *listing) numbers() []string {
	numbers := make([]string, 0, len(l.releases))
	for number := range l.releases {
		numbers = append(numbers, number)
	}
	sort.Slice(numbers, func(i, j int) bool {
		if comparison := vers.CompareWithScheme(numbers[i], numbers[j], "semver"); comparison != 0 {
			return comparison > 0
		}
		return numbers[i] < numbers[j]
	})
	return numbers
}

// latest prefers the registry's latest-version link, then the highest
// available stable release, then the highest available prerelease.
func (l *listing) latest() string {
	for _, link := range l.links["latest-version"] {
		if ref, err := url.Parse(link); err == nil {
			number := path.Base(ref.Path)
			if release, ok := l.releases[number]; ok && release.Problem == nil {
				return number
			}
		}
	}
	var prerelease string
	for _, number := range l.numbers() {
		if l.releases[number].Problem != nil {
			continue
		}
		if info, err := vers.ParseVersion(number); err == nil && !info.IsPrerelease() {
			return number
		}
		if prerelease == "" {
			prerelease = number
		}
	}
	return prerelease
}

func (r *Registry) FetchPackage(ctx context.Context, name string) (*core.Package, error) {
	list, err := r.list(ctx, name)
	if err != nil {
		return nil, err
	}
	identity := strings.ToLower(name)
	scope, _, _ := strings.Cut(identity, ".")
	pkg := &core.Package{Name: identity, Namespace: scope}
	for _, link := range list.links["canonical"] {
		if repository := urlparser.CanonicalURL(link); repository != "" {
			pkg.Repository = repository
			break
		}
	}
	if number := list.latest(); number != "" {
		info, err := r.release(ctx, name, number, list.releases[number].URL)
		if err != nil {
			return nil, err
		}
		pkg.LatestVersion = number
		pkg.Metadata = info.Metadata
		info.populatePackage(pkg)
	}
	return pkg, nil
}

func (r *Registry) FetchVersions(ctx context.Context, name string) ([]core.Version, error) {
	list, err := r.list(ctx, name)
	if err != nil {
		return nil, err
	}
	versions := make([]core.Version, 0, len(list.releases))
	for _, number := range list.numbers() {
		release := list.releases[number]
		if release.Problem != nil {
			versions = append(versions, core.Version{
				Number: number, Status: core.StatusRetracted,
				Metadata: map[string]any{"problem": release.Problem},
			})
			continue
		}
		info, err := r.release(ctx, name, number, release.URL)
		if err != nil {
			return nil, err
		}
		version, err := info.version(r.urls, name)
		if err != nil {
			return nil, err
		}
		versions = append(versions, *version)
	}
	return versions, nil
}

func (r *Registry) FetchDependencies(_ context.Context, _, _ string) ([]core.Dependency, error) {
	return nil, fmt.Errorf("swift registry protocol does not expose structured dependencies: %w", errors.ErrUnsupported)
}

func (r *Registry) FetchMaintainers(_ context.Context, _ string) ([]core.Maintainer, error) {
	return nil, fmt.Errorf("swift registry protocol does not expose maintainers: %w", errors.ErrUnsupported)
}
