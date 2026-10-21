// Package client provides an HTTP client with retry logic for registry APIs.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"time"

	"github.com/git-pkgs/registries/safehttp"
)

const (
	defaultTimeout    = 30 * time.Second
	defaultMaxRetries = 5
	defaultBaseDelay  = 50 * time.Millisecond
	backoffBase       = 2
	jitterFactor      = 0.1

	// maxResponseSize is the default limit for buffered response bodies.
	maxResponseSize = 10 << 20
	// maxStreamSize is the default limit for streamed bodies. The largest npm
	// packuments are around 100 MB.
	maxStreamSize = 512 << 20
)

// RateLimiter controls request pacing.
type RateLimiter interface {
	Wait(ctx context.Context) error
}

// Client is an HTTP client with retry logic for registry APIs.
type Client struct {
	HTTPClient  *http.Client
	UserAgent   string
	MaxRetries  int
	BaseDelay   time.Duration
	RateLimiter RateLimiter
	// MaxResponseSize limits buffered bodies; zero means 10 MiB.
	MaxResponseSize int64
	// MaxStreamSize limits bodies read through StreamJSON; zero means 512 MiB.
	MaxStreamSize int64
}

// DefaultClient returns a client with sensible defaults.
func DefaultClient() *Client {
	return &Client{
		HTTPClient: &http.Client{
			Timeout: defaultTimeout,
		},
		UserAgent:  "registries",
		MaxRetries: defaultMaxRetries,
		BaseDelay:  defaultBaseDelay,
	}
}

// GetJSON fetches a URL and decodes the JSON response into v.
func (c *Client) GetJSON(ctx context.Context, url string, v any) error {
	_, err := c.GetJSONWithHeaders(ctx, url, nil, v)
	return err
}

// GetJSONWithHeaders sends request headers and returns response headers.
// It uses the same retry and rate-limit policy as GetJSON.
func (c *Client) GetJSONWithHeaders(ctx context.Context, url string, headers http.Header, v any) (http.Header, error) {
	body, responseHeaders, err := c.getBody(ctx, url, headers)
	if err != nil {
		return responseHeaders, err
	}
	return responseHeaders, json.Unmarshal(body, v)
}

// GetBody fetches a URL and returns the response body. A body larger than
// the client's MaxResponseSize fails with ErrResponseTooLarge.
func (c *Client) GetBody(ctx context.Context, url string) ([]byte, error) {
	body, _, err := c.getBody(ctx, url, nil)
	return body, err
}

func (c *Client) getBody(ctx context.Context, url string, headers http.Header) ([]byte, http.Header, error) {
	var body []byte
	resp, responseHeaders, err := c.open(ctx, url, headers, func(resp *http.Response) error {
		defer func() { _ = resp.Body.Close() }()
		var err error
		body, err = readLimited(resp.Body, url, c.maxResponseSize())
		return err
	})
	if err != nil {
		return nil, responseHeaders, err
	}
	return body, resp.Header, nil
}

// StreamJSON fetches a URL and passes a decoder over the response body to fn,
// so large documents can be decoded without holding them in memory. Bodies
// are limited to MaxStreamSize. Retries cover getting a response; an error
// while fn reads the body is returned as is. fn must consume the complete
// JSON document; trailing data and body read errors are rejected.
func (c *Client) StreamJSON(ctx context.Context, url string, fn func(*json.Decoder) error) error {
	resp, _, err := c.open(ctx, url, nil, nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	limit := c.maxStreamSize()
	dec := json.NewDecoder(&limitedReader{r: resp.Body, remaining: limit, limit: limit, url: url})
	if err := fn(dec); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		if err != nil {
			return err
		}
		return errors.New("unexpected data after JSON document")
	}
	return nil
}

func (c *Client) maxResponseSize() int64 {
	if c.MaxResponseSize > 0 {
		return c.MaxResponseSize
	}
	return maxResponseSize
}

func (c *Client) maxStreamSize() int64 {
	if c.MaxStreamSize > 0 {
		return c.MaxStreamSize
	}
	return maxStreamSize
}

func readLimited(r io.Reader, url string, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, tooLarge(url, limit)
	}
	return body, nil
}

func tooLarge(url string, limit int64) error {
	return fmt.Errorf("%w: response body for %s exceeds %d bytes", ErrResponseTooLarge, url, limit)
}

// limitedReader fails once more than limit bytes have been read, where
// io.LimitReader would end the stream early and look like a complete body.
type limitedReader struct {
	r         io.Reader
	remaining int64
	limit     int64
	url       string
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if l.remaining < 0 {
		return 0, tooLarge(l.url, l.limit)
	}
	if int64(len(p)) > l.remaining+1 {
		p = p[:l.remaining+1]
	}
	n, err := l.r.Read(p)
	if int64(n) > l.remaining {
		n = int(l.remaining)
		l.remaining = -1
		return n, tooLarge(l.url, l.limit)
	}
	l.remaining -= int64(n)
	return n, err
}

// open retries requests and optional body reads within one retry budget.
// read must close the body; a nil read leaves it open for the caller. On
// failure it returns the headers of the last response received, if any.
func (c *Client) open(ctx context.Context, url string, headers http.Header, read func(*http.Response) error) (*http.Response, http.Header, error) {
	var lastErr error
	var lastHeaders http.Header

	for attempt := 0; attempt <= c.MaxRetries; attempt++ {
		if attempt > 0 {
			delay := c.BaseDelay * time.Duration(math.Pow(backoffBase, float64(attempt-1)))
			jitter := time.Duration(float64(delay) * (rand.Float64() * jitterFactor))
			delay += jitter

			select {
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		if c.RateLimiter != nil {
			if err := c.RateLimiter.Wait(ctx); err != nil {
				return nil, nil, err
			}
		}

		resp, responseHeaders, err := c.doRequest(ctx, url, headers)
		if err == nil && read != nil {
			err = read(resp)
		}
		if err == nil {
			return resp, responseHeaders, nil
		}

		lastErr = err
		lastHeaders = responseHeaders
		if errors.Is(err, ErrUpstreamHeld) || errors.Is(err, ErrResponseTooLarge) {
			return nil, responseHeaders, err
		}
		var rateErr *RateLimitError
		if errors.As(err, &rateErr) {
			return nil, responseHeaders, err
		}

		var httpErr *HTTPError
		if ok := isHTTPError(err, &httpErr); ok {
			if httpErr.StatusCode == http.StatusNotFound {
				return nil, responseHeaders, err
			}
			if httpErr.StatusCode == http.StatusTooManyRequests || httpErr.StatusCode >= http.StatusInternalServerError {
				continue
			}
			return nil, responseHeaders, err
		}
	}

	return nil, lastHeaders, lastErr
}

func (c *Client) doRequest(ctx context.Context, url string, headers http.Header) (*http.Response, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, err
	}

	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/json")
	for key, values := range headers {
		req.Header.Del(key)
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode < http.StatusBadRequest {
		return resp, resp.Header, nil
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return nil, resp.Header, err
	}
	httpErr := &HTTPError{
		StatusCode: resp.StatusCode,
		URL:        url,
		Body:       string(body),
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		if delay, ok := parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()); ok {
			seconds := delay / time.Second
			if delay%time.Second != 0 {
				seconds++
			}
			return nil, resp.Header, &RateLimitError{RetryAfter: int(seconds)}
		}
	}
	return nil, resp.Header, httpErr
}

func isHTTPError(err error, target **HTTPError) bool {
	if httpErr, ok := err.(*HTTPError); ok {
		*target = httpErr
		return true
	}
	return false
}

// GetText fetches a URL and returns the response body as a string.
func (c *Client) GetText(ctx context.Context, url string) (string, error) {
	body, err := c.GetBody(ctx, url)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// Head sends a HEAD request and returns the status code.
func (c *Client) Head(ctx context.Context, url string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return 0, err
	}

	req.Header.Set("User-Agent", c.UserAgent)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return 0, err
	}
	_ = resp.Body.Close()

	return resp.StatusCode, nil
}

// WithRateLimiter returns a copy of the client with the given rate limiter.
func (c *Client) WithRateLimiter(rl RateLimiter) *Client {
	copy := *c
	copy.RateLimiter = rl
	return &copy
}

// WithUserAgent returns a copy of the client with the given user agent.
func (c *Client) WithUserAgent(ua string) *Client {
	copy := *c
	copy.UserAgent = ua
	return &copy
}

// Option configures a Client.
type Option func(*Client)

// WithTimeout sets the HTTP client timeout.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		c.HTTPClient.Timeout = d
	}
}

// WithMaxRetries sets the maximum number of retries.
func WithMaxRetries(n int) Option {
	return func(c *Client) {
		c.MaxRetries = n
	}
}

// WithMaxResponseSize sets the limit for buffered response bodies.
func WithMaxResponseSize(n int64) Option {
	return func(c *Client) {
		c.MaxResponseSize = n
	}
}

// WithMaxStreamSize sets the limit for bodies read through StreamJSON.
func WithMaxStreamSize(n int64) Option {
	return func(c *Client) {
		c.MaxStreamSize = n
	}
}

// NewClient creates a new client with the given options.
func NewClient(opts ...Option) *Client {
	c := DefaultClient()
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// WithHTTPClient swaps in a caller-supplied *http.Client. Use this
// when the application has its own connection pool, mTLS config, or
// auth-injecting transport that other Options like WithTimeout can't
// express. The supplied client's Timeout, Jar, and Transport are
// preserved as-is.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		c.HTTPClient = h
	}
}

// WithTransport replaces the underlying http.RoundTripper without
// touching the Client's other settings (timeout, jar, etc.). Useful
// for wrapping the transport in middleware (auth, logging, retry,
// rate-limit) while keeping the rest of the configured shape intact.
func WithTransport(rt http.RoundTripper) Option {
	return func(c *Client) {
		if c.HTTPClient == nil {
			c.HTTPClient = &http.Client{Timeout: defaultTimeout}
		}
		c.HTTPClient.Transport = rt
	}
}

// WithSafeHTTP wraps the client's underlying *http.Client with the
// safehttp transport: dial-time IP gate (rejects loopback, RFC1918,
// CGNAT, link-local, multicast, unspecified), redirect chain capped
// at 10, non-http(s) schemes rejected on redirect. DNS is resolved at
// dial time and the connection dials the resolved IP directly so a
// rebind between resolve and connect cannot escape the gate. Suitable
// for any code path that fetches from URLs an attacker might control
// (a malicious registry response, a manifest-supplied URL, a redirect
// target).
func WithSafeHTTP() Option {
	return func(c *Client) {
		c.HTTPClient = safehttp.New(c.HTTPClient, safehttp.Options{})
	}
}
