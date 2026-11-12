package apm

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/git-pkgs/registries/internal/core"
)

const ecosystem = "apm"

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

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

type versionsResponse struct {
	Package  string         `json:"package"`
	Versions []versionEntry `json:"versions"`
}

type versionEntry struct {
	Version     string `json:"version"`
	Digest      string `json:"digest"`
	PublishedAt string `json:"published_at"`
	SizeBytes   int64  `json:"size_bytes"`
}

func (r *Registry) FetchPackage(ctx context.Context, name string) (*core.Package, error) {
	versions, err := r.FetchVersions(ctx, name)
	if err != nil {
		return nil, err
	}
	owner, _, _ := splitIdentity(name)
	pkg := &core.Package{Name: name, Namespace: owner}
	if latest := core.SelectLatestVersion(versions, ecosystem, ""); latest != nil {
		pkg.LatestVersion = latest.Number
	}
	return pkg, nil
}

// FetchVersions lists versions newest first, since the API does not guarantee an order.
func (r *Registry) FetchVersions(ctx context.Context, name string) ([]core.Version, error) {
	endpoint, err := r.urls.endpoint(name, "")
	if err != nil {
		return nil, err
	}
	var resp versionsResponse
	if err := r.client.GetJSON(ctx, endpoint, &resp); err != nil {
		var httpErr *core.HTTPError
		if errors.As(err, &httpErr) && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}
	if resp.Versions == nil {
		return nil, fmt.Errorf("APM registry response for %s has no versions array", name)
	}
	if !strings.EqualFold(resp.Package, name) {
		return nil, fmt.Errorf("APM registry returned package %q for %q", resp.Package, name)
	}

	versions := make([]core.Version, 0, len(resp.Versions))
	seen := make(map[string]bool, len(resp.Versions))
	for _, entry := range resp.Versions {
		version, err := r.version(name, entry)
		if err != nil {
			return nil, err
		}
		if seen[version.Number] {
			return nil, fmt.Errorf("APM registry listed version %q twice for %s", version.Number, name)
		}
		seen[version.Number] = true
		versions = append(versions, *version)
	}
	sort.SliceStable(versions, func(i, j int) bool {
		return versions[i].PublishedAt.After(versions[j].PublishedAt)
	})
	return versions, nil
}

func (r *Registry) version(name string, entry versionEntry) (*core.Version, error) {
	if entry.Version == "" {
		return nil, fmt.Errorf("APM registry returned a version without a version string for %s", name)
	}
	if !digestPattern.MatchString(entry.Digest) {
		return nil, fmt.Errorf("APM registry version %s has invalid digest %q", entry.Version, entry.Digest)
	}
	published, err := time.Parse(time.RFC3339Nano, entry.PublishedAt)
	if err != nil {
		return nil, fmt.Errorf("APM registry version %s has invalid published_at: %w", entry.Version, err)
	}
	download, err := r.urls.endpoint(name, entry.Version)
	if err != nil {
		return nil, err
	}
	integrity := "sha256-" + strings.TrimPrefix(entry.Digest, "sha256:")
	artifact := core.Artifact{URL: download, Integrity: integrity}
	if entry.SizeBytes > 0 {
		artifact.Size = entry.SizeBytes
	}
	return &core.Version{
		Number:      entry.Version,
		PublishedAt: published,
		Integrity:   integrity,
		Artifacts:   []core.Artifact{artifact},
	}, nil
}

func (r *Registry) FetchDependencies(_ context.Context, _, _ string) ([]core.Dependency, error) {
	return nil, fmt.Errorf("APM registry API does not expose dependencies: %w", errors.ErrUnsupported)
}

func (r *Registry) FetchMaintainers(_ context.Context, _ string) ([]core.Maintainer, error) {
	return nil, fmt.Errorf("APM registry API does not expose maintainers: %w", errors.ErrUnsupported)
}
