package registries_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/git-pkgs/purl"
	"github.com/git-pkgs/registries"
)

func TestFetchVersionFromPURLIdentities(t *testing.T) {
	for _, tc := range []struct{ ecosystem, name, version, path string }{
		{"golang", "github.com/Azure/GoSDK", "v1.2.3", "/github.com/!azure/!go!s!d!k/@v/v1.2.3.info"},
		{"golang", "github.com/azure/gosdk", "v1.2.3", "/github.com/azure/gosdk/@v/v1.2.3.info"},
		{"swift", "Mona.Linked-List", "1.0.0", "/mona/linked-list/1.0.0"},
	} {
		t.Run(tc.ecosystem+"/"+tc.name, func(t *testing.T) {
			var paths []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.URL.Path)
				if tc.ecosystem == "swift" && r.URL.Path == strings.TrimSuffix(tc.path, "/"+tc.version) {
					_ = json.NewEncoder(w).Encode(map[string]any{"releases": map[string]any{tc.version: map[string]any{}}})
					return
				}
				if tc.ecosystem == "golang" && r.URL.Path == strings.TrimSuffix(tc.path, tc.version+".info")+"list" {
					_, _ = w.Write([]byte(tc.version + "\n"))
					return
				}
				if r.URL.Path != tc.path {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if tc.ecosystem == "swift" {
					_ = json.NewEncoder(w).Encode(swiftRelease(tc.version))
				} else {
					_ = json.NewEncoder(w).Encode(map[string]string{"Version": tc.version, "Time": "2026-01-01T00:00:00Z"})
				}
			}))
			defer server.Close()
			input := purl.BuildPURLString(tc.ecosystem, tc.name, tc.version, server.URL)
			version, err := registries.FetchVersionFromPURL(context.Background(), input, registries.NewClient(registries.WithMaxRetries(0)))
			if err != nil || version == nil || version.Number != tc.version {
				t.Fatalf("FetchVersionFromPURL(%q) = %+v, %v; requests=%v", input, version, err, paths)
			}
			if len(paths) == 0 || paths[len(paths)-1] != tc.path {
				t.Errorf("requests=%v, want %s", paths, tc.path)
			}
		})
	}
}
