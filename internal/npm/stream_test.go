package npm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/git-pkgs/registries/client"
	"github.com/git-pkgs/registries/internal/core"
)

// largePackument builds a packument in registry.npmjs.org key order with
// enough versions to pass the client's 10 MiB buffered limit. A long readme
// sits between versions and time, as in real packuments.
func largePackument(versions int) string {
	var b strings.Builder
	b.WriteString(`{"_id":"big","_rev":"1-abc","name":"big","dist-tags":{"latest":"1.0.`)
	fmt.Fprintf(&b, "%d", versions-1)
	b.WriteString(`"},"versions":{`)
	padding := strings.Repeat("x", 2000)
	for i := range versions {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `"1.0.%d":{"name":"big","version":"1.0.%d","description":"%s","license":"MIT",`+
			`"dependencies":{"dep":"^%d.0.0"},"dist":{"integrity":"sha512-v%d","tarball":"https://registry.npmjs.org/big/-/big-1.0.%d.tgz"}}`,
			i, i, padding, i, i, i)
	}
	b.WriteString(`},"readme":"` + strings.Repeat("r", 1<<20) + `","time":{"created":"2020-01-01T00:00:00.000Z"`)
	for i := range versions {
		fmt.Fprintf(&b, `,"1.0.%d":"2021-01-01T00:00:%02d.000Z"`, i, i%60)
	}
	b.WriteString(`},"maintainers":[{"name":"mona","email":"mona@example.com"}]}`)
	return b.String()
}

func TestLargePackument(t *testing.T) {
	doc := largePackument(6000)
	if len(doc) <= 10<<20 {
		t.Fatalf("fixture is %d bytes, want more than the 10 MiB buffered limit", len(doc))
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/big" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(doc))
	}))
	defer server.Close()
	reg := New(server.URL, core.DefaultClient())
	ctx := context.Background()

	versions, err := reg.FetchVersions(ctx, "big")
	if err != nil {
		t.Fatalf("FetchVersions failed: %v", err)
	}
	if len(versions) != 6000 {
		t.Fatalf("got %d versions, want 6000", len(versions))
	}
	v := versions[1234]
	if v.Number != "1.0.1234" || v.Integrity != "sha512-v1234" || v.Licenses != "MIT" {
		t.Errorf("version = %+v", v)
	}
	if want := time.Date(2021, 1, 1, 0, 0, 34, 0, time.UTC); !v.PublishedAt.Equal(want) {
		t.Errorf("PublishedAt = %v, want %v from time after versions", v.PublishedAt, want)
	}

	pkg, err := reg.FetchPackage(ctx, "big")
	if err != nil || pkg.LatestVersion != "1.0.5999" || pkg.Licenses != "MIT" || pkg.Name != "big" {
		t.Errorf("FetchPackage = %+v, %v", pkg, err)
	}

	one, err := reg.FetchVersion(ctx, "big", "1.0.42")
	if err != nil || one.Integrity != "sha512-v42" || one.PublishedAt.Second() != 42 {
		t.Errorf("FetchVersion = %+v, %v", one, err)
	}

	deps, err := reg.FetchDependencies(ctx, "big", "1.0.7")
	if err != nil || len(deps) != 1 || deps[0].Requirements != "^7.0.0" {
		t.Errorf("FetchDependencies = %+v, %v", deps, err)
	}

	maintainers, err := reg.FetchMaintainers(ctx, "big")
	if err != nil || len(maintainers) != 1 || maintainers[0].Login != "mona" {
		t.Errorf("FetchMaintainers = %+v, %v", maintainers, err)
	}

	var notFound *core.NotFoundError
	if _, err := reg.FetchVersion(ctx, "big", "9.9.9"); !errors.As(err, &notFound) || notFound.Version != "9.9.9" {
		t.Errorf("missing version err = %v", err)
	}
	if _, err := reg.FetchDependencies(ctx, "big", "9.9.9"); !errors.As(err, &notFound) {
		t.Errorf("missing version deps err = %v", err)
	}
	if _, err := reg.FetchVersions(ctx, "missing"); !errors.As(err, &notFound) {
		t.Errorf("missing package err = %v", err)
	}
}

func TestFetchPackageWithDistTagsAfterVersions(t *testing.T) {
	doc := `{"_id":"late","versions":{"1.0.0":{"description":"old"},"2.0.0":{"description":"new"}},"dist-tags":{"latest":"2.0.0"}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(doc))
	}))
	defer server.Close()

	pkg, err := New(server.URL, core.DefaultClient()).FetchPackage(context.Background(), "late")
	if err != nil || pkg.LatestVersion != "2.0.0" || pkg.Description != "new" {
		t.Errorf("FetchPackage = %+v, %v", pkg, err)
	}
}

func TestFetchPackageWithLatestMissingFromVersions(t *testing.T) {
	doc := `{"_id":"late","description":"top","versions":{"1.0.0":{"description":"old"}},"dist-tags":{"latest":"2.0.0"}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(doc))
	}))
	defer server.Close()

	pkg, err := New(server.URL, core.DefaultClient()).FetchPackage(context.Background(), "late")
	if err != nil || pkg.LatestVersion != "2.0.0" || pkg.Description != "top" {
		t.Errorf("FetchPackage = %+v, %v", pkg, err)
	}
}

func TestStreamPackumentErrors(t *testing.T) {
	tests := map[string]string{
		"truncated":              `{"_id":"x","versions":{"1.0.0":{"name":"x"`,
		"versions not an object": `{"_id":"x","versions":[1,2]}`,
		"not an object":          `[]`,
		"trailing garbage":       `{"_id":"x"}invalid`,
		"second value":           `{"_id":"x"}{}`,
	}
	for name, doc := range tests {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(doc))
			}))
			defer server.Close()
			if _, err := New(server.URL, core.DefaultClient()).FetchVersions(context.Background(), "x"); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestPackumentOverStreamLimit(t *testing.T) {
	doc := largePackument(50)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(doc))
	}))
	defer server.Close()

	c := client.NewClient(client.WithMaxStreamSize(int64(len(doc)/2)), client.WithMaxRetries(0))
	_, err := New(server.URL, c).FetchVersions(context.Background(), "big")
	if !errors.Is(err, client.ErrResponseTooLarge) {
		t.Errorf("err = %v, want ErrResponseTooLarge", err)
	}
}

func TestPackumentWhitespaceOverStreamLimit(t *testing.T) {
	doc := `{"_id":"x","versions":{}}` + strings.Repeat(" ", 1024)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(doc))
	}))
	defer server.Close()

	c := client.NewClient(client.WithMaxStreamSize(64))
	_, err := New(server.URL, c).FetchPackage(context.Background(), "x")
	if !errors.Is(err, client.ErrResponseTooLarge) {
		t.Errorf("err = %v, want ErrResponseTooLarge", err)
	}
}
