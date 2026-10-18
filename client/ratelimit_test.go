package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

type gateHarness struct {
	gate  *RateLimitGate
	now   time.Time
	calls map[string]int
	// respond returns the status and Retry-After the fake upstream answers with.
	respond func(req *http.Request) (int, string)
}

func newGateHarness() *gateHarness {
	h := &gateHarness{
		now:   time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC),
		calls: map[string]int{},
		respond: func(*http.Request) (int, string) {
			return http.StatusOK, ""
		},
	}
	h.gate = NewRateLimitGate(roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		h.calls[req.URL.Host]++
		status, retryAfter := h.respond(req)
		resp := &http.Response{
			StatusCode: status,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    req,
		}
		if retryAfter != "" {
			resp.Header.Set("Retry-After", retryAfter)
		}
		return resp, nil
	}))
	h.gate.now = func() time.Time { return h.now }
	return h
}

func (h *gateHarness) get(t *testing.T, rawURL string) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	resp, err := h.gate.RoundTrip(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	return resp, err
}

func TestRateLimitGate_HoldsHostForRetryAfter(t *testing.T) {
	h := newGateHarness()
	h.respond = func(*http.Request) (int, string) { return http.StatusTooManyRequests, "785" }

	resp, err := h.get(t, "https://repo1.maven.org/maven2/a/b/maven-metadata.xml")
	if err != nil || resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("first request: resp=%v err=%v, want the upstream 429 passed through", resp, err)
	}

	h.respond = func(*http.Request) (int, string) { return http.StatusOK, "" }
	_, err = h.get(t, "https://repo1.maven.org/maven2/a/b/b-1.0.jar")
	var held *HeldError
	if !errors.As(err, &held) || !errors.Is(err, ErrUpstreamHeld) {
		t.Fatalf("second request error = %v, want *HeldError", err)
	}
	if held.Host != "repo1.maven.org" || held.RetryAfter != 785*time.Second {
		t.Errorf("held = %+v, want repo1.maven.org for 785s", held)
	}
	if h.calls["repo1.maven.org"] != 1 {
		t.Errorf("upstream calls = %d, want 1: held requests must not reach it", h.calls["repo1.maven.org"])
	}

	if _, err := h.get(t, "https://plugins.gradle.org/m2/x.pom"); err != nil {
		t.Errorf("other host: %v, want unaffected", err)
	}

	holds := h.gate.Holds()
	if until, ok := holds["repo1.maven.org"]; !ok || !until.Equal(h.now.Add(785*time.Second)) {
		t.Errorf("Holds() = %v, want repo1.maven.org until now+785s", holds)
	}
}

func TestRateLimitGate_ReleasesWhenHoldExpiresAndUpstreamServes(t *testing.T) {
	h := newGateHarness()
	h.respond = func(*http.Request) (int, string) { return http.StatusTooManyRequests, "60" }
	_, _ = h.get(t, "https://repo1.maven.org/x")

	h.now = h.now.Add(59 * time.Second)
	if _, err := h.get(t, "https://repo1.maven.org/x"); !errors.Is(err, ErrUpstreamHeld) {
		t.Fatalf("at 59s: err = %v, want still held", err)
	}

	h.now = h.now.Add(time.Second)
	h.respond = func(*http.Request) (int, string) { return http.StatusOK, "" }
	resp, err := h.get(t, "https://repo1.maven.org/x")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("at 60s: resp=%v err=%v, want the request sent", resp, err)
	}
	if len(h.gate.Holds()) != 0 {
		t.Errorf("Holds() = %v after a success, want empty", h.gate.Holds())
	}
}

func TestRateLimitGate_LateSuccessKeepsHold(t *testing.T) {
	h := newGateHarness()
	// The slow request is in flight when the limited one starts the hold,
	// and its 200 arrives afterwards.
	h.respond = func(req *http.Request) (int, string) {
		if req.URL.Path == "/limited" {
			return http.StatusTooManyRequests, "120"
		}
		_, _ = h.get(t, "https://repo1.maven.org/limited")
		return http.StatusOK, ""
	}

	resp, err := h.get(t, "https://repo1.maven.org/slow")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("slow request: resp=%v err=%v, want its 200", resp, err)
	}
	if until, ok := h.gate.Holds()["repo1.maven.org"]; !ok || !until.Equal(h.now.Add(120*time.Second)) {
		t.Fatalf("Holds() = %v after a late 200, want repo1.maven.org until now+120s", h.gate.Holds())
	}
	if _, err := h.get(t, "https://repo1.maven.org/x"); !errors.Is(err, ErrUpstreamHeld) {
		t.Errorf("next request err = %v, want held", err)
	}
}

func TestRateLimitGate_Held(t *testing.T) {
	var nilGate *RateLimitGate
	if err := nilGate.Held("https://repo1.maven.org/x"); err != nil {
		t.Errorf("nil gate Held() = %v, want nil", err)
	}

	h := newGateHarness()
	if err := h.gate.Held("https://repo1.maven.org/x"); err != nil {
		t.Errorf("before any limit: Held() = %v, want nil", err)
	}
	h.respond = func(*http.Request) (int, string) { return http.StatusTooManyRequests, "45" }
	_, _ = h.get(t, "https://repo1.maven.org/x")

	var held *HeldError
	if err := h.gate.Held("https://REPO1.maven.org/a/b.jar"); !errors.As(err, &held) || held.RetryAfter != 45*time.Second {
		t.Errorf("Held() = %v, want *HeldError for 45s", err)
	}
	if err := h.gate.Held("https://plugins.gradle.org/m2/x.pom"); err != nil {
		t.Errorf("other host: Held() = %v, want nil", err)
	}
	h.now = h.now.Add(45 * time.Second)
	if err := h.gate.Held("https://repo1.maven.org/x"); err != nil {
		t.Errorf("after the hold: Held() = %v, want nil", err)
	}
}

func TestRateLimitGate_DefaultHoldDoublesWithoutHeader(t *testing.T) {
	h := newGateHarness()
	h.respond = func(*http.Request) (int, string) { return http.StatusTooManyRequests, "" }

	wantHolds := []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute}
	for i, want := range wantHolds {
		_, _ = h.get(t, "https://repo1.maven.org/x")
		until := h.gate.Holds()["repo1.maven.org"]
		if got := until.Sub(h.now); got != want {
			t.Errorf("strike %d: hold = %s, want %s", i+1, got, want)
		}
		h.now = until
	}
}

func TestRateLimitGate_ConcurrentLimitedResponsesAreOneStrike(t *testing.T) {
	h := newGateHarness()
	h.respond = func(*http.Request) (int, string) { return http.StatusTooManyRequests, "" }

	// Two requests already in flight when the limit starts both see 429.
	// The gate only consults the hold before sending, so drive the base
	// directly for the second.
	_, _ = h.get(t, "https://repo1.maven.org/x")
	req, _ := http.NewRequest(http.MethodGet, "https://repo1.maven.org/y", nil)
	h.gate.hold(hostKey(req.URL), "")

	if got := h.gate.Holds()["repo1.maven.org"].Sub(h.now); got != time.Minute {
		t.Errorf("hold = %s after two concurrent 429s, want the single-strike %s", got, time.Minute)
	}
}

func TestRateLimitGate_CapsUpstreamRetryAfter(t *testing.T) {
	h := newGateHarness()
	h.respond = func(*http.Request) (int, string) { return http.StatusTooManyRequests, "86400" }
	_, _ = h.get(t, "https://repo1.maven.org/x")
	if got := h.gate.Holds()["repo1.maven.org"].Sub(h.now); got != maxHold {
		t.Errorf("hold = %s, want capped at %s", got, maxHold)
	}
}

func TestRateLimitGate_ServiceUnavailableNeedsRetryAfter(t *testing.T) {
	h := newGateHarness()
	h.respond = func(*http.Request) (int, string) { return http.StatusServiceUnavailable, "" }
	_, _ = h.get(t, "https://repo1.maven.org/x")
	if len(h.gate.Holds()) != 0 {
		t.Errorf("503 without Retry-After created hold %v, want none", h.gate.Holds())
	}

	h.respond = func(*http.Request) (int, string) { return http.StatusServiceUnavailable, "30" }
	_, _ = h.get(t, "https://repo1.maven.org/x")
	if got := h.gate.Holds()["repo1.maven.org"].Sub(h.now); got != 30*time.Second {
		t.Errorf("hold = %s, want 30s from the 503's Retry-After", got)
	}
}

func TestRateLimitGate_ReportsHoldsAndNilSafe(t *testing.T) {
	var nilGate *RateLimitGate
	if holds := nilGate.Holds(); holds != nil {
		t.Errorf("nil gate Holds() = %v, want nil", holds)
	}

	h := newGateHarness()
	var reported []string
	h.gate.OnHold = func(event HoldEvent) {
		reported = append(reported, event.Host)
	}
	h.respond = func(*http.Request) (int, string) { return http.StatusTooManyRequests, "10" }
	_, _ = h.get(t, "https://repo1.maven.org/x")
	if len(reported) != 1 || reported[0] != "repo1.maven.org" {
		t.Errorf("OnHold reported %v, want [repo1.maven.org]", reported)
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		value string
		want  time.Duration
		ok    bool
	}{
		{"785", 785 * time.Second, true},
		{" 30 ", 30 * time.Second, true},
		{"0", 0, false},
		{"-5", 0, false},
		{"", 0, false},
		{"soon", 0, false},
		{"9223372036854775807", maxRetryAfter, true},
		{"999999999999999999999999999999", maxRetryAfter, true},
		{"99999999999999999999999999999x", 0, false},
		{now.Add(90 * time.Second).Format(http.TimeFormat), 90 * time.Second, true},
		{now.Add(-time.Minute).Format(http.TimeFormat), 0, false},
	}
	for _, tt := range tests {
		got, ok := parseRetryAfter(tt.value, now)
		if got != tt.want || ok != tt.ok {
			t.Errorf("parseRetryAfter(%q) = (%s, %v), want (%s, %v)", tt.value, got, ok, tt.want, tt.ok)
		}
	}
}

func TestRateLimitGateNeverOverflows(t *testing.T) {
	for _, header := range []string{"9223372036854775807", "999999999999999999999999999999"} {
		h := newGateHarness()
		h.respond = func(*http.Request) (int, string) { return http.StatusTooManyRequests, header }
		_, _ = h.get(t, "https://example.com/a")
		if got := h.gate.Holds()["example.com"].Sub(h.now); got != maxHold {
			t.Fatalf("header %s: hold = %s", header, got)
		}
	}
	h := newGateHarness()
	h.respond = func(*http.Request) (int, string) { return http.StatusTooManyRequests, "" }
	for i := range 100 {
		_, _ = h.get(t, "https://example.com/a")
		until := h.gate.Holds()["example.com"]
		if delay := until.Sub(h.now); delay <= 0 || delay > maxHoldWithoutHeader || (i >= 4 && delay != maxHoldWithoutHeader) {
			t.Fatalf("hold %d = %s", i, delay)
		}
		h.now = until
	}
}

func TestRateLimitGateCallbacksOutsideLock(t *testing.T) {
	h := newGateHarness()
	var event HoldEvent
	var released string
	h.gate.OnHold = func(e HoldEvent) { event = e; _ = h.gate.Holds() }
	h.gate.OnRelease = func(host string) { released = host; _ = h.gate.Holds() }
	h.respond = func(*http.Request) (int, string) {
		return http.StatusServiceUnavailable, h.now.Add(time.Minute).Format(http.TimeFormat)
	}
	_, _ = h.get(t, "https://example.com/a")
	if event.Host != "example.com" || event.RetryAfter != time.Minute || !event.FromHeader || !event.Until.Equal(h.now.Add(time.Minute)) {
		t.Fatalf("hold event = %+v", event)
	}
	h.now = event.Until
	h.respond = func(*http.Request) (int, string) { return http.StatusOK, "" }
	_, _ = h.get(t, "https://example.com/a")
	if released != "example.com" {
		t.Fatalf("release = %q", released)
	}
}

func TestClientReturnsHeldWithoutRetrying(t *testing.T) {
	h := newGateHarness()
	h.respond = func(*http.Request) (int, string) { return http.StatusTooManyRequests, "60" }
	_, _ = h.get(t, "https://example.com/a")
	c := NewClient(WithTransport(h.gate))
	c.BaseDelay = time.Hour
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := c.GetBody(ctx, "https://example.com/b")
	if !errors.Is(err, ErrUpstreamHeld) {
		t.Fatalf("GetBody = %v, want held error before retry delay", err)
	}
	if h.calls["example.com"] != 1 {
		t.Fatalf("upstream calls = %d", h.calls["example.com"])
	}
}

func TestClientRetryAfter(t *testing.T) {
	for _, header := range []string{"60", time.Now().Add(time.Minute).UTC().Format(http.TimeFormat), "9223372036854775807"} {
		calls := 0
		c := NewClient(WithTransport(roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{"Retry-After": {header}}, Body: io.NopCloser(strings.NewReader("limited")), Request: req}, nil
		})))
		c.BaseDelay = time.Hour
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, err := c.GetBody(ctx, "https://example.com/package")
		cancel()
		var limited *RateLimitError
		if !errors.As(err, &limited) || limited.RetryAfter <= 0 || calls != 1 {
			t.Fatalf("header %s: error %v, calls %d", header, err, calls)
		}
	}
}
