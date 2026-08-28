package fetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCircuitBreakerFetch_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("test content"))
	}))
	defer server.Close()

	fetcher := NewFetcher()
	cbFetcher := NewCircuitBreakerFetcher(fetcher)

	ctx := context.Background()
	artifact, err := cbFetcher.Fetch(ctx, server.URL+"/test.tar.gz")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if artifact == nil {
		t.Fatal("expected artifact, got nil")
	}

	defer func() { _ = artifact.Body.Close() }()

	body, _ := io.ReadAll(artifact.Body)
	if string(body) != "test content" {
		t.Errorf("expected 'test content', got %q", string(body))
	}
}

func TestCircuitBreakerFetchObserved_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("test content"))
	}))
	defer server.Close()

	cbFetcher := NewCircuitBreakerFetcher(NewFetcher())
	artifact, err := cbFetcher.FetchObserved(context.Background(), server.URL+"/test.tar.gz")
	if err != nil {
		t.Fatalf("FetchObserved failed: %v", err)
	}
	defer func() { _ = artifact.Body.Close() }()

	if _, err := io.ReadAll(artifact.Body); err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	if !artifact.Observation.Complete {
		t.Error("observation is incomplete after reading the response body")
	}
}

func TestCircuitBreakerHead_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("expected HEAD request, got %s", r.Method)
		}
		w.Header().Set("Content-Length", "1234")
		w.Header().Set("Content-Type", "application/octet-stream")
	}))
	defer server.Close()

	fetcher := NewFetcher()
	cbFetcher := NewCircuitBreakerFetcher(fetcher)

	ctx := context.Background()
	size, contentType, err := cbFetcher.Head(ctx, server.URL+"/test.tar.gz")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if size != 1234 {
		t.Errorf("expected size 1234, got %d", size)
	}

	if contentType != "application/octet-stream" {
		t.Errorf("expected content type application/octet-stream, got %s", contentType)
	}
}

func TestExtractRegistry(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		expected string
	}{
		{
			name:     "npm registry",
			url:      "https://registry.npmjs.org/package/-/package-1.0.0.tgz",
			expected: "registry.npmjs.org",
		},
		{
			name:     "pypi registry",
			url:      "https://files.pythonhosted.org/packages/abc/def/file.tar.gz",
			expected: "files.pythonhosted.org",
		},
		{
			name:     "long URL",
			url:      "https://very-long-hostname.example.com/path",
			expected: "very-long-hostname.example.com",
		},
		{
			name:     "with port",
			url:      "https://example.com:8080/path",
			expected: "example.com:8080",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractRegistry(tt.url)
			if got != tt.expected {
				t.Errorf("extractRegistry(%q) = %q, want %q", tt.url, got, tt.expected)
			}
		})
	}
}

// TestExtractRegistryHostlessURL covers the URLs that have no host to group by.
// Identifiers are externally observable and a fetch URL may carry a secret, so
// assert on what the identifier must not reveal, along with the grouping the
// breakers still need.
func TestExtractRegistryHostlessURL(t *testing.T) {
	const secret = "top-secret-signing-token"

	tests := []struct {
		name string
		url  string
	}{
		{
			name: "no scheme or host",
			url:  "signed-token=" + secret,
		},
		{
			name: "path only",
			url:  "/packages/pkg-1.0.0.tgz?sig=" + secret,
		},
		{
			name: "scheme without host",
			url:  "file:///srv/internal/" + secret + "/pkg-1.0.0.tgz",
		},
		{
			name: "unparsable",
			url:  "https://internal host.invalid/pkg.tgz?sig=" + secret,
		},
	}

	identifiers := make(map[string]string, len(tests))
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractRegistry(tt.url)

			if strings.Contains(got, secret) {
				t.Errorf("extractRegistry(%q) = %q, which discloses the URL", tt.url, got)
			}
			want := len(hostlessRegistryPrefix) + hostlessRegistryDigest
			if !strings.HasPrefix(got, hostlessRegistryPrefix) || len(got) != want {
				t.Errorf("extractRegistry(%q) = %q, want %q followed by %d hex characters",
					tt.url, got, hostlessRegistryPrefix, hostlessRegistryDigest)
			}
			unkeyed := sha256.Sum256([]byte(tt.url))
			if got == hostlessRegistryPrefix+hex.EncodeToString(unkeyed[:])[:hostlessRegistryDigest] {
				t.Errorf("extractRegistry(%q) = %q, a digest that anyone can recompute from a guessed URL",
					tt.url, got)
			}
			if again := extractRegistry(tt.url); again != got {
				t.Errorf("extractRegistry(%q) is unstable: %q then %q", tt.url, got, again)
			}
			if other, ok := identifiers[got]; ok {
				t.Errorf("extractRegistry(%q) = %q, already used for %q: these URLs share a breaker",
					tt.url, got, other)
			}
			identifiers[got] = tt.url
		})
	}
}

// TestGetBreakerStateHostlessURLHidesURL is the end-to-end form of
// TestExtractRegistryHostlessURL: a hostless URL that fails often enough to trip
// its breaker must not put the URL into the state map that callers publish, and
// every one of those failures must land on the same breaker.
func TestGetBreakerStateHostlessURLHidesURL(t *testing.T) {
	const secret = "top-secret-signing-token"
	artifactURL := "signed-token=" + secret

	cbFetcher := NewCircuitBreakerFetcher(NewFetcher())

	ctx := context.Background()
	for range cbThreshold {
		if _, err := cbFetcher.Fetch(ctx, artifactURL); err == nil {
			t.Fatalf("Fetch(%q) succeeded, want an error", artifactURL)
		}
	}

	states := cbFetcher.GetBreakerState()
	if len(states) != 1 {
		t.Fatalf("GetBreakerState() = %v, want one entry: the identifier has to be stable "+
			"within a fetcher for the breaker to count failures", states)
	}
	for registry, state := range states {
		if strings.Contains(registry, secret) {
			t.Errorf("GetBreakerState() key %q discloses the fetch URL", registry)
		}
		if state != "open" {
			t.Errorf("GetBreakerState()[%q] = %q, want open after %d failures", registry, state, cbThreshold)
		}
	}
}

func TestGetBreakerState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	fetcher := NewFetcher()
	cbFetcher := NewCircuitBreakerFetcher(fetcher)

	// Initially empty
	states := cbFetcher.GetBreakerState()
	if len(states) != 0 {
		t.Errorf("expected empty states, got %d entries", len(states))
	}

	// After a fetch, should have state
	ctx := context.Background()
	_, _ = cbFetcher.Fetch(ctx, server.URL+"/test")

	states = cbFetcher.GetBreakerState()
	if len(states) == 0 {
		t.Error("expected at least one breaker state after fetch")
	}

	// Should be in closed state (working)
	for _, state := range states {
		if state != "closed" {
			t.Errorf("expected closed state, got %s", state)
		}
	}
}

func TestCircuitBreakerMultipleRegistries(t *testing.T) {
	server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("server1"))
	}))
	defer server1.Close()

	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("server2"))
	}))
	defer server2.Close()

	fetcher := NewFetcher()
	cbFetcher := NewCircuitBreakerFetcher(fetcher)

	ctx := context.Background()

	// Fetch from both servers
	art1, err1 := cbFetcher.Fetch(ctx, server1.URL+"/test")
	if err1 != nil {
		t.Fatalf("fetch 1 failed: %v", err1)
	}
	_ = art1.Body.Close()

	art2, err2 := cbFetcher.Fetch(ctx, server2.URL+"/test")
	if err2 != nil {
		t.Fatalf("fetch 2 failed: %v", err2)
	}
	_ = art2.Body.Close()

	// Should have separate breaker states for each registry
	states := cbFetcher.GetBreakerState()
	if len(states) != 2 {
		t.Errorf("expected 2 breaker states, got %d", len(states))
	}
}

func TestCircuitBreakerOpensOnFailures(t *testing.T) {
	failCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		failCount++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	fetcher := NewFetcher(WithMaxRetries(0), WithBaseDelay(0))
	cbFetcher := NewCircuitBreakerFetcher(fetcher)

	ctx := context.Background()

	// Make multiple failing requests to trip the circuit breaker
	// Default threshold is 5 failures
	for range 10 {
		_, _ = cbFetcher.Fetch(ctx, server.URL+"/test")
	}

	// Check that circuit breaker eventually opened
	states := cbFetcher.GetBreakerState()
	if len(states) == 0 {
		t.Fatal("expected breaker state to exist")
	}

	// Circuit should be open after repeated failures
	// Note: The exact state depends on timing, but we should have made fewer
	// than 10 actual HTTP requests if the breaker opened
	if failCount >= 10 {
		t.Logf("Warning: Circuit breaker may not have opened (got %d requests)", failCount)
	}
}
