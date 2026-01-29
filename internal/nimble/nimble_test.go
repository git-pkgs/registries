package nimble

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
		if r.URL.Path != "/api/packages/chronicles" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}

		resp := packageDetailResponse{
			Name:        "chronicles",
			URL:         "https://github.com/status-im/nim-chronicles",
			Method:      "git",
			Tags:        []string{"logging", "debug"},
			Description: "A crafty implementation of structured logging",
			License:     "Apache-2.0",
			Web:         "https://status-im.github.io/nim-chronicles",
			Versions: []versionDetail{
				{Version: "0.10.3"},
				{Version: "0.10.2"},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	pkg, err := reg.FetchPackage(context.Background(), "chronicles")
	if err != nil {
		t.Fatalf("FetchPackage failed: %v", err)
	}

	if pkg.Name != "chronicles" {
		t.Errorf("expected name 'chronicles', got %q", pkg.Name)
	}
	if pkg.Description != "A crafty implementation of structured logging" {
		t.Errorf("unexpected description: %q", pkg.Description)
	}
	if pkg.Licenses != "Apache-2.0" {
		t.Errorf("unexpected license: %q", pkg.Licenses)
	}
	if pkg.Repository != "https://github.com/status-im/nim-chronicles" {
		t.Errorf("unexpected repository: %q", pkg.Repository)
	}
	if pkg.Homepage != "https://status-im.github.io/nim-chronicles" {
		t.Errorf("unexpected homepage: %q", pkg.Homepage)
	}
	if len(pkg.Keywords) != 2 {
		t.Errorf("expected 2 keywords, got %d", len(pkg.Keywords))
	}
}

func TestFetchVersions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := packageDetailResponse{
			Name:    "stew",
			License: "Apache-2.0",
			Versions: []versionDetail{
				{Version: "0.1.0"},
				{Version: "0.1.1"},
				{Version: "0.1.2"},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	versions, err := reg.FetchVersions(context.Background(), "stew")
	if err != nil {
		t.Fatalf("FetchVersions failed: %v", err)
	}

	if len(versions) != 3 {
		t.Fatalf("expected 3 versions, got %d", len(versions))
	}

	// Should be reversed (newest first)
	if versions[0].Number != "0.1.2" {
		t.Errorf("expected first version '0.1.2', got %q", versions[0].Number)
	}
	if versions[0].Licenses != "Apache-2.0" {
		t.Errorf("unexpected license: %q", versions[0].Licenses)
	}
}

func TestFetchDependencies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := packageDetailResponse{
			Name: "chronicles",
			Versions: []versionDetail{
				{
					Version: "0.10.3",
					Requires: []string{
						"nim >= 1.2.0",
						"stew",
						"json_serialization >= 0.1.0",
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	deps, err := reg.FetchDependencies(context.Background(), "chronicles", "0.10.3")
	if err != nil {
		t.Fatalf("FetchDependencies failed: %v", err)
	}

	// "nim" should be filtered out
	if len(deps) != 2 {
		t.Fatalf("expected 2 dependencies (nim filtered), got %d", len(deps))
	}

	reqMap := make(map[string]string)
	for _, d := range deps {
		reqMap[d.Name] = d.Requirements
	}

	if reqMap["stew"] != "" {
		t.Errorf("expected no requirement for stew, got %q", reqMap["stew"])
	}
	if reqMap["json_serialization"] != ">= 0.1.0" {
		t.Errorf("unexpected json_serialization requirement: %q", reqMap["json_serialization"])
	}
}

func TestFetchMaintainers(t *testing.T) {
	reg := New("", nil)
	maintainers, err := reg.FetchMaintainers(context.Background(), "chronicles")
	if err != nil {
		t.Fatalf("FetchMaintainers failed: %v", err)
	}

	// Nimble doesn't expose maintainer info
	if len(maintainers) != 0 {
		t.Errorf("expected 0 maintainers, got %d", len(maintainers))
	}
}

func TestParseDependency(t *testing.T) {
	tests := []struct {
		input string
		name  string
		req   string
	}{
		{"nim >= 1.2.0", "nim", ">= 1.2.0"},
		{"stew", "stew", ""},
		{"chronicles >= 0.10.0", "chronicles", ">= 0.10.0"},
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
	reg := New("https://nimble.directory", nil)
	urls := reg.URLs()

	tests := []struct {
		name     string
		fn       func() string
		expected string
	}{
		{"registry", func() string { return urls.Registry("chronicles", "0.10.3") }, "https://nimble.directory/pkg/chronicles/0.10.3"},
		{"registry_no_version", func() string { return urls.Registry("chronicles", "") }, "https://nimble.directory/pkg/chronicles"},
		{"purl", func() string { return urls.PURL("chronicles", "0.10.3") }, "pkg:nimble/chronicles@0.10.3"},
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
	if reg.Ecosystem() != "nimble" {
		t.Errorf("expected ecosystem 'nimble', got %q", reg.Ecosystem())
	}
}
