package swift

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/git-pkgs/registries/internal/core"
)

const releaseJSON = `{
  "id": "apple.swift-nio",
  "version": "2.65.0",
  "resources": [
    {"name": "source-archive", "type": "application/zip",
     "checksum": "a2ac54cf25fbc1ad0028f03f0aa4b96833b83bb05a14e510892bb27dea4dc812"}
  ],
  "metadata": {
    "author": {"name": "Mona", "email": "mona@example.com", "organization": {"name": "Apple"}},
    "description": "Event-driven network application framework",
    "licenseURL": "https://github.com/apple/swift-nio/blob/main/LICENSE.txt",
    "repositoryURLs": ["git@github.com:apple/swift-nio.git", "https://github.com/apple/swift-nio.git"]
  },
  "publishedAt": "2024-04-02T10:00:00Z"
}`

func newServer(t *testing.T, handler http.HandlerFunc) *Registry {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); got != acceptHeader {
			t.Errorf("Accept = %q, want %q", got, acceptHeader)
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	return New(server.URL, core.DefaultClient())
}

func TestSplitName(t *testing.T) {
	valid := map[string][2]string{
		"apple/swift-nio":              {"apple", "swift-nio"},
		"apple.swift-nio":              {"apple", "swift-nio"},
		"mona/Linked_List":             {"mona", "Linked_List"},
		"a-b/c":                        {"a-b", "c"},
		"Mona.LinkedList2":             {"Mona", "LinkedList2"},
		strings.Repeat("a", 39) + "/x": {strings.Repeat("a", 39), "x"},
	}
	for name, want := range valid {
		scope, pkg, err := SplitName(name)
		if err != nil || scope != want[0] || pkg != want[1] {
			t.Errorf("SplitName(%q) = %q, %q, %v; want %q, %q", name, scope, pkg, err, want[0], want[1])
		}
	}

	invalid := []string{
		"github.com/apple/swift-argument-parser",
		"apple",
		"",
		"-apple/nio",
		"apple-/nio",
		"ap--ple/nio",
		"apple/_nio",
		"apple/ni__o",
		"apple/nio-",
		"app_le/nio",
		"apple/ni.o",
		strings.Repeat("a", 40) + "/x",
		"apple/" + strings.Repeat("n", 101),
		"äpple/nio",
	}
	for _, name := range invalid {
		if _, _, err := SplitName(name); !errors.Is(err, ErrInvalidName) {
			t.Errorf("SplitName(%q) err = %v, want ErrInvalidName", name, err)
		}
	}
}

func TestFetchVersions(t *testing.T) {
	reg := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/apple/swift-nio" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"releases": {
			"2.9.0": {"url": "https://example.com/apple/swift-nio/2.9.0"},
			"2.65.0": {},
			"2.10.0": {"problem": {"status": 410, "title": "Gone", "detail": "removed"}}
		}}`))
	})

	versions, err := reg.FetchVersions(context.Background(), "apple/swift-nio")
	if err != nil {
		t.Fatalf("FetchVersions failed: %v", err)
	}
	var got []string
	for _, v := range versions {
		got = append(got, v.Number)
	}
	if strings.Join(got, " ") != "2.65.0 2.10.0 2.9.0" {
		t.Errorf("versions = %v, want highest first", got)
	}
	if versions[1].Status != core.StatusYanked || versions[1].Metadata["problem_status"] != 410 {
		t.Errorf("2.10.0 = %+v, want yanked with problem status", versions[1])
	}
	if versions[0].Status != core.StatusNone {
		t.Errorf("2.65.0 status = %q, want none", versions[0].Status)
	}
}

func TestFetchVersionsFollowsPagination(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.RawQuery {
		case "":
			w.Header().Add("Link", `<`+server.URL+`/apple/swift-nio>; rel="latest-version"`)
			w.Header().Add("Link", `</apple/swift-nio?page=2>; rel="next", <https://elsewhere.example/apple/swift-nio?page=9>; rel="last"`)
			_, _ = w.Write([]byte(`{"releases": {"2.65.0": {}}}`))
		case "page=2":
			w.Header().Set("Link", `<https://elsewhere.example/apple/swift-nio?page=3>; rel="next"`)
			_, _ = w.Write([]byte(`{"releases": {"2.64.0": {}}}`))
		default:
			t.Errorf("unexpected request %s", r.URL)
		}
	}))
	defer server.Close()

	versions, err := New(server.URL, core.DefaultClient()).FetchVersions(context.Background(), "apple.swift-nio")
	if err != nil {
		t.Fatalf("FetchVersions failed: %v", err)
	}
	if len(versions) != 2 {
		t.Errorf("got %d versions, want 2 from two same-host pages", len(versions))
	}
}

func TestFetchVersionsPaginationLimit(t *testing.T) {
	for _, pages := range []int{maxPages, maxPages + 1} {
		t.Run(strconv.Itoa(pages), func(t *testing.T) {
			requests := 0
			reg := newServer(t, func(w http.ResponseWriter, r *http.Request) {
				requests++
				page, _ := strconv.Atoi(r.URL.Query().Get("page"))
				if page == 0 {
					page = 1
				}
				if page < pages {
					w.Header().Set("Link", fmt.Sprintf(`<?page=%d>; rel="next"`, page+1))
				}
				_, _ = fmt.Fprintf(w, `{"releases":{"1.0.%d":{}}}`, page)
			})

			versions, err := reg.FetchVersions(context.Background(), "mona/demo")
			if pages == maxPages {
				if err != nil || len(versions) != pages {
					t.Fatalf("FetchVersions returned %d versions, %v", len(versions), err)
				}
			} else if err == nil || versions != nil {
				t.Fatalf("FetchVersions returned %d versions, %v; want no partial results and an error", len(versions), err)
			}
			if requests != maxPages {
				t.Errorf("requests = %d, want %d", requests, maxPages)
			}
			if pages > maxPages {
				pkg, err := reg.FetchPackage(context.Background(), "mona/demo")
				if err == nil || pkg != nil {
					t.Errorf("FetchPackage = %+v, %v; want no package and an error", pkg, err)
				}
			}
		})
	}
}

func TestFetchVersion(t *testing.T) {
	reg := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/apple/swift-nio/2.65.0" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(releaseJSON))
	})

	v, err := reg.FetchVersion(context.Background(), "apple/swift-nio", "2.65.0")
	if err != nil {
		t.Fatalf("FetchVersion failed: %v", err)
	}
	wantIntegrity := "sha256-a2ac54cf25fbc1ad0028f03f0aa4b96833b83bb05a14e510892bb27dea4dc812"
	if v.Integrity != wantIntegrity {
		t.Errorf("Integrity = %q, want %q", v.Integrity, wantIntegrity)
	}
	if !v.PublishedAt.Equal(time.Date(2024, 4, 2, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("PublishedAt = %v", v.PublishedAt)
	}
	if len(v.Artifacts) != 1 {
		t.Fatalf("got %d artifacts, want 1", len(v.Artifacts))
	}
	a := v.Artifacts[0]
	if a.URL != reg.baseURL+"/apple/swift-nio/2.65.0.zip" || a.Filename != "2.65.0.zip" || a.Integrity != wantIntegrity || a.MediaType != "application/zip" {
		t.Errorf("artifact = %+v", a)
	}
}

func TestFetchVersionPrefersOriginalPublicationTime(t *testing.T) {
	reg := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id": "mona.LinkedList", "version": "1.0.0", "resources": [],
			"metadata": {"originalPublicationTime": "2020-01-01T00:00:00Z"}, "publishedAt": "2024-01-01T00:00:00Z"}`))
	})

	v, err := reg.FetchVersion(context.Background(), "mona/LinkedList", "1.0.0")
	if err != nil {
		t.Fatalf("FetchVersion failed: %v", err)
	}
	if v.PublishedAt.Year() != 2020 {
		t.Errorf("PublishedAt = %v, want originalPublicationTime", v.PublishedAt)
	}
	if v.Integrity != "" || len(v.Artifacts) != 0 {
		t.Errorf("release without a source archive = %+v", v)
	}
}

func TestFetchPackageAndMaintainers(t *testing.T) {
	var requested []string
	reg := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		requested = append(requested, r.URL.Path)
		switch r.URL.Path {
		case "/apple/swift-nio":
			_, _ = w.Write([]byte(`{"releases": {"2.64.0": {}, "2.65.0": {}, "3.0.0": {"problem": {"status": 410}}}}`))
		case "/apple/swift-nio/2.65.0":
			_, _ = w.Write([]byte(releaseJSON))
		default:
			http.NotFound(w, r)
		}
	})

	pkg, err := reg.FetchPackage(context.Background(), "apple/swift-nio")
	if err != nil {
		t.Fatalf("FetchPackage failed: %v", err)
	}
	if pkg.Name != "apple.swift-nio" || pkg.Namespace != "apple" || pkg.LatestVersion != "2.65.0" {
		t.Errorf("package = %+v", pkg)
	}
	if pkg.Repository != "https://github.com/apple/swift-nio" {
		t.Errorf("Repository = %q", pkg.Repository)
	}
	if pkg.Description != "Event-driven network application framework" {
		t.Errorf("Description = %q", pkg.Description)
	}

	maintainers, err := reg.FetchMaintainers(context.Background(), "apple/swift-nio")
	if err != nil {
		t.Fatalf("FetchMaintainers failed: %v", err)
	}
	if len(maintainers) != 1 || maintainers[0].Name != "Mona" || maintainers[0].Email != "mona@example.com" || maintainers[0].Role != "Apple" {
		t.Errorf("maintainers = %+v", maintainers)
	}
	for _, p := range requested {
		if strings.HasSuffix(p, "/3.0.0") {
			t.Errorf("fetched the unavailable release %s", p)
		}
	}
}

func TestNotFound(t *testing.T) {
	reg := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/apple/swift-nio/9.9.9":
			http.NotFound(w, r)
		case "/apple/swift-nio/1.0.0":
			w.WriteHeader(http.StatusGone)
			_, _ = w.Write([]byte(`{"status": 410, "detail": "removed"}`))
		default:
			http.NotFound(w, r)
		}
	})
	ctx := context.Background()

	var notFound *core.NotFoundError
	if _, err := reg.FetchVersions(ctx, "apple/missing"); !errors.As(err, &notFound) {
		t.Errorf("FetchVersions missing package err = %v, want NotFoundError", err)
	}
	if _, err := reg.FetchPackage(ctx, "apple/missing"); !errors.As(err, &notFound) {
		t.Errorf("FetchPackage missing package err = %v, want NotFoundError", err)
	}
	for _, version := range []string{"9.9.9", "1.0.0"} {
		_, err := reg.FetchVersion(ctx, "apple/swift-nio", version)
		if !errors.As(err, &notFound) || notFound.Version != version {
			t.Errorf("FetchVersion %s err = %v, want NotFoundError", version, err)
		}
	}
}

func TestInvalidNameMakesNoRequest(t *testing.T) {
	reg := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s", r.URL)
	})
	name := "github.com/apple/swift-argument-parser"
	if _, err := reg.FetchVersions(context.Background(), name); !errors.Is(err, ErrInvalidName) {
		t.Errorf("FetchVersions err = %v, want ErrInvalidName", err)
	}
	if _, err := reg.FetchVersion(context.Background(), name, "1.3.0"); !errors.Is(err, ErrInvalidName) {
		t.Errorf("FetchVersion err = %v, want ErrInvalidName", err)
	}
	if got := reg.URLs().Download(name, "1.3.0"); got != "" {
		t.Errorf("Download = %q, want empty", got)
	}
}

type tokenTransport struct{ base http.RoundTripper }

func (t tokenTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer secret")
	return t.base.RoundTrip(req)
}

func TestUsesSuppliedHTTPClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"releases": {"1.0.0": {}}}`))
	}))
	defer server.Close()

	client := core.DefaultClient()
	client.HTTPClient = &http.Client{Transport: tokenTransport{base: http.DefaultTransport}}
	versions, err := New(server.URL, client).FetchVersions(context.Background(), "mona/LinkedList")
	if err != nil || len(versions) != 1 {
		t.Errorf("FetchVersions = %v, %v; want one version through the authenticated client", versions, err)
	}
}

func TestURLs(t *testing.T) {
	u := New("https://packages.example.com/", nil).URLs()
	tests := [][2]string{
		{u.Registry("apple/swift-nio", ""), "https://packages.example.com/apple/swift-nio"},
		{u.Registry("apple.swift-nio", "2.65.0"), "https://packages.example.com/apple/swift-nio/2.65.0"},
		{u.Download("apple/swift-nio", "2.65.0"), "https://packages.example.com/apple/swift-nio/2.65.0.zip"},
		{u.Download("apple/swift-nio", ""), ""},
		{u.PURL("apple.swift-nio", "2.65.0"), "pkg:swift/apple/swift-nio@2.65.0"},
		{u.Documentation("apple/swift-nio", ""), ""},
	}
	for _, tt := range tests {
		if tt[0] != tt[1] {
			t.Errorf("got %q, want %q", tt[0], tt[1])
		}
	}
}

func TestParseLinks(t *testing.T) {
	links := parseLinks([]string{`<https://a.example/x?page=2>; rel="next", <https://a.example/x>;rel=latest-version`, `garbage`})
	if len(links) != 2 || links[0].url != "https://a.example/x?page=2" || links[0].rel != "next" || links[1].rel != "latest-version" {
		t.Errorf("links = %+v", links)
	}
}
