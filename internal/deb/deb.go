// Package deb provides a registry client for Debian repositories, read from
// their Packages indexes.
package deb

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/git-pkgs/purl"
	"github.com/git-pkgs/registries/internal/core"
	"github.com/git-pkgs/registries/internal/xzcheck"
	"github.com/git-pkgs/vers"
	"github.com/ulikunitz/xz"
)

const (
	DefaultURL = ""
	ecosystem  = "deb"

	// maxIndexSize bounds one decompressed Packages index. noble universe
	// amd64 is about 73 MB.
	maxIndexSize = 512 << 20
	// maxXZSize bounds a Packages.xz, which is read whole so its blocks can
	// be checked before decoding. sid main amd64 is about 11 MB.
	maxXZSize = 128 << 20

	archAll = "all"
)

var (
	// ErrInvalidConfig is returned when the repository URL names no
	// archive, suites or architectures, or asks for source packages.
	ErrInvalidConfig = errors.New("invalid Debian repository configuration")
	// ErrNoIndex is returned when no configured suite, component and
	// architecture has a Packages index.
	ErrNoIndex = errors.New("no Debian Packages index found")
	// ErrChecksumMismatch is returned when an index doesn't match the
	// SHA256 its suite's Release lists.
	ErrChecksumMismatch = errors.New("index does not match its Debian Release checksum")
	// ErrMalformedIndex is returned for Release files and indexes that
	// cannot be parsed.
	ErrMalformedIndex = errors.New("malformed Debian index")
)

// indexVariants are tried in this order, moving on after a 404.
var indexVariants = []string{"Packages.xz", "Packages.gz", "Packages"}

func init() {
	core.Register(ecosystem, DefaultURL, func(baseURL string, client *core.Client) core.Registry {
		return New(baseURL, client)
	})
}

// Registry reads the Packages indexes of one archive. The URL is the
// archive root, with the suites, components and architectures to read as
// query parameters, which archives don't use:
//
//	https://deb.debian.org/debian?suites=bookworm,bookworm-updates&components=main&architectures=amd64
//
// components defaults to main. An archive under another root, such as
// deb.debian.org/debian-security, is a Registry of its own.
//
// The indexes are downloaded once per Registry and kept for its lifetime,
// so callers looking up many packages should reuse one Registry.
type Registry struct {
	archiveURL    string
	suites        []string
	components    []string
	architectures []string
	configErr     error
	client        *core.Client
	urls          *URLs

	maxIndexSize int64
	maxXZSize    int64

	mu       sync.Mutex
	packages map[string][]record
}

func New(baseURL string, client *core.Client) *Registry {
	if client == nil {
		client = core.DefaultClient()
	}
	r := &Registry{client: client, maxIndexSize: maxIndexSize, maxXZSize: maxXZSize}
	r.configErr = r.configure(baseURL)
	r.urls = &URLs{repositoryURL: r.repositoryURL()}
	return r
}

// repositoryURL is the URL that configures this Registry, as a PURL's
// repository_url carries it: the archive root with what to read from it.
func (r *Registry) repositoryURL() string {
	if r.archiveURL == "" {
		return ""
	}
	return r.archiveURL + "?suites=" + strings.Join(r.suites, ",") +
		"&components=" + strings.Join(r.components, ",") +
		"&architectures=" + strings.Join(r.architectures, ",")
}

func (r *Registry) configure(baseURL string) error {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("%w: %q is not an archive URL", ErrInvalidConfig, baseURL)
	}
	q := u.Query()
	u.RawQuery, u.Fragment = "", ""
	r.archiveURL = strings.TrimSuffix(u.String(), "/")
	r.suites = splitList(q.Get("suites"))
	r.components = splitList(q.Get("components"))
	r.architectures = splitList(q.Get("architectures"))
	if len(r.components) == 0 {
		r.components = []string{"main"}
	}
	switch {
	case len(r.suites) == 0:
		return fmt.Errorf("%w: no suites in %q", ErrInvalidConfig, baseURL)
	case len(r.architectures) == 0:
		return fmt.Errorf("%w: no architectures in %q", ErrInvalidConfig, baseURL)
	}
	for _, list := range [][]string{r.suites, r.components, r.architectures} {
		for _, item := range list {
			if !safeSegment(item) {
				return fmt.Errorf("%w: %q", ErrInvalidConfig, item)
			}
		}
	}
	for _, arch := range r.architectures {
		if arch == "source" {
			return fmt.Errorf("%w: source packages are not supported", ErrInvalidConfig)
		}
	}
	return nil
}

func splitList(s string) []string {
	var out []string
	for _, item := range strings.Split(s, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// safeSegment refuses suite, component and architecture names that would
// leave the dists/ path they are placed in.
func safeSegment(s string) bool {
	return s != "." && s != ".." && !strings.ContainsAny(s, `/\?#%`)
}

func (r *Registry) Ecosystem() string {
	return ecosystem
}

func (r *Registry) URLs() core.URLBuilder { //nolint:ireturn
	return r.urls
}

// index reads every configured index, once. Nothing is kept unless all of
// them were read and checked, so a failed read is tried again next time.
func (r *Registry) index(ctx context.Context) (map[string][]record, error) {
	if r.configErr != nil {
		return nil, r.configErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.packages != nil {
		return r.packages, nil
	}

	packages := map[string][]record{}
	seen := map[string]bool{}
	add := func(rec record) {
		key := rec.name + "\x00" + rec.version + "\x00" + rec.arch
		if seen[key] {
			return
		}
		seen[key] = true
		packages[rec.name] = append(packages[rec.name], rec)
	}
	read := 0
	for _, suite := range r.suites {
		n, err := r.readSuite(ctx, suite, add)
		if err != nil {
			return nil, err
		}
		read += n
	}
	if read == 0 {
		return nil, fmt.Errorf("%w: under %s for suites %v", ErrNoIndex, r.archiveURL, r.suites)
	}
	for _, recs := range packages {
		sort.SliceStable(recs, func(i, j int) bool {
			return vers.CompareWithScheme(recs[i].version, recs[j].version, ecosystem) > 0
		})
	}
	r.packages = packages
	return packages, nil
}

// readSuite reads a suite's indexes and returns how many it found.
func (r *Registry) readSuite(ctx context.Context, suite string, add func(record)) (int, error) {
	rel, err := r.fetchRelease(ctx, suite)
	if err != nil {
		return 0, err
	}
	codename := suite
	if rel != nil && rel.codename != "" {
		codename = rel.codename
	}

	read := 0
	for _, component := range r.components {
		dirs := make([]string, 0, len(r.architectures)+1)
		for _, arch := range r.architectures {
			dirs = append(dirs, component+"/binary-"+arch)
		}
		// Architecture: all packages are in every binary-<arch> index,
		// and in binary-all where the Release lists one.
		if rel != nil && rel.lists(component+"/binary-"+archAll) {
			dirs = append(dirs, component+"/binary-"+archAll)
		}
		for _, dir := range dirs {
			found, err := r.readIndex(ctx, suite, dir, rel, func(rec record) {
				rec.suite, rec.component, rec.distro = suite, component, distro(codename)
				add(rec)
			})
			if err != nil {
				return 0, err
			}
			if found {
				read++
			}
		}
	}
	return read, nil
}

// fetchRelease reads InRelease, falling back to Release. It returns nil
// when the suite has neither, and its indexes are read unchecked.
func (r *Registry) fetchRelease(ctx context.Context, suite string) (*release, error) {
	for _, name := range []string{"InRelease", "Release"} {
		body, err := r.client.GetBody(ctx, r.archiveURL+"/dists/"+suite+"/"+name)
		if isNotFound(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return parseRelease(body)
	}
	return nil, nil
}

// lists reports whether the Release has an index variant for dir.
func (rel *release) lists(dir string) bool {
	for _, variant := range indexVariants {
		if _, ok := rel.files[dir+"/"+variant]; ok {
			return true
		}
	}
	return false
}

// readIndex reads the first variant of dir's index that is served. With a
// Release, only variants it lists are tried, and each is checked against
// its entry. It reports whether an index was read.
func (r *Registry) readIndex(ctx context.Context, suite, dir string, rel *release, fn func(record)) (bool, error) {
	for _, variant := range indexVariants {
		var sum *fileSum
		if rel != nil && len(rel.files) > 0 {
			entry, ok := rel.files[dir+"/"+variant]
			if !ok {
				continue
			}
			sum = &entry
		}
		indexURL := r.archiveURL + "/dists/" + suite + "/" + dir + "/" + variant
		var recs []record
		err := r.client.Stream(ctx, indexURL, func(body io.Reader) error {
			var err error
			recs, err = r.parseIndex(body, variant, indexURL, sum)
			return err
		})
		if isNotFound(err) {
			continue
		}
		if err != nil {
			return false, err
		}
		for _, rec := range recs {
			fn(rec)
		}
		return true, nil
	}
	return false, nil
}

// parseIndex decompresses and parses one index, checking the compressed
// bytes against sum when there is one.
func (r *Registry) parseIndex(body io.Reader, variant, indexURL string, sum *fileSum) ([]record, error) {
	hasher := sha256.New()
	counted := &countingReader{r: io.TeeReader(body, hasher)}

	var decoded io.Reader
	switch variant {
	case "Packages.xz":
		data, err := io.ReadAll(io.LimitReader(counted, r.maxXZSize+1))
		if err != nil {
			return nil, err
		}
		if int64(len(data)) > r.maxXZSize {
			return nil, fmt.Errorf("%w: %s is larger than %d bytes", ErrMalformedIndex, indexURL, r.maxXZSize)
		}
		// Checked before decoding, so a bad file never reaches the decoder.
		if err := checkSum(hasher, counted.n, sum, indexURL); err != nil {
			return nil, err
		}
		if err := xzcheck.Check(data, xzcheck.MaxDict); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrMalformedIndex, err)
		}
		xr, err := xz.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrMalformedIndex, err)
		}
		decoded = xr
	case "Packages.gz":
		gz, err := gzip.NewReader(counted)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrMalformedIndex, err)
		}
		defer func() { _ = gz.Close() }()
		decoded = gz
	default:
		decoded = counted
	}

	limited := &io.LimitedReader{R: decoded, N: r.maxIndexSize + 1}
	var recs []record
	if err := parseStanzas(limited, func(rec record) { recs = append(recs, rec) }); err != nil {
		return nil, err
	}
	if limited.N == 0 {
		return nil, fmt.Errorf("%w: %s decompresses to more than %d bytes", ErrMalformedIndex, indexURL, r.maxIndexSize)
	}
	// The gzip reader and the parser read to the end of the body.
	if variant != "Packages.xz" {
		if err := checkSum(hasher, counted.n, sum, indexURL); err != nil {
			return nil, err
		}
	}
	return recs, nil
}

func checkSum(h hash.Hash, size int64, sum *fileSum, indexURL string) error {
	if sum == nil {
		return nil
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != sum.sha256 || size != sum.size {
		return fmt.Errorf("%w: %s is %d bytes with SHA256 %s, Release lists %d bytes with %s",
			ErrChecksumMismatch, indexURL, size, got, sum.size, sum.sha256)
	}
	return nil
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func isNotFound(err error) bool {
	var httpErr *core.HTTPError
	return errors.As(err, &httpErr) && httpErr.IsNotFound()
}

// splitName strips a PURL vendor namespace: "debian/curl" looks up "curl".
func splitName(name string) (namespace, pkg string) {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[:i], name[i+1:]
	}
	return "", name
}

func (r *Registry) lookup(ctx context.Context, name string) ([]record, error) {
	packages, err := r.index(ctx)
	if err != nil {
		return nil, err
	}
	_, pkg := splitName(name)
	recs := packages[pkg]
	if len(recs) == 0 {
		return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
	}
	return recs, nil
}

func (r *Registry) FetchPackage(ctx context.Context, name string) (*core.Package, error) {
	recs, err := r.lookup(ctx, name)
	if err != nil {
		return nil, err
	}
	namespace, _ := splitName(name)
	latest := recs[0]
	return &core.Package{
		Name:          latest.name,
		Description:   latest.description,
		Homepage:      latest.homepage,
		Namespace:     namespace,
		LatestVersion: latest.version,
		Metadata: map[string]any{
			"source":  latest.source,
			"section": latest.section,
		},
	}, nil
}

// FetchVersions returns one Version per version number, newest first, with
// an artifact for each architecture and suite it was found in.
func (r *Registry) FetchVersions(ctx context.Context, name string) ([]core.Version, error) {
	recs, err := r.lookup(ctx, name)
	if err != nil {
		return nil, err
	}
	var versions []core.Version
	for _, rec := range recs {
		if len(versions) == 0 || versions[len(versions)-1].Number != rec.version {
			versions = append(versions, core.Version{
				Number: rec.version,
				Metadata: map[string]any{
					"source":         rec.source,
					"section":        rec.section,
					"maintainer":     rec.maintainer,
					"installed_size": rec.installedSize,
				},
			})
		}
		if artifact, ok := r.artifact(rec); ok {
			v := &versions[len(versions)-1]
			v.Artifacts = append(v.Artifacts, artifact)
		}
	}
	return versions, nil
}

func (r *Registry) artifact(rec record) (core.Artifact, bool) {
	if !safePoolPath(rec.filename) {
		return core.Artifact{}, false
	}
	artifact := core.Artifact{
		URL:      r.archiveURL + "/" + rec.filename,
		Filename: path.Base(rec.filename),
		Size:     rec.size,
		Metadata: map[string]any{
			"arch":      rec.arch,
			"distro":    rec.distro,
			"suite":     rec.suite,
			"component": rec.component,
		},
	}
	if rec.sha256 != "" {
		artifact.Integrity = "sha256-" + strings.ToLower(rec.sha256)
	}
	return artifact, true
}

func (r *Registry) FetchDependencies(ctx context.Context, name, version string) ([]core.Dependency, error) {
	recs, err := r.lookup(ctx, name)
	if err != nil {
		return nil, err
	}
	for _, rec := range recs {
		if rec.version != version {
			continue
		}
		var deps []core.Dependency
		for _, d := range parseDependencies(rec.preDepends, rec.depends) {
			deps = append(deps, core.Dependency{Name: d.name, Requirements: d.requirement, Scope: core.Runtime})
		}
		return deps, nil
	}
	return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name, Version: version}
}

func (r *Registry) FetchMaintainers(ctx context.Context, name string) ([]core.Maintainer, error) {
	recs, err := r.lookup(ctx, name)
	if err != nil {
		return nil, err
	}
	if recs[0].maintainer == "" {
		return nil, nil
	}
	return []core.Maintainer{parseMaintainer(recs[0].maintainer)}, nil
}

// parseMaintainer parses "Name <email>"; any other value is kept whole as the name.
func parseMaintainer(value string) core.Maintainer {
	if open := strings.LastIndex(value, "<"); open >= 0 && strings.HasSuffix(value, ">") {
		name := strings.TrimSpace(value[:open])
		email := value[open+1 : len(value)-1]
		if email != "" && strings.Contains(email, "@") && !strings.ContainsAny(email, "<> ") {
			return core.Maintainer{Name: name, Email: email}
		}
	}
	return core.Maintainer{Name: strings.TrimSpace(value)}
}

type URLs struct {
	repositoryURL string
}

func (u *URLs) Registry(name, version string) string {
	return ""
}

// Download returns "": a package's file is only known from the index.
func (u *URLs) Download(name, version string) string {
	return ""
}

func (u *URLs) Documentation(name, version string) string {
	return ""
}

func (u *URLs) PURL(name, version string) string {
	namespace, pkg := splitName(name)
	var qualifiers map[string]string
	if u.repositoryURL != "" {
		qualifiers = map[string]string{"repository_url": u.repositoryURL}
	}
	return purl.New(ecosystem, namespace, pkg, version, qualifiers).String()
}
