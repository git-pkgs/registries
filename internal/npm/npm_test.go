package npm

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
		resp := map[string]interface{}{
			"_id":         "react",
			"name":        "react",
			"description": "React is a JavaScript library for building user interfaces.",
			"homepage":    "https://reactjs.org/",
			"repository": map[string]string{
				"type": "git",
				"url":  "git+https://github.com/facebook/react.git",
			},
			"dist-tags": map[string]string{"latest": "18.3.1"},
			"versions": map[string]interface{}{
				"18.3.1": map[string]interface{}{
					"name":        "react",
					"version":     "18.3.1",
					"description": "React is a JavaScript library for building user interfaces.",
					"license":     "MIT",
					"keywords":    []string{"react"},
					"dist": map[string]string{
						"integrity": "sha512-wS+hAgJShR0KhEvPJArfuPVN1+Hz1t0Y6n5jLrGQbkb4urgPE/0Rve+1kMB1v/oWgHgm4WIcV+i7F2pTVj+2iQ==",
					},
				},
			},
			"time": map[string]string{
				"18.3.1": "2024-04-26T16:09:06.245Z",
			},
			"maintainers": []map[string]string{
				{"name": "react-bot", "email": "react-core@meta.com"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	pkg, err := reg.FetchPackage(context.Background(), "react")
	if err != nil {
		t.Fatalf("FetchPackage failed: %v", err)
	}

	if pkg.Name != "react" {
		t.Errorf("expected name 'react', got %q", pkg.Name)
	}
	if pkg.Licenses != "MIT" {
		t.Errorf("expected license 'MIT', got %q", pkg.Licenses)
	}
	if pkg.Repository != "https://github.com/facebook/react" {
		t.Errorf("unexpected repository: %q", pkg.Repository)
	}
}

func TestFetchPackageScoped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Path can be encoded in different ways depending on the URL library
		if r.URL.Path != "/%40babel%2Fcore" && r.URL.Path != "/@babel%2Fcore" && r.URL.Path != "/@babel/core" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		resp := map[string]interface{}{
			"_id":         "@babel/core",
			"name":        "@babel/core",
			"description": "Babel compiler core.",
			"dist-tags":   map[string]string{"latest": "7.24.0"},
			"versions": map[string]interface{}{
				"7.24.0": map[string]interface{}{
					"name":    "@babel/core",
					"version": "7.24.0",
					"license": "MIT",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	pkg, err := reg.FetchPackage(context.Background(), "@babel/core")
	if err != nil {
		t.Fatalf("FetchPackage failed: %v", err)
	}

	if pkg.Name != "@babel/core" {
		t.Errorf("expected name '@babel/core', got %q", pkg.Name)
	}
	if pkg.Namespace != "babel" {
		t.Errorf("expected namespace 'babel', got %q", pkg.Namespace)
	}
}

func TestFetchDependencies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"_id":       "express",
			"dist-tags": map[string]string{"latest": "4.19.0"},
			"versions": map[string]interface{}{
				"4.19.0": map[string]interface{}{
					"dependencies": map[string]string{
						"body-parser": "1.20.2",
						"cookie":      "0.6.0",
					},
					"devDependencies": map[string]string{
						"mocha": "10.4.0",
					},
					"optionalDependencies": map[string]string{
						"fsevents": "2.3.3",
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	deps, err := reg.FetchDependencies(context.Background(), "express", "4.19.0")
	if err != nil {
		t.Fatalf("FetchDependencies failed: %v", err)
	}

	if len(deps) != 4 {
		t.Fatalf("expected 4 dependencies, got %d", len(deps))
	}

	runtimeCount := 0
	devCount := 0
	optionalCount := 0
	for _, d := range deps {
		switch d.Scope {
		case core.Runtime:
			runtimeCount++
		case core.Development:
			devCount++
		case core.Optional:
			optionalCount++
			if !d.Optional {
				t.Error("optional dep should have Optional=true")
			}
		}
	}

	if runtimeCount != 2 {
		t.Errorf("expected 2 runtime deps, got %d", runtimeCount)
	}
	if devCount != 1 {
		t.Errorf("expected 1 dev dep, got %d", devCount)
	}
	if optionalCount != 1 {
		t.Errorf("expected 1 optional dep, got %d", optionalCount)
	}
}

func TestFetchMaintainers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"_id":       "lodash",
			"dist-tags": map[string]string{"latest": "4.17.21"},
			"versions": map[string]interface{}{
				"4.17.21": map[string]interface{}{},
			},
			"maintainers": []map[string]string{
				{"name": "jdalton", "email": "john.david.dalton@gmail.com"},
				{"name": "bnjmnt4n", "email": "bnjmnt4n@users.noreply.github.com"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	maintainers, err := reg.FetchMaintainers(context.Background(), "lodash")
	if err != nil {
		t.Fatalf("FetchMaintainers failed: %v", err)
	}

	if len(maintainers) != 2 {
		t.Fatalf("expected 2 maintainers, got %d", len(maintainers))
	}

	if maintainers[0].Login != "jdalton" {
		t.Errorf("expected login 'jdalton', got %q", maintainers[0].Login)
	}
}

func TestURLBuilder(t *testing.T) {
	reg := New("https://registry.npmjs.org", nil)
	urls := reg.URLs()

	tests := []struct {
		name     string
		fn       func() string
		expected string
	}{
		{"registry", func() string { return urls.Registry("lodash", "4.17.21") }, "https://www.npmjs.com/package/lodash/v/4.17.21"},
		{"download", func() string { return urls.Download("lodash", "4.17.21") }, "https://registry.npmjs.org/lodash/-/lodash-4.17.21.tgz"},
		{"scoped download", func() string { return urls.Download("@babel/core", "7.24.0") }, "https://registry.npmjs.org/@babel/core/-/core-7.24.0.tgz"},
		{"purl", func() string { return urls.PURL("lodash", "4.17.21") }, "pkg:npm/lodash@4.17.21"},
		{"scoped purl", func() string { return urls.PURL("@babel/core", "7.24.0") }, "pkg:npm/@babel/core@7.24.0"},
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

func TestExtractNamespace(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
	}{
		{"lodash", ""},
		{"@babel/core", "babel"},
		{"@types/node", "types"},
		{"express", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractNamespace(tt.name)
			if got != tt.namespace {
				t.Errorf("expected namespace %q, got %q", tt.namespace, got)
			}
		})
	}
}
