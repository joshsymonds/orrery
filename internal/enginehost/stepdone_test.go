package enginehost

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/store"
)

// stepDoneOwned is the one repo-relative path a writing step in these tests owns.
const stepDoneOwned = "pkg/owned.txt"

// stepDoneScript writes a gate script whose body follows a shebang and returns its path.
func stepDoneScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gate.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatalf("write gate script: %v", err)
	}
	return path
}

// stepDoneWriting returns a configure func that makes the fixture's step a writing step in dir, starting at start,
// that owns stepDoneOwned and runs gate under lock with limit.
func stepDoneWriting(dir, start string, gate []string, limit time.Duration, lock string) func(*stepRunConfig) {
	return func(cfg *stepRunConfig) {
		cfg.writing = true
		cfg.dir = dir
		cfg.start = start
		cfg.owned = []string{stepDoneOwned}
		cfg.gate = gate
		cfg.gateLimit = limit
		cfg.locks = []string{lock}
		cfg.environ = gateEnv("TMUX=/tmp/unused,1,0", "TMUX_PANE=%1")
	}
}

// newStepDoneFixture returns a step loop fixture whose step is configured by configure before its run starts.
func newStepDoneFixture(t *testing.T, configure func(*stepRunConfig)) *stepLoopFixture {
	t.Helper()
	f := newStepLoopFixture(t, nil)
	cfg := f.run.stepRunConfig
	configure(&cfg)
	f.run = newStepRun(cfg)
	return f
}

// stepDoneRepo returns a repo under t.TempDir() with its base commit as the start revision.
func stepDoneRepo(t *testing.T) (dir, start string) {
	t.Helper()
	dir = stepGitTestRepo(t)
	start = stepGitTestRun(t, dir, "rev-parse", "HEAD")
	return dir, start
}

// stepDoneReturn is a done return that names commit as its top-level commit.
func stepDoneReturn(commit string) session.TurnEnded {
	raw := fmt.Sprintf(`{"status":%q,"commit":%q}`, stepLoopDone, commit)
	return session.TurnEnded{StructuredOutput: json.RawMessage(raw)}
}

// stepDoneRoundChecks returns the checks stored for round, in the order they were stored.
func stepDoneRoundChecks(checks []store.StepCheck, round int) []store.StepCheck {
	var out []store.StepCheck
	for _, c := range checks {
		if c.Round == round {
			out = append(out, c)
		}
	}
	return out
}

// stepDoneNames returns the names of checks in order.
func stepDoneNames(checks []store.StepCheck) []string {
	names := make([]string, 0, len(checks))
	for _, c := range checks {
		names = append(names, c.Name)
	}
	return names
}

// stepDoneCheck returns the check named name among checks and fails the test if it is absent.
func stepDoneCheck(t *testing.T, checks []store.StepCheck, name string) store.StepCheck {
	t.Helper()
	for _, c := range checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no check named %q in %+v", name, checks)
	return store.StepCheck{}
}

func TestStepDoneCleanOwnedCommitAndGreenGateEndDone(t *testing.T) {
	dir, start := stepDoneRepo(t)
	commit := stepGitTestCommit(t, dir, map[string]string{stepDoneOwned: "owned\n"})
	marker := filepath.Join(t.TempDir(), "environ")
	gate := stepDoneScript(t, fmt.Sprintf(`printf '%%s\n' "${TMUX-unset}" > %q; echo green`, marker))
	lock := filepath.Join(t.TempDir(), "slot")
	f := newStepDoneFixture(t, stepDoneWriting(dir, start, []string{gate}, time.Minute, lock))
	_, done := f.start(t)

	f.run.Return(stepDoneReturn(commit))
	waitRunEnd(t, done)

	row := f.row(t)
	if row.End != stepLoopEndDone || row.Status != stepLoopDone {
		t.Fatalf("end = %q status %q, want done DONE", row.End, row.Status)
	}
	wantNames := []string{
		stepLoopSchemaName, stepGitHeadName, stepGitAncestryName, stepGitCleanName, stepGitOwnedName, stepLoopGateName,
	}
	if got := stepDoneNames(row.Checks); !reflect.DeepEqual(got, wantNames) {
		t.Fatalf("checks = %v, want %v", got, wantNames)
	}
	for _, c := range row.Checks {
		if c.Round != 1 || !c.OK {
			t.Errorf("check %+v, want round 1 passing", c)
		}
	}
	if got := f.sessions.continuedTexts(); len(got) != 0 {
		t.Errorf("Continue called %d times for a green done return, want 0", len(got))
	}
	environ, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("gate did not run: %v", err)
	}
	if strings.TrimSpace(string(environ)) != "unset" {
		t.Errorf("gate saw TMUX = %q, want it removed", environ)
	}
	if _, err := os.Stat(lock); err != nil {
		t.Errorf("gate did not hold its lock file %s: %v", lock, err)
	}
}

func TestStepDoneGateRedThenGreenContinuesOnceNamingGate(t *testing.T) {
	dir, start := stepDoneRepo(t)
	commit := stepGitTestCommit(t, dir, map[string]string{stepDoneOwned: "owned\n"})
	marker := filepath.Join(t.TempDir(), "ran-once")
	gate := stepDoneScript(t, fmt.Sprintf(`if [ -e %[1]q ]; then echo green; exit 0; fi
touch %[1]q; echo red; exit 1`, marker))
	lock := filepath.Join(t.TempDir(), "slot")
	f := newStepDoneFixture(t, stepDoneWriting(dir, start, []string{gate}, time.Minute, lock))
	_, done := f.start(t)

	f.run.Return(stepDoneReturn(commit))
	f.run.Return(stepDoneReturn(commit))
	waitRunEnd(t, done)

	continued := f.sessions.continuedTexts()
	if len(continued) != 1 {
		t.Fatalf("Continue called %d times, want 1", len(continued))
	}
	if !strings.Contains(continued[0], stepLoopGateName) {
		t.Errorf("Continue text %q does not name the gate check", continued[0])
	}
	row := f.row(t)
	if row.End != stepLoopEndDone {
		t.Fatalf("end = %q, want done", row.End)
	}
	gateRed := stepDoneCheck(t, stepDoneRoundChecks(row.Checks, 1), stepLoopGateName)
	if gateRed.OK || !strings.Contains(gateRed.Detail, "gate exited 1") || !strings.Contains(gateRed.Detail, "red") {
		t.Errorf("round 1 gate check = %+v, want a failing exit 1 with the output tail", gateRed)
	}
	for _, c := range stepDoneRoundChecks(row.Checks, 2) {
		if !c.OK {
			t.Errorf("round 2 check %+v, want passing", c)
		}
	}
	if got := len(stepDoneRoundChecks(row.Checks, 2)); got != 6 {
		t.Errorf("round 2 stored %d checks, want 6", got)
	}
}

func TestStepDoneUnownedPathSendsBackOwnedAndRunsNoGate(t *testing.T) {
	dir, start := stepDoneRepo(t)
	commit := stepGitTestCommit(t, dir, map[string]string{"other/outside.txt": "stray\n"})
	sentinel := filepath.Join(t.TempDir(), "gate-ran")
	gate := stepDoneScript(t, fmt.Sprintf("touch %q", sentinel))
	lock := filepath.Join(t.TempDir(), "slot")
	f := newStepDoneFixture(t, stepDoneWriting(dir, start, []string{gate}, time.Minute, lock))
	_, done := f.start(t)

	for range stepLoopMaxContinues + 1 {
		f.run.Return(stepDoneReturn(commit))
	}
	waitRunEnd(t, done)

	if _, err := os.Stat(sentinel); err == nil {
		t.Fatal("gate ran for a return with an unowned path, want no gate run")
	}
	row := f.row(t)
	if row.End != stepLoopEndChecksFailed {
		t.Fatalf("end = %q, want checks_failed", row.End)
	}
	if !strings.Contains(row.Reason, stepGitOwnedName) {
		t.Errorf("reason %q does not name the owned check", row.Reason)
	}
	for _, text := range f.sessions.continuedTexts() {
		if !strings.Contains(text, stepGitOwnedName) {
			t.Errorf("Continue text %q does not name the owned check", text)
		}
	}
	for round := 1; round <= stepLoopMaxContinues+1; round++ {
		checks := stepDoneRoundChecks(row.Checks, round)
		if got := len(checks); got != 5 {
			t.Errorf("round %d stored %d checks, want 5 with no gate: %+v", round, got, checks)
		}
		owned := stepDoneCheck(t, checks, stepGitOwnedName)
		if owned.OK || !strings.Contains(owned.Detail, "other/outside.txt") {
			t.Errorf("round %d owned check = %+v, want a failure naming the path", round, owned)
		}
	}
}

func TestStepDoneGateOverLimitFailsGateNamingLimit(t *testing.T) {
	dir, start := stepDoneRepo(t)
	commit := stepGitTestCommit(t, dir, map[string]string{stepDoneOwned: "owned\n"})
	gate := stepDoneScript(t, "sleep 30")
	lock := filepath.Join(t.TempDir(), "slot")
	f := newStepDoneFixture(t, stepDoneWriting(dir, start, []string{gate}, 200*time.Millisecond, lock))
	_, done := f.start(t)

	for range stepLoopMaxContinues + 1 {
		f.run.Return(stepDoneReturn(commit))
	}
	waitRunEnd(t, done)

	row := f.row(t)
	if row.End != stepLoopEndChecksFailed {
		t.Fatalf("end = %q, want checks_failed", row.End)
	}
	gateCheck := stepDoneCheck(t, stepDoneRoundChecks(row.Checks, 1), stepLoopGateName)
	if gateCheck.OK || !strings.Contains(gateCheck.Detail, "200ms") {
		t.Errorf("gate check = %+v, want a failure naming the 200ms limit", gateCheck)
	}
	if !strings.Contains(row.Reason, stepLoopGateName) {
		t.Errorf("reason %q does not name the gate check", row.Reason)
	}
}

func TestStepDoneReadOnlyDoneReturnEndsDoneWithSchemaCheckOnly(t *testing.T) {
	gate := stepDoneScript(t, "exit 1")
	lock := filepath.Join(t.TempDir(), "slot")
	f := newStepDoneFixture(t, func(cfg *stepRunConfig) {
		cfg.writing = false
		cfg.dir = "/nonexistent/not-a-repo"
		cfg.start = "HEAD"
		cfg.owned = []string{stepDoneOwned}
		cfg.gate = []string{gate}
		cfg.gateLimit = time.Minute
		cfg.locks = []string{lock}
	})
	_, done := f.start(t)

	f.run.Return(session.TurnEnded{StructuredOutput: json.RawMessage(`{"status":"DONE"}`)})
	waitRunEnd(t, done)

	row := f.row(t)
	if row.End != stepLoopEndDone {
		t.Fatalf("end = %q, want done", row.End)
	}
	want := []store.StepCheck{{Round: 1, Name: stepLoopSchemaName, OK: true}}
	if !reflect.DeepEqual(row.Checks, want) {
		t.Errorf("checks = %+v, want only the schema check %+v", row.Checks, want)
	}
	if _, err := os.Stat(lock); err == nil {
		t.Error("read-only done return took the gate lock, want no gate run")
	}
}

func TestStepDoneGateErrorFromEndedCtxStoresNoEnd(t *testing.T) {
	dir, start := stepDoneRepo(t)
	commit := stepGitTestCommit(t, dir, map[string]string{stepDoneOwned: "owned\n"})
	started := filepath.Join(t.TempDir(), "started")
	gate := stepDoneScript(t, fmt.Sprintf("echo started > %q; sleep 30", started))
	lock := filepath.Join(t.TempDir(), "slot")
	f := newStepDoneFixture(t, stepDoneWriting(dir, start, []string{gate}, time.Minute, lock))
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	done := f.runOn(ctx)

	f.run.Return(stepDoneReturn(commit))
	gateWaitFile(t, started)
	cancel()
	waitRunEnd(t, done)

	f.assertNoEndAfterCancel(t)
}

func TestStepDoneNonStringCommitSendsBackHeadAndReadOnlyIgnoresIt(t *testing.T) {
	dir, start := stepDoneRepo(t)
	lock := filepath.Join(t.TempDir(), "slot")
	f := newStepDoneFixture(t, stepDoneWriting(dir, start, nil, time.Minute, lock))
	_, done := f.start(t)

	bad := session.TurnEnded{StructuredOutput: json.RawMessage(`{"status":"DONE","commit":123}`)}
	f.run.Return(bad)
	waitUntil(t, "the bad commit is sent back", func() bool { return len(f.sessions.continuedTexts()) == 1 })
	if text := f.sessions.continuedTexts()[0]; !strings.Contains(text, stepGitHeadName) {
		t.Errorf("Continue text %q does not name the head check", text)
	}
	f.run.Return(stepDoneReturn(start))
	waitRunEnd(t, done)
	row := f.row(t)
	if row.End != stepLoopEndDone {
		t.Fatalf("writing end = %q, want done after the good return", row.End)
	}
	if head := stepDoneCheck(t, stepDoneRoundChecks(row.Checks, 1), stepGitHeadName); head.OK {
		t.Errorf("round 1 head check = %+v, want a failure for a non-string commit", head)
	}

	ro := newStepDoneFixture(t, func(cfg *stepRunConfig) { cfg.writing = false })
	_, roDone := ro.start(t)
	ro.run.Return(bad)
	waitRunEnd(t, roDone)
	if got := ro.row(t); got.End != stepLoopEndDone {
		t.Fatalf("read-only end = %q, want done for a non-string commit", got.End)
	}
}
