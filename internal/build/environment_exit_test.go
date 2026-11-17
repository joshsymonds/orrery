package build

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// envEnded is the time the environment tests' runs ended at.
func envEnded() time.Time {
	return time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
}

// envRun returns an evidence run of phase at rerun of attempt of task 3 of decideEpic, ended with exit and kept with the
// not-captured reason notCaptured. A run that exited store.EnvironmentExit is kept with store.NotCapturedEnvironment.
func envRun(phase string, attempt, rerun int64, exit int, notCaptured string) *store.EvidenceRun {
	return &store.EvidenceRun{
		ID: RunID(decideEpic, 3, phase, attempt, rerun), Phase: phase, Ended: envEnded(),
		Exit: exit, NotCaptured: notCaptured,
	}
}

// TestDecideRedEnvironmentExit covers R2 for the red capture: a run that exits store.EnvironmentExit without timing out
// reruns under its next rerun, the third in a row parks the build with a reason that names the capture and says
// environment, a run that holds at the base moves to implement, and any other exit is decided at once.
func TestDecideRedEnvironmentExit(t *testing.T) {
	t.Parallel()
	capture := evidence.Item{Kind: evidence.KindCapture, Capture: []string{"scripts/check"}}
	red := store.TaskBuild{Stage: StageRed, Base: decideBranch}
	withRerun := func(rerun int64) store.TaskBuild {
		build := red
		build.Rerun = rerun
		return build
	}
	parked := withRerun(2)
	parked.Held = HeldParked
	implement := store.TaskBuild{
		Stage: StageImplement, Base: decideBranch, Attempt: 1, RedRun: RunID(decideEpic, 3, StageRed, 0, 1),
	}
	holds := envRun(StageRed, 0, 1, 1, "")
	holds.Verdict = evidence.VerdictFails
	holds.Passed = true
	exitOne := envRun(StageRed, 0, 0, 1, "")
	exitOne.Verdict = evidence.VerdictFails
	exitOne.Passed = true
	const reason = "T3 red capture scripts/check exited 75 in 3 runs in a row: an environment result, not a verdict"
	runDecideCases(t, Decide, []decideCase{
		{
			name: "exit 75 on the first run reruns the unit",
			view: View{Epic: decideEpic, Task: store.EpicTask{Seq: 3, Build: red, Evidence: capture},
				Run: envRun(StageRed, 0, 0, store.EnvironmentExit, store.NotCapturedEnvironment)},
			want: Action{Kind: ActMove, Next: withRerun(1)},
		},
		{
			name: "exit 75 on the second run reruns the unit again",
			view: View{Epic: decideEpic, Task: store.EpicTask{Seq: 3, Build: withRerun(1), Evidence: capture},
				Run:               envRun(StageRed, 0, 1, store.EnvironmentExit, store.NotCapturedEnvironment),
				EnvironmentBefore: 1},
			want: Action{Kind: ActMove, Next: withRerun(2)},
		},
		{
			name: "exit 75 after a run that did not exit 75 reruns the unit",
			view: View{Epic: decideEpic, Task: store.EpicTask{Seq: 3, Build: withRerun(2), Evidence: capture},
				Run:               envRun(StageRed, 0, 2, store.EnvironmentExit, store.NotCapturedEnvironment),
				EnvironmentBefore: 1},
			want: Action{Kind: ActMove, Next: withRerun(3)},
		},
		{
			name: "third exit 75 in a row parks the build with a reason that says environment",
			view: View{Epic: decideEpic, Task: store.EpicTask{Seq: 3, Build: withRerun(2), Evidence: capture},
				Run:               envRun(StageRed, 0, 2, store.EnvironmentExit, store.NotCapturedEnvironment),
				EnvironmentBefore: 2},
			want: Action{Kind: ActPark, Next: parked, Reason: reason},
		},
		{
			name: "a rerun that holds at the base passes into implement",
			view: View{Epic: decideEpic, Task: store.EpicTask{Seq: 3, Build: withRerun(1), Evidence: capture},
				Run: holds, EnvironmentBefore: 1},
			want: Action{Kind: ActMove, Next: implement},
		},
		{
			name: "exit 1 at the base moves to implement at once",
			view: View{Epic: decideEpic, Task: store.EpicTask{Seq: 3, Build: red, Evidence: capture}, Run: exitOne},
			want: Action{Kind: ActMove, Next: store.TaskBuild{
				Stage: StageImplement, Base: decideBranch, Attempt: 1, RedRun: RunID(decideEpic, 3, StageRed, 0, 0),
			}},
		},
		{
			name: "a red capture not captured for another reason fails at once",
			view: View{Epic: decideEpic, Task: store.EpicTask{Seq: 3, Build: red, Evidence: capture},
				Run: envRun(StageRed, 0, 0, 0, "the runner has no display")},
			want: Action{Kind: ActFail, Reason: "red not captured: the runner has no display"},
		},
	})
}

// TestDecideGreenEnvironmentExit covers R2 for the green capture: an exit 75 reruns the green unit at its commit, a
// passing rerun goes on to review, the third exit 75 in a row parks the build, and exit 1 spends the attempt at once.
func TestDecideGreenEnvironmentExit(t *testing.T) {
	t.Parallel()
	capture := evidence.Item{Kind: evidence.KindCapture, Capture: []string{"scripts/check"}}
	green := store.TaskBuild{Stage: StageGreen, Base: "base", Attempt: 1, Revision: "rev", RedRun: "red-run"}
	withRerun := func(rerun int64) store.TaskBuild {
		build := green
		build.Rerun = rerun
		return build
	}
	parked := withRerun(2)
	parked.Held = HeldParked
	review := store.TaskBuild{
		Stage: StageReview, Base: "base", Attempt: 1, Revision: "rev", RedRun: "red-run",
		GreenRun: RunID(decideEpic, 3, StageGreen, 1, 1),
	}
	spent := store.TaskBuild{
		Stage: StageImplement, Base: "base", Attempt: 2, RedRun: "red-run",
		Failure: "green not met: exit 1, verdict fails",
	}
	const reason = "T3 green capture scripts/check exited 75 in 3 runs in a row: an environment result, not a verdict"
	passing := envRun(StageGreen, 1, 1, 0, "")
	passing.Verdict = evidence.VerdictHolds
	passing.Passed = true
	failing := envRun(StageGreen, 1, 0, 1, "")
	failing.Verdict = evidence.VerdictFails
	runDecideCases(t, Decide, []decideCase{
		{
			name: "exit 75 on the first run reruns the unit",
			view: View{Epic: decideEpic, Task: store.EpicTask{Seq: 3, Build: green, Evidence: capture},
				Run: envRun(StageGreen, 1, 0, store.EnvironmentExit, store.NotCapturedEnvironment)},
			want: Action{Kind: ActMove, Next: withRerun(1)},
		},
		{
			name: "exit 75 on the second run reruns the unit again",
			view: View{Epic: decideEpic, Task: store.EpicTask{Seq: 3, Build: withRerun(1), Evidence: capture},
				Run:               envRun(StageGreen, 1, 1, store.EnvironmentExit, store.NotCapturedEnvironment),
				EnvironmentBefore: 1},
			want: Action{Kind: ActMove, Next: withRerun(2)},
		},
		{
			name: "third exit 75 in a row parks the build with a reason that says environment",
			view: View{Epic: decideEpic, Task: store.EpicTask{Seq: 3, Build: withRerun(2), Evidence: capture},
				Run:               envRun(StageGreen, 1, 2, store.EnvironmentExit, store.NotCapturedEnvironment),
				EnvironmentBefore: 2},
			want: Action{Kind: ActPark, Next: parked, Reason: reason},
		},
		{
			name: "a passing rerun goes on to review",
			view: View{Epic: decideEpic, Task: store.EpicTask{Seq: 3, Build: withRerun(1), Evidence: capture},
				Run: passing, EnvironmentBefore: 1},
			want: Action{Kind: ActMove, Next: review},
		},
		{
			name: "exit 1 spends the first attempt at once",
			view: View{Epic: decideEpic, Task: store.EpicTask{Seq: 3, Build: green, Evidence: capture}, Run: failing},
			want: Action{Kind: ActMove, Next: spent},
		},
	})
}

// TestDecideReproductionEnvironmentExit covers R2 for the reproduction of a test item's green unit: an exit 75 that did
// not time out reruns the unit, a reproduction that exits 1 starts the green run at once and on its rerun, the third
// exit 75 in a row parks the build, and a timeout is decided as before.
func TestDecideReproductionEnvironmentExit(t *testing.T) {
	t.Parallel()
	item := evidence.Item{
		Kind: evidence.KindTest, Capture: []string{"scripts/check"}, Tests: []string{"feature_test.go"},
	}
	green := store.TaskBuild{Stage: StageGreen, Base: reproBase, Attempt: 1, Revision: reproRevision, RedRun: "red-run"}
	withRerun := func(rerun int64) store.TaskBuild {
		build := green
		build.Rerun = rerun
		return build
	}
	parked := withRerun(2)
	parked.Held = HeldParked
	repro := func(rerun int64, exit int, timedOut bool) *store.NodeCheck {
		return &store.NodeCheck{
			ID: RunID(decideEpic, 3, unitRepro, 1, rerun), State: store.NodeEnded, Exit: exit, TimedOut: timedOut,
		}
	}
	startGreen := func(rerun int64) Action {
		return Action{
			Kind: ActStartRun, Phase: StageGreen, Revision: reproRevision,
			ID: RunID(decideEpic, 3, StageGreen, 1, rerun),
		}
	}
	const reason = "T3 reproduction scripts/check exited 75 in 3 runs in a row: an environment result, not a verdict"
	runDecideCases(t, Decide, []decideCase{
		{
			name: "exit 75 on the first reproduction reruns the unit",
			view: View{Epic: decideEpic, Task: store.EpicTask{Seq: 3, Build: green, Evidence: item},
				Repro: repro(0, store.EnvironmentExit, false)},
			want: Action{Kind: ActMove, Next: withRerun(1)},
		},
		{
			name: "exit 75 on the second reproduction reruns the unit again",
			view: View{Epic: decideEpic, Task: store.EpicTask{Seq: 3, Build: withRerun(1), Evidence: item},
				Repro: repro(1, store.EnvironmentExit, false), EnvironmentBefore: 1},
			want: Action{Kind: ActMove, Next: withRerun(2)},
		},
		{
			name: "third exit 75 in a row parks the build with a reason that says environment",
			view: View{Epic: decideEpic, Task: store.EpicTask{Seq: 3, Build: withRerun(2), Evidence: item},
				Repro: repro(2, store.EnvironmentExit, false), EnvironmentBefore: 2},
			want: Action{Kind: ActPark, Next: parked, Reason: reason},
		},
		{
			name: "a reproduction that exits 1 on its rerun starts the green run on that rerun",
			view: View{Epic: decideEpic, Task: store.EpicTask{Seq: 3, Build: withRerun(1), Evidence: item},
				Repro: repro(1, 1, false), EnvironmentBefore: 1},
			want: startGreen(1),
		},
		{
			name: "exit 1 on the first reproduction starts the green run at once",
			view: View{Epic: decideEpic, Task: store.EpicTask{Seq: 3, Build: green, Evidence: item},
				Repro: repro(0, 1, false)},
			want: startGreen(0),
		},
		{
			name: "exit 75 that timed out is decided as before",
			view: View{Epic: decideEpic, Task: store.EpicTask{Seq: 3, Build: green, Evidence: item},
				Repro: repro(0, store.EnvironmentExit, true)},
			want: startGreen(0),
		},
	})
}

// envRunID returns the derived id of the evidence run of phase for attempt and rerun of task 1 of passEpic.
func envRunID(phase string, attempt, rerun int64) string {
	return RunID(passEpic, 1, phase, attempt, rerun)
}

// addEnvironmentCapture stores evidence run id of phase at revision as a capture that exited store.EnvironmentExit. It is
// added first when the runner did not start it, and ends uncaptured and unjudged, as the engine leaves it.
func (f *passFixture) addEnvironmentCapture(id, phase, revision string) {
	f.t.Helper()
	at := passClock()
	_, err := f.store.EvidenceRun(f.ctx, id)
	switch {
	case errors.Is(err, store.ErrNoEvidenceRun):
		if err = f.store.AddEvidenceRun(f.ctx, store.EvidenceRun{
			ID: id, Epic: passEpic, Item: "T1", Revision: revision, Phase: phase, Source: evidence.SourceCaptured,
			Spec: f.task().Evidence, Created: at,
		}); err != nil {
			f.t.Fatalf("AddEvidenceRun: %v", err)
		}
	case err != nil:
		f.t.Fatalf("EvidenceRun: %v", err)
	}
	if err := f.store.RecordEvidenceCapture(f.ctx, id, store.EvidenceCapture{
		Exit: store.EnvironmentExit, NotCaptured: store.NotCapturedEnvironment, At: at,
	}); err != nil {
		f.t.Fatalf("RecordEvidenceCapture: %v", err)
	}
}

// TestPassRedEnvironmentExitRerunsUnderNewID covers R2 for the red capture through a pass: an exit 75 of the red run
// moves the build to its next rerun, and the pass starts the red run under that rerun's own id.
func TestPassRedEnvironmentExitRerunsUnderNewID(t *testing.T) {
	t.Parallel()
	f := newPassFixture(t)
	f.pass()
	f.addEnvironmentCapture(envRunID(StageRed, 0, 0), evidence.PhaseRed, f.base)
	f.pass()
	build := f.task().Build
	if build.Stage != StageRed || build.Rerun != 1 {
		t.Fatalf("build = %+v, want red at rerun 1", build)
	}
	want := passRun{id: envRunID(StageRed, 0, 1), epic: passEpic, seq: 1, revision: f.base, phase: evidence.PhaseRed}
	if got := f.lastRun(); got != want {
		t.Errorf("last evidence run = %+v, want %+v", got, want)
	}
}

// TestPassRedEnvironmentExitPassOnRerunGoesOn covers R2 for the red capture through passes: a rerun that holds at the
// base moves the build to implement, as a red run that did not exit 75 does.
func TestPassRedEnvironmentExitPassOnRerunGoesOn(t *testing.T) {
	t.Parallel()
	f := newPassFixture(t)
	f.pass()
	f.addEnvironmentCapture(envRunID(StageRed, 0, 0), evidence.PhaseRed, f.base)
	f.pass()
	f.addRun(envRunID(StageRed, 0, 1), evidence.PhaseRed, f.base, 1, evidence.VerdictFails)
	f.pass()
	build := f.task().Build
	if build.Stage != StageImplement || build.Attempt != 1 || build.RedRun != envRunID(StageRed, 0, 1) {
		t.Errorf("build = %+v, want implement at attempt 1 with the rerun as its red run", build)
	}
}

// TestPassRedEnvironmentExitParksAfterThreeRuns covers R2 for the red capture across a restart: the count of runs that
// exited 75 is read from the stored runs, so a runner that starts afresh parks the build at the third run in a row.
func TestPassRedEnvironmentExitParksAfterThreeRuns(t *testing.T) {
	t.Parallel()
	f := newPassFixture(t)
	f.setBuild(store.TaskBuild{Stage: StageRed, Base: f.base, Rerun: 2})
	for rerun := range int64(3) {
		f.addEnvironmentCapture(envRunID(StageRed, 0, rerun), evidence.PhaseRed, f.base)
	}
	restarted := New(Config{
		Store: f.store, Steps: f.steps, Evidence: f.evidence, Getenv: f.getenv, StateDir: f.stateDir, Now: passClock,
	})
	restarted.pass(f.ctx)
	build := f.task().Build
	if build.Held != HeldParked || build.Rerun != 2 {
		t.Errorf("build = %+v, want parked at rerun 2", build)
	}
	if len(f.evidence.runs) != 0 {
		t.Errorf("evidence runs started = %d, want none once parked", len(f.evidence.runs))
	}
	if parkedFor := f.epic().Parked; !strings.Contains(parkedFor, "red capture") ||
		!strings.Contains(parkedFor, "environment") {
		t.Errorf("epic parked for %q, want a reason that names the red capture and says environment", parkedFor)
	}
}

// TestPassGreenEnvironmentExitRerunsAndPassGoesOn covers R2 for the green capture through passes: an exit 75 reruns the
// green run under its own id, and a passing rerun goes on to review.
func TestPassGreenEnvironmentExitRerunsAndPassGoesOn(t *testing.T) {
	t.Parallel()
	f := newPassFixture(t, "review")
	f.setBuild(store.TaskBuild{Stage: StageGreen, Base: f.base, Attempt: 1, Revision: f.base})
	f.addEnvironmentCapture(envRunID(StageGreen, 1, 0), evidence.PhaseGreen, f.base)
	f.pass()
	want := passRun{
		id: envRunID(StageGreen, 1, 1), epic: passEpic, seq: 1, revision: f.base, phase: evidence.PhaseGreen,
	}
	if got := f.lastRun(); got != want {
		t.Fatalf("last evidence run = %+v, want the green rerun %+v", got, want)
	}
	f.addRun(envRunID(StageGreen, 1, 1), evidence.PhaseGreen, f.base, 0, evidence.VerdictHolds)
	f.pass()
	build := f.task().Build
	if build.Stage != StageReview || build.GreenRun != envRunID(StageGreen, 1, 1) {
		t.Errorf("build = %+v, want review with the rerun as the green run", build)
	}
}

// endLastReproduction ends the reproduction check the runner started last with exit, as the engine does when the capture
// exits. It fails the test when the runner started no reproduction.
func endLastReproduction(t *testing.T, f *passFixture, checks *reproChecks, exit int) {
	t.Helper()
	if len(checks.runs) == 0 {
		t.Fatal("no reproduction check was started")
	}
	id := checks.runs[len(checks.runs)-1].ID
	if err := f.store.EndNodeCheck(f.ctx, id, store.NodeCheckEnd{Exit: exit}, passClock()); err != nil {
		t.Fatalf("EndNodeCheck: %v", err)
	}
}

// TestGreenReproductionEnvironmentExitRerunsUnderNewID covers R2 for the reproduction through passes: an exit 75 reruns
// the unit, the rerun's reproduction runs under its own id, and its exit 1 starts the green run on that rerun.
func TestGreenReproductionEnvironmentExitRerunsUnderNewID(t *testing.T) {
	t.Parallel()
	f, checks := newReproFixture(t)
	commit := toReproGreen(f)
	endLastReproduction(t, f, checks, store.EnvironmentExit)
	f.pass()
	if build := f.task().Build; build.Stage != StageGreen || build.Rerun != 1 {
		t.Fatalf("build = %+v, want green at rerun 1", build)
	}
	if got, want := checks.runs[len(checks.runs)-1].ID, RunID(passEpic, 1, unitRepro, 1, 1); got != want {
		t.Fatalf("reproduction rerun id = %s, want %s", got, want)
	}
	endLastReproduction(t, f, checks, 1)
	f.pass()
	want := passRun{
		id: RunID(passEpic, 1, StageGreen, 1, 1), epic: passEpic, seq: 1, revision: commit, phase: StageGreen,
	}
	if got := f.lastRun(); got != want {
		t.Errorf("last evidence run = %+v, want the green run on the rerun %+v", got, want)
	}
}

// TestGreenReproductionEnvironmentExitParksAfterThreeRuns covers R2 for the reproduction through passes: three exits 75
// in a row park the build, with a reason that names the reproduction's capture and says environment.
func TestGreenReproductionEnvironmentExitParksAfterThreeRuns(t *testing.T) {
	t.Parallel()
	f, checks := newReproFixture(t)
	toReproGreen(f)
	for range store.EnvironmentReruns + 1 {
		endLastReproduction(t, f, checks, store.EnvironmentExit)
		f.pass()
	}
	if build := f.task().Build; build.Held != HeldParked {
		t.Fatalf("build = %+v, want parked after three exits 75", build)
	}
	if got, want := len(checks.runs), store.EnvironmentReruns+1; got != want {
		t.Errorf("reproduction checks started = %d, want %d", got, want)
	}
	if parkedFor := f.epic().Parked; !strings.Contains(parkedFor, "reproduction") ||
		!strings.Contains(parkedFor, "environment") {
		t.Errorf("epic parked for %q, want a reason that names the reproduction and says environment", parkedFor)
	}
}
