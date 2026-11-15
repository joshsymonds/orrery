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
