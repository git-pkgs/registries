// Package conda provides a registry client for Anaconda/Conda packages.
package conda

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/git-pkgs/registries/internal/core"
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

func (r *Registry) URLs() core.URLBuilder {
	return r.urls
}

type packageResponse struct {
	Name          string        `json:"name"`
	Summary       string        `json:"summary"`
	Description   string        `json:"description"`
	License       string        `json:"license"`
	LicenseURL    string        `json:"license_url"`
	DevURL        string        `json:"dev_url"`
	HomeURL       string        `json:"home"`
	DocURL        string        `json:"doc_url"`
	SourceURL     string        `json:"source_url"`
	Versions      []string      `json:"versions"`
	LatestVersion string        `json:"latest_version"`
	Files         []fileInfo    `json:"files"`
	Owner         string        `json:"owner"`
	PublicAccess  bool          `json:"public_access"`
}

type fileInfo struct {
	Version   string            `json:"version"`
	Basename  string            `json:"basename"`
	Attrs     fileAttrs         `json:"attrs"`
	UploadTime int64            `json:"upload_time"`
	MD5       string            `json:"md5"`
	SHA256    string            `json:"sha256"`
	Size      int64             `json:"size"`
	Ndownloads int64            `json:"ndownloads"`
}

type fileAttrs struct {
	Depends  []string `json:"depends"`
	Arch     string   `json:"arch"`
	Platform string   `json:"platform"`
	BuildNumber int   `json:"build_number"`
}

// parsePackageName parses a package name that may include a channel prefix
// Format: "channel/name" or just "name" (uses default channel)
func parsePackageName(name string) (channel, pkgName string) {
	parts := strings.SplitN(name, "/", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
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

	repository := resp.DevURL
	if repository == "" {
		repository = resp.SourceURL
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
			"owner":       resp.Owner,
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
		if _, exists := versionMap[f.Version]; !exists {
			var publishedAt time.Time
			if f.UploadTime > 0 {
				publishedAt = time.Unix(f.UploadTime, 0)
			}

			var integrity string
			if f.SHA256 != "" {
				integrity = "sha256-" + f.SHA256
			} else if f.MD5 != "" {
				integrity = "md5-" + f.MD5
			}

			versionMap[f.Version] = &core.Version{
				Number:      f.Version,
				PublishedAt: publishedAt,
				Integrity:   integrity,
				Licenses:    resp.License,
				Metadata: map[string]any{
					"downloads": f.Ndownloads,
				},
			}
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
	// Conda dependency format: "name version_constraint" or just "name"
	// Examples: "python >=3.8", "numpy", "pandas >=1.0,<2.0"
	dep = strings.TrimSpace(dep)
	parts := strings.SplitN(dep, " ", 2)
	name = parts[0]
	if len(parts) > 1 {
		requirements = strings.TrimSpace(parts[1])
	}
	return
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

	if resp.Owner == "" {
		return nil, nil
	}

	return []core.Maintainer{{
		Login: resp.Owner,
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
