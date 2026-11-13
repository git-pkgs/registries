package vscodeextension

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/git-pkgs/registries/internal/core"
)

const yamlExtension = `{
	"namespace": "redhat",
	"name": "vscode-yaml",
	"version": "1.15.0",
	"timestamp": "2024-06-03T10:00:00.5Z",
	"targetPlatform": "universal",
	"displayName": "YAML",
	"namespaceDisplayName": "Red Hat",
	"description": "YAML Language Support by Red Hat",
	"license": "MIT",
	"homepage": "https://github.com/redhat-developer/vscode-yaml#readme",
	"repository": "https://github.com/redhat-developer/vscode-yaml.git",
	"bugs": "https://github.com/redhat-developer/vscode-yaml/issues",
	"downloadCount": 9255952,
	"verified": true,
	"deprecated": false,
	"preRelease": false,
	"tags": ["__ext_yaml", "__web_extension", "kubernetes", "yaml"],
	"publishedBy": {
		"loginName": "rhdevelopers-ci",
		"fullName": "Red Hat Developers CI",
		"homepage": "https://github.com/rhdevelopers-ci",
		"provider": "github"
	},
	"dependencies": [],
	"bundledExtensions": []
}`

// csharpEntry is one entry of a query response: one version built for one
// target platform.
func csharpEntry(version, platform, timestamp string, preRelease bool) string {
	file := "muhammad-sammy.csharp-" + version
	dir := version
	if platform != "universal" {
		file += "@" + platform
		dir = platform + "/" + version
	}
	base := "https://open-vsx.org/api/muhammad-sammy/csharp/" + dir + "/file/"
	return fmt.Sprintf(`{
		"namespace": "muhammad-sammy",
		"name": "csharp",
		"version": %q,
		"targetPlatform": %q,
		"timestamp": %q,
		"preRelease": %t,
		"license": "MIT",
		"files": {
			"download": %q,
			"signature": %q,
			"sha256": %q,
			"manifest": %q
		}
	}`, version, platform, timestamp, preRelease, base+file+".vsix", base+file+".sigzip", base+file+".sha256", base+"package.json")
}

func queryPage(total, offset int, entries ...string) string {
	return fmt.Sprintf(`{"offset": %d, "totalSize": %d, "extensions": [%s]}`, offset, total, strings.Join(entries, ","))
}

func newTestRegistry(t *testing.T, handler http.HandlerFunc) (*Registry, *atomic.Int32) {
	t.Helper()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	return New(server.URL, core.DefaultClient()), &requests
}

func TestFetchPackage(t *testing.T) {
	for _, name := range []string{"redhat/vscode-yaml", "redhat.vscode-yaml"} {
		t.Run(name, func(t *testing.T) {
			reg, _ := newTestRegistry(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/redhat/vscode-yaml" {
					t.Errorf("unexpected path %s", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				_, _ = w.Write([]byte(yamlExtension))
			})

			pkg, err := reg.FetchPackage(context.Background(), name)
			if err != nil {
				t.Fatalf("FetchPackage: %v", err)
			}
			want := &core.Package{
				Name:          "redhat/vscode-yaml",
				Description:   "YAML Language Support by Red Hat",
				Homepage:      "https://github.com/redhat-developer/vscode-yaml#readme",
				Repository:    "https://github.com/redhat-developer/vscode-yaml",
				Licenses:      "MIT",
				Keywords:      []string{"kubernetes", "yaml"},
				Namespace:     "redhat",
				LatestVersion: "1.15.0",
				Maintainers: []core.Maintainer{{
					Login: "rhdevelopers-ci",
					Name:  "Red Hat Developers CI",
					URL:   "https://github.com/rhdevelopers-ci",
				}},
				Metadata: map[string]any{
					"display_name":           "YAML",
					"namespace_display_name": "Red Hat",
					"downloads":              int64(9255952),
					"verified":               true,
					"deprecated":             false,
					"bugs":                   "https://github.com/redhat-developer/vscode-yaml/issues",
				},
			}
			if !reflect.DeepEqual(pkg, want) {
				t.Errorf("FetchPackage =\n%#v\nwant\n%#v", pkg, want)
			}
		})
	}
}

func TestFetchPackageNotFound(t *testing.T) {
	reg, _ := newTestRegistry(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error": "Extension not found: nope.missing"}`))
	})
	_, err := reg.FetchPackage(context.Background(), "nope/missing")
	var notFound *core.NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("FetchPackage error = %v, want NotFoundError", err)
	}
}

// A registry that answers with another extension's metadata, for example
// after a redirect, must not have it reported as the one asked for.
func TestFetchPackageRejectsAnotherExtension(t *testing.T) {
	reg, _ := newTestRegistry(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(yamlExtension))
	})
	if _, err := reg.FetchPackage(context.Background(), "redhat/java"); err == nil {
		t.Fatal("FetchPackage accepted metadata for redhat/vscode-yaml as redhat/java")
	}
}

func TestFetchPackageMatchesNamesWithoutCase(t *testing.T) {
	reg, _ := newTestRegistry(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(yamlExtension))
	})
	pkg, err := reg.FetchPackage(context.Background(), "RedHat/VSCode-YAML")
	if err != nil {
		t.Fatalf("FetchPackage: %v", err)
	}
	if pkg.Name != "redhat/vscode-yaml" {
		t.Errorf("Name = %q, want the registry's spelling redhat/vscode-yaml", pkg.Name)
	}
}

func TestFetchVersions(t *testing.T) {
	entries := []string{
		csharpEntry("2.160.9", "linux-x64", "2026-10-09T16:23:47.834682Z", false),
		csharpEntry("2.160.9", "alpine-x64", "2026-10-09T16:23:47.049825Z", false),
		csharpEntry("2.170.0-rc1", "universal", "2026-11-01T08:00:00Z", true),
		csharpEntry("2.110.4", "universal", "2025-01-02T03:04:05Z", false),
	}
	// The registry answers at most two entries a page, whatever size asks for.
	const perPage = 2
	reg, requests := newTestRegistry(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/api/v2/-/query" || q.Get("namespaceName") != "muhammad-sammy" ||
			q.Get("extensionName") != "csharp" || q.Get("includeAllVersions") != "true" {
			t.Errorf("unexpected request %s", r.URL)
			http.NotFound(w, r)
			return
		}
		offset, _ := strconv.Atoi(q.Get("offset"))
		end := min(offset+perPage, len(entries))
		_, _ = w.Write([]byte(queryPage(len(entries), offset, entries[offset:end]...)))
	})

	versions, err := reg.FetchVersions(context.Background(), "muhammad-sammy/csharp")
	if err != nil {
		t.Fatalf("FetchVersions: %v", err)
	}
	if got := requests.Load(); got != 2 {
		t.Errorf("made %d requests, want 2 pages", got)
	}

	artifact := func(version, platform string) core.Artifact {
		file := "muhammad-sammy.csharp-" + version
		dir := version
		if platform != "universal" {
			file += "@" + platform
			dir = platform + "/" + version
		}
		base := "https://open-vsx.org/api/muhammad-sammy/csharp/" + dir + "/file/"
		return core.Artifact{
			URL:      base + file + ".vsix",
			Filename: file + ".vsix",
			Metadata: map[string]any{
				"target_platform": platform,
				"signature":       base + file + ".sigzip",
				"sha256":          base + file + ".sha256",
			},
		}
	}
	want := []core.Version{
		{
			Number:      "2.170.0-rc1",
			PublishedAt: time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC),
			Licenses:    "MIT",
			Artifacts:   []core.Artifact{artifact("2.170.0-rc1", "universal")},
			Metadata:    map[string]any{"pre_release": true},
		},
		{
			Number: "2.160.9",
			// The earliest of its builds.
			PublishedAt: time.Date(2026, 10, 9, 16, 23, 47, 49825000, time.UTC),
			Licenses:    "MIT",
			Artifacts:   []core.Artifact{artifact("2.160.9", "linux-x64"), artifact("2.160.9", "alpine-x64")},
			Metadata:    map[string]any{"pre_release": false},
		},
		{
			Number:      "2.110.4",
			PublishedAt: time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC),
			Licenses:    "MIT",
			Artifacts:   []core.Artifact{artifact("2.110.4", "universal")},
			Metadata:    map[string]any{"pre_release": false},
		},
	}
	if !reflect.DeepEqual(versions, want) {
		t.Errorf("FetchVersions =\n%#v\nwant\n%#v", versions, want)
	}
}

func TestFetchVersionsNotFound(t *testing.T) {
	reg, _ := newTestRegistry(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(queryPage(0, 0)))
	})
	_, err := reg.FetchVersions(context.Background(), "nope/missing")
	var notFound *core.NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("FetchVersions error = %v, want NotFoundError", err)
	}
}

// A page that adds nothing ends the listing even if totalSize promises more,
// so a registry that miscounts cannot keep the client asking.
func TestFetchVersionsStopsAtAnEmptyPage(t *testing.T) {
	reg, requests := newTestRegistry(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("offset") == "0" {
			_, _ = w.Write([]byte(queryPage(50, 0, csharpEntry("2.110.4", "universal", "2025-01-02T03:04:05Z", false))))
			return
		}
		_, _ = w.Write([]byte(queryPage(50, 1)))
	})
	versions, err := reg.FetchVersions(context.Background(), "muhammad-sammy/csharp")
	if err != nil {
		t.Fatalf("FetchVersions: %v", err)
	}
	if len(versions) != 1 || requests.Load() != 2 {
		t.Errorf("got %d versions in %d requests, want 1 in 2", len(versions), requests.Load())
	}
}

func TestFetchVersionsRejectsBadEntries(t *testing.T) {
	for _, tt := range []struct {
		name    string
		entries []string
	}{
		{"another extension", []string{strings.Replace(csharpEntry("1.0.0", "universal", "2025-01-02T03:04:05Z", false), `"csharp"`, `"other"`, 1)}},
		{"no version", []string{csharpEntry("", "universal", "2025-01-02T03:04:05Z", false)}},
		{"bad timestamp", []string{csharpEntry("1.0.0", "universal", "yesterday", false)}},
		{"same build twice", []string{
			csharpEntry("1.0.0", "linux-x64", "2025-01-02T03:04:05Z", false),
			csharpEntry("1.0.0", "linux-x64", "2025-01-02T03:04:05Z", false),
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			reg, _ := newTestRegistry(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(queryPage(len(tt.entries), 0, tt.entries...)))
			})
			if _, err := reg.FetchVersions(context.Background(), "muhammad-sammy/csharp"); err == nil {
				t.Error("FetchVersions accepted the response")
			}
		})
	}
}

func TestFetchVersion(t *testing.T) {
	reg, _ := newTestRegistry(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/api/v2/-/query" || q.Get("extensionVersion") != "2.160.9" {
			t.Errorf("unexpected request %s", r.URL)
		}
		_, _ = w.Write([]byte(queryPage(2, 0,
			csharpEntry("2.160.9", "linux-x64", "2026-10-09T16:23:47Z", false),
			csharpEntry("2.160.9", "win32-x64", "2026-10-09T16:23:48Z", false))))
	})
	version, err := reg.FetchVersion(context.Background(), "muhammad-sammy/csharp", "2.160.9")
	if err != nil {
		t.Fatalf("FetchVersion: %v", err)
	}
	var platforms []string
	for _, a := range version.Artifacts {
		platforms = append(platforms, a.Metadata["target_platform"].(string))
	}
	if version.Number != "2.160.9" || !reflect.DeepEqual(platforms, []string{"linux-x64", "win32-x64"}) {
		t.Errorf("FetchVersion = %s with %v, want 2.160.9 with linux-x64 and win32-x64", version.Number, platforms)
	}
}

func TestFetchVersionNotFound(t *testing.T) {
	reg, _ := newTestRegistry(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(queryPage(0, 0)))
	})
	_, err := reg.FetchVersion(context.Background(), "muhammad-sammy/csharp", "9.9.9")
	var notFound *core.NotFoundError
	if !errors.As(err, &notFound) || notFound.Version != "9.9.9" {
		t.Fatalf("FetchVersion error = %v, want NotFoundError for 9.9.9", err)
	}
}

func TestFetchDependencies(t *testing.T) {
	reg, _ := newTestRegistry(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ms-python/python/2026.4.0" {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{
			"namespace": "ms-python", "name": "python", "version": "2026.4.0",
			"dependencies": [{"namespace": "ms-toolsai", "extension": "jupyter"}],
			"bundledExtensions": [
				{"namespace": "ms-python", "extension": "vscode-pylance"},
				{"namespace": "ms-python", "extension": "debugpy"}
			]
		}`))
	})
	deps, err := reg.FetchDependencies(context.Background(), "ms-python/python", "2026.4.0")
	if err != nil {
		t.Fatalf("FetchDependencies: %v", err)
	}
	want := []core.Dependency{
		{Name: "ms-python/debugpy", Scope: core.Runtime},
		{Name: "ms-python/vscode-pylance", Scope: core.Runtime},
		{Name: "ms-toolsai/jupyter", Scope: core.Runtime},
	}
	if !reflect.DeepEqual(deps, want) {
		t.Errorf("FetchDependencies = %#v, want %#v", deps, want)
	}
}

func TestFetchDependenciesNotFound(t *testing.T) {
	reg, _ := newTestRegistry(t, func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	_, err := reg.FetchDependencies(context.Background(), "ms-python/python", "0.0.1")
	var notFound *core.NotFoundError
	if !errors.As(err, &notFound) || notFound.Version != "0.0.1" {
		t.Fatalf("FetchDependencies error = %v, want NotFoundError for 0.0.1", err)
	}
}

func TestInvalidNamesMakeNoRequest(t *testing.T) {
	reg, requests := newTestRegistry(t, func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	ctx := context.Background()
	for _, name := range []string{"", "redhat", "redhat/", "/vscode-yaml", "a/b/c", "../x", "x/..", ".", "redhat."} {
		if _, err := reg.FetchPackage(ctx, name); err == nil {
			t.Errorf("FetchPackage(%q) succeeded", name)
		}
		if _, err := reg.FetchVersions(ctx, name); err == nil {
			t.Errorf("FetchVersions(%q) succeeded", name)
		}
		if _, err := reg.FetchDependencies(ctx, name, "1.0.0"); err == nil {
			t.Errorf("FetchDependencies(%q) succeeded", name)
		}
	}
	for _, version := range []string{"", ".", ".."} {
		if _, err := reg.FetchDependencies(ctx, "redhat/vscode-yaml", version); err == nil {
			t.Errorf("FetchDependencies(version %q) succeeded", version)
		}
	}
	if got := requests.Load(); got != 0 {
		t.Errorf("made %d requests for invalid names", got)
	}
}

func TestEscapesPathSegments(t *testing.T) {
	var path string
	reg, _ := newTestRegistry(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.EscapedPath()
		http.NotFound(w, r)
	})
	_, _ = reg.FetchDependencies(context.Background(), "a b/c", "1.0.0?x#y")
	if want := "/api/a%20b/c/1.0.0%3Fx%23y"; path != want {
		t.Errorf("requested %q, want %q", path, want)
	}
}

func TestURLs(t *testing.T) {
	urls := New("", nil).URLs()
	for _, tt := range []struct {
		name, got, want string
	}{
		{"registry", urls.Registry("redhat/vscode-yaml", ""), "https://open-vsx.org/extension/redhat/vscode-yaml"},
		{"registry version", urls.Registry("redhat.vscode-yaml", "1.15.0"), "https://open-vsx.org/extension/redhat/vscode-yaml/1.15.0"},
		{"download", urls.Download("redhat/vscode-yaml", "1.15.0"), "https://open-vsx.org/api/redhat/vscode-yaml/1.15.0/file/redhat.vscode-yaml-1.15.0.vsix"},
		{"download without version", urls.Download("redhat/vscode-yaml", ""), ""},
		{"documentation", urls.Documentation("redhat/vscode-yaml", "1.15.0"), "https://open-vsx.org/extension/redhat/vscode-yaml/1.15.0"},
		{"purl", urls.PURL("RedHat/VSCode-YAML", "1.15.0"), "pkg:vscode-extension/redhat/vscode-yaml@1.15.0?repository_url=https:%2F%2Fopen-vsx.org"},
		{"purl without version", urls.PURL("redhat/vscode-yaml", ""), "pkg:vscode-extension/redhat/vscode-yaml?repository_url=https:%2F%2Fopen-vsx.org"},
		{"invalid name", urls.Registry("vscode-yaml", ""), ""},
		{"invalid name purl", urls.PURL("vscode-yaml", "1.0.0"), ""},
	} {
		if tt.got != tt.want {
			t.Errorf("%s = %q, want %q", tt.name, tt.got, tt.want)
		}
	}

	self := New("https://vsx.example.com/", nil).URLs()
	if got, want := self.Download("redhat/vscode-yaml", "1.15.0"), "https://vsx.example.com/api/redhat/vscode-yaml/1.15.0/file/redhat.vscode-yaml-1.15.0.vsix"; got != want {
		t.Errorf("self-hosted download = %q, want %q", got, want)
	}
	if got, want := self.PURL("redhat/vscode-yaml", "1.15.0"), "pkg:vscode-extension/redhat/vscode-yaml@1.15.0?repository_url=https:%2F%2Fvsx.example.com"; got != want {
		t.Errorf("self-hosted purl = %q, want %q", got, want)
	}
}
