package fetch

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"unicode"

	"github.com/git-pkgs/registries"
	"github.com/git-pkgs/registries/client"
)

var (
	ErrUnsupportedEcosystem = errors.New("unsupported ecosystem")
	ErrNoDownloadURL        = errors.New("no download URL available")
	ErrNoMatchingArtifact   = errors.New("no artifact matches the requested file")
	ErrUnsafeURL            = errors.New("unsafe download URL from registry metadata")
)

// Registry provides package metadata and URL information for artifact resolution.
// This interface is satisfied by registries.Registry implementations.
type Registry interface {
	Ecosystem() string
	FetchVersions(ctx context.Context, name string) ([]registries.Version, error)
	URLs() client.URLBuilder
}

// Resolver determines download URLs for package artifacts.
type Resolver struct {
	registries map[string]Registry
}

// NewResolver creates a new URL resolver.
func NewResolver() *Resolver {
	return &Resolver{
		registries: make(map[string]Registry),
	}
}

// RegisterRegistry adds a registry for URL resolution.
func (r *Resolver) RegisterRegistry(reg Registry) {
	r.registries[reg.Ecosystem()] = reg
}

// ArtifactInfo contains information about a downloadable artifact.
type ArtifactInfo struct {
	URL       string
	Filename  string
	Integrity string // sha256-... or sha512-...
	Size      int64  // Zero when the registry does not publish a size.
}

// ResolveOptions narrows versions that publish several artifact files.
type ResolveOptions struct {
	Filename  string
	Integrity string
}

// Resolve returns the download URL and filename for a package artifact.
func (r *Resolver) Resolve(ctx context.Context, ecosystem, name, version string) (*ArtifactInfo, error) {
	return r.ResolveWithOptions(ctx, ecosystem, name, version, ResolveOptions{})
}

// ResolveWithOptions returns the download metadata matching filename and
// registry-native integrity constraints when they are populated.
func (r *Resolver) ResolveWithOptions(
	ctx context.Context,
	ecosystem string,
	name string,
	version string,
	options ResolveOptions,
) (*ArtifactInfo, error) {
	reg, ok := r.registries[ecosystem]
	if !ok {
		info, err := r.resolveWithoutRegistry(ecosystem, name, version)
		return matchSingleArtifact(info, options, err)
	}

	// Try the simple URL builder first
	if url := reg.URLs().Download(name, version); url != "" {
		info := &ArtifactInfo{
			URL:      url,
			Filename: filenameFromURL(url),
		}
		return matchSingleArtifact(info, options, nil)
	}

	// For ecosystems like PyPI, we need to fetch metadata to get the URL
	return r.resolveFromMetadata(ctx, reg, name, version, options)
}

// resolveWithoutRegistry handles ecosystems with predictable URLs
// when no registry client is configured.
func (r *Resolver) resolveWithoutRegistry(ecosystem, name, version string) (*ArtifactInfo, error) {
	var url, filename string

	switch ecosystem {
	case "npm":
		shortName := name
		if idx := strings.LastIndex(name, "/"); idx >= 0 {
			shortName = name[idx+1:]
		}
		url = fmt.Sprintf("https://registry.npmjs.org/%s/-/%s-%s.tgz", name, shortName, version)
		filename = fmt.Sprintf("%s-%s.tgz", shortName, version)

	case "cargo":
		url = fmt.Sprintf("https://static.crates.io/crates/%s/%s-%s.crate", name, name, version)
		filename = fmt.Sprintf("%s-%s.crate", name, version)

	case "gem":
		url = fmt.Sprintf("https://rubygems.org/downloads/%s-%s.gem", name, version)
		filename = fmt.Sprintf("%s-%s.gem", name, version)

	case "golang":
		encoded := encodeGoModule(name)
		url = fmt.Sprintf("https://proxy.golang.org/%s/@v/%s.zip", encoded, version)
		filename = fmt.Sprintf("%s@%s.zip", lastPathComponent(name), version)

	case "hex":
		url = fmt.Sprintf("https://repo.hex.pm/tarballs/%s-%s.tar", name, version)
		filename = fmt.Sprintf("%s-%s.tar", name, version)

	case "pub":
		url = fmt.Sprintf("https://pub.dev/packages/%s/versions/%s.tar.gz", name, version)
		filename = fmt.Sprintf("%s-%s.tar.gz", name, version)

	case "maven":
		// Maven name format is "group:artifact", e.g., "com.google.guava:guava"
		group, artifact, found := strings.Cut(name, ":")
		if !found {
			return nil, fmt.Errorf("invalid maven name format, expected group:artifact")
		}
		group = strings.ReplaceAll(group, ".", "/")
		url = fmt.Sprintf("https://repo1.maven.org/maven2/%s/%s/%s/%s-%s.jar", group, artifact, version, artifact, version)
		filename = fmt.Sprintf("%s-%s.jar", artifact, version)

	case "nuget":
		// NuGet package IDs are case-insensitive, use lowercase
		lowername := strings.ToLower(name)
		url = fmt.Sprintf("https://api.nuget.org/v3-flatcontainer/%s/%s/%s.%s.nupkg", lowername, version, lowername, version)
		filename = fmt.Sprintf("%s.%s.nupkg", lowername, version)

	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedEcosystem, ecosystem)
	}

	return &ArtifactInfo{
		URL:      url,
		Filename: filename,
	}, nil
}

// resolveFromMetadata fetches version metadata to find download URL.
func (r *Resolver) resolveFromMetadata(
	ctx context.Context,
	reg Registry,
	name string,
	version string,
	options ResolveOptions,
) (*ArtifactInfo, error) {
	versions, err := reg.FetchVersions(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("fetching versions: %w", err)
	}

	for _, v := range versions {
		if v.Number != version {
			continue
		}

		if len(v.Artifacts) > 0 {
			return selectArtifact(v.Artifacts, options)
		}

		// Look for download URL in metadata. These come from the
		// registry's API response, not from us, so they need checking
		// before anyone fetches them.
		if v.Metadata != nil {
			if u, ok := v.Metadata["download_url"].(string); ok && u != "" {
				info, err := artifactFromMetadataURL(u, v.Integrity)
				return matchSingleArtifact(info, options, err)
			}
			if u, ok := v.Metadata["tarball"].(string); ok && u != "" {
				info, err := artifactFromMetadataURL(u, v.Integrity)
				return matchSingleArtifact(info, options, err)
			}
		}

		return nil, ErrNoDownloadURL
	}

	return nil, ErrNotFound
}

func selectArtifact(candidates []registries.Artifact, options ResolveOptions) (*ArtifactInfo, error) {
	for _, candidate := range candidates {
		filename := candidate.Filename
		if filename == "" {
			filename = filenameFromURL(candidate.URL)
		}
		if options.Filename != "" && filename != options.Filename {
			continue
		}
		if options.Integrity != "" && !integrityMatches(candidate.Integrity, options.Integrity) {
			continue
		}
		if err := checkMetadataURL(candidate.URL); err != nil {
			return nil, err
		}
		return &ArtifactInfo{
			URL:       candidate.URL,
			Filename:  filename,
			Integrity: candidate.Integrity,
			Size:      candidate.Size,
		}, nil
	}
	return nil, ErrNoMatchingArtifact
}

func matchSingleArtifact(info *ArtifactInfo, options ResolveOptions, err error) (*ArtifactInfo, error) {
	if err != nil {
		return nil, err
	}
	if options.Filename != "" && info.Filename != options.Filename {
		return nil, ErrNoMatchingArtifact
	}
	if options.Integrity != "" && info.Integrity != "" &&
		!integrityMatches(info.Integrity, options.Integrity) {
		return nil, ErrNoMatchingArtifact
	}
	return info, nil
}

func integrityMatches(candidate, requested string) bool {
	for _, expected := range strings.Fields(requested) {
		if candidate == expected {
			return true
		}
	}
	return false
}

func artifactFromMetadataURL(raw, integrity string) (*ArtifactInfo, error) {
	if err := checkMetadataURL(raw); err != nil {
		return nil, err
	}
	return &ArtifactInfo{
		URL:       raw,
		Filename:  filenameFromURL(raw),
		Integrity: integrity,
	}, nil
}

// checkMetadataURL rejects download URLs from registry responses that
// could direct the fetcher somewhere it shouldn't go. A compromised or
// MITM'd registry could otherwise hand back file:// or a cloud metadata
// endpoint.
func checkMetadataURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnsafeURL, err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("%w: scheme %q", ErrUnsafeURL, u.Scheme)
	}
	hostname := u.Hostname()
	if hostname == "" {
		return fmt.Errorf("%w: empty host", ErrUnsafeURL)
	}
	if isPrivateHost(hostname) {
		return fmt.Errorf("%w: private/loopback host %q", ErrUnsafeURL, hostname)
	}
	return nil
}

func isPrivateHost(hostname string) bool {
	ip := net.ParseIP(hostname)
	if ip == nil {
		ips, err := net.LookupIP(hostname)
		if err != nil || len(ips) == 0 {
			return false
		}
		ip = ips[0]
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}

func filenameFromURL(url string) string {
	if idx := strings.LastIndex(url, "/"); idx >= 0 {
		return url[idx+1:]
	}
	return url
}

func lastPathComponent(path string) string {
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		return path[idx+1:]
	}
	return path
}

// encodeGoModule encodes a module path per goproxy protocol.
// Capital letters become "!" followed by lowercase.
func encodeGoModule(path string) string {
	var b strings.Builder
	for _, r := range path {
		if r >= 'A' && r <= 'Z' {
			b.WriteRune('!')
			b.WriteRune(unicode.ToLower(r))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
