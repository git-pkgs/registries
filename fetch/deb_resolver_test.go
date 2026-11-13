package fetch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/git-pkgs/registries"
	_ "github.com/git-pkgs/registries/internal/deb"
	"github.com/git-pkgs/registries/safehttp"
)

// debArchive serves bookworm amd64 and arm64 plain Packages indexes with no
// Release, over TLS: the resolver only hands out https download URLs.
func debArchive(t *testing.T) (string, *registries.Client) {
	t.Helper()
	indexes := map[string]string{
		"/debian/dists/bookworm/main/binary-amd64/Packages": "Package: curl\nVersion: 7.88.1-10\nArchitecture: amd64\n" +
			"Filename: pool/main/c/curl/curl_7.88.1-10_amd64.deb\nSize: 3\nSHA256: aa\n",
		"/debian/dists/bookworm/main/binary-arm64/Packages": "Package: curl\nVersion: 7.88.1-10\nArchitecture: arm64\n" +
			"Filename: pool/main/c/curl/curl_7.88.1-10_arm64.deb\nSize: 4\nSHA256: bb\n",
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := indexes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	client := registries.DefaultClient()
	client.HTTPClient = server.Client()
	return server.URL + "/debian", client
}

func TestResolveDebByQualifiers(t *testing.T) {
	base, client := debArchive(t)
	reg, err := registries.New("deb", base+"?suites=bookworm&architectures=amd64,arm64", client)
	if err != nil {
		t.Fatal(err)
	}
	r := NewResolver(WithAddressPolicy(safehttp.Options{AllowLoopback: true}))
	r.RegisterRegistry(reg)

	info, err := r.ResolveWithOptions(context.Background(), "deb", "debian/curl", "7.88.1-10",
		ResolveOptions{Qualifiers: map[string]string{"arch": "arm64", "distro": "bookworm"}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if want := base + "/pool/main/c/curl/curl_7.88.1-10_arm64.deb"; info.URL != want || info.Integrity != "sha256-bb" || info.Size != 4 {
		t.Errorf("info = %+v, want %s", info, want)
	}

	_, err = r.ResolveWithOptions(context.Background(), "deb", "debian/curl", "7.88.1-10",
		ResolveOptions{Qualifiers: map[string]string{"arch": "amd64", "distro": "trixie"}})
	if !errors.Is(err, ErrNoMatchingArtifact) {
		t.Errorf("other distro: err = %v, want ErrNoMatchingArtifact", err)
	}
}
