package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/route"
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
