package main

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/route"
	"github.com/joshsymonds/orrery/internal/store"
)

// TestJudgeDelayFlag checks the --judge-delay test seam: without it the wait is route.JudgeDelay, which reaches
// route.Config unchanged; a duration replaces it, and zero reaches route.Config as route.NoJudgeDelay; a negative
// value is a usage error; and the usage text does not list the seam.
func TestJudgeDelayFlag(t *testing.T) {
	opts, err := parseFlags(nil, holderScopeGetenv)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if opts.judgeDelay != route.JudgeDelay || routeJudgeDelay(opts.judgeDelay) != route.JudgeDelay {
		t.Errorf("judgeDelay without the flag = %v (route %v), want %v for both",
			opts.judgeDelay, routeJudgeDelay(opts.judgeDelay), route.JudgeDelay)
	}

	opts, err = parseFlags([]string{"--judge-delay", "1ms"}, holderScopeGetenv)
	if err != nil {
		t.Fatalf("parseFlags(--judge-delay 1ms): %v", err)
	}
	if routeJudgeDelay(opts.judgeDelay) != time.Millisecond {
		t.Errorf("route judge delay for 1ms = %v, want 1ms", routeJudgeDelay(opts.judgeDelay))
	}

	opts, err = parseFlags([]string{"--judge-delay", "0s"}, holderScopeGetenv)
	if err != nil {
		t.Fatalf("parseFlags(--judge-delay 0s): %v", err)
	}
	if routeJudgeDelay(opts.judgeDelay) != route.NoJudgeDelay {
		t.Errorf("route judge delay for 0s = %v, want route.NoJudgeDelay", routeJudgeDelay(opts.judgeDelay))
	}

	_, err = parseFlags([]string{"--judge-delay", "-1s"}, holderScopeGetenv)
	var usage usageError
	if !errors.As(err, &usage) || !strings.Contains(err.Error(), "--judge-delay must not be negative") {
		t.Errorf("parseFlags(--judge-delay -1s) error = %v, want the negative-value usage error", err)
	}
	if strings.Contains(usageText, "judge-delay") {
		t.Errorf("usage text lists the --judge-delay test seam:\n%s", usageText)
	}
}

// TestDueAtFlag checks the --due-at test seam: without it, or with zero, the due pass reads the system clock; with a
// unix second it reads that second; a negative value is a usage error; and the usage text does not list the seam.
func TestDueAtFlag(t *testing.T) {
	for _, args := range [][]string{nil, {"--due-at", "0"}} {
		opts, err := parseFlags(args, holderScopeGetenv)
		if err != nil {
			t.Fatalf("parseFlags(%q): %v", args, err)
		}
		if opts.dueNow != nil {
			t.Errorf("parseFlags(%q) dueNow is set, want nil, the system clock", args)
		}
	}

	opts, err := parseFlags([]string{"--due-at", "1900000000"}, holderScopeGetenv)
	if err != nil {
		t.Fatalf("parseFlags(--due-at 1900000000): %v", err)
	}
	if opts.dueNow == nil || !opts.dueNow().Equal(time.Unix(1900000000, 0)) {
		t.Fatalf("dueNow for --due-at 1900000000 does not read unix second 1900000000")
	}

	_, err = parseFlags([]string{"--due-at", "-1"}, holderScopeGetenv)
	var usage usageError
	if !errors.As(err, &usage) || !strings.Contains(err.Error(), "--due-at must not be negative") {
		t.Errorf("parseFlags(--due-at -1) error = %v, want the negative-value usage error", err)
	}
	if strings.Contains(usageText, "due-at") {
		t.Errorf("usage text lists the --due-at test seam:\n%s", usageText)
	}
}

// TestWriteTickFlag checks the --write-tick test seam: without it the tick is zero, which the engine writer reads as
// its own; a duration replaces it; a negative value is a usage error; and the usage text does not list the seam.
func TestWriteTickFlag(t *testing.T) {
	opts, err := parseFlags(nil, holderScopeGetenv)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if opts.writeTick != 0 {
		t.Errorf("writeTick without the flag = %v, want 0", opts.writeTick)
	}

	opts, err = parseFlags([]string{"--write-tick", "5ms"}, holderScopeGetenv)
	if err != nil {
		t.Fatalf("parseFlags(--write-tick 5ms): %v", err)
	}
	if opts.writeTick != 5*time.Millisecond {
		t.Errorf("writeTick for 5ms = %v, want 5ms", opts.writeTick)
	}

	_, err = parseFlags([]string{"--write-tick", "-1ms"}, holderScopeGetenv)
	var usage usageError
	if !errors.As(err, &usage) || !strings.Contains(err.Error(), "--write-tick must not be negative") {
		t.Errorf("parseFlags(--write-tick -1ms) error = %v, want the negative-value usage error", err)
	}
	if strings.Contains(usageText, "write-tick") {
		t.Errorf("usage text lists the --write-tick test seam:\n%s", usageText)
	}
}

// TestStoreBusyTimeoutFlag checks the --store-busy-timeout test seam: without it a store write waits store.BusyTimeout
// for the write lock; a duration replaces it, zero among them; a negative value is a usage error; and the usage text
// does not list the seam.
func TestStoreBusyTimeoutFlag(t *testing.T) {
	opts, err := parseFlags(nil, holderScopeGetenv)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if opts.storeBusy != store.BusyTimeout {
		t.Errorf("storeBusy without the flag = %v, want store.BusyTimeout %v", opts.storeBusy, store.BusyTimeout)
	}

	opts, err = parseFlags([]string{"--store-busy-timeout", "0s"}, holderScopeGetenv)
	if err != nil {
		t.Fatalf("parseFlags(--store-busy-timeout 0s): %v", err)
	}
	if opts.storeBusy != 0 {
		t.Errorf("storeBusy for 0s = %v, want 0", opts.storeBusy)
	}

	_, err = parseFlags([]string{"--store-busy-timeout", "-1ms"}, holderScopeGetenv)
	var usage usageError
	if !errors.As(err, &usage) || !strings.Contains(err.Error(), "--store-busy-timeout must not be negative") {
		t.Errorf("parseFlags(--store-busy-timeout -1ms) error = %v, want the negative-value usage error", err)
	}
	if strings.Contains(usageText, "store-busy-timeout") {
		t.Errorf("usage text lists the --store-busy-timeout test seam:\n%s", usageText)
	}
}

// TestHostFlag checks the --host test seam: without it the host name is the machine's, with it the name it gives, and
// the usage text does not list the seam.
func TestHostFlag(t *testing.T) {
	opts, err := parseFlags(nil, holderScopeGetenv)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	machine, err := os.Hostname()
	if err != nil {
		t.Fatalf("os.Hostname: %v", err)
	}
	if got, err := hostName(opts.host); err != nil || got != machine {
		t.Errorf("host name without the flag = %q, %v; want the machine's %q", got, err, machine)
	}
	opts, err = parseFlags([]string{"--host", "synthetic-host"}, holderScopeGetenv)
	if err != nil {
		t.Fatalf("parseFlags(--host synthetic-host): %v", err)
	}
	if got, err := hostName(opts.host); err != nil || got != "synthetic-host" {
		t.Errorf("host name with --host synthetic-host = %q, %v; want synthetic-host", got, err)
	}
	if strings.Contains(usageText, "--host ") {
		t.Errorf("usage text lists the --host test seam:\n%s", usageText)
	}
}
