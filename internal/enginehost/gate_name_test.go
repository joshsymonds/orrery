package enginehost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/grants"
	"github.com/joshsymonds/orrery/internal/store"
)

// TestStepDoneGateNameIsStepIDGateAndRestartReadoptsItOnce detaches a writing step's done gate under its scope, so the gate
// keeps running under the holder named for the step and "-gate". A restart then re-adopts the gate under that name: the gate
// runs once, and the step ends done.
func TestStepDoneGateNameIsStepIDGateAndRestartReadoptsItOnce(t *testing.T) {
	t.Parallel()
	dir, start := stepDoneRepo(t)
	commit := stepGitTestCommit(t, dir, map[string]string{stepDoneOwned: "owned\n"})
	counter := filepath.Join(t.TempDir(), "counter")
	started := filepath.Join(t.TempDir(), "started")
	release := filepath.Join(t.TempDir(), "release")
	gate := stepDoneScript(t, fmt.Sprintf(
		`n=$(cat %[1]q 2>/dev/null || echo 0); echo $((n + 1)) > %[1]q; echo started > %[2]q; `+
			`while [ ! -e %[3]q ]; do sleep 0.05; done; echo green`,
		counter, started, release))
	pools := &fakeGatePools{drop: make(chan error, 1)}
	stateDir := gateShortStateDir(t)
	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0o600) })
	f := newStepDoneFixture(t, func(cfg *stepRunConfig) {
		stepDoneWriting(dir, start, []string{gate}, time.Minute, pools)(cfg)
		cfg.scope = fakeSystemdRunPath(t)
		cfg.stateDir = stateDir
	})
	gateHolder := holderDir(t, stateDir, stepLoopID+"-gate")

	_, done := f.start(t)
	f.run.Return(stepDoneReturn(commit))
	gateWaitFile(t, started, done)
	pools.drop <- grants.ErrClosed
	waitRunEnd(t, done)

	f.settle(t)
	row := f.row(t)
	if row.GateHolder != gateHolder {
		t.Fatalf("stored gate holder = %q, want %q", row.GateHolder, gateHolder)
	}
	if requests := pools.requested(); len(requests) != 1 || requests[0].Holder != gateHolder {
		t.Fatalf("gate requests = %+v, want one naming holder %q", requests, gateHolder)
	}

	f.resumeFrom(stepRestore{Row: row, Turn: store.StepTurnEnd{Seq: 3, Output: resumeDoneOutput(commit)}, Found: true})
	_, done = f.start(t)
	gateWaitRequests(t, pools, 2, done)
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatalf("release the gate: %v", err)
	}
	waitRunEnd(t, done)

	row = f.row(t)
	if row.End != stepLoopEndDone {
		t.Fatalf("end = %q reason %q, want done", row.End, row.Reason)
	}
	data, err := os.ReadFile(counter)
	if err != nil || strings.TrimSpace(string(data)) != "1" {
		t.Fatalf("counter = %q, %v: want the gate started once, not again after the restart", data, err)
	}
	requests := pools.requested()
	if len(requests) != 2 || requests[1].GrantID != "grant-1" || requests[1].Holder != gateHolder {
		t.Fatalf("gate requests = %+v, want the first grant then one reclaim of grant-1 for %q", requests, gateHolder)
	}
}

// TestStepRestartDuringARetriedRoundsGateStoresItsResultAndEnds restarts a writing step while the gate of its second
// round runs, after the first round's gate failed and went back, as a restart left step 3b7d8ceb: the row stores loop
// round 1 with round 1's failed checks, and the running gate as round 2. The restored run takes the turn end after its
// stored return as round 2, re-adopts that round's gate under its grant without starting it again, stores the gate's
// result with round 2's checks and ends done.
func TestStepRestartDuringARetriedRoundsGateStoresItsResultAndEnds(t *testing.T) {
	t.Parallel()
	dir, start := stepDoneRepo(t)
	commit := stepGitTestCommit(t, dir, map[string]string{stepDoneOwned: "owned\n"})
	counter := filepath.Join(t.TempDir(), "counter")
	started := filepath.Join(t.TempDir(), "started")
	release := filepath.Join(t.TempDir(), "release")
	// The first start fails at once. Every later start writes started and runs until release exists.
	gate := stepDoneScript(t, fmt.Sprintf(
		`n=$(cat %[1]q 2>/dev/null || echo 0); n=$((n + 1)); echo $n > %[1]q; `+
			`if [ $n -eq 1 ]; then echo red; exit 1; fi; echo started > %[2]q; `+
			`while [ ! -e %[3]q ]; do sleep 0.05; done; echo green`,
		counter, started, release))
	pools := &fakeGatePools{drop: make(chan error, 1)}
	stateDir := gateShortStateDir(t)
	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0o600) })
	f := newStepDoneFixture(t, func(cfg *stepRunConfig) {
		stepDoneWriting(dir, start, []string{gate}, time.Minute, pools)(cfg)
		cfg.scope = fakeSystemdRunPath(t)
		cfg.stateDir = stateDir
	})
	gateHolder := holderDir(t, stateDir, stepLoopID+"-gate")

	_, done := f.start(t)
	f.run.Return(stepDoneReturn(commit))
	f.run.Return(stepDoneReturn(commit))
	gateWaitFile(t, started, done)
	pools.drop <- grants.ErrClosed
	waitRunEnd(t, done)

	f.settle(t)
	row := f.row(t)
	if row.LoopRound != 1 || row.GateRound != 2 || row.GateHolder != gateHolder || row.GateGrant != "grant-2" {
		t.Fatalf("row at the restart: loop round %d, gate round %d, holder %q, grant %q; want 1, 2, %q, grant-2",
			row.LoopRound, row.GateRound, row.GateHolder, row.GateGrant, gateHolder)
	}

	turn := store.StepTurnEnd{Seq: row.LoopSeq + 1, Output: resumeDoneOutput(commit)}
	f.resumeFrom(stepRestore{Row: row, Turn: turn, Found: true})
	_, done = f.start(t)
	gateWaitRequests(t, pools, 3, done)
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatalf("release the gate: %v", err)
	}
	waitRunEnd(t, done)

	row = f.row(t)
	if row.End != stepLoopEndDone {
		t.Fatalf("end = %q reason %q, want done", row.End, row.Reason)
	}
	green := stepDoneCheck(t, stepDoneRoundChecks(row.Checks, 2), stepLoopGateName)
	if !green.OK || green.Stdout != "green\n" {
		t.Errorf("round 2 gate check = %+v, want the re-adopted gate's passing exit with stdout green", green)
	}
	if data, err := os.ReadFile(counter); err != nil || strings.TrimSpace(string(data)) != "2" {
		t.Fatalf("counter = %q, %v: want two gate starts, none after the restart", data, err)
	}
	requests := pools.requested()
	if len(requests) != 3 || requests[2].GrantID != "grant-2" || requests[2].Holder != gateHolder {
		t.Fatalf("gate requests = %+v, want two grants then one reclaim of grant-2 for %q", requests, gateHolder)
	}
	if continued := f.sessions.continuedTexts(); len(continued) != 1 {
		t.Fatalf("Continue called %d times, want once, for round 1's failed gate", len(continued))
	}
}

// evidenceGateCountScript is a capture that counts its starts in $COUNTER, writes $STARTED once it runs, waits until
// $RELEASE exists, then prints done and exits 3.
const evidenceGateCountScript = `n=$(cat "$COUNTER" 2>/dev/null || echo 0); echo $((n + 1)) > "$COUNTER"; ` +
	`echo started > "$STARTED"; while [ ! -e "$RELEASE" ]; do sleep 0.05; done; echo done; exit 3`

// TestEvidenceCaptureGateNameIsRunIDAndRestartReadoptsItOnce detaches the gate of a capture run named by its run id, which
// keeps running under the holder named "evidence-" and the run id. A readopt under the stored grant re-adopts that gate under
// its name: the gate runs once, and its exit is kept.
func TestEvidenceCaptureGateNameIsRunIDAndRestartReadoptsItOnce(t *testing.T) {
	t.Parallel()
	repo, head := evidenceCaptureRepo(t)
	counter := filepath.Join(t.TempDir(), "counter")
	startedFile := filepath.Join(t.TempDir(), "started")
	release := filepath.Join(t.TempDir(), "release")
	pools := &fakeGatePools{}
	c := evidenceCaptureFor(t, pools, "COUNTER="+counter, "STARTED="+startedFile, "RELEASE="+release)
	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0o600) })
	item := evidence.Item{Capture: []string{"/bin/sh", "-c", evidenceGateCountScript}, Limit: 60}

	granted := make(chan string, 1)
	started := func(ctx context.Context, grant, _ string) error {
		granted <- grant
		return ctx.Err()
	}
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	var runErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, runErr = c.run(ctx, repo, head, "named", item, started)
	}()
	gateWaitFile(t, startedFile, done)
	cancel(errGateDetach)
	evidenceCaptureWait(t, done)
	if !errors.Is(runErr, errGateDetach) {
		t.Fatalf("detached run = %v, want errGateDetach", runErr)
	}
	grant := <-granted
	holder := filepath.Join(c.gateStateDir, "holders", "evidence-named")
	if requests := pools.requested(); len(requests) != 1 || requests[0].Holder != holder {
		t.Fatalf("gate requests = %+v, want one naming holder %q", requests, holder)
	}

	// A restart removes the worktree directory, which readopt re-adds.
	if err := os.RemoveAll(filepath.Join(c.worktrees, "named")); err != nil {
		t.Fatalf("remove the worktree directory: %v", err)
	}
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatalf("release the gate: %v", err)
	}
	gateWaitExit(t, holder)
	gateWaitLockFree(t, holder)

	got, err := c.readopt(t.Context(), repo, head, "named", item, grant)
	if err != nil {
		t.Fatalf("readopt: %v", err)
	}
	if got.NotCaptured != "" || got.TimedOut || got.Exit != 3 {
		t.Fatalf("readopt = %+v, want exit 3 and captured", got)
	}
	data, err := os.ReadFile(counter)
	if err != nil || strings.TrimSpace(string(data)) != "1" {
		t.Fatalf("counter = %q, %v: want the gate started once, not again after the restart", data, err)
	}
	requests := pools.requested()
	if len(requests) != 2 || requests[1].GrantID != grant || requests[1].Holder != holder {
		t.Fatalf("gate requests = %+v, want the run's grant then one reclaim of %q for %q", requests, grant, holder)
	}
}
