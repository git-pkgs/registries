//go:build !tinygo

package safehttp

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
)

// lookupIPAddr resolves the destination of a request sent through a forward
// proxy. It is a variable so tests can resolve names without DNS.
var lookupIPAddr = net.DefaultResolver.LookupIPAddr

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
	transport.Proxy = guardProxy(transport.Proxy, gate)
	c.Transport = transport

	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= MaxRedirects {
			return fmt.Errorf("safehttp: stopped after %d redirects", MaxRedirects)
		}
		return validateRedirect(req.URL)
	}
	return &c
}

// guardProxy wraps proxy so that a request sent through a forward proxy is
// held to the same address rules as a direct one. The dial gate only sees the
// address it connects to, which for a proxied request is the forward proxy.
//
// The destination is resolved here and every address must pass. A host that
// cannot be resolved locally is passed on, since networks that route all
// traffic through a forward proxy often resolve public names only there.
func guardProxy(proxy func(*http.Request) (*url.URL, error), gate *ipGate) func(*http.Request) (*url.URL, error) {
	if proxy == nil {
		return nil
	}
	return func(req *http.Request) (*url.URL, error) {
		proxyURL, err := proxy(req)
		if err != nil || proxyURL == nil {
			return proxyURL, err
		}
		if err := gate.checkProxied(req.Context(), req.URL.Hostname()); err != nil {
			return nil, err
		}
		return proxyURL, nil
	}
}

func (g *ipGate) checkProxied(ctx context.Context, host string) error {
	if ip := net.ParseIP(host); ip != nil {
		return g.checkHost(host, ip)
	}
	addrs, err := lookupIPAddr(ctx, host)
	if err != nil {
		return nil //nolint:nilerr // the forward proxy resolves names this host cannot
	}
	for _, addr := range addrs {
		if err := g.checkHost(host, addr.IP); err != nil {
			return err
		}
	}
	return nil
}
