package client

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	holdWithoutHeader    = time.Minute
	maxHoldWithoutHeader = 15 * time.Minute //nolint:mnd // doubling stops here
	// maxHold caps what an upstream's Retry-After can ask for.
	maxHold = time.Hour
)

// ErrUpstreamHeld marks a request refused locally during a rate-limit hold.
var ErrUpstreamHeld = errors.New("upstream requests are held by rate limiting")

// HeldError is returned in place of a response while a host is held.
type HeldError struct {
	Host       string
	Until      time.Time
	RetryAfter time.Duration
}

func (e *HeldError) Error() string {
	return fmt.Sprintf("%s is rate limiting requests, holding for %s", e.Host, e.RetryAfter.Round(time.Second))
}

func (e *HeldError) Is(target error) bool { return target == ErrUpstreamHeld }

type hostHold struct {
	until    time.Time
	fallback time.Duration
}

// HoldEvent describes a rate-limit response, including any longer active hold.
type HoldEvent struct {
	Host       string
	Until      time.Time
	RetryAfter time.Duration
	FromHeader bool
}

// RateLimitGate holds a host after a 429 or a 503 with Retry-After. Limited
// responses pass through; requests during a hold return HeldError. The first
// non-limited response after expiry releases the hold.
type RateLimitGate struct {
	base http.RoundTripper
	// Set callbacks before use. They run outside the lock and may run concurrently.
	OnHold    func(HoldEvent)
	OnRelease func(host string)
	now       func() time.Time

	mu    sync.Mutex
	holds map[string]*hostHold
}

// NewRateLimitGate wraps base. A nil base uses http.DefaultTransport.
func NewRateLimitGate(base http.RoundTripper) *RateLimitGate {
	if base == nil {
		base = http.DefaultTransport
	}
	return &RateLimitGate{
		base:  base,
		now:   time.Now,
		holds: make(map[string]*hostHold),
	}
}

// RoundTrip implements http.RoundTripper.
func (g *RateLimitGate) RoundTrip(req *http.Request) (*http.Response, error) {
	host := hostKey(req.URL)
	if until, held := g.heldUntil(host); held {
		return nil, &HeldError{Host: host, Until: until, RetryAfter: until.Sub(g.now())}
	}

	resp, err := g.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	retryAfter := resp.Header.Get("Retry-After")
	switch {
	case resp.StatusCode == http.StatusTooManyRequests,
		resp.StatusCode == http.StatusServiceUnavailable && retryAfter != "":
		g.hold(host, retryAfter)
	default:
		g.release(host)
	}
	return resp, nil
}

// Held returns a *HeldError when requests to rawURL's host are being held,
// and nil otherwise. It is safe to call on a nil gate.
func (g *RateLimitGate) Held(rawURL string) error {
	if g == nil {
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil
	}
	host := hostKey(u)
	until, held := g.heldUntil(host)
	if !held {
		return nil
	}
	return &HeldError{Host: host, Until: until, RetryAfter: until.Sub(g.now())}
}

// Holds returns the hosts currently held and when each hold ends. It is
// safe to call on a nil gate.
func (g *RateLimitGate) Holds() map[string]time.Time {
	if g == nil {
		return nil
	}
	now := g.now()
	g.mu.Lock()
	defer g.mu.Unlock()
	held := make(map[string]time.Time)
	for host, h := range g.holds {
		if now.Before(h.until) {
			held[host] = h.until
		}
	}
	return held
}

func (g *RateLimitGate) heldUntil(host string) (time.Time, bool) {
	now := g.now()
	g.mu.Lock()
	defer g.mu.Unlock()
	h, ok := g.holds[host]
	if !ok || !now.Before(h.until) {
		return time.Time{}, false
	}
	return h.until, true
}

func (g *RateLimitGate) hold(host, header string) {
	now := g.now()

	g.mu.Lock()
	h, ok := g.holds[host]
	if !ok {
		h = &hostHold{}
		g.holds[host] = h
	}
	// Responses already in flight when a hold starts all report the same
	// rate limiting; only a 429 after the hold ran out is a further strike.
	if !now.Before(h.until) {
		if h.fallback == 0 {
			h.fallback = holdWithoutHeader
		} else {
			h.fallback = min(h.fallback, maxHoldWithoutHeader/backoffBase) * backoffBase
		}
	}
	d, fromHeader := parseRetryAfter(header, now)
	if !fromHeader {
		d = h.fallback
	}
	d = min(d, maxHold)
	until := now.Add(d)
	if until.After(h.until) {
		h.until = until
	}
	event := HoldEvent{Host: host, Until: h.until, RetryAfter: h.until.Sub(now), FromHeader: fromHeader}
	g.mu.Unlock()

	if g.OnHold != nil {
		g.OnHold(event)
	}
}

// release drops host's hold once it has ended. Nothing is sent during a
// hold, so a success arriving while one is active was sent before it began.
func (g *RateLimitGate) release(host string) {
	now := g.now()
	g.mu.Lock()
	h, had := g.holds[host]
	if had && now.Before(h.until) {
		g.mu.Unlock()
		return
	}
	delete(g.holds, host)
	g.mu.Unlock()
	if had && g.OnRelease != nil {
		g.OnRelease(host)
	}
}

func hostKey(u *url.URL) string {
	if u == nil {
		return ""
	}
	return strings.ToLower(u.Host)
}
