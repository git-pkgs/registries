package fetch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/git-pkgs/registries"
	_ "github.com/git-pkgs/registries/internal/packagist"
)

func TestResolvePackagistDistURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/packages/monolog/monolog.json" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"package":{"name":"monolog/monolog","versions":{"3.5.0":{"version":"3.5.0",
			"dist":{"type":"zip","url":"https://api.github.com/repos/Seldaek/monolog/zipball/c915e2634718dbc8a4a15c61b0e62e7a44e14448","shasum":"abc123"}}}}}`))
	}))
	defer server.Close()

	reg, err := registries.New("composer", server.URL, registries.DefaultClient())
	if err != nil {
		t.Fatal(err)
	}
	r := NewResolver()
	r.RegisterRegistry(reg)

	info, err := r.Resolve(context.Background(), "composer", "monolog/monolog", "3.5.0")
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if want := "https://api.github.com/repos/Seldaek/monolog/zipball/c915e2634718dbc8a4a15c61b0e62e7a44e14448"; info.URL != want {
		t.Errorf("URL = %q, want %q", info.URL, want)
	}
	if want := "c915e2634718dbc8a4a15c61b0e62e7a44e14448.zip"; info.Filename != want {
		t.Errorf("Filename = %q, want %q", info.Filename, want)
	}
	if info.Integrity != "sha1-abc123" {
		t.Errorf("Integrity = %q, want sha1-abc123", info.Integrity)
	}
}
