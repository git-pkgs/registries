package hex

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
		if r.URL.Path != "/api/packages/phoenix" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}

		resp := packageResponse{
			Name: "phoenix",
			Meta: metaInfo{
				Description: "Peace of mind from prototype to production",
				Licenses:    []string{"MIT"},
				Links: map[string]string{
					"GitHub":  "https://github.com/phoenixframework/phoenix",
					"Website": "https://www.phoenixframework.org",
				},
			},
			Downloads: downloadsInfo{All: 50000000},
			Owners: []ownerInfo{
				{Username: "chrismccord", Email: "chris@example.com"},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	pkg, err := reg.FetchPackage(context.Background(), "phoenix")
	if err != nil {
		t.Fatalf("FetchPackage failed: %v", err)
	}

	if pkg.Name != "phoenix" {
		t.Errorf("expected name 'phoenix', got %q", pkg.Name)
	}
	if pkg.Repository != "https://github.com/phoenixframework/phoenix" {
		t.Errorf("unexpected repository: %q", pkg.Repository)
	}
	if pkg.Licenses != "MIT" {
		t.Errorf("unexpected licenses: %q", pkg.Licenses)
	}
}

func TestFetchVersions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/packages/phoenix":
			resp := packageResponse{
				Name: "phoenix",
				Releases: []releaseInfo{
					{Version: "1.7.0", InsertedAt: "2023-03-02T12:00:00Z"},
					{Version: "1.6.0", InsertedAt: "2022-01-15T12:00:00Z"},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
		case "/api/packages/phoenix/releases/1.7.0":
			resp := versionResponse{
				Version:  "1.7.0",
				Checksum: "abc123",
				Downloads: 1000000,
			}
			_ = json.NewEncoder(w).Encode(resp)
		case "/api/packages/phoenix/releases/1.6.0":
			resp := versionResponse{
				Version:  "1.6.0",
				Checksum: "def456",
				Downloads: 5000000,
				Retirement: map[string]interface{}{
					"reason":  "security",
					"message": "Security vulnerability",
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	versions, err := reg.FetchVersions(context.Background(), "phoenix")
	if err != nil {
		t.Fatalf("FetchVersions failed: %v", err)
	}

	if len(versions) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(versions))
	}

	if versions[0].Number != "1.7.0" {
		t.Errorf("expected version '1.7.0', got %q", versions[0].Number)
	}
	if versions[0].Integrity != "sha256-abc123" {
		t.Errorf("unexpected integrity: %q", versions[0].Integrity)
	}
	if versions[0].Status != core.StatusNone {
		t.Errorf("expected no status for first version")
	}

	if versions[1].Status != core.StatusRetracted {
		t.Errorf("expected retracted status for second version, got %q", versions[1].Status)
	}
}

func TestFetchDependencies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/packages/phoenix/releases/1.7.0" {
			w.WriteHeader(404)
			return
		}

		resp := versionResponse{
			Version: "1.7.0",
			Requirements: map[string]requirementInfo{
				"plug":        {Requirement: "~> 1.14", Optional: false},
				"phoenix_pubsub": {Requirement: "~> 2.1", Optional: false},
				"telemetry":   {Requirement: "~> 0.4 or ~> 1.0", Optional: true},
			},
		}

		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	deps, err := reg.FetchDependencies(context.Background(), "phoenix", "1.7.0")
	if err != nil {
		t.Fatalf("FetchDependencies failed: %v", err)
	}

	if len(deps) != 3 {
		t.Fatalf("expected 3 dependencies, got %d", len(deps))
	}

	optionalCount := 0
	for _, d := range deps {
		if d.Optional {
			optionalCount++
		}
	}

	if optionalCount != 1 {
		t.Errorf("expected 1 optional dep, got %d", optionalCount)
	}
}

func TestFetchMaintainers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := packageResponse{
			Name: "phoenix",
			Owners: []ownerInfo{
				{Username: "chrismccord", Email: "chris@example.com"},
				{Username: "josevalim", Email: "jose@example.com"},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	maintainers, err := reg.FetchMaintainers(context.Background(), "phoenix")
	if err != nil {
		t.Fatalf("FetchMaintainers failed: %v", err)
	}

	if len(maintainers) != 2 {
		t.Fatalf("expected 2 maintainers, got %d", len(maintainers))
	}

	if maintainers[0].Login != "chrismccord" {
		t.Errorf("expected login 'chrismccord', got %q", maintainers[0].Login)
	}
}

func TestURLBuilder(t *testing.T) {
	reg := New("https://hex.pm", nil)
	urls := reg.URLs()

	tests := []struct {
		name     string
		fn       func() string
		expected string
	}{
		{"registry", func() string { return urls.Registry("phoenix", "1.7.0") }, "https://hex.pm/packages/phoenix/1.7.0"},
		{"download", func() string { return urls.Download("phoenix", "1.7.0") }, "https://repo.hex.pm/tarballs/phoenix-1.7.0.tar"},
		{"documentation", func() string { return urls.Documentation("phoenix", "1.7.0") }, "https://hexdocs.pm/phoenix/1.7.0"},
		{"purl", func() string { return urls.PURL("phoenix", "1.7.0") }, "pkg:hex/phoenix@1.7.0"},
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
	if reg.Ecosystem() != "hex" {
		t.Errorf("expected ecosystem 'hex', got %q", reg.Ecosystem())
	}
}
