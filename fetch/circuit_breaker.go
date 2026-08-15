package fetch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/cenk/backoff"
	circuit "github.com/rubyist/circuitbreaker"
)

const (
	cbInitialInterval = 30 * time.Second
	cbMaxInterval     = 5 * time.Minute
	cbThreshold       = 5
	maxURLTruncate    = 50
)

// CircuitBreakerFetcher wraps a Fetcher with per-registry circuit breakers.
type CircuitBreakerFetcher struct {
	fetcher  *Fetcher
	breakers map[string]*circuit.Breaker
	mu       sync.RWMutex
}

// NewCircuitBreakerFetcher creates a new circuit breaker wrapper for a fetcher.
func NewCircuitBreakerFetcher(f *Fetcher) *CircuitBreakerFetcher {
	return &CircuitBreakerFetcher{
		fetcher:  f,
		breakers: make(map[string]*circuit.Breaker),
	}
}

// getBreaker returns or creates a circuit breaker for the given registry.
func (cbf *CircuitBreakerFetcher) getBreaker(registry string) *circuit.Breaker {
	cbf.mu.RLock()
	breaker, exists := cbf.breakers[registry]
	cbf.mu.RUnlock()

	if exists {
		return breaker
	}

	cbf.mu.Lock()
	defer cbf.mu.Unlock()

	// Double-check after acquiring write lock
	if breaker, exists := cbf.breakers[registry]; exists {
		return breaker
	}

	// Create new circuit breaker with exponential backoff
	// Trips after 5 consecutive failures
	expBackoff := backoff.NewExponentialBackOff()
	expBackoff.InitialInterval = cbInitialInterval
	expBackoff.MaxInterval = cbMaxInterval
	expBackoff.Multiplier = 2.0
	expBackoff.Reset()

	opts := &circuit.Options{
		BackOff:    expBackoff,
		ShouldTrip: circuit.ThresholdTripFunc(cbThreshold),
	}
	breaker = circuit.NewBreakerWithOptions(opts)

	cbf.breakers[registry] = breaker
	return breaker
}

// Fetch wraps the underlying fetcher's Fetch with circuit breaker logic.
func (cbf *CircuitBreakerFetcher) Fetch(ctx context.Context, fetchURL string) (*Artifact, error) {
	return cbf.FetchWithHeaders(ctx, fetchURL, nil)
}

// FetchWithHeaders wraps the underlying fetcher's FetchWithHeaders with circuit breaker logic.
func (cbf *CircuitBreakerFetcher) FetchWithHeaders(ctx context.Context, fetchURL string, headers http.Header) (*Artifact, error) {
	// Extract registry from URL for circuit breaker selection
	registry := extractRegistry(fetchURL)
	breaker := cbf.getBreaker(registry)

	// Check if circuit is open
	if !breaker.Ready() {
		return nil, fmt.Errorf("circuit breaker open for registry %s: %w", registry, ErrUpstreamDown)
	}

	// Attempt fetch
	var artifact *Artifact
	var fetchErr error
	err := breaker.Call(func() error {
		artifact, fetchErr = cbf.fetcher.FetchWithHeaders(ctx, fetchURL, headers)
		if errors.Is(fetchErr, ErrNotFound) {
			return nil
		}
		return fetchErr
	}, 0)

	if err != nil {
		return nil, err
	}

	return artifact, fetchErr
}

// FetchObserved wraps the underlying fetcher's FetchObserved with circuit breaker logic.
func (cbf *CircuitBreakerFetcher) FetchObserved(ctx context.Context, fetchURL string) (*ObservedArtifact, error) {
	return cbf.FetchObservedWithHeaders(ctx, fetchURL, nil)
}

// FetchObservedWithHeaders wraps the underlying fetcher's FetchObservedWithHeaders with circuit breaker logic.
func (cbf *CircuitBreakerFetcher) FetchObservedWithHeaders(ctx context.Context, fetchURL string, headers http.Header) (*ObservedArtifact, error) {
	registry := extractRegistry(fetchURL)
	breaker := cbf.getBreaker(registry)

	if !breaker.Ready() {
		return nil, fmt.Errorf("circuit breaker open for registry %s: %w", registry, ErrUpstreamDown)
	}

	var artifact *ObservedArtifact
	var fetchErr error
	err := breaker.Call(func() error {
		artifact, fetchErr = cbf.fetcher.FetchObservedWithHeaders(ctx, fetchURL, headers)
		if errors.Is(fetchErr, ErrNotFound) {
			return nil
		}
		return fetchErr
	}, 0)

	if err != nil {
		return nil, err
	}

	return artifact, fetchErr
}

// Head wraps the underlying fetcher's Head with circuit breaker logic.
func (cbf *CircuitBreakerFetcher) Head(ctx context.Context, headURL string) (size int64, contentType string, err error) {
	registry := extractRegistry(headURL)
	breaker := cbf.getBreaker(registry)

	if !breaker.Ready() {
		return 0, "", fmt.Errorf("circuit breaker open for registry %s: %w", registry, ErrUpstreamDown)
	}

	var headErr error
	err = breaker.Call(func() error {
		size, contentType, headErr = cbf.fetcher.Head(ctx, headURL)
		if errors.Is(headErr, ErrNotFound) {
			return nil
		}
		return headErr
	}, 0)

	if err != nil {
		return 0, "", err
	}
	return size, contentType, headErr
}

// extractRegistry extracts a registry identifier from a URL for circuit breaker grouping.
func extractRegistry(rawURL string) string {
	// Parse URL and extract host for circuit breaker grouping
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		// Fallback to simple truncation
		if len(rawURL) > maxURLTruncate {
			return rawURL[:50]
		}
		return rawURL
	}
	return parsed.Host
}

// GetBreakerState returns the current state of circuit breakers (for health checks).
func (cbf *CircuitBreakerFetcher) GetBreakerState() map[string]string {
	cbf.mu.RLock()
	defer cbf.mu.RUnlock()

	states := make(map[string]string)
	for registry, breaker := range cbf.breakers {
		if breaker.Tripped() {
			states[registry] = "open"
		} else {
			states[registry] = "closed"
		}
	}
	return states
}
