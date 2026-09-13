package registries_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/git-pkgs/registries"
)

func TestNPMVersionLookupMatchesList(t *testing.T) {
	const document = `{
		"_id":"@scope/demo", "dist-tags":{"latest":"2.0.0"},
		"time":{"1.0.0":"2024-01-15T12:00:00.123Z", "2.0.0":"invalid"},
		"versions":{
			"1.0.0":{"version":"different", "license":{"type":"MIT"}, "deprecated":"use v2",
				"engines":{"node":">=18"}, "_npmUser":{"name":"publisher"},
				"contentPolicy":{"class":"dual-use"},
				"dist":{"integrity":"sha512-fixture", "shasum":"ignored", "tarball":"https://example.invalid/demo.tgz",
					"attestations":{"url":"https://example.invalid/attestations", "provenance":{"predicateType":"fixture"}},
					"signatures":[{"sig":"signature", "keyid":"key"}]}},
			"2.0.0":{"license":"Apache-2.0", "deprecated":false, "dist":{"shasum":"fallback"}},
			"3.0.0":{"deprecated":true},
			"4.0.0":null
		}
	}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/@scope/demo" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(document))
	}))
	defer server.Close()
	client := registries.DefaultClient()
	reg, err := registries.New("npm", server.URL, client)
	if err != nil {
		t.Fatal(err)
	}
	versions, err := reg.FetchVersions(context.Background(), "@scope/demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 4 {
		t.Fatalf("got %d versions", len(versions))
	}
	for _, want := range versions {
		t.Run(want.Number, func(t *testing.T) {
			purl := "pkg:npm/@scope/demo@" + want.Number + "?repository_url=" + url.QueryEscape(server.URL)
			got, err := registries.FetchVersionFromPURL(context.Background(), purl, client)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, &want) {
				t.Fatalf("version mismatch\ngot: %+v\nwant: %+v", got, want)
			}
		})
	}
}

func TestNPMSelectedLookupErrors(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		body     string
		version  string
		notFound bool
	}{
		{"missing package", http.StatusNotFound, `{}`, "", true},
		{"missing version", http.StatusOK, `{"versions":{}}`, "1.0.0", true},
		{"invalid JSON", http.StatusOK, `{"versions":`, "", false},
		{"invalid release", http.StatusOK, `{"dist-tags":{"latest":"1.0.0"},"versions":{"1.0.0":{"dist":false}}}`, "", false},
		{"HTTP error", http.StatusForbidden, `{}`, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			purl := "pkg:npm/demo@1.0.0?repository_url=" + url.QueryEscape(server.URL)
			client := registries.DefaultClient()
			_, err := registries.FetchVersionFromPURL(context.Background(), purl, client)
			if err == nil {
				t.Fatal("expected version lookup error")
			}
			if tc.notFound {
				var missing *registries.NotFoundError
				if !errors.As(err, &missing) || missing.Name != "demo" || missing.Version != tc.version {
					t.Fatalf("unexpected not-found error: %v", err)
				}
			}
			if tc.name != "missing version" {
				if _, err := registries.FetchPackageFromPURL(context.Background(), purl, client); err == nil {
					t.Fatal("expected package lookup error")
				}
			}
		})
	}
}

func TestNPMSelectedLookupCancellation(t *testing.T) {
	started := make(chan struct{}, 2)
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-r.Context().Done()
	}))
	defer server.Close()
	purl := "pkg:npm/demo@1.0.0?repository_url=" + url.QueryEscape(server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, err := fetchNPMPair(ctx, purl, registries.DefaultClient())
		done <- err
	}()
	<-started
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func TestNPMPackageSelection(t *testing.T) {
	for _, tc := range []struct {
		name    string
		body    string
		license string
	}{
		{"no tag", `"versions":{"1.0.0":{"license":"MIT"}}`, "MIT"},
		{"missing tagged release", `"dist-tags":{"latest":"2.0.0"},"versions":{"1.0.0":{"license":"MIT"}}`, ""},
		{"no versions", `"versions":{}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"_id":"demo","description":"fallback",` + tc.body + `}`))
			}))
			defer server.Close()
			purl := "pkg:npm/demo?repository_url=" + url.QueryEscape(server.URL)
			pkg, err := registries.FetchPackageFromPURL(context.Background(), purl, nil)
			if err != nil {
				t.Fatal(err)
			}
			if pkg.Name != "demo" || pkg.Description != "fallback" || pkg.Licenses != tc.license {
				t.Fatalf("unexpected package: %+v", pkg)
			}
		})
	}
}

func TestVersionLookupFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"versions":[{"num":"1.0.0","license":"MIT"}]}`))
	}))
	defer server.Close()
	purl := "pkg:cargo/demo@1.0.0?repository_url=" + url.QueryEscape(server.URL)
	version, err := registries.FetchVersionFromPURL(context.Background(), purl, nil)
	if err != nil || version == nil || version.Number != "1.0.0" || !strings.Contains(version.Licenses, "MIT") {
		t.Fatalf("fallback lookup: version=%+v err=%v", version, err)
	}
}

func TestNPMSelectedLookupSkipsOtherReleaseFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"_id":"demo","dist-tags":{"latest":"1.0.0"},
			"maintainers":[{"name":"publisher","email":"publisher@example.invalid"}],
			"versions":{
				"1.0.0":{"license":"MIT","dependencies":{"alpha":"^1"},"devDependencies":{"beta":"^2"}},
				"2.0.0":{"dist":false}
		}}`))
	}))
	defer server.Close()
	client := registries.DefaultClient()
	purl := "pkg:npm/demo@1.0.0?repository_url=" + url.QueryEscape(server.URL)
	pkg, version, err := fetchNPMPair(context.Background(), purl, client)
	if err != nil {
		t.Fatal(err)
	}
	if pkg == nil || version == nil || pkg.Licenses != "MIT" || version.Licenses != "MIT" {
		t.Fatalf("unexpected results: package=%+v version=%+v", pkg, version)
	}
	reg, err := registries.New("npm", server.URL, client)
	if err != nil {
		t.Fatal(err)
	}
	deps, err := reg.FetchDependencies(context.Background(), "demo", "1.0.0")
	if err != nil || len(deps) != 2 {
		t.Fatalf("dependencies=%+v err=%v", deps, err)
	}
	if _, err := reg.FetchDependencies(context.Background(), "demo", "2.0.0"); err == nil {
		t.Fatal("expected error decoding invalid selected release")
	}
	maintainers, err := reg.FetchMaintainers(context.Background(), "demo")
	if err != nil || len(maintainers) != 1 || maintainers[0].Login != "publisher" {
		t.Fatalf("maintainers=%+v err=%v", maintainers, err)
	}
}
