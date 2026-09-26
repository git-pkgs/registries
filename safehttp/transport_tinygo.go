//go:build tinygo

package safehttp

import (
	"errors"
	"fmt"
	"net/http"
)

func newClient(base *http.Client, _ Options) *http.Client {
	c := http.Client{Timeout: defaultTimeout}
	if base != nil {
		c = *base
	}
	// TinyGo transports bypass the dialer that enforces the address policy.
	c.Transport = unsupportedTransport{}
	return &c
}

type unsupportedTransport struct{}

func (unsupportedTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("safehttp: TinyGo cannot enforce the address policy: %w", errors.ErrUnsupported)
}
