package enginehost

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// resumeFrom makes the fixture's run resume from restore when it runs. The run is rebuilt from its configuration, so a
// clock or limit set on it first carries over.
func (f *stepLoopFixture) resumeFrom(restore stepRestore) {
	cfg := f.run.stepRunConfig
	cfg.restore = &restore
	f.run = newStepRun(cfg)
}

// commit stores ops on the fixture's step through its writer and returns the step row they leave.
func (f *stepLoopFixture) commit(t *testing.T, ops ...store.EngineOp) store.StepRow {
	t.Helper()
	if err := f.writer.Commit(t.Context(), stepLoopID, ops...); err != nil {
		t.Fatalf("commit ops of step: %v", err)
	}
	return f.row(t)
}

// resumeGateRun returns the gate a re-adoption test runs as name in stateDir. The gate counts its starts in counter,
// waits until release exists, then prints green and exits 0. The cleanup writes release, so a failed test leaves no
// gate running.
func resumeGateRun(t *testing.T, stateDir, name, counter, release string) gateRun {
	t.Helper()
	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0o600) })
	return gateRun{
		Argv: []string{"/bin/sh", "-c",
			`n=$(cat "$COUNTER" 2>/dev/null || echo 0); echo $((n + 1)) > "$COUNTER"; ` +
				`while [ ! -e "$RELEASE" ]; do sleep 0.05; done; echo green; exit 0`},
		Dir:      t.TempDir(),
		Limit:    time.Minute,
		Name:     name,
		StateDir: stateDir,
		Environ:  gateEnv("COUNTER="+counter, "RELEASE="+release),
	}
}

// resumeDoneOutput is the output of a done return of a writing step that names commit.
func resumeDoneOutput(commit string) string {
	return fmt.Sprintf(`{"status":%q,"commit":%q}`, stepLoopDone, commit)
}

func TestStepLoopResumeReturnedFailureGoesBackOnce(t *testing.T) {
	f := newStepLoopFixture(t, nil)
	row := f.commit(t,
		store.StepReturn{ID: stepLoopID, Result: `{"status":1}`, Checks: []store.StepCheck{
			{Round: 1, Name: stepLoopSchemaName, OK: false, Detail: "$.status: want string"},
		}},
		store.StepLoop{ID: stepLoopID, Round: 1},
	)
	f.resumeFrom(stepRestore{Row: row, Turn: store.StepTurnEnd{Seq: 4, Returned: true}, Found: true})
	f.start(t)
	f.loopSettled(t)

	continued := f.sessions.continuedTexts()
	if len(continued) != 1 || !strings.Contains(continued[0], stepLoopSchemaName) {
		t.Fatalf("Continue texts = %q, want one that names the failed schema check", continued)
	}
	row = f.row(t)
	if row.LoopRound != 1 || row.LoopContinues != 1 || row.End != "" {
		t.Fatalf("loop round %d continues %d end %q, want round 1, continues 1 and no end",
			row.LoopRound, row.LoopContinues, row.End)
	}
}

func TestStepLoopResumeReturnedDoneReturnIsJudged(t *testing.T) {
	haiku := &judgeLoopHaiku{replies: []judgeLoopReply{judgeLoopVerdict("met", "the report is complete")}}
	f := newJudgeLoopFixture(t, haiku, judgeLoopNoConfig)
	row := f.commit(t,
		store.StepReturn{ID: stepLoopID, Result: `{"status":"DONE"}`, Checks: []store.StepCheck{
			{Round: 1, Name: stepLoopSchemaName, OK: true},
		}},
		store.StepLoop{ID: stepLoopID, Round: 1},
	)
	f.resumeFrom(stepRestore{Row: row, Turn: store.StepTurnEnd{Seq: 4, Returned: true}, Found: true})
	_, done := f.start(t)
	waitRunEnd(t, done)

	row = f.row(t)
	if row.End != stepLoopEndDone || row.Status != stepLoopDone {
		t.Fatalf("end = %q status %q, want done DONE", row.End, row.Status)
	}
	if got := len(haiku.callInputs()); got != 1 {
		t.Fatalf("judge called %d times, want 1 for the returned done return", got)
	}
	if len(row.Verdicts) != 1 {
		t.Fatalf("stored %d verdicts, want 1: %+v", len(row.Verdicts), row.Verdicts)
	}
	judgeLoopCheckVerdict(t, row.Verdicts[0], 1, "met", "the report is complete", "end")
	if got := f.sessions.continuedTexts(); len(got) != 0 {
		t.Errorf("Continue called %d times for a met return, want 0", len(got))
	}
}

func TestStepLoopResumeReturnedOtherStatusEndsReturned(t *testing.T) {
	f := newStepLoopFixture(t, nil)
	row := f.commit(t,
		store.StepReturn{ID: stepLoopID, Result: `{"status":"PARTIAL"}`, Checks: []store.StepCheck{
			{Round: 1, Name: stepLoopSchemaName, OK: true},
		}},
		store.StepLoop{ID: stepLoopID, Round: 1},
	)
	f.resumeFrom(stepRestore{Row: row, Turn: store.StepTurnEnd{Seq: 4, Returned: true}, Found: true})
	_, done := f.start(t)
	waitRunEnd(t, done)

	row = f.row(t)
	if row.End != stepLoopEndReturned || row.Status != "PARTIAL" {
		t.Fatalf("end = %q status %q, want returned PARTIAL", row.End, row.Status)
	}
	if got := f.sessions.continuedTexts(); len(got) != 0 {
		t.Errorf("Continue called %d times for a returned end, want 0", len(got))
	}
}

func TestStepLoopResumeFreshDoneTurnRunsItsGateOnce(t *testing.T) {
	dir, start := stepDoneRepo(t)
	commit := stepGitTestCommit(t, dir, map[string]string{stepDoneOwned: "owned\n"})
	counter := filepath.Join(t.TempDir(), "counter")
	gate := stepDoneScript(t, fmt.Sprintf(
		`n=$(cat %[1]q 2>/dev/null || echo 0); echo $((n + 1)) > %[1]q; echo green`, counter))
	pools := &fakeGatePools{}
	f := newStepDoneFixture(t, stepDoneWriting(dir, start, []string{gate}, time.Minute, pools))
	row := f.row(t)
	f.resumeFrom(stepRestore{Row: row, Turn: store.StepTurnEnd{Seq: 3, Output: resumeDoneOutput(commit)}, Found: true})
	_, done := f.start(t)
	waitRunEnd(t, done)

	row = f.row(t)
	if row.End != stepLoopEndDone {
		t.Fatalf("end = %q, want done", row.End)
	}
	data, err := os.ReadFile(counter)
	if err != nil || strings.TrimSpace(string(data)) != "1" {
		t.Fatalf("counter = %q, %v: want the gate started once", data, err)
	}
	if n := len(pools.requested()); n != 1 {
		t.Fatalf("Acquire was called %d times, want 1 for the gate", n)
	}
	if check := stepDoneCheck(t, stepDoneRoundChecks(row.Checks, 1), stepLoopGateName); !check.OK {
		t.Fatalf("gate check = %+v, want passing", check)
	}
}

func TestStepLoopResumeLiveStoredGateIsReadoptedNotRerun(t *testing.T) {
	dir, start := stepDoneRepo(t)
	commit := stepGitTestCommit(t, dir, map[string]string{stepDoneOwned: "owned\n"})
	stateDir := gateShortStateDir(t)
	counter := filepath.Join(t.TempDir(), "counter")
	release := filepath.Join(t.TempDir(), "release")
	pools := &fakeGatePools{}
	run := resumeGateRun(t, stateDir, stepLoopID+"-gate", counter, release)
	gateDetachedRun(t, pools, run, counter)
	plan, err := planGate(run)
	if err != nil {
		t.Fatalf("planGate: %v", err)
	}
	f := newStepDoneFixture(t, func(cfg *stepRunConfig) {
		stepDoneWriting(dir, start, run.Argv, time.Minute, pools)(cfg)
		cfg.stateDir = stateDir
	})
	row := f.commit(t, store.StepGate{ID: stepLoopID, Grant: "grant-1", Holder: plan.dir, Round: 1})
	f.resumeFrom(stepRestore{Row: row, Turn: store.StepTurnEnd{Seq: 3, Output: resumeDoneOutput(commit)}, Found: true})
	_, done := f.start(t)
	gateWaitRequests(t, pools, 2)
	if err = os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatalf("release the gate: %v", err)
	}
	waitRunEnd(t, done)

	row = f.row(t)
	if row.End != stepLoopEndDone {
		t.Fatalf("end = %q reason %q, want done", row.End, row.Reason)
	}
	data, err := os.ReadFile(counter)
	if err != nil || strings.TrimSpace(string(data)) != "1" {
		t.Fatalf("counter = %q, %v: want the live gate started once and never rerun", data, err)
	}
	requests := pools.requested()
	if len(requests) != 2 || requests[1].GrantID != "grant-1" || requests[1].Holder != plan.dir {
		t.Fatalf("requests = %+v, want the original grant then one reclaim of grant-1 for %s", requests, plan.dir)
	}
	if check := stepDoneCheck(t, stepDoneRoundChecks(row.Checks, 1), stepLoopGateName); !check.OK ||
		!strings.Contains(check.Detail, "gate exited 0") {
		t.Fatalf("gate check = %+v, want the readopted gate's passing exit recorded", check)
	}
}

func TestStepLoopResumeLostGateEndsEngineRestarted(t *testing.T) {
	dir, start := stepDoneRepo(t)
	commit := stepGitTestCommit(t, dir, map[string]string{stepDoneOwned: "owned\n"})
	marker := filepath.Join(t.TempDir(), "gate-ran")
	gate := stepDoneScript(t, fmt.Sprintf("touch %q", marker))
	stateDir := gateShortStateDir(t)
	pools := &fakeGatePools{}
	plan, err := planGate(gateRun{Argv: []string{gate}, Dir: dir, Name: stepLoopID + "-gate", StateDir: stateDir})
	if err != nil {
		t.Fatalf("planGate: %v", err)
	}
	f := newStepDoneFixture(t, func(cfg *stepRunConfig) {
		stepDoneWriting(dir, start, []string{gate}, time.Minute, pools)(cfg)
		cfg.stateDir = stateDir
	})
	row := f.commit(t, store.StepGate{ID: stepLoopID, Grant: "grant-1", Holder: plan.dir, Round: 1})
	f.resumeFrom(stepRestore{Row: row, Turn: store.StepTurnEnd{Seq: 3, Output: resumeDoneOutput(commit)}, Found: true})
	_, done := f.start(t)
	waitRunEnd(t, done)

	row = f.row(t)
	if row.End != stepRestartedEnd {
		t.Fatalf("end = %q reason %q, want %s", row.End, row.Reason, stepRestartedEnd)
	}
	if !strings.Contains(row.Reason, errGateLost.Error()) {
		t.Errorf("reason %q does not name the lost gate %q", row.Reason, errGateLost)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Error("the gate ran although its outcome was lost, want it never to run again")
	}
	if n := len(pools.requested()); n != 0 {
		t.Errorf("Acquire was called %d times, want none", n)
	}
}

func TestStepLoopResumeRestoredNudgeContinuesTurnEnd(t *testing.T) {
	f := newStepLoopFixture(t, nil)
	const text = "The watchdog interrupted your turn. Continue the step where it stopped."
	row := f.commit(t, store.StepLoop{ID: stepLoopID, Round: 1, Nudge: text, Nudged: true})
	f.resumeFrom(stepRestore{Row: row, Turn: store.StepTurnEnd{Seq: 6, IsError: true}, Found: true})
	f.start(t)
	f.loopSettled(t)

	if got := f.sessions.continuedTexts(); len(got) != 1 || got[0] != text {
		t.Fatalf("Continue texts = %q, want the restored nudge once", got)
	}
	row = f.row(t)
	if row.LoopNudge != "" {
		t.Errorf("loop nudge = %q after the turn end, want it cleared", row.LoopNudge)
	}
	if !row.LoopNudged {
		t.Error("loop nudged = false after the restored turn end, want the spent nudge kept")
	}
}

func TestStepLoopResumeRestoredOwnerNudgeContinuesTurnEnd(t *testing.T) {
	f := newStepLoopFixture(t, nil)
	const text = "use the parser API instead"
	row := f.commit(t, store.StepLoop{ID: stepLoopID, Round: 1, OwnerNudge: text})
	f.resumeFrom(stepRestore{Row: row, Turn: store.StepTurnEnd{Seq: 6, Output: `{"status":"DONE"}`}, Found: true})
	f.start(t)
	f.loopSettled(t)

	if got := f.sessions.continuedTexts(); len(got) != 1 || got[0] != text {
		t.Fatalf("Continue texts = %q, want the restored owner nudge once", got)
	}
	row = f.row(t)
	if row.LoopOwnerNudge != "" {
		t.Errorf("loop owner nudge = %q after the turn end, want it cleared", row.LoopOwnerNudge)
	}
	if row.LoopRound != 1 {
		t.Errorf("loop round = %d, want 1: an owner nudge's turn end is no round", row.LoopRound)
	}
}

func TestStepLoopResumeRestoredHeldTurnEndWaitsForOwnerNudge(t *testing.T) {
	f := newStepLoopFixture(t, nil)
	row := f.commit(t, store.StepLoop{ID: stepLoopID, Round: 1, Held: true})
	f.resumeFrom(stepRestore{Row: row, Turn: store.StepTurnEnd{Seq: 6, Output: `{"status":"DONE"}`}, Found: true})
	f.start(t)
	f.loopSettled(t)

	row = f.row(t)
	if row.End != "" || !row.LoopHeld || row.LoopRound != 1 {
		t.Fatalf("end %q held %v round %d, want no end, still held and no round for the held turn end",
			row.End, row.LoopHeld, row.LoopRound)
	}
	if got := f.sessions.continuedTexts(); len(got) != 0 {
		t.Fatalf("Continue texts = %q before any owner nudge, want none", got)
	}

	const text = "resume with the parser API"
	if err := f.run.do(t.Context(), func(ctx context.Context) error {
		return f.run.nudgeOwner(ctx, text)
	}); err != nil {
		t.Fatalf("owner nudge of the restored held step: %v", err)
	}
	f.settle(t)
	if got := f.sessions.continuedTexts(); len(got) != 1 || got[0] != text {
		t.Fatalf("Continue texts = %q, want the owner nudge once", got)
	}
	if row := f.row(t); row.LoopHeld {
		t.Error("loop held = true after the owner nudge was sent, want false")
	}
}

func TestStepLoopResumeWithoutTurnEndLeavesTurnRunning(t *testing.T) {
	f := newStepLoopFixture(t, nil)
	clock := newIdleClock()
	f.run.now = clock.now
	f.run.modelLimit = time.Hour
	row := f.row(t)
	f.resumeFrom(stepRestore{Row: row, Found: false})
	f.start(t)
	f.loopSettled(t)

	clock.advance(5 * time.Minute)
	trip, due := f.run.watch.due(clock.now())
	if !due || trip.Kind != watchTripPeriodic {
		t.Fatalf("due = %+v, %v, want a periodic trip after five minutes of the restored turn", trip, due)
	}
}

func TestStepLoopResumeTurnEndLeavesNoTurnRunning(t *testing.T) {
	f := newStepLoopFixture(t, nil)
	clock := newIdleClock()
	f.run.now = clock.now
	f.run.modelLimit = time.Hour
	row := f.row(t)
	f.resumeFrom(stepRestore{Row: row, Turn: store.StepTurnEnd{Seq: 6, Output: `{"status":"PARTIAL"}`}, Found: true})
	_, done := f.start(t)
	waitRunEnd(t, done)

	clock.advance(5 * time.Minute)
	if trip, due := f.run.watch.due(clock.now()); due {
		t.Fatalf("due = %+v after a turn end was found, want no trip: no turn is running", trip)
	}
}

func TestStepLoopResumeFreshDoneAfterJudgeSendBackEndsGoalUnmet(t *testing.T) {
	haiku := &judgeLoopHaiku{replies: []judgeLoopReply{judgeLoopVerdict("unmet", "the summary is still missing")}}
	f := newJudgeLoopFixture(t, haiku, judgeLoopNoConfig)
	row := f.commit(t, store.StepLoop{ID: stepLoopID, Round: 1, JudgeSent: true})
	const output = `{"status":"DONE","note":"second"}`
	f.resumeFrom(stepRestore{Row: row, Turn: store.StepTurnEnd{Seq: 9, Output: output}, Found: true})
	_, done := f.start(t)
	waitRunEnd(t, done)

	row = f.row(t)
	if row.End != judgeLoopEndGoalUnmet {
		t.Fatalf("end = %q, want goal_unmet: the judge already sent the step back once", row.End)
	}
	if row.Result != output {
		t.Errorf("result = %s, want the second return kept as %s", row.Result, output)
	}
	if len(row.Verdicts) != 1 {
		t.Fatalf("stored %d verdicts, want 1: %+v", len(row.Verdicts), row.Verdicts)
	}
	judgeLoopCheckVerdict(t, row.Verdicts[0], 2, "unmet", "the summary is still missing", "end")
	if got := f.sessions.continuedTexts(); len(got) != 0 {
		t.Errorf("Continue called %d times, want 0", len(got))
	}
}

func TestStepLoopResumeNudgedTurnKeepsPeriodicCheckOff(t *testing.T) {
	f := newStepLoopFixture(t, nil)
	clock := newIdleClock()
	f.run.now = clock.now
	f.run.modelLimit = time.Hour
	row := f.commit(t, store.StepLoop{
		ID: stepLoopID, Round: 1, Nudge: "The watchdog interrupted your turn.", Nudged: true,
	})
	f.resumeFrom(stepRestore{Row: row, Found: false})
	f.start(t)
	f.loopSettled(t)

	clock.advance(5 * time.Minute)
	if trip, due := f.run.watch.due(clock.now()); due {
		t.Fatalf("due = %+v after a restored nudge interrupted the running turn, want no periodic check", trip)
	}
}
