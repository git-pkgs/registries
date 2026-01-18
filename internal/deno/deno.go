// Package deno provides a registry client for Deno modules.
package deno

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/git-pkgs/registries/internal/core"
)

const (
	DefaultURL = "https://apiland.deno.dev"
	ecosystem  = "deno"
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

type moduleResponse struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Star        int    `json:"star"`
	RepoID      int64  `json:"repo_id"`
}

type moduleInfoResponse struct {
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	LatestVersion   string          `json:"latest_version"`
	Versions        []string        `json:"versions"`
	UploadOptions   uploadOptions   `json:"upload_options"`
}

type uploadOptions struct {
	Type       string `json:"type"`
	Repository string `json:"repository"`
	Ref        string `json:"ref"`
}

type versionResponse struct {
	Version     string    `json:"version"`
	UploadedAt  time.Time `json:"uploaded_at"`
}

type versionMetaResponse struct {
	UploadedAt       string            `json:"uploaded_at"`
	DirectoryListing []directoryEntry  `json:"directory_listing"`
	UploadOptions    uploadOptions     `json:"upload_options"`
}

type directoryEntry struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	Type string `json:"type"`
}

func (r *Registry) FetchPackage(ctx context.Context, name string) (*core.Package, error) {
	url := fmt.Sprintf("%s/v2/modules/%s", r.baseURL, name)

	var resp moduleInfoResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	repository := ""
	if resp.UploadOptions.Type == "github" && resp.UploadOptions.Repository != "" {
		repository = "https://github.com/" + resp.UploadOptions.Repository
	}

	return &core.Package{
		Name:          resp.Name,
		Description:   resp.Description,
		Homepage:      fmt.Sprintf("https://deno.land/x/%s", resp.Name),
		Repository:    repository,
		LatestVersion: resp.LatestVersion,
		Metadata: map[string]any{
			"upload_type": resp.UploadOptions.Type,
		},
	}, nil
}

func (r *Registry) FetchVersions(ctx context.Context, name string) ([]core.Version, error) {
	url := fmt.Sprintf("%s/v2/modules/%s", r.baseURL, name)

	var resp moduleInfoResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	versions := make([]core.Version, 0, len(resp.Versions))
	for _, v := range resp.Versions {
		versions = append(versions, core.Version{
			Number: v,
		})
	}

	return versions, nil
}

func (r *Registry) FetchDependencies(ctx context.Context, name, version string) ([]core.Dependency, error) {
	// Deno modules use URL imports, not a manifest file
	// Dependencies are determined by analyzing the source code
	// The API doesn't expose a dependency list directly
	return nil, nil
}

func (r *Registry) FetchMaintainers(ctx context.Context, name string) ([]core.Maintainer, error) {
	// Deno modules are linked to GitHub repos
	// Maintainer info would come from the GitHub API
	return nil, nil
}

type URLs struct {
	baseURL string
}

func (u *URLs) Registry(name, version string) string {
	if version != "" {
		return fmt.Sprintf("https://deno.land/x/%s@%s", name, version)
	}
	return fmt.Sprintf("https://deno.land/x/%s", name)
}

func (u *URLs) Download(name, version string) string {
	if version == "" {
		return ""
	}
	return fmt.Sprintf("https://deno.land/x/%s@%s/mod.ts", name, version)
}

func (u *URLs) Documentation(name, version string) string {
	if version != "" {
		return fmt.Sprintf("https://deno.land/x/%s@%s", name, version)
	}
	return fmt.Sprintf("https://deno.land/x/%s", name)
}

func (u *URLs) PURL(name, version string) string {
	if version != "" {
		return fmt.Sprintf("pkg:deno/%s@%s", name, version)
	}
	return fmt.Sprintf("pkg:deno/%s", name)
}
