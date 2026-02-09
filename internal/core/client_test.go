package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDefaultClient_UserAgent(t *testing.T) {
	var gotUA string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client := DefaultClient()
	_, _ = client.GetBody(context.Background(), server.URL)

	if gotUA != "registries" {
		t.Errorf("default User-Agent = %q, want %q", gotUA, "registries")
	}
}

func TestClient_WithUserAgent(t *testing.T) {
	var gotUA string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client := DefaultClient().WithUserAgent("custom-agent/2.0")
	_, _ = client.GetBody(context.Background(), server.URL)

	if gotUA != "custom-agent/2.0" {
		t.Errorf("User-Agent = %q, want %q", gotUA, "custom-agent/2.0")
	}
}

func TestClient_Head_UserAgent(t *testing.T) {
	var gotUA string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := DefaultClient().WithUserAgent("head-test/1.0")
	_, _ = client.Head(context.Background(), server.URL)

	if gotUA != "head-test/1.0" {
		t.Errorf("Head User-Agent = %q, want %q", gotUA, "head-test/1.0")
	}
}
