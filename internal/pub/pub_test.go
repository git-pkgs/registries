package pub

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
		if r.URL.Path != "/api/packages/flutter" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}

		resp := packageResponse{
			Name: "flutter",
			Latest: versionInfo{
				Version: "3.0.0",
				Pubspec: pubspec{
					Name:        "flutter",
					Description: "A framework for building Flutter applications",
					Homepage:    "https://flutter.dev",
					Repository:  "https://github.com/flutter/flutter",
					License:     "BSD-3-Clause",
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	pkg, err := reg.FetchPackage(context.Background(), "flutter")
	if err != nil {
		t.Fatalf("FetchPackage failed: %v", err)
	}

	if pkg.Name != "flutter" {
		t.Errorf("expected name 'flutter', got %q", pkg.Name)
	}
	if pkg.Repository != "https://github.com/flutter/flutter" {
		t.Errorf("unexpected repository: %q", pkg.Repository)
	}
	if pkg.Licenses != "BSD-3-Clause" {
		t.Errorf("unexpected licenses: %q", pkg.Licenses)
	}
}

func TestFetchVersions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := packageResponse{
			Name: "provider",
			Versions: []versionInfo{
				{Version: "6.1.0", Pubspec: pubspec{License: "MIT"}},
				{Version: "6.0.0", Pubspec: pubspec{License: "MIT"}},
				{Version: "5.0.0", Pubspec: pubspec{License: "MIT"}},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	versions, err := reg.FetchVersions(context.Background(), "provider")
	if err != nil {
		t.Fatalf("FetchVersions failed: %v", err)
	}

	if len(versions) != 3 {
		t.Fatalf("expected 3 versions, got %d", len(versions))
	}

	if versions[0].Number != "6.1.0" {
		t.Errorf("expected version '6.1.0', got %q", versions[0].Number)
	}
	if versions[0].Licenses != "MIT" {
		t.Errorf("expected MIT license, got %q", versions[0].Licenses)
	}
}

func TestFetchDependencies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/packages/provider/versions/6.1.0" {
			w.WriteHeader(404)
			return
		}

		resp := versionInfo{
			Version: "6.1.0",
			Pubspec: pubspec{
				Name:    "provider",
				Version: "6.1.0",
				Dependencies: map[string]interface{}{
					"flutter":    ">=3.0.0",
					"collection": "^1.15.0",
				},
				DevDeps: map[string]interface{}{
					"flutter_test": map[string]interface{}{"sdk": "flutter"},
					"test":         "^1.16.0",
				},
			},
		}

		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	deps, err := reg.FetchDependencies(context.Background(), "provider", "6.1.0")
	if err != nil {
		t.Fatalf("FetchDependencies failed: %v", err)
	}

	if len(deps) != 4 {
		t.Fatalf("expected 4 dependencies, got %d", len(deps))
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
	if devCount != 2 {
		t.Errorf("expected 2 dev deps, got %d", devCount)
	}
}

func TestFetchDependenciesGit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := versionInfo{
			Version: "1.0.0",
			Pubspec: pubspec{
				Dependencies: map[string]interface{}{
					"some_pkg": map[string]interface{}{
						"git": "https://github.com/example/some_pkg.git",
					},
					"another_pkg": map[string]interface{}{
						"git": map[string]interface{}{
							"url": "https://github.com/example/another.git",
							"ref": "main",
						},
					},
					"local_pkg": map[string]interface{}{
						"path": "../local_pkg",
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	deps, err := reg.FetchDependencies(context.Background(), "test", "1.0.0")
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

	if reqMap["some_pkg"] != "git:https://github.com/example/some_pkg.git" {
		t.Errorf("unexpected git requirement: %q", reqMap["some_pkg"])
	}
	if reqMap["another_pkg"] != "git:https://github.com/example/another.git" {
		t.Errorf("unexpected git map requirement: %q", reqMap["another_pkg"])
	}
	if reqMap["local_pkg"] != "path:../local_pkg" {
		t.Errorf("unexpected path requirement: %q", reqMap["local_pkg"])
	}
}

func TestURLBuilder(t *testing.T) {
	reg := New("https://pub.dev", nil)
	urls := reg.URLs()

	tests := []struct {
		name     string
		fn       func() string
		expected string
	}{
		{"registry", func() string { return urls.Registry("provider", "6.1.0") }, "https://pub.dev/packages/provider/versions/6.1.0"},
		{"download", func() string { return urls.Download("provider", "6.1.0") }, "https://pub.dev/packages/provider/versions/6.1.0.tar.gz"},
		{"documentation", func() string { return urls.Documentation("provider", "6.1.0") }, "https://pub.dev/documentation/provider/6.1.0/"},
		{"purl", func() string { return urls.PURL("provider", "6.1.0") }, "pkg:pub/provider@6.1.0"},
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
	if reg.Ecosystem() != "pub" {
		t.Errorf("expected ecosystem 'pub', got %q", reg.Ecosystem())
	}
}
