package pypi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/git-pkgs/registries/internal/core"
)

func TestFetchPackage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pypi/requests/json" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}

		resp := packageResponse{
			Info: infoBlock{
				Name:              "requests",
				Summary:           "Python HTTP for Humans.",
				License:           "Apache 2.0",
				HomePage:          "https://requests.readthedocs.io",
				Keywords:          "http,web,client",
				ProjectURLs: map[string]string{
					"Source":        "https://github.com/psf/requests",
					"Documentation": "https://requests.readthedocs.io",
				},
			},
			Releases: map[string][]releaseFile{
				"2.31.0": {
					{
						Digests:    map[string]string{"sha256": "abc123"},
						UploadTime: "2023-05-22T12:00:00",
					},
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	pkg, err := reg.FetchPackage(context.Background(), "requests")
	if err != nil {
		t.Fatalf("FetchPackage failed: %v", err)
	}

	if pkg.Name != "requests" {
		t.Errorf("expected name 'requests', got %q", pkg.Name)
	}
	if pkg.Repository != "https://github.com/psf/requests" {
		t.Errorf("unexpected repository: %q", pkg.Repository)
	}
	if pkg.Licenses != "Apache-2.0" {
		t.Errorf("unexpected licenses: %q", pkg.Licenses)
	}
	if len(pkg.Keywords) != 3 {
		t.Errorf("expected 3 keywords, got %d", len(pkg.Keywords))
	}
}

func TestFetchPackageWithLicenseExpression(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := packageResponse{
			Info: infoBlock{
				Name:              "some-package",
				LicenseExpression: "MIT OR Apache-2.0",
				License:           "MIT",
			},
			Releases: map[string][]releaseFile{},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	pkg, err := reg.FetchPackage(context.Background(), "some-package")
	if err != nil {
		t.Fatalf("FetchPackage failed: %v", err)
	}

	// license_expression takes precedence
	if pkg.Licenses != "MIT OR Apache-2.0" {
		t.Errorf("expected license expression, got %q", pkg.Licenses)
	}
}

func TestFetchVersions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := packageResponse{
			Info: infoBlock{Name: "requests"},
			Releases: map[string][]releaseFile{
				"2.31.0": {
					{
						Digests:    map[string]string{"sha256": "abc123"},
						UploadTime: "2023-05-22T12:00:00",
						Yanked:     false,
					},
				},
				"2.30.0": {
					{
						Digests:      map[string]string{"sha256": "def456"},
						UploadTime:   "2023-05-01T12:00:00",
						Yanked:       true,
						YankedReason: "security issue",
					},
				},
				"0.1.0": {}, // empty release
			},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	versions, err := reg.FetchVersions(context.Background(), "requests")
	if err != nil {
		t.Fatalf("FetchVersions failed: %v", err)
	}

	if len(versions) != 3 {
		t.Fatalf("expected 3 versions, got %d", len(versions))
	}

	yankedCount := 0
	for _, v := range versions {
		if v.Status == core.StatusYanked {
			yankedCount++
		}
	}

	if yankedCount != 1 {
		t.Errorf("expected 1 yanked version, got %d", yankedCount)
	}
}

func TestFetchDependencies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pypi/requests/2.31.0/json" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}

		resp := versionInfoResponse{
			Info: infoBlock{
				RequiresDist: []string{
					"charset-normalizer<4,>=2",
					"idna<4,>=2.5",
					"urllib3<3,>=1.21.1",
					"certifi>=2017.4.17",
					"PySocks!=1.5.7,>=1.5.6; extra == 'socks'",
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	deps, err := reg.FetchDependencies(context.Background(), "requests", "2.31.0")
	if err != nil {
		t.Fatalf("FetchDependencies failed: %v", err)
	}

	if len(deps) != 5 {
		t.Fatalf("expected 5 dependencies, got %d", len(deps))
	}

	runtimeCount := 0
	optionalCount := 0
	for _, d := range deps {
		if d.Scope == core.Runtime {
			runtimeCount++
		}
		if d.Optional {
			optionalCount++
		}
	}

	if runtimeCount != 4 {
		t.Errorf("expected 4 runtime deps, got %d", runtimeCount)
	}
	if optionalCount != 1 {
		t.Errorf("expected 1 optional dep, got %d", optionalCount)
	}
}

func TestParsePEP508(t *testing.T) {
	tests := []struct {
		input        string
		name         string
		requirements string
		envMarker    string
	}{
		{"requests>=2.0", "requests", ">=2.0", ""},
		{"charset-normalizer<4,>=2", "charset-normalizer", "<4,>=2", ""},
		{"PySocks!=1.5.7,>=1.5.6; extra == 'socks'", "PySocks", "!=1.5.7,>=1.5.6", "extra == 'socks'"},
		{"typing-extensions; python_version < '3.10'", "typing-extensions", "*", "python_version < '3.10'"},
		{"numpy", "numpy", "*", ""},
		{"foo[bar,baz]>=1.0", "foo", ">=1.0", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			name, req, env := parsePEP508(tt.input)
			if name != tt.name {
				t.Errorf("expected name %q, got %q", tt.name, name)
			}
			if req != tt.requirements {
				t.Errorf("expected requirements %q, got %q", tt.requirements, req)
			}
			if env != tt.envMarker {
				t.Errorf("expected envMarker %q, got %q", tt.envMarker, env)
			}
		})
	}
}

func TestNormalizeName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Requests", "requests"},
		{"typing_extensions", "typing-extensions"},
		{"Flask.SocketIO", "flask-socketio"},
		{"PyYAML", "pyyaml"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := normalizeName(tt.input)
			if got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestURLBuilder(t *testing.T) {
	reg := New("https://pypi.org", nil)
	urls := reg.URLs()

	tests := []struct {
		name     string
		fn       func() string
		expected string
	}{
		{"registry", func() string { return urls.Registry("requests", "2.31.0") }, "https://pypi.org/project/requests/2.31.0/"},
		{"documentation", func() string { return urls.Documentation("requests", "2.31.0") }, "https://requests.readthedocs.io/en/2.31.0/"},
		{"purl", func() string { return urls.PURL("requests", "2.31.0") }, "pkg:pypi/requests@2.31.0"},
		{"purl normalized", func() string { return urls.PURL("typing_extensions", "4.0.0") }, "pkg:pypi/typing-extensions@4.0.0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.fn()
			if got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestEcosystem(t *testing.T) {
	reg := New("", nil)
	if reg.Ecosystem() != "pypi" {
		t.Errorf("expected ecosystem 'pypi', got %q", reg.Ecosystem())
	}
}

func TestParseKeywords(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"http,web,client", 3},
		{"http web client", 3},
		{"", 0},
		{"single", 1},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseKeywords(tt.input)
			if len(got) != tt.expected {
				t.Errorf("expected %d keywords, got %d", tt.expected, len(got))
			}
		})
	}
}
