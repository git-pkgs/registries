// Package maven provides a registry client for Maven Central (Java).
package maven

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/git-pkgs/pom"
	"github.com/git-pkgs/registries/internal/core"
	"github.com/git-pkgs/registries/internal/urlparser"
)

const (
	DefaultURL = "https://repo1.maven.org/maven2"
	SearchURL  = "https://search.maven.org"
	ecosystem  = "maven"
	// minCoordParts is the minimum number of parts in a Maven coordinate (group:artifact)
	minCoordParts = 2
	// coordPartsWithVersion is the number of parts when version is included (group:artifact:version)
	coordPartsWithVersion = 3
)

func init() {
	core.Register(ecosystem, DefaultURL, func(baseURL string, client *core.Client) core.Registry {
		return New(baseURL, client)
	})
}

type Registry struct {
	baseURL   string
	searchURL string
	client    *core.Client
	resolver  *pom.Resolver
	urls      *URLs
}

func New(baseURL string, client *core.Client) *Registry {
	if baseURL == "" {
		baseURL = DefaultURL
	}
	r := &Registry{
		baseURL:   strings.TrimSuffix(baseURL, "/"),
		searchURL: SearchURL,
		client:    client,
	}
	r.urls = &URLs{baseURL: r.baseURL}
	r.resolver = pom.NewResolver(pom.NewCachingFetcher(&clientFetcher{client: client, baseURL: r.baseURL}))
	return r
}

// clientFetcher adapts core.Client to pom.Fetcher so the resolver's HTTP
// goes through the same rate limiting, retries and circuit breaker as
// every other registry call.
type clientFetcher struct {
	client  *core.Client
	baseURL string
}

func (f *clientFetcher) Fetch(ctx context.Context, gav pom.GAV) (*pom.POM, error) {
	body, err := f.client.GetBody(ctx, pom.POMURL(f.baseURL, gav))
	if err != nil {
		return nil, err
	}
	return pom.ParsePOM(body)
}

func (r *Registry) effectivePOM(ctx context.Context, groupID, artifactID, version string) (*pom.EffectivePOM, error) {
	return r.resolver.Resolve(ctx, pom.GAV{GroupID: groupID, ArtifactID: artifactID, Version: version}, pom.Options{})
}

func (r *Registry) Ecosystem() string {
	return ecosystem
}

func (r *Registry) URLs() core.URLBuilder { //nolint:ireturn
	return r.urls
}

// searchResponse represents the Maven Central Search API response
type searchResponse struct {
	Response searchResponseBody `json:"response"`
}

type searchResponseBody struct {
	NumFound int         `json:"numFound"`
	Docs     []searchDoc `json:"docs"`
}

type searchDoc struct {
	ID           string `json:"id"`
	GroupID      string `json:"g"`
	ArtifactID   string `json:"a"`
	Version      string `json:"latestVersion"`
	Timestamp    int64  `json:"timestamp"`
	VersionCount int    `json:"versionCount"`
}

// pomDevelopers is the minimal XML shape needed for FetchMaintainers; the
// pom library doesn't carry <developers>.
type pomDevelopers struct {
	Developers []pomDeveloper `xml:"developers>developer"`
}

type pomDeveloper struct {
	ID    string `xml:"id"`
	Name  string `xml:"name"`
	Email string `xml:"email"`
	URL   string `xml:"url"`
}

// ParseCoordinates parses a Maven coordinate string.
// Accepts both "groupId:artifactId" (traditional) and "groupId/artifactId" (PURL FullName) formats.
// Optionally includes version: "groupId:artifactId:version" or "groupId/artifactId/version"
func ParseCoordinates(coord string) (groupID, artifactID, version string) {
	// Try colon separator first (traditional maven format)
	parts := strings.Split(coord, ":")
	if len(parts) >= minCoordParts {
		groupID = parts[0]
		artifactID = parts[1]
		if len(parts) >= coordPartsWithVersion {
			version = parts[2]
		}
		return
	}

	// Fall back to slash separator (PURL FullName format)
	parts = strings.Split(coord, "/")
	if len(parts) >= minCoordParts {
		groupID = parts[0]
		artifactID = parts[1]
		if len(parts) >= coordPartsWithVersion {
			version = parts[2]
		}
	}
	return
}

func (r *Registry) FetchPackage(ctx context.Context, name string) (*core.Package, error) {
	groupID, artifactID, _ := ParseCoordinates(name)
	if groupID == "" || artifactID == "" {
		return nil, fmt.Errorf("invalid Maven coordinate: %s (expected groupId:artifactId)", name)
	}

	// First try the search API to get basic metadata
	searchURL := fmt.Sprintf("%s/solrsearch/select?q=g:%s+AND+a:%s&core=gav&rows=1&wt=json",
		r.searchURL, url.QueryEscape(groupID), url.QueryEscape(artifactID))

	var searchResp searchResponse
	if err := r.client.GetJSON(ctx, searchURL, &searchResp); err == nil && searchResp.Response.NumFound > 0 {
		doc := searchResp.Response.Docs[0]
		// Fetch the POM for more details
		ep, _ := r.effectivePOM(ctx, groupID, artifactID, doc.Version)
		return r.packageFromSearchAndPOM(doc, ep), nil
	}

	// Fallback: try to get maven-metadata.xml
	metadataURL := fmt.Sprintf("%s/%s/%s/maven-metadata.xml",
		r.baseURL, groupIDToPath(groupID), artifactID)

	body, err := r.client.GetBody(ctx, metadataURL)
	if err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	var metadata mavenMetadata
	if err := xml.Unmarshal(body, &metadata); err != nil {
		return nil, err
	}

	// Get the latest version's POM
	latestVersion := metadata.Versioning.Latest
	if latestVersion == "" && len(metadata.Versioning.Versions) > 0 {
		latestVersion = metadata.Versioning.Versions[len(metadata.Versioning.Versions)-1]
	}

	ep, _ := r.effectivePOM(ctx, groupID, artifactID, latestVersion)
	return r.packageFromMetadataAndPOM(metadata, ep), nil
}

type mavenMetadata struct {
	GroupID    string     `xml:"groupId"`
	ArtifactID string     `xml:"artifactId"`
	Versioning versioning `xml:"versioning"`
}

type versioning struct {
	Latest   string   `xml:"latest"`
	Release  string   `xml:"release"`
	Versions []string `xml:"versions>version"`
}

func (r *Registry) packageFromSearchAndPOM(doc searchDoc, ep *pom.EffectivePOM) *core.Package {
	pkg := &core.Package{
		Name:      fmt.Sprintf("%s:%s", doc.GroupID, doc.ArtifactID),
		Namespace: doc.GroupID,
		Metadata: map[string]any{
			"group_id":      doc.GroupID,
			"artifact_id":   doc.ArtifactID,
			"version_count": doc.VersionCount,
		},
	}

	applyPOMMetadata(pkg, ep)
	return pkg
}

func (r *Registry) packageFromMetadataAndPOM(metadata mavenMetadata, ep *pom.EffectivePOM) *core.Package {
	pkg := &core.Package{
		Name:      fmt.Sprintf("%s:%s", metadata.GroupID, metadata.ArtifactID),
		Namespace: metadata.GroupID,
		Metadata: map[string]any{
			"group_id":    metadata.GroupID,
			"artifact_id": metadata.ArtifactID,
		},
	}

	applyPOMMetadata(pkg, ep)
	return pkg
}

func applyPOMMetadata(pkg *core.Package, ep *pom.EffectivePOM) {
	if ep == nil {
		return
	}
	pkg.Description = ep.Description
	pkg.Homepage = ep.URL
	pkg.Repository = urlparser.FirstRepoURL(ep.SCM.URL, ep.SCM.Connection)
	names := make([]string, len(ep.Licenses))
	for i, l := range ep.Licenses {
		names[i] = l.Name
	}
	pkg.Licenses = strings.Join(names, ",")
}

func (r *Registry) FetchVersions(ctx context.Context, name string) ([]core.Version, error) {
	groupID, artifactID, _ := ParseCoordinates(name)
	if groupID == "" || artifactID == "" {
		return nil, fmt.Errorf("invalid Maven coordinate: %s (expected groupId:artifactId)", name)
	}

	// Use search API to get all versions
	searchURL := fmt.Sprintf("%s/solrsearch/select?q=g:%s+AND+a:%s&core=gav&rows=200&wt=json",
		r.searchURL, url.QueryEscape(groupID), url.QueryEscape(artifactID))

	var searchResp searchResponse
	if err := r.client.GetJSON(ctx, searchURL, &searchResp); err == nil && searchResp.Response.NumFound > 0 {
		versions := make([]core.Version, len(searchResp.Response.Docs))
		for i, doc := range searchResp.Response.Docs {
			var publishedAt time.Time
			if doc.Timestamp > 0 {
				publishedAt = time.UnixMilli(doc.Timestamp)
			}
			versions[i] = core.Version{
				Number:      doc.Version,
				PublishedAt: publishedAt,
			}
		}
		return versions, nil
	}

	// Fallback: maven-metadata.xml
	metadataURL := fmt.Sprintf("%s/%s/%s/maven-metadata.xml",
		r.baseURL, groupIDToPath(groupID), artifactID)

	body, err := r.client.GetBody(ctx, metadataURL)
	if err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	var metadata mavenMetadata
	if err := xml.Unmarshal(body, &metadata); err != nil {
		return nil, err
	}

	versions := make([]core.Version, len(metadata.Versioning.Versions))
	for i, v := range metadata.Versioning.Versions {
		versions[i] = core.Version{
			Number: v,
		}
	}

	return versions, nil
}

func (r *Registry) FetchDependencies(ctx context.Context, name, version string) ([]core.Dependency, error) {
	groupID, artifactID, _ := ParseCoordinates(name)
	if groupID == "" || artifactID == "" {
		return nil, fmt.Errorf("invalid Maven coordinate: %s (expected groupId:artifactId)", name)
	}

	ep, err := r.effectivePOM(ctx, groupID, artifactID, version)
	if err != nil {
		var httpErr *core.HTTPError
		if errors.As(err, &httpErr) && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name, Version: version}
		}
		return nil, err
	}

	deps := make([]core.Dependency, 0, len(ep.Dependencies))
	for _, d := range ep.Dependencies {
		scope := mapMavenScope(d.Scope)
		if d.Optional {
			scope = core.Optional
		}
		deps = append(deps, core.Dependency{
			Name:         fmt.Sprintf("%s:%s", d.GroupID, d.ArtifactID),
			Requirements: d.Version,
			Scope:        scope,
			Optional:     d.Optional,
		})
	}

	return deps, nil
}

func mapMavenScope(scope string) core.Scope {
	switch strings.ToLower(scope) {
	case "compile", "":
		return core.Runtime
	case "test":
		return core.Test
	case "provided":
		return core.Build
	case "runtime":
		return core.Runtime
	default:
		return core.Runtime
	}
}

func (r *Registry) FetchMaintainers(ctx context.Context, name string) ([]core.Maintainer, error) {
	groupID, artifactID, _ := ParseCoordinates(name)
	if groupID == "" || artifactID == "" {
		return nil, fmt.Errorf("invalid Maven coordinate: %s (expected groupId:artifactId)", name)
	}

	// Get latest version first
	versions, err := r.FetchVersions(ctx, name)
	if err != nil {
		return nil, err
	}
	if len(versions) == 0 {
		return nil, nil
	}

	latestVersion := versions[0].Number
	body, err := r.client.GetBody(ctx, pom.POMURL(r.baseURL, pom.GAV{GroupID: groupID, ArtifactID: artifactID, Version: latestVersion}))
	if err != nil {
		return nil, err
	}
	var devs pomDevelopers
	if err := xml.Unmarshal(body, &devs); err != nil {
		return nil, err
	}

	maintainers := make([]core.Maintainer, len(devs.Developers))
	for i, dev := range devs.Developers {
		maintainers[i] = core.Maintainer{
			UUID:  dev.ID,
			Login: dev.ID,
			Name:  dev.Name,
			Email: dev.Email,
			URL:   dev.URL,
		}
	}

	return maintainers, nil
}

func groupIDToPath(groupID string) string {
	return strings.ReplaceAll(groupID, ".", "/")
}

type URLs struct {
	baseURL string
}

func (u *URLs) Registry(name, version string) string {
	groupID, artifactID, _ := ParseCoordinates(name)
	if version != "" {
		return fmt.Sprintf("https://search.maven.org/artifact/%s/%s/%s/jar", groupID, artifactID, version)
	}
	return fmt.Sprintf("https://search.maven.org/artifact/%s/%s", groupID, artifactID)
}

func (u *URLs) Download(name, version string) string {
	if version == "" {
		return ""
	}
	groupID, artifactID, _ := ParseCoordinates(name)
	return fmt.Sprintf("%s/%s/%s/%s/%s-%s.jar",
		u.baseURL, groupIDToPath(groupID), artifactID, version, artifactID, version)
}

func (u *URLs) Documentation(name, version string) string {
	groupID, artifactID, _ := ParseCoordinates(name)
	if version != "" {
		return fmt.Sprintf("https://javadoc.io/doc/%s/%s/%s", groupID, artifactID, version)
	}
	return fmt.Sprintf("https://javadoc.io/doc/%s/%s", groupID, artifactID)
}

func (u *URLs) PURL(name, version string) string {
	groupID, artifactID, _ := ParseCoordinates(name)
	if version != "" {
		return fmt.Sprintf("pkg:maven/%s/%s@%s", groupID, artifactID, version)
	}
	return fmt.Sprintf("pkg:maven/%s/%s", groupID, artifactID)
}
