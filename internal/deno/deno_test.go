package deno

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
		if r.URL.Path != "/v2/modules/oak" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}

		resp := moduleInfoResponse{
			Name:          "oak",
			Description:   "A middleware framework for Deno's native HTTP server",
			LatestVersion: "12.6.1",
			Versions:      []string{"12.6.1", "12.6.0", "12.5.0"},
			UploadOptions: uploadOptions{
				Type:       "github",
				Repository: "oakserver/oak",
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	pkg, err := reg.FetchPackage(context.Background(), "oak")
	if err != nil {
		t.Fatalf("FetchPackage failed: %v", err)
	}

	if pkg.Name != "oak" {
		t.Errorf("expected name 'oak', got %q", pkg.Name)
	}
	if pkg.Description != "A middleware framework for Deno's native HTTP server" {
		t.Errorf("unexpected description: %q", pkg.Description)
	}
	if pkg.Repository != "https://github.com/oakserver/oak" {
		t.Errorf("unexpected repository: %q", pkg.Repository)
	}
	if pkg.Homepage != "https://deno.land/x/oak" {
		t.Errorf("unexpected homepage: %q", pkg.Homepage)
	}
}

func TestFetchVersions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := moduleInfoResponse{
			Name:     "std",
			Versions: []string{"0.210.0", "0.209.0", "0.208.0", "0.207.0"},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	versions, err := reg.FetchVersions(context.Background(), "std")
	if err != nil {
		t.Fatalf("FetchVersions failed: %v", err)
	}

	if len(versions) != 4 {
		t.Fatalf("expected 4 versions, got %d", len(versions))
	}

	if versions[0].Number != "0.210.0" {
		t.Errorf("expected first version '0.210.0', got %q", versions[0].Number)
	}
}

func TestFetchDependencies(t *testing.T) {
	reg := New("", core.DefaultClient())
	deps, err := reg.FetchDependencies(context.Background(), "oak", "12.6.1")
	if err != nil {
		t.Fatalf("FetchDependencies failed: %v", err)
	}

	// Deno doesn't expose dependencies via API
	if len(deps) != 0 {
		t.Errorf("expected 0 dependencies, got %d", len(deps))
	}
}

func TestFetchMaintainers(t *testing.T) {
	reg := New("", nil)
	maintainers, err := reg.FetchMaintainers(context.Background(), "oak")
	if err != nil {
		t.Fatalf("FetchMaintainers failed: %v", err)
	}

	if len(maintainers) != 0 {
		t.Errorf("expected 0 maintainers, got %d", len(maintainers))
	}
}

func TestURLBuilder(t *testing.T) {
	reg := New("https://apiland.deno.dev", nil)
	urls := reg.URLs()

	tests := []struct {
		name     string
		fn       func() string
		expected string
	}{
		{"registry", func() string { return urls.Registry("oak", "12.6.1") }, "https://deno.land/x/oak@12.6.1"},
		{"registry_no_version", func() string { return urls.Registry("oak", "") }, "https://deno.land/x/oak"},
		{"download", func() string { return urls.Download("oak", "12.6.1") }, "https://deno.land/x/oak@12.6.1/mod.ts"},
		{"documentation", func() string { return urls.Documentation("oak", "12.6.1") }, "https://deno.land/x/oak@12.6.1"},
		{"purl", func() string { return urls.PURL("oak", "12.6.1") }, "pkg:deno/oak@12.6.1"},
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
	if reg.Ecosystem() != "deno" {
		t.Errorf("expected ecosystem 'deno', got %q", reg.Ecosystem())
	}
}
