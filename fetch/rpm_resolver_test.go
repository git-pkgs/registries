//go:build !tinygo

package fetch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/git-pkgs/registries"
	_ "github.com/git-pkgs/registries/internal/rpm"
	"github.com/git-pkgs/registries/safehttp"
)

// rpmRepository serves a repository with one version of curl for two
// architectures, over TLS: the resolver only hands out https download URLs.
func rpmRepository(t *testing.T) (string, *registries.Client) {
	t.Helper()
	files := map[string]string{
		"/os/repodata/repomd.xml": `<repomd><data type="primary"><location href="repodata/primary.xml"/></data></repomd>`,
		"/os/repodata/primary.xml": `<metadata>
<package type="rpm"><name>curl</name><arch>x86_64</arch><version epoch="0" ver="8.15.0" rel="4.fc43"/>
  <checksum type="sha256">aa</checksum><size package="3"/><location href="Packages/c/curl-8.15.0-4.fc43.x86_64.rpm"/></package>
<package type="rpm"><name>curl</name><arch>aarch64</arch><version epoch="0" ver="8.15.0" rel="4.fc43"/>
  <checksum type="sha256">bb</checksum><size package="4"/><location href="Packages/c/curl-8.15.0-4.fc43.aarch64.rpm"/></package>
</metadata>`,
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	client := registries.DefaultClient()
	client.HTTPClient = server.Client()
	return server.URL + "/os", client
}

func TestResolveRPMByQualifiers(t *testing.T) {
	base, client := rpmRepository(t)
	reg, err := registries.New("rpm", base+"?distro=fedora-43", client)
	if err != nil {
		t.Fatal(err)
	}
	r := NewResolver(WithAddressPolicy(safehttp.Options{AllowLoopback: true}))
	r.RegisterRegistry(reg)

	info, err := r.ResolveWithOptions(context.Background(), "rpm", "fedora/curl", "8.15.0-4.fc43",
		ResolveOptions{Qualifiers: map[string]string{"arch": "aarch64", "distro": "fedora-43"}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if want := base + "/Packages/c/curl-8.15.0-4.fc43.aarch64.rpm"; info.URL != want || info.Integrity != "sha256-bb" || info.Size != 4 {
		t.Errorf("info = %+v, want %s", info, want)
	}

	_, err = r.ResolveWithOptions(context.Background(), "rpm", "fedora/curl", "8.15.0-4.fc43",
		ResolveOptions{Qualifiers: map[string]string{"arch": "x86_64", "distro": "fedora-42"}})
	if !errors.Is(err, ErrNoMatchingArtifact) {
		t.Errorf("other distro: err = %v, want ErrNoMatchingArtifact", err)
	}
}
