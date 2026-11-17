package main

import (
	"bytes"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/stepmcp"
)

// endedStepLine is the line `orrery step` writes for step id ended: the step object step_status returns for
// stepRow(id, true), with its newline.
func endedStepLine(id string) string {
	return `{"id":"` + id + `","agent":"claude","workspace":"/work/` + id +
		`","revision":"","readOnly":false,"worktree":"","end":"done","status":"done","result":"","checks":[],` +
		`"reason":"","started":1700000000,"ended":1700000100,"limited":false,"limitKind":"","limitResets":0}` + "\n"
}

// waitCallRecord is the record the fake keeps of a WaitSteps call that names ids: the call asks for stepmcp.MaxWait.
func waitCallRecord(ids string) string {
	return fmt.Sprintf("Wait %s timeout %d", ids, stepmcp.MaxWait/time.Second)
}

// TestStepStatusCommandPrintsStep prints a known, ended step as the JSON object step_status returns, on one line of
// stdout, and exits 0 with nothing on stderr.
func TestStepStatusCommandPrintsStep(t *testing.T) {
	t.Parallel()
	fake := &stepsFake{rows: map[string]*v1.Step{"s1": stepRow("s1", true)}}
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	serveStepsFake(t, daemon, fake)
	var stdout bytes.Buffer
	res := awaitOrrery(t, startOrrery(t, &stdout, "--socket", daemon, "step", "status", "s1"))
	if res.code != exitOK {
		t.Fatalf("exit status %d, want %d; stderr %q", res.code, exitOK, res.stderr)
	}
	if got, want := stdout.String(), endedStepLine("s1"); got != want {
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
	fake := &stepsFake{rows: map[string]*v1.Step{"s1": stepRow("s1", false)}}
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	serveStepsFake(t, daemon, fake)
	code, stdout, stderr := runStepCapture(t, newHoldClock(), daemon, "status", "s1")
	if code != exitOK {
		t.Fatalf("exit status %d, want %d; stderr %q", code, exitOK, stderr)
	}
	want := `{"id":"s1","agent":"claude","workspace":"/work/s1","revision":"","readOnly":false,"worktree":"",` +
		`"end":"","status":"","result":"","checks":[],"reason":"","started":1700000000,"ended":0,` +
		`"limited":false,"limitKind":"","limitResets":0}` + "\n"
	if stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
}

// TestStepStatusCommandUnknownIDExitsOne exits 1 with the id named on stderr when no step has it, and writes nothing to
// stdout.
func TestStepStatusCommandUnknownIDExitsOne(t *testing.T) {
	t.Parallel()
	fake := &stepsFake{rows: map[string]*v1.Step{"s1": stepRow("s1", false)}}
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
		rows:   map[string]*v1.Step{"s1": stepRow("s1", true)},
	}
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	serveStepsFake(t, daemon, fake)
	clock := newHoldClock()
	code, stdout, stderr := runStepCapture(t, clock, daemon, "status", "s1")
	if code != exitOK {
		t.Fatalf("exit status %d, want %d; stderr %q", code, exitOK, stderr)
	}
	if stdout != endedStepLine("s1") {
		t.Errorf("stdout = %q, want %q", stdout, endedStepLine("s1"))
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
		rows:   map[string]*v1.Step{"s1": stepRow("s1", true)},
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
	if stdout != endedStepLine("s1") {
		t.Errorf("stdout = %q, want %q", stdout, endedStepLine("s1"))
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

// TestStepStatusCommandUsageTextListsBothForms lists the status form and the wait form in the usage text.
func TestStepStatusCommandUsageTextListsBothForms(t *testing.T) {
	t.Parallel()
	for _, line := range []string{"\n  step status ID", "\n  step wait [--all] ID..."} {
		if !strings.Contains(usageText, line) {
			t.Errorf("usage text does not list %q:\n%s", strings.TrimPrefix(line, "\n"), usageText)
		}
	}
}

// TestStepWaitCommandReturnsWhenNamedStepEnds answers two timeouts before the named step ends. The command asks again
// after each timeout, returns only once the step has ended, and prints it. Each call asks for stepmcp.MaxWait.
func TestStepWaitCommandReturnsWhenNamedStepEnds(t *testing.T) {
	t.Parallel()
	fake := &stepsFake{
		rows:  map[string]*v1.Step{"s1": stepRow("s1", false)},
		waits: []string{"", "", "s1"},
	}
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	serveStepsFake(t, daemon, fake)
	clock := newHoldClock()
	code, stdout, stderr := runStepCapture(t, clock, daemon, "wait", "s1")
	if code != exitOK {
		t.Fatalf("exit status %d, want %d; stderr %q", code, exitOK, stderr)
	}
	if stdout != endedStepLine("s1") {
		t.Errorf("stdout = %q, want %q", stdout, endedStepLine("s1"))
	}
	want := []string{waitCallRecord("s1"), waitCallRecord("s1"), waitCallRecord("s1")}
	if got := fake.received(); !slices.Equal(got, want) {
		t.Errorf("orreryd received %q, want %q", got, want)
	}
	if len(clock.waits) != 0 {
		t.Errorf("waits = %v, want none: a timeout is asked again at once", clock.waits)
	}
}

// TestStepWaitCommandEndedStepReturnsAtOnce waits on a step that has already ended. The command asks once, prints the
// step and exits 0.
func TestStepWaitCommandEndedStepReturnsAtOnce(t *testing.T) {
	t.Parallel()
	fake := &stepsFake{rows: map[string]*v1.Step{"s1": stepRow("s1", true)}}
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	serveStepsFake(t, daemon, fake)
	clock := newHoldClock()
	code, stdout, stderr := runStepCapture(t, clock, daemon, "wait", "s1")
	if code != exitOK {
		t.Fatalf("exit status %d, want %d; stderr %q", code, exitOK, stderr)
	}
	if stdout != endedStepLine("s1") {
		t.Errorf("stdout = %q, want %q", stdout, endedStepLine("s1"))
	}
	if got := fake.received(); !slices.Equal(got, []string{waitCallRecord("s1")}) {
		t.Errorf("orreryd received %q, want one call", got)
	}
	if len(clock.waits) != 0 {
		t.Errorf("waits = %v, want none", clock.waits)
	}
}

// TestStepWaitCommandReturnsAfterTheFirstEnd names two steps without --all. The first to end is printed and the
// command returns, so the second is never asked for again.
func TestStepWaitCommandReturnsAfterTheFirstEnd(t *testing.T) {
	t.Parallel()
	fake := &stepsFake{
		rows:  map[string]*v1.Step{"s1": stepRow("s1", false), "s2": stepRow("s2", false)},
		waits: []string{"s2"},
	}
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	serveStepsFake(t, daemon, fake)
	code, stdout, stderr := runStepCapture(t, newHoldClock(), daemon, "wait", "s1", "s2")
	if code != exitOK {
		t.Fatalf("exit status %d, want %d; stderr %q", code, exitOK, stderr)
	}
	if stdout != endedStepLine("s2") {
		t.Errorf("stdout = %q, want %q", stdout, endedStepLine("s2"))
	}
	if got := fake.received(); !slices.Equal(got, []string{waitCallRecord("s1,s2")}) {
		t.Errorf("orreryd received %q, want one call naming both steps", got)
	}
}

// TestStepWaitCommandAllWaitsForEveryStep names two steps under --all. Each step is printed as it ends, and the command
// asks only for the steps still running after the first one ends.
func TestStepWaitCommandAllWaitsForEveryStep(t *testing.T) {
	t.Parallel()
	fake := &stepsFake{
		rows:  map[string]*v1.Step{"s1": stepRow("s1", false), "s2": stepRow("s2", false)},
		waits: []string{"s2", "", "s1"},
	}
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	serveStepsFake(t, daemon, fake)
	code, stdout, stderr := runStepCapture(t, newHoldClock(), daemon, "wait", "--all", "s1", "s2")
	if code != exitOK {
		t.Fatalf("exit status %d, want %d; stderr %q", code, exitOK, stderr)
	}
	if want := endedStepLine("s2") + endedStepLine("s1"); stdout != want {
		t.Errorf("stdout = %q, want each step as it ends: %q", stdout, want)
	}
	want := []string{waitCallRecord("s1,s2"), waitCallRecord("s1"), waitCallRecord("s1")}
	if got := fake.received(); !slices.Equal(got, want) {
		t.Errorf("orreryd received %q, want %q", got, want)
	}
}

// TestStepWaitCommandAllSkipsStepsAlreadyEnded names two steps under --all when one has already ended. The first call
// returns that step at once, and the next call names only the other step.
func TestStepWaitCommandAllSkipsStepsAlreadyEnded(t *testing.T) {
	t.Parallel()
	fake := &stepsFake{
		rows:  map[string]*v1.Step{"s1": stepRow("s1", true), "s2": stepRow("s2", false)},
		waits: []string{"s2"},
	}
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	serveStepsFake(t, daemon, fake)
	code, stdout, stderr := runStepCapture(t, newHoldClock(), daemon, "wait", "--all", "s1", "s2")
	if code != exitOK {
		t.Fatalf("exit status %d, want %d; stderr %q", code, exitOK, stderr)
	}
	if want := endedStepLine("s1") + endedStepLine("s2"); stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	want := []string{waitCallRecord("s1,s2"), waitCallRecord("s2")}
	if got := fake.received(); !slices.Equal(got, want) {
		t.Errorf("orreryd received %q, want %q", got, want)
	}
}

// TestStepWaitCommandUnknownIDExitsOneNamingIt names an id no step has, first alone and then under --all. Each exits 1
// with the id on stderr and prints nothing.
func TestStepWaitCommandUnknownIDExitsOneNamingIt(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args []string
	}{
		{"first ends", []string{"wait", "s1", "bogus"}},
		{"all", []string{"wait", "--all", "s1", "bogus"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := &stepsFake{rows: map[string]*v1.Step{"s1": stepRow("s1", false)}}
			daemon := filepath.Join(t.TempDir(), "orreryd.sock")
			serveStepsFake(t, daemon, fake)
			code, stdout, stderr := runStepCapture(t, newHoldClock(), daemon, tc.args...)
			if code != exitFailure {
				t.Errorf("exit status %d, want %d; stderr %q", code, exitFailure, stderr)
			}
			if !strings.HasPrefix(stderr, "orrery step: ") || !strings.Contains(stderr, "bogus") {
				t.Errorf("stderr %q, want the unknown id bogus prefixed with orrery step:", stderr)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want nothing", stdout)
			}
		})
	}
}

// TestStepWaitCommandRetriesUnavailable answers the first WaitSteps Unavailable. The retry waits on the clock once,
// asks again and prints the step that ends.
func TestStepWaitCommandRetriesUnavailable(t *testing.T) {
	t.Parallel()
	fake := &stepsFake{
		script: []holdAnswer{{err: connectErr(connect.CodeUnavailable, "orreryd restarting")}},
		rows:   map[string]*v1.Step{"s1": stepRow("s1", false)},
		waits:  []string{"s1"},
	}
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	serveStepsFake(t, daemon, fake)
	clock := newHoldClock()
	code, stdout, stderr := runStepCapture(t, clock, daemon, "wait", "s1")
	if code != exitOK {
		t.Fatalf("exit status %d, want %d; stderr %q", code, exitOK, stderr)
	}
	if stdout != endedStepLine("s1") {
		t.Errorf("stdout = %q, want %q", stdout, endedStepLine("s1"))
	}
	if len(clock.waits) != 1 {
		t.Errorf("waits = %v, want one wait before the retry", clock.waits)
	}
	want := []string{waitCallRecord("s1"), waitCallRecord("s1")}
	if got := fake.received(); !slices.Equal(got, want) {
		t.Errorf("orreryd received %q, want %q", got, want)
	}
}

// TestStepWaitCommandRetriesCut cuts the first WaitSteps mid-call. The retry waits for the step and exits 0.
func TestStepWaitCommandRetriesCut(t *testing.T) {
	t.Parallel()
	fake := &stepsFake{
		script: []holdAnswer{{cut: true}},
		rows:   map[string]*v1.Step{"s1": stepRow("s1", false)},
		waits:  []string{"s1"},
	}
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	serveStepsFake(t, daemon, fake)
	code, stdout, stderr := runStepCapture(t, newHoldClock(), daemon, "wait", "s1")
	if code != exitOK {
		t.Fatalf("exit status %d, want %d; stderr %q", code, exitOK, stderr)
	}
	if got := fake.requests(); got != 2 {
		t.Errorf("orreryd saw %d requests, want the cut and its retry", got)
	}
	if stdout != endedStepLine("s1") {
		t.Errorf("stdout = %q, want %q", stdout, endedStepLine("s1"))
	}
}

// TestStepWaitCommandGivesUpAtMaxWait answers every WaitSteps Unavailable. The retries stop when the wait's stepmcp.MaxWait
// has passed on the clock, and the command exits 1 with the reason.
func TestStepWaitCommandGivesUpAtMaxWait(t *testing.T) {
	t.Parallel()
	fake := &stepsFake{
		tail: holdAnswer{err: connectErr(connect.CodeUnavailable, "orreryd restarting")},
		rows: map[string]*v1.Step{"s1": stepRow("s1", false)},
	}
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	serveStepsFake(t, daemon, fake)
	clock := newHoldClock()
	start := clock.Now()
	code, stdout, stderr := runStepCapture(t, clock, daemon, "wait", "s1")
	if code != exitFailure {
		t.Errorf("exit status %d, want %d; stderr %q", code, exitFailure, stderr)
	}
	if !strings.HasPrefix(stderr, "orrery step: ") || !strings.Contains(stderr, "unavailable") {
		t.Errorf("stderr %q, want the reason unavailable prefixed with orrery step:", stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing", stdout)
	}
	if got := clock.Now().Sub(start); got != stepmcp.MaxWait {
		t.Errorf("wait gave up after %v on the clock, want exactly stepmcp.MaxWait %v", got, stepmcp.MaxWait)
	}
	if fake.requests() < 2 {
		t.Errorf("orreryd saw %d requests, want retries before the wait ends", fake.requests())
	}
}

// TestStepWaitCommandUsageErrorsExitTwo refuses each wait line that names no id or has a flag other than --all. Each
// exits 2 with the usage text and sends nothing.
func TestStepWaitCommandUsageErrorsExitTwo(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args []string
	}{
		{"wait without ID", []string{"step", "wait"}},
		{"wait with --all and no ID", []string{"step", "wait", "--all"}},
		{"wait with an unknown flag", []string{"step", "wait", "--once", "s1"}},
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
