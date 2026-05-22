package golang

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
		if r.URL.Path == "/github.com/gorilla/mux/@v/list" {
			_, _ = w.Write([]byte("v1.8.0\nv1.7.0\n"))
			return
		}
		w.WriteHeader(404)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	pkg, err := reg.FetchPackage(context.Background(), "github.com/gorilla/mux")
	if err != nil {
		t.Fatalf("FetchPackage failed: %v", err)
	}

	if pkg.Name != "github.com/gorilla/mux" {
		t.Errorf("expected name 'github.com/gorilla/mux', got %q", pkg.Name)
	}
	if pkg.Repository != "https://github.com/gorilla/mux" {
		t.Errorf("unexpected repository: %q", pkg.Repository)
	}
	if pkg.Namespace != "github.com/gorilla" {
		t.Errorf("unexpected namespace: %q", pkg.Namespace)
	}
}

func TestFetchPackagePkgsite(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/module/golang.org/x/sync" {
			w.WriteHeader(404)
			return
		}
		if r.URL.Query().Get("licenses") != "true" {
			t.Errorf("expected licenses=true query param")
		}
		_ = json.NewEncoder(w).Encode(pkgsiteModule{
			Path:    "golang.org/x/sync",
			Version: "v0.7.0",
			RepoURL: "https://go.googlesource.com/sync",
			Licenses: []pkgsiteLicense{
				{Types: []string{"BSD-3-Clause"}},
			},
		})
	}))
	defer server.Close()

	reg := New("", core.DefaultClient())
	reg.pkgsiteURL = server.URL

	pkg, err := reg.FetchPackage(context.Background(), "golang.org/x/sync")
	if err != nil {
		t.Fatalf("FetchPackage failed: %v", err)
	}

	if pkg.Repository != "https://go.googlesource.com/sync" {
		t.Errorf("unexpected repository: %q", pkg.Repository)
	}
	if pkg.Licenses != "BSD-3-Clause" {
		t.Errorf("unexpected licenses: %q", pkg.Licenses)
	}
	if pkg.LatestVersion != "v0.7.0" {
		t.Errorf("unexpected latest version: %q", pkg.LatestVersion)
	}
	if pkg.Namespace != "golang.org/x" {
		t.Errorf("unexpected namespace: %q", pkg.Namespace)
	}
}

func TestFetchPackagePkgsiteFallback(t *testing.T) {
	// pkgsite returns 500, proxy succeeds
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/github.com/gorilla/mux/@v/list" {
			_, _ = w.Write([]byte("v1.8.0\n"))
			return
		}
		w.WriteHeader(404)
	}))
	defer proxy.Close()

	pkgsite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer pkgsite.Close()

	reg := New(proxy.URL, core.NewClient(core.WithMaxRetries(0)))
	reg.pkgsiteURL = pkgsite.URL

	pkg, err := reg.FetchPackage(context.Background(), "github.com/gorilla/mux")
	if err != nil {
		t.Fatalf("FetchPackage should fall back to proxy: %v", err)
	}
	if pkg.Repository != "https://github.com/gorilla/mux" {
		t.Errorf("unexpected repository: %q", pkg.Repository)
	}
}

func TestFetchPackageNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	_, err := reg.FetchPackage(context.Background(), "github.com/nonexistent/pkg")
	if err == nil {
		t.Fatal("expected error for nonexistent package")
	}

	if _, ok := err.(*core.NotFoundError); !ok {
		t.Errorf("expected NotFoundError, got %T", err)
	}
}

func TestFetchVersions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/github.com/gorilla/mux/@v/list":
			_, _ = w.Write([]byte("v1.8.0\nv1.7.0\n"))
		case "/github.com/gorilla/mux/@v/v1.8.0.info":
			_ = json.NewEncoder(w).Encode(versionInfo{
				Version: "v1.8.0",
				Time:    time.Date(2023, 1, 15, 12, 0, 0, 0, time.UTC),
			})
		case "/github.com/gorilla/mux/@v/v1.7.0.info":
			_ = json.NewEncoder(w).Encode(versionInfo{
				Version: "v1.7.0",
				Time:    time.Date(2022, 6, 1, 12, 0, 0, 0, time.UTC),
			})
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	versions, err := reg.FetchVersions(context.Background(), "github.com/gorilla/mux")
	if err != nil {
		t.Fatalf("FetchVersions failed: %v", err)
	}

	if len(versions) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(versions))
	}
}

func TestFetchVersionsPkgsite(t *testing.T) {
	page1 := pkgsiteVersions{
		Items: []pkgsiteVersion{
			{Version: "v1.2.0", CommitTime: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)},
			{Version: "v1.1.0", CommitTime: time.Date(2023, 6, 1, 0, 0, 0, 0, time.UTC), Retracted: true},
		},
		NextPageToken: "abc",
	}
	page2 := pkgsiteVersions{
		Items: []pkgsiteVersion{
			{Version: "v1.0.0", CommitTime: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Deprecated: true},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/versions/example.com/mod" {
			w.WriteHeader(404)
			return
		}
		if r.URL.Query().Get("token") == "abc" {
			_ = json.NewEncoder(w).Encode(page2)
		} else {
			_ = json.NewEncoder(w).Encode(page1)
		}
	}))
	defer server.Close()

	reg := New("", core.DefaultClient())
	reg.pkgsiteURL = server.URL

	versions, err := reg.FetchVersions(context.Background(), "example.com/mod")
	if err != nil {
		t.Fatalf("FetchVersions failed: %v", err)
	}

	if len(versions) != 3 {
		t.Fatalf("expected 3 versions across 2 pages, got %d", len(versions))
	}
	if versions[0].Number != "v1.2.0" || versions[0].Status != core.StatusNone {
		t.Errorf("unexpected version[0]: %+v", versions[0])
	}
	if versions[1].Number != "v1.1.0" || versions[1].Status != core.StatusRetracted {
		t.Errorf("expected v1.1.0 retracted, got %+v", versions[1])
	}
	if versions[2].Number != "v1.0.0" || versions[2].Status != core.StatusDeprecated {
		t.Errorf("expected v1.0.0 deprecated, got %+v", versions[2])
	}
	if versions[0].PublishedAt.Year() != 2024 {
		t.Errorf("expected commitTime to populate PublishedAt, got %v", versions[0].PublishedAt)
	}
}

func TestFetchVersionsPkgsiteNotFoundFallsThrough(t *testing.T) {
	pkgsite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"code":404,"message":"not found"}`))
	}))
	defer pkgsite.Close()

	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/example.com/fresh/@v/list":
			_, _ = w.Write([]byte("v0.1.0\n"))
		case "/example.com/fresh/@v/v0.1.0.info":
			_ = json.NewEncoder(w).Encode(versionInfo{Version: "v0.1.0"})
		default:
			w.WriteHeader(404)
		}
	}))
	defer proxy.Close()

	reg := New(proxy.URL, core.DefaultClient())
	reg.pkgsiteURL = pkgsite.URL

	versions, err := reg.FetchVersions(context.Background(), "example.com/fresh")
	if err != nil {
		t.Fatalf("expected fallthrough to proxy on pkgsite 404: %v", err)
	}
	if len(versions) != 1 || versions[0].Number != "v0.1.0" {
		t.Errorf("expected proxy version v0.1.0, got %+v", versions)
	}

	_, err = reg.FetchVersions(context.Background(), "example.com/missing")
	if _, ok := err.(*core.NotFoundError); !ok {
		t.Errorf("expected NotFoundError from proxy, got %T: %v", err, err)
	}
}

func TestFetchDependencies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/github.com/gorilla/mux/@v/v1.8.0.mod" {
			w.WriteHeader(404)
			return
		}

		goMod := `module github.com/gorilla/mux

go 1.12

require (
	github.com/stretchr/testify v1.7.0
	golang.org/x/net v0.0.0-20210614182718-04defd469f4e // indirect
)
`
		_, _ = w.Write([]byte(goMod))
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	deps, err := reg.FetchDependencies(context.Background(), "github.com/gorilla/mux", "v1.8.0")
	if err != nil {
		t.Fatalf("FetchDependencies failed: %v", err)
	}

	if len(deps) != 2 {
		t.Fatalf("expected 2 dependencies, got %d", len(deps))
	}

	runtimeCount := 0
	indirectCount := 0
	for _, d := range deps {
		if d.Scope == core.Runtime {
			runtimeCount++
		}
		if d.Optional {
			indirectCount++
		}
	}

	if runtimeCount != 1 {
		t.Errorf("expected 1 direct dep, got %d", runtimeCount)
	}
	if indirectCount != 1 {
		t.Errorf("expected 1 indirect dep, got %d", indirectCount)
	}
}

func TestEncodeForProxy(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"github.com/gorilla/mux", "github.com/gorilla/mux"},
		{"github.com/Azure/azure-sdk-for-go", "github.com/!azure/azure-sdk-for-go"},
		{"github.com/BurntSushi/toml", "github.com/!burnt!sushi/toml"},
		{"golang.org/x/net", "golang.org/x/net"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := encodeForProxy(tt.input)
			if got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestParseGoMod(t *testing.T) {
	goMod := `module example.com/mymodule

go 1.19

require (
	github.com/pkg/errors v0.9.1
	golang.org/x/sync v0.0.0-20220722155255-886fb9371eb4
	rsc.io/quote v1.5.2 // indirect
)

require github.com/single/dep v1.0.0
`

	deps := parseGoMod(goMod)

	if len(deps) != 4 {
		t.Fatalf("expected 4 dependencies, got %d", len(deps))
	}

	found := map[string]bool{}
	for _, d := range deps {
		found[d.Name] = true
		if d.Name == "rsc.io/quote" && !d.Optional {
			t.Error("rsc.io/quote should be indirect/optional")
		}
		if d.Name == "github.com/pkg/errors" && d.Optional {
			t.Error("github.com/pkg/errors should not be indirect")
		}
	}

	expected := []string{"github.com/pkg/errors", "golang.org/x/sync", "rsc.io/quote", "github.com/single/dep"}
	for _, e := range expected {
		if !found[e] {
			t.Errorf("expected to find dependency %q", e)
		}
	}
}

func TestURLBuilder(t *testing.T) {
	reg := New("https://proxy.golang.org", nil)
	urls := reg.URLs()

	tests := []struct {
		name     string
		fn       func() string
		expected string
	}{
		{"registry", func() string { return urls.Registry("github.com/gorilla/mux", "v1.8.0") }, "https://pkg.go.dev/github.com/gorilla/mux@v1.8.0"},
		{"download", func() string { return urls.Download("github.com/gorilla/mux", "v1.8.0") }, "https://proxy.golang.org/github.com/gorilla/mux/@v/v1.8.0.zip"},
		{"download azure", func() string { return urls.Download("github.com/Azure/go-sdk", "v1.0.0") }, "https://proxy.golang.org/github.com/!azure/go-sdk/@v/v1.0.0.zip"},
		{"documentation", func() string { return urls.Documentation("github.com/gorilla/mux", "v1.8.0") }, "https://pkg.go.dev/github.com/gorilla/mux@v1.8.0#section-documentation"},
		{"purl", func() string { return urls.PURL("github.com/gorilla/mux", "v1.8.0") }, "pkg:golang/github.com/gorilla/mux@v1.8.0"},
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
	if reg.Ecosystem() != "golang" {
		t.Errorf("expected ecosystem 'golang', got %q", reg.Ecosystem())
	}
}

func TestDeriveRepoURL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"github.com/gorilla/mux", "https://github.com/gorilla/mux"},
		{"github.com/gorilla/mux/subpkg", "https://github.com/gorilla/mux"},
		{"gitlab.com/my/project", "https://gitlab.com/my/project"},
		{"golang.org/x/net", "https://golang.org/x/net"},
		{"rsc.io/quote", "https://rsc.io/quote"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := deriveRepoURL(tt.input)
			if got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}
