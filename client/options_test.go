package client

import (
	"net/http"
	"testing"
	"time"
)

func TestWithHTTPClient(t *testing.T) {
	custom := &http.Client{Timeout: 7 * time.Second}
	c := NewClient(WithHTTPClient(custom))
	if c.HTTPClient != custom {
		t.Errorf("HTTPClient = %p, want %p", c.HTTPClient, custom)
	}
	if c.HTTPClient.Timeout != 7*time.Second {
		t.Errorf("supplied Timeout not preserved: got %v", c.HTTPClient.Timeout)
	}
}

type stubTransport struct{ called bool }

func (s *stubTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	s.called = true
	return nil, http.ErrServerClosed
}

func TestWithTransport(t *testing.T) {
	rt := &stubTransport{}
	c := NewClient(WithTransport(rt))
	if c.HTTPClient.Transport != rt {
		t.Errorf("Transport not set; got %v", c.HTTPClient.Transport)
	}
	// Other Client defaults should remain.
	if c.HTTPClient.Timeout == 0 {
		t.Error("WithTransport should preserve default timeout")
	}
}

// WithTransport over a Client whose HTTPClient was nilled out (via an
// earlier custom option) should still attach without panicking.
func TestWithTransport_NilHTTPClient(t *testing.T) {
	c := DefaultClient()
	c.HTTPClient = nil
	rt := &stubTransport{}
	WithTransport(rt)(c)
	if c.HTTPClient == nil || c.HTTPClient.Transport != rt {
		t.Errorf("WithTransport should backfill HTTPClient; got %+v", c.HTTPClient)
	}
}
