package main

import (
	"bytes"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// endedStepLine is the line `orrery step` writes for step s1 ended: the step object step_status returns for
// stepRow(true), with its newline.
func endedStepLine() string {
	return `{"id":"s1","agent":"claude","end":"done","status":"done","stage":"","doing":"","epic":"","task":"",` +
		`"started":1700000000,"ended":1700000100,"limited":false,"limitKind":"","limitResets":0,` +
		`"reason":"","checks":[]}` + "\n"
}

// TestStepStatusCommandPrintsStep prints a known, ended step as the JSON object step_status returns, on one line of
// stdout, and exits 0 with nothing on stderr.
func TestStepStatusCommandPrintsStep(t *testing.T) {
	t.Parallel()
	fake := &stepsFake{rows: map[string]*v1.Step{"s1": stepRow(true)}}
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	serveStepsFake(t, daemon, fake)
	var stdout bytes.Buffer
	res := awaitOrrery(t, startOrrery(t, &stdout, "--socket", daemon, "step", "status", "s1"))
	if res.code != exitOK {
		t.Fatalf("exit status %d, want %d; stderr %q", res.code, exitOK, res.stderr)
	}
	if got, want := stdout.String(), endedStepLine(); got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if res.stderr != "" {
		t.Errorf("stderr = %q, want nothing", res.stderr)
	}
	if got := fake.received(); !slices.Equal(got, []string{"Status s1"}) {
		t.Errorf("orreryd received %q, want [Status s1]", got)
	}
}

// TestStepStatusCommandPrintsRunningStep prints a step that has not ended with an empty end and a zero ended time.
func TestStepStatusCommandPrintsRunningStep(t *testing.T) {
	t.Parallel()
	fake := &stepsFake{rows: map[string]*v1.Step{"s1": stepRow(false)}}
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	serveStepsFake(t, daemon, fake)
	code, stdout, stderr := runStepCapture(t, newHoldClock(), daemon, "status", "s1")
	if code != exitOK {
		t.Fatalf("exit status %d, want %d; stderr %q", code, exitOK, stderr)
	}
	want := `{"id":"s1","agent":"claude","end":"","status":"","stage":"","doing":"","epic":"","task":"",` +
		`"started":1700000000,"ended":0,"limited":false,"limitKind":"","limitResets":0,` +
		`"reason":"","checks":[]}` + "\n"
	if stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
}

// TestStepStatusCommandUnknownIDExitsOne exits 1 with the id named on stderr when no step has it, and writes nothing to
// stdout.
func TestStepStatusCommandUnknownIDExitsOne(t *testing.T) {
	t.Parallel()
	fake := &stepsFake{rows: map[string]*v1.Step{"s1": stepRow(false)}}
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	serveStepsFake(t, daemon, fake)
	code, stdout, stderr := runStepCapture(t, newHoldClock(), daemon, "status", "bogus")
	if code != exitFailure {
		t.Errorf("exit status %d, want %d; stderr %q", code, exitFailure, stderr)
	}
	if !strings.HasPrefix(stderr, "orrery step: ") || !strings.Contains(stderr, "bogus") {
		t.Errorf("stderr %q, want the unknown id bogus prefixed with orrery step:", stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing", stdout)
	}
}

// TestStepStatusCommandRetriesUnavailable answers the first StepStatus Unavailable. The retry prints the step after one
// wait on the clock.
func TestStepStatusCommandRetriesUnavailable(t *testing.T) {
	t.Parallel()
	fake := &stepsFake{
		script: []holdAnswer{{err: connectErr(connect.CodeUnavailable, "orreryd restarting")}},
		rows:   map[string]*v1.Step{"s1": stepRow(true)},
	}
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	serveStepsFake(t, daemon, fake)
	clock := newHoldClock()
	code, stdout, stderr := runStepCapture(t, clock, daemon, "status", "s1")
	if code != exitOK {
		t.Fatalf("exit status %d, want %d; stderr %q", code, exitOK, stderr)
	}
	if stdout != endedStepLine() {
		t.Errorf("stdout = %q, want %q", stdout, endedStepLine())
	}
	if len(clock.waits) != 1 {
		t.Errorf("waits = %v, want one wait before the retry", clock.waits)
	}
	if got := fake.received(); !slices.Equal(got, []string{"Status s1", "Status s1"}) {
		t.Errorf("orreryd received %q, want the Unavailable call and its retry", got)
	}
}

// TestStepStatusCommandRetriesCut cuts the first StepStatus mid-call. The retry prints the step and exits 0.
func TestStepStatusCommandRetriesCut(t *testing.T) {
	t.Parallel()
	fake := &stepsFake{
		script: []holdAnswer{{cut: true}},
		rows:   map[string]*v1.Step{"s1": stepRow(true)},
	}
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	serveStepsFake(t, daemon, fake)
	code, stdout, stderr := runStepCapture(t, newHoldClock(), daemon, "status", "s1")
	if code != exitOK {
		t.Fatalf("exit status %d, want %d; stderr %q", code, exitOK, stderr)
	}
	if got := fake.requests(); got != 2 {
		t.Errorf("orreryd saw %d requests, want the cut and its retry", got)
	}
	if stdout != endedStepLine() {
		t.Errorf("stdout = %q, want %q", stdout, endedStepLine())
	}
}

// TestStepStatusCommandUsageErrorsExitTwo refuses each status line that does not name exactly one id. Each exits 2 with
// the usage text and sends nothing.
func TestStepStatusCommandUsageErrorsExitTwo(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args []string
	}{
		{"status without ID", []string{"step", "status"}},
		{"status with two IDs", []string{"step", "status", "a", "b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := &stepsFake{}
			daemon := filepath.Join(t.TempDir(), "orreryd.sock")
			serveStepsFake(t, daemon, fake)
			var stdout bytes.Buffer
			res := awaitOrrery(t, startOrrery(t, &stdout, append([]string{"--socket", daemon}, tc.args...)...))
			if res.code != exitUsage {
				t.Errorf("exit status %d, want %d; stderr %q", res.code, exitUsage, res.stderr)
			}
			if !strings.HasPrefix(res.stderr, "orrery: step: ") || !strings.HasSuffix(res.stderr, usageText) {
				t.Errorf("stderr %q, want a usage error naming step and ending with the usage text", res.stderr)
			}
			if got := fake.received(); len(got) != 0 {
				t.Errorf("orreryd received %q, want nothing", got)
			}
		})
	}
}

// TestStepStatusCommandUsageTextListsBothForms lists the status form in the usage text.
func TestStepStatusCommandUsageTextListsBothForms(t *testing.T) {
	t.Parallel()
	if !strings.Contains(usageText, "\n  step status ID") {
		t.Errorf("usage text does not list step status ID:\n%s", usageText)
	}
}
