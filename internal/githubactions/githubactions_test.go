package githubactions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/git-pkgs/registries/internal/core"
)

func TestParseActionName(t *testing.T) {
	tests := []struct {
		name   string
		want   actionName
		wantOK bool
	}{
		{"actions/checkout", actionName{owner: "actions", repo: "checkout"}, true},
		{"github/codeql-action/init", actionName{owner: "github", repo: "codeql-action", path: "init"}, true},
		{"owner/repo/deep/sub/dir", actionName{owner: "owner", repo: "repo", path: "deep/sub/dir"}, true},
		{"/actions/checkout/", actionName{owner: "actions", repo: "checkout"}, true},
		{"checkout", actionName{}, false},
		{"actions/", actionName{}, false},
		{"", actionName{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseActionName(tt.name)
			if ok != tt.wantOK || got != tt.want {
				t.Errorf("parseActionName(%q) = %+v, %v; want %+v, %v", tt.name, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestFetchPackage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/github/codeql-action" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Accept"); got != mediaType {
			t.Errorf("Accept = %q, want %q", got, mediaType)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "full_name": "github/codeql-action",
  "description": "Actions for running CodeQL analysis",
  "homepage": "",
  "html_url": "https://github.com/github/codeql-action",
  "default_branch": "main",
  "archived": false,
  "topics": ["codeql", "security"],
  "license": {"spdx_id": "MIT"}
}`))
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	pkg, err := reg.FetchPackage(context.Background(), "github/codeql-action/init")
	if err != nil {
		t.Fatalf("FetchPackage failed: %v", err)
	}

	if pkg.Name != "github/codeql-action/init" {
		t.Errorf("Name = %q", pkg.Name)
	}
	if pkg.Description != "Actions for running CodeQL analysis" {
		t.Errorf("Description = %q", pkg.Description)
	}
	if pkg.Repository != "https://github.com/github/codeql-action" {
		t.Errorf("Repository = %q", pkg.Repository)
	}
	if pkg.Homepage != "https://github.com/github/codeql-action" {
		t.Errorf("Homepage = %q, want html_url fallback", pkg.Homepage)
	}
	if pkg.Licenses != "MIT" {
		t.Errorf("Licenses = %q", pkg.Licenses)
	}
	if pkg.Namespace != "github" {
		t.Errorf("Namespace = %q", pkg.Namespace)
	}
	if !reflect.DeepEqual(pkg.Keywords, []string{"codeql", "security"}) {
		t.Errorf("Keywords = %v", pkg.Keywords)
	}
	if pkg.Metadata["path"] != "init" || pkg.Metadata["default_branch"] != "main" {
		t.Errorf("Metadata = %v", pkg.Metadata)
	}
}

func TestFetchPackageIgnoresNoAssertionLicense(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"html_url": "https://github.com/a/b", "homepage": "https://b.example", "license": {"spdx_id": "NOASSERTION"}}`))
	}))
	defer server.Close()

	pkg, err := New(server.URL, core.DefaultClient()).FetchPackage(context.Background(), "a/b")
	if err != nil {
		t.Fatalf("FetchPackage failed: %v", err)
	}
	if pkg.Licenses != "" {
		t.Errorf("Licenses = %q, want empty", pkg.Licenses)
	}
	if pkg.Homepage != "https://b.example" {
		t.Errorf("Homepage = %q", pkg.Homepage)
	}
}

func TestFetchPackageNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()

	_, err := New(server.URL, core.DefaultClient()).FetchPackage(context.Background(), "missing/action")
	var notFound *core.NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("expected NotFoundError, got %v", err)
	}
}

func TestFetchPackageInvalidName(t *testing.T) {
	reg := New("", core.DefaultClient())
	if _, err := reg.FetchPackage(context.Background(), "checkout"); err == nil {
		t.Error("expected error for name without owner")
	}
	if _, err := reg.FetchVersions(context.Background(), "checkout"); err == nil {
		t.Error("expected error for name without owner")
	}
}

func TestFetchVersionsPaginates(t *testing.T) {
	var pages []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/actions/checkout/tags" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		page := r.URL.Query().Get("page")
		pages = append(pages, page)
		if got := r.URL.Query().Get("per_page"); got != "100" {
			t.Errorf("per_page = %q", got)
		}

		var tags []map[string]any
		count := tagsPerPage
		if page == "2" {
			count = 1
		}
		for i := range count {
			tags = append(tags, map[string]any{
				"name":   fmt.Sprintf("v%s.%d", page, i),
				"commit": map[string]string{"sha": fmt.Sprintf("sha-%s-%d", page, i)},
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(tags)
	}))
	defer server.Close()

	versions, err := New(server.URL, core.DefaultClient()).FetchVersions(context.Background(), "actions/checkout")
	if err != nil {
		t.Fatalf("FetchVersions failed: %v", err)
	}

	if !reflect.DeepEqual(pages, []string{"1", "2"}) {
		t.Errorf("requested pages %v, want [1 2]", pages)
	}
	if len(versions) != tagsPerPage+1 {
		t.Fatalf("got %d versions, want %d", len(versions), tagsPerPage+1)
	}
	last := versions[len(versions)-1]
	if last.Number != "v2.0" || last.Metadata["commit"] != "sha-2-0" {
		t.Errorf("last version = %+v", last)
	}
	if last.Integrity != "" {
		t.Errorf("Integrity = %q, want empty", last.Integrity)
	}
}

func TestFetchVersionsEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	versions, err := New(server.URL, core.DefaultClient()).FetchVersions(context.Background(), "a/b")
	if err != nil {
		t.Fatalf("FetchVersions failed: %v", err)
	}
	if len(versions) != 0 {
		t.Errorf("got %d versions, want 0", len(versions))
	}
}

func TestFetchVersionsNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()

	_, err := New(server.URL, core.DefaultClient()).FetchVersions(context.Background(), "missing/action")
	var notFound *core.NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("expected NotFoundError, got %v", err)
	}
}

func TestUnsupportedOperations(t *testing.T) {
	reg := New("", core.DefaultClient())
	if _, err := reg.FetchDependencies(context.Background(), "actions/checkout", "v4"); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("FetchDependencies error = %v, want ErrUnsupported", err)
	}
	if _, err := reg.FetchMaintainers(context.Background(), "actions/checkout"); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("FetchMaintainers error = %v, want ErrUnsupported", err)
	}
}

func TestURLBuilder(t *testing.T) {
	urls := New("", nil).URLs()
	sha := "8e5e7e5ab8b370d6c329ec480221332ada57f0ab"

	tests := []struct {
		name, version string
		registry      string
		download      string
		purl          string
	}{
		{
			"actions/checkout", "v4",
			"https://github.com/actions/checkout/tree/v4",
			"https://github.com/actions/checkout/archive/v4.tar.gz",
			"pkg:githubactions/actions/checkout@v4",
		},
		{
			"actions/checkout", "",
			"https://github.com/actions/checkout",
			"",
			"pkg:githubactions/actions/checkout",
		},
		{
			"actions/checkout", sha,
			"https://github.com/actions/checkout/tree/" + sha,
			"https://github.com/actions/checkout/archive/" + sha + ".tar.gz",
			"pkg:githubactions/actions/checkout@" + sha,
		},
		{
			"github/codeql-action/init", "v3",
			"https://github.com/github/codeql-action/tree/v3/init",
			"https://github.com/github/codeql-action/archive/v3.tar.gz",
			"pkg:githubactions/github/codeql-action/init@v3",
		},
		{
			"github/codeql-action/init", "",
			"https://github.com/github/codeql-action/tree/HEAD/init",
			"",
			"pkg:githubactions/github/codeql-action/init",
		},
		{
			"owner/repo", "releases/v1",
			"https://github.com/owner/repo/tree/releases/v1",
			"https://github.com/owner/repo/archive/releases/v1.tar.gz",
			"pkg:githubactions/owner/repo@releases%2Fv1",
		},
		{"checkout", "v4", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name+"@"+tt.version, func(t *testing.T) {
			if got := urls.Registry(tt.name, tt.version); got != tt.registry {
				t.Errorf("Registry() = %q, want %q", got, tt.registry)
			}
			if got := urls.Documentation(tt.name, tt.version); got != tt.registry {
				t.Errorf("Documentation() = %q, want %q", got, tt.registry)
			}
			if got := urls.Download(tt.name, tt.version); got != tt.download {
				t.Errorf("Download() = %q, want %q", got, tt.download)
			}
			if got := urls.PURL(tt.name, tt.version); got != tt.purl {
				t.Errorf("PURL() = %q, want %q", got, tt.purl)
			}
		})
	}
}

func TestEnterpriseWebURL(t *testing.T) {
	urls := New("https://ghe.example.com/api/v3/", nil).URLs()
	if got := urls.Download("org/action", "v1"); got != "https://ghe.example.com/org/action/archive/v1.tar.gz" {
		t.Errorf("Download() = %q", got)
	}
	if got := urls.Registry("org/action", ""); got != "https://ghe.example.com/org/action" {
		t.Errorf("Registry() = %q", got)
	}
}

func TestNewFromPURL(t *testing.T) {
	tests := []struct {
		purl         string
		wantName     string
		wantVersion  string
		wantDownload string
	}{
		{
			"pkg:githubactions/actions/checkout@v4", "actions/checkout", "v4",
			"https://github.com/actions/checkout/archive/v4.tar.gz",
		},
		{
			"pkg:githubactions/github/codeql-action/init@v3", "github/codeql-action/init", "v3",
			"https://github.com/github/codeql-action/archive/v3.tar.gz",
		},
	}
	for _, tt := range tests {
		t.Run(tt.purl, func(t *testing.T) {
			reg, name, version, err := core.NewFromPURL(tt.purl, nil)
			if err != nil {
				t.Fatalf("NewFromPURL failed: %v", err)
			}
			if reg.Ecosystem() != ecosystem || name != tt.wantName || version != tt.wantVersion {
				t.Errorf("NewFromPURL() = %s, %q, %q", reg.Ecosystem(), name, version)
			}
			if got := reg.URLs().Download(name, version); got != tt.wantDownload {
				t.Errorf("Download() = %q, want %q", got, tt.wantDownload)
			}
		})
	}
}

func TestEcosystem(t *testing.T) {
	if got := New("", nil).Ecosystem(); got != "githubactions" {
		t.Errorf("Ecosystem() = %q", got)
	}
}

func TestFetchVersionsStopsAtPageCap(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		tags := make([]map[string]any, tagsPerPage)
		for i := range tags {
			tags[i] = map[string]any{"name": fmt.Sprintf("v%d.%d", requests, i)}
		}
		_ = json.NewEncoder(w).Encode(tags)
	}))
	defer server.Close()

	_, err := New(server.URL, core.DefaultClient()).FetchVersions(context.Background(), "actions/checkout")
	if !errors.Is(err, core.ErrTooManyPages) {
		t.Errorf("err = %v, want ErrTooManyPages", err)
	}
	if requests != core.MaxPages {
		t.Errorf("made %d requests, want %d", requests, core.MaxPages)
	}
}
