package helm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/git-pkgs/registries/client"
	"github.com/git-pkgs/registries/internal/core"
)

const testIndex = `apiVersion: v1
generated: 2026-08-10T12:00:00Z
entries:
  demo:
    - name: demo
      version: 3.0.0
      description: Deprecated demo chart
      deprecated: true
      checksum: old-checksum-is-not-a-digest
      urls:
        - charts/demo-3.0.0.tgz
      created: 2026-08-03T12:00:00Z
    - name: demo
      version: 1.5.0
      description: Older demo chart
      urls:
        - charts/demo-1.5.0.tgz
      created: 2026-08-01T12:00:00Z
      digest: 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
    - name: demo
      version: 2.5.0
      description: Removed demo chart
      removed: true
      urls:
        - charts/demo-2.5.0.tgz
      created: 2026-08-04T12:00:00Z
    - name: demo
      version: 2.0.0
      description: Active demo chart
      home: https://example.com/demo
      sources:
        - https://github.com/example/demo.git
        - https://gitlab.com/example/demo-mirror
      keywords:
        - demo
        - kubernetes
      maintainers:
        - name: Example Maintainer
          email: maintainer@example.com
          url: https://example.com/maintainer
      icon: https://example.com/demo.svg
      apiVersion: v2
      condition: demo.enabled
      tags: backend
      appVersion: "12.3.0"
      annotations:
        example.com/channel: stable
      kubeVersion: ">= 1.28.0"
      dependencies:
        - name: redis
          version: "~17.0.0"
          repository: https://charts.example.com/dependencies
          alias: cache
          condition: redis.enabled
          tags:
            - database
          enabled: true
          import-values:
            - child: exports
              parent: imports
        - name: common
          version: ">=2.0.0"
          repository: file://../common
      type: application
      urls:
        - charts/demo-2.0.0.tgz
        - https://cdn.example.com/demo-2.0.0.tgz
        - http://username:password@downloads.example.com/demo-2.0.0.tgz
      created: 2026-08-02T12:34:56Z
      digest: sha256:abcdef123456
  minimal:
    - name: minimal
      version: 0.1.0
`

func TestFetchPackage(t *testing.T) {
	server := indexServer(t, "/repository/index.yaml", testIndex)
	defer server.Close()

	registry := New(server.URL+"/repository", core.DefaultClient())
	pkg, err := registry.FetchPackage(context.Background(), "demo")
	if err != nil {
		t.Fatalf("FetchPackage failed: %v", err)
	}

	if pkg.Name != "demo" {
		t.Errorf("Name = %q, want demo", pkg.Name)
	}
	if pkg.Description != "Active demo chart" {
		t.Errorf("Description = %q, want active chart description", pkg.Description)
	}
	if pkg.Homepage != "https://example.com/demo" {
		t.Errorf("Homepage = %q, want https://example.com/demo", pkg.Homepage)
	}
	if pkg.Repository != "https://github.com/example/demo" {
		t.Errorf("Repository = %q, want normalized source repository", pkg.Repository)
	}
	if pkg.LatestVersion != "2.0.0" {
		t.Errorf("LatestVersion = %q, want 2.0.0", pkg.LatestVersion)
	}
	if len(pkg.Keywords) != 2 || pkg.Keywords[1] != "kubernetes" {
		t.Errorf("Keywords = %v, want chart keywords", pkg.Keywords)
	}

	if pkg.Metadata["appVersion"] != "12.3.0" {
		t.Errorf("Metadata appVersion = %v, want 12.3.0", pkg.Metadata["appVersion"])
	}
	if pkg.Metadata["apiVersion"] != "v2" {
		t.Errorf("Metadata apiVersion = %v, want v2", pkg.Metadata["apiVersion"])
	}
	if pkg.Metadata["kubeVersion"] != ">= 1.28.0" {
		t.Errorf("Metadata kubeVersion = %v, want constraint", pkg.Metadata["kubeVersion"])
	}
	if pkg.Metadata["type"] != "application" {
		t.Errorf("Metadata type = %v, want application", pkg.Metadata["type"])
	}
	annotations, ok := pkg.Metadata["annotations"].(map[string]string)
	if !ok || annotations["example.com/channel"] != "stable" {
		t.Errorf("Metadata annotations = %#v, want stable channel", pkg.Metadata["annotations"])
	}
	sources, ok := pkg.Metadata["sources"].([]string)
	if !ok || len(sources) != 2 {
		t.Errorf("Metadata sources = %#v, want both source URLs", pkg.Metadata["sources"])
	}
}

func TestFetchVersions(t *testing.T) {
	server := indexServer(t, "/repository/index.yaml", testIndex)
	defer server.Close()

	registry := New(server.URL+"/repository", core.DefaultClient())
	versions, err := registry.FetchVersions(context.Background(), "demo")
	if err != nil {
		t.Fatalf("FetchVersions failed: %v", err)
	}
	if len(versions) != 4 {
		t.Fatalf("got %d versions, want 4", len(versions))
	}

	byNumber := make(map[string]core.Version, len(versions))
	for _, version := range versions {
		byNumber[version.Number] = version
	}

	if byNumber["3.0.0"].Status != core.StatusDeprecated {
		t.Errorf("3.0.0 status = %q, want deprecated", byNumber["3.0.0"].Status)
	}
	if byNumber["3.0.0"].Integrity != "" {
		t.Errorf("3.0.0 integrity = %q, deprecated checksum must be ignored", byNumber["3.0.0"].Integrity)
	}
	if byNumber["2.5.0"].Status != core.StatusYanked {
		t.Errorf("2.5.0 status = %q, want yanked", byNumber["2.5.0"].Status)
	}
	wantBareDigest := "sha256-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if byNumber["1.5.0"].Integrity != wantBareDigest {
		t.Errorf("1.5.0 integrity = %q, want %q", byNumber["1.5.0"].Integrity, wantBareDigest)
	}

	active := byNumber["2.0.0"]
	if active.Integrity != "sha256-abcdef123456" {
		t.Errorf("2.0.0 integrity = %q, want normalized digest", active.Integrity)
	}
	wantCreated := time.Date(2026, time.August, 2, 12, 34, 56, 0, time.UTC)
	if !active.PublishedAt.Equal(wantCreated) {
		t.Errorf("2.0.0 PublishedAt = %v, want %v", active.PublishedAt, wantCreated)
	}

	urls, ok := active.Metadata["urls"].([]string)
	if !ok {
		t.Fatalf("urls metadata has type %T, want []string", active.Metadata["urls"])
	}
	wantRelative := server.URL + "/repository/charts/demo-2.0.0.tgz"
	if len(urls) != 3 || urls[0] != wantRelative || urls[1] != "https://cdn.example.com/demo-2.0.0.tgz" || urls[2] != "http://username:password@downloads.example.com/demo-2.0.0.tgz" {
		t.Errorf("resolved URLs = %v, want relative and absolute artifact URLs", urls)
	}
	if got := registry.URLs().Download("demo", "2.0.0"); got != wantRelative {
		t.Errorf("Download URL = %q, want %q", got, wantRelative)
	}

	dependencies, ok := active.Metadata["dependencies"].([]dependencyInfo)
	if !ok || len(dependencies) != 2 {
		t.Fatalf("dependencies metadata = %#v, want two dependencies", active.Metadata["dependencies"])
	}
	if dependencies[0].Repository != "https://charts.example.com/dependencies" || dependencies[0].Alias != "cache" {
		t.Errorf("dependency metadata = %#v, want repository and alias", dependencies[0])
	}
	if dependencies[0].Condition != "redis.enabled" || len(dependencies[0].Tags) != 1 || len(dependencies[0].ImportValues) != 1 {
		t.Errorf("dependency metadata = %#v, want condition, tags, and import values", dependencies[0])
	}
}

func TestFetchDependencies(t *testing.T) {
	server := indexServer(t, "/index.yaml", testIndex)
	defer server.Close()

	registry := New(server.URL, core.DefaultClient())
	dependencies, err := registry.FetchDependencies(context.Background(), "demo", "2.0.0")
	if err != nil {
		t.Fatalf("FetchDependencies failed: %v", err)
	}
	if len(dependencies) != 2 {
		t.Fatalf("got %d dependencies, want 2", len(dependencies))
	}
	if dependencies[0].Name != "redis" || dependencies[0].Requirements != "~17.0.0" {
		t.Errorf("first dependency = %#v, want redis version constraint", dependencies[0])
	}
	if dependencies[0].Scope != core.Runtime {
		t.Errorf("first dependency scope = %q, want runtime", dependencies[0].Scope)
	}
}

func TestFetchMaintainers(t *testing.T) {
	server := indexServer(t, "/index.yaml", testIndex)
	defer server.Close()

	registry := New(server.URL, core.DefaultClient())
	maintainers, err := registry.FetchMaintainers(context.Background(), "demo")
	if err != nil {
		t.Fatalf("FetchMaintainers failed: %v", err)
	}
	if len(maintainers) != 1 {
		t.Fatalf("got %d maintainers, want 1", len(maintainers))
	}
	if maintainers[0].Name != "Example Maintainer" || maintainers[0].Email != "maintainer@example.com" || maintainers[0].URL != "https://example.com/maintainer" {
		t.Errorf("maintainer = %#v, want name, email, and URL", maintainers[0])
	}
}

func TestMissingOptionalFields(t *testing.T) {
	server := indexServer(t, "/index.yaml", testIndex)
	defer server.Close()

	registry := New(server.URL, core.DefaultClient())
	pkg, err := registry.FetchPackage(context.Background(), "minimal")
	if err != nil {
		t.Fatalf("FetchPackage failed: %v", err)
	}
	if pkg.Name != "minimal" || pkg.LatestVersion != "0.1.0" {
		t.Errorf("package = %#v, want minimal chart", pkg)
	}

	versions, err := registry.FetchVersions(context.Background(), "minimal")
	if err != nil {
		t.Fatalf("FetchVersions failed: %v", err)
	}
	if len(versions) != 1 || versions[0].Number != "0.1.0" {
		t.Fatalf("versions = %#v, want minimal version", versions)
	}
	urls, ok := versions[0].Metadata["urls"].([]string)
	if !ok || len(urls) != 0 {
		t.Errorf("urls metadata = %#v, want empty []string", versions[0].Metadata["urls"])
	}
}

func TestNotFoundLookups(t *testing.T) {
	server := indexServer(t, "/index.yaml", testIndex)
	defer server.Close()

	registry := New(server.URL, core.DefaultClient())
	_, err := registry.FetchPackage(context.Background(), "missing")
	assertNotFound(t, err, "missing", "")

	_, err = registry.FetchDependencies(context.Background(), "demo", "9.9.9")
	assertNotFound(t, err, "demo", "9.9.9")
}

func TestMalformedIndex(t *testing.T) {
	tests := []struct {
		name  string
		index string
	}{
		{"invalid YAML", "apiVersion: v1\nentries: ["},
		{"missing apiVersion", "entries: {}\n"},
		{"missing entries", "apiVersion: v1\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := indexServer(t, "/index.yaml", tt.index)
			defer server.Close()

			registry := New(server.URL, core.DefaultClient())
			if _, err := registry.FetchVersions(context.Background(), "demo"); err == nil {
				t.Fatal("FetchVersions succeeded with malformed index")
			}
		})
	}
}

func TestExactIndexURL(t *testing.T) {
	server := indexServer(t, "/custom/index.yaml", testIndex)
	defer server.Close()

	registry := New(server.URL+"/custom/index.yaml", core.DefaultClient())
	if _, err := registry.FetchVersions(context.Background(), "minimal"); err != nil {
		t.Fatalf("FetchVersions failed: %v", err)
	}
	if got := registry.URLs().Registry("minimal", ""); got != server.URL+"/custom/index.yaml" {
		t.Errorf("Registry URL = %q, want exact index URL", got)
	}
}

func TestMalformedChartURLIsSkipped(t *testing.T) {
	index := `apiVersion: v1
entries:
  demo:
    - name: demo
      version: 1.0.0
      urls:
        - "http://[invalid"
        - charts/demo-1.0.0.tgz
`
	server := indexServer(t, "/index.yaml", index)
	defer server.Close()

	registry := New(server.URL, core.DefaultClient())
	versions, err := registry.FetchVersions(context.Background(), "demo")
	if err != nil {
		t.Fatalf("FetchVersions failed: %v", err)
	}
	urls, ok := versions[0].Metadata["urls"].([]string)
	want := server.URL + "/charts/demo-1.0.0.tgz"
	if !ok || len(urls) != 1 || urls[0] != want {
		t.Errorf("resolved URLs = %#v, want [%q]", versions[0].Metadata["urls"], want)
	}
	if got := registry.URLs().Download("demo", "1.0.0"); got != want {
		t.Errorf("Download URL = %q, want %q", got, want)
	}
}

func TestCustomAuthenticationClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer chart-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(testIndex))
	}))
	defer server.Close()

	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		request = request.Clone(request.Context())
		request.Header.Set("Authorization", "Bearer chart-token")
		return http.DefaultTransport.RoundTrip(request)
	})
	customClient := client.NewClient(client.WithTransport(transport))
	registry := New(server.URL, customClient)
	if _, err := registry.FetchPackage(context.Background(), "demo"); err != nil {
		t.Fatalf("FetchPackage with custom authentication failed: %v", err)
	}
}

func TestURLBuilder(t *testing.T) {
	registry := New("https://charts.example.com/repository", nil)
	urls := registry.URLs()

	if got := urls.Registry("demo", "2.0.0"); got != "https://charts.example.com/repository/index.yaml" {
		t.Errorf("Registry URL = %q, want index URL", got)
	}
	if got := urls.Download("demo", "2.0.0"); got != "" {
		t.Errorf("Download URL before fetching index = %q, want empty", got)
	}
	if got := urls.Documentation("demo", "2.0.0"); got != "" {
		t.Errorf("Documentation URL = %q, want empty", got)
	}
	if got := urls.PURL("demo", "2.0.0"); got != "pkg:helm/demo@2.0.0" {
		t.Errorf("version PURL = %q, want pkg:helm/demo@2.0.0", got)
	}
	if got := urls.PURL("demo", ""); got != "pkg:helm/demo" {
		t.Errorf("package PURL = %q, want pkg:helm/demo", got)
	}
}

func TestEcosystem(t *testing.T) {
	if got := New("https://charts.example.com", nil).Ecosystem(); got != ecosystem {
		t.Errorf("Ecosystem = %q, want %q", got, ecosystem)
	}
}

func indexServer(t *testing.T, expectedPath, index string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != expectedPath {
			t.Errorf("request path = %q, want %q", r.URL.Path, expectedPath)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(index))
	}))
}

func assertNotFound(t *testing.T, err error, name, version string) {
	t.Helper()
	var notFound *core.NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("error = %v, want NotFoundError", err)
	}
	if notFound.Ecosystem != ecosystem || notFound.Name != name || notFound.Version != version {
		t.Errorf("NotFoundError = %#v, want ecosystem %q, name %q, version %q", notFound, ecosystem, name, version)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}
