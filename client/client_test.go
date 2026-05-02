package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetBody(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"ok":true}`))
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
		w.Write([]byte(`{"name":"test","version":"1.0"}`))
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

func TestResponseSizeLimited(t *testing.T) {
	// Serve a response body larger than maxResponseSize (10 MB).
	oversized := strings.Repeat("x", maxResponseSize+1024)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(oversized))
	}))
	defer srv.Close()

	c := DefaultClient()
	c.MaxRetries = 0

	body, err := c.GetBody(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(body) > maxResponseSize {
		t.Fatalf("response body should be capped at %d bytes, got %d", maxResponseSize, len(body))
	}
	if len(body) != maxResponseSize {
		t.Fatalf("expected exactly %d bytes from LimitReader, got %d", maxResponseSize, len(body))
	}
}

func TestResponseWithinLimit(t *testing.T) {
	payload := `{"packages":["a","b","c"]}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(payload))
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
