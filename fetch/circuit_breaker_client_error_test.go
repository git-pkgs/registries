package fetch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// A 4xx answer is the registry replying, not failing, so the breaker must stay
// closed however many of them arrive, and every answer must reach the caller.
func TestCircuitBreakerClientErrorsDoNotTrip(t *testing.T) {
	const body = "denied by policy"
	const contentType = "application/json"

	for _, code := range []int{
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusConflict,
		http.StatusGone,
	} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			var hits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				w.Header().Set("Content-Type", contentType)
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
				if status.ContentType != contentType {
					t.Fatalf("got content type %q, want %q", status.ContentType, contentType)
				}
			}

			if got := hits.Load(); got != attempts {
				t.Errorf("breaker swallowed requests: want %d upstream hits, got %d", attempts, got)
			}
			for registry, state := range cbFetcher.GetBreakerState() {
				if state != "closed" {
					t.Errorf("breaker for %s is %s, want closed", registry, state)
				}
			}
		})
	}

	// A 304 answers a conditional request, so it is the registry answering too.
	// The server sends neither a body nor a Content-Type with it.
	t.Run(http.StatusText(http.StatusNotModified), func(t *testing.T) {
		var hits atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			w.WriteHeader(http.StatusNotModified)
		}))
		defer server.Close()

		cbFetcher := NewCircuitBreakerFetcher(NewFetcher(WithMaxRetries(0), WithBaseDelay(0)))
		headers := http.Header{"If-None-Match": []string{`"etag"`}}

		const attempts = 20
		for range attempts {
			_, err := cbFetcher.FetchWithHeaders(context.Background(), server.URL+"/artifact.tar.gz", headers)
			var status *StatusError
			if !errors.As(err, &status) {
				t.Fatalf("want a StatusError passed through, got %v", err)
			}
			if status.Code != http.StatusNotModified || status.Body != "" || status.ContentType != "" {
				t.Fatalf("got status %d body %q content type %q, want 304 without body or content type",
					status.Code, status.Body, status.ContentType)
			}
		}

		if got := hits.Load(); got != attempts {
			t.Errorf("breaker swallowed requests: want %d upstream hits, got %d", attempts, got)
		}
		for registry, state := range cbFetcher.GetBreakerState() {
			if state != "closed" {
				t.Errorf("breaker for %s is %s, want closed", registry, state)
			}
		}
	})
}

// The observed fetch path shares the predicate, so a repeated 4xx must reach
// the caller there as well and leave the breaker closed.
func TestCircuitBreakerObservedClientErrorsDoNotTrip(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	cbFetcher := NewCircuitBreakerFetcher(NewFetcher(WithMaxRetries(0), WithBaseDelay(0)))

	const attempts = 20
	for range attempts {
		_, err := cbFetcher.FetchObservedWithHeaders(context.Background(), server.URL+"/artifact.tar.gz", nil)
		var status *StatusError
		if !errors.As(err, &status) || status.Code != http.StatusForbidden {
			t.Fatalf("want a 403 StatusError passed through, got %v", err)
		}
	}

	if got := hits.Load(); got != attempts {
		t.Errorf("breaker swallowed requests: want %d upstream hits, got %d", attempts, got)
	}
	for registry, state := range cbFetcher.GetBreakerState() {
		if state != "closed" {
			t.Errorf("breaker for %s is %s, want closed", registry, state)
		}
	}
}

func TestCircuitBreakerHeadClientErrorDoesNotTrip(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
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

	if got := hits.Load(); got != attempts {
		t.Errorf("breaker swallowed requests: want %d upstream hits, got %d", attempts, got)
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
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
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

	if got := hits.Load(); got >= attempts {
		t.Errorf("breaker never opened: %d upstream hits for %d requests", got, attempts)
	}
	for registry, state := range cbFetcher.GetBreakerState() {
		if state != "open" {
			t.Errorf("breaker for %s is %s, want open", registry, state)
		}
	}
}

// Head reports the statuses that mean "cannot answer" with the same sentinels
// as Fetch, so a rate limit or a server error still counts against the breaker.
func TestCircuitBreakerHeadUnavailableStillTrips(t *testing.T) {
	tests := []struct {
		name string
		code int
		want error
	}{
		{name: "rate limited", code: http.StatusTooManyRequests, want: ErrRateLimited},
		{name: "server error", code: http.StatusServiceUnavailable, want: ErrUpstreamDown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var hits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				w.WriteHeader(tt.code)
			}))
			defer server.Close()

			cbFetcher := NewCircuitBreakerFetcher(NewFetcher(WithMaxRetries(0), WithBaseDelay(0)))

			if _, _, err := cbFetcher.Head(context.Background(), server.URL+"/artifact.tar.gz"); !errors.Is(err, tt.want) {
				t.Fatalf("first Head = %v, want %v", err, tt.want)
			}

			// Well past the threshold. Once the breaker is open the answer is
			// ErrUpstreamDown for both statuses.
			const attempts = 20
			for range attempts {
				_, _, err := cbFetcher.Head(context.Background(), server.URL+"/artifact.tar.gz")
				if !errors.Is(err, tt.want) && !errors.Is(err, ErrUpstreamDown) {
					t.Fatalf("Head = %v, want %v or ErrUpstreamDown once the breaker is open", err, tt.want)
				}
			}

			if got := hits.Load(); got >= attempts {
				t.Errorf("breaker never opened: %d upstream hits for %d requests", got, attempts)
			}
			for registry, state := range cbFetcher.GetBreakerState() {
				if state != "open" {
					t.Errorf("breaker for %s is %s, want open", registry, state)
				}
			}
		})
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
