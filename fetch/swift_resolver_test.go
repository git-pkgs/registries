package fetch

import (
	"context"
	"testing"

	"github.com/git-pkgs/registries"
	_ "github.com/git-pkgs/registries/internal/swift"
)

func TestResolveSwift(t *testing.T) {
	reg, err := registries.New("swift", "https://packages.example.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	r := NewResolver()
	r.RegisterRegistry(reg)

	for _, name := range []string{"apple/swift-nio", "apple.swift-nio"} {
		info, err := r.Resolve(context.Background(), "swift", name, "2.65.0")
		if err != nil {
			t.Fatalf("Resolve(%q) failed: %v", name, err)
		}
		if want := "https://packages.example.com/apple/swift-nio/2.65.0.zip"; info.URL != want {
			t.Errorf("URL = %q, want %q", info.URL, want)
		}
	}
}
