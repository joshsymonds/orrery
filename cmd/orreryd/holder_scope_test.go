package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/holder"
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

// TestHolderScopeMemoryFlagDefaultsTo8GiB checks that without --holder-scope-memory each scope is capped at
// holder.DefaultScopeMemory, 8 GiB.
func TestHolderScopeMemoryFlagDefaultsTo8GiB(t *testing.T) {
	opts, err := parseFlags(nil, holderScopeGetenv)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if opts.scopeMemory != holder.DefaultScopeMemory || holder.DefaultScopeMemory != 8<<30 {
		t.Errorf("scopeMemory = %d, want 8 GiB", opts.scopeMemory)
	}
}

// TestHolderScopeMemoryFlagParsesSizes checks the sizes --holder-scope-memory takes: bytes, or a whole number with K,
// M, G or T, each a power of 1024.
func TestHolderScopeMemoryFlagParsesSizes(t *testing.T) {
	for value, want := range map[string]int64{
		"1073741824": 1 << 30,
		"512K":       512 << 10,
		"768M":       768 << 20,
		"16G":        16 << 30,
		"1T":         1 << 40,
	} {
		opts, err := parseFlags([]string{"--holder-scope-memory", value}, holderScopeGetenv)
		if err != nil {
			t.Fatalf("parseFlags(%q): %v", value, err)
		}
		if opts.scopeMemory != want {
			t.Errorf("parseFlags(%q) scopeMemory = %d, want %d", value, opts.scopeMemory, want)
		}
	}
}

// TestHolderScopeMemoryFlagRefusesNonSizes checks that a --holder-scope-memory that is not a positive size is a usage
// error.
func TestHolderScopeMemoryFlagRefusesNonSizes(t *testing.T) {
	for _, value := range []string{"", "0", "0G", "-1", "G", "8g", "1.5G", "8GB", "infinity", "9223372036854775807K"} {
		_, err := parseFlags([]string{"--holder-scope-memory", value}, holderScopeGetenv)
		var usage usageError
		if !errors.As(err, &usage) {
			t.Fatalf("parseFlags(%q) error = %v, want a usage error", value, err)
		}
		if !strings.Contains(err.Error(), "holder-scope-memory") {
			t.Errorf("parseFlags(%q) error = %q, want it to name the flag", value, err.Error())
		}
	}
}

// TestUsageTextListsHolderScopeMemory lists the flag in the synopsis and in its flags entry.
func TestUsageTextListsHolderScopeMemory(t *testing.T) {
	if !strings.Contains(usageText, "[--holder-scope-memory SIZE]") {
		t.Errorf("usage text synopsis does not list --holder-scope-memory:\n%s", usageText)
	}
	entry := "\n  --holder-scope-memory SIZE\n" +
		"                    memory cap of each holder's scope, with no swap past it: bytes, or a whole\n" +
		"                    number with K, M, G or T (powers of 1024) (default 8G)\n"
	if !strings.Contains(usageText, entry) {
		t.Errorf("usage text does not describe --holder-scope-memory:\n%s", usageText)
	}
}
