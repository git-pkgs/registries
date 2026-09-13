package registries_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"testing"

	"github.com/git-pkgs/registries"
)

type metadataTransport struct {
	body []byte
	path string
}

func (m metadataTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Path != m.path {
		return nil, fmt.Errorf("unexpected path: %s", r.URL.Path)
	}
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(m.body)), Request: r}, nil
}
func metadataClient(body []byte, ecosystem string) *registries.Client {
	path := "/pypi/demo/json"
	if ecosystem == "pub" {
		path = "/api/packages/demo"
	}
	return &registries.Client{HTTPClient: &http.Client{Transport: metadataTransport{body: body, path: path}}}
}
func metadataDocument(t testing.TB, ecosystem string, count int) []byte {
	t.Helper()
	info := map[string]any{"name": "demo", "version": "2.0.0", "summary": "demo summary", "description": "demo description", "license": "MIT", "home_page": "https://example.invalid", "homepage": "https://example.invalid", "keywords": "one,two"}
	doc := map[string]any{"info": info, "name": "demo", "latest": map[string]any{"version": "2.0.0", "published": "2024-01-01T00:00:00Z", "pubspec": info}}
	releases := make(map[string]any, count)
	versions := make([]any, 0, count)
	for i := range count {
		version := fmt.Sprintf("1.0.%d", i)
		releases[version] = []any{map[string]any{"url": "https://example.invalid/demo.whl", "digests": map[string]string{"sha256": "abcdef"}, "upload_time": "2024-01-01T00:00:00", "size": 1024, "packagetype": "bdist_wheel"}}
		versions = append(versions, map[string]any{"version": version, "published": "2024-01-01T00:00:00Z", "pubspec": map[string]any{"license": "MIT", "dependencies": map[string]string{"a": "^1.0.0", "b": "^2.0.0"}}})
	}
	if ecosystem == "pypi" {
		doc["releases"] = releases
		delete(doc, "latest")
	} else {
		doc["versions"] = versions
		delete(doc, "info")
	}
	body, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
func TestPackageMetadataWithReleaseHistory(t *testing.T) {
	for _, eco := range []string{"pypi", "pub"} {
		t.Run(eco, func(t *testing.T) {
			var want *registries.Package
			for _, count := range []int{0, 500} {
				body := metadataDocument(t, eco, count)
				got, err := registries.FetchPackageFromPURL(context.Background(), "pkg:"+eco+"/demo", metadataClient(body, eco))
				if err != nil {
					t.Fatal(err)
				}
				if got.Name != "demo" || got.LatestVersion != "2.0.0" || got.Licenses != "MIT" {
					t.Fatalf("unexpected metadata: %+v", got)
				}
				if want == nil {
					want = got
				} else if !reflect.DeepEqual(got, want) {
					t.Fatalf("release history changed package metadata: got %+v, want %+v", got, want)
				}
				reg, err := registries.New(eco, "", metadataClient(body, eco))
				if err != nil {
					t.Fatal(err)
				}
				versions, err := reg.FetchVersions(context.Background(), "demo")
				if err != nil {
					t.Fatal(err)
				}
				if len(versions) != count {
					t.Fatalf("got %d versions, want %d", len(versions), count)
				}
			}
		})
	}
}
func BenchmarkPackageMetadataHistory(b *testing.B) {
	for _, eco := range []string{"pypi", "pub"} {
		b.Run(eco, func(b *testing.B) {
			for _, count := range []int{10, 500} {
				b.Run(fmt.Sprint(count), func(b *testing.B) {
					body := metadataDocument(b, eco, count)
					client := metadataClient(body, eco)
					purl := "pkg:" + eco + "/demo"
					b.ReportAllocs()
					b.ResetTimer()
					for b.Loop() {
						if _, err := registries.FetchPackageFromPURL(context.Background(), purl, client); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		})
	}
}

func TestPackageMetadataDecodingErrors(t *testing.T) {
	for _, eco := range []string{"pypi", "pub"} {
		t.Run(eco, func(t *testing.T) {
			var doc map[string]json.RawMessage
			if err := json.Unmarshal(metadataDocument(t, eco, 1), &doc); err != nil {
				t.Fatal(err)
			}
			history, selected := "releases", "info"
			if eco == "pub" {
				history, selected = "versions", "latest"
			}
			doc[history] = json.RawMessage(`"invalid history type"`)
			body, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := registries.FetchPackageFromPURL(context.Background(), "pkg:"+eco+"/demo", metadataClient(body, eco)); err != nil {
				t.Fatalf("unused history should be skipped: %v", err)
			}
			reg, err := registries.New(eco, "", metadataClient(body, eco))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := reg.FetchVersions(context.Background(), "demo"); err == nil {
				t.Fatal("version listing accepted invalid history")
			}
			doc[selected] = json.RawMessage(`"invalid metadata type"`)
			invalidMetadata, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			for _, invalid := range [][]byte{invalidMetadata, body[:len(body)-1]} {
				if _, err := registries.FetchPackageFromPURL(context.Background(), "pkg:"+eco+"/demo", metadataClient(invalid, eco)); err == nil {
					t.Fatal("accepted invalid metadata or malformed JSON")
				}
			}
		})
	}
}
