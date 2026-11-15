package fetch

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/git-pkgs/registries"
	"github.com/git-pkgs/registries/client"
	_ "github.com/git-pkgs/registries/internal/rubygems"
)

type rubyGemsVersions map[string]string

func (v rubyGemsVersions) RoundTrip(req *http.Request) (*http.Response, error) {
	name := strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/api/v1/versions/"), ".json")
	body, ok := v[name]
	status := http.StatusOK
	if !ok {
		body, status = "Not Found", http.StatusNotFound
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
}

// The platform qualifier picks a gem's platform build. A ruby build installs
// on any platform, and a build for the platform asked for wins over it.
func TestResolveRubyGemsPlatform(t *testing.T) {
	builds := rubyGemsVersions{
		"jruby-launcher": `[{"number": "1.1.2", "platform": "java"}]`,
		"nokogiri":       `[{"number": "1.13.6", "platform": "ruby"}, {"number": "1.13.6", "platform": "x86_64-linux"}]`,
		"rake":           `[{"number": "13.2.1", "platform": "ruby"}]`,
	}
	httpClient := client.NewClient(client.WithHTTPClient(&http.Client{Transport: builds}))
	reg, err := registries.New("gem", "https://gems.example.test", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	r := NewResolver()
	r.RegisterRegistry(reg)

	tests := []struct {
		name, gem, version, platform, want string
	}{
		{"platform-only version", "jruby-launcher", "1.1.2", "java", "jruby-launcher-1.1.2-java.gem"},
		{"both, platform build wins", "nokogiri", "1.13.6", "x86_64-linux", "nokogiri-1.13.6-x86_64-linux.gem"},
		{"both, ruby asked for", "nokogiri", "1.13.6", "ruby", "nokogiri-1.13.6.gem"},
		{"both, other platform gets ruby", "nokogiri", "1.13.6", "arm64-darwin", "nokogiri-1.13.6.gem"},
		{"ruby-only version", "rake", "13.2.1", "x86_64-linux", "rake-13.2.1.gem"},
		{"no build for the platform", "jruby-launcher", "1.1.2", "x86_64-linux", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := r.ResolveWithOptions(context.Background(), "gem", tt.gem, tt.version,
				ResolveOptions{Qualifiers: map[string]string{"platform": tt.platform}})
			if tt.want == "" {
				if !errors.Is(err, ErrNoMatchingArtifact) {
					t.Fatalf("Resolve = %+v, %v; want ErrNoMatchingArtifact", info, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve failed: %v", err)
			}
			if info.Filename != tt.want || info.URL != "https://gems.example.test/downloads/"+tt.want {
				t.Errorf("got %s (%s), want %s", info.Filename, info.URL, tt.want)
			}
		})
	}
}
