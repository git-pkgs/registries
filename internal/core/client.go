package core

import (
	"github.com/git-pkgs/registries/client"
)

// Type aliases for backward compatibility with ecosystem implementations.
type (
	RateLimiter = client.RateLimiter
	Client      = client.Client
	Option      = client.Option
	URLBuilder  = client.URLBuilder
	BaseURLs    = client.BaseURLs
)

// Function aliases for backward compatibility.
var (
	DefaultClient  = client.DefaultClient
	NewClient      = client.NewClient
	WithTimeout    = client.WithTimeout
	WithMaxRetries = client.WithMaxRetries
	BuildURLs      = client.BuildURLs
)
