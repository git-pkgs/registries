// Package vscodeextension provides a registry client for VS Code extensions
// served by Open VSX (https://open-vsx.org) or a self-hosted Open VSX server.
package vscodeextension

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/git-pkgs/purl"
	"github.com/git-pkgs/registries/internal/core"
	"github.com/git-pkgs/registries/internal/urlparser"
)

const (
	DefaultURL = "https://open-vsx.org"
	ecosystem  = "vscode-extension"

	// pageSize is how many builds a query asks for at once. The server may
	// answer fewer; the listing goes on from however many came back.
	pageSize = 100
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

func (r *Registry) URLs() core.URLBuilder { //nolint:ireturn
	return r.urls
}

// splitName splits "publisher/name", the PURL's namespace and name, or
// "publisher.name", VS Code's extension identifier.
func splitName(name string) (namespace, extension string, err error) {
	var ok bool
	if strings.Contains(name, "/") {
		namespace, extension, ok = strings.Cut(name, "/")
	} else {
		namespace, extension, ok = strings.Cut(name, ".")
	}
	if !ok || !validSegment(namespace) || !validSegment(extension) || strings.Contains(extension, "/") {
		return "", "", fmt.Errorf("invalid VS Code extension name %q: expected publisher/name or publisher.name", name)
	}
	return namespace, extension, nil
}

func validSegment(s string) bool {
	return s != "" && s != "." && s != ".."
}

func validVersion(version string) error {
	if !validSegment(version) {
		return fmt.Errorf("invalid VS Code extension version %q", version)
	}
	return nil
}

type extension struct {
	Namespace            string            `json:"namespace"`
	Name                 string            `json:"name"`
	Version              string            `json:"version"`
	Timestamp            string            `json:"timestamp"`
	TargetPlatform       string            `json:"targetPlatform"`
	DisplayName          string            `json:"displayName"`
	NamespaceDisplayName string            `json:"namespaceDisplayName"`
	Description          string            `json:"description"`
	License              string            `json:"license"`
	Homepage             string            `json:"homepage"`
	Repository           string            `json:"repository"`
	Bugs                 string            `json:"bugs"`
	DownloadCount        int64             `json:"downloadCount"`
	Verified             bool              `json:"verified"`
	Deprecated           bool              `json:"deprecated"`
	PreRelease           bool              `json:"preRelease"`
	Tags                 []string          `json:"tags"`
	Files                map[string]string `json:"files"`
	PublishedBy          *user             `json:"publishedBy"`
	Dependencies         []extensionRef    `json:"dependencies"`
	BundledExtensions    []extensionRef    `json:"bundledExtensions"`
}

type user struct {
	LoginName string `json:"loginName"`
	FullName  string `json:"fullName"`
	Homepage  string `json:"homepage"`
}

type extensionRef struct {
	Namespace string `json:"namespace"`
	Extension string `json:"extension"`
}

type queryResponse struct {
	Offset     int         `json:"offset"`
	TotalSize  int         `json:"totalSize"`
	Extensions []extension `json:"extensions"`
}

func (e *extension) is(namespace, name string) bool {
	return strings.EqualFold(e.Namespace, namespace) && strings.EqualFold(e.Name, name)
}

// get fetches one extension document, the latest version when version is
// empty.
func (r *Registry) get(ctx context.Context, name, version string) (*extension, error) {
	namespace, ext, err := splitName(name)
	if err != nil {
		return nil, err
	}
	endpoint := r.baseURL + "/api/" + url.PathEscape(namespace) + "/" + url.PathEscape(ext)
	if version != "" {
		endpoint += "/" + url.PathEscape(version)
	}
	var resp extension
	if err := r.client.GetJSON(ctx, endpoint, &resp); err != nil {
		var httpErr *core.HTTPError
		if errors.As(err, &httpErr) && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name, Version: version}
		}
		return nil, err
	}
	if !resp.is(namespace, ext) {
		return nil, fmt.Errorf("open VSX returned extension %s.%s for %s", resp.Namespace, resp.Name, name)
	}
	return &resp, nil
}

func (r *Registry) FetchPackage(ctx context.Context, name string) (*core.Package, error) {
	resp, err := r.get(ctx, name, "")
	if err != nil {
		return nil, err
	}
	var keywords []string
	for _, tag := range resp.Tags {
		// Tags such as __ext_yaml and __web_extension are VS Code's own
		// markers, not keywords the publisher chose.
		if !strings.HasPrefix(tag, "__") {
			keywords = append(keywords, tag)
		}
	}
	pkg := &core.Package{
		Name:          resp.Namespace + "/" + resp.Name,
		Description:   resp.Description,
		Homepage:      resp.Homepage,
		Repository:    urlparser.Parse(resp.Repository),
		Licenses:      resp.License,
		Keywords:      keywords,
		Namespace:     resp.Namespace,
		LatestVersion: resp.Version,
		Maintainers:   maintainers(resp),
		Metadata: map[string]any{
			"display_name":           resp.DisplayName,
			"namespace_display_name": resp.NamespaceDisplayName,
			"downloads":              resp.DownloadCount,
			"verified":               resp.Verified,
			"deprecated":             resp.Deprecated,
			"bugs":                   resp.Bugs,
		},
	}
	return pkg, nil
}

// maintainers lists the user who published the latest version. Open VSX
// does not list a namespace's members.
func maintainers(e *extension) []core.Maintainer {
	if e.PublishedBy == nil || e.PublishedBy.LoginName == "" {
		return nil
	}
	return []core.Maintainer{{
		Login: e.PublishedBy.LoginName,
		Name:  e.PublishedBy.FullName,
		URL:   e.PublishedBy.Homepage,
	}}
}

// query lists every build of the extension, or of one version of it, page
// by page. Each build is one version for one target platform.
func (r *Registry) query(ctx context.Context, name, version string) ([]extension, error) {
	namespace, ext, err := splitName(name)
	if err != nil {
		return nil, err
	}
	params := url.Values{
		"namespaceName":      {namespace},
		"extensionName":      {ext},
		"includeAllVersions": {"true"},
		"size":               {strconv.Itoa(pageSize)},
	}
	if version != "" {
		params.Set("extensionVersion", version)
	}
	var builds []extension
	for {
		params.Set("offset", strconv.Itoa(len(builds)))
		var page queryResponse
		if err := r.client.GetJSON(ctx, r.baseURL+"/api/v2/-/query?"+params.Encode(), &page); err != nil {
			return nil, err
		}
		for i := range page.Extensions {
			if !page.Extensions[i].is(namespace, ext) {
				return nil, fmt.Errorf("open VSX returned extension %s.%s for %s",
					page.Extensions[i].Namespace, page.Extensions[i].Name, name)
			}
		}
		builds = append(builds, page.Extensions...)
		if len(page.Extensions) == 0 || len(builds) >= page.TotalSize {
			break
		}
	}
	if len(builds) == 0 {
		return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name, Version: version}
	}
	return builds, nil
}

// versions groups builds by version, newest first, each with one artifact
// per target platform.
func versions(name string, builds []extension) ([]core.Version, error) {
	var out []core.Version
	index := make(map[string]int)
	seen := make(map[[2]string]bool)
	for _, b := range builds {
		if b.Version == "" {
			return nil, fmt.Errorf("open VSX returned a build of %s without a version", name)
		}
		published, err := time.Parse(time.RFC3339Nano, b.Timestamp)
		if err != nil {
			return nil, fmt.Errorf("open VSX build %s of %s has invalid timestamp: %w", b.Version, name, err)
		}
		key := [2]string{b.Version, b.TargetPlatform}
		if seen[key] {
			return nil, fmt.Errorf("open VSX listed the %s build of %s %s twice", b.TargetPlatform, name, b.Version)
		}
		seen[key] = true

		i, ok := index[b.Version]
		if !ok {
			i = len(out)
			index[b.Version] = i
			out = append(out, core.Version{
				Number:      b.Version,
				PublishedAt: published,
				Licenses:    b.License,
				Metadata:    map[string]any{"pre_release": b.PreRelease},
			})
		}
		v := &out[i]
		if published.Before(v.PublishedAt) {
			v.PublishedAt = published
		}
		if download := b.Files["download"]; download != "" {
			v.Artifacts = append(v.Artifacts, core.Artifact{
				URL:      download,
				Filename: filename(download),
				Metadata: map[string]any{
					"target_platform": b.TargetPlatform,
					"signature":       b.Files["signature"],
					"sha256":          b.Files["sha256"],
				},
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].PublishedAt.After(out[j].PublishedAt)
	})
	return out, nil
}

func filename(download string) string {
	u, err := url.Parse(download)
	if err != nil {
		return ""
	}
	return path.Base(u.Path)
}

func (r *Registry) FetchVersions(ctx context.Context, name string) ([]core.Version, error) {
	builds, err := r.query(ctx, name, "")
	if err != nil {
		return nil, err
	}
	return versions(name, builds)
}

// FetchVersion returns one version with the builds for all its target
// platforms.
func (r *Registry) FetchVersion(ctx context.Context, name, version string) (*core.Version, error) {
	if err := validVersion(version); err != nil {
		return nil, err
	}
	builds, err := r.query(ctx, name, version)
	if err != nil {
		return nil, err
	}
	all, err := versions(name, builds)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].Number == version {
			return &all[i], nil
		}
	}
	return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name, Version: version}
}

// FetchDependencies lists the extensions a version depends on and the ones
// it bundles as an extension pack. VS Code installs both with it.
func (r *Registry) FetchDependencies(ctx context.Context, name, version string) ([]core.Dependency, error) {
	if err := validVersion(version); err != nil {
		return nil, err
	}
	resp, err := r.get(ctx, name, version)
	if err != nil {
		return nil, err
	}
	refs := make([]extensionRef, 0, len(resp.Dependencies)+len(resp.BundledExtensions))
	refs = append(refs, resp.Dependencies...)
	refs = append(refs, resp.BundledExtensions...)
	deps := make([]core.Dependency, 0, len(refs))
	for _, ref := range refs {
		deps = append(deps, core.Dependency{
			Name:  ref.Namespace + "/" + ref.Extension,
			Scope: core.Runtime,
		})
	}
	sort.Slice(deps, func(i, j int) bool {
		return deps[i].Name < deps[j].Name
	})
	return deps, nil
}

func (r *Registry) FetchMaintainers(ctx context.Context, name string) ([]core.Maintainer, error) {
	resp, err := r.get(ctx, name, "")
	if err != nil {
		return nil, err
	}
	return maintainers(resp), nil
}

type URLs struct {
	baseURL string
}

func (u *URLs) Registry(name, version string) string {
	namespace, ext, err := splitName(name)
	if err != nil {
		return ""
	}
	page := u.baseURL + "/extension/" + url.PathEscape(namespace) + "/" + url.PathEscape(ext)
	if version != "" {
		page += "/" + url.PathEscape(version)
	}
	return page
}

// Download returns the universal build. A version built only for specific
// target platforms has none; its builds are the artifacts FetchVersions and
// FetchVersion list.
func (u *URLs) Download(name, version string) string {
	namespace, ext, err := splitName(name)
	if err != nil || validVersion(version) != nil {
		return ""
	}
	file := namespace + "." + ext + "-" + version + ".vsix"
	return u.baseURL + "/api/" + url.PathEscape(namespace) + "/" + url.PathEscape(ext) + "/" +
		url.PathEscape(version) + "/file/" + url.PathEscape(file)
}

func (u *URLs) Documentation(name, version string) string {
	return u.Registry(name, version)
}

// PURL carries the registry as repository_url, since a vscode-extension PURL
// without one names the Visual Studio Marketplace.
func (u *URLs) PURL(name, version string) string {
	namespace, ext, err := splitName(name)
	if err != nil {
		return ""
	}
	return purl.BuildPURLString(ecosystem, namespace+"."+ext, version, u.baseURL)
}
