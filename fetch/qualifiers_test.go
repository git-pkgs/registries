package fetch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/git-pkgs/registries"
	_ "github.com/git-pkgs/registries/internal/conda"
)

const anacondaZlib = `{"name": "zlib", "owner": {"login": "conda-forge"}, "versions": ["1.3.1"], "files": [
  {"version": "1.3.1", "basename": "linux-64/zlib-1.3.1-hb9d3cd8_2.conda",
   "download_url": "//api.anaconda.org/download/conda-forge/zlib/1.3.1/linux-64/zlib-1.3.1-hb9d3cd8_2.conda",
   "upload_time": "2024-09-17 07:32:19.214000+00:00", "sha256": "aaa",
   "attrs": {"subdir": "linux-64", "arch": "x86_64", "platform": "linux", "build": "hb9d3cd8_2"}},
  {"version": "1.3.1", "basename": "osx-arm64/zlib-1.3.1-h8359307_2.conda",
   "download_url": "//api.anaconda.org/download/conda-forge/zlib/1.3.1/osx-arm64/zlib-1.3.1-h8359307_2.conda",
   "upload_time": "2024-09-17 07:35:01.100000+00:00", "sha256": "bbb",
   "attrs": {"subdir": "osx-arm64", "arch": "arm64", "platform": "osx", "build": "h8359307_2"}}
]}`

func TestResolveCondaQualifiers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/package/conda-forge/zlib" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(anacondaZlib))
	}))
	defer server.Close()

	reg, err := registries.New("conda", server.URL, registries.DefaultClient())
	if err != nil {
		t.Fatal(err)
	}
	r := NewResolver()
	r.RegisterRegistry(reg)

	linux := "zlib-1.3.1-hb9d3cd8_2.conda"
	osx := "zlib-1.3.1-h8359307_2.conda"
	tests := []struct {
		name       string
		qualifiers map[string]string
		want       string
	}{
		{"none", nil, linux},
		{"subdir", map[string]string{"subdir": "osx-arm64"}, osx},
		{"build", map[string]string{"build": "hb9d3cd8_2"}, linux},
		{"arch", map[string]string{"arch": "arm64"}, osx},
		{"subdir and build", map[string]string{"subdir": "osx-arm64", "build": "h8359307_2"}, osx},
		{"repository_url and channel ignored", map[string]string{"repository_url": "https://example.com", "channel": "bioconda", "subdir": "osx-arm64"}, osx},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := r.ResolveWithOptions(context.Background(), "conda", "zlib", "1.3.1", ResolveOptions{Qualifiers: tt.qualifiers})
			if err != nil {
				t.Fatalf("Resolve failed: %v", err)
			}
			if info.Filename != tt.want {
				t.Errorf("Filename = %q, want %q", info.Filename, tt.want)
			}
		})
	}

	for _, q := range []map[string]string{
		{"subdir": "win-64"},
		{"subdir": "osx-arm64", "build": "hb9d3cd8_2"},
	} {
		_, err := r.ResolveWithOptions(context.Background(), "conda", "zlib", "1.3.1", ResolveOptions{Qualifiers: q})
		if !errors.Is(err, ErrNoMatchingArtifact) {
			t.Errorf("qualifiers %v: err = %v, want ErrNoMatchingArtifact", q, err)
		}
	}
}

func TestResolveQualifiersSkipArtifactsWithoutTheField(t *testing.T) {
	r := NewResolver()
	r.RegisterRegistry(&fakeRegistry{versions: []registries.Version{{
		Number: "1.0.0",
		Artifacts: []registries.Artifact{
			{URL: "https://example.com/pkg-1.0.0-bookworm-amd64.deb", Metadata: map[string]any{"distro": "bookworm", "arch": "amd64"}},
			{URL: "https://example.com/pkg-1.0.0-trixie.deb", Metadata: map[string]any{"distro": "trixie"}},
		},
	}}})

	info, err := r.ResolveWithOptions(context.Background(), "fake", "pkg", "1.0.0",
		ResolveOptions{Qualifiers: map[string]string{"distro": "trixie", "arch": "amd64"}})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if want := "pkg-1.0.0-trixie.deb"; info.Filename != want {
		t.Errorf("Filename = %q, want %q", info.Filename, want)
	}

	_, err = r.ResolveWithOptions(context.Background(), "fake", "pkg", "1.0.0",
		ResolveOptions{Qualifiers: map[string]string{"distro": "sid"}})
	if !errors.Is(err, ErrNoMatchingArtifact) {
		t.Errorf("err = %v, want ErrNoMatchingArtifact", err)
	}
}
