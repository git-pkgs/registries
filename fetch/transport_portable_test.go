package fetch_test

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/git-pkgs/registries/fetch"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPortableFetcherWithHTTPClient(t *testing.T) {
	const artifactURL = "https://repo.example.test/org/example/demo/1.0/demo-1.0.pom"
	const content = `<project><modelVersion>4.0.0</modelVersion><groupId>org.example</groupId><artifactId>demo</artifactId><version>1.0</version></project>`
	var methods []string
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		methods = append(methods, request.Method)
		if request.URL.String() != artifactURL || request.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Errorf("unexpected request: %s, headers: %v", request.URL, request.Header)
		}
		body := content
		if request.Method == http.MethodHead {
			body = ""
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Request:    request,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header: http.Header{
				"Content-Length": {strconv.Itoa(len(content))},
				"Content-Type":   {"application/xml"},
				"Etag":           {`"fixture"`},
			},
		}, nil
	})
	f := fetch.NewFetcher(
		fetch.WithHTTPClient(&http.Client{Transport: transport}),
		fetch.WithAuthFunc(func(string) (string, string) { return "Authorization", "Bearer fixture-token" }),
	)
	t.Cleanup(func() {
		if err := f.Close(); err != nil {
			t.Error(err)
		}
	})

	artifact, err := f.Fetch(context.Background(), artifactURL)
	if err != nil {
		t.Fatal(err)
	}
	checkArtifactBody(t, artifact, content)

	observed, err := f.FetchObserved(context.Background(), artifactURL)
	if err != nil {
		t.Fatal(err)
	}
	checkArtifactBody(t, observed.Artifact, content)
	if !observed.Observation.Complete || observed.Observation.ByteCount != int64(len(content)) {
		t.Errorf("observation = %+v", observed.Observation)
	}

	size, contentType, err := f.Head(context.Background(), artifactURL)
	if err != nil || size != int64(len(content)) || contentType != "application/xml" {
		t.Errorf("Head = %d, %q, %v", size, contentType, err)
	}
	if got := strings.Join(methods, ","); got != "GET,GET,HEAD" {
		t.Errorf("methods = %q", got)
	}
}

func checkArtifactBody(t *testing.T, artifact *fetch.Artifact, want string) {
	t.Helper()
	body, err := io.ReadAll(artifact.Body)
	if closeErr := artifact.Body.Close(); closeErr != nil {
		t.Error(closeErr)
	}
	if err != nil || string(body) != want {
		t.Errorf("body = %q, %v", body, err)
	}
	if artifact.Size != int64(len(want)) || artifact.ContentType != "application/xml" || artifact.ETag != `"fixture"` {
		t.Errorf("artifact = %+v", artifact)
	}
}
