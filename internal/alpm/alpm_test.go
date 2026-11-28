package alpm

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/git-pkgs/registries/internal/core"
	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

// Trimmed from Arch Linux's core.db for x86_64, with PGPSIG values cut and
// glibc>=2.38 and libfoo.so=1-64 added to pacman's dependencies.
const pacmanDesc = `%FILENAME%
pacman-7.1.0.r9.g54d9411-4-x86_64.pkg.tar.zst

%NAME%
pacman

%BASE%
pacman

%VERSION%
7.1.0.r9.g54d9411-4

%DESC%
A library-based package manager with dependency support

%GROUPS%
base-devel

%CSIZE%
993248

%ISIZE%
5284716

%SHA256SUM%
e7b847d2989de38ec17ce9e2ca2a7b585ca61604dda0c73bc285b44005b234b1

%PGPSIG%
iHUEABYKAB0WIQQ+gMoai4n2nLpX2Yp2pe+QVESaXAUCatyuLQAKCRB2pe+QVESaXO5UAQCA0M7T5AgIjKsxQjJyAhTHt50ufVS4HDbleEWD3JZ9xgEAjISQ4lebJ1SCHTuv0iOW6mpY1ErbeqQ5YLHQECgm4wk=

%URL%
https://www.archlinux.org/pacman/

%LICENSE%
GPL-2.0-or-later

%ARCH%
x86_64

%BUILDDATE%
1792847381

%PACKAGER%
Pierre Schmitz <pierre@archlinux.org>

%PROVIDES%
libalpm.so=16-64

%DEPENDS%
bash
curl
libcurl.so=4-64
glibc>=2.38
libfoo.so=1-64

%OPTDEPENDS%
base-devel: required to use makepkg
perl-locale-gettext: translation support in makepkg-template

%MAKEDEPENDS%
asciidoc
meson

%CHECKDEPENDS%
fakechroot
python

`

const curlDesc = `%FILENAME%
curl-8.23.0-2-x86_64.pkg.tar.zst

%NAME%
curl

%BASE%
curl

%VERSION%
8.23.0-2

%DESC%
command line tool and library for transferring data with URLs

%CSIZE%
1234567

%ISIZE%
2345678

%MD5SUM%
0123456789ABCDEF0123456789ABCDEF

%SHA256SUM%
4d2bbf66b26c6dc7236e505c9d80644d3da6a28473c690365324edf276a68775

%URL%
https://curl.se

%LICENSE%
MIT
curl

%ARCH%
x86_64

%BUILDDATE%
1794000000

%PACKAGER%
Arch Build System

%PROVIDES%
libcurl.so=4-64

%DEPENDS%
zlib
libz.so=1-64

`

const zlibDesc = `%FILENAME%
zlib-1:1.4-1-x86_64.pkg.tar.zst

%NAME%
zlib

%VERSION%
1:1.4-1

%LICENSE%
Zlib

%ARCH%
x86_64

%PROVIDES%
libz.so=1-64

%CONFLICTS%
zlib-ng-compat

%REPLACES%
zlib-ng

`

const mirrorlistDesc = `%FILENAME%
pacman-mirrorlist-20260610-1-any.pkg.tar.zst

%NAME%
pacman-mirrorlist

%VERSION%
20260610-1

%ARCH%
any

`

const bashDesc = `%FILENAME%
bash-5.3.20-1-x86_64.pkg.tar.zst

%NAME%
bash

%VERSION%
5.3.20-1

%PROVIDES%
sh

`

// coreEntries is a small sync database: one directory per package.
var coreEntries = []entry{
	{name: "bash-5.3.20-1/", dir: true},
	{name: "bash-5.3.20-1/desc", body: bashDesc},
	{name: "curl-8.23.0-2/", dir: true},
	{name: "curl-8.23.0-2/desc", body: curlDesc},
	{name: "pacman-7.1.0.r9.g54d9411-4/", dir: true},
	{name: "pacman-7.1.0.r9.g54d9411-4/desc", body: pacmanDesc},
	{name: "pacman-mirrorlist-20260610-1/", dir: true},
	{name: "pacman-mirrorlist-20260610-1/desc", body: mirrorlistDesc},
	{name: "zlib-1:1.4-1/", dir: true},
	{name: "zlib-1:1.4-1/desc", body: zlibDesc},
}

type entry struct {
	name     string
	body     string
	dir      bool
	typeflag byte
}

func buildTar(t *testing.T, entries []entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		switch {
		case e.dir:
			hdr.Typeflag, hdr.Mode, hdr.Size = tar.TypeDir, 0o755, 0
		case e.typeflag != 0:
			hdr.Typeflag, hdr.Size, hdr.Linkname = e.typeflag, 0, "elsewhere"
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if hdr.Size > 0 {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func compress(t *testing.T, format string, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	switch format {
	case "gzip":
		w := gzip.NewWriter(&buf)
		_, _ = w.Write(data)
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	case "zstd":
		w, err := zstd.NewWriter(&buf)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(data)
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	case "xz":
		w, err := xz.NewWriter(&buf)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(data)
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	default:
		return data
	}
	return buf.Bytes()
}

func gzipDB(t *testing.T, entries []entry) []byte {
	t.Helper()
	return compress(t, "gzip", buildTar(t, entries))
}

// dbServer serves body at /core/os/x86_64/core.db and counts requests.
func dbServer(t *testing.T, body []byte) (string, *atomic.Int32) {
	t.Helper()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/core/os/x86_64/core.db" {
			http.NotFound(w, r)
			return
		}
		requests.Add(1)
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	return server.URL + "/core/os/x86_64/core.db", &requests
}

func newCore(t *testing.T) (*Registry, string) {
	t.Helper()
	dbURL, _ := dbServer(t, gzipDB(t, coreEntries))
	return New(dbURL, core.DefaultClient()), strings.TrimSuffix(dbURL, "/core.db")
}

func TestFetchVersions(t *testing.T) {
	reg, dir := newCore(t)
	versions, err := reg.FetchVersions(context.Background(), "arch/pacman")
	if err != nil {
		t.Fatalf("FetchVersions failed: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("got %d versions, want 1", len(versions))
	}
	v := versions[0]
	if v.Number != "7.1.0.r9.g54d9411-4" || v.Licenses != "GPL-2.0-or-later" {
		t.Errorf("version = %+v", v)
	}
	if !v.PublishedAt.Equal(time.Unix(1792847381, 0)) {
		t.Errorf("PublishedAt = %v", v.PublishedAt)
	}
	const integrity = "sha256-e7b847d2989de38ec17ce9e2ca2a7b585ca61604dda0c73bc285b44005b234b1"
	if v.Integrity != integrity {
		t.Errorf("Integrity = %q", v.Integrity)
	}
	wantMeta := map[string]any{
		"arch":           "x86_64",
		"base":           "pacman",
		"packager":       "Pierre Schmitz <pierre@archlinux.org>",
		"filename":       "pacman-7.1.0.r9.g54d9411-4-x86_64.pkg.tar.zst",
		"size":           int64(993248),
		"installed_size": int64(5284716),
		"groups":         []string{"base-devel"},
		"provides":       []string{"libalpm.so=16-64"},
		"makedepends":    []string{"asciidoc", "meson"},
		"checkdepends":   []string{"fakechroot", "python"},
		"optdepends":     []string{"base-devel: required to use makepkg", "perl-locale-gettext: translation support in makepkg-template"},
	}
	for k, want := range wantMeta {
		if !reflect.DeepEqual(v.Metadata[k], want) {
			t.Errorf("Metadata[%s] = %#v, want %#v", k, v.Metadata[k], want)
		}
	}
	if _, ok := v.Metadata["md5sum"]; ok {
		t.Errorf("md5sum = %v, want none", v.Metadata["md5sum"])
	}
	for k, val := range v.Metadata {
		if s, ok := val.(string); ok && strings.HasPrefix(s, "iHUE") {
			t.Errorf("Metadata[%s] holds the PGP signature", k)
		}
	}

	want := []core.Artifact{{
		URL:       dir + "/pacman-7.1.0.r9.g54d9411-4-x86_64.pkg.tar.zst",
		Filename:  "pacman-7.1.0.r9.g54d9411-4-x86_64.pkg.tar.zst",
		Integrity: integrity,
		Size:      993248,
		Metadata:  map[string]any{"arch": "x86_64"},
	}}
	if !reflect.DeepEqual(v.Artifacts, want) {
		t.Errorf("artifacts = %+v, want %+v", v.Artifacts, want)
	}

	curl, err := reg.FetchVersions(context.Background(), "curl")
	if err != nil {
		t.Fatal(err)
	}
	if curl[0].Licenses != "MIT AND curl" || curl[0].Metadata["md5sum"] != "0123456789abcdef0123456789abcdef" {
		t.Errorf("curl = %+v", curl[0])
	}
}

func TestCompressions(t *testing.T) {
	for _, format := range []string{"gzip", "zstd", "xz", "tar"} {
		t.Run(format, func(t *testing.T) {
			dbURL, _ := dbServer(t, compress(t, format, buildTar(t, coreEntries)))
			versions, err := New(dbURL, core.DefaultClient()).FetchVersions(context.Background(), "curl")
			if err != nil {
				t.Fatalf("FetchVersions: %v", err)
			}
			if len(versions) != 1 || versions[0].Number != "8.23.0-2" {
				t.Errorf("versions = %+v", versions)
			}
		})
	}
}

func TestEpochAndReleaseVersions(t *testing.T) {
	reg, dir := newCore(t)
	versions, err := reg.FetchVersions(context.Background(), "arch/zlib")
	if err != nil {
		t.Fatal(err)
	}
	if versions[0].Number != "1:1.4-1" || versions[0].Artifacts[0].URL != dir+"/zlib-1:1.4-1-x86_64.pkg.tar.zst" {
		t.Errorf("version = %+v", versions[0])
	}
	if !reflect.DeepEqual(versions[0].Metadata["conflicts"], []string{"zlib-ng-compat"}) ||
		!reflect.DeepEqual(versions[0].Metadata["replaces"], []string{"zlib-ng"}) {
		t.Errorf("metadata = %+v", versions[0].Metadata)
	}

	// An epoch outranks any version, and pkgrel orders builds of one version.
	dbURL, _ := dbServer(t, gzipDB(t, []entry{
		{name: "foo-2.0-1/desc", body: "%NAME%\nfoo\n\n%VERSION%\n2.0-1\n"},
		{name: "foo-1:1.0-1/desc", body: "%NAME%\nfoo\n\n%VERSION%\n1:1.0-1\n"},
		{name: "foo-2.0-10/desc", body: "%NAME%\nfoo\n\n%VERSION%\n2.0-10\n"},
		{name: "foo-2.0-2/desc", body: "%NAME%\nfoo\n\n%VERSION%\n2.0-2\n"},
	}))
	versions, err = New(dbURL, core.DefaultClient()).FetchVersions(context.Background(), "foo")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, v := range versions {
		got = append(got, v.Number)
	}
	if want := []string{"1:1.0-1", "2.0-10", "2.0-2", "2.0-1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("versions = %v, want %v", got, want)
	}
}

func TestArchitectures(t *testing.T) {
	reg, dir := newCore(t)
	versions, err := reg.FetchVersions(context.Background(), "pacman-mirrorlist")
	if err != nil {
		t.Fatal(err)
	}
	a := versions[0].Artifacts[0]
	if a.URL != dir+"/pacman-mirrorlist-20260610-1-any.pkg.tar.zst" || a.Metadata["arch"] != "any" || versions[0].Metadata["arch"] != "any" {
		t.Errorf("artifact = %+v", a)
	}
}

func TestFetchPackageKeepsNamespace(t *testing.T) {
	reg, _ := newCore(t)
	pkg, err := reg.FetchPackage(context.Background(), "arch/pacman")
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Name != "pacman" || pkg.Namespace != "arch" || pkg.LatestVersion != "7.1.0.r9.g54d9411-4" ||
		pkg.Homepage != "https://www.archlinux.org/pacman/" || pkg.Licenses != "GPL-2.0-or-later" ||
		pkg.Description != "A library-based package manager with dependency support" {
		t.Errorf("package = %+v", pkg)
	}
	if pkg.Metadata["base"] != "pacman" || pkg.Metadata["arch"] != "x86_64" || !reflect.DeepEqual(pkg.Metadata["groups"], []string{"base-devel"}) {
		t.Errorf("metadata = %+v", pkg.Metadata)
	}

	plain, err := reg.FetchPackage(context.Background(), "pacman")
	if err != nil || plain.Namespace != "" {
		t.Errorf("package without namespace = %+v, %v", plain, err)
	}
}

func TestFetchDependencies(t *testing.T) {
	reg, _ := newCore(t)
	deps, err := reg.FetchDependencies(context.Background(), "arch/pacman", "7.1.0.r9.g54d9411-4")
	if err != nil {
		t.Fatal(err)
	}
	want := []core.Dependency{
		{Name: "bash", Scope: core.Runtime},
		// libcurl.so=4-64 resolves to curl, which is already listed.
		{Name: "curl", Scope: core.Runtime},
		{Name: "glibc", Requirements: ">=2.38", Scope: core.Runtime},
		// No package here provides it, so it is kept as written.
		{Name: "libfoo.so", Requirements: "=1-64", Scope: core.Runtime},
		{Name: "base-devel", Scope: core.Optional, Optional: true},
		{Name: "perl-locale-gettext", Scope: core.Optional, Optional: true},
	}
	if !reflect.DeepEqual(deps, want) {
		t.Errorf("deps = %+v\nwant %+v", deps, want)
	}

	deps, err = reg.FetchDependencies(context.Background(), "curl", "8.23.0-2")
	if err != nil {
		t.Fatal(err)
	}
	if want := []core.Dependency{{Name: "zlib", Scope: core.Runtime}}; !reflect.DeepEqual(deps, want) {
		t.Errorf("curl deps = %+v, want %+v", deps, want)
	}

	deps, err = reg.FetchDependencies(context.Background(), "pacman-mirrorlist", "20260610-1")
	if err != nil || len(deps) != 0 {
		t.Errorf("no dependencies = %+v, %v", deps, err)
	}
}

func TestProviderChoice(t *testing.T) {
	dbURL, _ := dbServer(t, gzipDB(t, []entry{
		{name: "app-1.0-1/desc", body: "%NAME%\napp\n\n%VERSION%\n1.0-1\n\n%DEPENDS%\nsh\njava-runtime>=17\n\n%OPTDEPENDS%\nsh: for scripts\n"},
		{name: "zsh-5.9-1/desc", body: "%NAME%\nzsh\n\n%VERSION%\n5.9-1\n\n%PROVIDES%\nsh\n"},
		{name: "bash-5.3-1/desc", body: "%NAME%\nbash\n\n%VERSION%\n5.3-1\n\n%PROVIDES%\nsh\n"},
		{name: "jre-openjdk-21-1/desc", body: "%NAME%\njre-openjdk\n\n%VERSION%\n21-1\n\n%PROVIDES%\njava-runtime=21\n"},
	}))
	deps, err := New(dbURL, core.DefaultClient()).FetchDependencies(context.Background(), "app", "1.0-1")
	if err != nil {
		t.Fatal(err)
	}
	want := []core.Dependency{
		{Name: "bash", Scope: core.Runtime},
		{Name: "jre-openjdk", Scope: core.Runtime},
		{Name: "bash", Scope: core.Optional, Optional: true},
	}
	if !reflect.DeepEqual(deps, want) {
		t.Errorf("deps = %+v, want %+v", deps, want)
	}
}

func TestFetchMaintainers(t *testing.T) {
	reg, _ := newCore(t)
	maintainers, err := reg.FetchMaintainers(context.Background(), "arch/pacman")
	if err != nil {
		t.Fatal(err)
	}
	if want := []core.Maintainer{{Name: "Pierre Schmitz", Email: "pierre@archlinux.org"}}; !reflect.DeepEqual(maintainers, want) {
		t.Errorf("maintainers = %+v", maintainers)
	}

	maintainers, err = reg.FetchMaintainers(context.Background(), "curl")
	if err != nil {
		t.Fatal(err)
	}
	if want := []core.Maintainer{{Name: "Arch Build System"}}; !reflect.DeepEqual(maintainers, want) {
		t.Errorf("unstructured maintainers = %+v", maintainers)
	}

	maintainers, err = reg.FetchMaintainers(context.Background(), "bash")
	if err != nil || maintainers != nil {
		t.Errorf("no packager = %+v, %v", maintainers, err)
	}
}

func TestParseMaintainer(t *testing.T) {
	tests := map[string]core.Maintainer{
		"Pierre Schmitz <pierre@archlinux.org>": {Name: "Pierre Schmitz", Email: "pierre@archlinux.org"},
		"<pierre@archlinux.org>":                {Email: "pierre@archlinux.org"},
		"Unknown Packager":                      {Name: "Unknown Packager"},
		"Someone <not an email>":                {Name: "Someone <not an email>"},
		"Someone <>":                            {Name: "Someone <>"},
	}
	for in, want := range tests {
		if got := parseMaintainer(in); got != want {
			t.Errorf("parseMaintainer(%q) = %+v, want %+v", in, got, want)
		}
	}
}

func TestMissingOptionalFields(t *testing.T) {
	dbURL, _ := dbServer(t, gzipDB(t, []entry{{name: "bare-1.0-1/desc", body: "%NAME%\nbare\n\n%VERSION%\n1.0-1\n"}}))
	reg := New(dbURL, core.DefaultClient())
	versions, err := reg.FetchVersions(context.Background(), "bare")
	if err != nil {
		t.Fatal(err)
	}
	v := versions[0]
	if v.Number != "1.0-1" || v.Integrity != "" || v.Licenses != "" || !v.PublishedAt.IsZero() || len(v.Artifacts) != 0 {
		t.Errorf("version = %+v", v)
	}
	pkg, err := reg.FetchPackage(context.Background(), "bare")
	if err != nil || pkg.Description != "" || pkg.Homepage != "" {
		t.Errorf("package = %+v, %v", pkg, err)
	}
}

func TestOlderDatabaseLayout(t *testing.T) {
	// Older databases keep dependencies in a depends file, and .files
	// databases add a files list; top-level files are not packages.
	dbURL, _ := dbServer(t, gzipDB(t, []entry{
		{name: "./", dir: true},
		{name: "./old-1.0-1/", dir: true},
		{name: "./old-1.0-1/desc", body: "%FILENAME%\nold-1.0-1-x86_64.pkg.tar.xz\n\n%NAME%\nold\n\n%VERSION%\n1.0-1\n\n%SHA256SUM%\nnot hex\n"},
		{name: "./old-1.0-1/depends", body: "%DEPENDS%\nglibc\n"},
		{name: "./old-1.0-1/files", body: "%FILES%\nusr/\nusr/bin/old\n"},
		{name: "./old-1.0-1/link", typeflag: tar.TypeSymlink},
		{name: "README", body: "not a package"},
	}))
	reg := New(dbURL, core.DefaultClient())
	deps, err := reg.FetchDependencies(context.Background(), "old", "1.0-1")
	if err != nil {
		t.Fatal(err)
	}
	if want := []core.Dependency{{Name: "glibc", Scope: core.Runtime}}; !reflect.DeepEqual(deps, want) {
		t.Errorf("deps = %+v", deps)
	}
	versions, err := reg.FetchVersions(context.Background(), "old")
	if err != nil {
		t.Fatal(err)
	}
	if versions[0].Integrity != "" || versions[0].Artifacts[0].Integrity != "" {
		t.Errorf("a SHA256SUM that is not one gives no integrity: %+v", versions[0])
	}
}

func TestMalformedDatabases(t *testing.T) {
	desc := func(name, version string) string {
		return "%NAME%\n" + name + "\n\n%VERSION%\n" + version + "\n"
	}
	tarOf := func(entries ...entry) func(t *testing.T) []byte {
		return func(t *testing.T) []byte { return gzipDB(t, entries) }
	}
	tests := []struct {
		name string
		body func(t *testing.T) []byte
	}{
		{"empty body", func(t *testing.T) []byte { return nil }},
		{"not an archive", func(t *testing.T) []byte { return []byte("<html>not found</html>") }},
		{"truncated gzip", func(t *testing.T) []byte {
			db := gzipDB(t, coreEntries)
			return db[:len(db)/2]
		}},
		{"corrupt gzip trailer", func(t *testing.T) []byte {
			db := gzipDB(t, coreEntries)
			db[len(db)-5] ^= 0xff
			return db
		}},
		{"truncated zstd", func(t *testing.T) []byte {
			db := compress(t, "zstd", buildTar(t, coreEntries))
			return db[:len(db)/2]
		}},
		{"truncated xz", func(t *testing.T) []byte {
			db := compress(t, "xz", buildTar(t, coreEntries))
			return db[:len(db)/2]
		}},
		{"parent path", tarOf(entry{name: "../foo-1.0-1/desc", body: desc("foo", "1.0-1")})},
		{"inner parent path", tarOf(entry{name: "foo-1.0-1/../../desc", body: desc("foo", "1.0-1")})},
		{"absolute path", tarOf(entry{name: "/foo-1.0-1/desc", body: desc("foo", "1.0-1")})},
		{"backslash path", tarOf(entry{name: `foo-1.0-1\..\desc`, body: desc("foo", "1.0-1")})},
		{"nested path", tarOf(entry{name: "foo-1.0-1/sub/desc", body: desc("foo", "1.0-1")})},
		{"directory without version", tarOf(entry{name: "foo/desc", body: desc("foo", "1.0-1")})},
		{"directory without pkgrel", tarOf(entry{name: "foo-1.0/desc", body: desc("foo", "1.0")})},
		{"name mismatch", tarOf(entry{name: "foo-1.0-1/desc", body: desc("bar", "1.0-1")})},
		{"version mismatch", tarOf(entry{name: "foo-1.0-1/desc", body: desc("foo", "1.0-2")})},
		{"filename with a path", tarOf(entry{name: "foo-1.0-1/desc", body: desc("foo", "1.0-1") + "\n%FILENAME%\n../foo-1.0-1-any.pkg.tar.zst\n"})},
		{"hidden filename", tarOf(entry{name: "foo-1.0-1/desc", body: desc("foo", "1.0-1") + "\n%FILENAME%\n.foo-1.0-1-any.pkg.tar.zst\n"})},
		{"filename with a query", tarOf(entry{name: "foo-1.0-1/desc", body: desc("foo", "1.0-1") + "\n%FILENAME%\nfoo.pkg.tar.zst?x=1\n"})},
		{"line outside a key", tarOf(entry{name: "foo-1.0-1/desc", body: "foo\n\n" + desc("foo", "1.0-1")})},
		{"bad size", tarOf(entry{name: "foo-1.0-1/desc", body: desc("foo", "1.0-1") + "\n%CSIZE%\nlarge\n"})},
		{"bad build date", tarOf(entry{name: "foo-1.0-1/desc", body: desc("foo", "1.0-1") + "\n%BUILDDATE%\nyesterday\n"})},
		{"oversized desc", tarOf(entry{name: "foo-1.0-1/desc", body: desc("foo", "1.0-1") + "\n%DESC%\n" + strings.Repeat("x", maxEntrySize) + "\n"})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbURL, _ := dbServer(t, tt.body(t))
			_, err := New(dbURL, core.DefaultClient()).FetchVersions(context.Background(), "foo")
			if !errors.Is(err, ErrMalformedDatabase) {
				t.Errorf("err = %v, want ErrMalformedDatabase", err)
			}
		})
	}
}

func TestDecompressedLimit(t *testing.T) {
	plain := buildTar(t, coreEntries)
	for _, format := range []string{"gzip", "zstd", "xz", "tar"} {
		t.Run(format, func(t *testing.T) {
			dbURL, _ := dbServer(t, compress(t, format, plain))
			for _, limit := range []int64{int64(len(plain)) - 1, int64(len(plain)) / 2} {
				r := New(dbURL, core.DefaultClient())
				r.maxDatabaseSize = limit
				if _, err := r.FetchVersions(context.Background(), "curl"); !errors.Is(err, ErrMalformedDatabase) || !strings.Contains(err.Error(), "decompresses to more than") {
					t.Errorf("limit %d: err = %v, want ErrMalformedDatabase naming the limit", limit, err)
				}
			}
			r := New(dbURL, core.DefaultClient())
			r.maxDatabaseSize = int64(len(plain))
			if _, err := r.FetchVersions(context.Background(), "curl"); err != nil {
				t.Errorf("at the limit: %v", err)
			}
		})
	}
}

func TestInvalidConfiguration(t *testing.T) {
	for _, u := range []string{"", "core.db", "https://geo.mirror.pkgbuild.com", "https://geo.mirror.pkgbuild.com/core/os/x86_64/"} {
		_, err := New(u, nil).FetchPackage(context.Background(), "pacman")
		if !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("New(%q): err = %v, want ErrInvalidConfig", u, err)
		}
	}
}

func TestDatabaseURLWithQuery(t *testing.T) {
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = w.Write(gzipDB(t, coreEntries))
	}))
	defer server.Close()

	versions, err := New(server.URL+"/core/os/x86_64/core.db?v=2", core.DefaultClient()).FetchVersions(context.Background(), "curl")
	if err != nil {
		t.Fatal(err)
	}
	if gotQuery != "v=2" {
		t.Errorf("query = %q, want the database URL's", gotQuery)
	}
	if want := server.URL + "/core/os/x86_64/curl-8.23.0-2-x86_64.pkg.tar.zst"; versions[0].Artifacts[0].URL != want {
		t.Errorf("artifact URL = %q, want %q", versions[0].Artifacts[0].URL, want)
	}
}

func TestNotFoundAndCaching(t *testing.T) {
	dbURL, requests := dbServer(t, gzipDB(t, coreEntries))
	reg := New(dbURL, core.DefaultClient())
	ctx := context.Background()

	var nf *core.NotFoundError
	if _, err := reg.FetchPackage(ctx, "arch/missing"); !errors.As(err, &nf) || nf.Name != "arch/missing" {
		t.Errorf("missing package: err = %v", err)
	}
	if _, err := reg.FetchVersions(ctx, "missing"); !errors.As(err, &nf) {
		t.Errorf("missing versions: err = %v", err)
	}
	if _, err := reg.FetchDependencies(ctx, "curl", "1.0-1"); !errors.As(err, &nf) || nf.Version != "1.0-1" {
		t.Errorf("missing version: err = %v", err)
	}
	if _, err := reg.FetchMaintainers(ctx, "missing"); !errors.As(err, &nf) {
		t.Errorf("missing maintainers: err = %v", err)
	}
	if n := requests.Load(); n != 1 {
		t.Errorf("database fetched %d times, want 1", n)
	}

	_, err := New(strings.Replace(dbURL, "core.db", "extra.db", 1), core.DefaultClient()).FetchPackage(ctx, "curl")
	var httpErr *core.HTTPError
	if !errors.As(err, &httpErr) || !httpErr.IsNotFound() {
		t.Errorf("missing database: err = %v", err)
	}
}

func TestFailedReadIsRetried(t *testing.T) {
	var requests atomic.Int32
	good := gzipDB(t, coreEntries)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			_, _ = w.Write(good[:len(good)/2])
			return
		}
		_, _ = w.Write(good)
	}))
	defer server.Close()

	reg := New(server.URL+"/core.db", core.DefaultClient())
	if _, err := reg.FetchPackage(context.Background(), "curl"); !errors.Is(err, ErrMalformedDatabase) {
		t.Fatalf("first read: err = %v, want ErrMalformedDatabase", err)
	}
	if _, err := reg.FetchPackage(context.Background(), "curl"); err != nil {
		t.Errorf("second read: %v", err)
	}
}

type tokenTransport struct{ base http.RoundTripper }

func (t tokenTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer secret")
	return t.base.RoundTrip(req)
}

func TestUsesSuppliedHTTPClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write(gzipDB(t, coreEntries))
	}))
	defer server.Close()

	if _, err := New(server.URL+"/core.db", core.DefaultClient()).FetchVersions(context.Background(), "curl"); err == nil {
		t.Error("FetchVersions without credentials succeeded")
	}
	client := core.DefaultClient()
	client.HTTPClient = &http.Client{Transport: tokenTransport{base: http.DefaultTransport}}
	if _, err := New(server.URL+"/core.db", client).FetchVersions(context.Background(), "curl"); err != nil {
		t.Errorf("FetchVersions through authenticated client: %v", err)
	}
}

func TestSplitEntryName(t *testing.T) {
	tests := []struct {
		dir, name, version string
		ok                 bool
	}{
		{"pacman-7.1.0.r9.g54d9411-4", "pacman", "7.1.0.r9.g54d9411-4", true},
		{"pacman-mirrorlist-20260610-1", "pacman-mirrorlist", "20260610-1", true},
		{"zlib-1:1.4-1", "zlib", "1:1.4-1", true},
		{"foo-1.0", "", "", false},
		{"foo", "", "", false},
		{"-1.0-1", "", "", false},
		{"foo--1", "", "", false},
		{"foo-1.0-", "", "", false},
	}
	for _, tt := range tests {
		name, version, ok := splitEntryName(tt.dir)
		if name != tt.name || version != tt.version || ok != tt.ok {
			t.Errorf("splitEntryName(%q) = %q, %q, %v", tt.dir, name, version, ok)
		}
	}
}

func TestURLs(t *testing.T) {
	u := New("https://geo.mirror.pkgbuild.com/core/os/x86_64/core.db", nil).URLs()
	tests := [][2]string{
		{u.PURL("arch/pacman", "7.1.0.r9.g54d9411-4"), "pkg:alpm/arch/pacman@7.1.0.r9.g54d9411-4?repository_url=https:%2F%2Fgeo.mirror.pkgbuild.com%2Fcore%2Fos%2Fx86_64%2Fcore.db"},
		{u.PURL("arch/zlib", "1:1.4-1"), "pkg:alpm/arch/zlib@1:1.4-1?repository_url=https:%2F%2Fgeo.mirror.pkgbuild.com%2Fcore%2Fos%2Fx86_64%2Fcore.db"},
		{u.Download("arch/pacman", "7.1.0.r9.g54d9411-4"), ""},
		{u.Registry("arch/pacman", ""), ""},
		{u.Documentation("arch/pacman", ""), ""},
	}
	for _, tt := range tests {
		if tt[0] != tt[1] {
			t.Errorf("got %q, want %q", tt[0], tt[1])
		}
	}
}
