//go:build tinygo

package fetch

import (
	"errors"
	"fmt"
	"net/http"
)

func (f *Fetcher) initHTTPClient() {
	f.client = &http.Client{
		Timeout:   httpClientTimeout,
		Transport: unsupportedTransport{},
	}
}

type unsupportedTransport struct{}

func (unsupportedTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("fetch: TinyGo requires WithHTTPClient: %w", errors.ErrUnsupported)
}
