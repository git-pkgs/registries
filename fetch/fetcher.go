// Package fetch provides streaming artifact downloading with retry, circuit breaking,
// and URL resolution for package registries.
package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"time"

	"github.com/git-pkgs/registries/safehttp"
)

const (
	httpClientTimeout  = 5 * time.Minute
	defaultMaxRetries  = 3
	defaultBaseDelay   = 500 * time.Millisecond
	backoffBase        = 2
	jitterFactor       = 0.1
	serverErrThreshold = 500
	maxErrBodySize     = 1024
)

var (
	ErrNotFound     = errors.New("artifact not found")
	ErrRateLimited  = errors.New("rate limited by upstream")
	ErrUpstreamDown = errors.New("upstream registry unavailable")
)

// Artifact contains the response from fetching an upstream artifact.
type Artifact struct {
	Body        io.ReadCloser
	Size        int64 // -1 if unknown
	ContentType string
	ETag        string
}

// FetcherInterface defines the interface for artifact fetchers.
type FetcherInterface interface {
	Fetch(ctx context.Context, url string) (*Artifact, error)
	FetchWithHeaders(ctx context.Context, url string, headers http.Header) (*Artifact, error)
	Head(ctx context.Context, url string) (size int64, contentType string, err error)
}

// Fetcher downloads artifacts from upstream registries.
type Fetcher struct {
	client       *http.Client
	userAgent    string
	maxRetries   int
	baseDelay    time.Duration
	authFn       func(url string) (headerName, headerValue string)
	safeHTTPOpts safehttp.Options
	ipChecker    *safehttp.HostIPChecker
	stop         chan struct{}
}

// Option configures a Fetcher.
type Option func(*Fetcher)

// WithHTTPClient sets a custom HTTP client. To allow private hosts with a
// custom client, construct it with safehttp.New and the desired options
// before applying transport wrappers and passing it here.
func WithHTTPClient(c *http.Client) Option {
	return func(f *Fetcher) {
		f.client = c
	}
}

// WithUserAgent sets the User-Agent header.
func WithUserAgent(ua string) Option {
	return func(f *Fetcher) {
		f.userAgent = ua
	}
}

// WithMaxRetries sets the maximum retry attempts.
func WithMaxRetries(n int) Option {
	return func(f *Fetcher) {
		f.maxRetries = n
	}
}

// WithBaseDelay sets the base delay for exponential backoff.
func WithBaseDelay(d time.Duration) Option {
	return func(f *Fetcher) {
		f.baseDelay = d
	}
}

// WithAuthFunc sets a function that returns auth headers for a given URL.
// The function receives the request URL and returns a header name and value.
// Return empty strings to skip authentication for that URL.
func WithAuthFunc(fn func(url string) (headerName, headerValue string)) Option {
	return func(f *Fetcher) {
		f.authFn = fn
	}
}

// WithAllowPrivateHosts permits the named hosts to resolve to private IP addresses.
// Loopback and link-local addresses remain blocked.
func WithAllowPrivateHosts(hosts ...string) Option {
	return func(f *Fetcher) {
		f.safeHTTPOpts.AllowPrivateHosts = append(f.safeHTTPOpts.AllowPrivateHosts, hosts...)
	}
}

// NewFetcher creates a new Fetcher with the given options.
// Callers should invoke Close when done to release the DNS refresh goroutine.
// Under TinyGo, requests require WithHTTPClient.
func NewFetcher(opts ...Option) *Fetcher {
	f := &Fetcher{
		userAgent:  "git-pkgs-proxy/1.0",
		maxRetries: defaultMaxRetries,
		baseDelay:  defaultBaseDelay,
	}
	f.initHTTPClient()
	for _, opt := range opts {
		opt(f)
	}
	f.ipChecker = safehttp.NewHostIPChecker(f.safeHTTPOpts)
	return f
}

// Close stops the Fetcher's background DNS refresh goroutine.
// It is safe to call Close more than once.
func (f *Fetcher) Close() error {
	if f.stop == nil {
		return nil
	}
	select {
	case <-f.stop:
	default:
		close(f.stop)
	}
	return nil
}

// Fetch downloads an artifact from the given URL.
// The caller must close the returned Artifact.Body when done.
func (f *Fetcher) Fetch(ctx context.Context, url string) (*Artifact, error) {
	return f.FetchWithHeaders(ctx, url, nil)
}

// FetchWithHeaders downloads an artifact from the given URL with additional HTTP headers.
// The caller must close the returned Artifact.Body when done.
func (f *Fetcher) FetchWithHeaders(ctx context.Context, url string, headers http.Header) (*Artifact, error) {
	artifact, _, err := f.fetch(ctx, url, headers, false)
	return artifact, err
}

// FetchObserved downloads an artifact and records metadata about the response.
// The caller must read the body to EOF before treating the observation as complete.
func (f *Fetcher) FetchObserved(ctx context.Context, url string) (*ObservedArtifact, error) {
	return f.FetchObservedWithHeaders(ctx, url, nil)
}

// FetchObservedWithHeaders downloads an artifact with additional HTTP headers and
// records metadata about the response. Request headers are not copied into the observation.
func (f *Fetcher) FetchObservedWithHeaders(ctx context.Context, url string, headers http.Header) (*ObservedArtifact, error) {
	artifact, observation, err := f.fetch(ctx, url, headers, true)
	if err != nil {
		return nil, err
	}
	return &ObservedArtifact{Artifact: artifact, Observation: observation}, nil
}

func (f *Fetcher) fetch(ctx context.Context, url string, headers http.Header, observe bool) (*Artifact, *FetchObservation, error) {
	var lastErr error

	for attempt := 0; attempt <= f.maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff with 10% jitter to prevent thundering herd
			delay := f.baseDelay * time.Duration(math.Pow(backoffBase, float64(attempt-1)))
			jitter := time.Duration(float64(delay) * (rand.Float64() * jitterFactor))
			delay += jitter

			select {
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		artifact, observation, err := f.doFetch(ctx, url, headers, observe)
		if err == nil {
			return artifact, observation, nil
		}

		lastErr = err

		// Don't retry on not found or client errors
		if errors.Is(err, ErrNotFound) {
			return nil, nil, err
		}

		// Retry on rate limit and server errors
		if errors.Is(err, ErrRateLimited) || errors.Is(err, ErrUpstreamDown) {
			continue
		}

		// Don't retry on other errors (network issues will be wrapped)
		return nil, nil, err
	}

	return nil, nil, lastErr
}

func (f *Fetcher) doFetch(ctx context.Context, url string, headers http.Header, observe bool) (*Artifact, *FetchObservation, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("User-Agent", f.userAgent)
	req.Header.Set("Accept", "*/*")

	// Add caller-provided headers
	for key, values := range headers {
		for _, v := range values {
			req.Header.Set(key, v)
		}
	}

	// Add authentication header if configured (overrides caller headers)
	if f.authFn != nil {
		if name, value := f.authFn(url); name != "" && value != "" {
			req.Header.Set(name, value)
		}
	}

	startedAt := time.Now()
	resp, err := f.client.Do(req)
	responseTime := time.Since(startedAt)
	if err != nil {
		return nil, nil, fmt.Errorf("fetching artifact: %w", err)
	}

	switch {
	case resp.StatusCode == http.StatusOK:
		size := int64(-1)
		if cl := resp.Header.Get("Content-Length"); cl != "" {
			if n, err := strconv.ParseInt(cl, 10, 64); err == nil {
				size = n
			}
		}

		artifact := &Artifact{
			Body:        resp.Body,
			Size:        size,
			ContentType: resp.Header.Get("Content-Type"),
			ETag:        resp.Header.Get("ETag"),
		}
		if !observe {
			return artifact, nil, nil
		}

		observation := &FetchObservation{
			RequestedURL: url,
			FinalURL:     resp.Request.URL.String(),
			ResponseTime: responseTime,
			StatusCode:   resp.StatusCode,
			Headers:      copyObservedHeaders(resp.Header),
			DeclaredSize: size,
			MediaType:    resp.Header.Get("Content-Type"),
		}
		artifact.Body = newObservedBody(resp.Body, observation)
		return artifact, observation, nil

	case resp.StatusCode == http.StatusNotFound:
		_ = resp.Body.Close()
		return nil, nil, ErrNotFound

	case resp.StatusCode == http.StatusTooManyRequests:
		_ = resp.Body.Close()
		return nil, nil, ErrRateLimited

	case resp.StatusCode >= serverErrThreshold:
		_ = resp.Body.Close()
		return nil, nil, ErrUpstreamDown

	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrBodySize))
		_ = resp.Body.Close()
		return nil, nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}
}

// Head checks if an artifact exists and returns its metadata without downloading.
func (f *Fetcher) Head(ctx context.Context, url string) (size int64, contentType string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return 0, "", fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("User-Agent", f.userAgent)

	// Add authentication header if configured
	if f.authFn != nil {
		if name, value := f.authFn(url); name != "" && value != "" {
			req.Header.Set(name, value)
		}
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("head request: %w", err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return 0, "", ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return 0, "", fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	size = -1
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		if n, err := strconv.ParseInt(cl, 10, 64); err == nil {
			size = n
		}
	}

	return size, resp.Header.Get("Content-Type"), nil
}
