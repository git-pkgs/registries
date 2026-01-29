package haxelib

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
		if r.URL.Path != "/api/3.0/package-info/openfl" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}

		resp := packageResponse{
			Name:        "openfl",
			Description: "Open Flash Library",
			Website:     "https://github.com/openfl/openfl",
			License:     "MIT",
			Tags:        []string{"graphics", "game"},
			Owner:       "jdonaldson",
			Contributors: []string{"player-03", "Aurel300"},
			Downloads:   50000,
			Versions: []versionInfo{
				{Version: "9.2.0"},
				{Version: "9.1.0"},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	pkg, err := reg.FetchPackage(context.Background(), "openfl")
	if err != nil {
		t.Fatalf("FetchPackage failed: %v", err)
	}

	if pkg.Name != "openfl" {
		t.Errorf("expected name 'openfl', got %q", pkg.Name)
	}
	if pkg.Description != "Open Flash Library" {
		t.Errorf("unexpected description: %q", pkg.Description)
	}
	if pkg.Licenses != "MIT" {
		t.Errorf("unexpected license: %q", pkg.Licenses)
	}
	if pkg.Repository != "https://github.com/openfl/openfl" {
		t.Errorf("unexpected repository: %q", pkg.Repository)
	}
	if len(pkg.Keywords) != 2 {
		t.Errorf("expected 2 keywords, got %d", len(pkg.Keywords))
	}
}

func TestFetchVersions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := packageResponse{
			Name:    "lime",
			License: "MIT",
			Versions: []versionInfo{
				{Version: "8.0.0"},
				{Version: "8.0.1"},
				{Version: "8.0.2"},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	versions, err := reg.FetchVersions(context.Background(), "lime")
	if err != nil {
		t.Fatalf("FetchVersions failed: %v", err)
	}

	if len(versions) != 3 {
		t.Fatalf("expected 3 versions, got %d", len(versions))
	}

	// Should be reversed (newest first)
	if versions[0].Number != "8.0.2" {
		t.Errorf("expected first version '8.0.2', got %q", versions[0].Number)
	}
	if versions[0].Licenses != "MIT" {
		t.Errorf("unexpected license: %q", versions[0].Licenses)
	}
}

func TestFetchDependencies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := packageResponse{
			Name: "openfl",
			Versions: []versionInfo{
				{
					Version: "9.2.0",
					Dependencies: map[string]string{
						"lime":   "8.0.2",
						"hxcpp":  "",
						"format": "3.4.0",
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	deps, err := reg.FetchDependencies(context.Background(), "openfl", "9.2.0")
	if err != nil {
		t.Fatalf("FetchDependencies failed: %v", err)
	}

	if len(deps) != 3 {
		t.Fatalf("expected 3 dependencies, got %d", len(deps))
	}

	reqMap := make(map[string]string)
	for _, d := range deps {
		reqMap[d.Name] = d.Requirements
	}

	if reqMap["lime"] != "8.0.2" {
		t.Errorf("unexpected lime requirement: %q", reqMap["lime"])
	}
	if reqMap["hxcpp"] != "" {
		t.Errorf("expected no requirement for hxcpp, got %q", reqMap["hxcpp"])
	}
}

func TestFetchMaintainers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := packageResponse{
			Name:         "openfl",
			Owner:        "jdonaldson",
			Contributors: []string{"player-03", "jdonaldson", "Aurel300"},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	maintainers, err := reg.FetchMaintainers(context.Background(), "openfl")
	if err != nil {
		t.Fatalf("FetchMaintainers failed: %v", err)
	}

	// Owner + 2 unique contributors (jdonaldson is both owner and contributor, deduplicated)
	if len(maintainers) != 3 {
		t.Fatalf("expected 3 maintainers, got %d", len(maintainers))
	}

	if maintainers[0].Login != "jdonaldson" {
		t.Errorf("expected first maintainer 'jdonaldson', got %q", maintainers[0].Login)
	}
	if maintainers[0].Role != "owner" {
		t.Errorf("expected role 'owner', got %q", maintainers[0].Role)
	}
}

func TestURLBuilder(t *testing.T) {
	reg := New("https://lib.haxe.org", nil)
	urls := reg.URLs()

	tests := []struct {
		name     string
		fn       func() string
		expected string
	}{
		{"registry", func() string { return urls.Registry("openfl", "9.2.0") }, "https://lib.haxe.org/p/openfl/9.2.0"},
		{"registry_no_version", func() string { return urls.Registry("openfl", "") }, "https://lib.haxe.org/p/openfl"},
		{"download", func() string { return urls.Download("openfl", "9.2.0") }, "https://lib.haxe.org/files/openfl-9.2.0.zip"},
		{"purl", func() string { return urls.PURL("openfl", "9.2.0") }, "pkg:haxelib/openfl@9.2.0"},
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
	if reg.Ecosystem() != "haxelib" {
		t.Errorf("expected ecosystem 'haxelib', got %q", reg.Ecosystem())
	}
}
