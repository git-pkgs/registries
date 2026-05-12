package fetch

import (
	"os"
	"testing"

	"github.com/git-pkgs/registries/safehttp"
)

// TestMain opts the safehttp dial gate off loopback so the package's
// httptest-backed test suite continues to run. The opt-out is binary-
// scoped — production code never sees it.
func TestMain(m *testing.M) {
	safehttp.EnableLoopbackForTesting()
	os.Exit(m.Run())
}
