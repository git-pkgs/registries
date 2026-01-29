package elm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/git-pkgs/registries/internal/core"
)

func TestFetchPackage(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("/packages/elm/json/releases.json", func(w http.ResponseWriter, r *http.Request) {
		releases := map[string]int64{
			"1.1.3": 1609459200000,
			"1.1.2": 1577836800000,
			"1.0.0": 1546300800000,
		}
		_ = json.NewEncoder(w).Encode(releases)
	})

	mux.HandleFunc("/packages/elm/json/1.1.3/elm.json", func(w http.ResponseWriter, r *http.Request) {
		elmJson := map[string]interface{}{
			"type":        "package",
			"name":        "elm/json",
			"summary":     "Encode and decode JSON values",
			"license":     "BSD-3-Clause",
			"version":     "1.1.3",
			"elm-version": "0.19.0 <= v < 0.20.0",
			"dependencies": map[string]string{
				"elm/core": "1.0.0 <= v < 2.0.0",
			},
		}
		_ = json.NewEncoder(w).Encode(elmJson)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	pkg, err := reg.FetchPackage(context.Background(), "elm/json")
	if err != nil {
		t.Fatalf("FetchPackage failed: %v", err)
	}

	if pkg.Name != "elm/json" {
		t.Errorf("expected name 'elm/json', got %q", pkg.Name)
	}
	if pkg.Description != "Encode and decode JSON values" {
		t.Errorf("unexpected description: %q", pkg.Description)
	}
	if pkg.Licenses != "BSD-3-Clause" {
		t.Errorf("unexpected license: %q", pkg.Licenses)
	}
	if pkg.Namespace != "elm" {
		t.Errorf("expected namespace 'elm', got %q", pkg.Namespace)
	}
	if pkg.Repository != "https://github.com/elm/json" {
		t.Errorf("unexpected repository: %q", pkg.Repository)
	}
}

func TestFetchVersions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		releases := map[string]int64{
			"1.1.3": 1609459200000,
			"1.1.2": 1577836800000,
			"1.0.0": 1546300800000,
		}
		_ = json.NewEncoder(w).Encode(releases)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	versions, err := reg.FetchVersions(context.Background(), "elm/json")
	if err != nil {
		t.Fatalf("FetchVersions failed: %v", err)
	}

	if len(versions) != 3 {
		t.Fatalf("expected 3 versions, got %d", len(versions))
	}

	// Should be sorted newest first
	if versions[0].Number != "1.1.3" {
		t.Errorf("expected first version '1.1.3', got %q", versions[0].Number)
	}
	if versions[0].PublishedAt.IsZero() {
		t.Error("expected non-zero published time")
	}
}

func TestFetchDependencies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		elmJson := map[string]interface{}{
			"type":    "package",
			"name":    "elm/http",
			"version": "2.0.0",
			"dependencies": map[string]string{
				"elm/core":  "1.0.0 <= v < 2.0.0",
				"elm/json":  "1.0.0 <= v < 2.0.0",
				"elm/bytes": "1.0.0 <= v < 2.0.0",
			},
			"test-dependencies": map[string]string{
				"elm-explorations/test": "1.0.0 <= v < 2.0.0",
			},
		}
		_ = json.NewEncoder(w).Encode(elmJson)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	deps, err := reg.FetchDependencies(context.Background(), "elm/http", "2.0.0")
	if err != nil {
		t.Fatalf("FetchDependencies failed: %v", err)
	}

	if len(deps) != 4 {
		t.Fatalf("expected 4 dependencies, got %d", len(deps))
	}

	// Check scopes
	scopeMap := make(map[string]core.Scope)
	for _, d := range deps {
		scopeMap[d.Name] = d.Scope
	}

	if scopeMap["elm/core"] != core.Runtime {
		t.Errorf("expected runtime scope for elm/core")
	}
	if scopeMap["elm-explorations/test"] != core.Test {
		t.Errorf("expected test scope for elm-explorations/test")
	}
}

func TestFetchMaintainers(t *testing.T) {
	reg := New("", nil)
	maintainers, err := reg.FetchMaintainers(context.Background(), "elm/json")
	if err != nil {
		t.Fatalf("FetchMaintainers failed: %v", err)
	}

	if len(maintainers) != 1 {
		t.Fatalf("expected 1 maintainer, got %d", len(maintainers))
	}

	if maintainers[0].Login != "elm" {
		t.Errorf("expected login 'elm', got %q", maintainers[0].Login)
	}
}

func TestParsePackageName(t *testing.T) {
	tests := []struct {
		input  string
		author string
		pkg    string
	}{
		{"elm/json", "elm", "json"},
		{"elm-community/list-extra", "elm-community", "list-extra"},
		{"invalid", "", "invalid"},
	}

	for _, tt := range tests {
		author, pkg := parsePackageName(tt.input)
		if author != tt.author || pkg != tt.pkg {
			t.Errorf("parsePackageName(%q) = (%q, %q), want (%q, %q)",
				tt.input, author, pkg, tt.author, tt.pkg)
		}
	}
}

func TestURLBuilder(t *testing.T) {
	reg := New("https://package.elm-lang.org", nil)
	urls := reg.URLs()

	tests := []struct {
		name     string
		fn       func() string
		expected string
	}{
		{"registry", func() string { return urls.Registry("elm/json", "1.1.3") }, "https://package.elm-lang.org/packages/elm/json/1.1.3"},
		{"registry_latest", func() string { return urls.Registry("elm/json", "") }, "https://package.elm-lang.org/packages/elm/json/latest"},
		{"download", func() string { return urls.Download("elm/json", "1.1.3") }, "https://github.com/elm/json/archive/1.1.3.tar.gz"},
		{"purl", func() string { return urls.PURL("elm/json", "1.1.3") }, "pkg:elm/elm/json@1.1.3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.fn()
			if got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestEcosystem(t *testing.T) {
	reg := New("", nil)
	if reg.Ecosystem() != "elm" {
		t.Errorf("expected ecosystem 'elm', got %q", reg.Ecosystem())
	}
}
