// Package holdertest gives test packages one TestMain body for sessions that run under orrery holder. It is imported
// only by tests.
package holdertest

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"
)

// Main builds cmd/orrery into a fresh temporary directory, puts that directory first on PATH, runs the tests,
// removes the directory, and returns the exit code. A build failure prints the go output to stderr and returns 1
// without running the tests.
func Main(m *testing.M) int {
	dir, err := os.MkdirTemp("", "orrery-holder-bin-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "make binary directory: %v\n", err)
		return 1
	}
	defer func() {
		if rmErr := os.RemoveAll(dir); rmErr != nil {
			fmt.Fprintf(os.Stderr, "remove %s: %v\n", dir, rmErr)
		}
	}()
	build := exec.CommandContext(context.Background(), "go", "install", "github.com/joshsymonds/orrery/cmd/orrery")
	build.Env = append(os.Environ(), "GOBIN="+dir)
	out, err := build.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "build orrery: %v\n%s", err, out)
		return 1
	}
	if err = os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH")); err != nil {
		fmt.Fprintf(os.Stderr, "set PATH: %v\n", err)
		return 1
	}
	return m.Run()
}
