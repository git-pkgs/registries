package registries_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/git-pkgs/registries"
)

const (
	apmDigestOld = "1111111111111111111111111111111111111111111111111111111111111111"
	apmDigestNew = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
)

func newAPMRegistry(t *testing.T, handler http.HandlerFunc) (registries.Registry, string) { //nolint:ireturn
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	base := server.URL + "/api/apm"
	reg, err := registries.New("apm", base, registries.NewClient(registries.WithMaxRetries(0)))
	if err != nil {
		t.Fatal(err)
	}
	return reg, base
}

func TestAPMRegistry(t *testing.T) {
	reg, base := newAPMRegistry(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/api/apm/v1/packages/acme/web-skills/versions" {
			t.Errorf("unexpected request %s", r.URL.EscapedPath())
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprintf(w, `{"package":"acme/web-skills","versions":[
			{"version":"1.0.0","digest":"sha256:%s","published_at":"2026-01-02T03:04:05Z"},
			{"version":"main","digest":"sha256:%s","published_at":"2026-03-04T05:06:07.5Z","size_bytes":2048}
		]}`, apmDigestOld, apmDigestNew)
	})
	ctx := context.Background()

	versions, err := reg.FetchVersions(ctx, "acme/web-skills")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 || versions[0].Number != "main" || versions[1].Number != "1.0.0" {
		t.Fatalf("versions = %#v, want newest first", versions)
	}
	newest := versions[0]
	wantURL := base + "/v1/packages/acme/web-skills/versions/main/download"
	if newest.Integrity != "sha256-"+apmDigestNew ||
		!newest.PublishedAt.Equal(time.Date(2026, 3, 4, 5, 6, 7, 500_000_000, time.UTC)) ||
		len(newest.Artifacts) != 1 || newest.Artifacts[0].URL != wantURL ||
		newest.Artifacts[0].Integrity != newest.Integrity || newest.Artifacts[0].Size != 2048 {
		t.Errorf("newest = %#v", newest)
	}
	if versions[1].Artifacts[0].Size != 0 {
		t.Errorf("size without size_bytes = %d", versions[1].Artifacts[0].Size)
	}

	pkg, err := reg.FetchPackage(ctx, "acme/web-skills")
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Name != "acme/web-skills" || pkg.Namespace != "acme" || pkg.LatestVersion != "main" || pkg.Repository != "" {
		t.Errorf("package = %#v", pkg)
	}
}

func TestAPMRegistryNamespacedID(t *testing.T) {
	reg, base := newAPMRegistry(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/api/apm/v1/packages/group%2Fsub_group/my.repo/versions" {
			t.Errorf("unexpected request %s", r.URL.EscapedPath())
		}
		_, _ = fmt.Fprint(w, `{"package":"group/sub_group/my.repo","versions":[]}`)
	})
	pkg, err := reg.FetchPackage(context.Background(), "group/sub_group/my.repo")
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Namespace != "group/sub_group" || pkg.LatestVersion != "" {
		t.Errorf("package = %#v", pkg)
	}
	want := base + "/v1/packages/group%2Fsub_group/my.repo/versions/v1.0%2Brc%201/download"
	if got := reg.URLs().Download("group/sub_group/my.repo", "v1.0+rc 1"); got != want {
		t.Errorf("Download = %q, want %q", got, want)
	}
}

func TestAPMRegistryURLsAndIdentity(t *testing.T) {
	reg, base := newAPMRegistry(t, func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s", r.URL)
	})
	urls := reg.URLs()
	if got := urls.Registry("acme/web-skills", ""); got != base+"/v1/packages/acme/web-skills/versions" {
		t.Errorf("Registry = %q", got)
	}
	if urls.Download("acme/web-skills", "") != "" || urls.Download("acme/web-skills", "..") != "" ||
		urls.Documentation("acme/web-skills", "1.0.0") != "" || urls.PURL("acme/web-skills", "1.0.0") != "" {
		t.Error("unexpected inferred URL")
	}
	for _, name := range []string{"", "acme", "acme/", "/acme/repo", "acme//repo", "acme/../repo", "./repo"} {
		if _, err := reg.FetchVersions(context.Background(), name); err == nil {
			t.Errorf("accepted invalid identity %q", name)
		}
	}
	if _, err := registries.New("apm", "", nil); err == nil {
		t.Fatal("missing registry URL accepted")
	}
	for _, raw := range []string{"https://user@registry.example.com", "https://registry.example.com/api?", "https://registry.example.com/api#"} {
		bad, err := registries.New("apm", raw, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := bad.FetchVersions(context.Background(), "acme/web-skills"); err == nil || !strings.Contains(err.Error(), "invalid APM registry URL") {
			t.Errorf("registry URL %q: %v", raw, err)
		}
		if got := bad.URLs().Registry("acme/web-skills", ""); got != "" {
			t.Errorf("Registry for %q = %q", raw, got)
		}
	}
}

func TestAPMRegistryMalformed(t *testing.T) {
	valid := `"digest":"sha256:` + apmDigestNew + `","published_at":"2026-01-02T03:04:05Z"`
	for name, body := range map[string]string{
		"not JSON":          `<html>`,
		"missing versions":  `{"package":"acme/web-skills"}`,
		"other package":     `{"package":"acme/other","versions":[]}`,
		"missing package":   `{"versions":[]}`,
		"missing version":   `{"package":"acme/web-skills","versions":[{` + valid + `}]}`,
		"missing digest":    `{"package":"acme/web-skills","versions":[{"version":"1.0.0","published_at":"2026-01-02T03:04:05Z"}]}`,
		"bare digest":       `{"package":"acme/web-skills","versions":[{"version":"1.0.0","digest":"` + apmDigestNew + `","published_at":"2026-01-02T03:04:05Z"}]}`,
		"uppercase digest":  `{"package":"acme/web-skills","versions":[{"version":"1.0.0","digest":"sha256:` + strings.ToUpper(apmDigestNew) + `","published_at":"2026-01-02T03:04:05Z"}]}`,
		"camelCase time":    `{"package":"acme/web-skills","versions":[{"version":"1.0.0","digest":"sha256:` + apmDigestNew + `","publishedAt":"2026-01-02T03:04:05Z"}]}`,
		"invalid time":      `{"package":"acme/web-skills","versions":[{"version":"1.0.0","digest":"sha256:` + apmDigestNew + `","published_at":"yesterday"}]}`,
		"duplicate version": `{"package":"acme/web-skills","versions":[{"version":"1.0.0",` + valid + `},{"version":"1.0.0",` + valid + `}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			reg, _ := newAPMRegistry(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprint(w, body)
			})
			if versions, err := reg.FetchVersions(context.Background(), "acme/web-skills"); err == nil {
				t.Errorf("versions = %#v, want error", versions)
			}
		})
	}
}

func TestAPMRegistryErrors(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusUnauthorized, http.StatusForbidden, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			reg, _ := newAPMRegistry(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(status)
				_, _ = fmt.Fprintf(w, `{"title":"error","status":%d}`, status)
			})
			_, err := reg.FetchPackage(context.Background(), "acme/web-skills")
			if err == nil || errors.Is(err, registries.ErrNotFound) != (status == http.StatusNotFound) {
				t.Errorf("error = %v", err)
			}
		})
	}
}

func TestAPMRegistryUnsupported(t *testing.T) {
	reg, _ := newAPMRegistry(t, func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s", r.URL)
	})
	if deps, err := reg.FetchDependencies(context.Background(), "acme/web-skills", "1.0.0"); !errors.Is(err, errors.ErrUnsupported) || deps != nil {
		t.Errorf("dependencies = %#v, %v", deps, err)
	}
	if maintainers, err := reg.FetchMaintainers(context.Background(), "acme/web-skills"); !errors.Is(err, errors.ErrUnsupported) || maintainers != nil {
		t.Errorf("maintainers = %#v, %v", maintainers, err)
	}
}
