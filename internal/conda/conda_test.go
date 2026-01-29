package conda

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/git-pkgs/registries/internal/core"
)

func TestParsePackageName(t *testing.T) {
	tests := []struct {
		input   string
		channel string
		name    string
	}{
		{"numpy", "", "numpy"},
		{"conda-forge/numpy", "conda-forge", "numpy"},
		{"bioconda/samtools", "bioconda", "samtools"},
	}

	for _, tt := range tests {
		channel, name := parsePackageName(tt.input)
		if channel != tt.channel || name != tt.name {
			t.Errorf("parsePackageName(%q) = (%q, %q), want (%q, %q)",
				tt.input, channel, name, tt.channel, tt.name)
		}
	}
}

func TestFetchPackage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/package/conda-forge/numpy" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}

		resp := packageResponse{
			Name:          "numpy",
			Summary:       "Array processing for numbers, strings, records, and objects",
			License:       "BSD-3-Clause",
			HomeURL:       "https://www.numpy.org",
			DevURL:        "https://github.com/numpy/numpy",
			Owner:         "conda-forge",
			LatestVersion: "1.26.0",
			Versions:      []string{"1.26.0", "1.25.2", "1.24.0"},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	pkg, err := reg.FetchPackage(context.Background(), "numpy")
	if err != nil {
		t.Fatalf("FetchPackage failed: %v", err)
	}

	if pkg.Name != "numpy" {
		t.Errorf("expected name 'numpy', got %q", pkg.Name)
	}
	if pkg.Description != "Array processing for numbers, strings, records, and objects" {
		t.Errorf("unexpected description: %q", pkg.Description)
	}
	if pkg.Repository != "https://github.com/numpy/numpy" {
		t.Errorf("unexpected repository: %q", pkg.Repository)
	}
	if pkg.Licenses != "BSD-3-Clause" {
		t.Errorf("unexpected licenses: %q", pkg.Licenses)
	}
	if pkg.Namespace != "conda-forge" {
		t.Errorf("expected namespace 'conda-forge', got %q", pkg.Namespace)
	}
}

func TestFetchPackageWithChannel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/package/bioconda/samtools" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}

		resp := packageResponse{
			Name:    "samtools",
			Summary: "Tools for manipulating next-gen sequencing data",
			Owner:   "bioconda",
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	pkg, err := reg.FetchPackage(context.Background(), "bioconda/samtools")
	if err != nil {
		t.Fatalf("FetchPackage failed: %v", err)
	}

	if pkg.Name != "samtools" {
		t.Errorf("expected name 'samtools', got %q", pkg.Name)
	}
	if pkg.Namespace != "bioconda" {
		t.Errorf("expected namespace 'bioconda', got %q", pkg.Namespace)
	}
}

func TestFetchVersions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := packageResponse{
			Name:     "pandas",
			Versions: []string{"2.1.0", "2.0.3", "1.5.3"},
			Files: []fileInfo{
				{Version: "2.1.0", UploadTime: 1699900000, SHA256: "abc123"},
				{Version: "2.0.3", UploadTime: 1689100000, SHA256: "def456"},
				{Version: "1.5.3", UploadTime: 1678300000, MD5: "ghi789"},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	versions, err := reg.FetchVersions(context.Background(), "pandas")
	if err != nil {
		t.Fatalf("FetchVersions failed: %v", err)
	}

	if len(versions) != 3 {
		t.Fatalf("expected 3 versions, got %d", len(versions))
	}

	if versions[0].Number != "2.1.0" {
		t.Errorf("expected version '2.1.0', got %q", versions[0].Number)
	}
	if versions[0].Integrity != "sha256-abc123" {
		t.Errorf("unexpected integrity: %q", versions[0].Integrity)
	}
	if versions[0].PublishedAt.IsZero() {
		t.Error("expected non-zero published time")
	}

	// Check MD5 fallback
	if versions[2].Integrity != "md5-ghi789" {
		t.Errorf("expected md5 integrity, got %q", versions[2].Integrity)
	}
}

func TestFetchDependencies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := packageResponse{
			Name: "pandas",
			Files: []fileInfo{
				{
					Version: "2.1.0",
					Attrs: fileAttrs{
						Depends: []string{
							"python >=3.9",
							"numpy >=1.22.4",
							"python-dateutil >=2.8.2",
							"pytz >=2020.1",
						},
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	deps, err := reg.FetchDependencies(context.Background(), "pandas", "2.1.0")
	if err != nil {
		t.Fatalf("FetchDependencies failed: %v", err)
	}

	if len(deps) != 4 {
		t.Fatalf("expected 4 dependencies, got %d", len(deps))
	}

	reqMap := make(map[string]string)
	for _, d := range deps {
		reqMap[d.Name] = d.Requirements
	}

	if reqMap["python"] != ">=3.9" {
		t.Errorf("unexpected python requirement: %q", reqMap["python"])
	}
	if reqMap["numpy"] != ">=1.22.4" {
		t.Errorf("unexpected numpy requirement: %q", reqMap["numpy"])
	}
}

func TestFetchMaintainers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := packageResponse{
			Name:  "scipy",
			Owner: "conda-forge",
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	maintainers, err := reg.FetchMaintainers(context.Background(), "scipy")
	if err != nil {
		t.Fatalf("FetchMaintainers failed: %v", err)
	}

	if len(maintainers) != 1 {
		t.Fatalf("expected 1 maintainer, got %d", len(maintainers))
	}

	if maintainers[0].Login != "conda-forge" {
		t.Errorf("expected login 'conda-forge', got %q", maintainers[0].Login)
	}
}

func TestParseDependency(t *testing.T) {
	tests := []struct {
		input string
		name  string
		req   string
	}{
		{"numpy", "numpy", ""},
		{"python >=3.8", "python", ">=3.8"},
		{"pandas >=1.0,<2.0", "pandas", ">=1.0,<2.0"},
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
	reg := New("https://api.anaconda.org", nil)
	urls := reg.URLs()

	tests := []struct {
		name     string
		fn       func() string
		expected string
	}{
		{"registry", func() string { return urls.Registry("numpy", "1.26.0") }, "https://anaconda.org/conda-forge/numpy/1.26.0"},
		{"registry_with_channel", func() string { return urls.Registry("bioconda/samtools", "1.18") }, "https://anaconda.org/bioconda/samtools/1.18"},
		{"purl", func() string { return urls.PURL("numpy", "1.26.0") }, "pkg:conda/conda-forge/numpy@1.26.0"},
		{"purl_with_channel", func() string { return urls.PURL("bioconda/samtools", "1.18") }, "pkg:conda/bioconda/samtools@1.18"},
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
	if reg.Ecosystem() != "conda" {
		t.Errorf("expected ecosystem 'conda', got %q", reg.Ecosystem())
	}
}
