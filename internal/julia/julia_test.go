package julia

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/git-pkgs/registries/internal/core"
)

const samplePackageToml = `name = "JSON"
uuid = "682c06a0-de6a-54ab-a142-c8b1cf79cde6"
repo = "https://github.com/JuliaIO/JSON.jl.git"
`

const sampleVersionsToml = `["0.21.4"]
git-tree-sha1 = "3043b8e5c7c7f4b6f6f5e3b4b4c5d6e7f8a9b0c1"

["0.21.3"]
git-tree-sha1 = "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b"

["0.20.0"]
git-tree-sha1 = "0123456789abcdef0123456789abcdef01234567"
`

const sampleDepsToml = `["0.20"]
Parsers = "69de0a69-1ddd-5017-9359-2bf0b02dc9f0"
Dates = "ade2ca70-3891-5945-98fb-dc099432e06a"

["0.21"]
Parsers = "69de0a69-1ddd-5017-9359-2bf0b02dc9f0"
Dates = "ade2ca70-3891-5945-98fb-dc099432e06a"
Mmap = "a63ad114-7e13-5084-954f-fe012c677804"
`

func TestFetchPackage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/J/JSON/Package.toml" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		_, _ = w.Write([]byte(samplePackageToml))
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	pkg, err := reg.FetchPackage(context.Background(), "JSON")
	if err != nil {
		t.Fatalf("FetchPackage failed: %v", err)
	}

	if pkg.Name != "JSON" {
		t.Errorf("expected name 'JSON', got %q", pkg.Name)
	}
	if pkg.Repository != "https://github.com/JuliaIO/JSON.jl" {
		t.Errorf("unexpected repository: %q", pkg.Repository)
	}
	if pkg.Metadata["uuid"] != "682c06a0-de6a-54ab-a142-c8b1cf79cde6" {
		t.Errorf("unexpected uuid: %v", pkg.Metadata["uuid"])
	}
}

func TestFetchVersions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/J/JSON/Versions.toml" {
			w.WriteHeader(404)
			return
		}
		_, _ = w.Write([]byte(sampleVersionsToml))
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	versions, err := reg.FetchVersions(context.Background(), "JSON")
	if err != nil {
		t.Fatalf("FetchVersions failed: %v", err)
	}

	if len(versions) != 3 {
		t.Fatalf("expected 3 versions, got %d", len(versions))
	}

	// Should be sorted newest first
	if versions[0].Number != "0.21.4" {
		t.Errorf("expected first version '0.21.4', got %q", versions[0].Number)
	}
	if versions[0].Metadata["git-tree-sha1"] != "3043b8e5c7c7f4b6f6f5e3b4b4c5d6e7f8a9b0c1" {
		t.Errorf("unexpected git-tree-sha1: %v", versions[0].Metadata["git-tree-sha1"])
	}
}

func TestFetchDependencies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/J/JSON/Deps.toml" {
			w.WriteHeader(404)
			return
		}
		_, _ = w.Write([]byte(sampleDepsToml))
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())

	// Test version in 0.21 range
	deps, err := reg.FetchDependencies(context.Background(), "JSON", "0.21")
	if err != nil {
		t.Fatalf("FetchDependencies failed: %v", err)
	}

	if len(deps) != 3 {
		t.Fatalf("expected 3 dependencies for 0.21, got %d", len(deps))
	}

	depNames := make(map[string]bool)
	for _, d := range deps {
		depNames[d.Name] = true
	}

	if !depNames["Parsers"] {
		t.Error("expected Parsers dependency")
	}
	if !depNames["Mmap"] {
		t.Error("expected Mmap dependency")
	}
}

func TestFetchDependenciesNoDeps(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	deps, err := reg.FetchDependencies(context.Background(), "NoDeps", "1.0.0")
	if err != nil {
		t.Fatalf("FetchDependencies failed: %v", err)
	}

	if len(deps) != 0 {
		t.Errorf("expected 0 dependencies, got %d", len(deps))
	}
}

func TestFetchMaintainers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(samplePackageToml))
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	maintainers, err := reg.FetchMaintainers(context.Background(), "JSON")
	if err != nil {
		t.Fatalf("FetchMaintainers failed: %v", err)
	}

	// Julia registry doesn't store maintainer info
	if len(maintainers) != 0 {
		t.Errorf("expected 0 maintainers, got %d", len(maintainers))
	}
}

func TestParsePackageToml(t *testing.T) {
	pkg := parsePackageToml(samplePackageToml)

	if pkg.name != "JSON" {
		t.Errorf("expected name 'JSON', got %q", pkg.name)
	}
	if pkg.uuid != "682c06a0-de6a-54ab-a142-c8b1cf79cde6" {
		t.Errorf("unexpected uuid: %q", pkg.uuid)
	}
	if pkg.repo != "https://github.com/JuliaIO/JSON.jl.git" {
		t.Errorf("unexpected repo: %q", pkg.repo)
	}
}

func TestParseVersionsToml(t *testing.T) {
	versions := parseVersionsToml(sampleVersionsToml)

	if len(versions) != 3 {
		t.Fatalf("expected 3 versions, got %d", len(versions))
	}

	if versions["0.21.4"].gitTreeSha1 != "3043b8e5c7c7f4b6f6f5e3b4b4c5d6e7f8a9b0c1" {
		t.Errorf("unexpected git-tree-sha1 for 0.21.4")
	}
}

func TestGetPackagePath(t *testing.T) {
	tests := []struct {
		name     string
		expected string
	}{
		{"JSON", "J/JSON"},
		{"DataFrames", "D/DataFrames"},
		{"CSV", "C/CSV"},
	}

	for _, tt := range tests {
		got := getPackagePath(tt.name)
		if got != tt.expected {
			t.Errorf("getPackagePath(%q) = %q, want %q", tt.name, got, tt.expected)
		}
	}
}

func TestURLBuilder(t *testing.T) {
	reg := New("https://raw.githubusercontent.com/JuliaRegistries/General/master", nil)
	urls := reg.URLs()

	tests := []struct {
		name     string
		fn       func() string
		expected string
	}{
		{"registry", func() string { return urls.Registry("JSON", "0.21.4") }, "https://juliahub.com/ui/Packages/General/JSON/0.21.4"},
		{"registry_no_version", func() string { return urls.Registry("JSON", "") }, "https://juliahub.com/ui/Packages/General/JSON"},
		{"documentation", func() string { return urls.Documentation("JSON", "") }, "https://juliahub.com/docs/General/JSON"},
		{"purl", func() string { return urls.PURL("JSON", "0.21.4") }, "pkg:julia/JSON@0.21.4"},
		{"purl_no_version", func() string { return urls.PURL("JSON", "") }, "pkg:julia/JSON"},
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
	if reg.Ecosystem() != "julia" {
		t.Errorf("expected ecosystem 'julia', got %q", reg.Ecosystem())
	}
}
