// Package conda provides a registry client for Anaconda/Conda packages.
package conda

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/git-pkgs/registries/internal/core"
	"github.com/git-pkgs/registries/internal/urlparser"
)

const (
	DefaultURL     = "https://api.anaconda.org"
	DefaultChannel = "conda-forge"
	ecosystem      = "conda"
)

func init() {
	core.Register(ecosystem, DefaultURL, func(baseURL string, client *core.Client) core.Registry {
		return New(baseURL, client)
	})
}

type Registry struct {
	baseURL string
	channel string
	client  *core.Client
	urls    *URLs
}

func New(baseURL string, client *core.Client) *Registry {
	if baseURL == "" {
		baseURL = DefaultURL
	}
	r := &Registry{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		channel: DefaultChannel,
		client:  client,
	}
	r.urls = &URLs{baseURL: r.baseURL, channel: r.channel}
	return r
}

// WithChannel returns a new Registry configured to use the specified channel
func (r *Registry) WithChannel(channel string) *Registry {
	return &Registry{
		baseURL: r.baseURL,
		channel: channel,
		client:  r.client,
		urls:    &URLs{baseURL: r.baseURL, channel: channel},
	}
}

func (r *Registry) Ecosystem() string {
	return ecosystem
}

func (r *Registry) URLs() core.URLBuilder { //nolint:ireturn
	return r.urls
}

type packageResponse struct {
	Name          string     `json:"name"`
	Summary       string     `json:"summary"`
	Description   string     `json:"description"`
	License       string     `json:"license"`
	LicenseURL    string     `json:"license_url"`
	DevURL        string     `json:"dev_url"`
	HomeURL       string     `json:"home"`
	DocURL        string     `json:"doc_url"`
	SourceURL     string     `json:"source_url"`
	Versions      []string   `json:"versions"`
	LatestVersion string     `json:"latest_version"`
	Files         []fileInfo `json:"files"`
	Owner         ownerInfo  `json:"owner"`
	PublicAccess  bool       `json:"public_access"`
}

type ownerInfo struct {
	Login string `json:"login"`
	Name  string `json:"name"`
}

type fileInfo struct {
	Version     string    `json:"version"`
	Basename    string    `json:"basename"`
	DownloadURL string    `json:"download_url"`
	Attrs       fileAttrs `json:"attrs"`
	UploadTime  string    `json:"upload_time"`
	MD5         string    `json:"md5"`
	SHA256      string    `json:"sha256"`
	Size        int64     `json:"size"`
	Ndownloads  int64     `json:"ndownloads"`
}

type fileAttrs struct {
	Depends     []string `json:"depends"`
	Arch        string   `json:"arch"`
	Platform    string   `json:"platform"`
	Subdir      string   `json:"subdir"`
	Build       string   `json:"build"`
	BuildNumber int      `json:"build_number"`
}

// parsePackageName parses a package name that may include a channel prefix
// Format: "channel/name" or just "name" (uses default channel)
func parsePackageName(name string) (channel, pkgName string) {
	if before, after, found := strings.Cut(name, "/"); found {
		return before, after
	}
	return "", name
}

func (r *Registry) FetchPackage(ctx context.Context, name string) (*core.Package, error) {
	channel, pkgName := parsePackageName(name)
	if channel == "" {
		channel = r.channel
	}

	url := fmt.Sprintf("%s/package/%s/%s", r.baseURL, channel, pkgName)

	var resp packageResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	description := resp.Summary
	if description == "" {
		description = resp.Description
	}

	repository := urlparser.Parse(resp.DevURL)
	if repository == "" {
		repository = urlparser.Parse(resp.SourceURL)
	}

	return &core.Package{
		Name:          resp.Name,
		Description:   description,
		Homepage:      resp.HomeURL,
		Repository:    repository,
		Licenses:      resp.License,
		Namespace:     channel,
		LatestVersion: resp.LatestVersion,
		Metadata: map[string]any{
			"channel":     channel,
			"owner":       resp.Owner.Login,
			"doc_url":     resp.DocURL,
			"license_url": resp.LicenseURL,
		},
	}, nil
}

func (r *Registry) FetchVersions(ctx context.Context, name string) ([]core.Version, error) {
	channel, pkgName := parsePackageName(name)
	if channel == "" {
		channel = r.channel
	}

	url := fmt.Sprintf("%s/package/%s/%s", r.baseURL, channel, pkgName)

	var resp packageResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	// Build version info from files
	versionMap := make(map[string]*core.Version)
	for _, f := range resp.Files {
		integrity := fileIntegrity(f)
		ver, exists := versionMap[f.Version]
		if !exists {
			publishedAt, _ := time.Parse(uploadTimeLayout, f.UploadTime)
			ver = &core.Version{
				Number:      f.Version,
				PublishedAt: publishedAt,
				Integrity:   integrity,
				Licenses:    resp.License,
				Metadata: map[string]any{
					"downloads": f.Ndownloads,
				},
			}
			versionMap[f.Version] = ver
		}
		if artifact, ok := fileArtifact(f, integrity); ok {
			ver.Artifacts = append(ver.Artifacts, artifact)
		}
	}

	// Convert map to slice, ordered by Versions list
	versions := make([]core.Version, 0, len(resp.Versions))
	for _, v := range resp.Versions {
		if ver, ok := versionMap[v]; ok {
			versions = append(versions, *ver)
		} else {
			versions = append(versions, core.Version{Number: v})
		}
	}

	return versions, nil
}

// uploadTimeLayout is the format of upload_time in anaconda.org file records,
// e.g. "2026-07-17 01:07:03.989000+00:00".
const uploadTimeLayout = "2006-01-02 15:04:05.999999-07:00"

func fileIntegrity(f fileInfo) string {
	if f.SHA256 != "" {
		return "sha256-" + f.SHA256
	}
	if f.MD5 != "" {
		return "md5-" + f.MD5
	}
	return ""
}

// fileArtifact describes one build of a version. anaconda.org returns
// scheme-relative download URLs ("//api.anaconda.org/download/...").
func fileArtifact(f fileInfo, integrity string) (core.Artifact, bool) {
	downloadURL := f.DownloadURL
	if downloadURL == "" {
		return core.Artifact{}, false
	}
	if strings.HasPrefix(downloadURL, "//") {
		downloadURL = "https:" + downloadURL
	}
	return core.Artifact{
		URL:       downloadURL,
		Filename:  path.Base(f.Basename),
		Integrity: integrity,
		Size:      f.Size,
		Metadata: map[string]any{
			"subdir":       f.Attrs.Subdir,
			"build":        f.Attrs.Build,
			"build_number": f.Attrs.BuildNumber,
			"arch":         f.Attrs.Arch,
			"platform":     f.Attrs.Platform,
		},
	}, true
}

func (r *Registry) FetchDependencies(ctx context.Context, name, version string) ([]core.Dependency, error) {
	channel, pkgName := parsePackageName(name)
	if channel == "" {
		channel = r.channel
	}

	url := fmt.Sprintf("%s/package/%s/%s", r.baseURL, channel, pkgName)

	var resp packageResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name, Version: version}
		}
		return nil, err
	}

	// Find dependencies for the specific version
	var deps []core.Dependency
	seen := make(map[string]bool)

	for _, f := range resp.Files {
		if f.Version == version {
			for _, d := range f.Attrs.Depends {
				depName, requirements := parseDependency(d)
				if depName == "" || seen[depName] {
					continue
				}
				seen[depName] = true

				deps = append(deps, core.Dependency{
					Name:         depName,
					Requirements: requirements,
					Scope:        core.Runtime,
				})
			}
			break
		}
	}

	return deps, nil
}

func parseDependency(dep string) (name, requirements string) {
	dep = strings.TrimSpace(dep)
	name, requirements, _ = strings.Cut(dep, " ")
	requirements = strings.TrimSpace(requirements)
	return name, requirements
}

func (r *Registry) FetchMaintainers(ctx context.Context, name string) ([]core.Maintainer, error) {
	channel, pkgName := parsePackageName(name)
	if channel == "" {
		channel = r.channel
	}

	url := fmt.Sprintf("%s/package/%s/%s", r.baseURL, channel, pkgName)

	var resp packageResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	if resp.Owner.Login == "" {
		return nil, nil
	}

	return []core.Maintainer{{
		Login: resp.Owner.Login,
		Name:  resp.Owner.Name,
	}}, nil
}

type URLs struct {
	baseURL string
	channel string
}

func (u *URLs) Registry(name, version string) string {
	channel, pkgName := parsePackageName(name)
	if channel == "" {
		channel = u.channel
	}
	if version != "" {
		return fmt.Sprintf("https://anaconda.org/%s/%s/%s", channel, pkgName, version)
	}
	return fmt.Sprintf("https://anaconda.org/%s/%s", channel, pkgName)
}

func (u *URLs) Download(name, version string) string {
	// Conda download URLs vary by platform and Python version
	// Return empty as there's no single download URL
	return ""
}

func (u *URLs) Documentation(name, version string) string {
	channel, pkgName := parsePackageName(name)
	if channel == "" {
		channel = u.channel
	}
	return fmt.Sprintf("https://anaconda.org/%s/%s", channel, pkgName)
}

func (u *URLs) PURL(name, version string) string {
	channel, pkgName := parsePackageName(name)
	if channel == "" {
		channel = u.channel
	}
	if version != "" {
		return fmt.Sprintf("pkg:conda/%s/%s@%s", channel, pkgName, version)
	}
	return fmt.Sprintf("pkg:conda/%s/%s", channel, pkgName)
}
