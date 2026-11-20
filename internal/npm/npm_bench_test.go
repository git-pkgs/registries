package npm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/git-pkgs/registries/internal/core"
)

// benchmarkRegistry serves one large packument, so the benchmarks measure
// the versions FetchPackage and FetchVersion read past and discard.
func benchmarkRegistry(b *testing.B, versions int) *Registry {
	b.Helper()
	doc := []byte(largePackument(versions))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(doc)
	}))
	b.Cleanup(server.Close)
	b.SetBytes(int64(len(doc)))
	return New(server.URL, core.DefaultClient())
}

func BenchmarkFetchPackageLargePackument(b *testing.B) {
	reg := benchmarkRegistry(b, 6000)
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := reg.FetchPackage(ctx, "big"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFetchVersionLargePackument(b *testing.B) {
	reg := benchmarkRegistry(b, 6000)
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := reg.FetchVersion(ctx, "big", "1.0.3000"); err != nil {
			b.Fatal(err)
		}
	}
}
