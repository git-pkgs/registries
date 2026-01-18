package core

import (
	"errors"
	"fmt"
)

// ErrNotFound is returned when a package or version is not found.
var ErrNotFound = errors.New("not found")

// HTTPError represents an HTTP error response.
type HTTPError struct {
	StatusCode int
	URL        string
	Body       string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.URL)
}

// IsNotFound returns true if the error represents a 404 response.
func (e *HTTPError) IsNotFound() bool {
	return e.StatusCode == 404
}

// NotFoundError wraps ErrNotFound with additional context.
type NotFoundError struct {
	Ecosystem string
	Name      string
	Version   string
}

func (e *NotFoundError) Error() string {
	if e.Version != "" {
		return fmt.Sprintf("%s: package %s version %s not found", e.Ecosystem, e.Name, e.Version)
	}
	return fmt.Sprintf("%s: package %s not found", e.Ecosystem, e.Name)
}

func (e *NotFoundError) Unwrap() error {
	return ErrNotFound
}

// RateLimitError is returned when the registry rate limits requests.
type RateLimitError struct {
	RetryAfter int // seconds
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("rate limited, retry after %d seconds", e.RetryAfter)
}
