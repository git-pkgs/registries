//go:build !tinygo

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
	return serveDebIndexes(t, map[string]string{
		"/debian/dists/bookworm/main/binary-amd64/Packages": "Package: curl\nVersion: 7.88.1-10\nArchitecture: amd64\n" +
			"Filename: pool/main/c/curl/curl_7.88.1-10_amd64.deb\nSize: 3\nSHA256: aa\n",
		"/debian/dists/bookworm/main/binary-arm64/Packages": "Package: curl\nVersion: 7.88.1-10\nArchitecture: arm64\n" +
			"Filename: pool/main/c/curl/curl_7.88.1-10_arm64.deb\nSize: 4\nSHA256: bb\n",
	})
}

// serveDebIndexes serves indexes, by path, over TLS.
func serveDebIndexes(t *testing.T, indexes map[string]string) (string, *registries.Client) {
	t.Helper()
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

func debResolver(t *testing.T, base string, client *registries.Client, suites string) *Resolver {
	t.Helper()
	reg, err := registries.New("deb", base+"?suites="+suites+"&architectures=amd64", client)
	if err != nil {
		t.Fatal(err)
	}
	r := NewResolver(WithAddressPolicy(safehttp.Options{AllowLoopback: true}))
	r.RegisterRegistry(reg)
	return r
}

func TestResolveDebByRelease(t *testing.T) {
	base, client := serveDebIndexes(t, map[string]string{
		"/debian/dists/bookworm/main/binary-amd64/Packages": "Package: curl\nVersion: 7.88.1-10\nArchitecture: amd64\n" +
			"Filename: pool/main/c/curl/curl_7.88.1-10+deb12_amd64.deb\nSize: 3\nSHA256: aa\n",
		"/debian/dists/jammy/main/binary-amd64/Packages": "Package: curl\nVersion: 7.88.1-10\nArchitecture: amd64\n" +
			"Filename: pool/main/c/curl/curl_7.88.1-10+jammy_amd64.deb\nSize: 4\nSHA256: bb\n",
	})
	r := debResolver(t, base, client, "bookworm,jammy")

	// Scanners write the release where the index records the codename.
	for distro, want := range map[string]string{
		"debian-12":    "curl_7.88.1-10+deb12_amd64.deb",
		"debian-12.4":  "curl_7.88.1-10+deb12_amd64.deb",
		"bookworm":     "curl_7.88.1-10+deb12_amd64.deb",
		"ubuntu-22.04": "curl_7.88.1-10+jammy_amd64.deb",
	} {
		qualifiers := map[string]string{"arch": "amd64", "distro": distro}
		info, err := r.ResolveWithOptions(context.Background(), "deb", "debian/curl", "7.88.1-10",
			ResolveOptions{Qualifiers: qualifiers})
		if err != nil {
			t.Errorf("distro=%s: %v", distro, err)
			continue
		}
		if info.Filename != want {
			t.Errorf("distro=%s: got %s, want %s", distro, info.Filename, want)
		}
		if qualifiers["distro"] != distro {
			t.Errorf("distro=%s: the caller's qualifiers were changed to %v", distro, qualifiers)
		}
	}

	for _, distro := range []string{"debian-13", "debian-99"} {
		_, err := r.ResolveWithOptions(context.Background(), "deb", "debian/curl", "7.88.1-10",
			ResolveOptions{Qualifiers: map[string]string{"arch": "amd64", "distro": distro}})
		if !errors.Is(err, ErrNoMatchingArtifact) {
			t.Errorf("distro=%s: err = %v, want ErrNoMatchingArtifact", distro, err)
		}
	}
}

func TestResolveDebVersionWithoutEpoch(t *testing.T) {
	entry := func(name, version, file string) string {
		return "Package: " + name + "\nVersion: " + version + "\nArchitecture: amd64\n" +
			"Filename: pool/main/" + file + "\nSize: 3\nSHA256: aa\n\n"
	}
	base, client := serveDebIndexes(t, map[string]string{
		"/debian/dists/bookworm/main/binary-amd64/Packages": entry("openssh-client", "1:9.2p1-2+deb12u3", "openssh-client_9.2p1-2+deb12u3_amd64.deb") +
			entry("demo", "2:2.0-1", "demo_2.0-1_epoch2.deb") +
			entry("demo", "1:2.0-1", "demo_2.0-1_epoch1.deb") +
			entry("plain", "3.0-1", "plain_3.0-1_amd64.deb") +
			entry("both", "1:4.0-1", "both_4.0-1_epoch1.deb") +
			entry("both", "4.0-1", "both_4.0-1_epoch0.deb"),
	})
	r := debResolver(t, base, client, "bookworm")
	resolve := func(name, version, epoch string) (*ArtifactInfo, error) {
		var qualifiers map[string]string
		if epoch != "" {
			qualifiers = map[string]string{"epoch": epoch}
		}
		return r.ResolveWithOptions(context.Background(), "deb", "debian/"+name, version, ResolveOptions{Qualifiers: qualifiers})
	}

	for _, tt := range []struct{ name, version, epoch, want string }{
		{"openssh-client", "1:9.2p1-2+deb12u3", "", "openssh-client_9.2p1-2+deb12u3_amd64.deb"},
		{"openssh-client", "9.2p1-2+deb12u3", "", "openssh-client_9.2p1-2+deb12u3_amd64.deb"},
		{"openssh-client", "9.2p1-2+deb12u3", "1", "openssh-client_9.2p1-2+deb12u3_amd64.deb"},
		{"demo", "2.0-1", "2", "demo_2.0-1_epoch2.deb"},
		{"demo", "2.0-1", "1", "demo_2.0-1_epoch1.deb"},
		{"demo", "1:2.0-1", "", "demo_2.0-1_epoch1.deb"},
		// trivy's epoch qualifier is the one asked for.
		{"demo", "1:2.0-1", "2", "demo_2.0-1_epoch2.deb"},
		{"plain", "3.0-1", "", "plain_3.0-1_amd64.deb"},
		{"plain", "3.0-1", "0", "plain_3.0-1_amd64.deb"},
		{"plain", "0:3.0-1", "", "plain_3.0-1_amd64.deb"},
		{"both", "4.0-1", "0", "both_4.0-1_epoch0.deb"},
		{"both", "4.0-1", "1", "both_4.0-1_epoch1.deb"},
		{"both", "1:4.0-1", "", "both_4.0-1_epoch1.deb"},
	} {
		info, err := resolve(tt.name, tt.version, tt.epoch)
		if err != nil {
			t.Errorf("%s@%s epoch=%s: %v", tt.name, tt.version, tt.epoch, err)
			continue
		}
		if info.Filename != tt.want {
			t.Errorf("%s@%s epoch=%s: got %s, want %s", tt.name, tt.version, tt.epoch, info.Filename, tt.want)
		}
	}

	// A PURL that leaves the epoch out can mean either.
	for _, name := range []string{"demo", "both"} {
		version := map[string]string{"demo": "2.0-1", "both": "4.0-1"}[name]
		if _, err := resolve(name, version, ""); !errors.Is(err, ErrAmbiguousVersion) {
			t.Errorf("%s@%s with two epochs: err = %v, want ErrAmbiguousVersion", name, version, err)
		}
	}
	for _, tt := range []struct{ name, version, epoch string }{
		{"openssh-client", "9.2p1-2+deb12u3", "2"},
		{"openssh-client", "9.2p1", ""},
		{"plain", "3.0-1", "1"},
	} {
		if _, err := resolve(tt.name, tt.version, tt.epoch); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s@%s epoch=%s: err = %v, want ErrNotFound", tt.name, tt.version, tt.epoch, err)
		}
	}
}
