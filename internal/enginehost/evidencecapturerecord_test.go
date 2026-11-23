package enginehost

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/waittest"
)

// evidenceRecordRun is the id of the run the record tests drive.
const evidenceRecordRun = "recorded-run"

// evidenceRecordStore wraps an EvidenceStore so that beforeRecord runs just before each capture is recorded. That is after a
// supplied read has been kept, and before its record is written, which is the window a shutdown can land in.
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

// evidenceRecordNew returns an Evidence over st and steps whose blobs live under stateDir. Close runs when the test ends.
func evidenceRecordNew(t *testing.T, st EvidenceStore, steps EvidenceSteps, stateDir string) *Evidence {
	t.Helper()
	ev := NewEvidence(EvidenceConfig{Store: st, Steps: steps, StateDir: stateDir})
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

// evidenceRecordKeptOut reports whether run kept exactly out.txt, as the record tests' supplied reads leave it.
func evidenceRecordKeptOut(run store.EvidenceRun) bool {
	return len(run.Artifacts) == 1 && run.Artifacts[0].Path == "out.txt" && run.Artifacts[0].State == artifactKept
}

// TestEvidenceSuppliedReadRecordedWhenCtxEndsBeforeItsRecord drives a supplied run whose context ends after its files are read and
// before the read is recorded, as a shutdown in that window does. The read is recorded with its artifact, and the run is left
// judging with no judge started. A Resume on a fresh Evidence over the same store then judges it and records the verdict.
func TestEvidenceSuppliedReadRecordedWhenCtxEndsBeforeItsRecord(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	head := stepGitTestCommit(t, repo, map[string]string{"out.txt": "kept\n"})
	st := epicTestStore(t)
	supplied := evidence.Item{Supplied: []string{"out.txt"}, Conclusion: "the build passes", Judge: "reviewer"}
	evidenceRunSeed(t, st, repo, supplied, supplied)
	evidenceReadoptAdd(t, st, evidenceRecordRun, head, evidence.PhaseGreen, supplied)
	stateDir := t.TempDir()
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	ended := false
	wrapped := evidenceRecordStore{EvidenceStore: st, beforeRecord: func() {
		ended = true
		cancel(nil)
	}}
	steps := &evidenceRunSteps{}
	ev := evidenceRecordNew(t, wrapped, steps, stateDir)

	ev.drive(ctx, repo, evidenceRecordStored(t, st))

	got := evidenceRecordStored(t, st)
	if got.State != "judging" || got.Exit != 0 || !evidenceRecordKeptOut(got) || got.JudgeStep != "" ||
		got.Judges != 0 ||
		steps.startCount() != 0 {
		t.Fatalf(
			"run after drive = state %q exit %d artifacts %+v judge %q judges %d starts %d, "+
				"want judging with exit 0, out.txt kept, and no judge",
			got.State, got.Exit, got.Artifacts, got.JudgeStep, got.Judges, steps.startCount(),
		)
	}
	if !ended {
		t.Fatal("the record hook did not run, so the context never ended before the record")
	}

	judges := &evidenceRunSteps{judges: []evidenceRunJudge{evidenceRunHolds()}}
	resumed := evidenceRecordNew(t, st, judges, stateDir)
	if err := resumed.Resume(t.Context()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	judged := evidenceRunSettled(t, engineOf(ev, resumed), st, evidenceRecordRun)
	if judged.Verdict != evidence.VerdictHolds || !judged.Passed || judged.Judges != 1 || judges.startCount() != 1 {
		t.Fatalf(
			"run after Resume = verdict %q passed %t judges %d starts %d, want holds, passed, one judge, one start",
			judged.Verdict, judged.Passed, judged.Judges, judges.startCount(),
		)
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
	ev := evidenceRecordNew(t, st, &evidenceRunSteps{}, stateDir)

	ev.drive(ctx, repo, evidenceRecordStored(t, st))

	got := evidenceRecordStored(t, st)
	if got.State != "capturing" || got.NotCaptured != "" || len(got.Artifacts) != 0 {
		t.Fatalf(
			"run after a cut-short read = state %q not captured %q artifacts %d, want capturing with nothing recorded",
			got.State, got.NotCaptured, len(got.Artifacts),
		)
	}

	judges := &evidenceRunSteps{judges: []evidenceRunJudge{evidenceRunHolds()}}
	resumed := evidenceRecordNew(t, st, judges, stateDir)
	if err := resumed.Resume(t.Context()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	judged := evidenceRunSettled(t, engineOf(ev, resumed), st, evidenceRecordRun)
	if judged.NotCaptured != "" || len(judged.Artifacts) != 1 || judged.Artifacts[0].State != artifactKept ||
		judged.Verdict != evidence.VerdictHolds || judges.startCount() != 1 {
		t.Fatalf(
			"run after Resume = not captured %q artifacts %+v verdict %q starts %d, "+
				"want the file kept, holds, and one judge start",
			judged.NotCaptured, judged.Artifacts, judged.Verdict, judges.startCount(),
		)
	}
}

// captureRecordStore wraps a CaptureStore so that beforeRecord runs just before each capture's end is recorded. That is after the
// capture's command has ended and been cleaned up, and before its end is written, which is the window a shutdown can land in.
type captureRecordStore struct {
	CaptureStore
	beforeRecord func()
}

// RecordCaptureEnd runs beforeRecord, then records the end through the wrapped store.
func (s captureRecordStore) RecordCaptureEnd(ctx context.Context, id string, end store.EvidenceCapture) error {
	s.beforeRecord()
	return s.CaptureStore.RecordCaptureEnd(ctx, id, end)
}

// captureRecordNew returns the Captures of rig over st, which may be a wrapper of the rig's store. Close runs when the test ends.
func captureRecordNew(t *testing.T, rig captureTestRig, st CaptureStore) *Captures {
	t.Helper()
	c := NewCaptures(CapturesConfig{
		Store: st, Steps: rig.steps, Deliver: rig.deliveries.deliver, Pools: rig.pools, Environ: rig.environ,
		StateDir: rig.stateDir, GateStateDir: rig.gateRoot, GateScope: rig.scope,
	})
	t.Cleanup(c.Close)
	return c
}

// evidenceRecordCapture stores a capture of argv at commit, asked for by captureTestStep, that answers briefs and keeps the marker
// file, and returns it as stored. The test runs the capture itself, so it decides the context the run ends under.
func evidenceRecordCapture(t *testing.T, st *store.Store, repo, commit string, argv, briefs []string) store.Capture {
	t.Helper()
	const id = "recorded-capture"
	if err := st.AddCapture(t.Context(), store.Capture{
		ID: id, Epic: captureTestEpic, Repository: repo, Step: captureTestStep, Role: captureRoleStep,
		Runner: localRunnerName, Commit: commit, Argv: argv, Files: []string{"marker"}, Briefs: briefs, Limit: 60,
		Created: time.Unix(1700000000, 0),
	}); err != nil {
		t.Fatalf("AddCapture %s: %v", id, err)
	}
	stored, err := st.Capture(t.Context(), id)
	if err != nil {
		t.Fatalf("Capture %s: %v", id, err)
	}
	return stored
}

// TestCaptureEndRecordedWhenCtxEndsBeforeItsRecord runs a capture whose context ends after its command has ended and been cleaned
// up, and before its end is recorded, as a shutdown in that window does. The end is recorded with its exit, log and kept file, and
// the brief it answers is left waiting: a capture never attaches itself, so the gatherer's attach does that.
func TestCaptureEndRecordedWhenCtxEndsBeforeItsRecord(t *testing.T) {
	t.Parallel()
	repo, head := captureTestRepo(t)
	st := epicTestStore(t)
	briefs := captureTestSeed(t, captureTestStore{Store: st}, repo, head, 60)
	rig := captureTestNewRig(t, captureTestStore{Store: st}, "")
	stored := evidenceRecordCapture(t, st, repo, head, evidenceAttachCat(), briefs)
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	ended := false
	c := captureRecordNew(t, rig, captureRecordStore{CaptureStore: captureTestStore{Store: st}, beforeRecord: func() {
		ended = true
		cancel(nil)
	}})

	c.runFresh(ctx, repo, stored, captureItem(stored, nil))

	if !ended {
		t.Fatal("the record hook did not run, so the context never ended before the record")
	}
	got := captureTestStored(t, rig.st, stored.ID)
	if got.State != "ended" || got.Exit != 0 || got.LogHash == "" || len(got.Artifacts) != 1 ||
		got.Artifacts[0].Path != "marker" || got.Artifacts[0].State != artifactKept {
		t.Fatalf(
			"capture after a cut-short record = state %q exit %d log %q artifacts %+v, "+
				"want ended with exit 0, a log, and marker kept",
			got.State, got.Exit, got.LogHash, got.Artifacts,
		)
	}
	run := evidenceRecordBrief(t, st, briefs[0])
	if run.State != store.RunWaiting || run.Capture != "" {
		t.Fatalf("brief %s after the capture ended = state %q capture %q, want waiting with no capture",
			briefs[0], run.State, run.Capture)
	}
}

// TestCaptureCutShortBeforeItsGateLaunchesIsLeftForARestart closes a Captures whose capture's grant still queues, before its gate
// launches. Nothing ran, so nothing is recorded: the capture stays running with no grant, and a Resume on a fresh Captures runs
// its command once and records its end.
func TestCaptureCutShortBeforeItsGateLaunchesIsLeftForARestart(t *testing.T) {
	t.Parallel()
	counter := filepath.Join(t.TempDir(), "runs")
	repo, head := captureTestRepo(t)
	st := epicTestStore(t)
	briefs := captureTestSeed(t, captureTestStore{Store: st}, repo, head, 60)
	rig := captureTestNewRig(t, captureTestStore{Store: st}, "")
	queued := &fakeGatePools{queue: make(chan struct{})}
	rig.pools = queued
	first := rig.captures(t)
	argv := []string{"/bin/sh", "-c", "echo run >> " + counter + "\necho logged\nexit 0\n"}
	started, err := first.Capture(t.Context(), captureTestStep, CaptureArgs{
		Commit: head, Argv: argv, Briefs: briefs, Files: []string{"marker"},
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	waittest.UntilIdle(
		t,
		first.workFinished,
		"the grant is requested",
		func() bool { return len(queued.requested()) == 1 },
	)
	first.Close()

	got := captureTestStored(t, rig.st, started.ID)
	if got.State != "running" || got.Grant != "" || !got.Ended.IsZero() || got.NotCaptured != "" {
		t.Fatalf(
			"capture after a cut-short start = state %q grant %q ended %v not captured %q, "+
				"want running with no grant and nothing recorded",
			got.State, got.Grant, got.Ended, got.NotCaptured,
		)
	}

	rig.pools = &fakeGatePools{}
	resumed := rig.captures(t)
	if err := resumed.Resume(t.Context()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	captureTestIdle(t, resumed)
	ended := captureTestStored(t, rig.st, started.ID)
	if ended.State != "ended" || ended.Exit != 0 {
		t.Fatalf("capture after Resume = state %q exit %d, want ended with exit 0", ended.State, ended.Exit)
	}
	if n := evidenceReadoptLines(t, counter); n != 1 {
		t.Errorf("capture command ran %d times, want once", n)
	}
}

// evidenceRecordBrief returns the stored run with id, failing the test when it cannot be read.
func evidenceRecordBrief(t *testing.T, st *store.Store, id string) store.EvidenceRun {
	t.Helper()
	run, err := st.EvidenceRun(t.Context(), id)
	if err != nil {
		t.Fatalf("read brief %s: %v", id, err)
	}
	return run
}
