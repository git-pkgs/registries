package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func BenchmarkClient_GetJSON(b *testing.B) {
	response := map[string]interface{}{
		"name":        "test",
		"description": "A test package",
		"version":     "1.0.0",
		"dependencies": map[string]string{
			"dep1": "^1.0.0",
			"dep2": "^2.0.0",
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := DefaultClient()
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var result map[string]interface{}
		_ = client.GetJSON(ctx, server.URL, &result)
	}
}

func BenchmarkClient_GetBody(b *testing.B) {
	body := `Package: test
Version: 1.0.0
Description: A test package
License: MIT
Depends: R (>= 4.0)
`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	client := DefaultClient()
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = client.GetBody(ctx, server.URL)
	}
}

func BenchmarkDefaultClient(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = DefaultClient()
	}
}
