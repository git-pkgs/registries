package fetch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A 4xx answer is the registry replying, not failing, so the breaker must stay
// closed however many of them arrive, and every answer must reach the caller.
func TestCircuitBreakerClientErrorsDoNotTrip(t *testing.T) {
	const body = "denied by policy"

	for _, code := range []int{
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusConflict,
		http.StatusGone,
	} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			hits := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits++
				w.WriteHeader(code)
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()

			cbFetcher := NewCircuitBreakerFetcher(NewFetcher(WithMaxRetries(0), WithBaseDelay(0)))

			// Well past the threshold.
			const attempts = 20
			for range attempts {
				_, err := cbFetcher.Fetch(context.Background(), server.URL+"/artifact.tar.gz")
				var status *StatusError
				if !errors.As(err, &status) {
					t.Fatalf("want a StatusError passed through, got %v", err)
				}
				if status.Code != code || status.Body != body {
					t.Fatalf("got status %d body %q, want %d body %q", status.Code, status.Body, code, body)
				}
			}

			if hits != attempts {
				t.Errorf("breaker swallowed requests: want %d upstream hits, got %d", attempts, hits)
			}
			for registry, state := range cbFetcher.GetBreakerState() {
				if state != "closed" {
					t.Errorf("breaker for %s is %s, want closed", registry, state)
				}
			}
		})
	}
}

func TestCircuitBreakerHeadClientErrorDoesNotTrip(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	cbFetcher := NewCircuitBreakerFetcher(NewFetcher(WithMaxRetries(0), WithBaseDelay(0)))

	const attempts = 20
	for range attempts {
		_, _, err := cbFetcher.Head(context.Background(), server.URL+"/artifact.tar.gz")
		var status *StatusError
		if !errors.As(err, &status) || status.Code != http.StatusForbidden {
			t.Fatalf("want a 403 StatusError passed through, got %v", err)
		}
	}

	if hits != attempts {
		t.Errorf("breaker swallowed requests: want %d upstream hits, got %d", attempts, hits)
	}
	for registry, state := range cbFetcher.GetBreakerState() {
		if state != "closed" {
			t.Errorf("breaker for %s is %s, want closed", registry, state)
		}
	}
}

// A registry that cannot answer still trips the breaker, so later requests are
// short-circuited instead of hammering it.
func TestCircuitBreakerServerErrorsStillTrip(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	cbFetcher := NewCircuitBreakerFetcher(NewFetcher(WithMaxRetries(0), WithBaseDelay(0)))

	const attempts = 20
	for range attempts {
		if _, err := cbFetcher.Fetch(context.Background(), server.URL+"/artifact.tar.gz"); !errors.Is(err, ErrUpstreamDown) {
			t.Fatalf("want ErrUpstreamDown, got %v", err)
		}
	}

	if hits >= attempts {
		t.Errorf("breaker never opened: %d upstream hits for %d requests", hits, attempts)
	}
	for registry, state := range cbFetcher.GetBreakerState() {
		if state != "open" {
			t.Errorf("breaker for %s is %s, want open", registry, state)
		}
	}
}

func TestCircuitBreakerTransportErrorsStillTrip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := server.URL + "/artifact.tar.gz"
	server.Close()

	cbFetcher := NewCircuitBreakerFetcher(NewFetcher(WithMaxRetries(0), WithBaseDelay(0)))

	const attempts = 20
	for range attempts {
		if _, err := cbFetcher.Fetch(context.Background(), deadURL); err == nil {
			t.Fatal("want an error from a registry that cannot be reached, got nil")
		}
	}

	// Open now, so the next request fails without dialling.
	if _, err := cbFetcher.Fetch(context.Background(), deadURL); !errors.Is(err, ErrUpstreamDown) {
		t.Errorf("want ErrUpstreamDown once the breaker is open, got %v", err)
	}
	for registry, state := range cbFetcher.GetBreakerState() {
		if state != "open" {
			t.Errorf("breaker for %s is %s, want open", registry, state)
		}
	}
}

// Callers that read the status out of the message keep working.
func TestStatusErrorMessage(t *testing.T) {
	tests := []struct {
		name string
		err  *StatusError
		want string
	}{
		{
			name: "fetch keeps the status and body",
			err:  &StatusError{Code: http.StatusForbidden, Body: "denied by policy"},
			want: "unexpected status 403: denied by policy",
		},
		{
			name: "head has no body",
			err:  &StatusError{Code: http.StatusForbidden},
			want: "unexpected status 403",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("message = %q, want %q", got, tt.want)
			}
		})
	}
}
