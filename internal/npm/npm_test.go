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
		_ = json.NewEncoder(w).Encode(resp)
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

// TestFetchVersions_Provenance asserts the dist.attestations pointer
// and dist.signatures slice round-trip into Version.Metadata as the
// typed AttestationRef and []Signature shapes.
func TestFetchVersions_Provenance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := map[string]interface{}{
			"_id":       "demo",
			"name":      "demo",
			"dist-tags": map[string]string{"latest": "1.0.0"},
			"versions": map[string]interface{}{
				"1.0.0": map[string]interface{}{
					"name":    "demo",
					"version": "1.0.0",
					"license": "MIT",
					"dist": map[string]interface{}{
						"integrity": "sha512-deadbeef",
						"tarball":   "https://example.invalid/demo-1.0.0.tgz",
						"attestations": map[string]interface{}{
							"url": "https://registry.example.invalid/-/npm/v1/attestations/demo@1.0.0",
							"provenance": map[string]string{
								"predicateType": "https://slsa.dev/provenance/v1",
							},
						},
						"signatures": []map[string]string{
							{"sig": "MEUCIabc==", "keyid": "SHA256:jl3bwswu80PjjokCgh0o2w5c2U4LhQAE57gj9cz1kzA"},
						},
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	versions, err := reg.FetchVersions(context.Background(), "demo")
	if err != nil {
		t.Fatalf("FetchVersions: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("versions = %d, want 1", len(versions))
	}

	v := versions[0]
	att, ok := v.Metadata["npm:attestations"].(*AttestationRef)
	if !ok || att == nil {
		t.Fatalf("Metadata[npm:attestations] not a *AttestationRef: %T", v.Metadata["npm:attestations"])
	}
	if att.URL != "https://registry.example.invalid/-/npm/v1/attestations/demo@1.0.0" {
		t.Errorf("attestation URL = %q", att.URL)
	}
	if att.Provenance.PredicateType != "https://slsa.dev/provenance/v1" {
		t.Errorf("attestation predicate type = %q", att.Provenance.PredicateType)
	}

	sigs, ok := v.Metadata["npm:signatures"].([]Signature)
	if !ok {
		t.Fatalf("Metadata[npm:signatures] not a []Signature: %T", v.Metadata["npm:signatures"])
	}
	if len(sigs) != 1 || sigs[0].Sig != "MEUCIabc==" {
		t.Errorf("signatures = %+v", sigs)
	}
}

// TestFetchVersions_NoProvenance asserts that versions without
// dist.attestations / dist.signatures expose nil and (typed) nil
// slice without panic.
func TestFetchVersions_NoProvenance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := map[string]interface{}{
			"_id":       "plain",
			"name":      "plain",
			"dist-tags": map[string]string{"latest": "1.0.0"},
			"versions": map[string]interface{}{
				"1.0.0": map[string]interface{}{
					"name":    "plain",
					"version": "1.0.0",
					"dist":    map[string]interface{}{"integrity": "sha512-xxx", "tarball": "https://example.invalid/plain-1.0.0.tgz"},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	versions, err := reg.FetchVersions(context.Background(), "plain")
	if err != nil {
		t.Fatal(err)
	}
	if att, _ := versions[0].Metadata["npm:attestations"].(*AttestationRef); att != nil {
		t.Errorf("expected nil attestation, got %+v", att)
	}
	if sigs, _ := versions[0].Metadata["npm:signatures"].([]Signature); len(sigs) != 0 {
		t.Errorf("expected empty signatures, got %+v", sigs)
	}
	if cp, _ := versions[0].Metadata["npm:contentPolicy"].(*ContentPolicy); cp != nil {
		t.Errorf("expected nil contentPolicy, got %+v", cp)
	}
}

// TestFetchVersions_ContentPolicy asserts the package.json contentPolicy
// field round-trips into Version.Metadata as a typed *ContentPolicy.
func TestFetchVersions_ContentPolicy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := map[string]interface{}{
			"_id":       "dualuse",
			"name":      "dualuse",
			"dist-tags": map[string]string{"latest": "1.0.0"},
			"versions": map[string]interface{}{
				"1.0.0": map[string]interface{}{
					"name":          "dualuse",
					"version":       "1.0.0",
					"contentPolicy": map[string]string{"class": "dual-use"},
					"dist":          map[string]interface{}{"integrity": "sha512-xxx", "tarball": "https://example.invalid/dualuse-1.0.0.tgz"},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	versions, err := reg.FetchVersions(context.Background(), "dualuse")
	if err != nil {
		t.Fatalf("FetchVersions: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("versions = %d, want 1", len(versions))
	}

	cp, ok := versions[0].Metadata["npm:contentPolicy"].(*ContentPolicy)
	if !ok || cp == nil {
		t.Fatalf("Metadata[npm:contentPolicy] not a *ContentPolicy: %T", versions[0].Metadata["npm:contentPolicy"])
	}
	if cp.Class != "dual-use" {
		t.Errorf("contentPolicy.class = %q, want %q", cp.Class, "dual-use")
	}
}

// TestFetchVersions_LegacyEnginesArray verifies that versions whose
// "engines" field is a legacy array (e.g. ["node","rhino"], as published
// by early lodash 0.x releases) are parsed without error. The struct field
// must accept both the modern object form and the legacy array form.
func TestFetchVersions_LegacyEnginesArray(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := map[string]interface{}{
			"_id":       "legacy",
			"name":      "legacy",
			"dist-tags": map[string]string{"latest": "0.1.0"},
			"versions": map[string]interface{}{
				"0.1.0": map[string]interface{}{
					"name":    "legacy",
					"version": "0.1.0",
					"license": "MIT",
					"engines": []string{"node", "rhino"}, // legacy array form
					"dist":    map[string]interface{}{"integrity": "sha512-legacy"},
				},
				"1.0.0": map[string]interface{}{
					"name":    "legacy",
					"version": "1.0.0",
					"engines": map[string]string{"node": ">=10"}, // modern object form
					"dist":    map[string]interface{}{"integrity": "sha512-modern"},
				},
			},
			"time": map[string]string{
				"0.1.0": "2012-01-01T00:00:00.000Z",
				"1.0.0": "2020-01-01T00:00:00.000Z",
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	versions, err := reg.FetchVersions(context.Background(), "legacy")
	if err != nil {
		t.Fatalf("FetchVersions with legacy engines array failed: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("versions = %d, want 2", len(versions))
	}

	byVersion := map[string]map[string]interface{}{}
	for _, v := range versions {
		byVersion[v.Number] = v.Metadata
	}

	// Legacy array form should round-trip as a []interface{}.
	if engines, ok := byVersion["0.1.0"]["engines"].([]interface{}); !ok {
		t.Errorf("0.1.0 engines = %T, want []interface{}", byVersion["0.1.0"]["engines"])
	} else if len(engines) != 2 || engines[0] != "node" || engines[1] != "rhino" {
		t.Errorf("0.1.0 engines = %+v, want [node rhino]", engines)
	}

	// Modern object form should round-trip as a map[string]interface{}.
	if engines, ok := byVersion["1.0.0"]["engines"].(map[string]interface{}); !ok {
		t.Errorf("1.0.0 engines = %T, want map[string]interface{}", byVersion["1.0.0"]["engines"])
	} else if engines["node"] != ">=10" {
		t.Errorf("1.0.0 engines = %+v, want {node:>=10}", engines)
	}
}

// TestFetchVersions_DeprecatedShapes verifies that the "deprecated" field is
// handled across the shapes npm packuments emit: absent/null, a string
// message, and the legacy boolean form (false == not deprecated,
// true == deprecated with no message). String and true values must mark the
// version StatusDeprecated; absent/null/false must leave it active.
func TestFetchVersions_DeprecatedShapes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := map[string]interface{}{
			"_id":       "shapes",
			"name":      "shapes",
			"dist-tags": map[string]string{"latest": "1.3.0"},
			"versions": map[string]interface{}{
				"1.0.0": map[string]interface{}{ // absent
					"name": "shapes", "version": "1.0.0",
					"dist": map[string]interface{}{"integrity": "sha512-a"},
				},
				"1.1.0": map[string]interface{}{ // string message
					"name": "shapes", "version": "1.1.0",
					"deprecated": "use v2 instead",
					"dist":       map[string]interface{}{"integrity": "sha512-b"},
				},
				"1.2.0": map[string]interface{}{ // boolean false
					"name": "shapes", "version": "1.2.0",
					"deprecated": false,
					"dist":       map[string]interface{}{"integrity": "sha512-c"},
				},
				"1.3.0": map[string]interface{}{ // boolean true
					"name": "shapes", "version": "1.3.0",
					"deprecated": true,
					"dist":       map[string]interface{}{"integrity": "sha512-d"},
				},
			},
			"time": map[string]string{
				"1.0.0": "2020-01-01T00:00:00.000Z",
				"1.1.0": "2020-02-01T00:00:00.000Z",
				"1.2.0": "2020-03-01T00:00:00.000Z",
				"1.3.0": "2020-04-01T00:00:00.000Z",
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	versions, err := reg.FetchVersions(context.Background(), "shapes")
	if err != nil {
		t.Fatalf("FetchVersions failed: %v", err)
	}

	wantStatus := map[string]core.VersionStatus{
		"1.0.0": core.StatusNone,
		"1.1.0": core.StatusDeprecated,
		"1.2.0": core.StatusNone, // boolean false is not deprecated
		"1.3.0": core.StatusDeprecated,
	}
	wantDep := map[string]string{
		"1.0.0": "",
		"1.1.0": "use v2 instead",
		"1.2.0": "",
		"1.3.0": "true",
	}

	byNumber := map[string]core.Version{}
	for _, v := range versions {
		byNumber[v.Number] = v
	}
	for _, num := range []string{"1.0.0", "1.1.0", "1.2.0", "1.3.0"} {
		v, ok := byNumber[num]
		if !ok {
			t.Fatalf("missing version %s", num)
		}
		if v.Status != wantStatus[num] {
			t.Errorf("%s status = %q, want %q", num, v.Status, wantStatus[num])
		}
		if v.Metadata["deprecated"] != wantDep[num] {
			t.Errorf("%s deprecated metadata = %q, want %q", num, v.Metadata["deprecated"], wantDep[num])
		}
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
		_ = json.NewEncoder(w).Encode(resp)
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
		_ = json.NewEncoder(w).Encode(resp)
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
		_ = json.NewEncoder(w).Encode(resp)
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
