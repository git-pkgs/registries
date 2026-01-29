package homebrew

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/git-pkgs/registries/internal/core"
)

func TestFetchPackage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/formula/wget.json" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}

		resp := formulaResponse{
			Name:     "wget",
			FullName: "wget",
			Tap:      "homebrew/core",
			Desc:     "Internet file retriever",
			License:  "GPL-3.0-or-later",
			Homepage: "https://www.gnu.org/software/wget/",
			Versions: versionsInfo{Stable: "1.21.4", Bottle: true},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	pkg, err := reg.FetchPackage(context.Background(), "wget")
	if err != nil {
		t.Fatalf("FetchPackage failed: %v", err)
	}

	if pkg.Name != "wget" {
		t.Errorf("expected name 'wget', got %q", pkg.Name)
	}
	if pkg.Description != "Internet file retriever" {
		t.Errorf("unexpected description: %q", pkg.Description)
	}
	if pkg.Licenses != "GPL-3.0-or-later" {
		t.Errorf("unexpected license: %q", pkg.Licenses)
	}
}

func TestFetchPackageWithGitHubRepo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := formulaResponse{
			Name:     "jq",
			Desc:     "Lightweight and flexible command-line JSON processor",
			Homepage: "https://github.com/stedolan/jq",
			License:  "MIT",
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	pkg, err := reg.FetchPackage(context.Background(), "jq")
	if err != nil {
		t.Fatalf("FetchPackage failed: %v", err)
	}

	if pkg.Repository != "https://github.com/stedolan/jq" {
		t.Errorf("expected repository from GitHub homepage, got %q", pkg.Repository)
	}
}

func TestFetchVersions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := formulaResponse{
			Name:    "python",
			License: "Python-2.0",
			Versions: versionsInfo{
				Stable: "3.12.1",
				Bottle: true,
			},
			URLs: urlsInfo{
				Stable: urlInfo{
					Checksum: "abc123def456",
				},
			},
			VersionedFormulae: []string{"python@3.11", "python@3.10", "python@3.9"},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	versions, err := reg.FetchVersions(context.Background(), "python")
	if err != nil {
		t.Fatalf("FetchVersions failed: %v", err)
	}

	if len(versions) != 4 {
		t.Fatalf("expected 4 versions (stable + 3 versioned), got %d", len(versions))
	}

	if versions[0].Number != "3.12.1" {
		t.Errorf("expected first version '3.12.1', got %q", versions[0].Number)
	}
	if versions[0].Integrity != "sha256-abc123def456" {
		t.Errorf("unexpected integrity: %q", versions[0].Integrity)
	}
}

func TestFetchDependencies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := formulaResponse{
			Name:              "imagemagick",
			Dependencies:      []string{"libtool", "pkg-config", "jpeg"},
			BuildDependencies: []string{"cmake"},
			TestDependencies:  []string{"webp"},
			OptionalDependencies: []string{"ghostscript"},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	deps, err := reg.FetchDependencies(context.Background(), "imagemagick", "7.1.1")
	if err != nil {
		t.Fatalf("FetchDependencies failed: %v", err)
	}

	if len(deps) != 6 {
		t.Fatalf("expected 6 dependencies, got %d", len(deps))
	}

	scopeMap := make(map[string]core.Scope)
	for _, d := range deps {
		scopeMap[d.Name] = d.Scope
	}

	if scopeMap["libtool"] != core.Runtime {
		t.Errorf("expected runtime scope for libtool")
	}
	if scopeMap["cmake"] != core.Build {
		t.Errorf("expected build scope for cmake")
	}
	if scopeMap["webp"] != core.Test {
		t.Errorf("expected test scope for webp")
	}
	if scopeMap["ghostscript"] != core.Optional {
		t.Errorf("expected optional scope for ghostscript")
	}
}

func TestFetchMaintainers(t *testing.T) {
	reg := New("", nil)
	maintainers, err := reg.FetchMaintainers(context.Background(), "wget")
	if err != nil {
		t.Fatalf("FetchMaintainers failed: %v", err)
	}

	if len(maintainers) != 0 {
		t.Errorf("expected 0 maintainers, got %d", len(maintainers))
	}
}

func TestURLBuilder(t *testing.T) {
	reg := New("https://formulae.brew.sh", nil)
	urls := reg.URLs()

	tests := []struct {
		name     string
		fn       func() string
		expected string
	}{
		{"registry", func() string { return urls.Registry("wget", "1.21.4") }, "https://formulae.brew.sh/formula/wget"},
		{"documentation", func() string { return urls.Documentation("wget", "") }, "https://formulae.brew.sh/formula/wget"},
		{"purl", func() string { return urls.PURL("wget", "1.21.4") }, "pkg:brew/wget@1.21.4"},
		{"purl_no_version", func() string { return urls.PURL("wget", "") }, "pkg:brew/wget"},
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
	if reg.Ecosystem() != "brew" {
		t.Errorf("expected ecosystem 'brew', got %q", reg.Ecosystem())
	}
}
