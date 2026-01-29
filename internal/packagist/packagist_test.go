package packagist

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
		if r.URL.Path != "/packages/laravel/framework.json" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}

		resp := packageResponse{
			Package: packageInfo{
				Name:        "laravel/framework",
				Description: "The Laravel Framework",
				Repository:  "https://github.com/laravel/framework.git",
				Versions: map[string]versionInfo{
					"v11.0.0": {
						Version:  "v11.0.0",
						Homepage: "https://laravel.com",
						License:  []string{"MIT"},
						Source: sourceInfo{
							URL: "https://github.com/laravel/framework.git",
						},
					},
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	pkg, err := reg.FetchPackage(context.Background(), "laravel/framework")
	if err != nil {
		t.Fatalf("FetchPackage failed: %v", err)
	}

	if pkg.Name != "laravel/framework" {
		t.Errorf("expected name 'laravel/framework', got %q", pkg.Name)
	}
	if pkg.Namespace != "laravel" {
		t.Errorf("expected namespace 'laravel', got %q", pkg.Namespace)
	}
	if pkg.Repository != "https://github.com/laravel/framework" {
		t.Errorf("unexpected repository: %q", pkg.Repository)
	}
	if pkg.Licenses != "MIT" {
		t.Errorf("unexpected licenses: %q", pkg.Licenses)
	}
}

func TestFetchVersions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := packageResponse{
			Package: packageInfo{
				Name: "monolog/monolog",
				Versions: map[string]versionInfo{
					"3.5.0": {
						Version: "3.5.0",
						Time:    "2024-01-15T12:00:00+00:00",
						License: []string{"MIT"},
						Dist: distInfo{
							Shasum: "abc123",
						},
					},
					"3.4.0": {
						Version: "3.4.0",
						Time:    "2023-12-01T12:00:00+00:00",
						License: []string{"MIT"},
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	versions, err := reg.FetchVersions(context.Background(), "monolog/monolog")
	if err != nil {
		t.Fatalf("FetchVersions failed: %v", err)
	}

	if len(versions) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(versions))
	}

	// Check that at least one version has integrity
	hasIntegrity := false
	for _, v := range versions {
		if v.Integrity != "" {
			hasIntegrity = true
			if v.Integrity != "sha1-abc123" {
				t.Errorf("unexpected integrity: %q", v.Integrity)
			}
		}
	}
	if !hasIntegrity {
		t.Error("expected at least one version with integrity")
	}
}

func TestFetchDependencies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := packageResponse{
			Package: packageInfo{
				Name: "symfony/console",
				Versions: map[string]versionInfo{
					"v7.0.0": {
						Version: "v7.0.0",
						Require: map[string]string{
							"php":                      ">=8.2",
							"symfony/polyfill-mbstring": "~1.0",
							"symfony/string":           "^6.4|^7.0",
						},
						RequireDev: map[string]string{
							"phpunit/phpunit": "^10.5",
							"symfony/process": "^6.4|^7.0",
						},
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	deps, err := reg.FetchDependencies(context.Background(), "symfony/console", "v7.0.0")
	if err != nil {
		t.Fatalf("FetchDependencies failed: %v", err)
	}

	// Should have 4 deps (2 runtime excluding php, 2 dev)
	if len(deps) != 4 {
		t.Fatalf("expected 4 dependencies, got %d", len(deps))
	}

	runtimeCount := 0
	devCount := 0
	for _, d := range deps {
		// php should be filtered out
		if d.Name == "php" {
			t.Error("php should be filtered from dependencies")
		}
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

func TestFetchMaintainers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := packageResponse{
			Package: packageInfo{
				Name: "guzzlehttp/guzzle",
				Maintainers: []maintainerInfo{
					{Name: "mtdowling", AvatarURL: "https://example.com/mtdowling.png"},
					{Name: "GrahamCampbell", AvatarURL: "https://example.com/graham.png"},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	maintainers, err := reg.FetchMaintainers(context.Background(), "guzzlehttp/guzzle")
	if err != nil {
		t.Fatalf("FetchMaintainers failed: %v", err)
	}

	if len(maintainers) != 2 {
		t.Fatalf("expected 2 maintainers, got %d", len(maintainers))
	}

	if maintainers[0].Login != "mtdowling" {
		t.Errorf("expected login 'mtdowling', got %q", maintainers[0].Login)
	}
}

func TestURLBuilder(t *testing.T) {
	reg := New("https://packagist.org", nil)
	urls := reg.URLs()

	tests := []struct {
		name     string
		fn       func() string
		expected string
	}{
		{"registry", func() string { return urls.Registry("laravel/framework", "11.0.0") }, "https://packagist.org/packages/laravel/framework#11.0.0"},
		{"registry_no_version", func() string { return urls.Registry("laravel/framework", "") }, "https://packagist.org/packages/laravel/framework"},
		{"purl", func() string { return urls.PURL("laravel/framework", "11.0.0") }, "pkg:composer/laravel/framework@11.0.0"},
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
	if reg.Ecosystem() != "composer" {
		t.Errorf("expected ecosystem 'composer', got %q", reg.Ecosystem())
	}
}
