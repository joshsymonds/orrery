package releasestep

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestMain keeps the owner's git configuration out of every git the tests and the command run: an empty global
// configuration and no system one, so a scratch repository signs, hooks and commits only as its own configuration says.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "releasestep-test-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "make the test directory: %v\n", err)
		os.Exit(1)
	}
	global := filepath.Join(dir, "gitconfig")
	if err = os.WriteFile(global, nil, 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "write the empty git configuration: %v\n", err)
		os.Exit(1)
	}
	os.Setenv("GIT_CONFIG_GLOBAL", global)
	os.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	code := m.Run()
	if err = os.RemoveAll(dir); err != nil {
		fmt.Fprintf(os.Stderr, "remove the test directory: %v\n", err)
	}
	os.Exit(code)
}
