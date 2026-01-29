package hackage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/git-pkgs/registries/internal/core"
)

func TestFetchPackage(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("/package/aeson/preferred", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("normal-versions: 2.2.0.0, 2.1.0.0, 2.0.0.0"))
	})

	mux.HandleFunc("/package/aeson-2.2.0.0/aeson.cabal", func(w http.ResponseWriter, r *http.Request) {
		cabal := `name:           aeson
version:        2.2.0.0
synopsis:       Fast JSON parsing and encoding
license:        BSD3
homepage:       https://github.com/haskell/aeson
author:         Bryan O'Sullivan
maintainer:     Adam Bergmark <adam@bergmark.nl>
category:       Text, Web, JSON

source-repository head
  type:     git
  location: https://github.com/haskell/aeson
`
		_, _ = w.Write([]byte(cabal))
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	pkg, err := reg.FetchPackage(context.Background(), "aeson")
	if err != nil {
		t.Fatalf("FetchPackage failed: %v", err)
	}

	if pkg.Name != "aeson" {
		t.Errorf("expected name 'aeson', got %q", pkg.Name)
	}
	if pkg.Description != "Fast JSON parsing and encoding" {
		t.Errorf("unexpected description: %q", pkg.Description)
	}
	if pkg.Repository != "https://github.com/haskell/aeson" {
		t.Errorf("unexpected repository: %q", pkg.Repository)
	}
	if pkg.Licenses != "BSD3" {
		t.Errorf("unexpected licenses: %q", pkg.Licenses)
	}
}

func TestFetchVersions(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("/package/lens/preferred", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("normal-versions: 5.2.3, 5.2.2, 5.1.0"))
	})

	mux.HandleFunc("/package/lens-5.2.3/upload-time", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("2023-10-15T12:00:00Z"))
	})

	mux.HandleFunc("/package/lens-5.2.2/upload-time", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("2023-08-01T12:00:00Z"))
	})

	mux.HandleFunc("/package/lens-5.1.0/upload-time", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("2023-01-15T12:00:00Z"))
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	versions, err := reg.FetchVersions(context.Background(), "lens")
	if err != nil {
		t.Fatalf("FetchVersions failed: %v", err)
	}

	if len(versions) != 3 {
		t.Fatalf("expected 3 versions, got %d", len(versions))
	}

	if versions[0].Number != "5.2.3" {
		t.Errorf("expected version '5.2.3', got %q", versions[0].Number)
	}
	if versions[0].PublishedAt.IsZero() {
		t.Error("expected non-zero published time")
	}
}

func TestFetchDependencies(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("/package/aeson-2.2.0.0/aeson.cabal", func(w http.ResponseWriter, r *http.Request) {
		cabal := `name:           aeson
version:        2.2.0.0

library
  build-depends:
    bytestring >= 0.10.4,
    containers >= 0.5.5.1,
    text >= 1.2.3.0,
    transformers >= 0.2.2.0

test-suite tests
  build-depends:
    base,
    aeson,
    QuickCheck >= 2.10
`
		_, _ = w.Write([]byte(cabal))
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	deps, err := reg.FetchDependencies(context.Background(), "aeson", "2.2.0.0")
	if err != nil {
		t.Fatalf("FetchDependencies failed: %v", err)
	}

	// Should have deps (base is filtered out)
	if len(deps) < 4 {
		t.Fatalf("expected at least 4 dependencies, got %d", len(deps))
	}

	nameMap := make(map[string]bool)
	for _, d := range deps {
		nameMap[d.Name] = true
	}

	if !nameMap["bytestring"] {
		t.Error("expected bytestring dependency")
	}
	if !nameMap["containers"] {
		t.Error("expected containers dependency")
	}
	if !nameMap["text"] {
		t.Error("expected text dependency")
	}
	if nameMap["base"] {
		t.Error("base should be filtered out")
	}
}

func TestParseCabalFile(t *testing.T) {
	cabal := `name:           test-package
version:        1.0.0
synopsis:       A test package
description:    This is a longer
                description that spans
                multiple lines.
license:        MIT
homepage:       https://example.com
author:         John Doe
maintainer:     maintainer@example.com
category:       Testing, Development

source-repository head
  type:     git
  location: https://github.com/example/test
`

	info := parseCabalFile(cabal)

	if info.Name != "test-package" {
		t.Errorf("expected name 'test-package', got %q", info.Name)
	}
	if info.Version != "1.0.0" {
		t.Errorf("expected version '1.0.0', got %q", info.Version)
	}
	if info.Synopsis != "A test package" {
		t.Errorf("unexpected synopsis: %q", info.Synopsis)
	}
	if info.License != "MIT" {
		t.Errorf("expected license 'MIT', got %q", info.License)
	}
	if info.SourceRepository != "https://github.com/example/test" {
		t.Errorf("unexpected source repository: %q", info.SourceRepository)
	}
}

func TestParsePreferredVersions(t *testing.T) {
	content := "normal-versions: 1.0.0, 2.0.0, 1.5.0"
	versions := parsePreferredVersions(content)

	if len(versions) != 3 {
		t.Fatalf("expected 3 versions, got %d", len(versions))
	}

	// Should be sorted descending
	if versions[0] != "2.0.0" {
		t.Errorf("expected first version '2.0.0', got %q", versions[0])
	}
	if versions[1] != "1.5.0" {
		t.Errorf("expected second version '1.5.0', got %q", versions[1])
	}
}

func TestURLBuilder(t *testing.T) {
	reg := New("https://hackage.haskell.org", nil)
	urls := reg.URLs()

	tests := []struct {
		name     string
		fn       func() string
		expected string
	}{
		{"registry", func() string { return urls.Registry("aeson", "2.2.0.0") }, "https://hackage.haskell.org/package/aeson-2.2.0.0"},
		{"download", func() string { return urls.Download("aeson", "2.2.0.0") }, "https://hackage.haskell.org/package/aeson-2.2.0.0/aeson-2.2.0.0.tar.gz"},
		{"documentation", func() string { return urls.Documentation("aeson", "2.2.0.0") }, "https://hackage.haskell.org/package/aeson-2.2.0.0/docs"},
		{"purl", func() string { return urls.PURL("aeson", "2.2.0.0") }, "pkg:hackage/aeson@2.2.0.0"},
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
	if reg.Ecosystem() != "hackage" {
		t.Errorf("expected ecosystem 'hackage', got %q", reg.Ecosystem())
	}
}
