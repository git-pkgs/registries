// Package npm provides a registry client for npmjs.com.
package npm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/git-pkgs/purl"
	"github.com/git-pkgs/registries/internal/core"
)

const (
	DefaultURL = "https://registry.npmjs.org"
	ecosystem  = "npm"
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

type versionInfo struct {
	Name         string                 `json:"name"`
	Version      string                 `json:"version"`
	Description  string                 `json:"description"`
	Keywords     interface{}            `json:"keywords"`
	License      interface{}            `json:"license"`
	Homepage     interface{}            `json:"homepage"`
	Repository   interface{}            `json:"repository"`
	Dependencies map[string]string      `json:"dependencies"`
	DevDeps      map[string]string      `json:"devDependencies"`
	OptionalDeps map[string]string      `json:"optionalDependencies"`
	Deprecated   deprecatedField        `json:"deprecated"`
	Dist         distInfo               `json:"dist"`
	Maintainers  []maintainerInfo       `json:"maintainers"`
	NpmUser      map[string]interface{} `json:"_npmUser"`
	Engines      interface{}            `json:"engines"`
	Funding      interface{}            `json:"funding"`

	ContentPolicy *ContentPolicy `json:"contentPolicy"`
}

// ContentPolicy is the npm package.json "contentPolicy" field, propagated
// into the packument version object. Maintainers set it to declare
// dual-use content (security-relevant capabilities that automated
// scanning could otherwise flag as malicious). See
// https://docs.npmjs.com/policies/dual-use.
type ContentPolicy struct {
	Class string `json:"class"` // "dual-use" is the only value npm defines today
}

// deprecatedField is the npm version "deprecated" field, which the packument
// spec defines as a string carrying the deprecation message. In practice some
// packuments emit a boolean instead (false meaning "not deprecated", true
// meaning deprecated with no message), which the registry historically
// accepted. UnmarshalJSON normalizes both shapes to the string form the rest
// of the code relies on: any non-empty value marks the version deprecated.
type deprecatedField string

// UnmarshalJSON accepts a string (the deprecation message), a boolean, or
// null/empty. Strings are kept verbatim; booleans map to "true" (deprecated)
// or "" (not deprecated) so the non-empty check driving StatusDeprecated keeps
// working.
func (d *deprecatedField) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*d = ""
		return nil
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return err
		}
		*d = deprecatedField(s)
		return nil
	}
	var b bool
	if err := json.Unmarshal(trimmed, &b); err != nil {
		return err
	}
	if b {
		*d = "true"
		return nil
	}
	*d = ""
	return nil
}

type distInfo struct {
	Shasum       string          `json:"shasum"`
	Tarball      string          `json:"tarball"`
	Integrity    string          `json:"integrity"`
	Attestations *AttestationRef `json:"attestations,omitempty"`
	Signatures   []Signature     `json:"signatures,omitempty"`
}

// AttestationRef is the npm dist.attestations pointer published
// alongside a version when the publisher used trusted publishing.
// The URL points at a separate endpoint that returns the signed
// sigstore bundle(s); Provenance.PredicateType hints at the in-toto
// predicate type living there (typically
// https://slsa.dev/provenance/v1).
type AttestationRef struct {
	URL        string `json:"url"`
	Provenance struct {
		PredicateType string `json:"predicateType"`
	} `json:"provenance"`
}

// Signature is one entry from a version's dist.signatures array — an
// ECDSA P-256 signature over "{name}@{version}:{integrity}" produced
// by the registry's signing key. Keyid identifies which key in
// /-/npm/v1/keys to verify against.
type Signature struct {
	Sig   string `json:"sig"`
	Keyid string `json:"keyid"`
}

type maintainerInfo struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

func (r *Registry) FetchPackage(ctx context.Context, name string) (*core.Package, error) {
	// dist-tags comes before versions in registry.npmjs.org packuments, so
	// usually only the latest version is decoded. If it hasn't been seen yet,
	// versions are kept raw until it is.
	var latest versionInfo
	var haveLatest bool
	var pending map[string]json.RawMessage
	var header *packumentHeader
	header, err := r.streamPackument(ctx, name, func(h *packumentHeader, number string, dec *json.Decoder) error {
		if want, ok := h.DistTags["latest"]; ok {
			if number != want || haveLatest {
				return skipValue(dec)
			}
			haveLatest = true
			return dec.Decode(&latest)
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return err
		}
		if pending == nil {
			pending = make(map[string]json.RawMessage)
		}
		pending[number] = raw
		return nil
	})
	if err != nil {
		return nil, err
	}

	latestVersion := header.DistTags["latest"]
	if !haveLatest && len(pending) > 0 {
		data, ok := pending[latestVersion]
		if !ok && latestVersion == "" {
			for _, v := range pending {
				data = v
				break
			}
		}
		if data != nil {
			if err := json.Unmarshal(data, &latest); err != nil {
				return nil, err
			}
		}
	}

	pkg := &core.Package{
		Name:          header.ID,
		Description:   coalesceString(latest.Description, header.Description),
		Homepage:      extractString(header.Homepage),
		Repository:    core.ExtractRepoURLWithFallback(latest.Repository, header.Repository),
		Licenses:      core.ExtractLicense(latest.License),
		Keywords:      extractKeywords(latest.Keywords),
		Namespace:     extractNamespace(header.ID),
		LatestVersion: latestVersion,
		Metadata: map[string]any{
			"dist-tags": header.DistTags,
			"funding":   latest.Funding,
		},
		FundingLinks: extractFundingLinks(latest.Funding),
	}
	if len(header.Maintainers) > 0 {
		pkg.Maintainers = convertMaintainers(header.Maintainers)
	}

	return pkg, nil
}

// packumentHeader is a packument without its versions.
type packumentHeader struct {
	ID          string            `json:"_id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Homepage    interface{}       `json:"homepage"`
	Repository  interface{}       `json:"repository"`
	Time        map[string]string `json:"time"`
	Maintainers []maintainerInfo  `json:"maintainers"`
	DistTags    map[string]string `json:"dist-tags"`
}

// streamPackument decodes a packument straight from the response body, so a
// document of tens of MB is never held whole. version is called with each
// entry of versions and must consume exactly one value from dec; header
// holds the top-level fields read so far.
func (r *Registry) streamPackument(ctx context.Context, name string, version func(h *packumentHeader, number string, dec *json.Decoder) error) (*packumentHeader, error) {
	endpoint := fmt.Sprintf("%s/%s", r.baseURL, url.PathEscape(name))
	var header packumentHeader
	err := r.client.StreamJSON(ctx, endpoint, func(dec *json.Decoder) error {
		if err := expectDelim(dec, '{'); err != nil {
			return err
		}
		for dec.More() {
			tok, err := dec.Token()
			if err != nil {
				return err
			}
			key, _ := tok.(string)
			var target any
			switch key {
			case "_id":
				target = &header.ID
			case "name":
				target = &header.Name
			case "description":
				target = &header.Description
			case "homepage":
				target = &header.Homepage
			case "repository":
				target = &header.Repository
			case "time":
				target = &header.Time
			case "maintainers":
				target = &header.Maintainers
			case "dist-tags":
				target = &header.DistTags
			case "versions":
				if err := decodeVersions(dec, &header, version); err != nil {
					return err
				}
				continue
			default:
				if err := skipValue(dec); err != nil {
					return err
				}
				continue
			}
			if err := dec.Decode(target); err != nil {
				return err
			}
		}
		return expectDelim(dec, '}')
	})
	if err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}
	return &header, nil
}

func decodeVersions(dec *json.Decoder, header *packumentHeader, version func(h *packumentHeader, number string, dec *json.Decoder) error) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if tok == nil {
		return nil
	}
	if tok != json.Delim('{') {
		return fmt.Errorf("npm packument: versions is not an object")
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		number, _ := tok.(string)
		if err := version(header, number, dec); err != nil {
			return err
		}
	}
	return expectDelim(dec, '}')
}

func expectDelim(dec *json.Decoder, want json.Delim) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if tok != want {
		return fmt.Errorf("npm packument: expected %q, got %v", want, tok)
	}
	return nil
}

// skipValue consumes one value without keeping it.
func skipValue(dec *json.Decoder) error {
	depth := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch tok {
		case json.Delim('{'), json.Delim('['):
			depth++
		case json.Delim('}'), json.Delim(']'):
			depth--
		}
		if depth == 0 {
			return nil
		}
	}
}

// decodeOne decodes the version named want and skips the rest.
func decodeOne(want string, info *versionInfo, found *bool) func(*packumentHeader, string, *json.Decoder) error {
	return func(_ *packumentHeader, number string, dec *json.Decoder) error {
		if number != want || *found {
			return skipValue(dec)
		}
		*found = true
		return dec.Decode(info)
	}
}

func (r *Registry) FetchVersions(ctx context.Context, name string) ([]core.Version, error) {
	var versions []core.Version
	header, err := r.streamPackument(ctx, name, func(_ *packumentHeader, number string, dec *json.Decoder) error {
		var v versionInfo
		if err := dec.Decode(&v); err != nil {
			return err
		}
		versions = append(versions, makeVersion(number, v, ""))
		return nil
	})
	if err != nil {
		return nil, err
	}

	// time can come after versions, so publication times are filled in last.
	for i := range versions {
		if ts := header.Time[versions[i].Number]; ts != "" {
			versions[i].PublishedAt, _ = time.Parse(time.RFC3339, ts)
		}
	}
	if versions == nil {
		versions = []core.Version{}
	}
	return versions, nil
}

// FetchVersion retrieves one release while retaining packument publication times.
func (r *Registry) FetchVersion(ctx context.Context, name, version string) (*core.Version, error) {
	var info versionInfo
	var found bool
	header, err := r.streamPackument(ctx, name, decodeOne(version, &info, &found))
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name, Version: version}
	}
	result := makeVersion(version, info, header.Time[version])
	return &result, nil
}

func makeVersion(num string, v versionInfo, timeStr string) core.Version {
	var publishedAt time.Time
	if timeStr != "" {
		publishedAt, _ = time.Parse(time.RFC3339, timeStr)
	}

	var status core.VersionStatus
	if v.Deprecated != "" {
		status = core.StatusDeprecated
	}

	integrity := v.Dist.Integrity
	if integrity == "" && v.Dist.Shasum != "" {
		integrity = "sha1-" + v.Dist.Shasum
	}

	return core.Version{
		Number:      num,
		PublishedAt: publishedAt,
		Licenses:    core.ExtractLicense(v.License),
		Integrity:   integrity,
		Status:      status,
		Metadata: map[string]any{
			"deprecated":        string(v.Deprecated),
			"dist":              v.Dist,
			"engines":           v.Engines,
			"_npmUser":          v.NpmUser,
			"tarball":           v.Dist.Tarball,
			"npm:attestations":  v.Dist.Attestations,
			"npm:signatures":    v.Dist.Signatures,
			"npm:contentPolicy": v.ContentPolicy,
		},
	}
}

func (r *Registry) FetchDependencies(ctx context.Context, name, version string) ([]core.Dependency, error) {
	var v versionInfo
	var found bool
	if _, err := r.streamPackument(ctx, name, decodeOne(version, &v, &found)); err != nil {
		return nil, err
	}
	if !found {
		return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name, Version: version}
	}

	var deps []core.Dependency

	for depName, req := range v.Dependencies {
		deps = append(deps, core.Dependency{
			Name:         depName,
			Requirements: req,
			Scope:        core.Runtime,
		})
	}

	for depName, req := range v.DevDeps {
		deps = append(deps, core.Dependency{
			Name:         depName,
			Requirements: req,
			Scope:        core.Development,
		})
	}

	for depName, req := range v.OptionalDeps {
		deps = append(deps, core.Dependency{
			Name:         depName,
			Requirements: req,
			Scope:        core.Optional,
			Optional:     true,
		})
	}

	return deps, nil
}

func (r *Registry) FetchMaintainers(ctx context.Context, name string) ([]core.Maintainer, error) {
	header, err := r.streamPackument(ctx, name, func(_ *packumentHeader, _ string, dec *json.Decoder) error {
		return skipValue(dec)
	})
	if err != nil {
		return nil, err
	}

	return convertMaintainers(header.Maintainers), nil
}

func convertMaintainers(infos []maintainerInfo) []core.Maintainer {
	maintainers := make([]core.Maintainer, len(infos))
	for i, m := range infos {
		maintainers[i] = core.Maintainer{
			UUID:  m.Name,
			Login: m.Name,
			Email: m.Email,
		}
	}
	return maintainers
}

// extractFundingLinks normalizes the package.json "funding" field, which may
// be a URL string, an object with a "url" key, or an array mixing both.
// Declaration order is kept since npm treats the first entry as primary.
func extractFundingLinks(v interface{}) []string {
	items, ok := v.([]interface{})
	if !ok {
		items = []interface{}{v}
	}
	var links []string
	for _, item := range items {
		switch f := item.(type) {
		case string:
			links = core.AppendFundingLink(links, f)
		case map[string]interface{}:
			if u, ok := f["url"].(string); ok {
				links = core.AppendFundingLink(links, u)
			}
		}
	}
	return links
}

func extractString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	if arr, ok := v.([]interface{}); ok && len(arr) > 0 {
		if s, ok := arr[0].(string); ok {
			return s
		}
	}
	return ""
}

func extractKeywords(v interface{}) []string {
	switch k := v.(type) {
	case []interface{}:
		keywords := make([]string, 0, len(k))
		for _, item := range k {
			if s, ok := item.(string); ok && s != "" {
				keywords = append(keywords, s)
			}
		}
		return keywords
	case []string:
		return k
	}
	return nil
}

func extractNamespace(id string) string {
	if strings.HasPrefix(id, "@") && strings.Contains(id, "/") {
		parts := strings.SplitN(id, "/", 2) //nolint:mnd // scope/name split
		return strings.TrimPrefix(parts[0], "@")
	}
	return ""
}

func coalesceString(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

type URLs struct {
	baseURL string
}

func (u *URLs) Registry(name, version string) string {
	if version != "" {
		return fmt.Sprintf("https://www.npmjs.com/package/%s/v/%s", name, version)
	}
	return fmt.Sprintf("https://www.npmjs.com/package/%s", name)
}

func (u *URLs) Download(name, version string) string {
	if version == "" {
		return ""
	}
	shortName := name
	if strings.Contains(name, "/") {
		parts := strings.SplitN(name, "/", 2) //nolint:mnd // scope/name split
		shortName = parts[1]
	}
	return fmt.Sprintf("%s/%s/-/%s-%s.tgz", u.baseURL, name, shortName, version)
}

func (u *URLs) Documentation(name, version string) string {
	if version != "" {
		return fmt.Sprintf("https://www.npmjs.com/package/%s/v/%s", name, version)
	}
	return fmt.Sprintf("https://www.npmjs.com/package/%s", name)
}

func (u *URLs) PURL(name, version string) string {
	return purl.BuildPURLString(ecosystem, name, version, "")
}
