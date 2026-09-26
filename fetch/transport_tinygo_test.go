//go:build tinygo

package fetch_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/git-pkgs/registries/fetch"
)

func TestTinyGoFetcherRequiresHTTPClient(t *testing.T) {
	original := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Error("default transport called without an injected client")
		return nil, errors.New("unexpected request")
	})
	t.Cleanup(func() { http.DefaultTransport = original })
	f := fetch.NewFetcher(fetch.WithAllowPrivateHosts("repo.example.test"))
	ctx := context.Background()
	const url = "https://repo.example.test/demo-1.0.pom"
	_, fetchErr := f.Fetch(ctx, url)
	_, observedErr := f.FetchObserved(ctx, url)
	_, _, headErr := f.Head(ctx, url)
	for _, err := range []error{fetchErr, observedErr, headErr} {
		if !errors.Is(err, errors.ErrUnsupported) {
			t.Errorf("request error = %v, want ErrUnsupported", err)
		}
	}
	for range 2 {
		if err := f.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}
}
