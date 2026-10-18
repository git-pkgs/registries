package fetch

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/git-pkgs/registries/client"
)

func TestCircuitBreakerHeldDoesNotTrip(t *testing.T) {
	for _, method := range []string{"fetch", "observed", "head"} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path == "/metadata" {
					w.Header().Set("Retry-After", "1")
					w.WriteHeader(http.StatusTooManyRequests)
					return
				}
				w.Header().Set("Content-Length", "8")
				w.Header().Set("Content-Type", "application/octet-stream")
				_, _ = io.WriteString(w, "artifact")
			}))
			defer server.Close()
			gate := client.NewRateLimitGate(server.Client().Transport)
			metadata := client.NewClient(client.WithTransport(gate))
			_, err := metadata.GetBody(context.Background(), server.URL+"/metadata")
			var limited *client.RateLimitError
			if !errors.As(err, &limited) {
				t.Fatalf("metadata error = %v, want rate limit", err)
			}
			var held *client.HeldError
			if err := gate.Held(server.URL); !errors.As(err, &held) {
				t.Fatalf("gate error = %v, want held", err)
			}
			f := NewFetcher(WithHTTPClient(&http.Client{Transport: gate}))
			defer func() { _ = f.Close() }()
			breaker := NewCircuitBreakerFetcher(f)
			for range cbThreshold + 1 {
				if err := checkHeldArtifact(t, breaker, server.URL+"/artifact", method); !errors.Is(err, client.ErrUpstreamHeld) {
					t.Fatalf("artifact error = %v, want held", err)
				}
			}
			if state := breaker.GetBreakerState()[extractRegistry(server.URL)]; state != "closed" {
				t.Fatalf("breaker = %s, want closed", state)
			}
			if got := calls.Load(); got != 1 {
				t.Fatalf("upstream calls during hold = %d, want 1", got)
			}
			time.Sleep(time.Until(held.Until))
			if err := checkHeldArtifact(t, breaker, server.URL+"/artifact", method); err != nil {
				t.Fatalf("artifact after hold expired: %v", err)
			}
			if got := calls.Load(); got != 2 {
				t.Fatalf("upstream calls after hold = %d, want 2", got)
			}
		})
	}
}

func checkHeldArtifact(t *testing.T, breaker *CircuitBreakerFetcher, url, method string) error {
	t.Helper()
	ctx := context.Background()
	if method == "head" {
		size, contentType, err := breaker.Head(ctx, url)
		if err == nil && (size != 8 || contentType != "application/octet-stream") {
			t.Fatalf("HEAD = %d, %q", size, contentType)
		}
		return err
	}
	var artifact *Artifact
	var observation *FetchObservation
	var err error
	if method == "observed" {
		var observed *ObservedArtifact
		observed, err = breaker.FetchObserved(ctx, url)
		if err == nil {
			artifact, observation = observed.Artifact, observed.Observation
		}
	} else {
		artifact, err = breaker.Fetch(ctx, url)
	}
	if err != nil {
		return err
	}
	defer func() { _ = artifact.Body.Close() }()
	body, err := io.ReadAll(artifact.Body)
	if err != nil || string(body) != "artifact" {
		t.Fatalf("body = %q, error = %v", body, err)
	}
	if observation != nil && (!observation.Complete || observation.ByteCount != int64(len(body))) {
		t.Fatalf("observation = %+v", observation)
	}
	return nil
}
