package core

import (
	"errors"

	"github.com/git-pkgs/registries/client"
)

// ErrNotFound is returned when a package or version is not found.
var ErrNotFound = client.ErrNotFound

// ErrTooManyPages is returned when a paginated listing runs past MaxPages.
var ErrTooManyPages = errors.New("too many pages")

// MaxPages caps paginated listings so a registry that never reports a last
// page cannot keep the caller fetching.
const MaxPages = 1000

// Type aliases for backward compatibility.
type (
	HTTPError      = client.HTTPError
	NotFoundError  = client.NotFoundError
	RateLimitError = client.RateLimitError
)
