package registries_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/git-pkgs/registries"
	_ "github.com/git-pkgs/registries/all"
)

func TestSupportedEcosystems(t *testing.T) {
	ecosystems := registries.SupportedEcosystems()

	expected := []string{"apk", "brew", "cargo", "clojars", "cocoapods", "composer", "conda", "cpan", "cran", "deno", "dub", "elm", "gem", "golang", "hackage", "haxelib", "helm", "hex", "julia", "luarocks", "maven", "nimble", "npm", "nuget", "pub", "pypi", "swift", "terraform"}
	sort.Strings(ecosystems)

	if len(ecosystems) != len(expected) {
		t.Fatalf("expected %d ecosystems, got %d: %v", len(expected), len(ecosystems), ecosystems)
	}

	for i, eco := range expected {
		if ecosystems[i] != eco {
			t.Errorf("expected ecosystem %q at position %d, got %q", eco, i, ecosystems[i])
		}
	}
}

func TestNew(t *testing.T) {
	tests := []struct {
		ecosystem string
		wantErr   bool
	}{
		{"brew", false},
		{"cargo", false},
		{"npm", false},
		{"gem", false},
		{"pypi", false},
		{"golang", false},
		{"hex", false},
		{"pub", false},
		{"composer", false},
		{"nuget", false},
		{"maven", false},
		{"cocoapods", false},
		{"clojars", false},
		{"cpan", false},
		{"hackage", false},
		{"cran", false},
		{"conda", false},
		{"julia", false},
		{"elm", false},
		{"dub", false},
		{"luarocks", false},
		{"nimble", false},
		{"haxelib", false},
		{"deno", false},
		{"terraform", false},
		{"helm", true},
		{"swift", true},
		{"apk", true},
		{"unknown", true},
	}

	for _, tt := range tests {
		t.Run(tt.ecosystem, func(t *testing.T) {
			_, err := registries.New(tt.ecosystem, "", nil)
			if (err != nil) != tt.wantErr {
				t.Errorf("New(%q) error = %v, wantErr %v", tt.ecosystem, err, tt.wantErr)
			}
		})
	}
}

func TestDefaultURL(t *testing.T) {
	tests := []struct {
		ecosystem string
		want      string
	}{
		{"brew", "https://formulae.brew.sh"},
		{"cargo", "https://crates.io"},
		{"npm", "https://registry.npmjs.org"},
		{"gem", "https://rubygems.org"},
		{"pypi", "https://pypi.org"},
		{"golang", "https://proxy.golang.org"},
		{"hex", "https://hex.pm"},
		{"pub", "https://pub.dev"},
		{"composer", "https://packagist.org"},
		{"nuget", "https://api.nuget.org/v3"},
		{"maven", "https://repo1.maven.org/maven2"},
		{"cocoapods", "https://trunk.cocoapods.org"},
		{"clojars", "https://clojars.org"},
		{"cpan", "https://fastapi.metacpan.org"},
		{"hackage", "https://hackage.haskell.org"},
		{"cran", "https://cran.r-project.org"},
		{"conda", "https://api.anaconda.org"},
		{"julia", "https://raw.githubusercontent.com/JuliaRegistries/General/master"},
		{"elm", "https://package.elm-lang.org"},
		{"dub", "https://code.dlang.org"},
		{"luarocks", "https://luarocks.org"},
		{"nimble", "https://nimble.directory"},
		{"haxelib", "https://lib.haxe.org"},
		{"deno", "https://apiland.deno.dev"},
		{"terraform", "https://registry.terraform.io"},
		{"helm", ""},
		{"swift", ""},
		{"apk", ""},
	}

	for _, tt := range tests {
		t.Run(tt.ecosystem, func(t *testing.T) {
			got := registries.DefaultURL(tt.ecosystem)
			if got != tt.want {
				t.Errorf("DefaultURL(%q) = %q, want %q", tt.ecosystem, got, tt.want)
			}
		})
	}
}

func TestNewWithRequiredCustomURL(t *testing.T) {
	reg, err := registries.New("helm", "https://charts.example.com", nil)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	if reg.Ecosystem() != "helm" {
		t.Errorf("Ecosystem = %q, want helm", reg.Ecosystem())
	}

	_, err = registries.New("helm", "", nil)
	if err == nil || err.Error() != "no registry URL configured for ecosystem: helm" {
		t.Errorf("New without URL error = %v, want clear missing URL error", err)
	}
}

func TestIntegration(t *testing.T) {
	// Test with a mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/crates/serde" {
			resp := map[string]interface{}{
				"crate": map[string]interface{}{
					"id":          "serde",
					"name":        "serde",
					"description": "A serialization framework",
					"repository":  "https://github.com/serde-rs/serde",
				},
				"versions": []map[string]interface{}{
					{
						"id":         1,
						"num":        "1.0.0",
						"license":    "MIT",
						"checksum":   "abc123",
						"yanked":     false,
						"created_at": "2023-01-01T00:00:00Z",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		w.WriteHeader(404)
	}))
	defer server.Close()

	reg, err := registries.New("cargo", server.URL, registries.DefaultClient())
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	// Test Ecosystem
	if reg.Ecosystem() != "cargo" {
		t.Errorf("expected ecosystem 'cargo', got %q", reg.Ecosystem())
	}

	// Test FetchPackage
	pkg, err := reg.FetchPackage(context.Background(), "serde")
	if err != nil {
		t.Fatalf("FetchPackage failed: %v", err)
	}

	if pkg.Name != "serde" {
		t.Errorf("expected name 'serde', got %q", pkg.Name)
	}
	if pkg.Repository != "https://github.com/serde-rs/serde" {
		t.Errorf("unexpected repository: %q", pkg.Repository)
	}

	// Test URLs
	urls := reg.URLs()
	if urls.PURL("serde", "1.0.0") != "pkg:cargo/serde@1.0.0" {
		t.Errorf("unexpected PURL: %q", urls.PURL("serde", "1.0.0"))
	}
}

func TestBuildURLs(t *testing.T) {
	reg, err := registries.New("cargo", "", nil)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	got := registries.BuildURLs(reg.URLs(), "serde", "1.0.0")

	if got["registry"] == "" {
		t.Error("expected non-empty registry URL")
	}
	if got["docs"] == "" {
		t.Error("expected non-empty docs URL")
	}
	if got["purl"] != "pkg:cargo/serde@1.0.0" {
		t.Errorf("purl = %q, want %q", got["purl"], "pkg:cargo/serde@1.0.0")
	}
}

func TestBuildURLsCleansPURLConstraints(t *testing.T) {
	reg, err := registries.New("npm", "", nil)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	tests := []struct {
		name    string
		version string
		want    string
	}{
		{name: "exclusive lower bound", version: ">1.0.0", want: "pkg:npm/lodash@%3E1.0.0"},
		{name: "union uses ecosystem ordering", version: "2.0.0 || 1.0.0", want: "pkg:npm/lodash@1.0.0"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := registries.BuildURLs(reg.URLs(), "lodash", test.version)
			if got["purl"] != test.want {
				t.Errorf("purl = %q, want %q", got["purl"], test.want)
			}
		})
	}
}

func TestConstants(t *testing.T) {
	// Verify constants are exported correctly
	if registries.Runtime != "runtime" {
		t.Errorf("Runtime constant mismatch")
	}
	if registries.Development != "development" {
		t.Errorf("Development constant mismatch")
	}
	if registries.StatusYanked != "yanked" {
		t.Errorf("StatusYanked constant mismatch")
	}
}

func TestRootClientOptions(t *testing.T) {
	custom := &http.Client{Timeout: 7 * time.Second}
	c := registries.NewClient(registries.WithHTTPClient(custom))
	if c.HTTPClient != custom {
		t.Errorf("WithHTTPClient set HTTPClient to %p; want %p", c.HTTPClient, custom)
	}

	transport := http.DefaultTransport
	c = registries.NewClient(registries.WithTransport(transport))
	if c.HTTPClient.Transport != transport {
		t.Errorf("WithTransport set Transport to %v; want %v", c.HTTPClient.Transport, transport)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	c = registries.NewClient(registries.WithSafeHTTP())
	resp, err := c.HTTPClient.Get(server.URL)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("WithSafeHTTP should refuse a loopback address")
	}
	if !strings.Contains(err.Error(), "loopback") {
		t.Errorf("WithSafeHTTP error %v should mention loopback", err)
	}
}

func TestAPKFromPURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/alpine/v3.23/main/x86_64/APKINDEX.tar.gz" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(apkIndex(t, "P:curl\nV:8.22.0-r0\nA:x86_64\nt:1788539493\nD:libcurl=8.22.0-r0\n"))
	}))
	defer srv.Close()

	purl := "pkg:apk/alpine/curl@8.22.0-r0?arch=x86_64&repository_url=" + srv.URL + "/alpine/v3.23/main/x86_64"
	v, err := registries.FetchVersionFromPURL(context.Background(), purl, nil)
	if err != nil {
		t.Fatalf("FetchVersionFromPURL failed: %v", err)
	}
	if v.Number != "8.22.0-r0" || v.Artifacts[0].URL != srv.URL+"/alpine/v3.23/main/x86_64/curl-8.22.0-r0.apk" {
		t.Errorf("version = %+v", v)
	}

	pkg, err := registries.FetchPackageFromPURL(context.Background(), purl, nil)
	if err != nil || pkg.Namespace != "alpine" || pkg.Name != "curl" {
		t.Errorf("package = %+v, %v", pkg, err)
	}

	deps, err := registries.FetchDependenciesFromPURL(context.Background(), purl, nil)
	if err != nil || len(deps) != 1 || deps[0].Name != "libcurl" {
		t.Errorf("deps = %+v, %v", deps, err)
	}

	if _, err := registries.FetchVersionFromPURL(context.Background(), "pkg:apk/alpine/curl@8.22.0-r0?arch=x86_64", nil); err == nil || !strings.Contains(err.Error(), "no registry URL configured") {
		t.Errorf("missing URL err = %v", err)
	}
}

func apkIndex(t *testing.T, index string) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "APKINDEX", Mode: 0o644, Size: int64(len(index))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(index)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
