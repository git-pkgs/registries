package rubygems

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
		if r.URL.Path != "/api/v1/gems/rails.json" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}

		resp := gemResponse{
			Name:          "rails",
			Info:          "Ruby on Rails is a full-stack web framework",
			Version:       "7.1.0",
			Downloads:     500000000,
			Licenses:      []string{"MIT"},
			HomepageURI:   "https://rubyonrails.org",
			SourceCodeURI: "https://github.com/rails/rails",
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	pkg, err := reg.FetchPackage(context.Background(), "rails")
	if err != nil {
		t.Fatalf("FetchPackage failed: %v", err)
	}

	if pkg.Name != "rails" {
		t.Errorf("expected name 'rails', got %q", pkg.Name)
	}
	if pkg.Repository != "https://github.com/rails/rails" {
		t.Errorf("unexpected repository: %q", pkg.Repository)
	}
	if pkg.Licenses != "MIT" {
		t.Errorf("unexpected licenses: %q", pkg.Licenses)
	}
}

func TestFetchVersions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/versions/nokogiri.json" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}

		resp := []versionResponse{
			{
				Number:    "1.13.6",
				Platform:  "ruby",
				CreatedAt: "2022-05-08T14:34:51.113Z",
				Licenses:  []string{"MIT"},
				SHA:       "b1512fdc0aba446e1ee30de3e0671518eb363e75fab53486e99e8891d44b8587",
			},
			{
				Number:    "1.13.6",
				Platform:  "x86_64-linux",
				CreatedAt: "2022-05-08T14:34:45.502Z",
				Licenses:  []string{"MIT"},
				SHA:       "3fa37b0c3b5744af45f9da3e4ae9cbd89480b35e12ae36b5e87a0452e0b38335",
			},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	versions, err := reg.FetchVersions(context.Background(), "nokogiri")
	if err != nil {
		t.Fatalf("FetchVersions failed: %v", err)
	}

	if len(versions) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(versions))
	}

	if versions[0].Number != "1.13.6" {
		t.Errorf("expected version '1.13.6', got %q", versions[0].Number)
	}
	if versions[1].Number != "1.13.6-x86_64-linux" {
		t.Errorf("expected version '1.13.6-x86_64-linux', got %q", versions[1].Number)
	}
	if versions[0].Integrity != "sha256-b1512fdc0aba446e1ee30de3e0671518eb363e75fab53486e99e8891d44b8587" {
		t.Errorf("unexpected integrity: %q", versions[0].Integrity)
	}
}

func TestFetchDependencies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/rubygems/rails/versions/7.1.0.json" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}

		resp := dependencyVersionResponse{
			Dependencies: dependenciesBlock{
				Runtime: []gemDep{
					{Name: "activesupport", Requirements: "= 7.1.0"},
					{Name: "actionpack", Requirements: "= 7.1.0"},
				},
				Development: []gemDep{
					{Name: "minitest", Requirements: "~> 5.15"},
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	deps, err := reg.FetchDependencies(context.Background(), "rails", "7.1.0")
	if err != nil {
		t.Fatalf("FetchDependencies failed: %v", err)
	}

	if len(deps) != 3 {
		t.Fatalf("expected 3 dependencies, got %d", len(deps))
	}

	runtimeCount := 0
	devCount := 0
	for _, d := range deps {
		switch d.Scope {
		case core.Runtime:
			runtimeCount++
		case core.Development:
			devCount++
		}
	}

	if runtimeCount != 2 {
		t.Errorf("expected 2 runtime deps, got %d", runtimeCount)
	}
	if devCount != 1 {
		t.Errorf("expected 1 dev dep, got %d", devCount)
	}
}

func TestFetchMaintainers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/gems/rails/owners.json" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}

		resp := []ownerResponse{
			{ID: 1, Handle: "dhh"},
			{ID: 2, Handle: "rafaelfranca"},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	maintainers, err := reg.FetchMaintainers(context.Background(), "rails")
	if err != nil {
		t.Fatalf("FetchMaintainers failed: %v", err)
	}

	if len(maintainers) != 2 {
		t.Fatalf("expected 2 maintainers, got %d", len(maintainers))
	}

	if maintainers[0].Login != "dhh" {
		t.Errorf("expected login 'dhh', got %q", maintainers[0].Login)
	}
}

func TestURLBuilder(t *testing.T) {
	reg := New("https://rubygems.org", nil)
	urls := reg.URLs()

	tests := []struct {
		name     string
		fn       func() string
		expected string
	}{
		{"registry", func() string { return urls.Registry("rails", "7.1.0") }, "https://rubygems.org/gems/rails/versions/7.1.0"},
		{"download", func() string { return urls.Download("rails", "7.1.0") }, "https://rubygems.org/downloads/rails-7.1.0.gem"},
		{"documentation", func() string { return urls.Documentation("rails", "7.1.0") }, "http://www.rubydoc.info/gems/rails/7.1.0"},
		{"purl", func() string { return urls.PURL("rails", "7.1.0") }, "pkg:gem/rails@7.1.0"},
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
	if reg.Ecosystem() != "gem" {
		t.Errorf("expected ecosystem 'gem', got %q", reg.Ecosystem())
	}
}
