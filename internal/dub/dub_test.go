package dub

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
		if r.URL.Path != "/api/packages/vibe-d" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}

		resp := packageResponse{
			Name:        "vibe-d",
			Description: "High-performance asynchronous I/O and web framework",
			Homepage:    "https://vibed.org",
			Repository:  "https://github.com/libmir/vibe-d",
			Categories:  []string{"networking", "web"},
			Owner:       "s-ludwig",
			Versions: []versionInfo{
				{Version: "0.9.5", Date: "2023-06-15T10:30:00Z", License: "BSL-1.0"},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	pkg, err := reg.FetchPackage(context.Background(), "vibe-d")
	if err != nil {
		t.Fatalf("FetchPackage failed: %v", err)
	}

	if pkg.Name != "vibe-d" {
		t.Errorf("expected name 'vibe-d', got %q", pkg.Name)
	}
	if pkg.Description != "High-performance asynchronous I/O and web framework" {
		t.Errorf("unexpected description: %q", pkg.Description)
	}
	if pkg.Licenses != "BSL-1.0" {
		t.Errorf("unexpected license: %q", pkg.Licenses)
	}
	if pkg.Repository != "https://github.com/libmir/vibe-d" {
		t.Errorf("unexpected repository: %q", pkg.Repository)
	}
	if len(pkg.Keywords) != 2 {
		t.Errorf("expected 2 keywords, got %d", len(pkg.Keywords))
	}
}

func TestFetchVersions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := packageResponse{
			Name: "mir-algorithm",
			Versions: []versionInfo{
				{Version: "3.3.14", Date: "2023-08-01T12:00:00Z", License: "BSL-1.0"},
				{Version: "3.3.13", Date: "2023-07-15T12:00:00Z", License: "BSL-1.0"},
				{Version: "3.3.12", Date: "2023-07-01T12:00:00Z", License: "BSL-1.0"},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	versions, err := reg.FetchVersions(context.Background(), "mir-algorithm")
	if err != nil {
		t.Fatalf("FetchVersions failed: %v", err)
	}

	if len(versions) != 3 {
		t.Fatalf("expected 3 versions, got %d", len(versions))
	}

	if versions[0].Number != "3.3.14" {
		t.Errorf("expected first version '3.3.14', got %q", versions[0].Number)
	}
	if versions[0].Licenses != "BSL-1.0" {
		t.Errorf("unexpected license: %q", versions[0].Licenses)
	}
	if versions[0].PublishedAt.IsZero() {
		t.Error("expected non-zero published time")
	}
}

func TestFetchDependencies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := packageResponse{
			Name: "test-pkg",
			Versions: []versionInfo{
				{
					Version: "1.0.0",
					Dependencies: map[string]interface{}{
						"mir-algorithm": "~>3.3.0",
						"taggedalgebraic": map[string]interface{}{
							"version": "~>0.6.0",
							"optional": true,
						},
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	deps, err := reg.FetchDependencies(context.Background(), "test-pkg", "1.0.0")
	if err != nil {
		t.Fatalf("FetchDependencies failed: %v", err)
	}

	if len(deps) != 2 {
		t.Fatalf("expected 2 dependencies, got %d", len(deps))
	}

	reqMap := make(map[string]string)
	for _, d := range deps {
		reqMap[d.Name] = d.Requirements
	}

	if reqMap["mir-algorithm"] != "~>3.3.0" {
		t.Errorf("unexpected mir-algorithm requirement: %q", reqMap["mir-algorithm"])
	}
	if reqMap["taggedalgebraic"] != "~>0.6.0" {
		t.Errorf("unexpected taggedalgebraic requirement: %q", reqMap["taggedalgebraic"])
	}
}

func TestFetchMaintainers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := packageResponse{
			Name:  "test-pkg",
			Owner: "test-owner",
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	maintainers, err := reg.FetchMaintainers(context.Background(), "test-pkg")
	if err != nil {
		t.Fatalf("FetchMaintainers failed: %v", err)
	}

	if len(maintainers) != 1 {
		t.Fatalf("expected 1 maintainer, got %d", len(maintainers))
	}

	if maintainers[0].Login != "test-owner" {
		t.Errorf("expected login 'test-owner', got %q", maintainers[0].Login)
	}
}

func TestURLBuilder(t *testing.T) {
	reg := New("https://code.dlang.org", nil)
	urls := reg.URLs()

	tests := []struct {
		name     string
		fn       func() string
		expected string
	}{
		{"registry", func() string { return urls.Registry("vibe-d", "0.9.5") }, "https://code.dlang.org/packages/vibe-d/0.9.5"},
		{"registry_no_version", func() string { return urls.Registry("vibe-d", "") }, "https://code.dlang.org/packages/vibe-d"},
		{"download", func() string { return urls.Download("vibe-d", "0.9.5") }, "https://code.dlang.org/packages/vibe-d/0.9.5.zip"},
		{"purl", func() string { return urls.PURL("vibe-d", "0.9.5") }, "pkg:dub/vibe-d@0.9.5"},
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
	if reg.Ecosystem() != "dub" {
		t.Errorf("expected ecosystem 'dub', got %q", reg.Ecosystem())
	}
}
