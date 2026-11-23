// Package holdertest gives test packages one TestMain body for sessions that run under orrery holder. It is imported
// only by tests.
package holdertest

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// Main builds cmd/orrery into a fresh temporary directory, puts that directory first on PATH, runs the tests,
// removes the directory, and returns the exit code. A build failure prints the go output to stderr and returns 1
// without running the tests.
//
// needing names the tests that run under orrery holder. When any are named, Main builds only if the run's -run pattern
// selects one of them, and otherwise runs the tests without a build. With no names, Main always builds.
func Main(m *testing.M, needing ...string) int {
	if len(needing) > 0 && !selectsAny(needing) {
		return m.Run()
	}
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

// selectsAny reports whether the run's -run pattern selects a top-level test named in names. No pattern selects every
// test, and a pattern that does not compile is reported as selecting one, so the build runs and m.Run reports it.
func selectsAny(names []string) bool {
	flag.Parse()
	run := flag.Lookup("test.run")
	if run == nil || run.Value.String() == "" {
		return true
	}
	top, _, _ := strings.Cut(run.Value.String(), "/")
	re, err := regexp.Compile(top)
	if err != nil {
		return true
	}
	for _, name := range names {
		if re.MatchString(name) {
			return true
		}
	}
	return false
}
