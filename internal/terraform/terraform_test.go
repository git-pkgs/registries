package terraform

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
		if r.URL.Path != "/v1/modules/hashicorp/consul/aws" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}

		resp := moduleResponse{
			ID:          "hashicorp/consul/aws/0.11.0",
			Namespace:   "hashicorp",
			Name:        "consul",
			Provider:    "aws",
			Description: "A Terraform module for deploying Consul on AWS",
			Source:      "github.com/hashicorp/terraform-aws-consul",
			Version:     "0.11.0",
			Downloads:   150000,
			Verified:    true,
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	pkg, err := reg.FetchPackage(context.Background(), "hashicorp/consul/aws")
	if err != nil {
		t.Fatalf("FetchPackage failed: %v", err)
	}

	if pkg.Name != "hashicorp/consul/aws" {
		t.Errorf("expected name 'hashicorp/consul/aws', got %q", pkg.Name)
	}
	if pkg.Description != "A Terraform module for deploying Consul on AWS" {
		t.Errorf("unexpected description: %q", pkg.Description)
	}
	if pkg.Repository != "https://github.com/hashicorp/terraform-aws-consul" {
		t.Errorf("unexpected repository: %q", pkg.Repository)
	}
	if pkg.Namespace != "hashicorp" {
		t.Errorf("expected namespace 'hashicorp', got %q", pkg.Namespace)
	}
}

func TestFetchPackageInvalidName(t *testing.T) {
	reg := New("", core.DefaultClient())
	_, err := reg.FetchPackage(context.Background(), "invalid-name")
	if err == nil {
		t.Error("expected error for invalid name format")
	}
}

func TestFetchVersions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/modules/hashicorp/consul/aws/versions" {
			w.WriteHeader(404)
			return
		}

		resp := moduleVersionsResponse{
			Modules: []moduleVersionsEntry{
				{
					Versions: []versionEntry{
						{Version: "0.9.0"},
						{Version: "0.10.0"},
						{Version: "0.11.0"},
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	versions, err := reg.FetchVersions(context.Background(), "hashicorp/consul/aws")
	if err != nil {
		t.Fatalf("FetchVersions failed: %v", err)
	}

	if len(versions) != 3 {
		t.Fatalf("expected 3 versions, got %d", len(versions))
	}

	// Should be sorted newest first
	if versions[0].Number != "0.9.0" {
		t.Errorf("expected first version '0.9.0', got %q", versions[0].Number)
	}
}

func TestFetchDependencies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/modules/hashicorp/consul/aws/0.11.0" {
			w.WriteHeader(404)
			return
		}

		resp := versionEntry{
			Version: "0.11.0",
			Root: rootModule{
				Dependencies: []dependencyEntry{
					{Name: "vpc", Source: "hashicorp/vpc/aws", Version: "3.0.0"},
				},
				Providers: []providerEntry{
					{Name: "aws", Namespace: "hashicorp", Version: ">= 4.0"},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	deps, err := reg.FetchDependencies(context.Background(), "hashicorp/consul/aws", "0.11.0")
	if err != nil {
		t.Fatalf("FetchDependencies failed: %v", err)
	}

	if len(deps) != 2 {
		t.Fatalf("expected 2 dependencies, got %d", len(deps))
	}

	reqMap := make(map[string]string)
	for _, d := range deps {
		reqMap[d.Name] = d.Requirements
	}

	if reqMap["vpc"] != "3.0.0" {
		t.Errorf("unexpected vpc requirement: %q", reqMap["vpc"])
	}
	if reqMap["hashicorp/aws"] != ">= 4.0" {
		t.Errorf("unexpected aws provider requirement: %q", reqMap["hashicorp/aws"])
	}
}

func TestFetchMaintainers(t *testing.T) {
	reg := New("", nil)
	maintainers, err := reg.FetchMaintainers(context.Background(), "hashicorp/consul/aws")
	if err != nil {
		t.Fatalf("FetchMaintainers failed: %v", err)
	}

	if len(maintainers) != 1 {
		t.Fatalf("expected 1 maintainer, got %d", len(maintainers))
	}

	if maintainers[0].Login != "hashicorp" {
		t.Errorf("expected login 'hashicorp', got %q", maintainers[0].Login)
	}
}

func TestParseModuleName(t *testing.T) {
	tests := []struct {
		input     string
		namespace string
		name      string
		provider  string
		ok        bool
	}{
		{"hashicorp/consul/aws", "hashicorp", "consul", "aws", true},
		{"terraform-aws-modules/vpc/aws", "terraform-aws-modules", "vpc", "aws", true},
		{"invalid", "", "", "", false},
		{"only/two", "", "", "", false},
	}

	for _, tt := range tests {
		namespace, name, provider, ok := parseModuleName(tt.input)
		if ok != tt.ok {
			t.Errorf("parseModuleName(%q) ok = %v, want %v", tt.input, ok, tt.ok)
		}
		if namespace != tt.namespace || name != tt.name || provider != tt.provider {
			t.Errorf("parseModuleName(%q) = (%q, %q, %q), want (%q, %q, %q)",
				tt.input, namespace, name, provider, tt.namespace, tt.name, tt.provider)
		}
	}
}

func TestURLBuilder(t *testing.T) {
	reg := New("https://registry.terraform.io", nil)
	urls := reg.URLs()

	tests := []struct {
		name     string
		fn       func() string
		expected string
	}{
		{"registry", func() string { return urls.Registry("hashicorp/consul/aws", "0.11.0") }, "https://registry.terraform.io/modules/hashicorp/consul/aws/0.11.0"},
		{"registry_no_version", func() string { return urls.Registry("hashicorp/consul/aws", "") }, "https://registry.terraform.io/modules/hashicorp/consul/aws"},
		{"download", func() string { return urls.Download("hashicorp/consul/aws", "0.11.0") }, "https://registry.terraform.io/v1/modules/hashicorp/consul/aws/0.11.0/download"},
		{"purl", func() string { return urls.PURL("hashicorp/consul/aws", "0.11.0") }, "pkg:terraform/hashicorp/consul/aws@0.11.0"},
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
	if reg.Ecosystem() != "terraform" {
		t.Errorf("expected ecosystem 'terraform', got %q", reg.Ecosystem())
	}
}
