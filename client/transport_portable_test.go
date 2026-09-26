package client_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/git-pkgs/registries"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPortableRegistryWithHTTPClient(t *testing.T) {
	calls := 0
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.String() != "https://registry.example.test/demo" || request.Header.Get("Accept") != "application/json" {
			t.Errorf("unexpected request: %s, headers: %v", request.URL, request.Header)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{
				"_id":"demo", "name":"demo", "description":"Fixture package",
				"dist-tags":{"latest":"1.0.0"},
				"versions":{"1.0.0":{"name":"demo","version":"1.0.0","license":"MIT"}}
			}`)),
		}, nil
	})
	client := registries.NewClient(registries.WithHTTPClient(&http.Client{Transport: transport}))
	registry, err := registries.New("npm", "https://registry.example.test", client)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := registry.FetchPackage(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Name != "demo" || pkg.Description != "Fixture package" || calls != 1 {
		t.Errorf("package = %+v, requests = %d", pkg, calls)
	}
}
