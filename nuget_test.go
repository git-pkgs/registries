package registries_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/git-pkgs/registries"
)

func TestNugetCatalogRedirects(t *testing.T) {
	for _, tt := range []struct {
		name          string
		target        string
		denyRedirect  bool
		wantIntegrity string
	}{
		{name: "same origin", target: "/catalog/leaf", wantIntegrity: "sha512-local"},
		{name: "other origin", target: "other"},
		{name: "later other origin", target: "/catalog/next"},
		{name: "caller policy", target: "/catalog/leaf", denyRedirect: true},
		{name: "redirect loop", target: "/catalog/start"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			testNugetCatalogRedirect(t, tt.target, tt.denyRedirect, tt.wantIntegrity)
		})
	}
}

func testNugetCatalogRedirect(t *testing.T, target string, denyRedirect bool, wantIntegrity string) {
	t.Helper()
	otherHits := 0
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		otherHits++
		_, _ = fmt.Fprint(w, `{"packageHash":"leaked","packageHashAlgorithm":"SHA512"}`)
	}))
	defer other.Close()
	if target == "other" {
		target = other.URL
	}
	var baseURL string
	redirectHits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/registration5-semver1/xunit/index.json":
			_, _ = fmt.Fprintf(w, `{"items":[{"items":[{"catalogEntry":{"@id":%q,"id":"xunit","version":"2.6.0","listed":true}}]}]}`, baseURL+"/catalog/start")
		case "/catalog/start":
			redirectHits++
			http.Redirect(w, r, target, http.StatusFound)
		case "/catalog/next":
			http.Redirect(w, r, other.URL, http.StatusFound)
		case "/catalog/leaf":
			_, _ = fmt.Fprint(w, `{"packageHash":"local","packageHashAlgorithm":"SHA512"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	baseURL = server.URL

	c := registries.NewClient(registries.WithMaxRetries(0))
	policyCalls := 0
	if denyRedirect {
		c.HTTPClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
			policyCalls++
			return errors.New("redirect denied by caller")
		}
	}
	reg, err := registries.New("nuget", baseURL, c)
	if err != nil {
		t.Fatal(err)
	}
	versions, err := reg.FetchVersions(context.Background(), "xunit")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 || versions[0].Integrity != wantIntegrity {
		t.Errorf("versions = %+v, want one version with integrity %q", versions, wantIntegrity)
	}
	if otherHits != 0 {
		t.Errorf("catalog fetched another origin %d times", otherHits)
	}
	if denyRedirect && policyCalls != 1 {
		t.Errorf("caller redirect policy called %d times, want 1", policyCalls)
	}
	if target == "/catalog/start" && redirectHits != 10 {
		t.Errorf("redirect loop made %d requests, want 10", redirectHits)
	}
	if !denyRedirect && c.HTTPClient.CheckRedirect != nil {
		t.Error("changed the shared client's redirect policy")
	}
}
