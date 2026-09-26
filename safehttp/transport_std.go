//go:build !tinygo

package safehttp

import (
	"context"
	"fmt"
	"net"
	"net/http"
)

func newClient(base *http.Client, opts Options) *http.Client {
	c := http.Client{Timeout: defaultTimeout}
	if base != nil {
		c = *base
	}

	transport, _ := http.DefaultTransport.(*http.Transport)
	transport = transport.Clone()
	if base != nil {
		if t, ok := base.Transport.(*http.Transport); ok && t != nil {
			transport = t.Clone()
		}
	}

	underlying := transport.DialContext
	if underlying == nil {
		d := &net.Dialer{Timeout: dialTimeout}
		underlying = d.DialContext
	}

	gate := newGate(opts)
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return gate.dial(ctx, network, addr, underlying)
	}
	c.Transport = transport

	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= MaxRedirects {
			return fmt.Errorf("safehttp: stopped after %d redirects", MaxRedirects)
		}
		return validateRedirect(req.URL)
	}
	return &c
}
