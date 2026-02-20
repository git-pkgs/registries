package core

import (
	"github.com/git-pkgs/registries/client"
)

// ErrNotFound is returned when a package or version is not found.
var ErrNotFound = client.ErrNotFound

// Type aliases for backward compatibility.
type (
	HTTPError      = client.HTTPError
	NotFoundError  = client.NotFoundError
	RateLimitError = client.RateLimitError
)
