package fetch

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/git-pkgs/registries"
	"github.com/git-pkgs/registries/client"
)

// listingRegistry lists versions without artifacts and returns them from
// FetchArtifacts, as the deb client does for snapshot.debian.org.
type listingRegistry struct {
	versions []registries.Version
	full     map[string]*registries.Version
	err      error
	fetched  []string
}

func (l *listingRegistry) Ecosystem() string       { return "deb" }
func (l *listingRegistry) URLs() client.URLBuilder { return &client.BaseURLs{} } //nolint:ireturn // satisfying Registry interface
func (l *listingRegistry) FetchVersions(ctx context.Context, name string) ([]registries.Version, error) {
	return l.versions, nil
}

func (l *listingRegistry) FetchArtifacts(ctx context.Context, name, version string) ([]registries.Artifact, error) {
	l.fetched = append(l.fetched, name+"@"+version)
	if l.err != nil {
		return nil, l.err
	}
	return l.full[version].Artifacts, nil
}

func TestResolveFetchesArtifactsOfAListedVersion(t *testing.T) {
	const number = "1:9.2p1-2+deb12u3"
	file := func(arch, hash string) registries.Artifact {
		return registries.Artifact{
			URL:       "https://snapshot.debian.org/file/" + hash,
			Filename:  "openssh-client_9.2p1-2+deb12u3_" + arch + ".deb",
			Integrity: "sha1-" + hash,
			Size:      1,
			Metadata:  map[string]any{"arch": arch},
		}
	}
	reg := &listingRegistry{
		versions: []registries.Version{{Number: "1:9.2p1-2+deb12u10"}, {Number: number}},
		full: map[string]*registries.Version{number: {Number: number, Artifacts: []registries.Artifact{
			file("amd64", "81171c850daaaedc27dabae3717dc20943ef371a"),
			file("arm64", "cbd60f5bdf4f576681cb75f888761abc94cb72ca"),
		}}},
	}
	r := NewResolver()
	r.RegisterRegistry(reg)

	// Given without its epoch, as scanners write it.
	info, err := r.ResolveWithOptions(context.Background(), "deb", "debian/openssh-client", "9.2p1-2+deb12u3",
		ResolveOptions{Qualifiers: map[string]string{"arch": "arm64"}})
	if err != nil {
		t.Fatal(err)
	}
	if info.URL != "https://snapshot.debian.org/file/cbd60f5bdf4f576681cb75f888761abc94cb72ca" ||
		info.Integrity != "sha1-cbd60f5bdf4f576681cb75f888761abc94cb72ca" {
		t.Errorf("info = %+v, want the arm64 file", info)
	}
	if want := []string{"debian/openssh-client@" + number}; !reflect.DeepEqual(reg.fetched, want) {
		t.Errorf("fetched %v, want %v", reg.fetched, want)
	}
}

func TestResolveReportsAFailedVersionFetch(t *testing.T) {
	boom := errors.New("boom")
	reg := &listingRegistry{versions: []registries.Version{{Number: "1.0"}}, err: boom}
	r := NewResolver()
	r.RegisterRegistry(reg)
	if _, err := r.ResolveWithOptions(context.Background(), "deb", "x", "1.0",
		ResolveOptions{Qualifiers: map[string]string{"arch": "amd64"}}); !errors.Is(err, boom) {
		t.Errorf("err = %v, want boom", err)
	}
}

func TestResolveKeepsListedArtifacts(t *testing.T) {
	listed := registries.Artifact{URL: "https://deb.debian.org/debian/pool/x.deb", Filename: "x.deb", Metadata: map[string]any{"arch": "amd64"}}
	reg := &listingRegistry{versions: []registries.Version{{Number: "1.0", Artifacts: []registries.Artifact{listed}}}}
	r := NewResolver()
	r.RegisterRegistry(reg)
	info, err := r.ResolveWithOptions(context.Background(), "deb", "x", "1.0",
		ResolveOptions{Qualifiers: map[string]string{"arch": "amd64"}})
	if err != nil || info.URL != listed.URL {
		t.Fatalf("info = %+v, %v", info, err)
	}
	if len(reg.fetched) != 0 {
		t.Errorf("fetched %v, want no FetchArtifacts for a version with artifacts", reg.fetched)
	}
}
