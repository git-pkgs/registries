package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"time"
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
			Timeout: 30 * time.Second,
		},
		UserAgent:  "registries",
		MaxRetries: 5,
		BaseDelay:  50 * time.Millisecond,
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

// GetBody fetches a URL and returns the response body.
func (c *Client) GetBody(ctx context.Context, url string) ([]byte, error) {
	var lastErr error

	for attempt := 0; attempt <= c.MaxRetries; attempt++ {
		if attempt > 0 {
			delay := c.BaseDelay * time.Duration(math.Pow(2, float64(attempt-1)))
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		if c.RateLimiter != nil {
			if err := c.RateLimiter.Wait(ctx); err != nil {
				return nil, err
			}
		}

		body, err := c.doRequest(ctx, url)
		if err == nil {
			return body, nil
		}

		lastErr = err

		var httpErr *HTTPError
		if ok := isHTTPError(err, &httpErr); ok {
			if httpErr.StatusCode == 404 {
				return nil, err
			}
			if httpErr.StatusCode == 429 || httpErr.StatusCode >= 500 {
				continue
			}
			return nil, err
		}
	}

	return nil, lastErr
}

func (c *Client) doRequest(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		httpErr := &HTTPError{
			StatusCode: resp.StatusCode,
			URL:        url,
			Body:       string(body),
		}
		if resp.StatusCode == 429 {
			if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
				if seconds, err := strconv.Atoi(retryAfter); err == nil {
					return nil, &RateLimitError{RetryAfter: seconds}
				}
			}
		}
		return nil, httpErr
	}

	return body, nil
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

// URLBuilder constructs URLs for a registry.
type URLBuilder interface {
	Registry(name, version string) string
	Download(name, version string) string
	Documentation(name, version string) string
	PURL(name, version string) string
}

// BaseURLs provides a default URLBuilder implementation.
type BaseURLs struct {
	RegistryFn      func(name, version string) string
	DownloadFn      func(name, version string) string
	DocumentationFn func(name, version string) string
	PURLFn          func(name, version string) string
}

func (b *BaseURLs) Registry(name, version string) string {
	if b.RegistryFn != nil {
		return b.RegistryFn(name, version)
	}
	return ""
}

func (b *BaseURLs) Download(name, version string) string {
	if b.DownloadFn != nil {
		return b.DownloadFn(name, version)
	}
	return ""
}

func (b *BaseURLs) Documentation(name, version string) string {
	if b.DocumentationFn != nil {
		return b.DocumentationFn(name, version)
	}
	return ""
}

func (b *BaseURLs) PURL(name, version string) string {
	if b.PURLFn != nil {
		return b.PURLFn(name, version)
	}
	return fmt.Sprintf("pkg:%s/%s", "generic", name)
}

// BuildURLs returns a map of all non-empty URLs for a package.
// Keys are "registry", "download", "docs", and "purl".
func BuildURLs(urls URLBuilder, name, version string) map[string]string {
	result := make(map[string]string)
	if v := urls.Registry(name, version); v != "" {
		result["registry"] = v
	}
	if v := urls.Download(name, version); v != "" {
		result["download"] = v
	}
	if v := urls.Documentation(name, version); v != "" {
		result["docs"] = v
	}
	if v := urls.PURL(name, version); v != "" {
		result["purl"] = v
	}
	return result
}
