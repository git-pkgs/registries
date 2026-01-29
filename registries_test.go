package registries_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/git-pkgs/registries"
	_ "github.com/git-pkgs/registries/all"
)

func TestSupportedEcosystems(t *testing.T) {
	ecosystems := registries.SupportedEcosystems()

	expected := []string{"brew", "cargo", "clojars", "cocoapods", "composer", "conda", "cpan", "cran", "deno", "dub", "elm", "gem", "golang", "hackage", "haxelib", "hex", "julia", "luarocks", "maven", "nimble", "npm", "nuget", "pub", "pypi", "terraform"}
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
