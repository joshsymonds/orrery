package enginehost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestFakeClaudeWrapperPutsGitOnPathAndRunsTheScript runs the wrapper under an environment whose PATH holds no git, as a
// session's login shell can leave it. The script it wraps still finds the test's git first on PATH and gets the
// wrapper's arguments.
func TestFakeClaudeWrapperPutsGitOnPathAndRunsTheScript(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	script := filepath.Join(dir, "probe.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncommand -v git\nprintf '%s\\n' \"$@\"\n"), 0o700); err != nil {
		t.Fatalf("write probe: %v", err)
	}
	wrapper, err := writeFakeClaudeWrapper(t.TempDir(), script)
	if err != nil {
		t.Fatalf("writeFakeClaudeWrapper: %v", err)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("find git: %v", err)
	}
	git, err = filepath.Abs(git)
	if err != nil {
		t.Fatalf("absolute git: %v", err)
	}
	cmd := exec.Command(wrapper, "one", "two words")
	cmd.Env = []string{"PATH=/nonexistent"}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run wrapper: %v", err)
	}
	if got, want := string(out), git+"\none\ntwo words\n"; got != want {
		t.Fatalf("wrapper output = %q, want %q", got, want)
	}
}
