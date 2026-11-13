// Package rpm provides a registry client for RPM repositories, read from
// their repodata.
package rpm

import (
	"compress/gzip"
	"context"
	"crypto/sha1" //nolint:gosec // repomd.xml checksums of old repositories
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/git-pkgs/purl"
	"github.com/git-pkgs/registries/internal/core"
	"github.com/git-pkgs/registries/internal/xzlimit"
	"github.com/git-pkgs/vers"
	"github.com/klauspost/compress/zstd"
)

const (
	DefaultURL = ""
	ecosystem  = "rpm"

	repomdPath = "repodata/repomd.xml"

	// maxPrimarySize bounds the decompressed primary metadata. Fedora 44
	// Everything x86_64 is about 185 MB.
	maxPrimarySize = 1 << 30
	// maxZstdWindow bounds the window a zstd frame may ask the decoder to
	// allocate. zstd --ultra -22 uses 128 MiB.
	maxZstdWindow = 128 << 20
)

var (
	// ErrInvalidConfig is returned when the repository URL is missing or
	// is not a URL.
	ErrInvalidConfig = errors.New("invalid RPM repository configuration")
	// ErrChecksumMismatch is returned when the primary metadata doesn't
	// match the checksum or size repomd.xml lists.
	ErrChecksumMismatch = errors.New("primary metadata does not match its repomd.xml checksum")
	// ErrMalformedMetadata is returned for repodata that cannot be parsed.
	ErrMalformedMetadata = errors.New("malformed RPM repository metadata")
	// ErrUnsupportedCompression is returned when the primary metadata is
	// compressed in a format other than zstd, xz or gzip.
	ErrUnsupportedCompression = errors.New("unsupported primary metadata compression")
)

func init() {
	core.Register(ecosystem, DefaultURL, func(baseURL string, client *core.Client) core.Registry {
		return New(baseURL, client)
	})
}

// Registry reads the primary metadata of one repository: the directory
// that holds repodata/, such as
// https://dl.fedoraproject.org/pub/fedora/linux/releases/43/Everything/x86_64/os.
// The release, variant and architecture are never inferred from a mirror
// root, and mirror lists and metalinks are not followed.
//
// Repository metadata doesn't name its distribution, so the distro to
// record on artifacts, as in the PURL qualifier, is given as a query
// parameter, which repositories don't use: .../x86_64/os?distro=fedora-43.
//
// The metadata is downloaded once per Registry and kept for its lifetime,
// so callers looking up many packages should reuse one Registry.
type Registry struct {
	repoURL   string
	distro    string
	configErr error
	client    *core.Client
	urls      *URLs

	maxPrimarySize int64

	mu       sync.Mutex
	packages map[string][]record
}

func New(baseURL string, client *core.Client) *Registry {
	if client == nil {
		client = core.DefaultClient()
	}
	r := &Registry{client: client, maxPrimarySize: maxPrimarySize}
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		r.configErr = fmt.Errorf("%w: %q is not a repository URL", ErrInvalidConfig, baseURL)
	} else {
		r.distro = u.Query().Get("distro")
		u.RawQuery, u.Fragment = "", ""
		r.repoURL = strings.TrimSuffix(u.String(), "/")
	}
	r.urls = &URLs{repositoryURL: r.repoURL}
	// As a PURL's repository_url carries it: the repository with the
	// distro it was configured with.
	if r.distro != "" {
		r.urls.repositoryURL += "?distro=" + r.distro
	}
	return r
}

func (r *Registry) Ecosystem() string {
	return ecosystem
}

func (r *Registry) URLs() core.URLBuilder { //nolint:ireturn
	return r.urls
}

// record is one package of the primary metadata.
type record struct {
	name          string
	arch          string
	epoch         string
	version       string // ver-rel
	checksumType  string
	checksum      string
	summary       string
	packager      string
	url           string
	license       string
	sourceRPM     string
	buildTime     time.Time
	size          int64
	installedSize int64
	href          string // "" when the file is not under the repository
	requires      []core.Dependency
}

// evr is the form versions are compared in.
func (rec record) evr() string {
	return rec.epoch + ":" + rec.version
}

// index reads the repository's metadata, once. Nothing is kept unless it
// was read and checked whole, so a failed read is tried again next time.
func (r *Registry) index(ctx context.Context) (map[string][]record, error) {
	if r.configErr != nil {
		return nil, r.configErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.packages != nil {
		return r.packages, nil
	}

	body, err := r.client.GetBody(ctx, r.repoURL+"/"+repomdPath)
	if err != nil {
		return nil, err
	}
	primary, err := parseRepomd(body)
	if err != nil {
		return nil, err
	}

	// Decided before the download, so an unsupported format costs none.
	if !supportedPrimary(primary.href) {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedCompression, primary.href)
	}

	packages := map[string][]record{}
	seen := map[string]bool{}
	primaryURL := r.repoURL + "/" + primary.href
	err = r.client.Stream(ctx, primaryURL, func(body io.Reader) error {
		return r.readPrimary(body, primary, primaryURL, func(rec record) {
			key := rec.name + "\x00" + rec.evr() + "\x00" + rec.arch
			if seen[key] {
				return
			}
			seen[key] = true
			packages[rec.name] = append(packages[rec.name], rec)
		})
	})
	if err != nil {
		return nil, err
	}
	for _, recs := range packages {
		sort.SliceStable(recs, func(i, j int) bool {
			return vers.CompareWithScheme(recs[i].evr(), recs[j].evr(), ecosystem) > 0
		})
	}
	r.packages = packages
	return packages, nil
}

// primaryData is repomd.xml's entry for the primary metadata.
type primaryData struct {
	href         string
	checksumType string // "" when repomd.xml lists no checksum
	checksum     string
	size         int64
}

func parseRepomd(data []byte) (*primaryData, error) {
	var repomd struct {
		Data []struct {
			Type     string `xml:"type,attr"`
			Checksum struct {
				Type  string `xml:"type,attr"`
				Value string `xml:",chardata"`
			} `xml:"checksum"`
			Location struct {
				Href string `xml:"href,attr"`
			} `xml:"location"`
			Size int64 `xml:"size"`
		} `xml:"data"`
	}
	if err := xml.Unmarshal(data, &repomd); err != nil {
		return nil, fmt.Errorf("%w: repomd.xml: %w", ErrMalformedMetadata, err)
	}
	for _, d := range repomd.Data {
		if d.Type != "primary" {
			continue
		}
		if !safeHref(d.Location.Href) {
			return nil, fmt.Errorf("%w: repomd.xml: primary location %q", ErrMalformedMetadata, d.Location.Href)
		}
		return &primaryData{
			href:         d.Location.Href,
			checksumType: strings.ToLower(strings.TrimSpace(d.Checksum.Type)),
			checksum:     strings.ToLower(strings.TrimSpace(d.Checksum.Value)),
			size:         d.Size,
		}, nil
	}
	return nil, fmt.Errorf("%w: repomd.xml lists no primary metadata", ErrMalformedMetadata)
}

func newHash(checksumType string) (hash.Hash, bool) {
	switch checksumType {
	case "sha256":
		return sha256.New(), true
	case "sha512":
		return sha512.New(), true
	case "sha384":
		return sha512.New384(), true
	case "sha224":
		return sha256.New224(), true
	case "sha", "sha1":
		return sha1.New(), true //nolint:gosec // as listed by the repository
	}
	return nil, false
}

// readPrimary decompresses and parses the primary metadata, checking the
// compressed bytes against the checksum and size repomd.xml lists.
func (r *Registry) readPrimary(body io.Reader, primary *primaryData, primaryURL string, fn func(record)) error {
	var hasher hash.Hash
	counted := &countingReader{r: body}
	if primary.checksumType != "" {
		h, ok := newHash(primary.checksumType)
		if !ok {
			return fmt.Errorf("%w: repomd.xml: checksum type %q", ErrMalformedMetadata, primary.checksumType)
		}
		hasher = h
		counted.r = io.TeeReader(body, hasher)
	}
	check := func() error {
		if hasher == nil {
			return nil
		}
		got := hex.EncodeToString(hasher.Sum(nil))
		if got != primary.checksum || (primary.size > 0 && counted.n != primary.size) {
			return fmt.Errorf("%w: %s is %d bytes with %s %s, repomd.xml lists %d bytes with %s",
				ErrChecksumMismatch, primaryURL, counted.n, primary.checksumType, got, primary.size, primary.checksum)
		}
		return nil
	}

	var decoded io.Reader
	switch {
	case strings.HasSuffix(primary.href, ".zst"):
		zr, err := zstd.NewReader(counted, zstd.WithDecoderMaxWindow(maxZstdWindow), zstd.WithDecoderConcurrency(1))
		if err != nil {
			return fmt.Errorf("%w: %w", ErrMalformedMetadata, err)
		}
		defer zr.Close()
		decoded = zr
	case strings.HasSuffix(primary.href, ".xz"):
		// Bounded as it decodes: a block asking for more is an error.
		decoded = xzlimit.NewReader(counted, xzlimit.MaxDict)
	case strings.HasSuffix(primary.href, ".gz"):
		gz, err := gzip.NewReader(counted)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrMalformedMetadata, err)
		}
		defer func() { _ = gz.Close() }()
		decoded = gz
	default:
		decoded = counted
	}

	limited := &io.LimitedReader{R: decoded, N: r.maxPrimarySize + 1}
	if err := parsePrimary(limited, fn); err != nil {
		if limited.N == 0 {
			return fmt.Errorf("%w: %s decompresses to more than %d bytes", ErrMalformedMetadata, primaryURL, r.maxPrimarySize)
		}
		return err
	}
	if limited.N == 0 {
		return fmt.Errorf("%w: %s decompresses to more than %d bytes", ErrMalformedMetadata, primaryURL, r.maxPrimarySize)
	}
	return check()
}

// supportedPrimary reports whether href names primary metadata the client
// can decode: zstd, xz, gzip or plain XML.
func supportedPrimary(href string) bool {
	for _, ext := range []string{".zst", ".xz", ".gz", ".xml"} {
		if strings.HasSuffix(href, ext) {
			return true
		}
	}
	return false
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

// xmlPackage is a <package> of primary.xml, with the fields the client uses.
type xmlPackage struct {
	Type    string `xml:"type,attr"`
	Name    string `xml:"name"`
	Arch    string `xml:"arch"`
	Version struct {
		Epoch string `xml:"epoch,attr"`
		Ver   string `xml:"ver,attr"`
		Rel   string `xml:"rel,attr"`
	} `xml:"version"`
	Checksum struct {
		Type  string `xml:"type,attr"`
		Value string `xml:",chardata"`
	} `xml:"checksum"`
	Summary  string `xml:"summary"`
	Packager string `xml:"packager"`
	URL      string `xml:"url"`
	Time     struct {
		Build int64 `xml:"build,attr"`
	} `xml:"time"`
	Size struct {
		Package   int64 `xml:"package,attr"`
		Installed int64 `xml:"installed,attr"`
	} `xml:"size"`
	Location struct {
		Href string `xml:"href,attr"`
		Base string `xml:"http://www.w3.org/XML/1998/namespace base,attr"`
	} `xml:"location"`
	Format struct {
		License   string `xml:"license"`
		SourceRPM string `xml:"sourcerpm"`
		Requires  []struct {
			Name  string `xml:"name,attr"`
			Flags string `xml:"flags,attr"`
			Epoch string `xml:"epoch,attr"`
			Ver   string `xml:"ver,attr"`
			Rel   string `xml:"rel,attr"`
		} `xml:"requires>entry"`
	} `xml:"format"`
}

// parsePrimary reads primary.xml as a stream, calling fn for each rpm
// package.
func parsePrimary(r io.Reader, fn func(record)) error {
	dec := xml.NewDecoder(r)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%w: primary.xml: %w", ErrMalformedMetadata, err)
		}
		start, ok := tok.(xml.StartElement)
		if !ok || start.Name.Local != "package" {
			continue
		}
		var pkg xmlPackage
		if err := dec.DecodeElement(&pkg, &start); err != nil {
			return fmt.Errorf("%w: primary.xml: %w", ErrMalformedMetadata, err)
		}
		if pkg.Type != "rpm" {
			continue
		}
		if pkg.Name == "" || pkg.Version.Ver == "" {
			return fmt.Errorf("%w: primary.xml: package without a name or version", ErrMalformedMetadata)
		}
		fn(pkg.record())
	}
}

func (pkg *xmlPackage) record() record {
	rec := record{
		name:          pkg.Name,
		arch:          pkg.Arch,
		epoch:         pkg.Version.Epoch,
		version:       evr("", pkg.Version.Ver, pkg.Version.Rel),
		checksumType:  strings.ToLower(pkg.Checksum.Type),
		checksum:      strings.ToLower(strings.TrimSpace(pkg.Checksum.Value)),
		summary:       pkg.Summary,
		packager:      pkg.Packager,
		url:           pkg.URL,
		license:       pkg.Format.License,
		sourceRPM:     pkg.Format.SourceRPM,
		size:          pkg.Size.Package,
		installedSize: pkg.Size.Installed,
	}
	if rec.epoch == "" {
		rec.epoch = "0"
	}
	if pkg.Time.Build > 0 {
		rec.buildTime = time.Unix(pkg.Time.Build, 0).UTC()
	}
	// xml:base points the file at another repository, which is not followed.
	if pkg.Location.Base == "" && safeHref(pkg.Location.Href) {
		rec.href = pkg.Location.Href
	}
	seen := map[string]bool{}
	for _, req := range pkg.Format.Requires {
		// File paths, sonames and capabilities such as rpmlib(...) name no
		// package.
		if req.Name == "" || strings.HasPrefix(req.Name, "/") || strings.Contains(req.Name, "(") || seen[req.Name] {
			continue
		}
		seen[req.Name] = true
		rec.requires = append(rec.requires, core.Dependency{
			Name:         req.Name,
			Requirements: requirement(req.Flags, req.Epoch, req.Ver, req.Rel),
			Scope:        core.Runtime,
		})
	}
	return rec
}

// evr joins an epoch, version and release, leaving out an epoch of 0 and
// an empty release.
func evr(epoch, ver, rel string) string {
	s := ver
	if epoch != "" && epoch != "0" {
		s = epoch + ":" + s
	}
	if rel != "" {
		s += "-" + rel
	}
	return s
}

// requirement renders a requires entry's version constraint.
func requirement(flags, epoch, ver, rel string) string {
	ops := map[string]string{"EQ": "=", "GE": ">=", "LE": "<=", "GT": ">", "LT": "<"}
	op, ok := ops[flags]
	if !ok || ver == "" {
		return ""
	}
	return op + " " + evr(epoch, ver, rel)
}

// safeHref reports whether a location stays under the repository root:
// relative, without dot segments, escapes or URL syntax.
func safeHref(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.ContainsAny(p, `\?#%:`) {
		return false
	}
	for _, segment := range strings.Split(p, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

// splitName strips a PURL vendor namespace: "fedora/curl" looks up "curl".
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
		Description:   latest.summary,
		Homepage:      latest.url,
		Licenses:      latest.license,
		Namespace:     namespace,
		LatestVersion: latest.version,
		Metadata: map[string]any{
			"epoch":      latest.epoch,
			"source_rpm": latest.sourceRPM,
		},
	}, nil
}

// FetchVersions returns one Version per epoch, version and release, newest
// first, with an artifact for each architecture. The version number is
// version-release, as in the rpm PURL type, where the epoch is a qualifier.
func (r *Registry) FetchVersions(ctx context.Context, name string) ([]core.Version, error) {
	recs, err := r.lookup(ctx, name)
	if err != nil {
		return nil, err
	}
	var versions []core.Version
	last := ""
	for _, rec := range recs {
		if len(versions) == 0 || last != rec.evr() {
			last = rec.evr()
			versions = append(versions, core.Version{
				Number:      rec.version,
				PublishedAt: rec.buildTime,
				Licenses:    rec.license,
				Metadata: map[string]any{
					"epoch":          rec.epoch,
					"source_rpm":     rec.sourceRPM,
					"packager":       rec.packager,
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
	if rec.href == "" {
		return core.Artifact{}, false
	}
	artifact := core.Artifact{
		URL:      r.repoURL + "/" + rec.href,
		Filename: path.Base(rec.href),
		Size:     rec.size,
		Metadata: map[string]any{"arch": rec.arch, "epoch": rec.epoch},
	}
	if r.distro != "" {
		artifact.Metadata["distro"] = r.distro
	}
	if rec.checksum != "" && rec.checksumType != "" {
		artifact.Integrity = rec.checksumType + "-" + rec.checksum
	}
	return artifact, true
}

func (r *Registry) FetchDependencies(ctx context.Context, name, version string) ([]core.Dependency, error) {
	recs, err := r.lookup(ctx, name)
	if err != nil {
		return nil, err
	}
	for _, rec := range recs {
		if rec.version == version {
			return rec.requires, nil
		}
	}
	return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name, Version: version}
}

func (r *Registry) FetchMaintainers(ctx context.Context, name string) ([]core.Maintainer, error) {
	recs, err := r.lookup(ctx, name)
	if err != nil {
		return nil, err
	}
	if recs[0].packager == "" {
		return nil, nil
	}
	return []core.Maintainer{{Name: recs[0].packager}}, nil
}

type URLs struct {
	repositoryURL string
}

func (u *URLs) Registry(name, version string) string {
	return ""
}

// Download returns "": a package's file is only known from the metadata.
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
