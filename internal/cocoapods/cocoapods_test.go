package cocoapods

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/git-pkgs/registries/internal/core"
)

func TestFetchPackage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/pods/Alamofire" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}

		resp := podResponse{
			Name: "Alamofire",
			Versions: []versionInfo{
				{
					Name:      "5.8.0",
					CreatedAt: time.Date(2023, 10, 1, 0, 0, 0, 0, time.UTC),
					Spec: podSpec{
						Name:        "Alamofire",
						Version:     "5.8.0",
						Summary:     "Elegant HTTP Networking in Swift",
						Homepage:    "https://github.com/Alamofire/Alamofire",
						License:     "MIT",
						Source:      map[string]interface{}{"git": "https://github.com/Alamofire/Alamofire.git"},
					},
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	pkg, err := reg.FetchPackage(context.Background(), "Alamofire")
	if err != nil {
		t.Fatalf("FetchPackage failed: %v", err)
	}

	if pkg.Name != "Alamofire" {
		t.Errorf("expected name 'Alamofire', got %q", pkg.Name)
	}
	if pkg.Description != "Elegant HTTP Networking in Swift" {
		t.Errorf("unexpected description: %q", pkg.Description)
	}
	if pkg.Repository != "https://github.com/Alamofire/Alamofire" {
		t.Errorf("unexpected repository: %q", pkg.Repository)
	}
	if pkg.Licenses != "MIT" {
		t.Errorf("unexpected licenses: %q", pkg.Licenses)
	}
}

func TestFetchPackageWithMapLicense(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := podResponse{
			Name: "TestPod",
			Versions: []versionInfo{
				{
					Name: "1.0.0",
					Spec: podSpec{
						Name:    "TestPod",
						Summary: "A test pod",
						License: map[string]interface{}{
							"type": "Apache-2.0",
							"file": "LICENSE",
						},
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	pkg, err := reg.FetchPackage(context.Background(), "TestPod")
	if err != nil {
		t.Fatalf("FetchPackage failed: %v", err)
	}

	if pkg.Licenses != "Apache-2.0" {
		t.Errorf("expected license 'Apache-2.0', got %q", pkg.Licenses)
	}
}

func TestFetchVersions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := podResponse{
			Name: "SDWebImage",
			Versions: []versionInfo{
				{Name: "5.18.0", CreatedAt: time.Date(2023, 11, 1, 0, 0, 0, 0, time.UTC)},
				{Name: "5.17.0", CreatedAt: time.Date(2023, 9, 1, 0, 0, 0, 0, time.UTC)},
				{Name: "5.16.0", CreatedAt: time.Date(2023, 7, 1, 0, 0, 0, 0, time.UTC)},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	versions, err := reg.FetchVersions(context.Background(), "SDWebImage")
	if err != nil {
		t.Fatalf("FetchVersions failed: %v", err)
	}

	if len(versions) != 3 {
		t.Fatalf("expected 3 versions, got %d", len(versions))
	}

	if versions[0].Number != "5.18.0" {
		t.Errorf("expected version '5.18.0', got %q", versions[0].Number)
	}
	if versions[0].PublishedAt.IsZero() {
		t.Error("expected non-zero published time")
	}
}

func TestFetchDependencies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := podResponse{
			Name: "Moya",
			Versions: []versionInfo{
				{
					Name: "15.0.0",
					Spec: podSpec{
						Name:    "Moya",
						Version: "15.0.0",
						Dependencies: map[string]interface{}{
							"Alamofire": "~> 5.0",
							"RxSwift":   []interface{}{"~> 6.0", ">= 6.0.0"},
						},
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	deps, err := reg.FetchDependencies(context.Background(), "Moya", "15.0.0")
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

	if reqMap["Alamofire"] != "~> 5.0" {
		t.Errorf("unexpected Alamofire requirement: %q", reqMap["Alamofire"])
	}
	if reqMap["RxSwift"] != "~> 6.0, >= 6.0.0" {
		t.Errorf("unexpected RxSwift requirement: %q", reqMap["RxSwift"])
	}
}

func TestFetchMaintainers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := podResponse{
			Name: "SnapKit",
			Owners: []ownerInfo{
				{Name: "Robert Payne", Email: "robertpayne@me.com"},
				{Name: "SnapKit", Email: "info@snapkit.io"},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	maintainers, err := reg.FetchMaintainers(context.Background(), "SnapKit")
	if err != nil {
		t.Fatalf("FetchMaintainers failed: %v", err)
	}

	if len(maintainers) != 2 {
		t.Fatalf("expected 2 maintainers, got %d", len(maintainers))
	}

	if maintainers[0].Name != "Robert Payne" {
		t.Errorf("expected name 'Robert Payne', got %q", maintainers[0].Name)
	}
	if maintainers[0].Email != "robertpayne@me.com" {
		t.Errorf("unexpected email: %q", maintainers[0].Email)
	}
}

func TestURLBuilder(t *testing.T) {
	reg := New("https://trunk.cocoapods.org", nil)
	urls := reg.URLs()

	tests := []struct {
		name     string
		fn       func() string
		expected string
	}{
		{"registry", func() string { return urls.Registry("Alamofire", "5.8.0") }, "https://cocoapods.org/pods/Alamofire"},
		{"documentation", func() string { return urls.Documentation("Alamofire", "5.8.0") }, "https://cocoadocs.org/docsets/Alamofire/5.8.0/"},
		{"purl", func() string { return urls.PURL("Alamofire", "5.8.0") }, "pkg:cocoapods/Alamofire@5.8.0"},
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
	if reg.Ecosystem() != "cocoapods" {
		t.Errorf("expected ecosystem 'cocoapods', got %q", reg.Ecosystem())
	}
}
