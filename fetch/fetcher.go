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
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rs/dnscache"

	"github.com/git-pkgs/registries/safehttp"
)

const (
	dnsRefreshInterval    = 5 * time.Minute
	dialTimeout           = 30 * time.Second
	dialKeepAlive         = 30 * time.Second
	httpClientTimeout     = 5 * time.Minute
	responseHeaderTimeout = 60 * time.Second
	maxIdleConns          = 100
	maxIdleConnsPerHost   = 10
	idleConnTimeout       = 90 * time.Second
	tlsHandshakeTimeout   = 10 * time.Second
	defaultMaxRetries     = 3
	defaultBaseDelay      = 500 * time.Millisecond
	backoffBase           = 2
	jitterFactor          = 0.1
	serverErrThreshold    = 500
	maxErrBodySize        = 1024
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
	allowPrivate map[string]bool
	stop         chan struct{}
}

// Option configures a Fetcher.
type Option func(*Fetcher)

// WithHTTPClient sets a custom HTTP client.
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
		if f.allowPrivate == nil {
			f.allowPrivate = make(map[string]bool, len(hosts))
		}
		for _, h := range hosts {
			if h = normalizeHost(h); h != "" {
				f.allowPrivate[h] = true
			}
		}
	}
}

func normalizeHost(host string) string {
	host = strings.TrimSpace(host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.ToLower(strings.TrimSuffix(host, "."))
}

// gateOptions returns the safehttp options for a dial to host.
// Zero-value strict gate unless the host was whitelisted via WithAllowPrivateHosts.
func (f *Fetcher) gateOptions(host string) safehttp.Options {
	if f.allowPrivate[normalizeHost(host)] {
		return safehttp.Options{AllowPrivate: true}
	}
	return safehttp.Options{}
}

// NewFetcher creates a new Fetcher with the given options.
// Callers should invoke Close when done to release the DNS refresh goroutine.
func NewFetcher(opts ...Option) *Fetcher {
	resolver := &dnscache.Resolver{}
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(dnsRefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				resolver.Refresh(true)
			case <-stop:
				return
			}
		}
	}()

	dialer := &net.Dialer{
		Timeout:   dialTimeout,
		KeepAlive: dialKeepAlive,
	}

	var f *Fetcher
	f = &Fetcher{
		client: &http.Client{
			Timeout: httpClientTimeout,
			Transport: &http.Transport{
				Proxy: http.ProxyFromEnvironment,
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					host, port, err := net.SplitHostPort(addr)
					if err != nil {
						return nil, err
					}
					ips, err := resolver.LookupHost(ctx, host)
					if err != nil {
						return nil, err
					}
					// Gate every resolved IP against the safehttp block
					// list (loopback, RFC1918, CGNAT, link-local, ...)
					// before dialing. The dial is to the resolved IP
					// directly so a rebind between gate and connect
					// cannot escape.
					var lastErr error
					for _, ip := range ips {
						if parsed := net.ParseIP(ip); parsed != nil {
							if err := safehttp.CheckIP(parsed, f.gateOptions(host)); err != nil {
								lastErr = err
								continue
							}
						}
						conn, derr := dialer.DialContext(ctx, network, net.JoinHostPort(ip, port))
						if derr == nil {
							return conn, nil
						}
						lastErr = derr
					}
					if lastErr == nil {
						return nil, fmt.Errorf("no IPs resolved for %s", host)
					}
					return nil, fmt.Errorf("dialing %s: %w", host, lastErr)
				},
				MaxIdleConns:          maxIdleConns,
				MaxIdleConnsPerHost:   maxIdleConnsPerHost,
				IdleConnTimeout:       idleConnTimeout,
				TLSHandshakeTimeout:   tlsHandshakeTimeout,
				ResponseHeaderTimeout: responseHeaderTimeout,
				ExpectContinueTimeout: 1 * time.Second,
			},
		},
		userAgent:  "git-pkgs-proxy/1.0",
		maxRetries: defaultMaxRetries,
		baseDelay:  defaultBaseDelay,
		stop:       stop,
	}
	for _, opt := range opts {
		opt(f)
	}
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
	var lastErr error

	for attempt := 0; attempt <= f.maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff with 10% jitter to prevent thundering herd
			delay := f.baseDelay * time.Duration(math.Pow(backoffBase, float64(attempt-1)))
			jitter := time.Duration(float64(delay) * (rand.Float64() * jitterFactor))
			delay += jitter

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		artifact, err := f.doFetch(ctx, url, headers)
		if err == nil {
			return artifact, nil
		}

		lastErr = err

		// Don't retry on not found or client errors
		if errors.Is(err, ErrNotFound) {
			return nil, err
		}

		// Retry on rate limit and server errors
		if errors.Is(err, ErrRateLimited) || errors.Is(err, ErrUpstreamDown) {
			continue
		}

		// Don't retry on other errors (network issues will be wrapped)
		return nil, err
	}

	return nil, lastErr
}

func (f *Fetcher) doFetch(ctx context.Context, url string, headers http.Header) (*Artifact, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
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

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching artifact: %w", err)
	}

	switch {
	case resp.StatusCode == http.StatusOK:
		size := int64(-1)
		if cl := resp.Header.Get("Content-Length"); cl != "" {
			if n, err := strconv.ParseInt(cl, 10, 64); err == nil {
				size = n
			}
		}

		return &Artifact{
			Body:        resp.Body,
			Size:        size,
			ContentType: resp.Header.Get("Content-Type"),
			ETag:        resp.Header.Get("ETag"),
		}, nil

	case resp.StatusCode == http.StatusNotFound:
		_ = resp.Body.Close()
		return nil, ErrNotFound

	case resp.StatusCode == http.StatusTooManyRequests:
		_ = resp.Body.Close()
		return nil, ErrRateLimited

	case resp.StatusCode >= serverErrThreshold:
		_ = resp.Body.Close()
		return nil, ErrUpstreamDown

	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrBodySize))
		_ = resp.Body.Close()
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
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
