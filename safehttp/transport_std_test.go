//go:build !tinygo

package safehttp

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
)

// routeThroughTestProxy starts a forward proxy that answers every request
// itself, returns a base client whose transport sends requests to it, and
// resolves hostnames from hosts.
func routeThroughTestProxy(t *testing.T, hosts map[string]string) (*http.Client, *atomic.Int32) {
	t.Helper()
	var received atomic.Int32
	forward := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		received.Add(1)
		_, _ = w.Write([]byte("via proxy"))
	}))
	t.Cleanup(forward.Close)
	forwardURL, err := url.Parse(forward.URL)
	if err != nil {
		t.Fatal(err)
	}

	oldLookup := lookupIPAddr
	lookupIPAddr = func(_ context.Context, host string) ([]net.IPAddr, error) {
		if ip, ok := hosts[host]; ok {
			return []net.IPAddr{{IP: net.ParseIP(ip)}}, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	t.Cleanup(func() { lookupIPAddr = oldLookup })

	base := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(forwardURL)}}
	return base, &received
}

func TestClient_ForwardProxyChecksDestination(t *testing.T) {
	base, received := routeThroughTestProxy(t, map[string]string{
		"metadata.example.test": "169.254.169.254",
		"intranet.example.test": "10.1.2.3",
		"allowed.example.test":  "10.1.2.4",
		"public.example.test":   "203.0.113.10",
	})
	// The forward proxy listens on loopback, so the client has to be allowed
	// to dial it.
	c := New(base, Options{
		AllowLoopback:     true,
		AllowPrivateHosts: []string{"allowed.example.test", "10.9.9.9"},
	})

	for _, tt := range []struct {
		url     string
		blocked bool
	}{
		{"http://169.254.169.254/latest/meta-data/", true},
		{"https://169.254.169.254/latest/meta-data/", true},
		{"http://[fe80::1]/", true},
		{"http://metadata.example.test/", true},
		{"http://10.1.2.3/", true},
		{"http://intranet.example.test/", true},
		{"http://allowed.example.test/", false},
		{"http://10.9.9.9/", false},
		{"http://203.0.113.10/", false},
		{"http://public.example.test/", false},
		{"http://unresolvable.example.test/", false},
	} {
		before := received.Load()
		resp, err := c.Get(tt.url)
		if err == nil {
			_ = resp.Body.Close()
		}
		reached := received.Load() > before
		if tt.blocked {
			if err == nil || reached {
				t.Errorf("GET %s: err = %v, reached forward proxy = %v; want refused before the forward proxy", tt.url, err, reached)
			}
			continue
		}
		if err != nil || !reached {
			t.Errorf("GET %s: err = %v, reached forward proxy = %v; want it passed to the forward proxy", tt.url, err, reached)
		}
	}
}

func TestClient_DefaultTransportProxyIsGuarded(t *testing.T) {
	if http.DefaultTransport.(*http.Transport).Proxy == nil {
		t.Skip("http.DefaultTransport has no proxy func")
	}
	c := New(nil, Options{})
	if c.Transport.(*http.Transport).Proxy == nil {
		t.Fatal("proxy func was dropped")
	}
}

func TestGuardProxyKeepsDirectRequests(t *testing.T) {
	gate := newGate(Options{})
	if guardProxy(nil, gate) != nil {
		t.Error("a transport without a proxy func got one")
	}
	req := httptest.NewRequest(http.MethodGet, "http://169.254.169.254/", nil)
	proxyErr := errors.New("bad proxy config")
	guarded := guardProxy(func(*http.Request) (*url.URL, error) { return nil, proxyErr }, gate)
	if _, err := guarded(req); !errors.Is(err, proxyErr) {
		t.Errorf("err = %v, want the proxy func's own error", err)
	}
	direct := guardProxy(func(*http.Request) (*url.URL, error) { return nil, nil }, gate)
	if proxyURL, err := direct(req); proxyURL != nil || err != nil {
		t.Errorf("direct request: proxy = %v, err = %v; want nil, nil so the dial gate checks it", proxyURL, err)
	}
}
