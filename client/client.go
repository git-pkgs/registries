// Package client provides an HTTP client with retry logic for registry APIs.
package client

import (
	"context"
	"encoding/json"
	"errors"
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

	// maxResponseSize is the maximum response body size we will read (10 MB).
	// Registry JSON API responses should be well under this limit.
	maxResponseSize = 10 << 20
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
	body, err := c.GetBody(ctx, url)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}

// GetJSONWithHeaders fetches a URL with extra request headers, which replace
// the defaults of the same name, decodes the JSON response into v and returns
// the response headers.
func (c *Client) GetJSONWithHeaders(ctx context.Context, url string, header http.Header, v any) (http.Header, error) {
	body, respHeader, err := c.getBody(ctx, url, header)
	if err != nil {
		return nil, err
	}
	return respHeader, json.Unmarshal(body, v)
}

// GetBody fetches a URL and returns the response body.
func (c *Client) GetBody(ctx context.Context, url string) ([]byte, error) {
	body, _, err := c.getBody(ctx, url, nil)
	return body, err
}

func (c *Client) getBody(ctx context.Context, url string, header http.Header) ([]byte, http.Header, error) {
	var lastErr error

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

		body, respHeader, err := c.doRequest(ctx, url, header)
		if err == nil {
			return body, respHeader, nil
		}

		lastErr = err
		if errors.Is(err, ErrUpstreamHeld) {
			return nil, nil, err
		}
		var rateErr *RateLimitError
		if errors.As(err, &rateErr) {
			return nil, nil, err
		}

		var httpErr *HTTPError
		if ok := isHTTPError(err, &httpErr); ok {
			if httpErr.StatusCode == http.StatusNotFound {
				return nil, nil, err
			}
			if httpErr.StatusCode == http.StatusTooManyRequests || httpErr.StatusCode >= http.StatusInternalServerError {
				continue
			}
			return nil, nil, err
		}
	}

	return nil, nil, lastErr
}

func (c *Client) doRequest(ctx context.Context, url string, header http.Header) ([]byte, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, err
	}

	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/json")
	for name, values := range header {
		req.Header[http.CanonicalHeaderKey(name)] = values
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return nil, nil, err
	}

	if resp.StatusCode >= http.StatusBadRequest {
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
				return nil, nil, &RateLimitError{RetryAfter: int(seconds)}
			}
		}
		return nil, nil, httpErr
	}

	return body, resp.Header, nil
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
