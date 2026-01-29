package registries_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/git-pkgs/registries"
	_ "github.com/git-pkgs/registries/all"
)

// Mock server responses for benchmarks
var cargoResponse = map[string]interface{}{
	"crate": map[string]interface{}{
		"id":          "serde",
		"name":        "serde",
		"description": "A generic serialization/deserialization framework",
		"repository":  "https://github.com/serde-rs/serde",
		"homepage":    "https://serde.rs",
		"keywords":    []string{"serde", "serialization"},
	},
	"versions": []map[string]interface{}{
		{"id": 1, "num": "1.0.195", "license": "MIT OR Apache-2.0", "checksum": "abc123", "yanked": false, "created_at": "2024-01-15T00:00:00Z"},
		{"id": 2, "num": "1.0.194", "license": "MIT OR Apache-2.0", "checksum": "def456", "yanked": false, "created_at": "2024-01-10T00:00:00Z"},
		{"id": 3, "num": "1.0.193", "license": "MIT OR Apache-2.0", "checksum": "ghi789", "yanked": false, "created_at": "2024-01-05T00:00:00Z"},
	},
}

var npmResponse = map[string]interface{}{
	"name":        "lodash",
	"description": "Lodash modular utilities",
	"homepage":    "https://lodash.com/",
	"repository":  map[string]string{"url": "git+https://github.com/lodash/lodash.git"},
	"license":     "MIT",
	"keywords":    []string{"modules", "stdlib", "util"},
	"maintainers": []map[string]string{{"name": "jdalton", "email": "john@example.com"}},
	"versions": map[string]interface{}{
		"4.17.21": map[string]interface{}{
			"name":    "lodash",
			"version": "4.17.21",
			"dependencies": map[string]string{},
		},
	},
	"time": map[string]string{
		"4.17.21": "2021-02-20T15:42:15.553Z",
	},
}

func BenchmarkNew(b *testing.B) {
	ecosystems := []string{"cargo", "npm", "pypi", "gem", "golang", "maven", "nuget", "hex", "pub", "elm", "dub"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		eco := ecosystems[i%len(ecosystems)]
		_, _ = registries.New(eco, "", nil)
	}
}

func BenchmarkFetchPackage_Cargo(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(cargoResponse)
	}))
	defer server.Close()

	reg, _ := registries.New("cargo", server.URL, registries.DefaultClient())
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = reg.FetchPackage(ctx, "serde")
	}
}

func BenchmarkFetchPackage_npm(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(npmResponse)
	}))
	defer server.Close()

	reg, _ := registries.New("npm", server.URL, registries.DefaultClient())
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = reg.FetchPackage(ctx, "lodash")
	}
}

func BenchmarkFetchVersions_Cargo(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(cargoResponse)
	}))
	defer server.Close()

	reg, _ := registries.New("cargo", server.URL, registries.DefaultClient())
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = reg.FetchVersions(ctx, "serde")
	}
}

func BenchmarkURLBuilder(b *testing.B) {
	reg, _ := registries.New("cargo", "", nil)
	urls := reg.URLs()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = urls.Registry("serde", "1.0.195")
		_ = urls.Download("serde", "1.0.195")
		_ = urls.PURL("serde", "1.0.195")
	}
}

func BenchmarkSupportedEcosystems(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = registries.SupportedEcosystems()
	}
}

func BenchmarkDefaultURL(b *testing.B) {
	ecosystems := []string{"cargo", "npm", "pypi", "gem", "golang"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		eco := ecosystems[i%len(ecosystems)]
		_ = registries.DefaultURL(eco)
	}
}

// Benchmark JSON parsing overhead
func BenchmarkJSONParsing_Small(b *testing.B) {
	data, _ := json.Marshal(cargoResponse)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var result map[string]interface{}
		_ = json.Unmarshal(data, &result)
	}
}

func BenchmarkJSONParsing_Large(b *testing.B) {
	// Simulate a large npm response with many versions
	largeResponse := map[string]interface{}{
		"name": "lodash",
		"versions": make(map[string]interface{}),
	}
	versions := largeResponse["versions"].(map[string]interface{})
	for i := 0; i < 500; i++ {
		ver := map[string]interface{}{
			"name":         "lodash",
			"version":      "4.17." + string(rune('0'+i%10)),
			"dependencies": map[string]string{"dep1": "^1.0.0", "dep2": "^2.0.0"},
		}
		versions["4.17."+string(rune('0'+i%10))+"-"+string(rune('0'+i/10))] = ver
	}

	data, _ := json.Marshal(largeResponse)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var result map[string]interface{}
		_ = json.Unmarshal(data, &result)
	}
}

func BenchmarkFetchPackage_Parallel(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(cargoResponse)
	}))
	defer server.Close()

	reg, _ := registries.New("cargo", server.URL, registries.DefaultClient())
	ctx := context.Background()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = reg.FetchPackage(ctx, "serde")
		}
	})
}

func BenchmarkMultipleRegistries_Creation(b *testing.B) {
	ecosystems := []string{"cargo", "npm", "pypi", "gem", "golang", "hex", "pub", "elm", "dub"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, eco := range ecosystems {
			_, _ = registries.New(eco, "", nil)
		}
	}
}
