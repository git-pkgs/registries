package fetch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/facebookgo/clock"
)

// A tripped breaker has to keep probing however long the registry stays down.
// backoff.NewExponentialBackOff defaults MaxElapsedTime to 15 minutes, and once
// NextBackOff returns backoff.Stop the breaker never half-opens again: it stays
// open for the life of the process even after the registry comes back, because
// only a success resets the backoff and no call gets through to produce one.
func TestCircuitBreakerRecoversAfterProlongedOutage(t *testing.T) {
	var down atomic.Bool
	down.Store(true)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if down.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	mockClock := clock.NewMock()
	cbFetcher := NewCircuitBreakerFetcher(NewFetcher(WithMaxRetries(0), WithBaseDelay(0)))
	cbFetcher.clock = mockClock

	ctx := context.Background()
	artifactURL := server.URL + "/test.tar.gz"
	registry := extractRegistry(artifactURL)

	for range cbThreshold {
		if _, err := cbFetcher.Fetch(ctx, artifactURL); err == nil {
			t.Fatal("expected a fetch against a failing registry to fail")
		}
	}
	if state := cbFetcher.GetBreakerState()[registry]; state != "open" {
		t.Fatalf("breaker state = %q, want open after %d failures", state, cbThreshold)
	}

	// The outage lasts an hour, with a probe on every retry that keeps failing.
	// Each step is longer than the 5 minute maximum backoff interval, so every
	// step lets one probe through.
	for range 6 {
		mockClock.Add(10 * time.Minute)
		if _, err := cbFetcher.Fetch(ctx, artifactURL); err == nil {
			t.Fatal("expected a fetch against a failing registry to fail")
		}
	}

	down.Store(false)
	mockClock.Add(cbMaxInterval * 2)

	artifact, err := cbFetcher.Fetch(ctx, artifactURL)
	if err != nil {
		t.Fatalf("fetch after the registry recovered: %v", err)
	}
	_ = artifact.Body.Close()

	if state := cbFetcher.GetBreakerState()[registry]; state != "closed" {
		t.Errorf("breaker state = %q, want closed after a successful fetch", state)
	}
}

// An open breaker lets exactly one request per backoff interval reach the
// registry, and refuses the rest without contacting it. Checking Ready() before
// Call() would spend the probe: Call() checks the breaker itself, and the first
// check advances the backoff, so the second one sees an interval that has not
// elapsed yet and refuses the very call it was meant to admit.
func TestCircuitBreakerProbesOncePerBackoffInterval(t *testing.T) {
	var requests atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	mockClock := clock.NewMock()
	cbFetcher := NewCircuitBreakerFetcher(NewFetcher(WithMaxRetries(0), WithBaseDelay(0)))
	cbFetcher.clock = mockClock

	ctx := context.Background()
	artifactURL := server.URL + "/test.tar.gz"

	for range cbThreshold {
		if _, err := cbFetcher.Fetch(ctx, artifactURL); err == nil {
			t.Fatal("expected a fetch against a failing registry to fail")
		}
	}

	// Each step is longer than the maximum backoff interval, so each one is
	// entitled to a probe.
	for step := range 5 {
		mockClock.Add(2 * cbMaxInterval)

		before := requests.Load()
		if _, err := cbFetcher.Fetch(ctx, artifactURL); !errors.Is(err, ErrUpstreamDown) {
			t.Fatalf("step %d: error = %v, want one wrapping ErrUpstreamDown", step, err)
		}
		if got := requests.Load() - before; got != 1 {
			t.Fatalf("step %d: %d requests reached the registry, want 1 probe", step, got)
		}

		// A second call in the same interval waits for the next one.
		before = requests.Load()
		_, err := cbFetcher.Fetch(ctx, artifactURL)
		if !errors.Is(err, ErrUpstreamDown) {
			t.Errorf("step %d: refusal = %v, want one wrapping ErrUpstreamDown", step, err)
		}
		if got := requests.Load() - before; got != 0 {
			t.Errorf("step %d: %d further requests reached the registry, want none", step, got)
		}
	}
}
