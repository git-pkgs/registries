package terraform

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/git-pkgs/registries/client"
	"github.com/git-pkgs/registries/internal/core"
)

// A registry names where its module and provider APIs live in its service
// discovery document. Requests go there, and the document is read once.
func TestServiceDiscovery(t *testing.T) {
	var discoveries atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case discoveryPath:
			discoveries.Add(1)
			// A relative and an absolute URL, as the protocol allows both.
			_, _ = w.Write([]byte(`{"modules.v1": "/api/terraform/modules/", "providers.v1": "` + server.URL + `/api/terraform/providers/"}`))
		case "/api/terraform/providers/hashicorp/random/versions":
			_, _ = w.Write([]byte(providerVersionsJSON))
		case "/api/terraform/modules/hashicorp/consul/aws/versions":
			_, _ = w.Write([]byte(`{"modules": [{"versions": [{"version": "0.11.0"}]}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	r := New(server.URL, core.DefaultClient())
	if versions, err := r.FetchVersions(context.Background(), "hashicorp/random"); err != nil || len(versions) != 3 {
		t.Errorf("provider versions = %v, %v; want 3", versions, err)
	}
	if versions, err := r.FetchVersions(context.Background(), "hashicorp/consul/aws"); err != nil || len(versions) != 1 {
		t.Errorf("module versions = %v, %v; want 1", versions, err)
	}
	if got := discoveries.Load(); got != 1 {
		t.Errorf("discovery read %d times, want once", got)
	}
}

// Without a discovery document, or without an entry for a service, the
// registry's base URL serves the protocol at /v1/modules and /v1/providers.
func TestServiceDiscoveryFallsBackToTheBaseURL(t *testing.T) {
	for name, document := range map[string]string{
		"no document":  "",
		"no entry":     `{"login.v1": {"client": "terraform-cli"}}`,
		"entry string": `{"providers.v1": 42, "modules.v1": ["x"]}`,
		// A host that answers unknown paths with an HTML page.
		"not json": `<!doctype html><html><body>app</body></html>`,
		// Refused, as by a host that serves no discovery.
		"forbidden": "403",
	} {
		t.Run(name, func(t *testing.T) {
			routes := map[string]string{
				"/registry/v1/providers/hashicorp/random/versions":   providerVersionsJSON,
				"/registry/v1/modules/hashicorp/consul/aws/versions": `{"modules": [{"versions": [{"version": "0.11.0"}]}]}`,
			}
			if document != "" && document != "403" {
				routes[discoveryPath] = document
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == discoveryPath && document == "403" {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				body, ok := routes[r.URL.Path]
				if !ok {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				_, _ = w.Write([]byte(body))
			}))
			t.Cleanup(server.Close)
			r := New(server.URL+"/registry", core.DefaultClient())
			if versions, err := r.FetchVersions(context.Background(), "hashicorp/random"); err != nil || len(versions) != 3 {
				t.Errorf("provider versions = %v, %v; want 3", versions, err)
			}
			if versions, err := r.FetchVersions(context.Background(), "hashicorp/consul/aws"); err != nil || len(versions) != 1 {
				t.Errorf("module versions = %v, %v; want 1", versions, err)
			}
		})
	}
}

// A discovery document that can't be read is an error, and is read again
// next time rather than replaced by the fallback for good.
func TestServiceDiscoveryFailureIsRetried(t *testing.T) {
	var failing atomic.Bool
	failing.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == discoveryPath && failing.Load():
			w.WriteHeader(http.StatusInternalServerError)
		case r.URL.Path == discoveryPath:
			_, _ = w.Write([]byte(`{"providers.v1": "/p/"}`))
		case r.URL.Path == "/p/hashicorp/random/versions":
			_, _ = w.Write([]byte(providerVersionsJSON))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	client := core.DefaultClient()
	client.MaxRetries = 0
	r := New(server.URL, client)
	if _, err := r.FetchVersions(context.Background(), "hashicorp/random"); err == nil {
		t.Fatal("FetchVersions succeeded although discovery failed")
	}
	failing.Store(false)
	if versions, err := r.FetchVersions(context.Background(), "hashicorp/random"); err != nil || len(versions) != 3 {
		t.Errorf("after recovery: %v, %v; want 3 versions", versions, err)
	}
}

// Only http and https service URLs are followed.
func TestServiceDiscoveryRefusesOtherSchemes(t *testing.T) {
	for _, location := range []string{"file:///etc/", "ftp://registry.example.com/providers/"} {
		server := providerServer(t, map[string]string{discoveryPath: `{"providers.v1": "` + location + `"}`})
		_, err := New(server.URL, core.DefaultClient()).FetchVersions(context.Background(), "hashicorp/random")
		if err == nil || !strings.Contains(err.Error(), "providers.v1") {
			t.Errorf("%s: err = %v, want a refused providers.v1", location, err)
		}
	}
}

// Service URLs are relative to the document's final URL, after redirects.
func TestServiceDiscoveryResolvesAgainstTheRedirectedDocument(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case discoveryPath:
			http.Redirect(w, r, "/discovery/terraform.json", http.StatusFound)
		case "/discovery/terraform.json":
			_, _ = w.Write([]byte(`{"providers.v1": "./providers/"}`))
		case "/discovery/providers/hashicorp/random/versions":
			_, _ = w.Write([]byte(providerVersionsJSON))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	if versions, err := New(server.URL, core.DefaultClient()).FetchVersions(context.Background(), "hashicorp/random"); err != nil || len(versions) != 3 {
		t.Errorf("versions = %v, %v; want 3", versions, err)
	}
}

type recordingTransport struct {
	paths []string
}

func (rt *recordingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	rt.paths = append(rt.paths, r.URL.Host+r.URL.Path)
	body := `{"modules": [{"versions": [{"version": "0.11.0"}]}]}`
	if strings.HasPrefix(r.URL.Path, "/v1/providers/") {
		body = providerVersionsJSON
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}

// registry.terraform.io serves the protocol at /v1/modules/ and
// /v1/providers/, so the default registry is asked without discovery: the
// PURL helpers build a new Registry per call.
func TestDefaultRegistrySkipsDiscovery(t *testing.T) {
	rt := &recordingTransport{}
	r := New(DefaultURL, core.NewClient(client.WithTransport(rt)))
	if _, err := r.FetchVersions(context.Background(), "hashicorp/random"); err != nil {
		t.Fatalf("provider versions: %v", err)
	}
	if _, err := r.FetchVersions(context.Background(), "hashicorp/consul/aws"); err != nil {
		t.Fatalf("module versions: %v", err)
	}
	want := []string{
		"registry.terraform.io/v1/providers/hashicorp/random/versions",
		"registry.terraform.io/v1/modules/hashicorp/consul/aws/versions",
	}
	if strings.Join(rt.paths, " ") != strings.Join(want, " ") {
		t.Errorf("requests = %v, want %v", rt.paths, want)
	}
}
