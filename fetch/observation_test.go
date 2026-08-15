package fetch

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchObserved(t *testing.T) {
	content := []byte("observed artifact content")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/artifact":
			http.Redirect(w, r, "/download", http.StatusFound)
		case "/download":
			w.Header().Set("Cache-Control", "public, max-age=3600")
			w.Header().Set("Content-Disposition", `attachment; filename="artifact.tgz"`)
			w.Header().Set("Content-Type", "application/gzip")
			w.Header().Set("ETag", `"abc123"`)
			w.Header().Set("Set-Cookie", "session=secret")
			w.Header().Set("X-Response-Secret", r.Header.Get("Authorization"))
			_, _ = w.Write(content)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	requestedURL := server.URL + "/artifact"
	f := NewFetcher()
	artifact, err := f.FetchObservedWithHeaders(context.Background(), requestedURL, http.Header{
		"Authorization": {"Bearer secret"},
		"X-Custom":      {"caller value"},
	})
	if err != nil {
		t.Fatalf("FetchObservedWithHeaders failed: %v", err)
	}
	defer func() { _ = artifact.Body.Close() }()

	observation := artifact.Observation
	if observation.RequestedURL != requestedURL {
		t.Errorf("RequestedURL = %q, want %q", observation.RequestedURL, requestedURL)
	}
	if observation.FinalURL != server.URL+"/download" {
		t.Errorf("FinalURL = %q, want %q", observation.FinalURL, server.URL+"/download")
	}
	if observation.ResponseTime <= 0 {
		t.Errorf("ResponseTime = %v, want a positive duration", observation.ResponseTime)
	}
	if observation.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want %d", observation.StatusCode, http.StatusOK)
	}
	if observation.DeclaredSize != int64(len(content)) {
		t.Errorf("DeclaredSize = %d, want %d", observation.DeclaredSize, len(content))
	}
	if observation.MediaType != "application/gzip" {
		t.Errorf("MediaType = %q, want %q", observation.MediaType, "application/gzip")
	}
	if observation.Headers.Get("Cache-Control") != "public, max-age=3600" {
		t.Errorf("Cache-Control = %q, want %q", observation.Headers.Get("Cache-Control"), "public, max-age=3600")
	}
	if observation.Headers.Get("Content-Disposition") != `attachment; filename="artifact.tgz"` {
		t.Errorf("Content-Disposition = %q", observation.Headers.Get("Content-Disposition"))
	}
	if observation.Headers.Get("ETag") != `"abc123"` {
		t.Errorf("ETag = %q, want %q", observation.Headers.Get("ETag"), `"abc123"`)
	}
	for _, name := range []string{"Authorization", "X-Custom", "Set-Cookie", "X-Response-Secret"} {
		if observation.Headers.Get(name) != "" {
			t.Errorf("observation contains disallowed header %q", name)
		}
	}
	if observation.Complete {
		t.Error("Complete = true before response body reached EOF")
	}
	if observation.ByteCount != 0 {
		t.Errorf("ByteCount = %d before EOF, want 0", observation.ByteCount)
	}
	if observation.Digests != nil {
		t.Errorf("Digests = %v before EOF, want nil", observation.Digests)
	}

	body, err := io.ReadAll(artifact.Body)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	if string(body) != string(content) {
		t.Errorf("body = %q, want %q", body, content)
	}
	if !observation.Complete {
		t.Error("Complete = false after response body reached EOF")
	}
	if observation.ByteCount != int64(len(content)) {
		t.Errorf("ByteCount = %d, want %d", observation.ByteCount, len(content))
	}

	sha256Sum := sha256.Sum256(content)
	if observation.Digests["sha256"] != hex.EncodeToString(sha256Sum[:]) {
		t.Errorf("sha256 digest = %q", observation.Digests["sha256"])
	}
	sha512Sum := sha512.Sum512(content)
	if observation.Digests["sha512"] != hex.EncodeToString(sha512Sum[:]) {
		t.Errorf("sha512 digest = %q", observation.Digests["sha512"])
	}
}

func TestFetchObservedIncompleteRead(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("content that is not fully read"))
	}))
	defer server.Close()

	f := NewFetcher()
	artifact, err := f.FetchObserved(context.Background(), server.URL+"/artifact")
	if err != nil {
		t.Fatalf("FetchObserved failed: %v", err)
	}

	buffer := make([]byte, 4)
	if _, err := artifact.Body.Read(buffer); err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if err := artifact.Body.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if artifact.Observation.Complete {
		t.Error("Complete = true after an incomplete read")
	}
	if artifact.Observation.ByteCount != 0 {
		t.Errorf("ByteCount = %d after an incomplete read, want 0", artifact.Observation.ByteCount)
	}
	if artifact.Observation.Digests != nil {
		t.Errorf("Digests = %v after an incomplete read, want nil", artifact.Observation.Digests)
	}
}
