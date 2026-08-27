package fetch

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/cenk/backoff"
	"github.com/facebookgo/clock"
	circuit "github.com/rubyist/circuitbreaker"
)

const (
	cbInitialInterval = 30 * time.Second
	cbMaxInterval     = 5 * time.Minute
	cbThreshold       = 5

	// hostlessRegistryPrefix labels the identifier used for a URL with no host
	// to group by, and hostlessRegistryDigest is how many hex characters of the
	// URL's keyed digest follow it. See extractRegistry.
	hostlessRegistryPrefix = "hostless-url-"
	hostlessRegistryDigest = 32
)

// hostlessRegistryKey keys the digest that extractRegistry falls back to. It is
// drawn once per process, so identifiers stay stable for as long as the breakers
// they name, while nobody outside the process can reproduce a digest: neither to
// match an identifier against a guessed URL, nor to pick inputs that collide
// onto one breaker.
var hostlessRegistryKey = sync.OnceValue(func() []byte {
	key := make([]byte, sha256.BlockSize)
	// Read fills key or panics; it never returns a short read.
	_, _ = rand.Read(key)
	return key
})

// CircuitBreakerFetcher wraps a Fetcher with per-registry circuit breakers.
type CircuitBreakerFetcher struct {
	fetcher  *Fetcher
	breakers map[string]*circuit.Breaker
	mu       sync.RWMutex

	// clock is the time source for the breakers and their backoff, though not
	// for the failure-count window, which the breaker library keeps on the
	// system clock. A nil clock means the system clock; tests replace it to
	// advance time without waiting out a backoff interval.
	clock clock.Clock
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

	breakerClock := cbf.clock
	if breakerClock == nil {
		breakerClock = clock.New()
	}

	// Create new circuit breaker with exponential backoff. It trips once
	// cbThreshold failures land inside the breaker's rolling failure window.
	expBackoff := backoff.NewExponentialBackOff()
	expBackoff.InitialInterval = cbInitialInterval
	expBackoff.MaxInterval = cbMaxInterval
	expBackoff.Multiplier = 2.0
	// Retry forever, which is what the breaker library itself defaults to.
	// NewExponentialBackOff instead defaults MaxElapsedTime to 15 minutes, after
	// which NextBackOff returns backoff.Stop and the breaker never half-opens
	// again. Only a success resets the backoff, and the breaker no longer lets
	// one through, so an outage lasting longer than MaxElapsedTime leaves the
	// breaker open for the life of the process even after the registry recovers.
	expBackoff.MaxElapsedTime = 0
	expBackoff.Clock = breakerClock
	expBackoff.Reset()

	opts := &circuit.Options{
		BackOff:    expBackoff,
		Clock:      breakerClock,
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

	// Attempt fetch. Call checks the breaker itself; checking it here as well
	// would spend the probe this call is about to make. See breakerError.
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
		return nil, breakerError(registry, err)
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
		return nil, breakerError(registry, err)
	}

	return artifact, fetchErr
}

// Head wraps the underlying fetcher's Head with circuit breaker logic.
func (cbf *CircuitBreakerFetcher) Head(ctx context.Context, headURL string) (size int64, contentType string, err error) {
	registry := extractRegistry(headURL)
	breaker := cbf.getBreaker(registry)

	var headErr error
	err = breaker.Call(func() error {
		size, contentType, headErr = cbf.fetcher.Head(ctx, headURL)
		if errors.Is(headErr, ErrNotFound) {
			return nil
		}
		return headErr
	}, 0)

	if err != nil {
		return 0, "", breakerError(registry, err)
	}
	return size, contentType, headErr
}

// breakerError maps the breaker's own open-circuit error onto ErrUpstreamDown,
// so that every refusal to contact a registry reports the same way to callers,
// and passes errors from the fetch itself through untouched.
func breakerError(registry string, err error) error {
	if errors.Is(err, circuit.ErrBreakerOpen) {
		return fmt.Errorf("circuit breaker open for registry %s: %w", registry, ErrUpstreamDown)
	}
	return err
}

// extractRegistry extracts a registry identifier from a URL for circuit breaker
// grouping.
//
// Identifiers are externally observable: they name the registry in the errors
// callers see and are the keys of GetBreakerState, which callers publish. Fetch
// URLs, meanwhile, may hold secrets, since not all of them come from
// configuration. So a URL with no host to group by cannot fall back to the URL
// itself; it is grouped under a keyed digest of the URL, which discloses
// nothing yet still gives one breaker per distinct URL.
func extractRegistry(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		mac := hmac.New(sha256.New, hostlessRegistryKey())
		// Write never returns an error, as hash.Hash documents.
		_, _ = mac.Write([]byte(rawURL))
		return hostlessRegistryPrefix + hex.EncodeToString(mac.Sum(nil))[:hostlessRegistryDigest]
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
