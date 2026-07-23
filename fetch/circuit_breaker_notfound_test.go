package fetch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCircuitBreakerNotFoundDoesNotTrip(t *testing.T) {
	hitCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitCount++
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	fetcher := NewFetcher(WithMaxRetries(0), WithBaseDelay(0))
	cbFetcher := NewCircuitBreakerFetcher(fetcher)
	ctx := context.Background()

	// well past the threshold
	for range 20 {
		_, err := cbFetcher.Fetch(ctx, server.URL+"/missing.tar.gz")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("want ErrNotFound passed through, got %v", err)
		}
	}

	if hitCount != 20 {
		t.Errorf("breaker swallowed requests: want 20 upstream hits, got %d", hitCount)
	}
	for registry, state := range cbFetcher.GetBreakerState() {
		if state != "closed" {
			t.Errorf("breaker for %s is %s, want closed", registry, state)
		}
	}
}

func TestCircuitBreakerHeadNotFoundDoesNotTrip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	fetcher := NewFetcher(WithMaxRetries(0), WithBaseDelay(0))
	cbFetcher := NewCircuitBreakerFetcher(fetcher)
	ctx := context.Background()

	for range 20 {
		_, _, err := cbFetcher.Head(ctx, server.URL+"/missing.tar.gz")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("want ErrNotFound passed through, got %v", err)
		}
	}

	for registry, state := range cbFetcher.GetBreakerState() {
		if state != "closed" {
			t.Errorf("breaker for %s is %s, want closed", registry, state)
		}
	}
}
