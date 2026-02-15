package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBuildURLs(t *testing.T) {
	urls := &BaseURLs{
		RegistryFn:      func(name, version string) string { return "https://example.com/" + name },
		DownloadFn:      func(name, version string) string { return "https://example.com/" + name + "/download" },
		DocumentationFn: func(name, version string) string { return "https://docs.example.com/" + name },
		PURLFn:          func(name, version string) string { return "pkg:test/" + name + "@" + version },
	}

	got := BuildURLs(urls, "foo", "1.0.0")

	expected := map[string]string{
		"registry": "https://example.com/foo",
		"download": "https://example.com/foo/download",
		"docs":     "https://docs.example.com/foo",
		"purl":     "pkg:test/foo@1.0.0",
	}

	if len(got) != len(expected) {
		t.Fatalf("BuildURLs returned %d entries, want %d", len(got), len(expected))
	}

	for k, want := range expected {
		if got[k] != want {
			t.Errorf("BuildURLs[%q] = %q, want %q", k, got[k], want)
		}
	}
}

func TestBuildURLs_OmitsEmpty(t *testing.T) {
	urls := &BaseURLs{
		RegistryFn: func(name, version string) string { return "https://example.com/" + name },
		// Documentation and Download are nil, so they return ""
	}

	got := BuildURLs(urls, "foo", "1.0.0")

	if _, ok := got["docs"]; ok {
		t.Error("BuildURLs should omit empty docs URL")
	}
	if _, ok := got["download"]; ok {
		t.Error("BuildURLs should omit empty download URL")
	}
	if _, ok := got["registry"]; !ok {
		t.Error("BuildURLs should include non-empty registry URL")
	}
	// PURL falls back to generic format in BaseURLs
	if _, ok := got["purl"]; !ok {
		t.Error("BuildURLs should include PURL fallback")
	}
}

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
