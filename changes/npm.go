package changes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// NPMURL is the change feed of the public npm registry.
const NPMURL = "https://replicate.npmjs.com/registry/_changes"

// npmPageLimit is the most changes the npm feed returns in one response.
const npmPageLimit = 10000

// npmMaxPages bounds how many pages one Changes call reads. The rest is read
// on the next call.
const npmMaxPages = 10

// NPM reads the npm registry's CouchDB-style changes feed at
// replicate.npmjs.com.
//
// Its cursor is a sequence, which npm asks followers to treat as opaque; the
// database document at the feed's parent URL, with its trailing slash
// (without it the URL is 404), gives the current one as update_seq. A page
// holds at most 10000 changes (1000 when no limit is given), and Changes
// reads up to ten pages. Each result's id is a package name; design
// documents are left out.
type NPM struct {
	o options
}

// NewNPM returns the feed of the public npm registry, or the one set with
// WithURL.
func NewNPM(opts ...Option) *NPM {
	return &NPM{o: newOptions(NPMURL, opts)}
}

func (*NPM) Ecosystem() string { return "npm" }

type npmChanges struct {
	Results []struct {
		ID string `json:"id"`
	} `json:"results"`
	LastSeq json.RawMessage `json:"last_seq"`
}

func (f *NPM) Start(ctx context.Context) (Cursor, error) {
	var info struct {
		UpdateSeq json.RawMessage `json:"update_seq"`
	}
	if err := f.o.client.GetJSON(ctx, strings.TrimSuffix(f.o.url, "_changes"), &info); err != nil {
		return "", fmt.Errorf("npm change feed: %w", err)
	}
	if len(info.UpdateSeq) == 0 {
		return "", errors.New("npm change feed: no update_seq to start from")
	}
	return seqCursor(info.UpdateSeq), nil
}

// Changes reads pages until one is not full, or up to ten. When a later page
// fails, it returns what the earlier ones found with the cursor after them,
// and no error: the next call goes on from there.
func (f *NPM) Changes(ctx context.Context, cursor Cursor) (Changes, Cursor, error) {
	if cursor == "" {
		return Changes{}, cursor, errors.New("npm change feed: empty cursor")
	}
	var changes Changes
	next := cursor
	for page := range npmMaxPages {
		res, err := f.page(ctx, next)
		if err != nil {
			if page > 0 {
				return changes, next, nil
			}
			return Changes{}, cursor, err
		}
		for _, r := range res.Results {
			if r.ID != "" && !strings.HasPrefix(r.ID, "_design/") {
				changes.Names = append(changes.Names, r.ID)
			}
		}
		next = seqCursor(res.LastSeq)
		if len(res.Results) < npmPageLimit {
			break
		}
	}
	return changes, next, nil
}

// page reads one page after cursor.
func (f *NPM) page(ctx context.Context, cursor Cursor) (npmChanges, error) {
	var res npmChanges
	u := withQuery(f.o.url, url.Values{
		"since": {string(cursor)},
		"limit": {strconv.Itoa(npmPageLimit)},
	})
	if err := f.o.client.GetJSON(ctx, u, &res); err != nil {
		return npmChanges{}, fmt.Errorf("npm change feed: %w", err)
	}
	if len(res.LastSeq) == 0 {
		return npmChanges{}, errors.New("npm change feed: response without last_seq")
	}
	return res, nil
}

// seqCursor returns a sequence as the since parameter takes it: a number as
// is, a string without its quotes.
func seqCursor(seq json.RawMessage) Cursor {
	var s string
	if json.Unmarshal(seq, &s) == nil {
		return Cursor(s)
	}
	return Cursor(seq)
}
