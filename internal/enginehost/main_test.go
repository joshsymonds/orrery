package enginehost

import (
	"fmt"
	"os"
	"testing"

	"github.com/joshsymonds/orrery/internal/holdertest"
)

// TestMain points git at no global or system config for the whole package, so the owner's git config never applies
// to the repositories the tests make or to the git the code under test runs. It is set here once rather than with
// t.Setenv in each test, because t.Setenv keeps a test from running in parallel.
func TestMain(m *testing.M) {
	for _, kv := range [][2]string{{"GIT_CONFIG_GLOBAL", "/dev/null"}, {"GIT_CONFIG_NOSYSTEM", "1"}} {
		if err := os.Setenv(kv[0], kv[1]); err != nil {
			fmt.Fprintf(os.Stderr, "set %s: %v\n", kv[0], err)
			os.Exit(1)
		}
	}
	os.Exit(holdertest.Main(m))
}
