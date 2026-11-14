package enginehost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// evidenceRecordRun is the id of the run the record tests drive.
const evidenceRecordRun = "recorded-run"

// evidenceRecordEndsAtOnceScript is the capture a record test commits as capture.sh. It prints a log line, keeps out.txt and
// exits 0 at once.
const evidenceRecordEndsAtOnceScript = "echo logged\necho kept > out.txt\nexit 0\n"

// evidenceRecordDetachScript is the capture a readopt record test commits as capture.sh. It writes $STARTED, waits until
// $RELEASE exists, then prints a log line, keeps out.txt and exits 0.
const evidenceRecordDetachScript = `echo started > "$STARTED"; while [ ! -e "$RELEASE" ]; do sleep 0.05; done; echo logged; echo kept > out.txt; exit 0`

// evidenceRecordStore wraps an EvidenceStore so that beforeRecord runs just before each capture is recorded. That is after the
// capture has ended and been cleaned up, and before its record is written, which is the window a shutdown can land in.
type evidenceRecordStore struct {
	EvidenceStore
	beforeRecord func()
}

// RecordEvidenceCapture runs beforeRecord, then records the capture through the wrapped store.
func (s evidenceRecordStore) RecordEvidenceCapture(
	ctx context.Context, id string, capture store.EvidenceCapture,
) error {
	s.beforeRecord()
	return s.EvidenceStore.RecordEvidenceCapture(ctx, id, capture)
}

// evidenceRecordNew returns an Evidence over st and steps whose blobs and capture worktrees live under stateDir, with pools
// for its gates. Close runs when the test ends.
func evidenceRecordNew(
	t *testing.T, st EvidenceStore, steps EvidenceSteps, stateDir string, pools PoolCommands,
) *Evidence {
	t.Helper()
	home := t.TempDir()
	stepGateWriteFile(t, filepath.Join(home, "orrery", "pools.toml"), stepGatePoolsText("gate"))
	ev := NewEvidence(EvidenceConfig{
		Store: st, Steps: steps, Pools: pools, Environ: gateEnv("XDG_CONFIG_HOME=" + home),
		StateDir: stateDir, GateStateDir: gateShortStateDir(t),
	})
	t.Cleanup(ev.Close)
	return ev
}

// evidenceRecordStored returns the record run evidenceRecordRun as st stores it now.
func evidenceRecordStored(t *testing.T, st *store.Store) store.EvidenceRun {
	t.Helper()
	run, err := st.EvidenceRun(t.Context(), evidenceRecordRun)
	if err != nil {
		t.Fatalf("read evidence run %s: %v", evidenceRecordRun, err)
	}
	return run
}

// evidenceRecordKeptOut reports whether run kept exactly out.txt, as the record tests' captures leave it.
func evidenceRecordKeptOut(run store.EvidenceRun) bool {
	return len(run.Artifacts) == 1 && run.Artifacts[0].Path == "out.txt" && run.Artifacts[0].State == artifactKept
}

// TestEvidenceCaptureRecordedWhenCtxEndsBeforeItsRecord drives a captured run whose context ends after its capture has ended
// and been cleaned up, and before the capture is recorded, as a shutdown in that window does. The capture is recorded with its
// exit, log and artifact, and the run is left judging with no judge started. A Resume on a fresh Evidence over the same store
// then judges it and records the verdict.
func TestEvidenceCaptureRecordedWhenCtxEndsBeforeItsRecord(t *testing.T) {
	t.Parallel()
	repo, head := evidenceRunRepo(t, evidenceRecordEndsAtOnceScript)
	st := epicTestStore(t)
	item := evidenceRunItem("out.txt")
	evidenceRunSeed(t, st, repo, item, item)
	evidenceReadoptAdd(t, st, evidenceRecordRun, head, evidence.PhaseGreen, item)
	stateDir := t.TempDir()
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	ended := false
	wrapped := evidenceRecordStore{EvidenceStore: st, beforeRecord: func() {
		ended = true
		cancel(errGateDetach)
	}}
	steps := &evidenceRunSteps{}
	ev := evidenceRecordNew(t, wrapped, steps, stateDir, &fakeGatePools{})

	ev.drive(ctx, repo, evidenceRecordStored(t, st))

	got := evidenceRecordStored(t, st)
	if got.State != "judging" || got.Exit != 0 || got.LogHash == "" || !evidenceRecordKeptOut(got) ||
		got.JudgeStep != "" || got.Judges != 0 || steps.startCount() != 0 {
		t.Fatalf(
			"run after drive = state %q exit %d log %q artifacts %+v judge %q judges %d starts %d, "+
				"want judging with exit 0, a log, out.txt kept, and no judge",
			got.State, got.Exit, got.LogHash, got.Artifacts, got.JudgeStep, got.Judges, steps.startCount(),
		)
	}
	if !ended {
		t.Fatal("the record hook did not run, so the context never ended before the record")
	}

	judges := &evidenceRunSteps{judges: []evidenceRunJudge{evidenceRunHolds()}}
	resumed := evidenceRecordNew(t, st, judges, stateDir, &fakeGatePools{})
	if err := resumed.Resume(t.Context()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	judged := evidenceRunSettled(t, st, evidenceRecordRun)
	if judged.Verdict != evidence.VerdictHolds || !judged.Passed || judged.Judges != 1 || judges.startCount() != 1 {
		t.Fatalf(
			"run after Resume = verdict %q passed %t judges %d starts %d, want holds, passed, one judge, one start",
			judged.Verdict, judged.Passed, judged.Judges, judges.startCount(),
		)
	}
}

// TestEvidenceReadoptCaptureRecordedWhenCtxEndsBeforeItsRecord readopts a capture whose gate ended under its holder, and whose
// context ends after the capture has been kept and before it is recorded. The capture is recorded, the run is left judging with
// no judge started, and a Resume on a fresh Evidence judges it and records the verdict.
func TestEvidenceReadoptCaptureRecordedWhenCtxEndsBeforeItsRecord(t *testing.T) {
	t.Parallel()
	repo, head := evidenceRunRepo(t, evidenceRecordDetachScript)
	release := filepath.Join(t.TempDir(), "release")
	startedFile := filepath.Join(t.TempDir(), "started")
	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0o600) })
	pools := &fakeGatePools{}
	c := evidenceCaptureFor(t, pools, "STARTED="+startedFile, "RELEASE="+release)
	item := evidenceRunItem("out.txt")

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
		_, runErr = c.run(ctx, repo, head, evidenceRecordRun, item, started)
	}()
	gateWaitFile(t, startedFile)
	cancel(errGateDetach)
	evidenceCaptureWait(t, done)
	if !errors.Is(runErr, errGateDetach) {
		t.Fatalf("detached run = %v, want errGateDetach", runErr)
	}
	grant := <-granted
	holderDir := filepath.Join(c.gateStateDir, "holders", "evidence-"+evidenceRecordRun)
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatalf("release the gate: %v", err)
	}
	gateWaitExit(t, holderDir)
	gateWaitLockFree(t, holderDir)

	st := epicTestStore(t)
	evidenceRunSeed(t, st, repo, item, item)
	evidenceReadoptAdd(t, st, evidenceRecordRun, head, evidence.PhaseGreen, item)
	if err := st.SetEvidenceGrant(t.Context(), evidenceRecordRun, grant, holderDir); err != nil {
		t.Fatalf("SetEvidenceGrant: %v", err)
	}
	stored := evidenceRecordStored(t, st)
	readoptCtx, cancelReadopt := context.WithCancelCause(t.Context())
	defer cancelReadopt(nil)
	ended := false
	wrapped := evidenceRecordStore{EvidenceStore: st, beforeRecord: func() {
		ended = true
		cancelReadopt(errGateDetach)
	}}
	steps := &evidenceRunSteps{}
	ev := evidenceRecordNew(t, wrapped, steps, t.TempDir(), pools)
	ev.captures = c
	ev.blobs = c.blobs

	ev.readoptCapture(readoptCtx, repo, stored)

	got := evidenceRecordStored(t, st)
	if got.State != "judging" || got.Exit != 0 || got.LogHash == "" || !evidenceRecordKeptOut(got) ||
		got.JudgeStep != "" || got.Judges != 0 || steps.startCount() != 0 {
		t.Fatalf(
			"run after readopt = state %q exit %d log %q artifacts %+v judge %q judges %d starts %d, "+
				"want judging with exit 0, a log, out.txt kept, and no judge",
			got.State, got.Exit, got.LogHash, got.Artifacts, got.JudgeStep, got.Judges, steps.startCount(),
		)
	}
	if !ended {
		t.Fatal("the record hook did not run, so the context never ended before the record")
	}

	judges := &evidenceRunSteps{judges: []evidenceRunJudge{evidenceRunHolds()}}
	resumed := evidenceRecordNew(t, st, judges, t.TempDir(), &fakeGatePools{})
	resumed.blobs = c.blobs
	if err := resumed.Resume(t.Context()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	judged := evidenceRunSettled(t, st, evidenceRecordRun)
	if judged.Verdict != evidence.VerdictHolds || !judged.Passed || judged.Judges != 1 || judges.startCount() != 1 {
		t.Fatalf(
			"run after Resume = verdict %q passed %t judges %d starts %d, want holds, passed, one judge, one start",
			judged.Verdict, judged.Passed, judged.Judges, judges.startCount(),
		)
	}
}

// TestEvidenceCaptureCutShortBeforeItsGateLaunchesIsLeftForARestart drives a captured run whose context ends while its grant
// still queues, before its gate launches. Nothing ran, so nothing is recorded: the run stays capturing with no grant, and a
// Resume on a fresh Evidence runs the capture once and judges it.
func TestEvidenceCaptureCutShortBeforeItsGateLaunchesIsLeftForARestart(t *testing.T) {
	t.Parallel()
	counter := filepath.Join(t.TempDir(), "runs")
	repo, head := evidenceRunRepo(t, "echo run >> "+counter+"\necho logged\nexit 0\n")
	st := epicTestStore(t)
	item := evidenceRunItem()
	evidenceRunSeed(t, st, repo, item, item)
	evidenceReadoptAdd(t, st, evidenceRecordRun, head, evidence.PhaseGreen, item)
	stateDir := t.TempDir()
	stored := evidenceRecordStored(t, st)
	queued := &fakeGatePools{queue: make(chan struct{})}
	ev := evidenceRecordNew(t, st, &evidenceRunSteps{}, stateDir, queued)
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ev.drive(ctx, repo, stored)
	}()
	evidenceRunEventually(t, "the grant is requested", func() bool { return len(queued.requested()) == 1 })
	cancel(errGateDetach)
	evidenceCaptureWait(t, done)

	got := evidenceRecordStored(t, st)
	if got.State != "capturing" || got.Grant != "" || got.NotCaptured != "" {
		t.Fatalf(
			"run after a cut-short drive = state %q grant %q not captured %q, "+
				"want capturing with no grant and nothing recorded",
			got.State, got.Grant, got.NotCaptured,
		)
	}

	judges := &evidenceRunSteps{judges: []evidenceRunJudge{evidenceRunHolds()}}
	resumed := evidenceRecordNew(t, st, judges, stateDir, &fakeGatePools{})
	if err := resumed.Resume(t.Context()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	judged := evidenceRunSettled(t, st, evidenceRecordRun)
	if judged.Verdict != evidence.VerdictHolds || !judged.Passed || judged.Judges != 1 || judges.startCount() != 1 {
		t.Fatalf(
			"run after Resume = verdict %q passed %t judges %d starts %d, want holds, passed, one judge, one start",
			judged.Verdict, judged.Passed, judged.Judges, judges.startCount(),
		)
	}
	if n := evidenceReadoptLines(t, counter); n != 1 {
		t.Fatalf("capture ran %d times, want once", n)
	}
}

// TestEvidenceSuppliedReadCutShortIsLeftForARestart drives a supplied run whose context has ended before its files are read.
// Nothing is read, so nothing is recorded: the run stays capturing, and a Resume reads the files and judges them.
func TestEvidenceSuppliedReadCutShortIsLeftForARestart(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	head := stepGitTestCommit(t, repo, map[string]string{"report.txt": "committed\n"})
	st := epicTestStore(t)
	supplied := evidence.Item{Supplied: []string{"report.txt"}, Conclusion: "the build passes", Judge: "reviewer"}
	evidenceRunSeed(t, st, repo, supplied, supplied)
	evidenceReadoptAdd(t, st, evidenceRecordRun, head, evidence.PhaseRed, supplied)
	stateDir := t.TempDir()
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(errGateDetach)
	ev := evidenceRecordNew(t, st, &evidenceRunSteps{}, stateDir, &fakeGatePools{})

	ev.drive(ctx, repo, evidenceRecordStored(t, st))

	got := evidenceRecordStored(t, st)
	if got.State != "capturing" || got.NotCaptured != "" || len(got.Artifacts) != 0 {
		t.Fatalf(
			"run after a cut-short read = state %q not captured %q artifacts %d, want capturing with nothing recorded",
			got.State, got.NotCaptured, len(got.Artifacts),
		)
	}

	judges := &evidenceRunSteps{judges: []evidenceRunJudge{evidenceRunHolds()}}
	resumed := evidenceRecordNew(t, st, judges, stateDir, &fakeGatePools{})
	if err := resumed.Resume(t.Context()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	judged := evidenceRunSettled(t, st, evidenceRecordRun)
	if judged.NotCaptured != "" || len(judged.Artifacts) != 1 || judged.Artifacts[0].State != artifactKept ||
		judged.Verdict != evidence.VerdictHolds || judges.startCount() != 1 {
		t.Fatalf(
			"run after Resume = not captured %q artifacts %+v verdict %q starts %d, "+
				"want the file kept, holds, and one judge start",
			judged.NotCaptured, judged.Artifacts, judged.Verdict, judges.startCount(),
		)
	}
}
