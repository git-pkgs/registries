package apk

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/git-pkgs/registries/internal/core"
)

// buildIndex packs APKINDEX text the way Alpine does: a signature gzip
// stream holding an unterminated tar, then a stream with DESCRIPTION and
// APKINDEX.
func buildIndex(t *testing.T, index string) []byte {
	t.Helper()
	var out bytes.Buffer

	sig := gzip.NewWriter(&out)
	sigTar := tar.NewWriter(sig)
	writeMember(t, sigTar, ".SIGN.RSA.alpine-devel@lists.alpinelinux.org-6165ee59.rsa.pub", "signature")
	if err := sigTar.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := sig.Close(); err != nil {
		t.Fatal(err)
	}

	body := gzip.NewWriter(&out)
	bodyTar := tar.NewWriter(body)
	writeMember(t, bodyTar, "DESCRIPTION", "v3.23.6-70-g8c6e6d800ba")
	writeMember(t, bodyTar, "APKINDEX", index)
	if err := bodyTar.Close(); err != nil {
		t.Fatal(err)
	}
	if err := body.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func writeMember(t *testing.T, tw *tar.Writer, name, content string) {
	t.Helper()
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
}

func realIndex(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("testdata/APKINDEX")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// repoServer serves a v2 repository per architecture directory, with no
// Packages.adb.
func repoServer(t *testing.T, indexes map[string]string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dir, file, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
		index, ok := indexes[dir]
		if !ok || file != indexV2 {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(buildIndex(t, index))
	}))
	t.Cleanup(server.Close)
	return server
}

func TestFetchVersionsRealIndex(t *testing.T) {
	server := repoServer(t, map[string]string{"x86_64": realIndex(t)})
	reg := New(server.URL+"/x86_64", core.DefaultClient())

	versions, err := reg.FetchVersions(context.Background(), "alpine/curl")
	if err != nil {
		t.Fatalf("FetchVersions failed: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("got %d versions, want 1", len(versions))
	}
	v := versions[0]
	if v.Number != "8.22.0-r0" || v.Licenses != "curl" {
		t.Errorf("version = %+v", v)
	}
	if !v.PublishedAt.Equal(time.Unix(1788539493, 0)) {
		t.Errorf("PublishedAt = %v", v.PublishedAt)
	}
	if v.Integrity != "" {
		t.Errorf("Integrity = %q, want empty: Q1 covers only the control section", v.Integrity)
	}
	wantMeta := map[string]any{
		"checksum":           "Q1Je9Cwt4OmEyiSGWGtYfOVCrvgWE=",
		"checksum_algorithm": "sha1",
		"checksum_hex":       "25ef42c2de0e984ca2486586b587ce542aef8161",
		"checksum_covers":    "control",
		"origin":             "curl",
		"commit":             "ce63a8e353eacc32ad36fd49ae1f427f4306da87",
		"arch":               "x86_64",
		"installed_size":     int64(285023),
		"size":               int64(178681),
	}
	for k, want := range wantMeta {
		if v.Metadata[k] != want {
			t.Errorf("Metadata[%s] = %#v, want %#v", k, v.Metadata[k], want)
		}
	}
	provides, _ := v.Metadata["provides"].([]string)
	if strings.Join(provides, " ") != "wcurl=8.22.0-r0 cmd:curl=8.22.0-r0 cmd:wcurl=8.22.0-r0" {
		t.Errorf("provides = %v", provides)
	}

	a := v.Artifacts[0]
	if a.URL != server.URL+"/x86_64/curl-8.22.0-r0.apk" || a.Filename != "curl-8.22.0-r0.apk" || a.Size != 178681 || a.Metadata["arch"] != "x86_64" {
		t.Errorf("artifact = %+v", a)
	}

	doc, err := reg.FetchVersions(context.Background(), "curl-doc")
	if err != nil {
		t.Fatalf("FetchVersions curl-doc failed: %v", err)
	}
	installIf, _ := doc[0].Metadata["install_if"].([]string)
	if strings.Join(installIf, " ") != "docs curl=8.22.0-r0" {
		t.Errorf("install_if = %v", installIf)
	}

	sevenZip, err := reg.FetchVersions(context.Background(), "7zip")
	if err != nil {
		t.Fatalf("FetchVersions 7zip failed: %v", err)
	}
	if sevenZip[0].Metadata["provider_priority"] != int64(100) {
		t.Errorf("provider_priority = %#v", sevenZip[0].Metadata["provider_priority"])
	}
}

func TestFetchPackageKeepsNamespace(t *testing.T) {
	server := repoServer(t, map[string]string{"x86_64": realIndex(t)})
	reg := New(server.URL+"/x86_64/", core.DefaultClient())

	pkg, err := reg.FetchPackage(context.Background(), "alpine/curl")
	if err != nil {
		t.Fatalf("FetchPackage failed: %v", err)
	}
	if pkg.Name != "curl" || pkg.Namespace != "alpine" || pkg.LatestVersion != "8.22.0-r0" ||
		pkg.Homepage != "https://curl.se/" || pkg.Description != "URL retrieval utility and library" {
		t.Errorf("package = %+v", pkg)
	}

	bare, err := reg.FetchPackage(context.Background(), "curl")
	if err != nil || bare.Namespace != "" {
		t.Errorf("bare name package = %+v, %v", bare, err)
	}
}

func TestFetchDependencies(t *testing.T) {
	index := realIndex(t) + "P:withconflict\nV:1.0-r0\nD:busybox>=1.36 !curl pc:zlib~1.3 so:libc.musl-x86_64.so.1\n"
	server := repoServer(t, map[string]string{"x86_64": index})
	reg := New(server.URL+"/x86_64", core.DefaultClient())

	deps, err := reg.FetchDependencies(context.Background(), "alpine/curl", "8.22.0-r0")
	if err != nil {
		t.Fatalf("FetchDependencies failed: %v", err)
	}
	var got []string
	for _, d := range deps {
		if d.Scope != core.Runtime {
			t.Errorf("%s scope = %q, want runtime", d.Name, d.Scope)
		}
		got = append(got, d.Name+d.Requirements)
	}
	if strings.Join(got, " ") != "libcurl=8.22.0-r0 so:libc.musl-x86_64.so.1 so:libcurl.so.4 so:libz.so.1" {
		t.Errorf("deps = %v", got)
	}

	deps, err = reg.FetchDependencies(context.Background(), "withconflict", "1.0-r0")
	if err != nil {
		t.Fatalf("FetchDependencies failed: %v", err)
	}
	got = nil
	for _, d := range deps {
		got = append(got, d.Name+"|"+d.Requirements)
	}
	if strings.Join(got, " ") != "busybox|>=1.36 pc:zlib|~1.3 so:libc.musl-x86_64.so.1|" {
		t.Errorf("deps = %v, want conflicts left out", got)
	}

	var notFound *core.NotFoundError
	if _, err := reg.FetchDependencies(context.Background(), "curl", "8.21.0-r0"); !errors.As(err, &notFound) || notFound.Version != "8.21.0-r0" {
		t.Errorf("missing version err = %v", err)
	}
}

func TestExactVersionsWithRevisions(t *testing.T) {
	index := "P:pkg\nV:1.2.3-r9\nA:x86_64\n\nP:pkg\nV:1.2.3-r10\nA:x86_64\n\nP:pkg\nV:1.2.3_rc1-r0\nA:x86_64\n"
	server := repoServer(t, map[string]string{"x86_64": index})
	reg := New(server.URL+"/x86_64", core.DefaultClient())

	versions, err := reg.FetchVersions(context.Background(), "pkg")
	if err != nil {
		t.Fatalf("FetchVersions failed: %v", err)
	}
	var got []string
	for _, v := range versions {
		got = append(got, v.Number)
	}
	if strings.Join(got, " ") != "1.2.3-r10 1.2.3-r9 1.2.3_rc1-r0" {
		t.Errorf("versions = %v, want apk ordering", got)
	}
	if _, err := reg.FetchDependencies(context.Background(), "pkg", "1.2.3-r9"); err != nil {
		t.Errorf("exact -r9 lookup failed: %v", err)
	}
	if _, err := reg.FetchDependencies(context.Background(), "pkg", "1.2.3"); err == nil {
		t.Error("1.2.3 matched without its revision")
	}
}

func TestTwoArchitectures(t *testing.T) {
	server := repoServer(t, map[string]string{
		"x86_64":  "P:curl\nV:8.22.0-r0\nA:x86_64\nS:100\n",
		"aarch64": "P:curl\nV:8.22.0-r0\nA:aarch64\nS:200\n",
	})
	for arch, size := range map[string]int64{"x86_64": 100, "aarch64": 200} {
		versions, err := New(server.URL+"/"+arch, core.DefaultClient()).FetchVersions(context.Background(), "curl")
		if err != nil {
			t.Fatalf("%s: %v", arch, err)
		}
		a := versions[0].Artifacts[0]
		if a.Metadata["arch"] != arch || a.Size != size || !strings.Contains(a.URL, "/"+arch+"/") {
			t.Errorf("%s artifact = %+v", arch, a)
		}
	}
}

func TestExactIndexURL(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		_, _ = w.Write(buildIndex(t, "P:curl\nV:8.22.0-r0\n"))
	}))
	defer server.Close()

	reg := New(server.URL+"/v3.23/main/x86_64/APKINDEX.tar.gz", core.DefaultClient())
	versions, err := reg.FetchVersions(context.Background(), "curl")
	if err != nil {
		t.Fatalf("FetchVersions failed: %v", err)
	}
	if want := server.URL + "/v3.23/main/x86_64/curl-8.22.0-r0.apk"; versions[0].Artifacts[0].URL != want {
		t.Errorf("URL = %q, want %q", versions[0].Artifacts[0].URL, want)
	}
	if _, err := reg.FetchPackage(context.Background(), "curl"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(requests, " ") != "/v3.23/main/x86_64/APKINDEX.tar.gz" {
		t.Errorf("requests = %v, want one index fetch with no Packages.adb probe", requests)
	}
}

func TestV3Index(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/"+indexV3) {
			_, _ = w.Write([]byte("ADB.indx"))
			return
		}
		_, _ = w.Write(buildIndex(t, "P:curl\nV:8.22.0-r0\n"))
	}))
	defer server.Close()

	for _, base := range []string{server.URL + "/x86_64", server.URL + "/x86_64/Packages.adb"} {
		_, err := New(base, core.DefaultClient()).FetchVersions(context.Background(), "curl")
		if !errors.Is(err, ErrV3IndexUnsupported) {
			t.Errorf("%s: err = %v, want ErrV3IndexUnsupported", base, err)
		}
	}
}

func TestProbeErrorIsNotFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/"+indexV3) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write(buildIndex(t, "P:curl\nV:8.22.0-r0\n"))
	}))
	defer server.Close()

	_, err := New(server.URL+"/x86_64", core.DefaultClient()).FetchVersions(context.Background(), "curl")
	var httpErr *core.HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusForbidden {
		t.Errorf("err = %v, want the 403 from Packages.adb", err)
	}
}

func TestMalformedIndexes(t *testing.T) {
	valid := buildIndex(t, realIndex(t))
	badChecksum := append([]byte(nil), valid...)
	badChecksum[len(badChecksum)-8] ^= 1
	tests := map[string][]byte{
		"not gzip":              []byte("not an index"),
		"truncated":             valid[:len(valid)/2],
		"missing gzip footer":   valid[:len(valid)-8],
		"corrupt gzip checksum": badChecksum,
		"no APKINDEX member":    tarGz(t, "DESCRIPTION", "x"),
		"line without a field":  buildIndex(t, "P:curl\nV:1.0-r0\nnonsense\n"),
		"record without name":   buildIndex(t, "V:1.0-r0\nA:x86_64\n"),
		"bad size":              buildIndex(t, "P:curl\nV:1.0-r0\nS:big\n"),
		"bad build time":        buildIndex(t, "P:curl\nV:1.0-r0\nt:yesterday\n"),
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, indexV2) {
					_, _ = w.Write(body)
					return
				}
				http.NotFound(w, r)
			}))
			defer server.Close()

			_, err := New(server.URL+"/x86_64", core.DefaultClient()).FetchVersions(context.Background(), "curl")
			if !errors.Is(err, ErrMalformedIndex) {
				t.Errorf("err = %v, want ErrMalformedIndex", err)
			}
		})
	}
}

func tarGz(t *testing.T, name, content string) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	writeMember(t, tw, name, content)
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestTrailingIndexDataSizeLimit(t *testing.T) {
	body := bytes.NewBuffer(buildIndex(t, "P:curl\nV:1.0-r0\n"))
	gz := gzip.NewWriter(body)
	if _, err := io.Copy(gz, strings.NewReader(strings.Repeat(" ", maxIndexSize))); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body.Bytes())
	}))
	defer server.Close()

	_, err := New(server.URL+"/APKINDEX.tar.gz", core.DefaultClient()).FetchVersions(context.Background(), "curl")
	if !errors.Is(err, ErrMalformedIndex) || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("err = %v, want index archive size limit error", err)
	}
}

func TestMissingOptionalFields(t *testing.T) {
	server := repoServer(t, map[string]string{"x86_64": "P:minimal\nV:0.1-r0\n"})
	reg := New(server.URL+"/x86_64", core.DefaultClient())

	versions, err := reg.FetchVersions(context.Background(), "minimal")
	if err != nil {
		t.Fatalf("FetchVersions failed: %v", err)
	}
	v := versions[0]
	if !v.PublishedAt.IsZero() || v.Licenses != "" || v.Metadata["checksum"] != nil {
		t.Errorf("version = %+v", v)
	}
	if deps, err := reg.FetchDependencies(context.Background(), "minimal", "0.1-r0"); err != nil || len(deps) != 0 {
		t.Errorf("deps = %v, %v", deps, err)
	}
	if m, err := reg.FetchMaintainers(context.Background(), "minimal"); err != nil || m != nil {
		t.Errorf("maintainers = %v, %v", m, err)
	}
}

func TestNotFoundAndCaching(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path == "/x86_64/"+indexV2 {
			_, _ = w.Write(buildIndex(t, "P:curl\nV:8.22.0-r0\n"))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	reg := New(server.URL+"/x86_64", core.DefaultClient())
	var notFound *core.NotFoundError
	for range 3 {
		if _, err := reg.FetchVersions(context.Background(), "alpine/missing"); !errors.As(err, &notFound) {
			t.Fatalf("err = %v, want NotFoundError", err)
		}
	}
	if requests != 2 {
		t.Errorf("requests = %d, want the index probed and fetched once", requests)
	}

	missingRepo := New(server.URL+"/nope/x86_64/APKINDEX.tar.gz", core.DefaultClient())
	if _, err := missingRepo.FetchVersions(context.Background(), "curl"); err == nil {
		t.Error("expected an error for a missing index")
	}
}

func TestMaintainers(t *testing.T) {
	tests := map[string]core.Maintainer{
		"Achill Gilgenast <achill@achill.org>":           {Name: "Achill Gilgenast", Email: "achill@achill.org"},
		"Alex Xu (Hello71) <alex_y_xu@yahoo.ca>":         {Name: "Alex Xu (Hello71)", Email: "alex_y_xu@yahoo.ca"},
		"Sören Tempel <soeren+alpine@soeren-tempel.net>": {Name: "Sören Tempel", Email: "soeren+alpine@soeren-tempel.net"},
		"Natanael Copa":         {Name: "Natanael Copa"},
		"broken <not an email>": {Name: "broken <not an email>"},
		"<only@example.com>":    {Email: "only@example.com"},
	}
	for value, want := range tests {
		if got := parseMaintainer(value); got != want {
			t.Errorf("parseMaintainer(%q) = %+v, want %+v", value, got, want)
		}
	}

	server := repoServer(t, map[string]string{"x86_64": realIndex(t)})
	m, err := New(server.URL+"/x86_64", core.DefaultClient()).FetchMaintainers(context.Background(), "alpine/busybox")
	if err != nil || len(m) != 1 || m[0].Email != "soeren+alpine@soeren-tempel.net" {
		t.Errorf("FetchMaintainers = %+v, %v", m, err)
	}
}

func TestDecodeChecksum(t *testing.T) {
	sum, ok := decodeChecksum("Q1Je9Cwt4OmEyiSGWGtYfOVCrvgWE=")
	if !ok || sum.Algorithm != "sha1" || sum.Hex != "25ef42c2de0e984ca2486586b587ce542aef8161" {
		t.Errorf("Q1 = %+v, %v", sum, ok)
	}
	sum, ok = decodeChecksum("d41d8cd98f00b204e9800998ecf8427e")
	if !ok || sum.Algorithm != "md5" {
		t.Errorf("md5 = %+v, %v", sum, ok)
	}
	for _, bad := range []string{"Q1notbase64!", "Q1AAAA", "abc", ""} {
		if _, ok := decodeChecksum(bad); ok {
			t.Errorf("decodeChecksum(%q) ok, want false", bad)
		}
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
		if strings.HasSuffix(r.URL.Path, indexV2) {
			_, _ = w.Write(buildIndex(t, "P:curl\nV:8.22.0-r0\n"))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := core.DefaultClient()
	client.HTTPClient = &http.Client{Transport: tokenTransport{base: http.DefaultTransport}}
	if _, err := New(server.URL+"/x86_64", client).FetchVersions(context.Background(), "curl"); err != nil {
		t.Errorf("FetchVersions through authenticated client: %v", err)
	}
}

func TestURLs(t *testing.T) {
	u := New("https://dl-cdn.alpinelinux.org/alpine/v3.23/main/x86_64", nil).URLs()
	tests := [][2]string{
		{u.Download("alpine/curl", "8.22.0-r0"), "https://dl-cdn.alpinelinux.org/alpine/v3.23/main/x86_64/curl-8.22.0-r0.apk"},
		{u.Download("curl", "8.22.0-r0"), "https://dl-cdn.alpinelinux.org/alpine/v3.23/main/x86_64/curl-8.22.0-r0.apk"},
		{u.Download("curl", ""), ""},
		{u.PURL("alpine/curl", "8.22.0-r0"), "pkg:apk/alpine/curl@8.22.0-r0"},
		{u.PURL("curl", ""), "pkg:apk/curl"},
		{u.Registry("curl", ""), ""},
	}
	for _, tt := range tests {
		if tt[0] != tt[1] {
			t.Errorf("got %q, want %q", tt[0], tt[1])
		}
	}

	exact := New("https://dl-cdn.alpinelinux.org/alpine/v3.23/main/x86_64/APKINDEX.tar.gz", nil).URLs()
	if got := exact.Download("curl", "8.22.0-r0"); got != "https://dl-cdn.alpinelinux.org/alpine/v3.23/main/x86_64/curl-8.22.0-r0.apk" {
		t.Errorf("exact index Download = %q", got)
	}
}
