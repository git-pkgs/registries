package cargo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/git-pkgs/registries/internal/core"
)

func TestFetchPackage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/crates/serde" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}

		resp := crateResponse{
			Crate: crateInfo{
				ID:          "serde",
				Name:        "serde",
				Description: "A generic serialization/deserialization framework",
				Homepage:    "https://serde.rs",
				Repository:  "https://github.com/serde-rs/serde",
				Keywords:    []string{"serialization", "no_std"},
				Categories:  []string{"encoding"},
			},
			Versions: []versionInfo{
				{
					ID:        1748414,
					Num:       "1.0.228",
					License:   "MIT OR Apache-2.0",
					Checksum:  "9a8e94ea7f378bd32cbbd37198a4a91436180c5bb472411e48b5ec2e2124ae9e",
					Yanked:    false,
					CreatedAt: "2025-09-27T16:51:35Z",
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	pkg, err := reg.FetchPackage(context.Background(), "serde")
	if err != nil {
		t.Fatalf("FetchPackage failed: %v", err)
	}

	if pkg.Name != "serde" {
		t.Errorf("expected name 'serde', got %q", pkg.Name)
	}
	if pkg.Description != "A generic serialization/deserialization framework" {
		t.Errorf("unexpected description: %q", pkg.Description)
	}
	if pkg.Repository != "https://github.com/serde-rs/serde" {
		t.Errorf("unexpected repository: %q", pkg.Repository)
	}
	if pkg.Licenses != "MIT OR Apache-2.0" {
		t.Errorf("unexpected licenses: %q", pkg.Licenses)
	}
	if len(pkg.Keywords) != 2 {
		t.Errorf("expected 2 keywords, got %d", len(pkg.Keywords))
	}
}

func TestFetchPackageNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	_, err := reg.FetchPackage(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent package")
	}

	var notFound *core.NotFoundError
	if _, ok := err.(*core.NotFoundError); !ok {
		t.Errorf("expected NotFoundError, got %T", err)
	}
	_ = notFound
}

func TestFetchVersions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := crateResponse{
			Crate: crateInfo{ID: "serde"},
			Versions: []versionInfo{
				{
					Num:       "1.0.228",
					License:   "MIT OR Apache-2.0",
					Checksum:  "abc123",
					Yanked:    false,
					CreatedAt: "2025-09-27T16:51:35Z",
				},
				{
					Num:       "1.0.227",
					License:   "MIT OR Apache-2.0",
					Checksum:  "def456",
					Yanked:    true,
					CreatedAt: "2025-09-25T23:43:08Z",
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	versions, err := reg.FetchVersions(context.Background(), "serde")
	if err != nil {
		t.Fatalf("FetchVersions failed: %v", err)
	}

	if len(versions) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(versions))
	}

	if versions[0].Number != "1.0.228" {
		t.Errorf("expected version '1.0.228', got %q", versions[0].Number)
	}
	if versions[0].Status != core.StatusNone {
		t.Errorf("expected no status for first version, got %q", versions[0].Status)
	}
	if versions[0].Integrity != "sha256-abc123" {
		t.Errorf("unexpected integrity: %q", versions[0].Integrity)
	}

	if versions[1].Status != core.StatusYanked {
		t.Errorf("expected yanked status for second version, got %q", versions[1].Status)
	}

	expectedTime, _ := time.Parse(time.RFC3339, "2025-09-27T16:51:35Z")
	if !versions[0].PublishedAt.Equal(expectedTime) {
		t.Errorf("unexpected published_at: %v", versions[0].PublishedAt)
	}
}

func TestFetchDependencies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/crates/tokio/1.0.0/dependencies" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}

		resp := dependenciesResponse{
			Dependencies: []dependencyInfo{
				{CrateID: "bytes", Req: "^1.0", Kind: "normal", Optional: false},
				{CrateID: "libc", Req: "^0.2", Kind: "normal", Optional: true},
				{CrateID: "tokio-test", Req: "^0.4", Kind: "dev", Optional: false},
				{CrateID: "cc", Req: "^1.0", Kind: "build", Optional: false},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	deps, err := reg.FetchDependencies(context.Background(), "tokio", "1.0.0")
	if err != nil {
		t.Fatalf("FetchDependencies failed: %v", err)
	}

	if len(deps) != 4 {
		t.Fatalf("expected 4 dependencies, got %d", len(deps))
	}

	if deps[0].Name != "bytes" {
		t.Errorf("expected name 'bytes', got %q", deps[0].Name)
	}
	if deps[0].Requirements != "^1.0" {
		t.Errorf("expected requirements '^1.0', got %q", deps[0].Requirements)
	}
	if deps[0].Scope != core.Runtime {
		t.Errorf("expected runtime scope, got %q", deps[0].Scope)
	}

	if deps[1].Optional != true {
		t.Error("expected libc to be optional")
	}

	if deps[2].Scope != core.Development {
		t.Errorf("expected development scope, got %q", deps[2].Scope)
	}

	if deps[3].Scope != core.Build {
		t.Errorf("expected build scope, got %q", deps[3].Scope)
	}
}

func TestFetchMaintainers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/crates/serde/owner_user" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}

		resp := ownersResponse{
			Users: []ownerInfo{
				{ID: 3618, Login: "dtolnay", Name: "David Tolnay", URL: "https://github.com/dtolnay"},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	maintainers, err := reg.FetchMaintainers(context.Background(), "serde")
	if err != nil {
		t.Fatalf("FetchMaintainers failed: %v", err)
	}

	if len(maintainers) != 1 {
		t.Fatalf("expected 1 maintainer, got %d", len(maintainers))
	}

	if maintainers[0].Login != "dtolnay" {
		t.Errorf("expected login 'dtolnay', got %q", maintainers[0].Login)
	}
	if maintainers[0].Name != "David Tolnay" {
		t.Errorf("expected name 'David Tolnay', got %q", maintainers[0].Name)
	}
}

func TestURLBuilder(t *testing.T) {
	reg := New("https://crates.io", nil)
	urls := reg.URLs()

	tests := []struct {
		name     string
		fn       func() string
		expected string
	}{
		{"registry with version", func() string { return urls.Registry("serde", "1.0.228") }, "https://crates.io/crates/serde/1.0.228"},
		{"registry without version", func() string { return urls.Registry("serde", "") }, "https://crates.io/crates/serde"},
		{"download", func() string { return urls.Download("serde", "1.0.228") }, "https://static.crates.io/crates/serde/serde-1.0.228.crate"},
		{"download no version", func() string { return urls.Download("serde", "") }, ""},
		{"documentation", func() string { return urls.Documentation("serde", "1.0.228") }, "https://docs.rs/serde/1.0.228"},
		{"purl with version", func() string { return urls.PURL("serde", "1.0.228") }, "pkg:cargo/serde@1.0.228"},
		{"purl without version", func() string { return urls.PURL("serde", "") }, "pkg:cargo/serde"},
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
	if reg.Ecosystem() != "cargo" {
		t.Errorf("expected ecosystem 'cargo', got %q", reg.Ecosystem())
	}
}
