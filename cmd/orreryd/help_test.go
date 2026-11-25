package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestHelpFlagPrintsUsageAndExitsZero asks orreryd for help, alone and after other flags. Each prints the usage text on
// stdout, nothing on stderr, and exits 0 before any work, so no socket path is resolved and no store opened: getenv
// names no runtime dir, which serving would refuse.
func TestHelpFlagPrintsUsageAndExitsZero(t *testing.T) {
	t.Parallel()
	cases := [][]string{
		{"--help"},
		{"-h"},
		{"-help"},
		{"--gateway", "http://127.0.0.1:4110", "--help"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			code := run(args, &stdout, &stderr, func(string) string { return "" })
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

// TestUnknownFlagStillExitsUsage checks that a flag orreryd does not define is refused with the usage on stderr and
// exit 2, as before help exited 0.
func TestUnknownFlagStillExitsUsage(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := run([]string{"--bogus"}, &stdout, &stderr, func(string) string { return "" })
	if code != exitUsage {
		t.Fatalf("exit = %d, want %d\nstderr:\n%s", code, exitUsage, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want nothing", stdout.String())
	}
	if want := "orreryd: flag provided but not defined: -bogus\n" + usageText; stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
}

// TestUsageTextListsGateway lists the flag in the synopsis and in its flags entry.
func TestUsageTextListsGateway(t *testing.T) {
	t.Parallel()
	if !strings.Contains(usageText, "[--gateway URL]") {
		t.Errorf("usage text synopsis does not list --gateway:\n%s", usageText)
	}
	entry := "\n  --gateway URL     probe each idle account's usage limits through the gateway at URL, an absolute http or\n" +
		"                    https URL (default: none, no limit probes)\n"
	if !strings.Contains(usageText, entry) {
		t.Errorf("usage text does not describe --gateway:\n%s", usageText)
	}
}
