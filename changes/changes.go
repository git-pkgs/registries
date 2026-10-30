// Package changes reads the feeds in which registries list the packages that
// changed since a given point, so a cache or mirror can refresh just those.
//
// Feeds are stateless: the caller keeps the Cursor, starting from Start and
// passing the one Changes returns to the next call. A caller that acts on the
// changes moves on to the new cursor only once it has, so a failure leaves
// the changes to be read again. A Cursor is a plain string and can be stored.
package changes

import (
	"context"
	"net/url"
	"strings"

	"github.com/git-pkgs/registries/client"
)

// Cursor is a position in a feed, as the feed's registry gives it. Treat it
// as opaque.
type Cursor string

// Changes is what one read of a feed found.
type Changes struct {
	// Names are the packages that changed, as the registry names them.
	Names []string
	// All is set when the feed can't say what changed since the cursor, so
	// every package has to be treated as changed.
	All bool
}

// Feed is one registry's change feed.
type Feed interface {
	// Ecosystem is the ecosystem the changed names belong to.
	Ecosystem() string
	// Start returns the feed's current position, from which Changes reports
	// what changes next.
	Start(ctx context.Context) (Cursor, error)
	// Changes returns what changed after cursor, and the cursor to read on
	// from. On error the returned cursor is the one passed in.
	Changes(ctx context.Context, cursor Cursor) (Changes, Cursor, error)
}

// Option configures a feed.
type Option func(*options)

type options struct {
	client *client.Client
	url    string
}

// WithClient sets the client a feed makes its requests with. The default is
// client.DefaultClient().
func WithClient(c *client.Client) Option {
	return func(o *options) { o.client = c }
}

// WithURL sets the URL of the feed, for a registry other than the public one.
func WithURL(u string) Option {
	return func(o *options) { o.url = u }
}

func newOptions(defaultURL string, opts []Option) options {
	o := options{url: defaultURL}
	for _, opt := range opts {
		opt(&o)
	}
	if o.client == nil {
		o.client = client.DefaultClient()
	}
	return o
}

// withQuery returns base with the query parameters in q added.
func withQuery(base string, q url.Values) string {
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	return base + sep + q.Encode()
}
