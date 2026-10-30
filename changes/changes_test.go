package changes

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/git-pkgs/registries/client"
)

// testClient returns a client for srv that doesn't retry, so failures show
// at once.
func testClient(srv *httptest.Server) *client.Client {
	return client.NewClient(client.WithHTTPClient(srv.Client()), client.WithMaxRetries(0))
}

// fakePackagist serves a change feed the way packagist.org does: without
// since, 400 with the current timestamp; with an old since, a resync; with a
// known one, the actions recorded after it.
type fakePackagist struct {
	mu      sync.Mutex
	now     int64
	actions map[int64]string // since -> JSON actions
	asked   []string
}

func (p *fakePackagist) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	since := r.URL.Query().Get("since")
	p.asked = append(p.asked, since)
	w.Header().Set("Content-Type", "application/json")
	if since == "" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprintf(w, `{"error":"Invalid or missing since query parameter","timestamp":%d}`, p.now)
		return
	}
	n, _ := strconv.ParseInt(since, 10, 64)
	actions, ok := p.actions[n]
	if !ok {
		actions = `[{"type":"resync","package":"*","time":1}]`
	}
	_, _ = fmt.Fprintf(w, `{"actions":%s,"timestamp":%d}`, actions, p.now)
}

func TestPackagist(t *testing.T) {
	p := &fakePackagist{now: 100, actions: map[int64]string{
		100: `[{"type":"update","package":"acme/lib","time":1},{"type":"update","package":"acme/lib~dev","time":1},{"type":"delete","package":"acme/gone","time":1}]`,
		200: `[]`,
	}}
	srv := httptest.NewServer(p)
	defer srv.Close()
	feed := NewPackagist(WithClient(testClient(srv)), WithURL(PackagistFeedURL(srv.URL+"/")))
	ctx := context.Background()

	cursor, err := feed.Start(ctx)
	if err != nil || cursor != "100" {
		t.Fatalf("Start = %q, %v; want 100", cursor, err)
	}
	p.mu.Lock()
	p.now = 200
	p.mu.Unlock()
	c, cursor, err := feed.Changes(ctx, cursor)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"acme/lib", "acme/lib~dev", "acme/gone", "acme/gone~dev"}; !slices.Equal(c.Names, want) || c.All || cursor != "200" {
		t.Errorf("changes = %+v, cursor %q; want %v and 200", c, cursor, want)
	}
	if c, _, err := feed.Changes(ctx, cursor); err != nil || len(c.Names) != 0 || c.All {
		t.Errorf("changes without news = %+v, %v", c, err)
	}

	// A cursor the feed no longer has history for gets a resync.
	if c, _, err := feed.Changes(ctx, "50"); err != nil || !c.All {
		t.Errorf("changes after a resync = %+v, %v, want All", c, err)
	}
	if want := []string{"", "100", "200", "50"}; !slices.Equal(p.asked, want) {
		t.Errorf("asked since %q, want %q", p.asked, want)
	}
	if _, cur, err := feed.Changes(ctx, "garbage"); err == nil || cur != "garbage" {
		t.Errorf("invalid cursor: cursor %q, err %v; want an error and the cursor back", cur, err)
	}
}

func TestPackagistErrorReturnsCursor(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"actions":[{"type":"update","package":"acme/lib","time":1}],"timestamp":8}`))
	}))
	defer srv.Close()
	feed := NewPackagist(WithClient(testClient(srv)), WithURL(srv.URL))
	if _, cursor, err := feed.Changes(context.Background(), "7"); err == nil || cursor != "7" {
		t.Fatalf("failing feed: cursor %q, err %v; want an error and cursor 7", cursor, err)
	}
	fail.Store(false)
	if c, cursor, err := feed.Changes(context.Background(), "7"); err != nil || !slices.Equal(c.Names, []string{"acme/lib"}) || cursor != "8" {
		t.Errorf("changes after recovery = %+v, %q, %v", c, cursor, err)
	}
}

// fakeReplicate serves a CouchDB-style changes feed over seqs 1..len(ids).
type fakeReplicate struct {
	mu    sync.Mutex
	ids   []string
	limit int
	// failFrom makes requests from this since on fail, when set.
	failFrom int
	// noLastSeq leaves last_seq out of responses from failFrom on.
	noLastSeq bool
	asked     []string
}

func (f *fakeReplicate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/registry/" {
		_, _ = fmt.Fprintf(w, `{"db_name":"registry","update_seq":%d}`, len(f.ids))
		return
	}
	if r.URL.Path != "/registry/_changes" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	since, _ := strconv.Atoi(r.URL.Query().Get("since"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	f.asked = append(f.asked, r.URL.Query().Get("since"))
	if f.failFrom > 0 && since >= f.failFrom {
		if f.noLastSeq {
			_, _ = w.Write([]byte(`{"results":[]}`))
			return
		}
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	if f.limit > 0 {
		limit = min(limit, f.limit)
	}
	var results []string
	last := since
	for seq := since + 1; seq <= len(f.ids) && len(results) < limit; seq++ {
		results = append(results, fmt.Sprintf(`{"seq":%d,"id":%q,"changes":[{"rev":"1-a"}]}`, seq, f.ids[seq-1]))
		last = seq
	}
	_, _ = fmt.Fprintf(w, `{"results":[%s],"last_seq":%d}`, strings.Join(results, ","), last)
}

func newTestNPM(srv *httptest.Server) *NPM {
	return NewNPM(WithClient(testClient(srv)), WithURL(srv.URL+"/registry/_changes"))
}

func TestNPM(t *testing.T) {
	f := &fakeReplicate{ids: []string{"old"}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	feed := newTestNPM(srv)
	ctx := context.Background()

	cursor, err := feed.Start(ctx)
	if err != nil || cursor != "1" {
		t.Fatalf("Start = %q, %v; want 1", cursor, err)
	}
	f.mu.Lock()
	f.ids = append(f.ids, "lodash", "@scope/pkg", "_design/app")
	f.mu.Unlock()
	c, cursor, err := feed.Changes(ctx, cursor)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"lodash", "@scope/pkg"}; !slices.Equal(c.Names, want) || cursor != "4" {
		t.Errorf("changes = %v, cursor %q; want %v and 4", c.Names, cursor, want)
	}
	if c, _, err := feed.Changes(ctx, cursor); err != nil || len(c.Names) != 0 {
		t.Errorf("changes without news = %+v, %v", c, err)
	}
	if want := []string{"1", "4"}; !slices.Equal(f.asked, want) {
		t.Errorf("asked since %q, want %q", f.asked, want)
	}
}

// TestNPMPages checks that a backlog longer than a page is read page by page,
// and that one read stops after npmMaxPages.
func TestNPMPages(t *testing.T) {
	f := &fakeReplicate{limit: npmPageLimit}
	f.ids = make([]string, npmPageLimit*(npmMaxPages+1)+5)
	for i := range f.ids {
		f.ids[i] = "pkg-" + strconv.Itoa(i)
	}
	srv := httptest.NewServer(f)
	defer srv.Close()
	feed := newTestNPM(srv)

	c, cursor, err := feed.Changes(context.Background(), "0")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Names) != npmPageLimit*npmMaxPages || cursor != Cursor(strconv.Itoa(npmPageLimit*npmMaxPages)) {
		t.Errorf("first read returned %d names, cursor %q", len(c.Names), cursor)
	}
	c, _, err = feed.Changes(context.Background(), cursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Names) != npmPageLimit+5 {
		t.Errorf("second read returned %d names, want %d", len(c.Names), npmPageLimit+5)
	}
}

// TestNPMLaterPageFails checks that when a page after the first fails, or
// comes without last_seq, the earlier pages' names are returned with the
// cursor after them and no error, and a failing first page returns the
// cursor passed in.
func TestNPMLaterPageFails(t *testing.T) {
	for _, noLastSeq := range []bool{false, true} {
		t.Run(fmt.Sprintf("noLastSeq=%t", noLastSeq), func(t *testing.T) {
			f := &fakeReplicate{limit: npmPageLimit, failFrom: npmPageLimit, noLastSeq: noLastSeq}
			f.ids = make([]string, npmPageLimit*2)
			for i := range f.ids {
				f.ids[i] = "pkg-" + strconv.Itoa(i)
			}
			srv := httptest.NewServer(f)
			defer srv.Close()
			feed := newTestNPM(srv)

			c, cursor, err := feed.Changes(context.Background(), "0")
			if err != nil || len(c.Names) != npmPageLimit || cursor != Cursor(strconv.Itoa(npmPageLimit)) {
				t.Errorf("read = %d names, cursor %q, %v; want the first page and its cursor", len(c.Names), cursor, err)
			}
			if _, cursor, err := feed.Changes(context.Background(), cursor); err == nil || cursor != Cursor(strconv.Itoa(npmPageLimit)) {
				t.Errorf("failing first page: cursor %q, err %v; want an error and the cursor back", cursor, err)
			}
		})
	}
}

// TestNPMStringSequence checks that a sequence given as a JSON string is
// passed back without its quotes.
func TestNPMStringSequence(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/registry/" {
			_, _ = w.Write([]byte(`{"update_seq":"42-g1AAAA"}`))
			return
		}
		if got := r.URL.Query().Get("since"); got != "42-g1AAAA" {
			t.Errorf("since = %q", got)
		}
		_, _ = w.Write([]byte(`{"results":[{"id":"a"}],"last_seq":"43-g1BBBB"}`))
	}))
	defer srv.Close()
	feed := newTestNPM(srv)
	cursor, err := feed.Start(context.Background())
	if err != nil || cursor != "42-g1AAAA" {
		t.Fatalf("Start = %q, %v", cursor, err)
	}
	if _, cursor, err := feed.Changes(context.Background(), cursor); err != nil || cursor != "43-g1BBBB" {
		t.Errorf("Changes cursor = %q, %v", cursor, err)
	}
}

func TestEcosystems(t *testing.T) {
	var feeds = []Feed{NewPackagist(), NewNPM()}
	if feeds[0].Ecosystem() != "composer" || feeds[1].Ecosystem() != "npm" {
		t.Errorf("ecosystems = %q, %q", feeds[0].Ecosystem(), feeds[1].Ecosystem())
	}
}
