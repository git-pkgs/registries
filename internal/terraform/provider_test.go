package terraform

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/git-pkgs/registries/internal/core"
)

// Trimmed from registry.terraform.io's /v1/providers/hashicorp/random.
const providerDetailJSON = `{
	"id": "hashicorp/random/3.9.1",
	"owner": "hashicorp",
	"namespace": "hashicorp",
	"name": "random",
	"version": "3.9.1",
	"description": "terraform-provider-random",
	"source": "https://github.com/hashicorp/terraform-provider-random",
	"published_at": "2026-09-11T15:17:02Z",
	"downloads": 3603195929,
	"tier": "official",
	"versions": ["3.6.0", "3.9.1"]
}`

// The Provider Registry Protocol's version list, which every registry
// serves, trimmed from registry.opentofu.org. Versions are unordered.
const providerVersionsJSON = `{
	"versions": [
		{"version": "3.6.0", "protocols": ["5.0"], "platforms": [
			{"os": "linux", "arch": "amd64"}, {"os": "darwin", "arch": "arm64"}]},
		{"version": "3.10.0-beta1", "protocols": ["5.0"], "platforms": [{"os": "linux", "arch": "amd64"}]},
		{"version": "3.9.1", "protocols": ["5.0", "6.0"], "platforms": [
			{"os": "linux", "arch": "amd64"}]}
	]
}`

// providerServer serves path → body, and 404 for anything else.
func providerServer(t *testing.T, routes map[string]string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := routes[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

func TestFetchProviderPackage(t *testing.T) {
	server := providerServer(t, map[string]string{"/v1/providers/hashicorp/random": providerDetailJSON})
	pkg, err := New(server.URL, core.DefaultClient()).FetchPackage(context.Background(), "hashicorp/random")
	if err != nil {
		t.Fatalf("FetchPackage: %v", err)
	}
	if pkg.Name != "hashicorp/random" || pkg.Namespace != "hashicorp" {
		t.Errorf("name = %q, namespace = %q", pkg.Name, pkg.Namespace)
	}
	if pkg.Description != "terraform-provider-random" {
		t.Errorf("description = %q", pkg.Description)
	}
	if pkg.Repository != "https://github.com/hashicorp/terraform-provider-random" {
		t.Errorf("repository = %q", pkg.Repository)
	}
	if pkg.LatestVersion != "3.9.1" {
		t.Errorf("latest = %q, want 3.9.1", pkg.LatestVersion)
	}
	if pkg.Metadata["tier"] != "official" || pkg.Metadata["downloads"] != int64(3603195929) {
		t.Errorf("metadata = %v", pkg.Metadata)
	}
}

// Registries other than registry.terraform.io, such as registry.opentofu.org,
// only serve the Provider Registry Protocol, so the package falls back to
// its version list.
func TestFetchProviderPackageFromProtocolOnlyRegistry(t *testing.T) {
	server := providerServer(t, map[string]string{"/v1/providers/hashicorp/random/versions": providerVersionsJSON})
	pkg, err := New(server.URL, core.DefaultClient()).FetchPackage(context.Background(), "hashicorp/random")
	if err != nil {
		t.Fatalf("FetchPackage: %v", err)
	}
	if pkg.Name != "hashicorp/random" || pkg.Namespace != "hashicorp" {
		t.Errorf("name = %q, namespace = %q", pkg.Name, pkg.Namespace)
	}
	if pkg.LatestVersion != "3.10.0-beta1" {
		t.Errorf("latest = %q, want the highest version, 3.10.0-beta1", pkg.LatestVersion)
	}
}

func TestFetchProviderPackageNotFound(t *testing.T) {
	server := providerServer(t, nil)
	_, err := New(server.URL, core.DefaultClient()).FetchPackage(context.Background(), "hashicorp/missing")
	var notFound *core.NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("err = %v, want a NotFoundError", err)
	}
}

func TestFetchProviderVersions(t *testing.T) {
	server := providerServer(t, map[string]string{"/v1/providers/hashicorp/random/versions": providerVersionsJSON})
	versions, err := New(server.URL, core.DefaultClient()).FetchVersions(context.Background(), "hashicorp/random")
	if err != nil {
		t.Fatalf("FetchVersions: %v", err)
	}
	var got []string
	for _, v := range versions {
		got = append(got, v.Number)
	}
	if want := []string{"3.10.0-beta1", "3.9.1", "3.6.0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("versions = %v, want %v (newest first, by version not by string)", got, want)
	}
	v := versions[1]
	if !reflect.DeepEqual(v.Metadata["protocols"], []string{"5.0", "6.0"}) {
		t.Errorf("protocols = %v", v.Metadata["protocols"])
	}
	if !reflect.DeepEqual(versions[2].Metadata["platforms"], []string{"linux_amd64", "darwin_arm64"}) {
		t.Errorf("platforms = %v", versions[2].Metadata["platforms"])
	}
}

func TestFetchProviderVersionsNotFound(t *testing.T) {
	server := providerServer(t, nil)
	_, err := New(server.URL, core.DefaultClient()).FetchVersions(context.Background(), "hashicorp/missing")
	var notFound *core.NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("err = %v, want a NotFoundError", err)
	}
}

// Providers declare no dependencies, so no request is made.
func TestFetchProviderDependencies(t *testing.T) {
	server := providerServer(t, nil)
	deps, err := New(server.URL, core.DefaultClient()).FetchDependencies(context.Background(), "hashicorp/random", "3.6.0")
	if err != nil || len(deps) != 0 {
		t.Errorf("deps = %v, %v; want none", deps, err)
	}
}

func TestFetchProviderMaintainers(t *testing.T) {
	maintainers, err := New("", nil).FetchMaintainers(context.Background(), "hashicorp/random")
	if err != nil || len(maintainers) != 1 || maintainers[0].Login != "hashicorp" {
		t.Errorf("maintainers = %v, %v; want the namespace", maintainers, err)
	}
}

func TestParseProviderName(t *testing.T) {
	tests := []struct {
		input, namespace, typ string
		ok                    bool
	}{
		{"hashicorp/random", "hashicorp", "random", true},
		{"integrations/github", "integrations", "github", true},
		{"random", "", "", false},
		{"hashicorp/consul/aws", "", "", false},
		{"/random", "", "", false},
		{"hashicorp/", "", "", false},
	}
	for _, tt := range tests {
		namespace, typ, ok := parseProviderName(tt.input)
		if namespace != tt.namespace || typ != tt.typ || ok != tt.ok {
			t.Errorf("parseProviderName(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tt.input, namespace, typ, ok, tt.namespace, tt.typ, tt.ok)
		}
	}
}

func TestProviderURLs(t *testing.T) {
	urls := New("https://registry.terraform.io", nil).URLs()
	other := New("https://registry.opentofu.org", nil).URLs()
	tests := []struct {
		name, got, want string
	}{
		{"registry", urls.Registry("hashicorp/random", "3.6.0"), "https://registry.terraform.io/providers/hashicorp/random/3.6.0"},
		{"registry latest", urls.Registry("hashicorp/random", ""), "https://registry.terraform.io/providers/hashicorp/random/latest"},
		{"documentation", urls.Documentation("hashicorp/random", "3.6.0"), "https://registry.terraform.io/providers/hashicorp/random/3.6.0/docs"},
		// Archives are per platform; the download endpoint names them.
		{"download", urls.Download("hashicorp/random", "3.6.0"), ""},
		{"purl", urls.PURL("hashicorp/random", "3.6.0"), "pkg:terraform/hashicorp/random@3.6.0"},
		{"purl without version", urls.PURL("hashicorp/random", ""), "pkg:terraform/hashicorp/random"},
		// Other registries have no pages at these paths.
		{"other registry page", other.Registry("hashicorp/random", "3.6.0"), ""},
		{"other registry purl", other.PURL("hashicorp/random", "3.6.0"),
			"pkg:terraform/hashicorp/random@3.6.0?repository_url=https:%2F%2Fregistry.opentofu.org"},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s = %q, want %q", tt.name, tt.got, tt.want)
		}
	}
}

func TestFetchProviderVersionsSkipsEntriesWithoutAVersion(t *testing.T) {
	server := providerServer(t, map[string]string{"/v1/providers/hashicorp/random/versions": `{"versions": [{"version": ""}, {"version": "3.6.0"}]}`})
	versions, err := New(server.URL, core.DefaultClient()).FetchVersions(context.Background(), "hashicorp/random")
	if err != nil || len(versions) != 1 || versions[0].Number != "3.6.0" {
		t.Errorf("versions = %v, %v; want only 3.6.0", versions, err)
	}
}
