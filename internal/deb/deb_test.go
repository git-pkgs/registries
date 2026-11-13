package deb

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/git-pkgs/registries/internal/core"
	"github.com/ulikunitz/xz"
)

// Trimmed from deb.debian.org's bookworm main/binary-amd64 and
// bookworm-updates indexes.
const bookwormAMD64 = `Package: curl
Version: 7.88.1-10+deb12u5
Installed-Size: 500
Maintainer: Alessandro Ghedini <ghedo@debian.org>
Architecture: amd64
Depends: libc6 (>= 2.34), libcurl4 (= 7.88.1-10+deb12u5), zlib1g (>= 1:1.1.4)
Description: command line tool for transferring data with URL syntax
 curl is a command line tool for transferring data with URL syntax.
Homepage: https://curl.se/
Section: web
Filename: pool/main/c/curl/curl_7.88.1-10+deb12u5_amd64.deb
Size: 315860
SHA256: 1b2f0f3c2a4bdbe8b1e3f9a1b6d7c9e0f1a2b3c4d5e6f708192a3b4c5d6e7f80

Package: openssh-client
Source: openssh
Version: 1:9.2p1-2+deb12u3
Maintainer: Debian OpenSSH Maintainers <debian-ssh@lists.debian.org>
Architecture: amd64
Pre-Depends: libc6 (>= 2.34)
Depends: adduser, passwd, libssl3 (>= 3.0.13) | libssl3t64, zlib1g:any (>= 1:1.1.4), libselinux1 [linux-any]
Tag: implemented-in::c, interface::commandline, interface::shell,
 network::client, protocol::sftp
Description: secure shell (SSH) client, for secure access to remote machines
Section: net
Filename: pool/main/o/openssh/openssh-client_9.2p1-2+deb12u3_amd64.deb
Size: 991056
SHA256: 2c3f0f3c2a4bdbe8b1e3f9a1b6d7c9e0f1a2b3c4d5e6f708192a3b4c5d6e7f81

Package: ca-certificates
Version: 20230311
Maintainer: Julien Cristau <jcristau@debian.org>
Architecture: all
Depends: openssl (>= 1.1.1), debconf (>= 0.5) | debconf-2.0
Description: Common CA certificates
Filename: pool/main/c/ca-certificates/ca-certificates_20230311_all.deb
Size: 153656
SHA256: 3d4f0f3c2a4bdbe8b1e3f9a1b6d7c9e0f1a2b3c4d5e6f708192a3b4c5d6e7f82
`

const bookwormARM64 = `Package: curl
Version: 7.88.1-10+deb12u5
Maintainer: Alessandro Ghedini <ghedo@debian.org>
Architecture: arm64
Filename: pool/main/c/curl/curl_7.88.1-10+deb12u5_arm64.deb
Size: 301024
SHA256: 4e5f0f3c2a4bdbe8b1e3f9a1b6d7c9e0f1a2b3c4d5e6f708192a3b4c5d6e7f83
`

const updatesAMD64 = `Package: curl
Version: 7.88.1-10+deb12u14
Maintainer: Alessandro Ghedini <ghedo@debian.org>
Architecture: amd64
Filename: pool/main/c/curl/curl_7.88.1-10+deb12u14_amd64.deb
Size: 316112
SHA256: 5f6f0f3c2a4bdbe8b1e3f9a1b6d7c9e0f1a2b3c4d5e6f708192a3b4c5d6e7f84
`

func xzData(t *testing.T, s string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := xz.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func gzData(t *testing.T, s string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// archive is a fake Debian archive. served are the files it answers with;
// listed are entries its Release files name, served or not.
type archive struct {
	t        *testing.T
	mu       sync.Mutex
	served   map[string][]byte
	listed   map[string][]byte
	requests []string
}

func newArchive(t *testing.T) *archive {
	return &archive{t: t, served: map[string][]byte{}, listed: map[string][]byte{}}
}

// index adds one Packages index of suite, as variant ("Packages.xz",
// "Packages.gz" or "Packages"), served and listed.
func (a *archive) index(suite, dir, variant, stanzas string) {
	data := []byte(stanzas)
	switch variant {
	case "Packages.xz":
		data = xzData(a.t, stanzas)
	case "Packages.gz":
		data = gzData(a.t, stanzas)
	}
	path := "dists/" + suite + "/" + dir + "/" + variant
	a.served[path] = data
	a.listed[path] = data
}

// release writes the suite's Release (or InRelease, clearsigned) over the
// files listed under it.
func (a *archive) release(suite, codename string, signed bool) {
	prefix := "dists/" + suite + "/"
	var paths []string
	for p := range a.listed {
		if strings.HasPrefix(p, prefix) {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	var b strings.Builder
	fmt.Fprintf(&b, "Origin: Debian\nSuite: %s\nCodename: %s\nArchitectures: all amd64 arm64\nComponents: main\nMD5Sum:\n", suite, codename)
	for _, p := range paths {
		fmt.Fprintf(&b, " 00000000000000000000000000000000 %8d %s\n", len(a.listed[p]), strings.TrimPrefix(p, prefix))
	}
	b.WriteString("SHA256:\n")
	for _, p := range paths {
		sum := sha256.Sum256(a.listed[p])
		fmt.Fprintf(&b, " %s %8d %s\n", hex.EncodeToString(sum[:]), len(a.listed[p]), strings.TrimPrefix(p, prefix))
	}
	if !signed {
		a.served[prefix+"Release"] = []byte(b.String())
		return
	}
	a.served[prefix+"InRelease"] = []byte("-----BEGIN PGP SIGNED MESSAGE-----\nHash: SHA256\n\n" + b.String() +
		"-----BEGIN PGP SIGNATURE-----\n\niQIzBAEBCAAdFiEE\n-----END PGP SIGNATURE-----\n")
}

func (a *archive) server() *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/debian/")
		a.mu.Lock()
		a.requests = append(a.requests, path)
		data, ok := a.served[path]
		a.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	a.t.Cleanup(srv.Close)
	return srv
}

// standard is bookworm (InRelease, xz, amd64 and arm64) plus
// bookworm-updates (plain Release, gz only).
func standard(t *testing.T) (*archive, string) {
	a := newArchive(t)
	a.index("bookworm", "main/binary-amd64", "Packages.xz", bookwormAMD64)
	a.index("bookworm", "main/binary-arm64", "Packages.xz", bookwormARM64)
	a.release("bookworm", "bookworm", true)
	a.index("bookworm-updates", "main/binary-amd64", "Packages.gz", updatesAMD64)
	a.release("bookworm-updates", "bookworm-updates", false)
	srv := a.server()
	return a, srv.URL + "/debian"
}

func newRegistry(base, query string) *Registry {
	return New(base+"?"+query, core.DefaultClient())
}

func numbers(versions []core.Version) []string {
	var out []string
	for _, v := range versions {
		out = append(out, v.Number)
	}
	return out
}

func TestFetchVersions(t *testing.T) {
	_, base := standard(t)
	r := newRegistry(base, "suites=bookworm,bookworm-updates&components=main&architectures=amd64")
	versions, err := r.FetchVersions(context.Background(), "curl")
	if err != nil {
		t.Fatalf("FetchVersions: %v", err)
	}
	if want := []string{"7.88.1-10+deb12u14", "7.88.1-10+deb12u5"}; !reflect.DeepEqual(numbers(versions), want) {
		t.Fatalf("versions = %v, want %v (newest first, by deb version)", numbers(versions), want)
	}

	newest := versions[0].Artifacts
	if len(newest) != 1 {
		t.Fatalf("artifacts = %+v, want one", newest)
	}
	got := newest[0]
	want := core.Artifact{
		URL:       base + "/pool/main/c/curl/curl_7.88.1-10+deb12u14_amd64.deb",
		Filename:  "curl_7.88.1-10+deb12u14_amd64.deb",
		Integrity: "sha256-5f6f0f3c2a4bdbe8b1e3f9a1b6d7c9e0f1a2b3c4d5e6f708192a3b4c5d6e7f84",
		Size:      316112,
		Metadata:  map[string]any{"arch": "amd64", "distro": "bookworm", "suite": "bookworm-updates", "component": "main"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("artifact =\n %+v\nwant\n %+v", got, want)
	}
	if d := versions[1].Artifacts[0].Metadata["distro"]; d != "bookworm" {
		t.Errorf("bookworm artifact distro = %v", d)
	}
}

func TestFetchVersionsAcrossArchitectures(t *testing.T) {
	_, base := standard(t)
	r := newRegistry(base, "suites=bookworm&architectures=amd64,arm64")
	versions, err := r.FetchVersions(context.Background(), "curl")
	if err != nil || len(versions) != 1 {
		t.Fatalf("FetchVersions = %v, %v", numbers(versions), err)
	}
	var archs []any
	for _, a := range versions[0].Artifacts {
		archs = append(archs, a.Metadata["arch"])
	}
	if want := []any{"amd64", "arm64"}; !reflect.DeepEqual(archs, want) {
		t.Errorf("artifact archs = %v, want %v", archs, want)
	}
}

// Architecture: all packages appear in every binary-<arch> index and in
// binary-all where the Release lists one; they are recorded once, with
// their own architecture.
func TestArchitectureAll(t *testing.T) {
	a := newArchive(t)
	a.index("bookworm", "main/binary-amd64", "Packages.xz", bookwormAMD64)
	a.index("bookworm", "main/binary-arm64", "Packages.xz", bookwormARM64+"\n"+caCertificatesOnly)
	a.index("bookworm", "main/binary-all", "Packages.xz", caCertificatesOnly)
	a.release("bookworm", "bookworm", true)
	base := a.server().URL + "/debian"

	r := newRegistry(base, "suites=bookworm&architectures=amd64,arm64")
	versions, err := r.FetchVersions(context.Background(), "ca-certificates")
	if err != nil || len(versions) != 1 {
		t.Fatalf("FetchVersions = %v, %v", numbers(versions), err)
	}
	if arts := versions[0].Artifacts; len(arts) != 1 || arts[0].Metadata["arch"] != "all" {
		t.Errorf("artifacts = %+v, want one, arch all", arts)
	}
	if !a.requested("dists/bookworm/main/binary-all/Packages.xz") {
		t.Errorf("binary-all was not read: %v", a.requests)
	}
}

const caCertificatesOnly = `Package: ca-certificates
Version: 20230311
Architecture: all
Filename: pool/main/c/ca-certificates/ca-certificates_20230311_all.deb
Size: 153656
SHA256: 3d4f0f3c2a4bdbe8b1e3f9a1b6d7c9e0f1a2b3c4d5e6f708192a3b4c5d6e7f82
`

func (a *archive) requested(path string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, p := range a.requests {
		if p == path {
			return true
		}
	}
	return false
}

// deb.debian.org lists a plain Packages it answers 404 for, and other
// archives list no .xz: the variants are tried in order past a 404.
func TestIndexVariantFallback(t *testing.T) {
	a := newArchive(t)
	a.index("bookworm", "main/binary-amd64", "Packages.xz", bookwormAMD64)
	a.listed["dists/bookworm/main/binary-amd64/Packages"] = []byte(bookwormAMD64)
	delete(a.served, "dists/bookworm/main/binary-amd64/Packages.xz")
	a.index("bookworm", "main/binary-amd64", "Packages.gz", bookwormAMD64)
	delete(a.served, "dists/bookworm/main/binary-amd64/Packages.gz")
	a.served["dists/bookworm/main/binary-amd64/Packages"] = []byte(bookwormAMD64)
	a.release("bookworm", "bookworm", true)
	base := a.server().URL + "/debian"

	versions, err := newRegistry(base, "suites=bookworm&architectures=amd64").FetchVersions(context.Background(), "curl")
	if err != nil || len(versions) != 1 {
		t.Fatalf("FetchVersions = %v, %v", numbers(versions), err)
	}
}

// Variants a Release doesn't list are not requested.
func TestIndexVariantsNotListedAreSkipped(t *testing.T) {
	a := newArchive(t)
	a.index("bookworm", "main/binary-amd64", "Packages.gz", bookwormAMD64)
	a.release("bookworm", "bookworm", true)
	a.served["dists/bookworm/main/binary-amd64/Packages.xz"] = []byte("not an index")
	base := a.server().URL + "/debian"

	if _, err := newRegistry(base, "suites=bookworm&architectures=amd64").FetchVersions(context.Background(), "curl"); err != nil {
		t.Fatalf("FetchVersions: %v", err)
	}
	if a.requested("dists/bookworm/main/binary-amd64/Packages.xz") {
		t.Error("requested a variant the Release doesn't list")
	}
}

// Without a Release, as some Artifactory setups serve, the index is read
// without a checksum to check against.
func TestNoRelease(t *testing.T) {
	a := newArchive(t)
	a.index("stable", "main/binary-amd64", "Packages.gz", bookwormAMD64)
	base := a.server().URL + "/debian"

	versions, err := newRegistry(base, "suites=stable&architectures=amd64").FetchVersions(context.Background(), "curl")
	if err != nil || len(versions) != 1 {
		t.Fatalf("FetchVersions = %v, %v", numbers(versions), err)
	}
	if d := versions[0].Artifacts[0].Metadata["distro"]; d != "stable" {
		t.Errorf("distro = %v, want the suite name without a Release", d)
	}
}

func TestChecksumMismatch(t *testing.T) {
	for _, variant := range []string{"Packages.xz", "Packages.gz", "Packages"} {
		t.Run(variant, func(t *testing.T) {
			a := newArchive(t)
			a.index("bookworm", "main/binary-amd64", variant, bookwormAMD64)
			a.release("bookworm", "bookworm", true)
			path := "dists/bookworm/main/binary-amd64/" + variant
			switch variant {
			case "Packages.xz":
				a.served[path] = xzData(t, updatesAMD64)
			case "Packages.gz":
				a.served[path] = gzData(t, updatesAMD64)
			default:
				a.served[path] = []byte(updatesAMD64)
			}
			base := a.server().URL + "/debian"

			r := newRegistry(base, "suites=bookworm&architectures=amd64")
			if _, err := r.FetchVersions(context.Background(), "curl"); !errors.Is(err, ErrChecksumMismatch) {
				t.Fatalf("err = %v, want ErrChecksumMismatch", err)
			}
			// Nothing was kept: the next lookup reads the archive again.
			a.served[path] = a.listed[path]
			if _, err := r.FetchVersions(context.Background(), "curl"); err != nil {
				t.Errorf("after the archive was fixed: %v", err)
			}
		})
	}
}

func TestInvalidConfiguration(t *testing.T) {
	_, base := standard(t)
	for name, url := range map[string]string{
		"no url":            "",
		"no host":           "/debian?suites=bookworm&architectures=amd64",
		"suite path":        base + "?suites=../../etc&architectures=amd64",
		"component path":    base + "?suites=bookworm&components=a/b&architectures=amd64",
		"architecture path": base + "?suites=bookworm&architectures=..",
		"no suites":         base + "?architectures=amd64",
		"no architectures":  base + "?suites=bookworm",
		"source":            base + "?suites=bookworm&architectures=source",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := New(url, core.DefaultClient()).FetchVersions(context.Background(), "curl")
			if !errors.Is(err, ErrInvalidConfig) {
				t.Errorf("err = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestNoIndexFound(t *testing.T) {
	a := newArchive(t)
	base := a.server().URL + "/debian"
	_, err := newRegistry(base, "suites=bookworm&architectures=amd64").FetchVersions(context.Background(), "curl")
	if !errors.Is(err, ErrNoIndex) {
		t.Errorf("err = %v, want ErrNoIndex", err)
	}
}

func TestNotFound(t *testing.T) {
	_, base := standard(t)
	r := newRegistry(base, "suites=bookworm&architectures=amd64")
	var notFound *core.NotFoundError
	if _, err := r.FetchVersions(context.Background(), "missing"); !errors.As(err, &notFound) {
		t.Errorf("missing package: err = %v, want NotFoundError", err)
	}
	if _, err := r.FetchDependencies(context.Background(), "curl", "1.0-1"); !errors.As(err, &notFound) || notFound.Version != "1.0-1" {
		t.Errorf("missing version: err = %v, want NotFoundError for 1.0-1", err)
	}
}

// The epoch is part of Version but never of Filename.
func TestEpochVersion(t *testing.T) {
	_, base := standard(t)
	versions, err := newRegistry(base, "suites=bookworm&architectures=amd64").FetchVersions(context.Background(), "debian/openssh-client")
	if err != nil || len(versions) != 1 {
		t.Fatalf("FetchVersions = %v, %v", numbers(versions), err)
	}
	if v := versions[0]; v.Number != "1:9.2p1-2+deb12u3" || v.Artifacts[0].Filename != "openssh-client_9.2p1-2+deb12u3_amd64.deb" {
		t.Errorf("version = %q, filename = %q", v.Number, v.Artifacts[0].Filename)
	}
}

func TestFetchPackage(t *testing.T) {
	_, base := standard(t)
	r := newRegistry(base, "suites=bookworm,bookworm-updates&architectures=amd64")
	pkg, err := r.FetchPackage(context.Background(), "debian/curl")
	if err != nil {
		t.Fatalf("FetchPackage: %v", err)
	}
	if pkg.Name != "curl" || pkg.Namespace != "debian" || pkg.LatestVersion != "7.88.1-10+deb12u14" {
		t.Errorf("name = %q, namespace = %q, latest = %q", pkg.Name, pkg.Namespace, pkg.LatestVersion)
	}
	pkg, err = r.FetchPackage(context.Background(), "openssh-client")
	if err != nil {
		t.Fatalf("FetchPackage: %v", err)
	}
	if pkg.Description != "secure shell (SSH) client, for secure access to remote machines" || pkg.Metadata["source"] != "openssh" || pkg.Metadata["section"] != "net" {
		t.Errorf("package = %+v", pkg)
	}
}

func TestFetchDependencies(t *testing.T) {
	_, base := standard(t)
	r := newRegistry(base, "suites=bookworm&architectures=amd64")
	deps, err := r.FetchDependencies(context.Background(), "openssh-client", "1:9.2p1-2+deb12u3")
	if err != nil {
		t.Fatalf("FetchDependencies: %v", err)
	}
	want := []core.Dependency{
		{Name: "libc6", Requirements: ">= 2.34", Scope: core.Runtime},
		{Name: "adduser", Scope: core.Runtime},
		{Name: "passwd", Scope: core.Runtime},
		{Name: "libssl3", Requirements: ">= 3.0.13", Scope: core.Runtime},
		{Name: "zlib1g", Requirements: ">= 1:1.1.4", Scope: core.Runtime},
		{Name: "libselinux1", Scope: core.Runtime},
	}
	if !reflect.DeepEqual(deps, want) {
		t.Errorf("deps =\n %+v\nwant\n %+v", deps, want)
	}
}

func TestFetchMaintainers(t *testing.T) {
	_, base := standard(t)
	r := newRegistry(base, "suites=bookworm&architectures=amd64")
	got, err := r.FetchMaintainers(context.Background(), "curl")
	if err != nil || !reflect.DeepEqual(got, []core.Maintainer{{Name: "Alessandro Ghedini", Email: "ghedo@debian.org"}}) {
		t.Errorf("maintainers = %+v, %v", got, err)
	}
}

func TestURLs(t *testing.T) {
	u := New("https://deb.debian.org/debian/?suites=bookworm&architectures=amd64", nil).URLs()
	if got := u.Download("curl", "7.88.1-10"); got != "" {
		t.Errorf("Download = %q, want none without the index", got)
	}
	if got, want := u.PURL("debian/curl", "7.88.1-10"), "pkg:deb/debian/curl@7.88.1-10?repository_url=https:%2F%2Fdeb.debian.org%2Fdebian"; got != want {
		t.Errorf("PURL = %q, want %q", got, want)
	}
	if got, want := u.PURL("debian/curl", "1:9.2p1-2"), "pkg:deb/debian/curl@1:9.2p1-2?repository_url=https:%2F%2Fdeb.debian.org%2Fdebian"; got != want {
		t.Errorf("PURL with epoch = %q, want %q", got, want)
	}
}

// An index is read once per Registry.
func TestIndexReadOnce(t *testing.T) {
	a, base := standard(t)
	r := newRegistry(base, "suites=bookworm&architectures=amd64")
	for range 3 {
		if _, err := r.FetchVersions(context.Background(), "curl"); err != nil {
			t.Fatal(err)
		}
	}
	n := 0
	for _, p := range a.requests {
		if strings.HasSuffix(p, "/Packages.xz") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("index requested %d times, want once", n)
	}
}

func TestDecompressedIndexLimit(t *testing.T) {
	for _, variant := range []string{"Packages.xz", "Packages.gz", "Packages"} {
		t.Run(variant, func(t *testing.T) {
			a := newArchive(t)
			a.index("bookworm", "main/binary-amd64", variant, bookwormAMD64)
			a.release("bookworm", "bookworm", true)
			base := a.server().URL + "/debian"

			r := newRegistry(base, "suites=bookworm&architectures=amd64")
			r.maxIndexSize = int64(len(bookwormAMD64)) - 1
			if _, err := r.FetchVersions(context.Background(), "curl"); !errors.Is(err, ErrMalformedIndex) {
				t.Errorf("over the limit: err = %v, want ErrMalformedIndex", err)
			}
			r = newRegistry(base, "suites=bookworm&architectures=amd64")
			r.maxIndexSize = int64(len(bookwormAMD64))
			if _, err := r.FetchVersions(context.Background(), "curl"); err != nil {
				t.Errorf("at the limit: %v", err)
			}
		})
	}
}

func TestXZSizeLimit(t *testing.T) {
	a := newArchive(t)
	a.index("bookworm", "main/binary-amd64", "Packages.xz", bookwormAMD64)
	a.release("bookworm", "bookworm", true)
	base := a.server().URL + "/debian"
	size := int64(len(a.served["dists/bookworm/main/binary-amd64/Packages.xz"]))

	r := newRegistry(base, "suites=bookworm&architectures=amd64")
	r.maxXZSize = size - 1
	if _, err := r.FetchVersions(context.Background(), "curl"); !errors.Is(err, ErrMalformedIndex) {
		t.Errorf("over the limit: err = %v, want ErrMalformedIndex", err)
	}
	r = newRegistry(base, "suites=bookworm&architectures=amd64")
	r.maxXZSize = size
	if _, err := r.FetchVersions(context.Background(), "curl"); err != nil {
		t.Errorf("at the limit: %v", err)
	}
}

// An .xz whose blocks ask for too large a dictionary is refused before it
// is decoded, even when the Release lists it.
func TestXZDictionaryRefused(t *testing.T) {
	a := newArchive(t)
	path := "dists/bookworm/main/binary-amd64/Packages.xz"
	a.index("bookworm", "main/binary-amd64", "Packages.xz", bookwormAMD64)
	a.served[path] = setXZDict(t, a.served[path], 30) // 128 MiB
	a.listed[path] = a.served[path]
	a.release("bookworm", "bookworm", true)
	base := a.server().URL + "/debian"
	if _, err := newRegistry(base, "suites=bookworm&architectures=amd64").FetchVersions(context.Background(), "curl"); !errors.Is(err, ErrMalformedIndex) {
		t.Errorf("err = %v, want ErrMalformedIndex", err)
	}
}

// A suite named by its alias, such as stable, records the Release's
// codename.
func TestDistroFromRelease(t *testing.T) {
	a := newArchive(t)
	a.index("stable", "main/binary-amd64", "Packages.xz", bookwormAMD64)
	a.release("stable", "bookworm", true)
	base := a.server().URL + "/debian"
	versions, err := newRegistry(base, "suites=stable&architectures=amd64").FetchVersions(context.Background(), "curl")
	if err != nil {
		t.Fatal(err)
	}
	if d := versions[0].Artifacts[0].Metadata["distro"]; d != "bookworm" {
		t.Errorf("distro = %v, want the Release's codename", d)
	}
}

// A Release with no SHA256 entries, only MD5Sum, checks nothing and lists
// nothing, so every variant is tried.
func TestReleaseWithoutSHA256(t *testing.T) {
	a := newArchive(t)
	a.index("bookworm", "main/binary-amd64", "Packages.gz", bookwormAMD64)
	a.served["dists/bookworm/Release"] = []byte("Codename: bookworm\nMD5Sum:\n 0123 12 main/binary-amd64/Packages.gz\n")
	base := a.server().URL + "/debian"
	if _, err := newRegistry(base, "suites=bookworm&architectures=amd64").FetchVersions(context.Background(), "curl"); err != nil {
		t.Errorf("FetchVersions: %v", err)
	}
}

// Same size, other content: only the hash tells them apart.
func TestChecksumHashMismatch(t *testing.T) {
	a := newArchive(t)
	a.index("bookworm", "main/binary-amd64", "Packages", bookwormAMD64)
	a.release("bookworm", "bookworm", true)
	a.served["dists/bookworm/main/binary-amd64/Packages"] = []byte(strings.Replace(bookwormAMD64, "Size: 315860", "Size: 315861", 1))
	base := a.server().URL + "/debian"
	if _, err := newRegistry(base, "suites=bookworm&architectures=amd64").FetchVersions(context.Background(), "curl"); !errors.Is(err, ErrChecksumMismatch) {
		t.Errorf("err = %v, want ErrChecksumMismatch", err)
	}
}

// A Filename leaving the archive root gets no artifact.
func TestUnsafeFilename(t *testing.T) {
	a := newArchive(t)
	a.index("bookworm", "main/binary-amd64", "Packages.xz", "Package: evil\nVersion: 1\nArchitecture: amd64\nFilename: ../../etc/passwd\n")
	a.release("bookworm", "bookworm", true)
	base := a.server().URL + "/debian"
	versions, err := newRegistry(base, "suites=bookworm&architectures=amd64").FetchVersions(context.Background(), "evil")
	if err != nil || len(versions) != 1 || len(versions[0].Artifacts) != 0 {
		t.Errorf("versions = %+v, %v; want one without artifacts", versions, err)
	}
	// It has no Maintainer either.
	r := newRegistry(base, "suites=bookworm&architectures=amd64")
	if got, err := r.FetchMaintainers(context.Background(), "evil"); err != nil || got != nil {
		t.Errorf("maintainers = %+v, %v; want none", got, err)
	}
}

// A Release whose SHA256 entry has the right hash but another size is
// rejected too.
func TestChecksumSizeMismatch(t *testing.T) {
	a := newArchive(t)
	a.index("bookworm", "main/binary-amd64", "Packages", bookwormAMD64)
	a.release("bookworm", "bookworm", false)
	rel := string(a.served["dists/bookworm/Release"])
	a.served["dists/bookworm/Release"] = []byte(strings.ReplaceAll(rel,
		fmt.Sprintf(" %8d main/binary-amd64/Packages\n", len(bookwormAMD64)),
		fmt.Sprintf(" %8d main/binary-amd64/Packages\n", len(bookwormAMD64)+1)))
	base := a.server().URL + "/debian"
	if _, err := newRegistry(base, "suites=bookworm&architectures=amd64").FetchVersions(context.Background(), "curl"); !errors.Is(err, ErrChecksumMismatch) {
		t.Errorf("err = %v, want ErrChecksumMismatch", err)
	}
}
