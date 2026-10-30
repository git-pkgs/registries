package registries_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/git-pkgs/registries"
)

const swiftChecksum = "a2ac54cf25fbc1ad0028f03f0aa4b96833b83bb05a14e510892bb27dea4dc812"

func swiftRelease(version string) map[string]any {
	return map[string]any{
		"id": "Mona.Linked-List", "version": version,
		"publishedAt": "2026-01-03T04:05:06Z",
		"resources": []any{map[string]any{
			"name": "source-archive", "type": "application/zip", "checksum": swiftChecksum,
			"signing": map[string]any{"signatureBase64Encoded": "c2lnbmF0dXJl", "signatureFormat": "cms-1.0.0"},
		}},
		"metadata": map[string]any{
			"description":             "A linked list.",
			"repositoryURLs":          []string{"https://github.com/Mona/Linked-List"},
			"licenseURL":              "https://example.com/LICENSE",
			"originalPublicationTime": "2025-12-01T02:03:04Z",
			"author":                  map[string]any{"name": "Mona", "email": "mona@example.com"},
		},
	}
}

func newSwiftRegistry(t *testing.T, handler http.HandlerFunc) registries.Registry { //nolint:ireturn
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	reg, err := registries.New("swift", server.URL+"/registry", registries.NewClient(registries.WithMaxRetries(0)))
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func TestSwiftRegistry(t *testing.T) {
	reg := newSwiftRegistry(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); got != "application/vnd.swift.registry.v1+json" {
			t.Errorf("Accept = %q", got)
		}
		w.Header().Set("Content-Version", "1")
		switch r.URL.RequestURI() {
		case "/registry/mona/linked-list":
			w.Header().Add("Link", `<?page=2>; rel="next"; title="Next, please"`)
			w.Header().Add("Link", `<https://github.com/Mona/Linked-List>; rel="canonical"`)
			_, _ = fmt.Fprint(w, `{"releases":{"1.0.0":{}, "3.0.0":{"problem":{"status":410,"title":"Gone"}}}}`)
		case "/registry/mona/linked-list?page=2":
			_, _ = fmt.Fprint(w, `{"releases":{"2.0.0":{"url":"/metadata/linked-list/2.0.0"}}}`)
		case "/metadata/linked-list/2.0.0":
			_ = json.NewEncoder(w).Encode(swiftRelease("2.0.0"))
		case "/registry/mona/linked-list/1.0.0":
			_ = json.NewEncoder(w).Encode(swiftRelease("1.0.0"))
		default:
			t.Errorf("unexpected request %s", r.URL)
			http.NotFound(w, r)
		}
	})
	ctx := context.Background()
	pkg, err := reg.FetchPackage(ctx, "Mona.Linked-List")
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Name != "mona.linked-list" || pkg.Namespace != "mona" || pkg.LatestVersion != "2.0.0" ||
		pkg.Repository != "https://github.com/Mona/Linked-List" || pkg.Description != "A linked list." {
		t.Errorf("package = %#v", pkg)
	}
	if pkg.Licenses != "" || len(pkg.Maintainers) != 0 || pkg.Metadata["licenseURL"] != "https://example.com/LICENSE" {
		t.Errorf("license URL or author promoted to unsupported metadata: %#v", pkg)
	}
	versions, err := reg.FetchVersions(ctx, "Mona.Linked-List")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 3 || versions[0].Number != "3.0.0" || versions[0].Status != registries.StatusRetracted {
		t.Fatalf("versions = %#v", versions)
	}
	version := versions[1]
	if version.Number != "2.0.0" || version.Integrity != "sha256-"+swiftChecksum ||
		!version.PublishedAt.Equal(time.Date(2025, 12, 1, 2, 3, 4, 0, time.UTC)) {
		t.Errorf("release = %#v", version)
	}
	checkSwiftArtifact(t, version, reg.URLs().Download("mona.linked-list", "2.0.0"))
	latest, err := registries.FetchLatestVersion(ctx, reg, "mona.linked-list")
	if err != nil || latest == nil || latest.Number != "2.0.0" {
		t.Fatalf("latest = %#v, %v", latest, err)
	}
}

func checkSwiftArtifact(t *testing.T, version registries.Version, downloadURL string) {
	t.Helper()
	if len(version.Artifacts) != 1 {
		t.Fatalf("artifacts = %#v", version.Artifacts)
	}
	artifact := version.Artifacts[0]
	if artifact.URL != downloadURL ||
		artifact.Filename != "2.0.0.zip" || artifact.Integrity != version.Integrity ||
		artifact.MediaType != "application/zip" || artifact.Metadata["signing"] == nil {
		t.Errorf("artifact = %#v", artifact)
	}
}

func TestSwiftRegistryURLsAndIdentity(t *testing.T) {
	reg := newSwiftRegistry(t, func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s", r.URL)
	})
	urls := reg.URLs()
	if got := urls.Registry("Mona.Linked_List", "1.0.0"); !strings.HasSuffix(got, "/registry/mona/linked_list/1.0.0") {
		t.Errorf("Registry = %q", got)
	}
	if urls.Download("mona.linked-list", "") != "" ||
		urls.PURL("mona.linked-list", "1.0.0") != "" ||
		urls.Documentation("mona.linked-list", "1.0.0") != "" {
		t.Error("unexpected inferred URL")
	}
	for _, name := range []string{
		"github.com/Mona/Linked-List", "mona", "mona.foo.bar", "../x", "mona.x?y",
		"mo_na.foo", "-mona.foo", "mo--na.foo", "mona.foo-_bar", "mona.foo-",
		strings.Repeat("a", 40) + ".foo", "mona." + strings.Repeat("a", 101),
	} {
		if _, err := reg.FetchPackage(context.Background(), name); err == nil {
			t.Errorf("accepted invalid identity %q", name)
		}
	}
	for _, version := range []string{"../1", "1.0.0/../../x", "1.0.0?x", "main"} {
		if got := urls.Download("mona.linked-list", version); got != "" {
			t.Errorf("Download accepted version %q: %q", version, got)
		}
	}
	if _, err := registries.New("swift", "", nil); err == nil {
		t.Fatal("missing registry URL accepted")
	}
	if _, err := registries.FetchPackageFromPURL(context.Background(),
		"pkg:swift/github.com/Mona/Linked-List?repository_url=https%3A%2F%2Fexample.com", nil); err == nil {
		t.Fatal("source coordinate was treated as a registry identity")
	}
}

func TestSwiftUnsupported(t *testing.T) {
	reg := newSwiftRegistry(t, func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s", r.URL)
	})
	if deps, err := reg.FetchDependencies(context.Background(), "mona.linked-list", "1.0.0"); !errors.Is(err, errors.ErrUnsupported) || deps != nil {
		t.Errorf("dependencies = %#v, %v", deps, err)
	}
	if maintainers, err := reg.FetchMaintainers(context.Background(), "mona.linked-list"); !errors.Is(err, errors.ErrUnsupported) || maintainers != nil {
		t.Errorf("maintainers = %#v, %v", maintainers, err)
	}
}

func TestSwiftRegistryErrors(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			reg := newSwiftRegistry(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				_, _ = fmt.Fprint(w, `{"detail":"unavailable"}`)
			})
			_, err := reg.FetchPackage(context.Background(), "mona.linked-list")
			if err == nil || errors.Is(err, registries.ErrNotFound) != (status == http.StatusNotFound) {
				t.Errorf("error = %v", err)
			}
		})
	}
	for _, body := range []string{`{`, `{}`, `null`, `{"releases":null}`, `{"releases":{"../bad":{}}}`, `{"releases":{"":{}}}`} {
		t.Run(body, func(t *testing.T) {
			reg := newSwiftRegistry(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprint(w, body)
			})
			if result, err := reg.FetchVersions(context.Background(), "mona.linked-list"); err == nil || result != nil {
				t.Errorf("versions = %#v, %v", result, err)
			}
		})
	}
}

func TestSwiftPaginationErrors(t *testing.T) {
	for _, link := range []string{
		`</registry/mona/linked-list>; rel="next"`,
		`<https://other.example.com/mona/linked-list>; rel="next"`,
		`<file:///tmp/data>; rel="next"`,
		`<?page=2>; rel="next", <?page=3>; rel="next"`,
	} {
		t.Run(link, func(t *testing.T) {
			reg := newSwiftRegistry(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Link", link)
				_, _ = fmt.Fprint(w, `{"releases":{}}`)
			})
			if _, err := reg.FetchVersions(context.Background(), "mona.linked-list"); err == nil {
				t.Error("invalid pagination accepted")
			}
		})
	}
}

func TestSwiftMalformedRelease(t *testing.T) {
	for _, field := range []string{"id", "version", "checksum", "publishedAt", "duplicate", "http", "protocol"} {
		t.Run(field, func(t *testing.T) {
			reg := newSwiftRegistry(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/registry/mona/linked-list" {
					_, _ = fmt.Fprint(w, `{"releases":{"1.0.0":{}}}`)
					return
				}
				info := swiftRelease("1.0.0")
				switch field {
				case "id", "version":
					info[field] = "wrong"
				case "checksum":
					info["resources"].([]any)[0].(map[string]any)["checksum"] = "garbage"
				case "publishedAt":
					info["metadata"].(map[string]any)["originalPublicationTime"] = "yesterday"
				case "duplicate":
					info["resources"] = append(info["resources"].([]any), info["resources"].([]any)[0])
				case "http":
					http.NotFound(w, r)
					return
				case "protocol":
					w.Header().Set("Content-Version", "2")
				}
				_ = json.NewEncoder(w).Encode(info)
			})
			if result, err := reg.FetchVersions(context.Background(), "mona.linked-list"); err == nil || result != nil {
				t.Errorf("versions = %#v, %v", result, err)
			}
		})
	}
}

func TestSwiftEmptyRegistry(t *testing.T) {
	reg := newSwiftRegistry(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"releases":{}}`)
	})
	pkg, err := reg.FetchPackage(context.Background(), "mona.linked-list")
	if err != nil || pkg.LatestVersion != "" || pkg.Repository != "" {
		t.Errorf("package = %#v, %v", pkg, err)
	}
	versions, err := reg.FetchVersions(context.Background(), "mona.linked-list")
	if err != nil || len(versions) != 0 {
		t.Errorf("versions = %#v, %v", versions, err)
	}
}

func TestSwiftOptionalMetadata(t *testing.T) {
	for _, metadata := range []bool{false, true} {
		t.Run(fmt.Sprint(metadata), func(t *testing.T) {
			reg := newSwiftRegistry(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/registry/mona/linked-list" {
					_, _ = fmt.Fprint(w, `{"releases":{"1.0.0":{}}}`)
					return
				}
				info := swiftRelease("1.0.0")
				delete(info["metadata"].(map[string]any), "originalPublicationTime")
				if !metadata {
					info["metadata"] = nil
					delete(info, "publishedAt")
				}
				_ = json.NewEncoder(w).Encode(info)
			})
			pkg, err := reg.FetchPackage(context.Background(), "mona.linked-list")
			if err != nil {
				t.Fatal(err)
			}
			wantRepository := ""
			if metadata {
				wantRepository = "https://github.com/Mona/Linked-List"
			}
			if pkg.Repository != wantRepository {
				t.Errorf("repository = %q, want %q", pkg.Repository, wantRepository)
			}
			versions, err := reg.FetchVersions(context.Background(), "mona.linked-list")
			if err != nil || len(versions) != 1 {
				t.Fatalf("versions = %#v, %v", versions, err)
			}
			published := versions[0].PublishedAt
			if metadata && !published.Equal(time.Date(2026, 1, 3, 4, 5, 6, 0, time.UTC)) ||
				!metadata && !published.IsZero() {
				t.Errorf("publication time = %v", published)
			}
		})
	}
}

func TestSwiftPaginationFailureReturnsNoPartialResults(t *testing.T) {
	reg := newSwiftRegistry(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			http.Error(w, "failed", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Link", `<?page=2>; rel="next"`)
		_, _ = fmt.Fprint(w, `{"releases":{"1.0.0":{}}}`)
	})
	if versions, err := reg.FetchVersions(context.Background(), "mona.linked-list"); err == nil || versions != nil {
		t.Errorf("versions = %#v, %v", versions, err)
	}
}

func TestSwiftLatestVersion(t *testing.T) {
	tests := []struct {
		releases, link, want string
	}{
		{`{"2.0.0":{},"3.0.0-beta.1":{}}`, "", "2.0.0"},
		{`{"1.0.0-alpha":{},"1.0.0-beta":{}}`, "", "1.0.0-beta"},
		{`{"2.0.0":{},"3.0.0-beta.1":{}}`, `</registry/Mona/Linked-List/3.0.0-beta.1>; rel="latest-version"`, "3.0.0-beta.1"},
		{`{"1.0.0":{},"2.0.0":{"problem":{"status":410}}}`, `</registry/mona/linked-list/2.0.0>; rel="latest-version"`, "1.0.0"},
	}
	for _, tt := range tests {
		t.Run(tt.releases+tt.link, func(t *testing.T) {
			reg := newSwiftRegistry(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/registry/mona/linked-list" {
					if tt.link != "" {
						w.Header().Set("Link", tt.link)
					}
					_, _ = fmt.Fprintf(w, `{"releases":%s}`, tt.releases)
					return
				}
				_ = json.NewEncoder(w).Encode(swiftRelease(r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]))
			})
			pkg, err := reg.FetchPackage(context.Background(), "mona.linked-list")
			if err != nil || pkg.LatestVersion != tt.want {
				t.Errorf("latest = %q, want %q, err = %v", pkg.LatestVersion, tt.want, err)
			}
		})
	}
}

func TestSwiftPaginationRelCaseInsensitive(t *testing.T) {
	reg := newSwiftRegistry(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.RequestURI() {
		case "/registry/mona/linked-list":
			w.Header().Set("Link", `<?page=2>; rel="Next"`)
			_, _ = fmt.Fprint(w, `{"releases":{"1.0.0":{"problem":{"status":410}}}}`)
		case "/registry/mona/linked-list?page=2":
			_, _ = fmt.Fprint(w, `{"releases":{"2.0.0":{"problem":{"status":410}}}}`)
		default:
			t.Errorf("unexpected request %s", r.URL)
		}
	})
	versions, err := reg.FetchVersions(context.Background(), "mona.linked-list")
	if err != nil || len(versions) != 2 {
		t.Errorf("versions = %#v, %v", versions, err)
	}
}

func TestSwiftReleaseURLOnOtherOrigin(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("request sent to other origin: %s", r.URL)
		_ = json.NewEncoder(w).Encode(swiftRelease("1.0.0"))
	}))
	t.Cleanup(other.Close)
	reg := newSwiftRegistry(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"releases":{"1.0.0":{"url":%q}}}`, other.URL+"/1.0.0")
	})
	if _, err := reg.FetchPackage(context.Background(), "mona.linked-list"); err == nil {
		t.Error("cross-origin release URL accepted")
	}
}

func TestSwiftPaginationStopsAtPageCap(t *testing.T) {
	var requests int
	reg := newSwiftRegistry(t, func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.Header().Set("Link", fmt.Sprintf(`<?page=%d>; rel="next"`, requests+1))
		_, _ = fmt.Fprint(w, `{"releases":{}}`)
	})
	if _, err := reg.FetchVersions(context.Background(), "mona.linked-list"); !errors.Is(err, registries.ErrTooManyPages) {
		t.Errorf("err = %v, want ErrTooManyPages", err)
	}
	if requests != 1000 {
		t.Errorf("made %d requests, want 1000", requests)
	}
}
