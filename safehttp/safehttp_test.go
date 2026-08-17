package safehttp

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
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

func TestCheckHostIP_AllowPrivateHosts(t *testing.T) {
	opts := Options{AllowPrivateHosts: []string{
		" Registry.Internal.svc ",
		"registry-with-port.internal:8080",
		"registry-with-dot.internal.",
		"[fd00::1]",
		"",
	}}
	privateIP := net.ParseIP("10.0.0.1")

	for _, host := range []string{
		"registry.internal.svc",
		"REGISTRY-WITH-PORT.INTERNAL",
		"registry-with-dot.internal",
		"fd00::1",
	} {
		if err := CheckHostIP(host, privateIP, opts); err != nil {
			t.Errorf("CheckHostIP(%q, %s) = %v; want nil", host, privateIP, err)
		}
	}

	if err := CheckHostIP("other.example.com", privateIP, opts); err == nil {
		t.Error("unlisted host should not be allowed to use a private IP")
	}
	if err := CheckIP(privateIP, opts); err == nil {
		t.Error("CheckIP without a hostname should not apply AllowPrivateHosts")
	}
}

func TestHostIPCheckerReusesNormalizedAllowlist(t *testing.T) {
	hosts := []string{" Registry.Internal.svc "}
	checker := NewHostIPChecker(Options{AllowPrivateHosts: hosts})
	hosts[0] = "other.example.com"

	privateIP := net.ParseIP("10.0.0.1")
	if err := checker.Check("registry.internal.svc", privateIP); err != nil {
		t.Errorf("Check(registry.internal.svc, %s) = %v; want nil", privateIP, err)
	}
	if err := checker.Check("other.example.com", privateIP); err == nil {
		t.Error("mutating the source options changed the compiled allowlist")
	}
}

func TestCheckHostIP_AllowPrivateHostsKeepsOtherBlocks(t *testing.T) {
	opts := Options{AllowPrivateHosts: []string{"registry.internal.svc"}}
	for _, in := range []string{"127.0.0.1", "169.254.169.254", "fe80::1"} {
		if err := CheckHostIP("registry.internal.svc", net.ParseIP(in), opts); err == nil {
			t.Errorf("CheckHostIP(registry.internal.svc, %s) = nil; want error", in)
		}
	}
}

func TestGateDial_AllowPrivateHostIPLiteral(t *testing.T) {
	g := newGate(Options{AllowPrivateHosts: []string{"10.0.0.1"}})
	wantErr := errors.New("dial reached")
	called := false
	_, err := g.dial(context.Background(), "tcp", "10.0.0.1:80", func(_ context.Context, _, _ string) (net.Conn, error) {
		called = true
		return nil, wantErr
	})
	if !called {
		t.Fatal("allowlisted private IP did not reach the underlying dialer")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("dial error = %v; want %v", err, wantErr)
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

func TestClient_RedirectToUnlistedPrivateHostRefused(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://10.0.0.1/", http.StatusFound)
	}))
	defer ts.Close()

	c := New(&http.Client{Timeout: time.Second}, Options{
		AllowLoopback:     true,
		AllowPrivateHosts: []string{"registry.internal.svc"},
	})
	resp, err := c.Get(ts.URL)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("expected redirect to an unlisted private host to be refused")
	}
	if !strings.Contains(err.Error(), "private") {
		t.Errorf("error %v should mention the private-IP refusal", err)
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
