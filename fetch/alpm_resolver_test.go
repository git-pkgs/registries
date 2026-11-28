//go:build !tinygo

package fetch

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/git-pkgs/registries"
	_ "github.com/git-pkgs/registries/internal/alpm"
	"github.com/git-pkgs/registries/safehttp"
)

// alpmDatabase serves a sync database holding pacman for x86_64, over TLS:
// the resolver only hands out https download URLs.
func alpmDatabase(t *testing.T) (string, *registries.Client) {
	t.Helper()
	desc := "%FILENAME%\npacman-7.1.0-4-x86_64.pkg.tar.zst\n\n%NAME%\npacman\n\n%VERSION%\n7.1.0-4\n\n" +
		"%CSIZE%\n993248\n\n%SHA256SUM%\ne7b847d2989de38ec17ce9e2ca2a7b585ca61604dda0c73bc285b44005b234b1\n\n%ARCH%\nx86_64\n"
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "pacman-7.1.0-4/desc", Mode: 0o644, Size: int64(len(desc))})
	_, _ = tw.Write([]byte(desc))
	_ = tw.Close()
	_ = gz.Close()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/core/os/x86_64/core.db" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(buf.Bytes())
	}))
	t.Cleanup(server.Close)
	client := registries.DefaultClient()
	client.HTTPClient = server.Client()
	return server.URL + "/core/os/x86_64", client
}

func TestResolveALPM(t *testing.T) {
	dir, client := alpmDatabase(t)
	reg, err := registries.New("alpm", dir+"/core.db", client)
	if err != nil {
		t.Fatal(err)
	}
	r := NewResolver(WithAddressPolicy(safehttp.Options{AllowLoopback: true}))
	r.RegisterRegistry(reg)

	for _, qualifiers := range []map[string]string{nil, {"arch": "x86_64"}} {
		info, err := r.ResolveWithOptions(context.Background(), "alpm", "arch/pacman", "7.1.0-4",
			ResolveOptions{Qualifiers: qualifiers})
		if err != nil {
			t.Fatalf("Resolve with %v: %v", qualifiers, err)
		}
		want := ArtifactInfo{
			URL:       dir + "/pacman-7.1.0-4-x86_64.pkg.tar.zst",
			Filename:  "pacman-7.1.0-4-x86_64.pkg.tar.zst",
			Integrity: "sha256-e7b847d2989de38ec17ce9e2ca2a7b585ca61604dda0c73bc285b44005b234b1",
			Size:      993248,
		}
		if *info != want {
			t.Errorf("info with %v = %+v, want %+v", qualifiers, info, want)
		}
	}

	_, err = r.ResolveWithOptions(context.Background(), "alpm", "arch/pacman", "7.1.0-4",
		ResolveOptions{Qualifiers: map[string]string{"arch": "aarch64"}})
	if !errors.Is(err, ErrNoMatchingArtifact) {
		t.Errorf("aarch64 against x86_64: err = %v, want ErrNoMatchingArtifact", err)
	}
}
