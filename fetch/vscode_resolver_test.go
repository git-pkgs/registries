package fetch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/git-pkgs/registries"
	"github.com/git-pkgs/registries/client"
	_ "github.com/git-pkgs/registries/internal/vscodeextension"
)

func openVSXBuild(version, platform string) string {
	file := "acme.tool-" + version
	dir := version
	if platform != "universal" {
		file += "@" + platform
		dir = platform + "/" + version
	}
	return fmt.Sprintf(`{"namespace": "acme", "name": "tool", "version": %q, "targetPlatform": %q,
		"timestamp": "2026-01-02T03:04:05Z",
		"files": {"download": "https://open-vsx.org/api/acme/tool/%s/file/%s.vsix"}}`, version, platform, dir, file)
}

// The platform qualifier picks a build among those a version lists. A
// universal build serves any platform, and a build for the platform wins
// over it.
func TestResolveOpenVSXPlatform(t *testing.T) {
	builds := []string{
		openVSXBuild("1.0.0", "linux-x64"),
		openVSXBuild("1.0.0", "darwin-arm64"),
		openVSXBuild("2.0.0", "universal"),
		openVSXBuild("3.0.0", "universal"),
		openVSXBuild("3.0.0", "linux-x64"),
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/-/query" || r.URL.Query().Get("offset") != "0" {
			_, _ = w.Write([]byte(`{"offset": 0, "totalSize": 0, "extensions": []}`))
			return
		}
		_, _ = fmt.Fprintf(w, `{"offset": 0, "totalSize": %d, "extensions": [%s]}`, len(builds), strings.Join(builds, ","))
	}))
	defer server.Close()

	reg, err := registries.New("vscode-extension", server.URL, registries.DefaultClient())
	if err != nil {
		t.Fatal(err)
	}
	r := NewResolver()
	r.RegisterRegistry(reg)

	tests := []struct {
		name     string
		version  string
		platform string
		want     string
	}{
		{"platform-only version", "1.0.0", "linux-x64", "acme.tool-1.0.0@linux-x64.vsix"},
		{"platform-only version, second build", "1.0.0", "darwin-arm64", "acme.tool-1.0.0@darwin-arm64.vsix"},
		{"universal-only version", "2.0.0", "linux-x64", "acme.tool-2.0.0.vsix"},
		{"both, platform build wins", "3.0.0", "linux-x64", "acme.tool-3.0.0@linux-x64.vsix"},
		{"both, other platform gets universal", "3.0.0", "darwin-arm64", "acme.tool-3.0.0.vsix"},
		{"both, universal asked for", "3.0.0", "universal", "acme.tool-3.0.0.vsix"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := r.ResolveWithOptions(context.Background(), "vscode-extension", "acme/tool", tt.version,
				ResolveOptions{Qualifiers: map[string]string{"platform": tt.platform}})
			if err != nil {
				t.Fatalf("Resolve failed: %v", err)
			}
			if info.Filename != tt.want {
				t.Errorf("Filename = %q, want %q", info.Filename, tt.want)
			}
		})
	}

	_, err = r.ResolveWithOptions(context.Background(), "vscode-extension", "acme/tool", "1.0.0",
		ResolveOptions{Qualifiers: map[string]string{"platform": "win32-x64"}})
	if !errors.Is(err, ErrNoMatchingArtifact) {
		t.Errorf("platform without a build: err = %v, want ErrNoMatchingArtifact", err)
	}
}

type urlOnlyRegistry struct{ fakeRegistry }

func (f *urlOnlyRegistry) URLs() client.URLBuilder { //nolint:ireturn // satisfying Registry interface
	return &client.BaseURLs{DownloadFn: func(name, version string) string {
		return "https://example.com/downloads/" + name + "-" + version + ".gem"
	}}
}

// A registry that lists no artifacts, such as RubyGems, has nothing to match
// a selection qualifier against, so its built URL is still the answer.
func TestResolveSelectionQualifierWithoutArtifactsUsesTheBuiltURL(t *testing.T) {
	r := NewResolver()
	r.RegisterRegistry(&urlOnlyRegistry{fakeRegistry{versions: []registries.Version{{Number: "1.0.0"}}}})

	info, err := r.ResolveWithOptions(context.Background(), "fake", "pkg", "1.0.0",
		ResolveOptions{Qualifiers: map[string]string{"platform": "java"}})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if want := "https://example.com/downloads/pkg-1.0.0.gem"; info.URL != want {
		t.Errorf("URL = %q, want %q", info.URL, want)
	}
}

// Only the platform qualifier has a value that stands for every other.
func TestResolveUniversalOnlyStandsForPlatforms(t *testing.T) {
	r := NewResolver()
	r.RegisterRegistry(&fakeRegistry{versions: []registries.Version{{
		Number: "1.0.0",
		Artifacts: []registries.Artifact{
			{URL: "https://example.com/pkg-1.0.0.bin", Metadata: map[string]any{"arch": "universal", "platform": "linux-x64"}},
		},
	}}})

	_, err := r.ResolveWithOptions(context.Background(), "fake", "pkg", "1.0.0",
		ResolveOptions{Qualifiers: map[string]string{"arch": "amd64", "platform": "linux-x64"}})
	if !errors.Is(err, ErrNoMatchingArtifact) {
		t.Errorf("err = %v, want ErrNoMatchingArtifact", err)
	}
}
