package nuget

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
		if r.URL.Path != "/registration5-semver1/newtonsoft.json/index.json" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}

		resp := registrationResponse{
			Items: []registrationPage{
				{
					Items: []registrationLeaf{
						{
							CatalogEntry: catalogEntry{
								ID:               "Newtonsoft.Json",
								Version:          "13.0.3",
								Description:      "Json.NET is a popular high-performance JSON framework for .NET",
								ProjectURL:       "https://www.newtonsoft.com/json",
								LicenseExpression: "MIT",
								Listed:           true,
								Tags:             []string{"json"},
							},
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
	pkg, err := reg.FetchPackage(context.Background(), "Newtonsoft.Json")
	if err != nil {
		t.Fatalf("FetchPackage failed: %v", err)
	}

	if pkg.Name != "Newtonsoft.Json" {
		t.Errorf("expected name 'Newtonsoft.Json', got %q", pkg.Name)
	}
	if pkg.Licenses != "MIT" {
		t.Errorf("unexpected licenses: %q", pkg.Licenses)
	}
	if len(pkg.Keywords) != 1 || pkg.Keywords[0] != "json" {
		t.Errorf("unexpected keywords: %v", pkg.Keywords)
	}
}

func TestFetchPackageWithGitHubRepository(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := registrationResponse{
			Items: []registrationPage{
				{
					Items: []registrationLeaf{
						{
							CatalogEntry: catalogEntry{
								ID:         "Serilog",
								Version:    "3.1.0",
								ProjectURL: "https://github.com/serilog/serilog",
								Listed:     true,
							},
						},
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	pkg, err := reg.FetchPackage(context.Background(), "Serilog")
	if err != nil {
		t.Fatalf("FetchPackage failed: %v", err)
	}

	if pkg.Repository != "https://github.com/serilog/serilog" {
		t.Errorf("expected repository from GitHub URL, got %q", pkg.Repository)
	}
}

func TestFetchVersions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := registrationResponse{
			Items: []registrationPage{
				{
					Items: []registrationLeaf{
						{
							CatalogEntry: catalogEntry{
								ID:        "xunit",
								Version:   "2.6.0",
								Published: "2023-10-15T12:00:00Z",
								Listed:    true,
							},
						},
						{
							CatalogEntry: catalogEntry{
								ID:        "xunit",
								Version:   "2.5.0",
								Published: "2023-07-01T12:00:00Z",
								Listed:    false,
							},
						},
						{
							CatalogEntry: catalogEntry{
								ID:        "xunit",
								Version:   "2.4.0",
								Published: "2023-01-01T12:00:00Z",
								Listed:    true,
								Deprecation: &deprecationInfo{
									Message: "Use newer version",
									Reasons: []string{"Legacy"},
								},
							},
						},
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	versions, err := reg.FetchVersions(context.Background(), "xunit")
	if err != nil {
		t.Fatalf("FetchVersions failed: %v", err)
	}

	if len(versions) != 3 {
		t.Fatalf("expected 3 versions, got %d", len(versions))
	}

	// Check statuses
	statusMap := make(map[string]core.VersionStatus)
	for _, v := range versions {
		statusMap[v.Number] = v.Status
	}

	if statusMap["2.6.0"] != core.StatusNone {
		t.Errorf("expected no status for 2.6.0, got %q", statusMap["2.6.0"])
	}
	if statusMap["2.5.0"] != core.StatusYanked {
		t.Errorf("expected yanked status for 2.5.0, got %q", statusMap["2.5.0"])
	}
	if statusMap["2.4.0"] != core.StatusDeprecated {
		t.Errorf("expected deprecated status for 2.4.0, got %q", statusMap["2.4.0"])
	}
}

func TestFetchDependencies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := registrationResponse{
			Items: []registrationPage{
				{
					Items: []registrationLeaf{
						{
							CatalogEntry: catalogEntry{
								ID:      "Microsoft.Extensions.Logging",
								Version: "8.0.0",
								Dependencies: []dependencyGroup{
									{
										TargetFramework: "net8.0",
										Dependencies: []dependency{
											{ID: "Microsoft.Extensions.DependencyInjection.Abstractions", Range: "[8.0.0, )"},
											{ID: "Microsoft.Extensions.Options", Range: "[8.0.0, )"},
										},
									},
									{
										TargetFramework: "net6.0",
										Dependencies: []dependency{
											{ID: "Microsoft.Extensions.DependencyInjection.Abstractions", Range: "[6.0.0, )"},
										},
									},
								},
							},
						},
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	deps, err := reg.FetchDependencies(context.Background(), "Microsoft.Extensions.Logging", "8.0.0")
	if err != nil {
		t.Fatalf("FetchDependencies failed: %v", err)
	}

	// Should have 2 unique dependencies (deduplicated across frameworks)
	if len(deps) != 2 {
		t.Fatalf("expected 2 dependencies, got %d", len(deps))
	}

	for _, d := range deps {
		if d.Scope != core.Runtime {
			t.Errorf("expected runtime scope, got %q", d.Scope)
		}
	}
}

func TestFetchMaintainers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := registrationResponse{
			Items: []registrationPage{
				{
					Items: []registrationLeaf{
						{
							CatalogEntry: catalogEntry{
								ID:      "Moq",
								Version: "4.20.0",
								Authors: "Daniel Cazzulino, kzu",
							},
						},
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	maintainers, err := reg.FetchMaintainers(context.Background(), "Moq")
	if err != nil {
		t.Fatalf("FetchMaintainers failed: %v", err)
	}

	if len(maintainers) != 2 {
		t.Fatalf("expected 2 maintainers, got %d", len(maintainers))
	}

	if maintainers[0].Name != "Daniel Cazzulino" {
		t.Errorf("expected first maintainer 'Daniel Cazzulino', got %q", maintainers[0].Name)
	}
}

func TestURLBuilder(t *testing.T) {
	reg := New("https://api.nuget.org/v3", nil)
	urls := reg.URLs()

	tests := []struct {
		name     string
		fn       func() string
		expected string
	}{
		{"registry", func() string { return urls.Registry("Newtonsoft.Json", "13.0.3") }, "https://www.nuget.org/packages/Newtonsoft.Json/13.0.3"},
		{"download", func() string { return urls.Download("Newtonsoft.Json", "13.0.3") }, "https://api.nuget.org/v3-flatcontainer/newtonsoft.json/13.0.3/newtonsoft.json.13.0.3.nupkg"},
		{"purl", func() string { return urls.PURL("Newtonsoft.Json", "13.0.3") }, "pkg:nuget/Newtonsoft.Json@13.0.3"},
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
	if reg.Ecosystem() != "nuget" {
		t.Errorf("expected ecosystem 'nuget', got %q", reg.Ecosystem())
	}
}
