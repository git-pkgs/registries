package deb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"sync"
	"testing"

	"github.com/git-pkgs/registries/internal/core"
)

// snapshotFixtures maps snapshot.debian.org API paths to responses trimmed
// from the real service.
var snapshotFixtures = map[string]string{
	"/mr/binary/hello/":                                    "binary-hello.json",
	"/mr/binary/hello/2.10-3/binfiles":                     "binfiles-hello-2.10-3.json",
	"/mr/binary/openssh-client/":                           "binary-openssh-client.json",
	"/mr/binary/openssh-client/1:9.2p1-2+deb12u3/binfiles": "binfiles-openssh-client.json",
	"/mr/binary/ca-certificates/20230311/binfiles":         "binfiles-ca-certificates.json",
}

// rewriteTransport sends requests for any host to target, so a Registry
// configured with https://snapshot.debian.org talks to the test server.
type rewriteTransport struct{ target *url.URL }

func (t rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme, req.URL.Host = t.target.Scheme, t.target.Host
	return http.DefaultTransport.RoundTrip(req)
}

// snapshotServer serves the fixtures and records the paths requested.
func snapshotServer(t *testing.T) (*core.Client, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var requested []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requested = append(requested, r.URL.Path+"?"+r.URL.RawQuery)
		mu.Unlock()
		name, ok := snapshotFixtures[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		body, err := os.ReadFile("testdata/snapshot/" + name)
		if err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	target, _ := url.Parse(srv.URL)
	client := core.DefaultClient()
	client.HTTPClient = &http.Client{Transport: rewriteTransport{target}}
	return client, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), requested...)
	}
}

func TestSnapshotVersions(t *testing.T) {
	client, requested := snapshotServer(t)
	reg := New("https://snapshot.debian.org?architectures=amd64", client)

	versions, err := reg.FetchVersions(context.Background(), "debian/hello")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, v := range versions {
		got = append(got, v.Number)
		if len(v.Artifacts) != 0 {
			t.Errorf("%s has artifacts %v, want them left to FetchVersion", v.Number, v.Artifacts)
		}
	}
	want := []string{"2.12.3-1", "2.10-5+b1", "2.10-5", "2.10-4", "2.10-3+b1", "2.10-3", "2.10-2", "2.10-1+deb9u1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("versions = %v, want %v", got, want)
	}
	if src := versions[1].Metadata["source_version"]; src != "2.10-5" {
		t.Errorf("source_version of 2.10-5+b1 = %v, want 2.10-5", src)
	}
	if paths := requested(); !reflect.DeepEqual(paths, []string{"/mr/binary/hello/?"}) {
		t.Errorf("requested %v, want only the binary's version list", paths)
	}
	if got := reg.URLs().PURL("debian/hello", "2.10-3"); got != "pkg:deb/debian/hello@2.10-3?repository_url=https:%2F%2Fsnapshot.debian.org%3Farchitectures%3Damd64" {
		t.Errorf("PURL = %s", got)
	}
}

func TestSnapshotVersionsSortNewestFirst(t *testing.T) {
	client, _ := snapshotServer(t)
	reg := New("https://snapshot.debian.org?architectures=amd64", client)
	versions, err := reg.FetchVersions(context.Background(), "openssh-client")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 || versions[0].Number != "1:9.2p1-2+deb12u10" || versions[1].Number != "1:9.2p1-2+deb12u3" {
		t.Errorf("versions = %+v", versions)
	}
	pkg, err := reg.FetchPackage(context.Background(), "openssh-client")
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Name != "openssh-client" || pkg.LatestVersion != "1:9.2p1-2+deb12u10" || pkg.Metadata["source"] != "openssh" {
		t.Errorf("package = %+v", pkg)
	}
}

func TestSnapshotFetchVersion(t *testing.T) {
	client, requested := snapshotServer(t)
	reg := New("https://snapshot.debian.org?architectures=amd64,arm64", client)

	v, err := reg.FetchVersion(context.Background(), "debian/openssh-client", "1:9.2p1-2+deb12u3")
	if err != nil {
		t.Fatal(err)
	}
	want := []core.Artifact{
		{
			URL:       "https://snapshot.debian.org/file/81171c850daaaedc27dabae3717dc20943ef371a",
			Filename:  "openssh-client_9.2p1-2+deb12u3_amd64.deb",
			Size:      990808,
			Integrity: "sha1-81171c850daaaedc27dabae3717dc20943ef371a",
			Metadata:  map[string]any{"arch": "amd64", "archive": "debian-security", "first_seen": "20240701T082523Z"},
		},
		{
			URL:       "https://snapshot.debian.org/file/cbd60f5bdf4f576681cb75f888761abc94cb72ca",
			Filename:  "openssh-client_9.2p1-2+deb12u3_arm64.deb",
			Size:      937432,
			Integrity: "sha1-cbd60f5bdf4f576681cb75f888761abc94cb72ca",
			Metadata:  map[string]any{"arch": "arm64", "archive": "debian-security", "first_seen": "20240701T082523Z"},
		},
	}
	if v.Number != "1:9.2p1-2+deb12u3" || !reflect.DeepEqual(v.Artifacts, want) {
		t.Errorf("version = %+v\nwant artifacts %+v", v, want)
	}
	if paths := requested(); !reflect.DeepEqual(paths, []string{"/mr/binary/openssh-client/1:9.2p1-2+deb12u3/binfiles?fileinfo=1"}) {
		t.Errorf("requested %v", paths)
	}
}

func TestSnapshotFetchVersionKeepsConfiguredArchitectures(t *testing.T) {
	client, _ := snapshotServer(t)
	reg := New("https://snapshot.debian.org?architectures=arm64", client)

	v, err := reg.FetchVersion(context.Background(), "hello", "2.10-3")
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Artifacts) != 1 || v.Artifacts[0].Filename != "hello_2.10-3_arm64.deb" {
		t.Errorf("artifacts = %+v, want only arm64", v.Artifacts)
	}

	// Architecture: all packages are kept whatever is configured.
	v, err = reg.FetchVersion(context.Background(), "ca-certificates", "20230311")
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Artifacts) != 1 || v.Artifacts[0].Metadata["arch"] != "all" || v.Artifacts[0].Filename != "ca-certificates_20230311_all.deb" {
		t.Errorf("artifacts = %+v, want the all package", v.Artifacts)
	}
}

func TestSnapshotNotFound(t *testing.T) {
	client, requested := snapshotServer(t)
	reg := New("https://snapshot.debian.org?architectures=amd64", client)

	var notFound *core.NotFoundError
	if _, err := reg.FetchVersions(context.Background(), "no-such-package"); !errors.As(err, &notFound) {
		t.Errorf("FetchVersions: err = %v, want NotFoundError", err)
	}
	if _, err := reg.FetchVersion(context.Background(), "hello", "9.9-9"); !errors.As(err, &notFound) || notFound.Version != "9.9-9" {
		t.Errorf("FetchVersion: err = %v, want NotFoundError for the version", err)
	}
	for _, path := range requested() {
		if path[:4] != "/mr/" {
			t.Errorf("requested %s, want only the snapshot API", path)
		}
	}
}

func TestSnapshotHasNoDependenciesOrMaintainers(t *testing.T) {
	client, _ := snapshotServer(t)
	reg := New("https://snapshot.debian.org?architectures=amd64", client)
	if _, err := reg.FetchDependencies(context.Background(), "hello", "2.10-3"); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("FetchDependencies: err = %v, want ErrUnsupported", err)
	}
	if _, err := reg.FetchMaintainers(context.Background(), "hello"); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("FetchMaintainers: err = %v, want ErrUnsupported", err)
	}
}

func TestSnapshotConfig(t *testing.T) {
	for _, tc := range []struct {
		url      string
		snapshot bool
		wantErr  bool
	}{
		{"https://snapshot.debian.org?architectures=amd64", true, false},
		{"https://snapshot.debian.org/?architectures=amd64", true, false},
		{"https://snapshot.debian.org", true, true},
		{"https://snapshot.debian.org?suites=bookworm&architectures=amd64", true, true},
		{"https://snapshot.debian.org?components=main&architectures=amd64", true, true},
		{"https://snapshot.debian.org?architectures=source", true, true},
		{"https://snapshot.debian.org?architectures=..", true, true},
		// Only https selects the API; otherwise it is an archive missing
		// its suites.
		{"http://snapshot.debian.org?architectures=amd64", false, true},
		// A dated archive under snapshot.debian.org is an ordinary archive.
		{"https://snapshot.debian.org/archive/debian/20240101T000000Z?suites=bookworm&architectures=amd64", false, false},
		{"https://deb.debian.org/debian?suites=bookworm&architectures=amd64", false, false},
	} {
		reg := New(tc.url, nil)
		if reg.snapshot != tc.snapshot || (reg.configErr != nil) != tc.wantErr {
			t.Errorf("%s: snapshot = %v, err = %v", tc.url, reg.snapshot, reg.configErr)
		}
		if tc.wantErr && !errors.Is(reg.configErr, ErrInvalidConfig) {
			t.Errorf("%s: err = %v, want ErrInvalidConfig", tc.url, reg.configErr)
		}
	}
}

func TestSnapshotRefusesBadFiles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"binary_version":"1","fileinfo":{
			"0123456789abcdef0123456789abcdef01234567":[{"archive_name":"debian","name":"../evil.deb","size":1}],
			"not-a-sha1":[{"archive_name":"debian","name":"x_1_amd64.deb","size":1}],
			"89abcdef0123456789abcdef0123456789abcdef":[],
			"1111111111111111111111111111111111111111":[{"archive_name":"debian","name":"","size":1}],
			"2222222222222222222222222222222222222222":[{"archive_name":"debian","name":"x\u0000.deb","size":1}]},
			"result":[
			{"architecture":"amd64","hash":"0123456789abcdef0123456789abcdef01234567"},
			{"architecture":"amd64","hash":"not-a-sha1"},
			{"architecture":"amd64","hash":"89abcdef0123456789abcdef0123456789abcdef"},
			{"architecture":"amd64","hash":"fedcba9876543210fedcba9876543210fedcba98"},
			{"architecture":"amd64","hash":"1111111111111111111111111111111111111111"},
			{"architecture":"amd64","hash":"2222222222222222222222222222222222222222"}]}`))
	}))
	t.Cleanup(srv.Close)
	target, _ := url.Parse(srv.URL)
	client := core.DefaultClient()
	client.HTTPClient = &http.Client{Transport: rewriteTransport{target}}
	reg := New("https://snapshot.debian.org?architectures=amd64", client)
	v, err := reg.FetchVersion(context.Background(), "x", "1")
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Artifacts) != 0 {
		t.Errorf("artifacts = %+v, want none", v.Artifacts)
	}
}

func TestFetchArtifacts(t *testing.T) {
	client, _ := snapshotServer(t)
	reg := New("https://snapshot.debian.org?architectures=amd64", client)
	artifacts, err := reg.FetchArtifacts(context.Background(), "hello", "2.10-3")
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 1 || artifacts[0].Filename != "hello_2.10-3_amd64.deb" {
		t.Errorf("artifacts = %+v", artifacts)
	}
}

func TestFetchVersionFromIndexes(t *testing.T) {
	_, base := standard(t)
	reg := newRegistry(base, "suites=bookworm&architectures=amd64")
	v, err := reg.FetchVersion(context.Background(), "debian/curl", "7.88.1-10+deb12u5")
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Artifacts) != 1 || v.Artifacts[0].Filename != "curl_7.88.1-10+deb12u5_amd64.deb" {
		t.Errorf("version = %+v", v)
	}
	var notFound *core.NotFoundError
	if _, err := reg.FetchVersion(context.Background(), "debian/curl", "0.1"); !errors.As(err, &notFound) || notFound.Version != "0.1" {
		t.Errorf("err = %v, want NotFoundError for the version", err)
	}
	if _, err := reg.FetchVersion(context.Background(), "nope", "0.1"); !errors.As(err, &notFound) {
		t.Errorf("err = %v, want NotFoundError", err)
	}
}

func TestSnapshotPURLRoundTrip(t *testing.T) {
	client, _ := snapshotServer(t)
	p := New("https://snapshot.debian.org?architectures=amd64,arm64", nil).URLs().PURL("debian/hello", "2.10-3")
	reg, name, version, err := core.NewFromPURL(p, client)
	if err != nil {
		t.Fatal(err)
	}
	v, err := reg.(*Registry).FetchVersion(context.Background(), name, version)
	if err != nil || len(v.Artifacts) != 2 {
		t.Errorf("FetchVersion = %+v, %v", v, err)
	}
}

// fakeSnapshot serves body for every request and records the raw paths.
func fakeSnapshot(t *testing.T, body string) (*core.Client, *[]string) {
	t.Helper()
	var requested []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = append(requested, r.URL.EscapedPath())
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	target, _ := url.Parse(srv.URL)
	client := core.DefaultClient()
	client.HTTPClient = &http.Client{Transport: rewriteTransport{target}}
	return client, &requested
}

func TestSnapshotVersionsSkipsRepeatsAndBlanks(t *testing.T) {
	client, _ := fakeSnapshot(t, `{"result":[
		{"binary_version":"2","source":"x","version":"2"},
		{"binary_version":"","source":"x","version":"1"},
		{"binary_version":"2","source":"x","version":"2"},
		{"binary_version":"1","source":"x","version":"1"}]}`)
	reg := New("https://snapshot.debian.org?architectures=amd64", client)
	versions, err := reg.FetchVersions(context.Background(), "x")
	if err != nil {
		t.Fatal(err)
	}
	if got := numbers(versions); !reflect.DeepEqual(got, []string{"2", "1"}) {
		t.Errorf("versions = %v, want [2 1]", got)
	}

	client, _ = fakeSnapshot(t, `{"result":[]}`)
	reg = New("https://snapshot.debian.org?architectures=amd64", client)
	var notFound *core.NotFoundError
	if _, err := reg.FetchVersions(context.Background(), "x"); !errors.As(err, &notFound) {
		t.Errorf("no versions: err = %v, want NotFoundError", err)
	}
}

func TestSnapshotEscapesPathSegments(t *testing.T) {
	client, requested := fakeSnapshot(t, `{"result":[{"binary_version":"1"}]}`)
	reg := New("https://snapshot.debian.org?architectures=amd64", client)
	_, _ = reg.FetchVersions(context.Background(), "a?b")
	_, _ = reg.FetchVersion(context.Background(), "a?b", "1#2")
	want := []string{"/mr/binary/a%3Fb/", "/mr/binary/a%3Fb/1%232/binfiles"}
	if !reflect.DeepEqual(*requested, want) {
		t.Errorf("requested %v, want %v", *requested, want)
	}
}

func TestSnapshotMisconfiguredMakesNoRequest(t *testing.T) {
	client, requested := fakeSnapshot(t, `{"result":[]}`)
	reg := New("https://snapshot.debian.org?suites=bookworm&architectures=amd64", client)
	if _, err := reg.FetchVersions(context.Background(), "x"); !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("FetchVersions: err = %v, want ErrInvalidConfig", err)
	}
	if _, err := reg.FetchVersion(context.Background(), "x", "1"); !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("FetchVersion: err = %v, want ErrInvalidConfig", err)
	}
	if len(*requested) != 0 {
		t.Errorf("requested %v, want nothing", *requested)
	}
}
