package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestGetBody(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))
		defer srv.Close()

		c := DefaultClient()
		c.MaxRetries = 0

		body, err := c.GetBody(context.Background(), srv.URL)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if string(body) != `{"ok":true}` {
			t.Fatalf("got %q, want %q", string(body), `{"ok":true}`)
		}
	})

	t.Run("404 returns error without retry", func(t *testing.T) {
		attempts := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts++
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		c := DefaultClient()
		c.MaxRetries = 3

		_, err := c.GetBody(context.Background(), srv.URL)
		if err == nil {
			t.Fatal("expected error")
		}
		if attempts != 1 {
			t.Fatalf("expected 1 attempt, got %d", attempts)
		}
	})
}

func TestGetJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"test","version":"1.0"}`))
	}))
	defer srv.Close()

	c := DefaultClient()
	c.MaxRetries = 0

	var result struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := c.GetJSON(context.Background(), srv.URL, &result); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Name != "test" || result.Version != "1.0" {
		t.Fatalf("got %+v", result)
	}
}

func TestGetBodyRetriesTruncatedResponse(t *testing.T) {
	for _, failures := range []int{1, 3} {
		t.Run(strconv.Itoa(failures), func(t *testing.T) {
			requests := 0
			payload := `{"ok":true}`
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests++
				w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
				if requests <= failures {
					_, _ = w.Write([]byte(payload[:2]))
					return
				}
				_, _ = w.Write([]byte(payload))
			}))
			defer srv.Close()

			c := NewClient(WithMaxRetries(1))
			c.BaseDelay = 0
			body, err := c.GetBody(context.Background(), srv.URL)
			if failures == 1 {
				if err != nil || string(body) != payload {
					t.Errorf("GetBody = %q, %v", body, err)
				}
			} else if !errors.Is(err, io.ErrUnexpectedEOF) || body != nil {
				t.Errorf("GetBody = %q, %v; want no body and unexpected EOF", body, err)
			}
			if requests != 2 {
				t.Errorf("requests = %d, want 2", requests)
			}
		})
	}
}

func TestStreamJSONValidatesEndOfResponse(t *testing.T) {
	for _, tt := range []struct {
		name      string
		suffix    string
		wantErr   bool
		tooLarge  bool
		truncated bool
	}{
		{name: "whitespace", suffix: " \n\t"},
		{name: "garbage", suffix: "invalid", wantErr: true},
		{name: "second value", suffix: "{}", wantErr: true},
		{name: "oversized whitespace", suffix: strings.Repeat(" ", 1024), wantErr: true, tooLarge: true},
		{name: "truncated transfer", wantErr: true, truncated: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests++
				doc := `{"ok":true}` + tt.suffix
				if tt.truncated {
					w.Header().Set("Content-Length", strconv.Itoa(len(doc)+1))
				}
				_, _ = w.Write([]byte(doc))
			}))
			defer srv.Close()

			c := NewClient(WithMaxStreamSize(64))
			err := c.StreamJSON(context.Background(), srv.URL, func(dec *json.Decoder) error {
				var value map[string]bool
				return dec.Decode(&value)
			})
			if (err != nil) != tt.wantErr || errors.Is(err, ErrResponseTooLarge) != tt.tooLarge {
				t.Errorf("StreamJSON error = %v", err)
			}
			if tt.truncated && !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Errorf("StreamJSON error = %v, want unexpected EOF", err)
			}
			if requests != 1 {
				t.Errorf("requests = %d, want 1", requests)
			}
		})
	}
}

func TestResponseSizeLimited(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_, _ = w.Write([]byte(strings.Repeat("x", maxResponseSize+1)))
	}))
	defer srv.Close()

	c := DefaultClient()
	body, err := c.GetBody(context.Background(), srv.URL)
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("err = %v, want ErrResponseTooLarge", err)
	}
	if body != nil {
		t.Errorf("got %d bytes, want none", len(body))
	}
	if requests != 1 {
		t.Errorf("requests = %d, want no retry for an oversized body", requests)
	}

	var v any
	if err := c.GetJSON(context.Background(), srv.URL, &v); !errors.Is(err, ErrResponseTooLarge) {
		t.Errorf("GetJSON err = %v, want ErrResponseTooLarge", err)
	}
}

func TestResponseAtLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 100)))
	}))
	defer srv.Close()

	c := NewClient(WithMaxResponseSize(100))
	if body, err := c.GetBody(context.Background(), srv.URL); err != nil || len(body) != 100 {
		t.Errorf("at limit: %d bytes, %v", len(body), err)
	}
	c = NewClient(WithMaxResponseSize(99), WithMaxRetries(0))
	if _, err := c.GetBody(context.Background(), srv.URL); !errors.Is(err, ErrResponseTooLarge) {
		t.Errorf("over a raised limit: err = %v", err)
	}
}

func TestStreamJSON(t *testing.T) {
	doc := `{"versions":{` + strings.Repeat(`"1.0.0":{"name":"x"},`, 100) + `"2.0.0":{"name":"y"}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(doc))
	}))
	defer srv.Close()

	c := NewClient(WithMaxResponseSize(10), WithMaxRetries(0))
	var v struct {
		Versions map[string]struct{ Name string } `json:"versions"`
	}
	err := c.StreamJSON(context.Background(), srv.URL, func(dec *json.Decoder) error { return dec.Decode(&v) })
	if err != nil || v.Versions["2.0.0"].Name != "y" {
		t.Errorf("StreamJSON = %+v, %v; the buffered limit should not apply", v, err)
	}

	small := NewClient(WithMaxStreamSize(int64(len(doc)-1)), WithMaxRetries(0))
	err = small.StreamJSON(context.Background(), srv.URL, func(dec *json.Decoder) error { return dec.Decode(&v) })
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Errorf("over the stream limit: err = %v, want ErrResponseTooLarge", err)
	}
	exact := NewClient(WithMaxStreamSize(int64(len(doc))), WithMaxRetries(0))
	if err := exact.StreamJSON(context.Background(), srv.URL, func(dec *json.Decoder) error { return dec.Decode(&v) }); err != nil {
		t.Errorf("at the stream limit: %v", err)
	}

	var httpErr *HTTPError
	err = c.StreamJSON(context.Background(), srv.URL+"/missing", func(dec *json.Decoder) error {
		t.Error("fn called for a 404")
		return nil
	})
	if !errors.As(err, &httpErr) || !httpErr.IsNotFound() {
		t.Errorf("404 err = %v", err)
	}
}

func TestResponseWithinLimit(t *testing.T) {
	payload := `{"packages":["a","b","c"]}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	c := DefaultClient()
	c.MaxRetries = 0

	body, err := c.GetBody(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if string(body) != payload {
		t.Fatalf("got %q, want %q", string(body), payload)
	}
}
