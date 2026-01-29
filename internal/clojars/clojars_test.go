package clojars

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/git-pkgs/registries/internal/core"
)

func TestParseCoordinates(t *testing.T) {
	tests := []struct {
		input    string
		group    string
		artifact string
	}{
		{"ring/ring-core", "ring", "ring-core"},
		{"compojure", "compojure", "compojure"},
		{"org.clojure/clojure", "org.clojure", "clojure"},
	}

	for _, tt := range tests {
		g, a := ParseCoordinates(tt.input)
		if g != tt.group || a != tt.artifact {
			t.Errorf("ParseCoordinates(%q) = (%q, %q), want (%q, %q)",
				tt.input, g, a, tt.group, tt.artifact)
		}
	}
}

func TestFetchPackage(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/artifacts/ring/ring-core", func(w http.ResponseWriter, r *http.Request) {
		resp := artifactResponse{
			GroupName:   "ring",
			JarName:     "ring-core",
			Description: "Ring core library",
			Homepage:    "https://github.com/ring-clojure/ring",
			RecentVersions: []versionInfo{
				{Version: "1.11.0", Downloads: 10000},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/api/artifacts/ring/ring-core/versions/1.11.0", func(w http.ResponseWriter, r *http.Request) {
		resp := versionDetailResponse{
			Version:  "1.11.0",
			Licenses: []string{"MIT"},
			SCM: scmInfo{
				URL: "https://github.com/ring-clojure/ring.git",
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	pkg, err := reg.FetchPackage(context.Background(), "ring/ring-core")
	if err != nil {
		t.Fatalf("FetchPackage failed: %v", err)
	}

	if pkg.Name != "ring/ring-core" {
		t.Errorf("expected name 'ring/ring-core', got %q", pkg.Name)
	}
	if pkg.Namespace != "ring" {
		t.Errorf("expected namespace 'ring', got %q", pkg.Namespace)
	}
	if pkg.Repository != "https://github.com/ring-clojure/ring" {
		t.Errorf("unexpected repository: %q", pkg.Repository)
	}
	if pkg.Licenses != "MIT" {
		t.Errorf("unexpected licenses: %q", pkg.Licenses)
	}
}

func TestFetchPackageSingleName(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/artifacts/compojure/compojure", func(w http.ResponseWriter, r *http.Request) {
		resp := artifactResponse{
			GroupName:   "compojure",
			JarName:     "compojure",
			Description: "A concise routing library for Ring",
			RecentVersions: []versionInfo{
				{Version: "1.7.0"},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/api/artifacts/compojure/compojure/versions/1.7.0", func(w http.ResponseWriter, r *http.Request) {
		resp := versionDetailResponse{Version: "1.7.0"}
		_ = json.NewEncoder(w).Encode(resp)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	pkg, err := reg.FetchPackage(context.Background(), "compojure")
	if err != nil {
		t.Fatalf("FetchPackage failed: %v", err)
	}

	// When group == artifact, name should be just the artifact
	if pkg.Name != "compojure" {
		t.Errorf("expected name 'compojure', got %q", pkg.Name)
	}
}

func TestFetchVersions(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/artifacts/hiccup/hiccup", func(w http.ResponseWriter, r *http.Request) {
		resp := artifactResponse{
			GroupName: "hiccup",
			JarName:   "hiccup",
			RecentVersions: []versionInfo{
				{Version: "2.0.0", Downloads: 5000},
				{Version: "1.0.5", Downloads: 50000},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/api/artifacts/hiccup/hiccup/versions/2.0.0", func(w http.ResponseWriter, r *http.Request) {
		resp := versionDetailResponse{
			Version:      "2.0.0",
			CreatedEpoch: 1699900000000,
			Licenses:     []string{"EPL-1.0"},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/api/artifacts/hiccup/hiccup/versions/1.0.5", func(w http.ResponseWriter, r *http.Request) {
		resp := versionDetailResponse{
			Version:      "1.0.5",
			CreatedEpoch: 1600000000000,
			Licenses:     []string{"EPL-1.0"},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	versions, err := reg.FetchVersions(context.Background(), "hiccup")
	if err != nil {
		t.Fatalf("FetchVersions failed: %v", err)
	}

	if len(versions) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(versions))
	}

	if versions[0].Number != "2.0.0" {
		t.Errorf("expected version '2.0.0', got %q", versions[0].Number)
	}
	if versions[0].PublishedAt.IsZero() {
		t.Error("expected non-zero published time")
	}
	if versions[0].Licenses != "EPL-1.0" {
		t.Errorf("unexpected license: %q", versions[0].Licenses)
	}
}

func TestFetchDependencies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/artifacts/ring/ring-core/versions/1.11.0" {
			w.WriteHeader(404)
			return
		}

		resp := versionDetailResponse{
			Version: "1.11.0",
			Dependencies: []depInfo{
				{GroupName: "ring", JarName: "ring-codec", Version: "1.2.0", Scope: "compile"},
				{GroupName: "crypto-equality", JarName: "crypto-equality", Version: "1.0.1", Scope: "compile"},
				{GroupName: "clj-time", JarName: "clj-time", Version: "0.15.2", Scope: "test"},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	deps, err := reg.FetchDependencies(context.Background(), "ring/ring-core", "1.11.0")
	if err != nil {
		t.Fatalf("FetchDependencies failed: %v", err)
	}

	if len(deps) != 3 {
		t.Fatalf("expected 3 dependencies, got %d", len(deps))
	}

	scopeMap := make(map[string]core.Scope)
	for _, d := range deps {
		scopeMap[d.Name] = d.Scope
	}

	if scopeMap["ring/ring-codec"] != core.Runtime {
		t.Errorf("expected runtime scope for ring-codec, got %q", scopeMap["ring/ring-codec"])
	}
	if scopeMap["crypto-equality"] != core.Runtime {
		t.Errorf("expected runtime scope for crypto-equality, got %q", scopeMap["crypto-equality"])
	}
	if scopeMap["clj-time"] != core.Test {
		t.Errorf("expected test scope for clj-time, got %q", scopeMap["clj-time"])
	}
}

func TestURLBuilder(t *testing.T) {
	reg := New("https://clojars.org", nil)
	urls := reg.URLs()

	tests := []struct {
		name     string
		fn       func() string
		expected string
	}{
		{"registry_grouped", func() string { return urls.Registry("ring/ring-core", "1.11.0") }, "https://clojars.org/ring/ring-core/versions/1.11.0"},
		{"registry_single", func() string { return urls.Registry("compojure", "1.7.0") }, "https://clojars.org/compojure/versions/1.7.0"},
		{"download", func() string { return urls.Download("ring/ring-core", "1.11.0") }, "https://repo.clojars.org/ring/ring-core/1.11.0/ring-core-1.11.0.jar"},
		{"documentation", func() string { return urls.Documentation("ring/ring-core", "") }, "https://cljdoc.org/d/ring/ring-core/CURRENT"},
		{"purl", func() string { return urls.PURL("ring/ring-core", "1.11.0") }, "pkg:clojars/ring/ring-core@1.11.0"},
		{"purl_single", func() string { return urls.PURL("compojure", "1.7.0") }, "pkg:clojars/compojure/compojure@1.7.0"},
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
	if reg.Ecosystem() != "clojars" {
		t.Errorf("expected ecosystem 'clojars', got %q", reg.Ecosystem())
	}
}
