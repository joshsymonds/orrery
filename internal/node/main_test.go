package node

import (
	"os"
	"testing"

	"github.com/joshsymonds/orrery/internal/holdertest"
)

// TestMain builds orrery and puts it first on PATH, since a session runs under orrery's holder (see holdertest.Main).
func TestMain(m *testing.M) {
	os.Exit(holdertest.Main(m))
}
