// Package alpm provides a registry client for pacman sync databases, as
// used by Arch Linux and other users of libalpm.
package alpm

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
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
	ecosystem  = "alpm"

	// maxDatabaseSize bounds the decompressed database. Arch's extra.db is
	// about 40 MB as a tar, and a .files database about ten times that.
	maxDatabaseSize = 1 << 30
	// maxEntrySize bounds one desc or depends file. pacman reads lines of
	// up to 512 KiB.
	maxEntrySize = 1 << 20
	// maxZstdWindow bounds the window a zstd frame may ask the decoder to
	// allocate. zstd --ultra -22 uses 128 MiB.
	maxZstdWindow = 128 << 20

	archKey = "arch"
)

var (
	// ErrInvalidConfig is returned when the database URL is missing or does
	// not name a file.
	ErrInvalidConfig = errors.New("invalid ALPM database configuration")
	// ErrMalformedDatabase is returned for sync databases that cannot be
	// read, hold unsafe paths, or have inconsistent package records.
	ErrMalformedDatabase = errors.New("malformed ALPM sync database")
)

func init() {
	core.Register(ecosystem, DefaultURL, func(baseURL string, client *core.Client) core.Registry {
		return New(baseURL, client)
	})
}

// Registry reads one pacman sync database, such as
// https://geo.mirror.pkgbuild.com/core/os/x86_64/core.db. The URL names the
// repository and architecture; neither is inferred from a mirror root.
// Package files are resolved from each package's FILENAME, relative to the
// database's directory.
//
// The database is downloaded once per Registry and kept for its lifetime,
// so callers looking up many packages should reuse one Registry.
type Registry struct {
	dbURL     string
	dirURL    string
	configErr error
	client    *core.Client
	urls      *URLs

	maxDatabaseSize int64

	mu  sync.Mutex
	idx *dbIndex
}

type dbIndex struct {
	packages map[string][]record
	// providers maps a name from PROVIDES, such as libalpm.so, to the
	// packages providing it, ordered by package name.
	providers map[string][]provision
}

// provision is one PROVIDES entry: the providing package and the version
// it provides, empty when the entry names none.
type provision struct {
	pkg     string
	version string
}

func New(baseURL string, client *core.Client) *Registry {
	if client == nil {
		client = core.DefaultClient()
	}
	r := &Registry{client: client, maxDatabaseSize: maxDatabaseSize}
	u, err := url.Parse(baseURL)
	switch {
	case err != nil || u.Scheme == "" || u.Host == "":
		r.configErr = fmt.Errorf("%w: %q is not a database URL", ErrInvalidConfig, baseURL)
	case u.Path == "" || strings.HasSuffix(u.Path, "/"):
		r.configErr = fmt.Errorf("%w: %q names a directory, not a sync database such as core.db", ErrInvalidConfig, baseURL)
	default:
		r.dbURL = baseURL
		u.RawQuery, u.Fragment = "", ""
		s := u.String()
		r.dirURL = s[:strings.LastIndex(s, "/")]
	}
	r.urls = &URLs{dbURL: r.dbURL}
	return r
}

func (r *Registry) Ecosystem() string {
	return ecosystem
}

func (r *Registry) URLs() core.URLBuilder { //nolint:ireturn
	return r.urls
}

// record is one package of the sync database, from its desc file and, in
// older databases, its depends file.
type record struct {
	name          string
	version       string
	base          string
	filename      string
	description   string
	url           string
	arch          string
	packager      string
	md5sum        string
	sha256sum     string
	buildTime     time.Time
	size          int64
	installedSize int64
	groups        []string
	licenses      []string
	depends       []string
	optDepends    []string
	makeDepends   []string
	checkDepends  []string
	conflicts     []string
	provides      []string
	replaces      []string
}

// splitName strips a PURL vendor namespace: "arch/pacman" looks up "pacman".
func splitName(name string) (namespace, pkg string) {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[:i], name[i+1:]
	}
	return "", name
}

// index reads the database, once. Nothing is kept unless it was read
// whole, so a failed read is tried again next time.
func (r *Registry) index(ctx context.Context) (*dbIndex, error) {
	if r.configErr != nil {
		return nil, r.configErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.idx != nil {
		return r.idx, nil
	}

	var records []record
	err := r.client.Stream(ctx, r.dbURL, func(body io.Reader) error {
		var err error
		records, err = r.readDatabase(body)
		return err
	})
	if err != nil {
		return nil, err
	}

	packages := make(map[string][]record)
	for _, rec := range records {
		packages[rec.name] = append(packages[rec.name], rec)
	}
	for _, recs := range packages {
		sort.SliceStable(recs, func(i, j int) bool {
			return vers.CompareWithScheme(recs[i].version, recs[j].version, ecosystem) > 0
		})
	}
	r.idx = &dbIndex{packages: packages, providers: buildProviders(records)}
	return r.idx, nil
}

// buildProviders lists the packages providing each name, ordered by
// package name so the choice among them is stable. pacman lists providers
// in that order and takes the first when not asked to choose.
func buildProviders(records []record) map[string][]provision {
	providers := make(map[string][]provision)
	for _, rec := range records {
		for _, p := range rec.provides {
			provided, requirement := splitConstraint(p)
			// Only "name=version" provides a version; pacman ignores any
			// other operator in PROVIDES.
			version, ok := strings.CutPrefix(requirement, "=")
			if !ok {
				version = ""
			}
			providers[provided] = append(providers[provided], provision{pkg: rec.name, version: version})
		}
	}
	for _, provs := range providers {
		sort.SliceStable(provs, func(i, j int) bool { return provs[i].pkg < provs[j].pkg })
	}
	return providers
}

// provider returns the first package, by name, whose PROVIDES satisfies a
// dependency on name, as pacman's _alpm_depcmp_provides decides: any
// provision of the name meets an unversioned dependency, but a versioned
// one needs a provided version that meets its constraint.
func (idx *dbIndex) provider(name, requirement string) (string, bool) {
	for _, p := range idx.providers[name] {
		if requirement == "" || satisfies(p.version, requirement) {
			return p.pkg, true
		}
	}
	return "", false
}

// satisfies reports whether a provided version meets a requirement such as
// ">=17", compared as pacman's dep_vercmp does. A provision without a
// version meets none.
func satisfies(version, requirement string) bool {
	if version == "" {
		return false
	}
	for _, op := range []string{">=", "<=", "=", ">", "<"} {
		want, ok := strings.CutPrefix(requirement, op)
		if !ok {
			continue
		}
		if want == "" {
			return false
		}
		cmp := vers.CompareWithScheme(version, want, ecosystem)
		switch op {
		case ">=":
			return cmp >= 0
		case "<=":
			return cmp <= 0
		case "=":
			return cmp == 0
		case ">":
			return cmp > 0
		case "<":
			return cmp < 0
		}
	}
	return false
}

// readDatabase decompresses a sync database, detecting the format from its
// first bytes as libarchive does, since core.db names no compression.
func (r *Registry) readDatabase(body io.Reader) ([]record, error) {
	br := bufio.NewReader(body)
	magic, err := br.Peek(6)
	if err != nil && len(magic) == 0 {
		if err == io.EOF {
			return nil, fmt.Errorf("%w: %s is empty", ErrMalformedDatabase, r.dbURL)
		}
		return nil, err
	}

	var decoded io.Reader
	switch {
	case bytes.HasPrefix(magic, []byte{0x1f, 0x8b}):
		gz, err := gzip.NewReader(br)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrMalformedDatabase, err)
		}
		defer func() { _ = gz.Close() }()
		decoded = gz
	case bytes.HasPrefix(magic, []byte{0x28, 0xb5, 0x2f, 0xfd}):
		zr, err := zstd.NewReader(br, zstd.WithDecoderMaxWindow(maxZstdWindow), zstd.WithDecoderConcurrency(1))
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrMalformedDatabase, err)
		}
		defer zr.Close()
		decoded = zr
	case bytes.HasPrefix(magic, []byte{0xfd, '7', 'z', 'X', 'Z', 0x00}):
		// Bounded as it decodes: a block asking for more is an error.
		decoded = xzlimit.NewReader(br, xzlimit.MaxDict)
	default:
		decoded = br // an uncompressed tar
	}

	limited := &io.LimitedReader{R: decoded, N: r.maxDatabaseSize + 1}
	records, err := parseDatabase(limited)
	if limited.N == 0 {
		return nil, fmt.Errorf("%w: %s decompresses to more than %d bytes", ErrMalformedDatabase, r.dbURL, r.maxDatabaseSize)
	}
	if err != nil {
		return nil, err
	}
	// tar's end can precede the compressed stream's trailer and checksum.
	if _, err := io.Copy(io.Discard, limited); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMalformedDatabase, err)
	}
	if limited.N == 0 {
		return nil, fmt.Errorf("%w: %s decompresses to more than %d bytes", ErrMalformedDatabase, r.dbURL, r.maxDatabaseSize)
	}
	return records, nil
}

// parseDatabase reads the tar of a sync database: a directory named
// name-version per package, holding desc and, in older databases, depends.
// files lists are skipped.
func parseDatabase(r io.Reader) ([]record, error) {
	tr := tar.NewReader(r)
	entries := make(map[string]*record)
	var order []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrMalformedDatabase, err)
		}
		dir, file, err := entryPath(hdr.Name)
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag != tar.TypeReg || dir == "" || (file != "desc" && file != "depends") {
			continue
		}
		if hdr.Size > maxEntrySize {
			return nil, fmt.Errorf("%w: %s is larger than %d bytes", ErrMalformedDatabase, hdr.Name, maxEntrySize)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrMalformedDatabase, err)
		}
		rec, ok := entries[dir]
		if !ok {
			rec = &record{}
			entries[dir] = rec
			order = append(order, dir)
		}
		if err := rec.parse(data); err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrMalformedDatabase, hdr.Name, err)
		}
	}

	records := make([]record, 0, len(order))
	for _, dir := range order {
		rec := entries[dir]
		if err := rec.check(dir); err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrMalformedDatabase, dir, err)
		}
		records = append(records, *rec)
	}
	return records, nil
}

// entryPath splits an archive path into its package directory and file
// name, refusing paths that are absolute, climb out or nest deeper than
// one directory. A file outside any directory has an empty dir.
func entryPath(name string) (dir, file string, err error) {
	if strings.HasPrefix(name, "/") || strings.Contains(name, `\`) {
		return "", "", fmt.Errorf("%w: unsafe archive path %q", ErrMalformedDatabase, name)
	}
	clean := strings.TrimSuffix(strings.TrimPrefix(name, "./"), "/")
	if clean == "" || clean == "." {
		return "", "", nil // the archive's root directory
	}
	segments := strings.Split(clean, "/")
	unsafe := len(segments) > 2
	for _, s := range segments {
		if s == "" || s == "." || s == ".." {
			unsafe = true
		}
	}
	if unsafe {
		return "", "", fmt.Errorf("%w: unsafe archive path %q", ErrMalformedDatabase, name)
	}
	if len(segments) == 1 {
		return "", "", nil
	}
	return segments[0], segments[1], nil
}

// parse reads the %KEY% sections of a desc or depends file. A section's
// values run to a blank line, as pacman's be_sync.c reads them, or to the
// next %KEY% line, which alpm-repo-desc(5) also lets end a section.
func (rec *record) parse(data []byte) error {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		key := lines[i]
		if key == "" {
			continue
		}
		if !isSectionHeader(key) {
			return fmt.Errorf("line %d: %q is not a %%KEY%% line", i+1, key)
		}
		var values []string
		for i+1 < len(lines) && lines[i+1] != "" && !isSectionHeader(lines[i+1]) {
			i++
			values = append(values, lines[i])
		}
		if err := rec.set(key[1:len(key)-1], values); err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
	}
	return nil
}

// isSectionHeader reports whether a line is a %KEY% line.
func isSectionHeader(line string) bool {
	return len(line) >= 3 && line[0] == '%' && line[len(line)-1] == '%'
}

func (rec *record) set(key string, values []string) error {
	first := ""
	if len(values) > 0 {
		first = values[0]
	}
	var err error
	switch key {
	case "NAME":
		rec.name = first
	case "VERSION":
		rec.version = first
	case "BASE":
		rec.base = first
	case "FILENAME":
		rec.filename = first
	case "DESC":
		rec.description = first
	case "URL":
		rec.url = first
	case "ARCH":
		rec.arch = first
	case "PACKAGER":
		rec.packager = first
	case "MD5SUM":
		rec.md5sum = strings.ToLower(first)
	case "SHA256SUM":
		rec.sha256sum = strings.ToLower(first)
	case "CSIZE":
		rec.size, err = parseSize(first)
	case "ISIZE":
		rec.installedSize, err = parseSize(first)
	case "BUILDDATE":
		var secs int64
		if secs, err = strconv.ParseInt(first, 10, 64); err == nil {
			rec.buildTime = time.Unix(secs, 0).UTC()
		}
	case "GROUPS":
		rec.groups = values
	case "LICENSE":
		rec.licenses = values
	case "DEPENDS":
		rec.depends = values
	case "OPTDEPENDS":
		rec.optDepends = values
	case "MAKEDEPENDS":
		rec.makeDepends = values
	case "CHECKDEPENDS":
		rec.checkDepends = values
	case "CONFLICTS":
		rec.conflicts = values
	case "PROVIDES":
		rec.provides = values
	case "REPLACES":
		rec.replaces = values
	}
	return err
}

// parseSize reads CSIZE or ISIZE, a byte count, refusing a sign as
// pacman's _alpm_strtoofft does.
func parseSize(value string) (int64, error) {
	n, err := strconv.ParseUint(value, 10, 63)
	return int64(n), err
}

// check holds a record to its directory name, name-pkgver-pkgrel, which
// pacman takes the package's name and version from.
func (rec *record) check(dir string) error {
	name, version, ok := splitEntryName(dir)
	if !ok {
		return errors.New("directory is not named name-version")
	}
	if rec.name == "" {
		rec.name = name
	}
	if rec.version == "" {
		rec.version = version
	}
	if rec.name != name || rec.version != version {
		return fmt.Errorf("NAME %q and VERSION %q do not match the directory", rec.name, rec.version)
	}
	// pacman refuses a FILENAME with a path, or one starting with a dot.
	if rec.filename != "" && !safeFilename(rec.filename) {
		return fmt.Errorf("FILENAME %q is not a file name", rec.filename)
	}
	return nil
}

// splitEntryName splits a directory name at its second-last hyphen, since
// package names may contain hyphens and versions end in -pkgrel.
func splitEntryName(dir string) (name, version string, ok bool) {
	rel := strings.LastIndex(dir, "-")
	if rel <= 0 {
		return "", "", false
	}
	ver := strings.LastIndex(dir[:rel], "-")
	if ver <= 0 || rel == len(dir)-1 || ver == rel-1 {
		return "", "", false
	}
	return dir[:ver], dir[ver+1:], true
}

// safeFilename reports whether a FILENAME is a plain file name in the
// database's directory, with nothing a URL would read as a path, query or
// escape.
func safeFilename(name string) bool {
	return name != "" && !strings.HasPrefix(name, ".") && !strings.ContainsAny(name, `/\?#%`)
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

func (r *Registry) lookup(ctx context.Context, name string) (*dbIndex, []record, error) {
	idx, err := r.index(ctx)
	if err != nil {
		return nil, nil, err
	}
	_, pkg := splitName(name)
	recs := idx.packages[pkg]
	if len(recs) == 0 {
		return nil, nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
	}
	return idx, recs, nil
}

func (r *Registry) FetchPackage(ctx context.Context, name string) (*core.Package, error) {
	_, recs, err := r.lookup(ctx, name)
	if err != nil {
		return nil, err
	}
	namespace, _ := splitName(name)
	latest := recs[0]
	return &core.Package{
		Name:          latest.name,
		Description:   latest.description,
		Homepage:      latest.url,
		Licenses:      joinLicenses(latest.licenses),
		Namespace:     namespace,
		LatestVersion: latest.version,
		Metadata: map[string]any{
			"base":   latest.base,
			"groups": latest.groups,
			archKey:  latest.arch,
		},
	}, nil
}

// joinLicenses joins LICENSE lines, each of which applies to the package.
// A line may itself be an SPDX expression such as "Apache-2.0 OR MIT", so
// when there are several, each such line is wrapped in parentheses first.
func joinLicenses(licenses []string) string {
	if len(licenses) == 1 {
		return licenses[0]
	}
	grouped := make([]string, len(licenses))
	for i, l := range licenses {
		if strings.Contains(l, " ") {
			l = "(" + l + ")"
		}
		grouped[i] = l
	}
	return strings.Join(grouped, " AND ")
}

func (r *Registry) FetchVersions(ctx context.Context, name string) ([]core.Version, error) {
	_, recs, err := r.lookup(ctx, name)
	if err != nil {
		return nil, err
	}
	versions := make([]core.Version, 0, len(recs))
	for _, rec := range recs {
		versions = append(versions, r.version(rec))
	}
	return versions, nil
}

func (r *Registry) version(rec record) core.Version {
	metadata := map[string]any{
		archKey:          rec.arch,
		"base":           rec.base,
		"description":    rec.description,
		"url":            rec.url,
		"packager":       rec.packager,
		"filename":       rec.filename,
		"size":           rec.size,
		"installed_size": rec.installedSize,
		"groups":         rec.groups,
		"licenses":       rec.licenses,
		"depends":        rec.depends,
		"optdepends":     rec.optDepends,
		"makedepends":    rec.makeDepends,
		"checkdepends":   rec.checkDepends,
		"conflicts":      rec.conflicts,
		"provides":       rec.provides,
		"replaces":       rec.replaces,
	}
	if rec.md5sum != "" {
		metadata["md5sum"] = rec.md5sum
	}

	v := core.Version{
		Number:      rec.version,
		PublishedAt: rec.buildTime,
		Licenses:    joinLicenses(rec.licenses),
		Metadata:    metadata,
	}
	// SHA256SUM covers the whole package file, which pacman checks it against.
	if raw, err := hex.DecodeString(rec.sha256sum); err == nil && len(raw) == 32 {
		v.Integrity = "sha256-" + rec.sha256sum
	}
	if rec.filename != "" {
		v.Artifacts = []core.Artifact{{
			URL:       r.dirURL + "/" + rec.filename,
			Filename:  rec.filename,
			Integrity: v.Integrity,
			Size:      rec.size,
			Metadata:  map[string]any{archKey: rec.arch},
		}}
	}
	return v
}

func (r *Registry) FetchDependencies(ctx context.Context, name, version string) ([]core.Dependency, error) {
	idx, recs, err := r.lookup(ctx, name)
	if err != nil {
		return nil, err
	}
	for _, rec := range recs {
		if rec.version == version {
			return idx.dependencies(rec), nil
		}
	}
	return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name, Version: version}
}

// dependencies lists DEPENDS as runtime and OPTDEPENDS as optional
// dependencies. A provided name such as libalpm.so is resolved to the
// package providing a version that meets its constraint; the constraint
// applies to the provided version, not the package's, so it is dropped. A
// provided name that nothing in this database satisfies, usually one from
// another repository, is kept as is.
// Make, check, conflict, replace and provide relationships are in the
// version's metadata.
func (idx *dbIndex) dependencies(rec record) []core.Dependency {
	deps := make([]core.Dependency, 0, len(rec.depends)+len(rec.optDepends))
	seen := map[core.Dependency]bool{}
	add := func(dep string, scope core.Scope) {
		depName, requirement := splitConstraint(dep)
		if depName == "" {
			return
		}
		if _, ok := idx.packages[depName]; !ok {
			if provider, ok := idx.provider(depName, requirement); ok {
				depName, requirement = provider, ""
			}
		}
		if depName == rec.name {
			return
		}
		// "curl libcurl.so=4-64" is one dependency once the soname is resolved.
		d := core.Dependency{Name: depName, Requirements: requirement, Scope: scope, Optional: scope == core.Optional}
		if seen[d] {
			return
		}
		seen[d] = true
		deps = append(deps, d)
	}
	for _, dep := range rec.depends {
		add(dep, core.Runtime)
	}
	for _, dep := range rec.optDepends {
		// "base-devel: required to use makepkg" names base-devel.
		dep, _, _ = strings.Cut(dep, ": ")
		add(dep, core.Optional)
	}
	return deps
}

// splitConstraint splits "glibc>=2.38" into "glibc" and ">=2.38".
func splitConstraint(dep string) (name, requirement string) {
	dep = strings.TrimSpace(dep)
	if i := strings.IndexAny(dep, "<>="); i >= 0 {
		return dep[:i], dep[i:]
	}
	return dep, ""
}

func (r *Registry) FetchMaintainers(ctx context.Context, name string) ([]core.Maintainer, error) {
	_, recs, err := r.lookup(ctx, name)
	if err != nil {
		return nil, err
	}
	if recs[0].packager == "" {
		return nil, nil
	}
	return []core.Maintainer{parseMaintainer(recs[0].packager)}, nil
}

type URLs struct {
	dbURL string
}

func (u *URLs) Registry(name, version string) string {
	return ""
}

// Download returns "": a package's file name, which carries its
// architecture, is only known from the database.
func (u *URLs) Download(name, version string) string {
	return ""
}

func (u *URLs) Documentation(name, version string) string {
	return ""
}

func (u *URLs) PURL(name, version string) string {
	namespace, pkg := splitName(name)
	var qualifiers map[string]string
	if u.dbURL != "" {
		qualifiers = map[string]string{"repository_url": u.dbURL}
	}
	return purl.New(ecosystem, namespace, pkg, version, qualifiers).String()
}
