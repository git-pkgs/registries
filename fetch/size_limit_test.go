package fetch

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestArtifactSizeLimitHTTP(t *testing.T) {
	for _, declared := range []bool{false, true} {
		for _, tc := range []struct {
			name  string
			body  string
			limit int64
			want  error
		}{
			{"unlimited", "12345", 0, nil},
			{"negative", "12345", -1, nil},
			{"exact", "1234", 4, nil},
			{"over", "12345", 4, ErrArtifactTooLarge},
			{"max-int", "1234", math.MaxInt64, nil},
		} {
			t.Run(fmt.Sprintf("%s/declared=%v", tc.name, declared), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("X-Check") != "present" && strings.HasSuffix(r.URL.Path, "headers") {
						t.Error("caller header lost")
					}
					if declared {
						w.Header().Set("Content-Length", strconv.Itoa(len(tc.body)))
					} else {
						w.(http.Flusher).Flush()
					}
					_, _ = io.WriteString(w, tc.body)
				}))
				defer server.Close()
				f := NewFetcher(WithHTTPClient(server.Client()), WithMaxArtifactSize(tc.limit))
				defer func() { _ = f.Close() }()
				for _, method := range []string{"fetch", "fetch-headers", "observed", "observed-headers"} {
					assertLimitedFetch(t, f, server.URL+"/"+method, method, tc.body, tc.want)
				}
			})
		}
	}
}

func assertLimitedFetch(t *testing.T, f *Fetcher, url, method, body string, want error) {
	t.Helper()
	var artifact *Artifact
	var observation *FetchObservation
	var err error
	headers := http.Header{"X-Check": {"present"}}
	switch method {
	case "fetch":
		artifact, err = f.Fetch(context.Background(), url)
	case "fetch-headers":
		artifact, err = f.FetchWithHeaders(context.Background(), url, headers)
	default:
		var observed *ObservedArtifact
		if method == "observed" {
			observed, err = f.FetchObserved(context.Background(), url)
		} else {
			observed, err = f.FetchObservedWithHeaders(context.Background(), url, headers)
		}
		if observed != nil {
			artifact, observation = observed.Artifact, observed.Observation
		}
	}
	var data []byte
	if err == nil {
		data, err = io.ReadAll(artifact.Body)
		_ = artifact.Body.Close()
	}
	if !errors.Is(err, want) {
		t.Fatalf("%s: error = %v, want %v", method, err, want)
	}
	if want == nil && string(data) != body {
		t.Fatalf("%s: body = %q", method, data)
	}
	if observation == nil {
		return
	}
	if want != nil {
		if observation.Complete || observation.Digests != nil || observation.ByteCount != 0 {
			t.Fatalf("rejected artifact produced completed observation: %+v", observation)
		}
		if _, err := observation.Artifact("pkg:npm/test@1", "test.tgz"); err == nil {
			t.Fatal("rejected observation converted to artifact")
		}
	} else if !observation.Complete || observation.ByteCount != int64(len(body)) || observation.Digests["sha256"] != fmt.Sprintf("%x", sha256.Sum256([]byte(body))) {
		t.Fatalf("completed observation = %+v", observation)
	}
}

type sizeTestBody struct {
	io.Reader
	reads    int
	closed   bool
	finalEOF bool
}

func (b *sizeTestBody) Read(p []byte) (int, error) {
	b.reads++
	n, err := b.Reader.Read(p)
	if b.finalEOF {
		err = io.EOF
	}
	return n, err
}

func (b *sizeTestBody) Close() error { b.closed = true; return nil }

func TestArtifactSizeLimitBeforeReadingOrObserving(t *testing.T) {
	for _, declared := range []bool{true, false} {
		body := &sizeTestBody{Reader: strings.NewReader("12345"), finalEOF: true}
		calls := 0
		f := NewFetcher(WithMaxArtifactSize(4), WithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			header := http.Header{}
			if declared {
				header.Set("Content-Length", "5")
			}
			return &http.Response{StatusCode: http.StatusOK, Header: header, Body: body, Request: req}, nil
		})}))
		defer func() { _ = f.Close() }()
		observed, err := f.FetchObserved(context.Background(), "https://example.com/archive")
		if declared {
			if !errors.Is(err, ErrArtifactTooLarge) || observed != nil || !body.closed || body.reads != 0 || calls != 1 {
				t.Fatalf("declared rejection: %v; body %+v, calls %d", err, body, calls)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		_, err = io.ReadAll(observed.Body)
		_ = observed.Body.Close()
		if !errors.Is(err, ErrArtifactTooLarge) || observed.Observation.Complete || !body.closed {
			t.Fatalf("overflow alongside EOF: %v; observation %+v", err, observed.Observation)
		}
	}
}

func TestArtifactSizeRejectionDoesNotTripBreaker(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/big" {
			_, _ = io.WriteString(w, "12345")
			return
		}
		_, _ = io.WriteString(w, "1234")
	}))
	defer server.Close()
	f := NewFetcher(WithHTTPClient(server.Client()), WithMaxArtifactSize(4))
	defer func() { _ = f.Close() }()
	breaker := NewCircuitBreakerFetcher(f)
	for range cbThreshold + 1 {
		if _, err := breaker.Fetch(context.Background(), server.URL+"/big"); !errors.Is(err, ErrArtifactTooLarge) {
			t.Fatal(err)
		}
		if _, err := breaker.FetchObserved(context.Background(), server.URL+"/big"); !errors.Is(err, ErrArtifactTooLarge) {
			t.Fatal(err)
		}
	}
	artifact, err := breaker.Fetch(context.Background(), server.URL+"/small")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = artifact.Body.Close() }()
	data, err := io.ReadAll(artifact.Body)
	if err != nil || string(data) != "1234" {
		t.Fatalf("small artifact = %q, %v", data, err)
	}
}
