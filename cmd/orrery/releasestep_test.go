package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestReleaseStepDispatchesWithItsOwnStatus: `orrery release-step` reaches internal/releasestep, which refuses a
// command line with no kind before any work. The command exits with its status, 2, and its own usage line, not the
// CLI's usage text, and the CLI's usage text lists it as orreryd's.
func TestReleaseStepDispatchesWithItsOwnStatus(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := run([]string{"release-step"}, nil, &stdout, &stderr, envFrom(nil))
	if code != exitUsage {
		t.Fatalf("exit = %d, want %d\nstderr:\n%s", code, exitUsage, stderr.String())
	}
	if !strings.Contains(stderr.String(), "usage: orrery release-step") ||
		strings.Contains(stderr.String(), usageText) {
		t.Errorf("stderr = %q, want the release-step usage line alone", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want nothing", stdout.String())
	}
	if !strings.Contains(usageText, "release-step KIND -candidate SHA") {
		t.Errorf("usage text does not list release-step:\n%s", usageText)
	}
}
