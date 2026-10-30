package changes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/git-pkgs/registries/client"
)

// PackagistURL is the metadata change feed of packagist.org.
const PackagistURL = "https://packagist.org/metadata/changes.json"

// PackagistFeedURL returns where a Packagist instance at apiURL, such as
// https://packagist.org, publishes its metadata change feed. Packagist
// advertises it as "metadata-changes-url" in its packages.json.
func PackagistFeedURL(apiURL string) string {
	return strings.TrimSuffix(apiURL, "/") + "/metadata/changes.json"
}

// Packagist reads Packagist's metadata change feed
// (https://packagist.org/apidoc#track-package-updates).
//
// Its cursor is a timestamp in units of 1/10000 s. Asked without one the
// feed answers 400 with the current timestamp; asked with one older than its
// 24 hours of history it answers with a single "resync" action, reported as
// Changes.All. Each update names the metadata file that changed,
// "vendor/package" or "vendor/package~dev" for its dev versions; a delete
// names the package and is reported as both.
type Packagist struct {
	o options
}

// NewPackagist returns the feed of packagist.org, or of the instance set
// with WithURL (see PackagistFeedURL).
func NewPackagist(opts ...Option) *Packagist {
	return &Packagist{o: newOptions(PackagistURL, opts)}
}

func (*Packagist) Ecosystem() string { return "composer" }

type packagistChanges struct {
	Actions []struct {
		Type    string `json:"type"`
		Package string `json:"package"`
	} `json:"actions"`
	Timestamp int64  `json:"timestamp"`
	Error     string `json:"error"`
}

func (f *Packagist) Start(ctx context.Context) (Cursor, error) {
	var start packagistChanges
	err := f.o.client.GetJSON(ctx, f.o.url, &start)
	var httpErr *client.HTTPError
	if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusBadRequest {
		// The expected answer to a request without a cursor.
		err = json.Unmarshal([]byte(httpErr.Body), &start)
	}
	if err != nil {
		return "", fmt.Errorf("packagist change feed: %w", err)
	}
	if start.Timestamp <= 0 {
		return "", errors.New("packagist change feed: no timestamp to start from")
	}
	return Cursor(strconv.FormatInt(start.Timestamp, 10)), nil
}

func (f *Packagist) Changes(ctx context.Context, cursor Cursor) (Changes, Cursor, error) {
	since, err := strconv.ParseInt(string(cursor), 10, 64)
	if err != nil || since <= 0 {
		return Changes{}, cursor, fmt.Errorf("packagist change feed: invalid cursor %q", cursor)
	}
	var page packagistChanges
	u := withQuery(f.o.url, url.Values{"since": {string(cursor)}})
	if err := f.o.client.GetJSON(ctx, u, &page); err != nil {
		return Changes{}, cursor, fmt.Errorf("packagist change feed: %w", err)
	}
	if page.Error != "" || page.Timestamp <= 0 {
		return Changes{}, cursor, fmt.Errorf("packagist change feed: %q", page.Error)
	}
	var changes Changes
	for _, a := range page.Actions {
		switch a.Type {
		case "update":
			changes.Names = append(changes.Names, a.Package)
		case "delete":
			// A deleted package takes its dev versions with it.
			changes.Names = append(changes.Names, a.Package, a.Package+"~dev")
		case "resync":
			// The cursor is too old for the feed to list what changed.
			changes.All = true
		}
	}
	return changes, Cursor(strconv.FormatInt(page.Timestamp, 10)), nil
}
