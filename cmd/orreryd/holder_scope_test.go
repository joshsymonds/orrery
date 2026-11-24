package main

import (
	"errors"
	"strings"
	"testing"
)

// holderScopeGetenv is the environment the holder scope flag tests parse with. It sets only the runtime directory, which
// gives the daemon socket a path.
func holderScopeGetenv(key string) string {
	if key == "XDG_RUNTIME_DIR" {
		return "/run/holder-scope-test"
	}
	return ""
}

// TestHolderScopeFlagParsesAbsolutePath checks that --holder-scope PATH names systemd-run in the parsed options.
func TestHolderScopeFlagParsesAbsolutePath(t *testing.T) {
	opts, err := parseFlags([]string{"--holder-scope", "/usr/bin/systemd-run"}, holderScopeGetenv)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if opts.holderScope != "/usr/bin/systemd-run" {
		t.Errorf("holderScope = %q, want /usr/bin/systemd-run", opts.holderScope)
	}
}

// TestHolderScopeFlagDefaultsToEmpty checks that without the flag, or with it empty, the holders start plainly: the
// scope is empty.
func TestHolderScopeFlagDefaultsToEmpty(t *testing.T) {
	for _, args := range [][]string{nil, {"--holder-scope", ""}} {
		opts, err := parseFlags(args, holderScopeGetenv)
		if err != nil {
			t.Fatalf("parseFlags(%q): %v", args, err)
		}
		if opts.holderScope != "" {
			t.Errorf("parseFlags(%q) holderScope = %q, want empty", args, opts.holderScope)
		}
	}
}

// TestHolderScopeFlagRefusesRelativePath checks that a --holder-scope that is not an absolute path is a usage error.
func TestHolderScopeFlagRefusesRelativePath(t *testing.T) {
	_, err := parseFlags([]string{"--holder-scope", "systemd-run"}, holderScopeGetenv)
	var usage usageError
	if !errors.As(err, &usage) {
		t.Fatalf("parseFlags error = %v, want a usage error", err)
	}
	want := `--holder-scope needs an absolute path, got "systemd-run"`
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

// TestUsageTextListsHolderScope lists the flag in the synopsis and in its flags entry.
func TestUsageTextListsHolderScope(t *testing.T) {
	if !strings.Contains(usageText, "[--holder-scope PATH]") {
		t.Errorf("usage text synopsis does not list --holder-scope:\n%s", usageText)
	}
	entry := "\n  --holder-scope PATH\n" +
		"                    systemd-run binary that runs each session's holder in its own user scope\n" +
		"                    (default: none, the holder starts plainly)\n"
	if !strings.Contains(usageText, entry) {
		t.Errorf("usage text does not describe --holder-scope:\n%s", usageText)
	}
}
