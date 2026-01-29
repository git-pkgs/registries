package luarocks

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
		if r.URL.Path != "/api/1/luasocket" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}

		resp := moduleResponse{
			Name:        "luasocket",
			Description: "Network support for the Lua language",
			Homepage:    "https://github.com/lunarmodules/luasocket",
			License:     "MIT",
			Labels:      []string{"networking", "socket"},
			Versions: map[string][]rockVersion{
				"3.1.0-1": {},
				"3.0.0-1": {},
			},
			Maintainers: []maintainerInfo{{Name: "hisham"}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	pkg, err := reg.FetchPackage(context.Background(), "luasocket")
	if err != nil {
		t.Fatalf("FetchPackage failed: %v", err)
	}

	if pkg.Name != "luasocket" {
		t.Errorf("expected name 'luasocket', got %q", pkg.Name)
	}
	if pkg.Description != "Network support for the Lua language" {
		t.Errorf("unexpected description: %q", pkg.Description)
	}
	if pkg.Licenses != "MIT" {
		t.Errorf("unexpected license: %q", pkg.Licenses)
	}
	if len(pkg.Keywords) != 2 {
		t.Errorf("expected 2 keywords, got %d", len(pkg.Keywords))
	}
}

func TestFetchVersions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := moduleResponse{
			Name:    "lpeg",
			License: "MIT",
			Versions: map[string][]rockVersion{
				"1.0.2-1": {},
				"1.0.1-1": {},
				"1.0.0-1": {},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	versions, err := reg.FetchVersions(context.Background(), "lpeg")
	if err != nil {
		t.Fatalf("FetchVersions failed: %v", err)
	}

	if len(versions) != 3 {
		t.Fatalf("expected 3 versions, got %d", len(versions))
	}

	if versions[0].Licenses != "MIT" {
		t.Errorf("unexpected license: %q", versions[0].Licenses)
	}
}

func TestFetchDependencies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/1/penlight/1.13.1-1" {
			w.WriteHeader(404)
			return
		}

		resp := rockspec{
			Package: "penlight",
			Version: "1.13.1-1",
			Dependencies: []string{
				"lua >= 5.1",
				"luafilesystem",
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	deps, err := reg.FetchDependencies(context.Background(), "penlight", "1.13.1-1")
	if err != nil {
		t.Fatalf("FetchDependencies failed: %v", err)
	}

	// "lua" should be filtered out
	if len(deps) != 1 {
		t.Fatalf("expected 1 dependency (lua filtered), got %d", len(deps))
	}

	if deps[0].Name != "luafilesystem" {
		t.Errorf("expected dependency 'luafilesystem', got %q", deps[0].Name)
	}
}

func TestFetchMaintainers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := moduleResponse{
			Name: "luasec",
			Maintainers: []maintainerInfo{
				{Name: "brunoos"},
				{Name: "hisham"},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	maintainers, err := reg.FetchMaintainers(context.Background(), "luasec")
	if err != nil {
		t.Fatalf("FetchMaintainers failed: %v", err)
	}

	if len(maintainers) != 2 {
		t.Fatalf("expected 2 maintainers, got %d", len(maintainers))
	}

	if maintainers[0].Login != "brunoos" {
		t.Errorf("expected first maintainer 'brunoos', got %q", maintainers[0].Login)
	}
}

func TestParseDependency(t *testing.T) {
	tests := []struct {
		input string
		name  string
		req   string
	}{
		{"lua >= 5.1", "lua", ">= 5.1"},
		{"lpeg", "lpeg", ""},
		{"luasocket >= 3.0", "luasocket", ">= 3.0"},
		{"luafilesystem >= 1.5, < 2", "luafilesystem", ">= 1.5, < 2"},
	}

	for _, tt := range tests {
		name, req := parseDependency(tt.input)
		if name != tt.name || req != tt.req {
			t.Errorf("parseDependency(%q) = (%q, %q), want (%q, %q)",
				tt.input, name, req, tt.name, tt.req)
		}
	}
}

func TestURLBuilder(t *testing.T) {
	reg := New("https://luarocks.org", nil)
	urls := reg.URLs()

	tests := []struct {
		name     string
		fn       func() string
		expected string
	}{
		{"registry", func() string { return urls.Registry("luasocket", "3.1.0-1") }, "https://luarocks.org/modules/luasocket/3.1.0-1"},
		{"registry_no_version", func() string { return urls.Registry("luasocket", "") }, "https://luarocks.org/modules/luasocket"},
		{"purl", func() string { return urls.PURL("luasocket", "3.1.0-1") }, "pkg:luarocks/luasocket@3.1.0-1"},
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
	if reg.Ecosystem() != "luarocks" {
		t.Errorf("expected ecosystem 'luarocks', got %q", reg.Ecosystem())
	}
}
