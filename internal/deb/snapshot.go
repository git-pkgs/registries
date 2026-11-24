package deb

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/git-pkgs/registries/internal/core"
	"github.com/git-pkgs/vers"
)

// snapshotHost is snapshot.debian.org, which keeps every file Debian has
// published. Its root, as the archive URL, selects the snapshot backend:
// versions come from its machine-readable API rather than from Packages
// indexes. A dated archive under it (/archive/debian/<timestamp>) is an
// ordinary archive.
const snapshotHost = "snapshot.debian.org"

// sha1Pattern matches the SHA-1 snapshot.debian.org names files by.
var sha1Pattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// isSnapshotRoot reports whether u is snapshot.debian.org's root.
func isSnapshotRoot(u *url.URL) bool {
	return u.Scheme == "https" && strings.EqualFold(u.Host, snapshotHost) && (u.Path == "" || u.Path == "/")
}

// configureSnapshot checks the configuration of the snapshot backend:
// architectures only, since the API lists a version's files whatever
// suite they were in.
func (r *Registry) configureSnapshot(q url.Values, baseURL string) error {
	r.snapshot = true
	r.archiveURL = "https://" + snapshotHost
	r.architectures = splitList(q.Get("architectures"))
	switch {
	case q.Has("suites") || q.Has("components"):
		return fmt.Errorf("%w: %s takes no suites or components in %q", ErrInvalidConfig, snapshotHost, baseURL)
	case len(r.architectures) == 0:
		return fmt.Errorf("%w: no architectures in %q", ErrInvalidConfig, baseURL)
	}
	for _, arch := range r.architectures {
		if !safeSegment(arch) {
			return fmt.Errorf("%w: %q", ErrInvalidConfig, arch)
		}
		if arch == "source" {
			return fmt.Errorf("%w: source packages are not supported", ErrInvalidConfig)
		}
	}
	return nil
}

type snapshotVersions struct {
	Result []struct {
		BinaryVersion string `json:"binary_version"`
		Source        string `json:"source"`
		Version       string `json:"version"`
	} `json:"result"`
}

type snapshotBinfiles struct {
	Result []struct {
		Architecture string `json:"architecture"`
		Hash         string `json:"hash"`
	} `json:"result"`
	Fileinfo map[string][]struct {
		ArchiveName string `json:"archive_name"`
		FirstSeen   string `json:"first_seen"`
		Name        string `json:"name"`
		Size        int64  `json:"size"`
	} `json:"fileinfo"`
}

// snapshotGet reads a snapshot API document, returning a NotFoundError
// for name and version on a 404.
func (r *Registry) snapshotGet(ctx context.Context, apiPath, name, version string, v any) error {
	if r.configErr != nil {
		return r.configErr
	}
	err := r.client.GetJSON(ctx, r.archiveURL+apiPath, v)
	if isNotFound(err) {
		return &core.NotFoundError{Ecosystem: ecosystem, Name: name, Version: version}
	}
	return err
}

// snapshotVersions lists a binary package's versions, newest first. Each
// takes another request to list its files, so they come without
// artifacts; FetchVersion adds them.
func (r *Registry) snapshotVersions(ctx context.Context, name string) ([]core.Version, error) {
	_, pkg := splitName(name)
	var doc snapshotVersions
	if err := r.snapshotGet(ctx, "/mr/binary/"+url.PathEscape(pkg)+"/", name, "", &doc); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var versions []core.Version
	for _, entry := range doc.Result {
		if entry.BinaryVersion == "" || seen[entry.BinaryVersion] {
			continue
		}
		seen[entry.BinaryVersion] = true
		versions = append(versions, core.Version{
			Number:   entry.BinaryVersion,
			Metadata: map[string]any{"source": entry.Source, "source_version": entry.Version},
		})
	}
	if len(versions) == 0 {
		return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
	}
	sort.SliceStable(versions, func(i, j int) bool {
		return vers.CompareWithScheme(versions[i].Number, versions[j].Number, ecosystem) > 0
	})
	return versions, nil
}

// snapshotVersion lists a version's files for the configured
// architectures, and Architecture: all. Their integrity is the SHA-1
// snapshot.debian.org names them by, not a SHA256 from a signed index.
func (r *Registry) snapshotVersion(ctx context.Context, name, version string) (*core.Version, error) {
	_, pkg := splitName(name)
	apiPath := "/mr/binary/" + url.PathEscape(pkg) + "/" + url.PathEscape(version) + "/binfiles?fileinfo=1"
	var doc snapshotBinfiles
	if err := r.snapshotGet(ctx, apiPath, name, version, &doc); err != nil {
		return nil, err
	}
	v := &core.Version{Number: version}
	for _, file := range doc.Result {
		if !r.wantsArch(file.Architecture) || !sha1Pattern.MatchString(file.Hash) {
			continue
		}
		infos := doc.Fileinfo[file.Hash]
		if len(infos) == 0 || !safeFilename(infos[0].Name) {
			continue
		}
		info := infos[0]
		v.Artifacts = append(v.Artifacts, core.Artifact{
			URL:       r.archiveURL + "/file/" + file.Hash,
			Filename:  info.Name,
			Size:      info.Size,
			Integrity: "sha1-" + file.Hash,
			Metadata: map[string]any{
				"arch":       file.Architecture,
				"archive":    info.ArchiveName,
				"first_seen": info.FirstSeen,
			},
		})
	}
	return v, nil
}

func (r *Registry) wantsArch(arch string) bool {
	if arch == archAll {
		return true
	}
	for _, want := range r.architectures {
		if arch == want {
			return true
		}
	}
	return false
}

// safeFilename refuses a file name that isn't a plain name.
func safeFilename(name string) bool {
	return name != "" && safeSegment(name) && !strings.ContainsAny(name, "\x00")
}

func (r *Registry) snapshotPackage(ctx context.Context, name string) (*core.Package, error) {
	versions, err := r.snapshotVersions(ctx, name)
	if err != nil {
		return nil, err
	}
	namespace, pkg := splitName(name)
	return &core.Package{
		Name:          pkg,
		Namespace:     namespace,
		LatestVersion: versions[0].Number,
		Metadata:      map[string]any{"source": versions[0].Metadata["source"]},
	}, nil
}
