package core

import (
	"strings"
	"testing"
)

func TestNewUnknownEcosystemErrorNamesFix(t *testing.T) {
	_, err := New("no-such-ecosystem", "", nil)
	if err == nil {
		t.Fatal("expected error for unknown ecosystem")
	}

	msg := err.Error()
	for _, want := range []string{
		`unknown ecosystem "no-such-ecosystem"`,
		"0 registered",
		`import _ "github.com/git-pkgs/registries/all"`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not contain %q", msg, want)
		}
	}
}
