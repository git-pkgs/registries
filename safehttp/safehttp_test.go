package safehttp

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestCheckIP_Blocks(t *testing.T) {
	g := newGate(Options{})
	cases := map[string]string{
		"127.0.0.1":       "loopback",
		"127.255.255.255": "loopback",
		"::1":             "loopback",
		"10.0.0.1":        "private",
		"172.16.5.4":      "private",
		"192.168.1.1":     "private",
		"169.254.169.254": "link-local",
		"100.64.0.1":      "CGNAT",
		"0.0.0.0":         "unspecified",
		"fc00::1":         "private",
		"fe80::1":         "link-local",
		"ff02::1":         "link-local", // link-local multicast: link-local check wins
		"239.255.255.250": "multicast",  // SSDP — multicast but not link-local
	}
	for in, wantKind := range cases {
		ip := net.ParseIP(in)
		if ip == nil {
			t.Fatalf("bad test IP %q", in)
		}
		err := g.check(ip)
		if err == nil {
			t.Errorf("checkIP(%s) = nil; want %s", in, wantKind)
			continue
		}
		if !strings.Contains(err.Error(), wantKind) {
			t.Errorf("checkIP(%s) = %v; want kind %q", in, err, wantKind)
		}
	}
}

func TestCheckIP_Allows(t *testing.T) {
	g := newGate(Options{})
	for _, in := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		ip := net.ParseIP(in)
		if err := g.check(ip); err != nil {
			t.Errorf("checkIP(%s) = %v; want nil", in, err)
		}
	}
}

func TestCheckIP_AllowLoopbackOptOut(t *testing.T) {
	g := newGate(Options{AllowLoopback: true})
	if err := g.check(net.ParseIP("127.0.0.1")); err != nil {
		t.Errorf("with AllowLoopback, checkIP(127.0.0.1) = %v; want nil", err)
	}
	if err := g.check(net.ParseIP("10.0.0.1")); err == nil {
		t.Error("AllowLoopback must not relax the private-IP check")
	}
}

// CheckIP is the exported entry point; assert it agrees with the
// internal gate.
func TestCheckIP_Exported(t *testing.T) {
	if err := CheckIP(net.ParseIP("127.0.0.1"), Options{}); err == nil {
		t.Error("CheckIP(127.0.0.1) under default options should fail")
	}
	if err := CheckIP(net.ParseIP("8.8.8.8"), Options{}); err != nil {
		t.Errorf("CheckIP(8.8.8.8) under default options should pass; err=%v", err)
	}
}

func TestClient_LoopbackRefused(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	c := New(nil, Options{})
	resp, err := c.Get(ts.URL)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatalf("expected loopback to be refused, got 200")
	}
	if !strings.Contains(err.Error(), "loopback") {
		t.Errorf("error %v should mention loopback", err)
	}
}

func TestClient_LoopbackAllowed(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	c := New(nil, Options{AllowLoopback: true})
	resp, err := c.Get(ts.URL)
	if err != nil {
		t.Fatalf("AllowLoopback: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Errorf("status %d, want 200", resp.StatusCode)
	}
}

func TestClient_RedirectCap(t *testing.T) {
	var hits int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.Redirect(w, r, "/", http.StatusFound)
	}))
	defer ts.Close()

	c := New(nil, Options{AllowLoopback: true})
	resp, err := c.Get(ts.URL)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatalf("expected redirect-cap error")
	}
	if !strings.Contains(err.Error(), "stopped after") {
		t.Errorf("error %v should mention the redirect cap", err)
	}
	if hits < MaxRedirects {
		t.Errorf("expected at least %d hits before bail, got %d", MaxRedirects, hits)
	}
}

func TestClient_BadSchemeRedirect(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "file:///etc/passwd")
		w.WriteHeader(http.StatusFound)
	}))
	defer ts.Close()

	c := New(nil, Options{AllowLoopback: true})
	resp, err := c.Get(ts.URL)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatalf("expected scheme rejection on redirect")
	}
	if !strings.Contains(err.Error(), "refusing redirect to scheme") {
		t.Errorf("error %v should mention scheme rejection", err)
	}
}

func TestValidateRedirect(t *testing.T) {
	for _, scheme := range []string{"file", "gopher", "ftp", "data"} {
		u, _ := url.Parse(scheme + "://x/")
		if err := validateRedirect(u); err == nil {
			t.Errorf("scheme %q should be refused", scheme)
		}
	}
	for _, scheme := range []string{"http", "https"} {
		u, _ := url.Parse(scheme + "://example.com/")
		if err := validateRedirect(u); err != nil {
			t.Errorf("scheme %q should be allowed: %v", scheme, err)
		}
	}
}
