package rpm

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/git-pkgs/registries/internal/core"
	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

// Trimmed from Fedora 43 updates' primary.xml, with a second architecture,
// an older build and a package with an epoch added.
const primaryXML = `<?xml version="1.0" encoding="UTF-8"?>
<metadata xmlns="http://linux.duke.edu/metadata/common" xmlns:rpm="http://linux.duke.edu/metadata/rpm" packages="5">
<package type="rpm">
  <name>curl</name>
  <arch>x86_64</arch>
  <version epoch="0" ver="8.15.0" rel="4.fc43"/>
  <checksum type="sha256" pkgid="YES">235b92fa8916229313a33eef861f4fd6453555164f5c3d3dae199a8853f6db5a</checksum>
  <summary>A utility for getting files from remote servers</summary>
  <description>curl is a command line tool
for transferring data.</description>
  <packager>Fedora Project</packager>
  <url>https://curl.se/</url>
  <time file="1771517864" build="1771515699"/>
  <size package="234567" installed="456789" archive="460000"/>
  <location href="Packages/c/curl-8.15.0-4.fc43.x86_64.rpm"/>
  <format>
    <rpm:license>curl</rpm:license>
    <rpm:vendor>Fedora Project</rpm:vendor>
    <rpm:sourcerpm>curl-8.15.0-4.fc43.src.rpm</rpm:sourcerpm>
    <rpm:provides>
      <rpm:entry name="curl" flags="EQ" epoch="0" ver="8.15.0" rel="4.fc43"/>
    </rpm:provides>
    <rpm:requires>
      <rpm:entry name="/usr/bin/sh"/>
      <rpm:entry name="libcurl" flags="GE" epoch="0" ver="8.15.0" rel="4.fc43"/>
      <rpm:entry name="libc.so.6(GLIBC_2.34)(64bit)"/>
      <rpm:entry name="openssl-libs" flags="EQ" epoch="1" ver="3.5.4"/>
      <rpm:entry name="rpmlib(PayloadIsZstd)" flags="LE" epoch="0" ver="5.4.18" rel="1"/>
      <rpm:entry name="zlib"/>
      <rpm:entry name="zlib"/>
    </rpm:requires>
  </format>
</package>
<package type="rpm">
  <name>curl</name>
  <arch>i686</arch>
  <version epoch="0" ver="8.15.0" rel="4.fc43"/>
  <checksum type="sha256" pkgid="YES">335b92fa8916229313a33eef861f4fd6453555164f5c3d3dae199a8853f6db5b</checksum>
  <location href="Packages/c/curl-8.15.0-4.fc43.i686.rpm"/>
</package>
<package type="rpm">
  <name>curl</name>
  <arch>x86_64</arch>
  <version epoch="0" ver="8.15.0" rel="10.fc43"/>
  <checksum type="SHA512" pkgid="YES">AB5b92fa</checksum>
  <location href="Packages/c/curl-8.15.0-10.fc43.x86_64.rpm"/>
</package>
<package type="rpm">
  <name>bind</name>
  <arch>x86_64</arch>
  <version epoch="32" ver="9.18.41" rel="1.fc43"/>
  <checksum type="sha256" pkgid="YES">435b92fa8916229313a33eef861f4fd6453555164f5c3d3dae199a8853f6db5c</checksum>
  <location href="Packages/b/bind-9.18.41-1.fc43.x86_64.rpm"/>
</package>
<package type="rpm">
  <name>bind</name>
  <arch>x86_64</arch>
  <version epoch="31" ver="9.20.1" rel="1.fc43"/>
  <checksum type="sha256" pkgid="YES">535b92fa8916229313a33eef861f4fd6453555164f5c3d3dae199a8853f6db5d</checksum>
  <location href="Packages/b/bind-9.20.1-1.fc43.x86_64.rpm"/>
</package>
<package type="rpm">
  <name>tzdata</name>
  <arch>noarch</arch>
  <version epoch="0" ver="2026a" rel="1.fc43"/>
  <checksum type="sha256" pkgid="YES">635b92fa8916229313a33eef861f4fd6453555164f5c3d3dae199a8853f6db5e</checksum>
  <location href="Packages/t/tzdata-2026a-1.fc43.noarch.rpm"/>
</package>
</metadata>
`

func compress(t *testing.T, ext, s string) []byte {
	t.Helper()
	var buf bytes.Buffer
	var w interface {
		Write([]byte) (int, error)
		Close() error
	}
	var err error
	switch ext {
	case ".zst":
		w, err = zstd.NewWriter(&buf)
	case ".xz":
		w, err = xz.NewWriter(&buf)
	case ".gz":
		w = gzip.NewWriter(&buf)
	default:
		return []byte(s)
	}
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

// repo is a fake RPM repository under /os/.
type repo struct {
	mu       sync.Mutex
	files    map[string][]byte
	requests []string
}

// newRepo serves primary, compressed as ext ("" for plain .xml), and a
// repomd.xml listing it with its sha256 and size.
func newRepo(t *testing.T, ext, primary string) (*repo, string) {
	t.Helper()
	r := &repo{files: map[string][]byte{}}
	data := compress(t, ext, primary)
	sum := sha256.Sum256(data)
	href := "repodata/" + hex.EncodeToString(sum[:]) + "-primary.xml" + ext
	r.files[href] = data
	r.setRepomd("sha256", hex.EncodeToString(sum[:]), href, len(data))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		path := strings.TrimPrefix(req.URL.Path, "/os/")
		r.mu.Lock()
		r.requests = append(r.requests, path)
		body, ok := r.files[path]
		r.mu.Unlock()
		if !ok {
			http.NotFound(w, req)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return r, srv.URL + "/os"
}

func (r *repo) setRepomd(sumType, sum, href string, size int) {
	r.files["repodata/repomd.xml"] = []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<repomd xmlns="http://linux.duke.edu/metadata/repo" xmlns:rpm="http://linux.duke.edu/metadata/rpm">
  <revision>1794529563</revision>
  <data type="filelists">
    <checksum type="sha256">00</checksum>
    <location href="repodata/filelists.xml.zst"/>
    <size>1</size>
  </data>
  <data type="primary">
    <checksum type="%s">%s</checksum>
    <location href="%s"/>
    <size>%d</size>
    <open-size>1</open-size>
  </data>
</repomd>
`, sumType, sum, href, size))
}

func (r *repo) primaryHref() string {
	for path := range r.files {
		if strings.Contains(path, "-primary.xml") {
			return path
		}
	}
	return ""
}

func numbers(versions []core.Version) []string {
	var out []string
	for _, v := range versions {
		out = append(out, v.Number)
	}
	return out
}

func TestFetchVersions(t *testing.T) {
	for _, ext := range []string{".zst", ".xz", ".gz", ""} {
		t.Run("primary.xml"+ext, func(t *testing.T) {
			_, base := newRepo(t, ext, primaryXML)
			r := New(base+"?distro=fedora-43", core.DefaultClient())
			versions, err := r.FetchVersions(context.Background(), "fedora/curl")
			if err != nil {
				t.Fatalf("FetchVersions: %v", err)
			}
			if want := []string{"8.15.0-10.fc43", "8.15.0-4.fc43"}; !reflect.DeepEqual(numbers(versions), want) {
				t.Fatalf("versions = %v, want %v (newest first, by rpm version)", numbers(versions), want)
			}
			older := versions[1]
			if !older.PublishedAt.Equal(time.Unix(1771515699, 0)) {
				t.Errorf("published = %v, want the build time", older.PublishedAt)
			}
			if older.Licenses != "curl" || older.Metadata["epoch"] != "0" || older.Metadata["source_rpm"] != "curl-8.15.0-4.fc43.src.rpm" {
				t.Errorf("version = %+v", older)
			}
			want := []core.Artifact{
				{
					URL:       base + "/Packages/c/curl-8.15.0-4.fc43.x86_64.rpm",
					Filename:  "curl-8.15.0-4.fc43.x86_64.rpm",
					Integrity: "sha256-235b92fa8916229313a33eef861f4fd6453555164f5c3d3dae199a8853f6db5a",
					Size:      234567,
					Metadata:  map[string]any{"arch": "x86_64", "distro": "fedora-43", "epoch": "0"},
				},
				{
					URL:       base + "/Packages/c/curl-8.15.0-4.fc43.i686.rpm",
					Filename:  "curl-8.15.0-4.fc43.i686.rpm",
					Integrity: "sha256-335b92fa8916229313a33eef861f4fd6453555164f5c3d3dae199a8853f6db5b",
					Metadata:  map[string]any{"arch": "i686", "distro": "fedora-43", "epoch": "0"},
				},
			}
			if !reflect.DeepEqual(older.Artifacts, want) {
				t.Errorf("artifacts =\n %+v\nwant\n %+v", older.Artifacts, want)
			}
			// The checksum's own type names the integrity, lowercased.
			if got := versions[0].Artifacts[0].Integrity; got != "sha512-ab5b92fa" {
				t.Errorf("sha512 integrity = %q", got)
			}
		})
	}
}

// The epoch orders versions but is not part of the version number, as in
// the rpm PURL type, where it is a qualifier.
func TestEpoch(t *testing.T) {
	_, base := newRepo(t, ".zst", primaryXML)
	versions, err := New(base, core.DefaultClient()).FetchVersions(context.Background(), "bind")
	if err != nil {
		t.Fatalf("FetchVersions: %v", err)
	}
	if want := []string{"9.18.41-1.fc43", "9.20.1-1.fc43"}; !reflect.DeepEqual(numbers(versions), want) {
		t.Fatalf("versions = %v, want %v (epoch 32 before epoch 31)", numbers(versions), want)
	}
	if e := versions[0].Metadata["epoch"]; e != "32" {
		t.Errorf("epoch = %v", e)
	}
	if e := versions[0].Artifacts[0].Metadata["epoch"]; e != "32" {
		t.Errorf("artifact epoch = %v", e)
	}
}

// Without a distro in the URL none is recorded, and noarch packages record
// their own architecture.
func TestNoarchAndNoDistro(t *testing.T) {
	_, base := newRepo(t, ".zst", primaryXML)
	versions, err := New(base+"/", core.DefaultClient()).FetchVersions(context.Background(), "tzdata")
	if err != nil || len(versions) != 1 || len(versions[0].Artifacts) != 1 {
		t.Fatalf("FetchVersions = %+v, %v", versions, err)
	}
	if got, want := versions[0].Artifacts[0].Metadata, (map[string]any{"arch": "noarch", "epoch": "0"}); !reflect.DeepEqual(got, want) {
		t.Errorf("metadata = %v, want %v", got, want)
	}
	if got, want := versions[0].Artifacts[0].URL, base+"/Packages/t/tzdata-2026a-1.fc43.noarch.rpm"; got != want {
		t.Errorf("URL = %q, want %q", got, want)
	}
}

// Primary metadata is streamed, so the client's limit on buffered
// responses doesn't apply to it.
func TestPrimaryLargerThanResponseCap(t *testing.T) {
	_, base := newRepo(t, "", primaryXML)
	client := core.NewClient(core.WithMaxRetries(0))
	client.MaxResponseSize = 1024 // repomd.xml fits, primary.xml doesn't
	if _, err := New(base, client).FetchVersions(context.Background(), "curl"); err != nil {
		t.Errorf("FetchVersions: %v", err)
	}
}

func TestChecksumMismatch(t *testing.T) {
	other := strings.Replace(primaryXML, "8.15.0", "8.15.1", 1)
	for _, ext := range []string{".zst", ".xz", ".gz", ""} {
		t.Run("primary.xml"+ext, func(t *testing.T) {
			rp, base := newRepo(t, ext, primaryXML)
			href := rp.primaryHref()
			good := rp.files[href]
			rp.files[href] = compress(t, ext, other)

			r := New(base, core.DefaultClient())
			if _, err := r.FetchVersions(context.Background(), "curl"); !errors.Is(err, ErrChecksumMismatch) {
				t.Fatalf("err = %v, want ErrChecksumMismatch", err)
			}
			// Nothing was kept: the next lookup reads the repository again.
			rp.files[href] = good
			if _, err := r.FetchVersions(context.Background(), "curl"); err != nil {
				t.Errorf("after the repository was fixed: %v", err)
			}
		})
	}
}

// The right hash with another size is rejected too.
func TestChecksumSizeMismatch(t *testing.T) {
	rp, base := newRepo(t, "", primaryXML)
	sum := sha256.Sum256([]byte(primaryXML))
	rp.setRepomd("sha256", hex.EncodeToString(sum[:]), rp.primaryHref(), len(primaryXML)+1)
	if _, err := New(base, core.DefaultClient()).FetchVersions(context.Background(), "curl"); !errors.Is(err, ErrChecksumMismatch) {
		t.Errorf("err = %v, want ErrChecksumMismatch", err)
	}
}

func TestChecksumTypes(t *testing.T) {
	sum512 := sha512.Sum512([]byte(primaryXML))
	for name, tt := range map[string]struct {
		sumType, sum string
		wantErr      error
	}{
		"sha512":       {"sha512", hex.EncodeToString(sum512[:]), nil},
		"sha512 upper": {"SHA512", strings.ToUpper(hex.EncodeToString(sum512[:])), nil},
		"sha512 wrong": {"sha512", "00", ErrChecksumMismatch},
		"unknown type": {"md5", "00", ErrMalformedMetadata},
	} {
		t.Run(name, func(t *testing.T) {
			rp, base := newRepo(t, "", primaryXML)
			rp.setRepomd(tt.sumType, tt.sum, rp.primaryHref(), len(primaryXML))
			_, err := New(base, core.DefaultClient()).FetchVersions(context.Background(), "curl")
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestInvalidConfiguration(t *testing.T) {
	for name, url := range map[string]string{"no url": "", "no host": "/pub/fedora"} {
		t.Run(name, func(t *testing.T) {
			_, err := New(url, core.DefaultClient()).FetchVersions(context.Background(), "curl")
			if !errors.Is(err, ErrInvalidConfig) {
				t.Errorf("err = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestMalformedRepomd(t *testing.T) {
	for name, repomd := range map[string]string{
		"not xml":                    "<html>",
		"no primary":                 `<repomd><data type="filelists"><location href="repodata/f.xml.gz"/></data></repomd>`,
		"href leaves the repository": `<repomd><data type="primary"><location href="../../etc/primary.xml.gz"/></data></repomd>`,
		"absolute href":              `<repomd><data type="primary"><location href="https://evil.example/primary.xml.gz"/></data></repomd>`,
	} {
		t.Run(name, func(t *testing.T) {
			rp, base := newRepo(t, "", primaryXML)
			rp.files["repodata/repomd.xml"] = []byte(repomd)
			_, err := New(base, core.DefaultClient()).FetchVersions(context.Background(), "curl")
			if !errors.Is(err, ErrMalformedMetadata) {
				t.Errorf("err = %v, want ErrMalformedMetadata", err)
			}
		})
	}
}

func TestUnsupportedCompression(t *testing.T) {
	rp, base := newRepo(t, "", primaryXML)
	rp.setRepomd("sha256", "00", "repodata/primary.xml.zck", 1)
	_, err := New(base, core.DefaultClient()).FetchVersions(context.Background(), "curl")
	if !errors.Is(err, ErrUnsupportedCompression) {
		t.Errorf("err = %v, want ErrUnsupportedCompression", err)
	}
}

// A repomd.xml that lists a checksum but no size checks the checksum alone.
func TestChecksumWithoutSize(t *testing.T) {
	rp, base := newRepo(t, ".gz", primaryXML)
	href := rp.primaryHref()
	sum := sha256.Sum256(rp.files[href])
	repomd := `<repomd><data type="primary"><checksum type="sha256">%s</checksum><location href="` + href + `"/></data></repomd>`
	rp.files["repodata/repomd.xml"] = []byte(fmt.Sprintf(repomd, hex.EncodeToString(sum[:])))
	if _, err := New(base, core.DefaultClient()).FetchVersions(context.Background(), "curl"); err != nil {
		t.Errorf("FetchVersions: %v", err)
	}
	rp.files["repodata/repomd.xml"] = []byte(fmt.Sprintf(repomd, "00"))
	if _, err := New(base, core.DefaultClient()).FetchVersions(context.Background(), "curl"); !errors.Is(err, ErrChecksumMismatch) {
		t.Errorf("wrong checksum: err = %v, want ErrChecksumMismatch", err)
	}
}

// A repomd.xml without a checksum for primary leaves it unchecked.
func TestNoChecksum(t *testing.T) {
	rp, base := newRepo(t, ".gz", primaryXML)
	rp.files["repodata/repomd.xml"] = []byte(`<repomd><data type="primary"><location href="` + rp.primaryHref() + `"/></data></repomd>`)
	if _, err := New(base, core.DefaultClient()).FetchVersions(context.Background(), "curl"); err != nil {
		t.Errorf("FetchVersions: %v", err)
	}
}

func TestMalformedPrimary(t *testing.T) {
	for _, ext := range []string{".zst", ".xz", ".gz"} {
		t.Run("not"+ext, func(t *testing.T) {
			rp, base := newRepo(t, "", primaryXML)
			garbage := []byte(strings.Repeat("not compressed data ", 10))
			sum := sha256.Sum256(garbage)
			rp.files["repodata/primary.xml"+ext] = garbage
			rp.setRepomd("sha256", hex.EncodeToString(sum[:]), "repodata/primary.xml"+ext, len(garbage))
			_, err := New(base, core.DefaultClient()).FetchVersions(context.Background(), "curl")
			if !errors.Is(err, ErrMalformedMetadata) {
				t.Errorf("err = %v, want ErrMalformedMetadata", err)
			}
		})
	}
	t.Run("truncated xml", func(t *testing.T) {
		_, base := newRepo(t, "", primaryXML[:len(primaryXML)/2])
		_, err := New(base, core.DefaultClient()).FetchVersions(context.Background(), "curl")
		if !errors.Is(err, ErrMalformedMetadata) {
			t.Errorf("err = %v, want ErrMalformedMetadata", err)
		}
	})
	t.Run("package without a name", func(t *testing.T) {
		_, base := newRepo(t, "", `<metadata><package type="rpm"><arch>x86_64</arch><version ver="1" rel="1"/></package></metadata>`)
		_, err := New(base, core.DefaultClient()).FetchVersions(context.Background(), "curl")
		if !errors.Is(err, ErrMalformedMetadata) {
			t.Errorf("err = %v, want ErrMalformedMetadata", err)
		}
	})
}

func TestDecompressedLimit(t *testing.T) {
	for _, ext := range []string{".zst", ".xz", ".gz", ""} {
		t.Run("primary.xml"+ext, func(t *testing.T) {
			_, base := newRepo(t, ext, primaryXML)
			r := New(base, core.DefaultClient())
			r.maxPrimarySize = int64(len(primaryXML)) - 1
			if _, err := r.FetchVersions(context.Background(), "curl"); !errors.Is(err, ErrMalformedMetadata) || !strings.Contains(err.Error(), "decompresses to more than") {
				t.Errorf("over the limit: err = %v, want ErrMalformedMetadata naming the limit", err)
			}
			// Cut mid-document, the limit is still what the error names,
			// not the XML it broke.
			r = New(base, core.DefaultClient())
			r.maxPrimarySize = int64(len(primaryXML)) / 2
			if _, err := r.FetchVersions(context.Background(), "curl"); !errors.Is(err, ErrMalformedMetadata) || !strings.Contains(err.Error(), "decompresses to more than") {
				t.Errorf("far over the limit: err = %v, want ErrMalformedMetadata naming the limit", err)
			}
			r = New(base, core.DefaultClient())
			r.maxPrimarySize = int64(len(primaryXML))
			if _, err := r.FetchVersions(context.Background(), "curl"); err != nil {
				t.Errorf("at the limit: %v", err)
			}
		})
	}
}

func TestXZSizeLimit(t *testing.T) {
	rp, base := newRepo(t, ".xz", primaryXML)
	size := int64(len(rp.files[rp.primaryHref()]))
	r := New(base, core.DefaultClient())
	r.maxXZSize = size - 1
	if _, err := r.FetchVersions(context.Background(), "curl"); !errors.Is(err, ErrMalformedMetadata) {
		t.Errorf("over the limit: err = %v, want ErrMalformedMetadata", err)
	}
	r = New(base, core.DefaultClient())
	r.maxXZSize = size
	if _, err := r.FetchVersions(context.Background(), "curl"); err != nil {
		t.Errorf("at the limit: %v", err)
	}
}

// A zstd frame asking for a 256 MiB window, which the decoder would allow
// by default, is refused rather than allocated.
func TestZstdWindowRefused(t *testing.T) {
	rp, base := newRepo(t, "", primaryXML)
	// Magic, a frame header with no content size, a window descriptor of
	// 2^28 bytes, and one last raw block holding a document.
	frame := append([]byte{0x28, 0xB5, 0x2F, 0xFD, 0x00, 0x90, 0x61, 0x00, 0x00}, "<metadata/>\n"...)
	sum := sha256.Sum256(frame)
	rp.files["repodata/primary.xml.zst"] = frame
	rp.setRepomd("sha256", hex.EncodeToString(sum[:]), "repodata/primary.xml.zst", len(frame))
	_, err := New(base, core.DefaultClient()).FetchVersions(context.Background(), "curl")
	if !errors.Is(err, ErrMalformedMetadata) {
		t.Errorf("err = %v, want ErrMalformedMetadata", err)
	}
}

func TestNotFound(t *testing.T) {
	_, base := newRepo(t, ".zst", primaryXML)
	r := New(base, core.DefaultClient())
	var notFound *core.NotFoundError
	if _, err := r.FetchVersions(context.Background(), "missing"); !errors.As(err, &notFound) {
		t.Errorf("missing package: err = %v, want NotFoundError", err)
	}
	if _, err := r.FetchDependencies(context.Background(), "curl", "1.0-1"); !errors.As(err, &notFound) || notFound.Version != "1.0-1" {
		t.Errorf("missing version: err = %v, want NotFoundError for 1.0-1", err)
	}
}

// A package whose file is outside the repository, by its href or by an
// xml:base pointing elsewhere, gets no artifact. Other package types are
// not read.
func TestUnsafeLocations(t *testing.T) {
	primary := `<metadata xmlns="http://linux.duke.edu/metadata/common">
<package type="rpm"><name>up</name><arch>x86_64</arch><version epoch="0" ver="1" rel="1"/><location href="../../etc/passwd"/></package>
<package type="rpm"><name>based</name><arch>x86_64</arch><version epoch="0" ver="1" rel="1"/><location xml:base="https://evil.example/repo" href="Packages/based-1-1.x86_64.rpm"/></package>
<package type="rpm"><name>none</name><arch>x86_64</arch><version epoch="0" ver="1" rel="1"/></package>
<package type="rpm"><name>bare</name><arch>x86_64</arch><version ver="1" rel="1"/><location href="Packages/bare-1-1.x86_64.rpm"/></package>
<package type="rpm"><name>bare</name><arch>x86_64</arch><version ver="1" rel="1"/><location href="Packages/again/bare-1-1.x86_64.rpm"/></package>
<package type="deb"><name>other</name><arch>x86_64</arch><version epoch="0" ver="1" rel="1"/><location href="Packages/other.rpm"/></package>
</metadata>`
	_, base := newRepo(t, "", primary)
	r := New(base, core.DefaultClient())
	for _, name := range []string{"up", "based", "none"} {
		versions, err := r.FetchVersions(context.Background(), name)
		if err != nil || len(versions) != 1 || len(versions[0].Artifacts) != 0 {
			t.Errorf("%s: versions = %+v, %v; want one without artifacts", name, versions, err)
		}
	}
	// No epoch means 0, no checksum means no integrity, and a package
	// listed twice is recorded once.
	bare, err := r.FetchVersions(context.Background(), "bare")
	if err != nil || len(bare) != 1 || len(bare[0].Artifacts) != 1 {
		t.Fatalf("bare: versions = %+v, %v; want one with one artifact", bare, err)
	}
	if a := bare[0].Artifacts[0]; a.Integrity != "" || a.Metadata["epoch"] != "0" || a.Filename != "bare-1-1.x86_64.rpm" || !strings.HasSuffix(a.URL, "/Packages/bare-1-1.x86_64.rpm") {
		t.Errorf("bare artifact = %+v", a)
	}
	var notFound *core.NotFoundError
	if _, err := r.FetchVersions(context.Background(), "other"); !errors.As(err, &notFound) {
		t.Errorf("non-rpm package: err = %v, want NotFoundError", err)
	}
	if got, err := r.FetchMaintainers(context.Background(), "up"); err != nil || got != nil {
		t.Errorf("maintainers = %+v, %v; want none without a packager", got, err)
	}
}

func TestFetchPackage(t *testing.T) {
	_, base := newRepo(t, ".zst", primaryXML)
	r := New(base, core.DefaultClient())
	pkg, err := r.FetchPackage(context.Background(), "fedora/curl")
	if err != nil {
		t.Fatalf("FetchPackage: %v", err)
	}
	// The newest build has no summary of its own in this fixture.
	if pkg.Name != "curl" || pkg.Namespace != "fedora" || pkg.LatestVersion != "8.15.0-10.fc43" {
		t.Errorf("package = %+v", pkg)
	}
	pkg, err = New(base, core.DefaultClient()).FetchPackage(context.Background(), "bind")
	if err != nil || pkg.LatestVersion != "9.18.41-1.fc43" || pkg.Metadata["epoch"] != "32" {
		t.Errorf("bind = %+v, %v", pkg, err)
	}
}

func TestFetchPackageDetails(t *testing.T) {
	single := primaryXML[:strings.Index(primaryXML, "</package>")+len("</package>")] + "\n</metadata>"
	_, base := newRepo(t, ".gz", single)
	pkg, err := New(base, core.DefaultClient()).FetchPackage(context.Background(), "curl")
	if err != nil {
		t.Fatalf("FetchPackage: %v", err)
	}
	if pkg.Description != "A utility for getting files from remote servers" || pkg.Homepage != "https://curl.se/" || pkg.Licenses != "curl" {
		t.Errorf("package = %+v", pkg)
	}
}

// Dependencies are the requirements that name packages: file paths,
// sonames and other capabilities are left out.
func TestFetchDependencies(t *testing.T) {
	_, base := newRepo(t, ".zst", primaryXML)
	deps, err := New(base, core.DefaultClient()).FetchDependencies(context.Background(), "curl", "8.15.0-4.fc43")
	if err != nil {
		t.Fatalf("FetchDependencies: %v", err)
	}
	want := []core.Dependency{
		{Name: "libcurl", Requirements: ">= 8.15.0-4.fc43", Scope: core.Runtime},
		{Name: "openssl-libs", Requirements: "= 1:3.5.4", Scope: core.Runtime},
		{Name: "zlib", Scope: core.Runtime},
	}
	if !reflect.DeepEqual(deps, want) {
		t.Errorf("deps =\n %+v\nwant\n %+v", deps, want)
	}
}

func TestRequirement(t *testing.T) {
	for _, tt := range []struct {
		flags, epoch, ver, rel, want string
	}{
		{"EQ", "0", "1.0", "1", "= 1.0-1"},
		{"GE", "", "1.0", "", ">= 1.0"},
		{"LE", "2", "1.0", "1", "<= 2:1.0-1"},
		{"GT", "0", "1.0", "", "> 1.0"},
		{"LT", "0", "1.0", "", "< 1.0"},
		{"", "0", "", "", ""},
		{"EQ", "0", "", "", ""},
		{"XX", "0", "1.0", "", ""},
	} {
		if got := requirement(tt.flags, tt.epoch, tt.ver, tt.rel); got != tt.want {
			t.Errorf("requirement(%q, %q, %q, %q) = %q, want %q", tt.flags, tt.epoch, tt.ver, tt.rel, got, tt.want)
		}
	}
}

func TestFetchMaintainers(t *testing.T) {
	single := primaryXML[:strings.Index(primaryXML, "</package>")+len("</package>")] + "\n</metadata>"
	_, base := newRepo(t, ".gz", single)
	got, err := New(base, core.DefaultClient()).FetchMaintainers(context.Background(), "curl")
	if err != nil || !reflect.DeepEqual(got, []core.Maintainer{{Name: "Fedora Project"}}) {
		t.Errorf("maintainers = %+v, %v", got, err)
	}
}

func TestURLs(t *testing.T) {
	u := New("https://dl.fedoraproject.org/pub/fedora/linux/releases/43/Everything/x86_64/os/?distro=fedora-43", nil).URLs()
	if got := u.Download("curl", "8.15.0-4.fc43"); got != "" {
		t.Errorf("Download = %q, want none without the metadata", got)
	}
	// repository_url is the whole configured URL, distro included.
	const repo = "?repository_url=https:%2F%2Fdl.fedoraproject.org%2Fpub%2Ffedora%2Flinux%2Freleases%2F43%2FEverything%2Fx86_64%2Fos"
	if got, want := u.PURL("fedora/curl", "8.15.0-4.fc43"), "pkg:rpm/fedora/curl@8.15.0-4.fc43"+repo+"%3Fdistro%3Dfedora-43"; got != want {
		t.Errorf("PURL = %q, want %q", got, want)
	}
	u = New("https://dl.fedoraproject.org/pub/fedora/linux/releases/43/Everything/x86_64/os", nil).URLs()
	if got, want := u.PURL("fedora/curl", "8.15.0-4.fc43"), "pkg:rpm/fedora/curl@8.15.0-4.fc43"+repo; got != want {
		t.Errorf("PURL without a distro = %q, want %q", got, want)
	}
	if got, want := New("", nil).URLs().PURL("fedora/curl", "1-1"), "pkg:rpm/fedora/curl@1-1"; got != want {
		t.Errorf("PURL without a repository = %q, want %q", got, want)
	}
}

// A PURL the registry builds leads back to the same repository and distro.
func TestPURLRoundTrip(t *testing.T) {
	_, base := newRepo(t, ".zst", primaryXML)
	r := New(base+"?distro=fedora-43", core.DefaultClient())
	reg, name, version, err := core.NewFromPURL(r.URLs().PURL("fedora/curl", "8.15.0-4.fc43"), core.DefaultClient())
	if err != nil || name != "fedora/curl" || version != "8.15.0-4.fc43" {
		t.Fatalf("NewFromPURL = %q, %q, %v", name, version, err)
	}
	versions, err := reg.FetchVersions(context.Background(), name)
	if err != nil || len(versions) != 2 || versions[1].Artifacts[0].Metadata["distro"] != "fedora-43" {
		t.Errorf("FetchVersions = %+v, %v", versions, err)
	}
}

// The metadata is read once per Registry.
func TestMetadataReadOnce(t *testing.T) {
	rp, base := newRepo(t, ".zst", primaryXML)
	r := New(base, core.DefaultClient())
	for range 3 {
		if _, err := r.FetchVersions(context.Background(), "curl"); err != nil {
			t.Fatal(err)
		}
	}
	if len(rp.requests) != 2 {
		t.Errorf("requests = %v, want repomd.xml and primary once each", rp.requests)
	}
}

func TestSafeHref(t *testing.T) {
	for in, ok := range map[string]bool{
		"Packages/c/curl-8.15.0-4.fc43.x86_64.rpm": true,
		"":                          false,
		"/etc/passwd":               false,
		"Packages/../../etc/passwd": false,
		"Packages/x.rpm?query":      false,
		"Packages/x.rpm#frag":       false,
		`Packages\x.rpm`:            false,
		"Packages/%2e%2e/x.rpm":     false,
		"http://evil.example/x.rpm": false,
	} {
		if got := safeHref(in); got != ok {
			t.Errorf("safeHref(%q) = %v, want %v", in, got, ok)
		}
	}
}
