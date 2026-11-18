package server_test

import (
	"os"
	"testing"

	"github.com/joshsymonds/orrery/internal/holdertest"
)

// TestMain builds orrery once for the package, because the speak-label test's session runs under orrery holder, and
// that test runs no build of its own.
func TestMain(m *testing.M) {
	os.Exit(holdertest.Main(m))
}
