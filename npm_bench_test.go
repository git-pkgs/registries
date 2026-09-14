package registries_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/git-pkgs/registries"
)

func npmHistory(tb testing.TB, name string, count int) []byte {
	tb.Helper()
	versions := make(map[string]any, count)
	times := make(map[string]string, count)
	for i := range count {
		number := fmt.Sprintf("1.0.%d", i)
		versions[number] = map[string]any{
			"name": name, "version": number, "description": "Package metadata for registry benchmarks",
			"license": "MIT", "keywords": []string{"utilities", "testing"},
			"repository":           map[string]string{"type": "git", "url": "https://example.invalid/project.git"},
			"dependencies":         map[string]string{"alpha": "^2.0.0", "beta": "~3.1.0", "gamma": ">=1"},
			"devDependencies":      map[string]string{"test-runner": "^4.0.0", "linter": "^5.0.0"},
			"optionalDependencies": map[string]string{"native-addon": "^1.0.0"},
			"engines":              map[string]string{"node": ">=18"},
			"_npmUser":             map[string]string{"name": "publisher", "email": "publisher@example.invalid"},
			"maintainers":          []map[string]string{{"name": "publisher", "email": "publisher@example.invalid"}},
			"dist": map[string]string{
				"tarball":   "https://example.invalid/package-" + number + ".tgz",
				"integrity": "sha512-Zml4dHVyZQ==", "shasum": "0123456789012345678901234567890123456789",
			},
		}
		times[number] = "2024-01-15T12:00:00Z"
	}
	data, err := json.Marshal(map[string]any{
		"_id": name, "name": name, "versions": versions, "time": times,
		"dist-tags": map[string]string{"latest": fmt.Sprintf("1.0.%d", count-1)},
		"homepage":  "https://example.invalid", "description": "Package metadata for registry benchmarks",
	})
	if err != nil {
		tb.Fatal(err)
	}
	return data
}

func npmHistoryServer(tb testing.TB, name string, count int) (string, *atomic.Int64, int) {
	tb.Helper()
	data := npmHistory(tb, name, count)
	requests := new(atomic.Int64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/"+name {
			http.NotFound(w, r)
			return
		}
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}))
	tb.Cleanup(server.Close)
	purl := "pkg:npm/" + name + "@1.0.0?repository_url=" + url.QueryEscape(server.URL)
	return purl, requests, len(data)
}

// This pair matches the registry calls made concurrently by proxy's EnrichFull.
func fetchNPMPair(ctx context.Context, purl string, client *registries.Client) (*registries.Package, *registries.Version, error) {
	var pkg *registries.Package
	var version *registries.Version
	var pkgErr, versionErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		pkg, pkgErr = registries.FetchPackageFromPURL(ctx, purl, client)
	}()
	go func() {
		defer wg.Done()
		version, versionErr = registries.FetchVersionFromPURL(ctx, purl, client)
	}()
	wg.Wait()
	if pkgErr != nil {
		return nil, nil, pkgErr
	}
	return pkg, version, versionErr
}

func TestNPMHistoryPublicLookups(t *testing.T) {
	for _, name := range []string{"history", "@scope/history"} {
		t.Run(name, func(t *testing.T) {
			purl, _, _ := npmHistoryServer(t, name, 100)
			pkg, version, err := fetchNPMPair(context.Background(), purl, registries.DefaultClient())
			if err != nil {
				t.Fatal(err)
			}
			if pkg == nil || pkg.Name != name || pkg.LatestVersion != "1.0.99" || pkg.Licenses != "MIT" {
				t.Fatalf("unexpected package: %+v", pkg)
			}
			if version == nil || version.Number != "1.0.0" || version.Licenses != "MIT" ||
				version.Integrity != "sha512-Zml4dHVyZQ==" || version.PublishedAt.Format("2006-01-02") != "2024-01-15" {
				t.Fatalf("unexpected version: %+v", version)
			}
			if version.Metadata["tarball"] != "https://example.invalid/package-1.0.0.tgz" {
				t.Fatalf("unexpected metadata: %+v", version.Metadata)
			}
		})
	}
}

func BenchmarkNPMHistory(b *testing.B) {
	for _, count := range []int{1, 100, 500, 2000} {
		b.Run(fmt.Sprintf("versions=%d", count), func(b *testing.B) {
			for _, operation := range []string{"package", "version", "package_and_version"} {
				b.Run(operation, func(b *testing.B) {
					benchmarkNPMLookup(b, count, operation)
				})
			}
		})
	}
}

func benchmarkNPMLookup(b *testing.B, count int, operation string) {
	purl, requests, size := npmHistoryServer(b, "@scope/history", count)
	client := registries.DefaultClient()
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		var pkg *registries.Package
		var version *registries.Version
		var err error
		switch operation {
		case "package":
			pkg, err = registries.FetchPackageFromPURL(ctx, purl, client)
		case "version":
			version, err = registries.FetchVersionFromPURL(ctx, purl, client)
		case "package_and_version":
			pkg, version, err = fetchNPMPair(ctx, purl, client)
		}
		if err != nil {
			b.Fatal(err)
		}
		if operation != "version" && (pkg == nil || pkg.Name != "@scope/history") {
			b.Fatalf("unexpected package: %+v", pkg)
		}
		if operation != "package" && (version == nil || version.Number != "1.0.0") {
			b.Fatalf("unexpected version: %+v", version)
		}
	}
	b.StopTimer()
	requestsPerOp := float64(requests.Load()) / float64(b.N)
	b.ReportMetric(requestsPerOp, "requests/op")
	b.ReportMetric(requestsPerOp*float64(size), "response-bytes/op")
}
