// Package safehttp builds an http.Client suitable for fetching from
// untrusted hosts. The transport applies three defences in concert:
//
//  1. Dial-time IP gate. DNS is resolved once per dial; each resolved
//     address is checked against the block list (loopback, RFC1918,
//     CGNAT 100.64.0.0/10, link-local, multicast, unspecified) before
//     any TCP connect. The connection then dials the resolved IP
//     directly, so a rebind between check and connect cannot escape
//     the gate.
//
//  2. Redirect cap. Go's default ignores chain length and re-trusts
//     each hop. CheckRedirect caps at 10 and re-validates every
//     redirect target.
//
//  3. Scheme gate on redirect. file://, gopher://, ftp://, data:// are
//     rejected — the only reason a registry would 30x to those is to
//     exfiltrate something.
//
// Threat model: a compromised registry, CDN, or maliciously-crafted
// URL that returns a 30x to http://localhost or an RFC1918 address
// should not be able to use this transport to probe internal services
// or exfiltrate local files. The gate is on every dial including
// redirect targets.
package safehttp

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

const (
	// MaxRedirects bounds the redirect chain length.
	MaxRedirects = 10

	defaultTimeout = 30 * time.Second
	dialTimeout    = 30 * time.Second
)

// Options configures a safehttp client. The zero value gives the
// production-strict gate; tests can opt parts of it off explicitly.
type Options struct {
	// AllowLoopback disables the loopback (127.0.0.0/8, ::1) check.
	// Test-only; never set in production paths.
	AllowLoopback bool

	// AllowPrivate disables the RFC1918 / ULA / CGNAT checks. Test-only.
	AllowPrivate bool
}

// testInsecure flips both AllowLoopback and AllowPrivate on at the
// gate. Set via EnableLoopbackForTesting from a test binary's TestMain
// so existing httptest-based test suites don't have to thread an
// Options flag through every constructor.
var testInsecure bool

// EnableLoopbackForTesting flips the SSRF dial gate's loopback and
// private-IP checks off for the calling test binary. Use in TestMain;
// never call from production code.
func EnableLoopbackForTesting() { testInsecure = true }

// New returns an http.Client that applies the SSRF defences described
// in the package doc. base may be nil; if non-nil its Timeout, Jar,
// and other non-Transport fields are preserved.
func New(base *http.Client, opts Options) *http.Client {
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

// CheckIP reports whether an IP is acceptable to dial under the
// supplied options. Exported so other transports (e.g. registries/
// fetch, which manages its own DialContext for DNS caching) can apply
// the same gate without wiring through a full safehttp client.
func CheckIP(ip net.IP, opts Options) error {
	return newGate(opts).check(ip)
}

type ipGate struct {
	allowLoopback bool
	allowPrivate  bool
}

func newGate(opts Options) *ipGate {
	return &ipGate{allowLoopback: opts.AllowLoopback, allowPrivate: opts.AllowPrivate}
}

func (g *ipGate) dial(ctx context.Context, network, addr string, dial func(context.Context, string, string) (net.Conn, error)) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}

	if ip := net.ParseIP(host); ip != nil {
		if err := g.check(ip); err != nil {
			return nil, err
		}
		return dial(ctx, network, addr)
	}

	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, ip := range ips {
		if err := g.check(ip.IP); err != nil {
			lastErr = err
			continue
		}
		conn, derr := dial(ctx, network, net.JoinHostPort(ip.IP.String(), port))
		if derr == nil {
			return conn, nil
		}
		lastErr = derr
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("safehttp: no addresses resolved for %s", host)
}

var cgnat = mustCIDR("100.64.0.0/10")

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

func (g *ipGate) check(ip net.IP) error {
	allowLoopback := g.allowLoopback || testInsecure
	allowPrivate := g.allowPrivate || testInsecure

	if ip.IsUnspecified() {
		return blockedErr(ip, "unspecified")
	}
	if ip.IsLoopback() && !allowLoopback {
		return blockedErr(ip, "loopback")
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return blockedErr(ip, "link-local")
	}
	if ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return blockedErr(ip, "multicast")
	}
	if !allowPrivate {
		if ip.IsPrivate() {
			return blockedErr(ip, "private")
		}
		if cgnat.Contains(ip) {
			return blockedErr(ip, "CGNAT")
		}
	}
	return nil
}

func blockedErr(ip net.IP, kind string) error {
	return fmt.Errorf("safehttp: refusing to connect to %s (%s)", ip, kind)
}

func validateRedirect(u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("safehttp: refusing redirect to scheme %q", u.Scheme)
	}
	return nil
}
