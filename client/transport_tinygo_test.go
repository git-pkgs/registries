//go:build tinygo

package client_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/git-pkgs/registries"
)

func TestTinyGoWithSafeHTTP(t *testing.T) {
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Error("WithSafeHTTP called the unprotected transport")
		return nil, errors.New("unexpected request")
	})
	client := registries.NewClient(
		registries.WithHTTPClient(&http.Client{Transport: transport}),
		registries.WithSafeHTTP(),
		registries.WithMaxRetries(0),
	)
	registry, err := registries.New("npm", "https://registry.example.test", client)
	if err != nil {
		t.Fatal(err)
	}
	_, err = registry.FetchPackage(context.Background(), "demo")
	if !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("FetchPackage error = %v, want ErrUnsupported", err)
	}
}
