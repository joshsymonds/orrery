package enginehost

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// evidenceReadoptRun is the id of the run the granted test readopts.
const evidenceReadoptRun = "granted-run"

// evidenceReadoptScript is a capture that appends a line to $STARTED each time it runs, waits until $RELEASE exists, then
// prints done and exits 3. A readopt that ran it again would append a second line.
const evidenceReadoptScript = `echo run >> "$STARTED"; while [ ! -e "$RELEASE" ]; do sleep 0.05; done; echo done; exit 3`

// evidenceReadoptAdd stores run id of epic evidenceRunEpic in state capturing, at revision and phase, with item as its spec.
func evidenceReadoptAdd(t *testing.T, st *store.Store, id, revision, phase string, item evidence.Item) {
	t.Helper()
	if err := st.AddEvidenceRun(t.Context(), store.EvidenceRun{
		ID: id, Epic: evidenceRunEpic, Item: "R1", Revision: revision, Phase: phase, Source: evidenceRunSource(item),
		Spec: item, Created: time.Unix(1700000000, 0),
	}); err != nil {
		t.Fatalf("AddEvidenceRun %s: %v", id, err)
	}
}

// evidenceReadoptJudging stores run id of epic evidenceRunEpic in phase green, at revision, in state judging after a capture
// that exits 0 and keeps nothing. Its judge step is judge, or none when judge is "".
func evidenceReadoptJudging(t *testing.T, st *store.Store, id, revision string, item evidence.Item, judge string) {
	t.Helper()
	evidenceReadoptAdd(t, st, id, revision, evidence.PhaseGreen, item)
	capture := store.EvidenceCapture{At: time.Unix(1700000001, 0)}
	if err := st.RecordEvidenceCapture(t.Context(), id, capture); err != nil {
		t.Fatalf("RecordEvidenceCapture %s: %v", id, err)
	}
	if judge == "" {
		return
	}
	if err := st.SetEvidenceJudge(t.Context(), id, judge); err != nil {
		t.Fatalf("SetEvidenceJudge %s: %v", id, err)
	}
}

// evidenceReadoptNew returns an Evidence over st and steps with the scratch state evidenceRunNew gives it. Unlike evidenceRunNew
// it registers no cleanup, so the test closes it with evidenceReadoptClose, and a drive it cannot close is not waited for at
// cleanup.
func evidenceReadoptNew(t *testing.T, st *store.Store, steps EvidenceSteps) *Evidence {
	t.Helper()
	home := t.TempDir()
	stepGateWriteFile(t, filepath.Join(home, "orrery", "pools.toml"), stepGatePoolsText("gate"))
	return NewEvidence(EvidenceConfig{
		Store: st, Steps: steps, Pools: &fakeGatePools{}, Environ: gateEnv("XDG_CONFIG_HOME=" + home),
		StateDir: t.TempDir(), GateStateDir: gateShortStateDir(t),
	})
}

// evidenceReadoptClose closes ev and returns once Close has. A drive Close does not know of would keep it waiting until
// the test binary's timeout.
func evidenceReadoptClose(t *testing.T, ev *Evidence) {
	t.Helper()
	closed := make(chan struct{})
	go func() {
		ev.Close()
		close(closed)
	}()
	<-closed
}

// evidenceReadoptStep stores a judge step with id at repo and revision, with no labels: a restart finds it by its derived id
// alone, whatever labels it carries.
func evidenceReadoptStep(t *testing.T, st *store.Store, step, repo, revision string) {
	t.Helper()
	ops := []store.EngineOp{store.StepAdd{
		ID: step, Prompt: "judge", DoneValue: stepLoopEndDone, Workspace: repo, Revision: revision, ReadOnly: true,
	}}
	if err := st.ApplyEngine(t.Context(), ops, time.Unix(1700000002, 0)); err != nil {
		t.Fatalf("ApplyEngine step %s: %v", step, err)
	}
}

// evidenceIDJudge is a judge evidenceIDSteps starts. It ends with end, the row it stores. A judge whose start fails stores that
// row and returns its id with startErr, as Steps.Start does when a StartStep fails after its row is written.
type evidenceIDJudge struct {
	end      store.StepRow
	startErr error
}

// evidenceIDSteps is an EvidenceSteps that starts each judge under the id its StepStart carries, as Steps.Start does, and
// records each id a start carries. Judges start in the order judges gives. ended holds the rows Subscribe offers, by id, and may
// be set before the test runs.
type evidenceIDSteps struct {
	mu     sync.Mutex
	judges []evidenceIDJudge
	ids    []string
	ended  map[string]store.StepRow
}

// Start starts the next scripted judge under start.ID. A start with no judge left is an error.
func (f *evidenceIDSteps) Start(_ context.Context, start StepStart) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ids = append(f.ids, start.ID)
	if len(f.judges) == 0 {
		return "", errors.New("evidence run test: a judge started with none left")
	}
	judge := f.judges[0]
	f.judges = f.judges[1:]
	if f.ended == nil {
		f.ended = map[string]store.StepRow{}
	}
	judge.end.ID = start.ID
	f.ended[start.ID] = judge.end
	return start.ID, judge.startErr
}

// Subscribe returns a subscription to the judge with id ids[0], offered its row when the judge has ended. A judge with no row
// is offered nothing, so its subscription waits until ctx ends.
func (f *evidenceIDSteps) Subscribe(_ context.Context, ids []string) (*StepSubscription, error) {
	sub := NewStepSubscription(ids)
	f.mu.Lock()
	row, ended := f.ended[ids[0]]
	f.mu.Unlock()
	if ended {
		sub.Offer(row)
	}
	return sub, nil
}

// startIDs returns the id each start carried, in start order.
func (f *evidenceIDSteps) startIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.ids...)
}

// evidenceReadoptLines returns how many lines the file at path holds, and 0 when there is no file.
func evidenceReadoptLines(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strings.Count(string(data), "\n")
}

// TestEvidenceReadoptCapturingWithNoGrantIsCapturedOnceAndJudged resumes a run whose capture never launched. The capture runs
// once, from the start, and the judge gives the verdict.
func TestEvidenceReadoptCapturingWithNoGrantIsCapturedOnceAndJudged(t *testing.T) {
	t.Parallel()
	counter := filepath.Join(t.TempDir(), "runs")
	repo, head := evidenceRunRepo(t, "echo run >> "+counter+"\nexit 0\n")
	st := epicTestStore(t)
	evidenceRunSeed(t, st, repo, evidenceRunItem(), evidenceRunItem())
	evidenceReadoptAdd(t, st, "fresh", head, evidence.PhaseGreen, evidenceRunItem())
	steps := &evidenceRunSteps{judges: []evidenceRunJudge{evidenceRunHolds()}}
	ev := evidenceRunNew(t, st, steps)

	if err := ev.Resume(t.Context()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	got := evidenceRunSettled(t, engineOf(ev), st, "fresh")
	if got.Verdict != evidence.VerdictHolds || !got.Passed || got.Judges != 1 || steps.startCount() != 1 {
		t.Fatalf("run = verdict %q passed %t judges %d starts %d, want holds, passed, one judge, one start",
			got.Verdict, got.Passed, got.Judges, steps.startCount())
	}
	if n := evidenceReadoptLines(t, counter); n != 1 {
		t.Fatalf("capture ran %d times, want once", n)
	}
}

// TestEvidenceReadoptCapturingWithNoGrantClearsTheWorktreeAStopLeft resumes a run with no grant whose worktree a start left at
// its path before the grant. The stale worktree is cleared, the capture runs once from the start, and the judge gives the
// verdict.
func TestEvidenceReadoptCapturingWithNoGrantClearsTheWorktreeAStopLeft(t *testing.T) {
	t.Parallel()
	counter := filepath.Join(t.TempDir(), "runs")
	repo, head := evidenceRunRepo(t, "echo run >> "+counter+"\nexit 0\n")
	st := epicTestStore(t)
	evidenceRunSeed(t, st, repo, evidenceRunItem(), evidenceRunItem())
	evidenceReadoptAdd(t, st, "left", head, evidence.PhaseGreen, evidenceRunItem())
	steps := &evidenceRunSteps{judges: []evidenceRunJudge{evidenceRunHolds()}}
	ev := evidenceRunNew(t, st, steps)
	left := filepath.Join(ev.captures.worktrees, "left")
	if err := addStepWorktree(t.Context(), repo, left, head); err != nil {
		t.Fatalf("add the worktree a stop left: %v", err)
	}

	if err := ev.Resume(t.Context()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	got := evidenceRunSettled(t, engineOf(ev), st, "left")
	if got.NotCaptured != "" || got.Verdict != evidence.VerdictHolds || !got.Passed || steps.startCount() != 1 {
		t.Fatalf("run = not captured %q verdict %q passed %t starts %d, want captured, holds, passed, one start",
			got.NotCaptured, got.Verdict, got.Passed, steps.startCount())
	}
	if n := evidenceReadoptLines(t, counter); n != 1 {
		t.Fatalf("capture ran %d times, want once", n)
	}
}

// TestEvidenceReadoptCapturingWithNoGrantClearsAHalfMadeWorktree resumes a run with no grant whose start stopped inside git
// worktree add, leaving checked out files at its path and no .git entry. The directory is cleared, the capture runs once
// from the start, and the judge gives the verdict.
func TestEvidenceReadoptCapturingWithNoGrantClearsAHalfMadeWorktree(t *testing.T) {
	t.Parallel()
	counter := filepath.Join(t.TempDir(), "runs")
	repo, head := evidenceRunRepo(t, "echo run >> "+counter+"\nexit 0\n")
	st := epicTestStore(t)
	evidenceRunSeed(t, st, repo, evidenceRunItem(), evidenceRunItem())
	evidenceReadoptAdd(t, st, "half", head, evidence.PhaseGreen, evidenceRunItem())
	steps := &evidenceRunSteps{judges: []evidenceRunJudge{evidenceRunHolds()}}
	ev := evidenceRunNew(t, st, steps)
	stepWorktreeTestHalfMade(t, filepath.Join(ev.captures.worktrees, "half"))

	if err := ev.Resume(t.Context()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	got := evidenceRunSettled(t, engineOf(ev), st, "half")
	if got.NotCaptured != "" || got.Verdict != evidence.VerdictHolds || !got.Passed || steps.startCount() != 1 {
		t.Fatalf("run = not captured %q verdict %q passed %t starts %d, want captured, holds, passed, one start",
			got.NotCaptured, got.Verdict, got.Passed, steps.startCount())
	}
	if n := evidenceReadoptLines(t, counter); n != 1 {
		t.Fatalf("capture ran %d times, want once", n)
	}
}

// TestEvidenceReadoptCapturingWithAGrantReadsTheHoldersExitAndIsNotRunAgain detaches a capture and lets its gate end under
// its holder, then stores the grant the run held. Resume reads the holder's exit record, records the capture and judges it,
// and the gate runs once.
func TestEvidenceReadoptCapturingWithAGrantReadsTheHoldersExitAndIsNotRunAgain(t *testing.T) {
	t.Parallel()
	repo, head := evidenceCaptureRepo(t)
	release := filepath.Join(t.TempDir(), "release")
	startedFile := filepath.Join(t.TempDir(), "started")
	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0o600) })
	pools := &fakeGatePools{}
	c := evidenceCaptureFor(t, pools, "STARTED="+startedFile, "RELEASE="+release)
	item := evidence.Item{
		Capture: []string{"/bin/sh", "-c", evidenceReadoptScript}, Limit: 60,
		Conclusion: "the build passes", Judge: "reviewer",
	}

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
		_, runErr = c.run(ctx, repo, head, evidenceReadoptRun, item, started)
	}()
	gateWaitFile(t, startedFile, done)
	cancel(errGateDetach)
	evidenceCaptureWait(t, done)
	if !errors.Is(runErr, errGateDetach) {
		t.Fatalf("detached run = %v, want errGateDetach", runErr)
	}
	grant := <-granted
	// A restart removes the worktree directory, which readopt re-adds.
	if err := os.RemoveAll(filepath.Join(c.worktrees, evidenceReadoptRun)); err != nil {
		t.Fatalf("remove the worktree directory: %v", err)
	}
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatalf("release the gate: %v", err)
	}
	holderDir := filepath.Join(c.gateStateDir, "holders", "evidence-"+evidenceReadoptRun)
	gateWaitExit(t, holderDir)
	gateWaitLockFree(t, holderDir)

	st := epicTestStore(t)
	evidenceRunSeed(t, st, repo, item, item)
	evidenceReadoptAdd(t, st, evidenceReadoptRun, head, evidence.PhaseRed, item)
	if err := st.SetEvidenceGrant(t.Context(), evidenceReadoptRun, grant, holderDir); err != nil {
		t.Fatalf("SetEvidenceGrant: %v", err)
	}
	steps := &evidenceRunSteps{judges: []evidenceRunJudge{evidenceRunHolds()}}
	ev := evidenceRunNew(t, st, steps)
	ev.captures = c
	ev.blobs = c.blobs

	if err := ev.Resume(t.Context()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	got := evidenceRunSettled(t, engineOf(ev), st, evidenceReadoptRun)
	if got.NotCaptured != "" || got.Exit != 3 || got.LogHash == "" || got.Verdict != evidence.VerdictHolds ||
		got.Passed || got.Judges != 1 || steps.startCount() != 1 {
		t.Fatalf(
			"run = not captured %q exit %d log %q verdict %q passed %t judges %d starts %d, want captured exit 3 with a log, holds, not passed, one judge",
			got.NotCaptured,
			got.Exit,
			got.LogHash,
			got.Verdict,
			got.Passed,
			got.Judges,
			steps.startCount(),
		)
	}
	if n := evidenceReadoptLines(t, startedFile); n != 1 {
		t.Fatalf("gate ran %d times, want once", n)
	}
}

// TestEvidenceReadoptJudgingRunWhoseStepEndsHoldsRecordsOnce resumes a judging run whose stored judge step ends holds. The
// verdict is recorded, and a second Resume finds nothing open, so it starts nothing and records nothing more.
func TestEvidenceReadoptJudgingRunWhoseStepEndsHoldsRecordsOnce(t *testing.T) {
	t.Parallel()
	repo, head := evidenceRunRepo(t, "exit 0\n")
	st := epicTestStore(t)
	evidenceRunSeed(t, st, repo, evidenceRunItem(), evidenceRunItem())
	evidenceReadoptJudging(t, st, "judged", head, evidenceRunItem(), "judge-stored")
	steps := &evidenceRunSteps{ended: map[string]store.StepRow{
		"judge-stored": {ID: "judge-stored", End: stepLoopEndDone},
	}}
	ev := evidenceRunNew(t, st, steps)

	if err := ev.Resume(t.Context()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	got := evidenceRunSettled(t, engineOf(ev), st, "judged")
	if got.Verdict != evidence.VerdictHolds || !got.Passed || got.Judges != 1 || steps.startCount() != 0 {
		t.Fatalf("run = verdict %q passed %t judges %d starts %d, want holds, passed, one judge, no start",
			got.Verdict, got.Passed, got.Judges, steps.startCount())
	}
	if err := ev.Resume(t.Context()); err != nil {
		t.Fatalf("second Resume: %v", err)
	}
	ev.Close()
	again, err := st.EvidenceRun(t.Context(), "judged")
	if err != nil {
		t.Fatalf("read evidence run after the second Resume: %v", err)
	}
	if !reflect.DeepEqual(again, got) || steps.startCount() != 0 {
		t.Fatalf("run after the second Resume = %+v starts %d, want %+v and no start", again, steps.startCount(), got)
	}
}

// TestEvidenceReadoptJudgingRunWithNoStoredStepAdoptsItsDerivedStep resumes a judging run with no judge step stored, whose first
// judge a restart found started under its derived id and never stored. The step is adopted and waited for, and no judge starts.
func TestEvidenceReadoptJudgingRunWithNoStoredStepAdoptsItsDerivedStep(t *testing.T) {
	t.Parallel()
	repo, head := evidenceRunRepo(t, "exit 0\n")
	st := epicTestStore(t)
	evidenceRunSeed(t, st, repo, evidenceRunItem(), evidenceRunItem())
	evidenceReadoptJudging(t, st, "adopt", head, evidenceRunItem(), "")
	const first = "dc467cbb72e3c9f4dcea18893c6bf3bb"
	evidenceReadoptStep(t, st, first, repo, head)
	steps := &evidenceRunSteps{ended: map[string]store.StepRow{
		first: {ID: first, End: stepLoopEndDone},
	}}
	ev := evidenceRunNew(t, st, steps)

	if err := ev.Resume(t.Context()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	got := evidenceRunSettled(t, engineOf(ev), st, "adopt")
	if got.JudgeStep != first || got.Verdict != evidence.VerdictHolds || !got.Passed || got.Judges != 1 ||
		steps.startCount() != 0 {
		t.Fatalf(
			"run = judge %q verdict %q passed %t judges %d starts %d, want the derived step, holds, passed, one judge, no start",
			got.JudgeStep,
			got.Verdict,
			got.Passed,
			got.Judges,
			steps.startCount(),
		)
	}
}

// TestEvidenceReadoptJudgingRunAdoptsANewerJudgeThanItsStoredOne resumes a judging run whose stored judge ended with no
// verdict while a newer judge, started before a kill and never stored, still runs. The newer judge is adopted, waited for
// and counted, and no judge starts beside it.
func TestEvidenceReadoptJudgingRunAdoptsANewerJudgeThanItsStoredOne(t *testing.T) {
	t.Parallel()
	repo, head := evidenceRunRepo(t, "exit 0\n")
	st := epicTestStore(t)
	evidenceRunSeed(t, st, repo, evidenceRunItem(), evidenceRunItem())
	const first = "785122470b5ca51872b9ce3eefbd21e1"
	const second = "da53b42727c0909d009e5828941ba009"
	evidenceReadoptJudging(t, st, "newer", head, evidenceRunItem(), first)
	evidenceReadoptStep(t, st, first, repo, head)
	ended := []store.EngineOp{store.StepEnd{ID: first, End: stepRestartedEnd}}
	if err := st.ApplyEngine(t.Context(), ended, time.Unix(1700000002, 0)); err != nil {
		t.Fatalf("ApplyEngine end of the stored judge: %v", err)
	}
	evidenceReadoptStep(t, st, second, repo, head)
	steps := &evidenceRunSteps{ended: map[string]store.StepRow{
		first:  {ID: first, End: stepRestartedEnd},
		second: {ID: second, End: stepLoopEndDone},
	}}
	ev := evidenceRunNew(t, st, steps)

	if err := ev.Resume(t.Context()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	got := evidenceRunSettled(t, engineOf(ev), st, "newer")
	if got.JudgeStep != second || got.Verdict != evidence.VerdictHolds || !got.Passed || got.Judges != 2 ||
		steps.startCount() != 0 {
		t.Fatalf(
			"run = judge %q verdict %q passed %t judges %d starts %d, want the newer derived step, holds, passed, two judges, no start",
			got.JudgeStep,
			got.Verdict,
			got.Passed,
			got.Judges,
			steps.startCount(),
		)
	}
}

// TestEvidenceReadoptJudgeStartsCarryTheDerivedIDOfEachJudge resumes a run whose capture never launched. Its first judge ends
// with no verdict, so its rejudge follows. Each start carries the derived id of its judge number: 1 for the first judge and 2
// for the rejudge, and the rejudge's verdict is the one recorded.
func TestEvidenceReadoptJudgeStartsCarryTheDerivedIDOfEachJudge(t *testing.T) {
	t.Parallel()
	repo, head := evidenceRunRepo(t, "exit 0\n")
	st := epicTestStore(t)
	evidenceRunSeed(t, st, repo, evidenceRunItem(), evidenceRunItem())
	evidenceReadoptAdd(t, st, "fresh", head, evidence.PhaseGreen, evidenceRunItem())
	steps := &evidenceIDSteps{judges: []evidenceIDJudge{
		{end: store.StepRow{End: stepRestartedEnd}},
		{end: store.StepRow{End: stepLoopEndDone}},
	}}
	ev := evidenceRunNew(t, st, steps)

	if err := ev.Resume(t.Context()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	got := evidenceRunSettled(t, engineOf(ev), st, "fresh")
	first, second := "61b04f6e1d4b02210ada8ecd11226496", "0580241a3520094d961bf03184e0fcf2"
	if ids := steps.startIDs(); !reflect.DeepEqual(ids, []string{first, second}) {
		t.Fatalf("judge start ids = %q, want the derived ids of judges 1 and 2 %q", ids, []string{first, second})
	}
	if got.JudgeStep != second || got.Verdict != evidence.VerdictHolds || !got.Passed || got.Judges != 2 {
		t.Fatalf("run = judge %q verdict %q passed %t judges %d, want judge 2, holds, passed, two judges",
			got.JudgeStep, got.Verdict, got.Passed, got.Judges)
	}
}

// TestEvidenceReadoptJudgingRunRejudgesUnderTheNextDerivedID resumes a judging run whose stored judge, judge 1, ended with no
// verdict. The rejudge starts under the derived id of judge 2, and its verdict is recorded.
func TestEvidenceReadoptJudgingRunRejudgesUnderTheNextDerivedID(t *testing.T) {
	t.Parallel()
	repo, head := evidenceRunRepo(t, "exit 0\n")
	st := epicTestStore(t)
	evidenceRunSeed(t, st, repo, evidenceRunItem(), evidenceRunItem())
	const first = "5bd81b62ae3a68826ef5872b4d8f41a4"
	const second = "d3ff02b2d6758b2f093c7e67e2be04d2"
	evidenceReadoptJudging(t, st, "rejudged", head, evidenceRunItem(), first)
	steps := &evidenceIDSteps{
		judges: []evidenceIDJudge{{end: store.StepRow{End: stepLoopEndDone}}},
		ended:  map[string]store.StepRow{first: {ID: first, End: stepRestartedEnd}},
	}
	ev := evidenceReadoptNew(t, st, steps)

	if err := ev.Resume(t.Context()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	got := evidenceRunSettled(t, engineOf(ev), st, "rejudged")
	if ids := steps.startIDs(); !reflect.DeepEqual(ids, []string{second}) {
		t.Fatalf("judge start ids = %q, want the derived id of judge 2 %q", ids, second)
	}
	if got.JudgeStep != second || got.Verdict != evidence.VerdictHolds || !got.Passed || got.Judges != 2 {
		t.Fatalf("run = judge %q verdict %q passed %t judges %d, want judge 2, holds, passed, two judges",
			got.JudgeStep, got.Verdict, got.Passed, got.Judges)
	}
}

// TestEvidenceReadoptFailedJudgeStartIsCountedAndRetriedUnderTheNextID resumes a run whose first judge's start fails after its
// row is written, as a StartStep failure leaves it: the row ends failed and Start returns its id with the error. That judge is
// counted, so its failure gives no verdict and the rejudge starts under the derived id of judge 2 and holds.
func TestEvidenceReadoptFailedJudgeStartIsCountedAndRetriedUnderTheNextID(t *testing.T) {
	t.Parallel()
	repo, head := evidenceRunRepo(t, "exit 0\n")
	st := epicTestStore(t)
	evidenceRunSeed(t, st, repo, evidenceRunItem(), evidenceRunItem())
	evidenceReadoptAdd(t, st, "failed", head, evidence.PhaseGreen, evidenceRunItem())
	steps := &evidenceIDSteps{judges: []evidenceIDJudge{
		{
			end:      store.StepRow{End: stepLoopEndFailed, Reason: "spawn failed"},
			startErr: errors.New("start step: spawn failed"),
		},
		{end: store.StepRow{End: stepLoopEndDone}},
	}}
	ev := evidenceRunNew(t, st, steps)

	if err := ev.Resume(t.Context()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	got := evidenceRunSettled(t, engineOf(ev), st, "failed")
	first, second := "216b3db2193649102c12c582b1d0fd61", "1e2d554126c2140aeb63d4b88707e4d5"
	if ids := steps.startIDs(); !reflect.DeepEqual(ids, []string{first, second}) {
		t.Fatalf("judge start ids = %q, want the derived ids of judges 1 and 2 %q", ids, []string{first, second})
	}
	if got.JudgeStep != second || got.Verdict != evidence.VerdictHolds || !got.Passed || got.Judges != 2 {
		t.Fatalf("run = judge %q verdict %q passed %t judges %d, want judge 2, holds, passed, two judges",
			got.JudgeStep, got.Verdict, got.Passed, got.Judges)
	}
}

// TestEvidenceReadoptJudgingRunWhoseStepEndedEngineRestartedIsRejudged resumes a judging run whose judge step ended
// engine_restarted, which gives no verdict. A new judge starts on the same kept output and its verdict is recorded.
func TestEvidenceReadoptJudgingRunWhoseStepEndedEngineRestartedIsRejudged(t *testing.T) {
	t.Parallel()
	repo, head := evidenceRunRepo(t, "exit 0\n")
	st := epicTestStore(t)
	evidenceRunSeed(t, st, repo, evidenceRunItem(), evidenceRunItem())
	evidenceReadoptJudging(t, st, "restarted", head, evidenceRunItem(), "judge-old")
	steps := &evidenceRunSteps{
		judges: []evidenceRunJudge{evidenceRunHolds()},
		ended:  map[string]store.StepRow{"judge-old": {ID: "judge-old", End: stepRestartedEnd}},
	}
	ev := evidenceRunNew(t, st, steps)

	if err := ev.Resume(t.Context()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	got := evidenceRunSettled(t, engineOf(ev), st, "restarted")
	if got.Verdict != evidence.VerdictHolds || !got.Passed || got.Judges != 2 || steps.startCount() != 1 {
		t.Fatalf("run = verdict %q passed %t judges %d starts %d, want holds, passed, two judges, one start",
			got.Verdict, got.Passed, got.Judges, steps.startCount())
	}
}

// TestEvidenceReadoptResumeDrivesARunOnce resumes a judging run twice while its judge hangs. The second Resume finds the run
// already under way and starts nothing. Close cancels the drive and waits for it, and the run keeps its judge and no verdict.
func TestEvidenceReadoptResumeDrivesARunOnce(t *testing.T) {
	t.Parallel()
	repo, head := evidenceRunRepo(t, "exit 0\n")
	st := epicTestStore(t)
	evidenceRunSeed(t, st, repo, evidenceRunItem(), evidenceRunItem())
	evidenceReadoptJudging(t, st, "driven", head, evidenceRunItem(), "")
	steps := &evidenceRunSteps{judges: []evidenceRunJudge{{hang: true}}}
	ev := evidenceReadoptNew(t, st, steps)

	if err := ev.Resume(t.Context()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	evidenceRunEventually(t, engineOf(ev), "the judge step is recorded", func() bool {
		run, err := st.EvidenceRun(t.Context(), "driven")
		if err != nil {
			t.Fatalf("read evidence run: %v", err)
		}
		return run.JudgeStep != ""
	})
	if err := ev.Resume(t.Context()); err != nil {
		t.Fatalf("second Resume: %v", err)
	}
	evidenceReadoptClose(t, ev)
	got, err := st.EvidenceRun(t.Context(), "driven")
	if err != nil {
		t.Fatalf("read evidence run after Close: %v", err)
	}
	if got.State != "judging" || got.Verdict != "" || got.Judges != 1 || steps.startCount() != 1 {
		t.Fatalf(
			"run after Close = state %q verdict %q judges %d starts %d, want judging, no verdict, one judge, one start",
			got.State,
			got.Verdict,
			got.Judges,
			steps.startCount(),
		)
	}
}

// TestEvidenceReadoptGateRebuildsTheJudgeGateOfAJudgeRow asks EvidenceReadoptGate for the gate of a judge row, which is the
// judge gate of the run's stored item, for a planner row, which has none, and for a judge row whose run is not stored.
func TestEvidenceReadoptGateRebuildsTheJudgeGateOfAJudgeRow(t *testing.T) {
	t.Parallel()
	repo, head := evidenceRunRepo(t, "exit 0\n")
	st := epicTestStore(t)
	spec := evidenceRunItem("small.txt")
	evidenceRunSeed(t, st, repo, spec, spec)
	evidenceReadoptAdd(t, st, "gated", head, evidence.PhaseGreen, spec)
	readopt := EvidenceReadoptGate(st)

	judge := store.StepRow{ID: "judge-1", GateName: evidenceJudgeGate, Task: "evidence-gated"}
	got, err := readopt(t.Context(), judge)
	if err != nil || got == nil {
		t.Fatalf("judge row gate = %v, error %v, want the judge gate", got, err)
	}
	if want := judgeGate(spec); !reflect.DeepEqual(*got, want) {
		t.Fatalf("judge row gate = %+v, want %+v", *got, want)
	}

	for _, row := range []store.StepRow{
		{ID: "plan-1", Task: "T1"},
		{ID: "check-1", GateName: "check", Task: "evidence-gated"},
	} {
		gate, rowErr := readopt(t.Context(), row)
		if rowErr != nil || gate != nil {
			t.Fatalf("step %s gate = %v, error %v, want no gate and no error", row.ID, gate, rowErr)
		}
	}

	missing := store.StepRow{ID: "judge-2", GateName: evidenceJudgeGate, Task: "evidence-no-such-run"}
	if _, err = readopt(t.Context(), missing); !errors.Is(err, store.ErrNoEvidenceRun) {
		t.Fatalf("judge row of a run not stored = %v, want an error matching ErrNoEvidenceRun", err)
	}
}
