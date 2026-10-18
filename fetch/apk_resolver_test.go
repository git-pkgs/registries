package fetch

import (
	"context"
	"testing"

	"github.com/git-pkgs/registries"
	_ "github.com/git-pkgs/registries/internal/apk"
)

func TestResolveAPK(t *testing.T) {
	reg, err := registries.New("apk", "https://dl-cdn.alpinelinux.org/alpine/v3.23/main/x86_64", nil)
	if err != nil {
		t.Fatal(err)
	}
	r := NewResolver()
	r.RegisterRegistry(reg)

	info, err := r.Resolve(context.Background(), "apk", "alpine/curl", "8.22.0-r0")
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if want := "https://dl-cdn.alpinelinux.org/alpine/v3.23/main/x86_64/curl-8.22.0-r0.apk"; info.URL != want {
		t.Errorf("URL = %q, want %q", info.URL, want)
	}
	if info.Filename != "curl-8.22.0-r0.apk" {
		t.Errorf("Filename = %q", info.Filename)
	}
}
