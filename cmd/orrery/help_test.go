package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// TestHelpFlagPrintsUsageAndExitsZero asks for help at each place a help flag is read: before the subcommand, as a
// subcommand's first argument, as a verb's first argument, and among a command's own flags. Each prints the usage text
// on stdout, nothing on stderr, and exits 0, and none dials the daemon socket, which does not exist.
func TestHelpFlagPrintsUsageAndExitsZero(t *testing.T) {
	t.Parallel()
	cases := [][]string{
		{"--help"},
		{"-h"},
		{"gateway", "--help"},
		{"gateway", "-help"},
		{"routes", "--help"},
		{"routes", "import", "--help"},
		{"epic", "--help"},
		{"epic", "get", "--help"},
		{"epic", "get", "some-epic", "--help"},
		{"epic", "answer", "some-epic", "-h"},
		{"session", "say", "--help"},
		{"run", "--help"},
		{"events", "--help"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()
			daemon := filepath.Join(t.TempDir(), "orreryd.sock")
			var stdout, stderr bytes.Buffer
			code := run(append([]string{"--socket", daemon}, args...), nil, &stdout, &stderr, envFrom(nil))
			if code != exitOK {
				t.Fatalf("exit = %d, want %d\nstderr:\n%s", code, exitOK, stderr.String())
			}
			if stdout.String() != usageText {
				t.Errorf("stdout = %q, want the usage text", stdout.String())
			}
			if stderr.Len() != 0 {
				t.Errorf("stderr = %q, want nothing", stderr.String())
			}
		})
	}
}

// TestHelpFlagAsTextIsLeftToTheCommand gives a help flag where a command reads free text, a session say's TEXT. It is
// sent as text, not read as help, so the command dials the daemon socket, which does not exist, and fails with 1.
func TestHelpFlagAsTextIsLeftToTheCommand(t *testing.T) {
	t.Parallel()
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	var stdout, stderr bytes.Buffer
	code := run([]string{"--socket", daemon, "session", "say", "some-session", "--help"}, nil, &stdout, &stderr,
		envFrom(nil))
	if code != exitFailure {
		t.Fatalf("exit = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, exitFailure, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), usageText) {
		t.Errorf("stdout = %q, want no usage text", stdout.String())
	}
}

// TestRoutesImportRefusesAFlagAsItsFile checks that `orrery routes import` refuses a flag in place of FILE as a usage
// error naming the flag, rather than reading a file of that name, and dials nothing.
func TestRoutesImportRefusesAFlagAsItsFile(t *testing.T) {
	t.Parallel()
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	code, stdout, stderr := runRoutesCLI(t, daemon, "import", "--bogus")
	if code != exitUsage {
		t.Fatalf("exit code = %d, want %d; stderr %q", code, exitUsage, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing", stdout)
	}
	if want := "routes import: flag provided but not defined: -bogus"; !strings.Contains(stderr, want) {
		t.Errorf("stderr = %q, want it to name the flag: %q", stderr, want)
	}
}
