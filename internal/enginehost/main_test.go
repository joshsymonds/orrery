package enginehost

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/exectest"
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
	dir, err := os.MkdirTemp("", "orrery-fakeclaude-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "make fake claude directory: %v\n", err)
		os.Exit(1)
	}
	wrapper, err := writeFakeClaudeWrapper(dir, filepath.Join("testdata", "fakeclaude.sh"))
	if err == nil {
		err = os.Setenv(fakeClaudeEnv, wrapper)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	code := holdertest.Main(m)
	if err = os.RemoveAll(dir); err != nil {
		fmt.Fprintf(os.Stderr, "remove %s: %v\n", dir, err)
		code = 1
	}
	os.Exit(code)
}

// fakeClaudeEnv names the environment variable TestMain sets to the path of the wrapper that runs the fake claude with
// git on its PATH. An environment variable rather than a package variable carries it, as GIT_CONFIG_GLOBAL is carried.
const fakeClaudeEnv = "ORRERY_TEST_FAKE_CLAUDE"

// writeFakeClaudeWrapper writes, in dir, a script that puts the directory of the git on the test's PATH first on PATH
// and then runs the fake claude at script, and returns the script's path. A session's login shell rebuilds PATH from
// its HOME, which holds no git on a host where git is not in a system profile, and the fake's STEPDONE commits with git,
// so the wrapper makes a committing fake commit on every host, as it does on CI.
func writeFakeClaudeWrapper(dir, script string) (string, error) {
	git, err := exec.LookPath("git")
	if err != nil {
		return "", fmt.Errorf("find git for the fake claude: %w", err)
	}
	git, err = filepath.Abs(git)
	if err != nil {
		return "", fmt.Errorf("absolute git path: %w", err)
	}
	script, err = filepath.Abs(script)
	if err != nil {
		return "", fmt.Errorf("absolute fake claude path: %w", err)
	}
	body := "#!/bin/sh\nPATH=" + shellQuote(filepath.Dir(git)) + ":\"$PATH\"\nexport PATH\nexec " + shellQuote(script) +
		" \"$@\"\n"
	wrapper := filepath.Join(dir, "fakeclaude")
	// The wrapper is a program a parallel test may run, so it is written under the fork lock (see exectest.WriteFile).
	if err = exectest.Write(wrapper, []byte(body), 0o700); err != nil {
		return "", fmt.Errorf("write fake claude wrapper: %w", err)
	}
	return wrapper, nil
}

// shellQuote quotes s as one single-quoted shell word.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
