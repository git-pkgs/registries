package cpan

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
		if r.URL.Path != "/v1/module/Moose" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}

		resp := distributionResponse{
			Name:     "Moose",
			Abstract: "A postmodern object system for Perl 5",
			Version:  "2.2201",
			License:  []string{"perl_5"},
			Author:   "ETHER",
			Resources: struct {
				Homepage   string `json:"homepage"`
				Repository struct {
					URL  string `json:"url"`
					Web  string `json:"web"`
					Type string `json:"type"`
				} `json:"repository"`
				Bugtracker struct {
					Web string `json:"web"`
				} `json:"bugtracker"`
			}{
				Homepage: "https://metacpan.org/release/Moose",
				Repository: struct {
					URL  string `json:"url"`
					Web  string `json:"web"`
					Type string `json:"type"`
				}{
					Web: "https://github.com/moose/Moose",
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	pkg, err := reg.FetchPackage(context.Background(), "Moose")
	if err != nil {
		t.Fatalf("FetchPackage failed: %v", err)
	}

	if pkg.Name != "Moose" {
		t.Errorf("expected name 'Moose', got %q", pkg.Name)
	}
	if pkg.Description != "A postmodern object system for Perl 5" {
		t.Errorf("unexpected description: %q", pkg.Description)
	}
	if pkg.Repository != "https://github.com/moose/Moose" {
		t.Errorf("unexpected repository: %q", pkg.Repository)
	}
	if pkg.Licenses != "perl_5" {
		t.Errorf("unexpected licenses: %q", pkg.Licenses)
	}
}

func TestFetchVersions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := releaseSearchResponse{
			Hits: struct {
				Hits []struct {
					Source releaseInfo `json:"_source"`
				} `json:"hits"`
			}{
				Hits: []struct {
					Source releaseInfo `json:"_source"`
				}{
					{Source: releaseInfo{Version: "2.2201", Date: "2023-10-15T12:00:00Z", License: []string{"perl_5"}, Checksum: "abc123"}},
					{Source: releaseInfo{Version: "2.2200", Date: "2023-08-01T12:00:00Z", License: []string{"perl_5"}, Status: "backpan"}},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	versions, err := reg.FetchVersions(context.Background(), "Moose")
	if err != nil {
		t.Fatalf("FetchVersions failed: %v", err)
	}

	if len(versions) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(versions))
	}

	if versions[0].Number != "2.2201" {
		t.Errorf("expected version '2.2201', got %q", versions[0].Number)
	}
	if versions[0].Status != core.StatusNone {
		t.Errorf("expected no status for first version, got %q", versions[0].Status)
	}
	if versions[0].Integrity != "sha256-abc123" {
		t.Errorf("unexpected integrity: %q", versions[0].Integrity)
	}

	if versions[1].Status != core.StatusYanked {
		t.Errorf("expected yanked status for backpan version, got %q", versions[1].Status)
	}
}

func TestFetchDependencies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/release/Moose-2.2201" {
			w.WriteHeader(404)
			return
		}

		resp := distributionResponse{
			Name:    "Moose",
			Version: "2.2201",
			Dependency: []dependencyInfo{
				{Module: "perl", Version: "5.008003", Phase: "runtime", Relationship: "requires"},
				{Module: "Carp", Version: "1.22", Phase: "runtime", Relationship: "requires"},
				{Module: "Class::Load", Version: "0.09", Phase: "runtime", Relationship: "requires"},
				{Module: "Test::More", Version: "0.88", Phase: "test", Relationship: "requires"},
				{Module: "Test::Fatal", Version: "0.001", Phase: "test", Relationship: "recommends"},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	deps, err := reg.FetchDependencies(context.Background(), "Moose", "2.2201")
	if err != nil {
		t.Fatalf("FetchDependencies failed: %v", err)
	}

	// Should have 4 deps (perl is filtered out)
	if len(deps) != 4 {
		t.Fatalf("expected 4 dependencies, got %d", len(deps))
	}

	scopeMap := make(map[string]core.Scope)
	optMap := make(map[string]bool)
	for _, d := range deps {
		scopeMap[d.Name] = d.Scope
		optMap[d.Name] = d.Optional
	}

	if scopeMap["Carp"] != core.Runtime {
		t.Errorf("expected runtime scope for Carp, got %q", scopeMap["Carp"])
	}
	if scopeMap["Test::More"] != core.Test {
		t.Errorf("expected test scope for Test::More, got %q", scopeMap["Test::More"])
	}
	if scopeMap["Test::Fatal"] != core.Optional {
		t.Errorf("expected optional scope for Test::Fatal, got %q", scopeMap["Test::Fatal"])
	}
	if !optMap["Test::Fatal"] {
		t.Error("expected Test::Fatal to be optional")
	}
}

func TestFetchMaintainers(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("/v1/module/Moose", func(w http.ResponseWriter, r *http.Request) {
		resp := distributionResponse{
			Name:   "Moose",
			Author: "ETHER",
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/v1/author/ETHER", func(w http.ResponseWriter, r *http.Request) {
		resp := authorResponse{
			PAUSEID: "ETHER",
			Name:    "Karen Etheridge",
			Email:   []string{"ether@cpan.org"},
			Website: []string{"https://metacpan.org/author/ETHER"},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	maintainers, err := reg.FetchMaintainers(context.Background(), "Moose")
	if err != nil {
		t.Fatalf("FetchMaintainers failed: %v", err)
	}

	if len(maintainers) != 1 {
		t.Fatalf("expected 1 maintainer, got %d", len(maintainers))
	}

	if maintainers[0].Login != "ETHER" {
		t.Errorf("expected login 'ETHER', got %q", maintainers[0].Login)
	}
	if maintainers[0].Name != "Karen Etheridge" {
		t.Errorf("expected name 'Karen Etheridge', got %q", maintainers[0].Name)
	}
	if maintainers[0].Email != "ether@cpan.org" {
		t.Errorf("unexpected email: %q", maintainers[0].Email)
	}
}

func TestURLBuilder(t *testing.T) {
	reg := New("https://fastapi.metacpan.org", nil)
	urls := reg.URLs()

	tests := []struct {
		name     string
		fn       func() string
		expected string
	}{
		{"registry_no_version", func() string { return urls.Registry("Moose", "") }, "https://metacpan.org/dist/Moose"},
		{"documentation", func() string { return urls.Documentation("Moose", "2.2201") }, "https://metacpan.org/pod/release/Moose-2.2201/Moose"},
		{"documentation_module", func() string { return urls.Documentation("DBIx::Class", "") }, "https://metacpan.org/pod/DBIx::Class"},
		{"purl", func() string { return urls.PURL("Moose", "2.2201") }, "pkg:cpan/Moose@2.2201"},
		{"purl_with_colons", func() string { return urls.PURL("DBIx::Class", "0.08") }, "pkg:cpan/DBIx-Class@0.08"},
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
	if reg.Ecosystem() != "cpan" {
		t.Errorf("expected ecosystem 'cpan', got %q", reg.Ecosystem())
	}
}
