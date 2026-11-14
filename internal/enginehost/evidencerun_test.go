package enginehost

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// evidenceRunEpic is the id of the epic the runner tests run items of.
const evidenceRunEpic = "run-epic"

// The poll evidenceRunEventually checks its condition every evidenceRunPoll, for evidenceRunDeadline at most.
const (
	evidenceRunPoll     = 20 * time.Millisecond
	evidenceRunDeadline = 20 * time.Second
)

// evidenceRunRepo returns a scratch repository whose HEAD commits script as capture.sh, and the HEAD.
func evidenceRunRepo(t *testing.T, script string) (string, string) {
	t.Helper()
	repo := stepGitTestRepo(t)
	head := stepGitTestCommit(t, repo, map[string]string{"capture.sh": script})
	return repo, head
}

// evidenceRunItem returns a captured item that runs capture.sh for 60 seconds, keeps the named artifacts, and is judged
// on the conclusion the tests use.
func evidenceRunItem(artifacts ...string) evidence.Item {
	return evidence.Item{
		Capture:    []string{"/bin/sh", "capture.sh"},
		Limit:      60,
		Artifacts:  artifacts,
		Conclusion: "the build passes",
		Judge:      "reviewer",
	}
}

// evidenceRunSeed stores epic evidenceRunEpic in st with repository repo. Requirement R1 holds requirement, R2 is a
// legacy requirement with no item, and a planned task of one turn, seq 1, holds task.
func evidenceRunSeed(t *testing.T, st *store.Store, repo string, requirement, task evidence.Item) {
	t.Helper()
	created := time.Unix(1700000000, 0)
	if err := st.CreateEpic(t.Context(), store.EpicRow{
		ID: evidenceRunEpic, Repository: repo, Title: "Synthetic evidence epic", Intent: "Synthetic intent.",
		Care: "serious", Phase: "accepted", Created: created, Changed: created,
		Requirements: []store.EpicRequirement{
			{
				ID: "R1", Text: "Synthetic requirement.", Evidence: "Synthetic evidence.", Kind: "test",
				Item: requirement,
			},
			{ID: "R2", Text: "Synthetic legacy requirement.", Evidence: "Synthetic legacy evidence.", Kind: "test"},
		},
	}); err != nil {
		t.Fatalf("store CreateEpic: %v", err)
	}
	turn, err := st.ClaimTurn(t.Context(), evidenceRunEpic, created)
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	plan := store.EpicPlan{
		Tasks: []store.EpicTaskInput{{
			Title: "Synthetic task.", Deliverable: "Synthetic deliverable.", Requirements: []string{"R1"},
			Gate: "check", Evidence: task,
		}},
		Outline: "Synthetic outline.", Outcome: "Synthetic outcome.",
	}
	if err = st.CompleteTurn(t.Context(), evidenceRunEpic, turn.Seq, plan, created); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
}

// evidenceRunNew returns an Evidence over st and steps that captures under scratch state directories with fake pools.
// Close runs when the test ends.
func evidenceRunNew(t *testing.T, st *store.Store, steps EvidenceSteps) *Evidence {
	t.Helper()
	home := t.TempDir()
	stepGateWriteFile(t, filepath.Join(home, "orrery", "pools.toml"), stepGatePoolsText("gate"))
	ev := NewEvidence(EvidenceConfig{
		Store:        st,
		Steps:        steps,
		Pools:        &fakeGatePools{},
		Environ:      gateEnv("XDG_CONFIG_HOME=" + home),
		StateDir:     t.TempDir(),
		GateStateDir: gateShortStateDir(t),
	})
	t.Cleanup(ev.Close)
	return ev
}

// evidenceRunEventually polls ok until it holds, and fails the test when it has not held within evidenceRunDeadline.
func evidenceRunEventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(evidenceRunDeadline)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not happen within %s", what, evidenceRunDeadline)
		}
		time.Sleep(evidenceRunPoll)
	}
}

// evidenceRunSettled waits until the run with id is ended in st, and returns it.
func evidenceRunSettled(t *testing.T, st *store.Store, id string) store.EvidenceRun {
	t.Helper()
	var run store.EvidenceRun
	evidenceRunEventually(t, "evidence run "+id+" ends", func() bool {
		var err error
		run, err = st.EvidenceRun(t.Context(), id)
		if err != nil {
			t.Fatalf("read evidence run %s: %v", id, err)
		}
		return run.State == "ended"
	})
	return run
}

// evidenceRunOnce starts a run of Requirement R1 at revision in phase through ev. It returns the run Run stored and the
// run once it ended.
func evidenceRunOnce(
	t *testing.T, ev *Evidence, st *store.Store, revision, phase string,
) (store.EvidenceRun, store.EvidenceRun) {
	t.Helper()
	started, err := ev.Run(t.Context(), evidenceRunEpic, "R1", revision, phase)
	if err != nil {
		t.Fatalf("Run(R1, %s, %s): %v", revision, phase, err)
	}
	return started, evidenceRunSettled(t, st, started.ID)
}

// evidenceRunWantStarted fails the test unless started, the run Run returned, is stored in state capturing with the item,
// revision, phase and source its request named.
func evidenceRunWantStarted(t *testing.T, started store.EvidenceRun, item, revision, phase, source string) {
	t.Helper()
	if started.Item != item || started.Revision != revision || started.Phase != phase || started.Source != source ||
		started.State != "capturing" {
		t.Fatalf(
			"started run = item %q revision %q phase %q source %q state %q, want %q %q %q %q capturing",
			started.Item, started.Revision, started.Phase, started.Source, started.State, item, revision, phase, source,
		)
	}
}

// evidenceRunJudge is what one judge step of evidenceRunSteps does: the row it ends with, or the error its Start refuses
// with, or that it never ends until its context does.
type evidenceRunJudge struct {
	end  store.StepRow
	err  error
	hang bool
}

// evidenceRunHolds is a judge that ends done: its conclusion holds.
func evidenceRunHolds() evidenceRunJudge {
	return evidenceRunJudge{end: store.StepRow{End: stepLoopEndDone}}
}

// evidenceRunFails is a judge that ends gate_failed with a first result that fails: its conclusion fails.
func evidenceRunFails() evidenceRunJudge {
	return evidenceRunJudge{end: store.StepRow{
		End:    stepLoopEndGateFailed,
		Result: `{"status":"done","results":[{"assertion":"the build passes","kind":"must","result":"fails"}]}`,
	}}
}

// evidenceRunNoVerdict is a judge that ends failed with reason, so it gives no verdict.
func evidenceRunNoVerdict(reason string) evidenceRunJudge {
	return evidenceRunJudge{end: store.StepRow{End: stepLoopEndFailed, Reason: reason}}
}

// evidenceRunSteps is an EvidenceSteps whose judge steps are the judges it is given, one per Start in order.
type evidenceRunSteps struct {
	mu     sync.Mutex
	judges []evidenceRunJudge
	count  int
	ended  map[string]store.StepRow
}

// Start starts the next scripted judge. It counts every start, and a start with no judge left is an error.
func (f *evidenceRunSteps) Start(_ context.Context, _ StepStart) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.count++
	if len(f.judges) == 0 {
		return "", errors.New("evidence run test: a judge started with none left")
	}
	judge := f.judges[0]
	f.judges = f.judges[1:]
	if judge.err != nil {
		return "", judge.err
	}
	id := fmt.Sprintf("judge-%d", f.count)
	if !judge.hang {
		if f.ended == nil {
			f.ended = map[string]store.StepRow{}
		}
		judge.end.ID = id
		f.ended[id] = judge.end
	}
	return id, nil
}

// Wait returns the judge with the id given once it has ended. A judge that hangs waits until ctx ends.
func (f *evidenceRunSteps) Wait(
	ctx context.Context, ids []string, _ time.Duration,
) (*store.StepRow, []store.StepRow, error) {
	f.mu.Lock()
	row, ended := f.ended[ids[0]]
	f.mu.Unlock()
	if !ended {
		<-ctx.Done()
		return nil, nil, fmt.Errorf("wait for judge %s: %w", ids[0], ctx.Err())
	}
	return &row, []store.StepRow{row}, nil
}

// startCount returns how many judge steps were started.
func (f *evidenceRunSteps) startCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.count
}

// TestEvidenceRunRedJudgeFailsPasses runs a red item whose judge finds the conclusion fails, which passes the phase.
func TestEvidenceRunRedJudgeFailsPasses(t *testing.T) {
	t.Parallel()
	repo, head := evidenceRunRepo(t, "exit 1\n")
	st := epicTestStore(t)
	evidenceRunSeed(t, st, repo, evidenceRunItem(), evidenceRunItem())
	steps := &evidenceRunSteps{judges: []evidenceRunJudge{evidenceRunFails()}}
	ev := evidenceRunNew(t, st, steps)

	started, got := evidenceRunOnce(t, ev, st, head, evidence.PhaseRed)
	evidenceRunWantStarted(t, started, "R1", head, evidence.PhaseRed, evidence.SourceCaptured)
	if got.Verdict != evidence.VerdictFails || !got.Passed || got.Judges != 1 {
		t.Fatalf(
			"run = verdict %q passed %t judges %d, want fails, passed, one judge",
			got.Verdict, got.Passed, got.Judges,
		)
	}
	if got.Grant == "" || got.Holder == "" {
		t.Fatalf("run grant %q holder %q, want the capture's grant and holder kept", got.Grant, got.Holder)
	}
}

// TestEvidenceRunRedJudgeHoldsDoesNotPass runs a red item whose judge finds the conclusion holds, which fails the phase.
func TestEvidenceRunRedJudgeHoldsDoesNotPass(t *testing.T) {
	t.Parallel()
	repo, head := evidenceRunRepo(t, "exit 0\n")
	st := epicTestStore(t)
	evidenceRunSeed(t, st, repo, evidenceRunItem(), evidenceRunItem())
	steps := &evidenceRunSteps{judges: []evidenceRunJudge{evidenceRunHolds()}}
	ev := evidenceRunNew(t, st, steps)

	_, got := evidenceRunOnce(t, ev, st, head, evidence.PhaseRed)
	if got.Verdict != evidence.VerdictHolds || got.Passed {
		t.Fatalf("run = verdict %q passed %t, want holds and not passed", got.Verdict, got.Passed)
	}
}

// TestEvidenceRunGreenExitZeroHoldsPasses runs a green item that exits 0 whose judge finds the conclusion holds.
func TestEvidenceRunGreenExitZeroHoldsPasses(t *testing.T) {
	t.Parallel()
	repo, head := evidenceRunRepo(t, "exit 0\n")
	st := epicTestStore(t)
	evidenceRunSeed(t, st, repo, evidenceRunItem(), evidenceRunItem())
	steps := &evidenceRunSteps{judges: []evidenceRunJudge{evidenceRunHolds()}}
	ev := evidenceRunNew(t, st, steps)

	_, got := evidenceRunOnce(t, ev, st, head, evidence.PhaseGreen)
	if got.Exit != 0 || got.Verdict != evidence.VerdictHolds || !got.Passed {
		t.Fatalf("run = exit %d verdict %q passed %t, want exit 0, holds, passed", got.Exit, got.Verdict, got.Passed)
	}
}

// TestEvidenceRunGreenExitOneHoldsDoesNotPass runs a green item that exits 1 whose judge finds the conclusion holds.
func TestEvidenceRunGreenExitOneHoldsDoesNotPass(t *testing.T) {
	t.Parallel()
	repo, head := evidenceRunRepo(t, "exit 1\n")
	st := epicTestStore(t)
	evidenceRunSeed(t, st, repo, evidenceRunItem(), evidenceRunItem())
	steps := &evidenceRunSteps{judges: []evidenceRunJudge{evidenceRunHolds()}}
	ev := evidenceRunNew(t, st, steps)

	_, got := evidenceRunOnce(t, ev, st, head, evidence.PhaseGreen)
	if got.Exit != 1 || got.Verdict != evidence.VerdictHolds || got.Passed {
		t.Fatalf(
			"run = exit %d verdict %q passed %t, want exit 1, holds, not passed",
			got.Exit, got.Verdict, got.Passed,
		)
	}
}

// TestEvidenceRunTimedOutCaptureStartsNoJudge runs an item that sleeps past its one-second limit. The run ends not
// captured, and no judge step starts.
func TestEvidenceRunTimedOutCaptureStartsNoJudge(t *testing.T) {
	t.Parallel()
	repo, head := evidenceRunRepo(t, "exit 0\n")
	st := epicTestStore(t)
	killed := evidence.Item{
		Capture: []string{"/bin/sh", "-c", "sleep 30"}, Limit: 1,
		Conclusion: "the build passes", Judge: "reviewer",
	}
	evidenceRunSeed(t, st, repo, killed, killed)
	steps := &evidenceRunSteps{}
	ev := evidenceRunNew(t, st, steps)

	_, got := evidenceRunOnce(t, ev, st, head, evidence.PhaseGreen)
	if !got.TimedOut || got.NotCaptured != evidenceCaptureKilled || got.Passed || got.Verdict != "" {
		t.Fatalf("run = timed out %t not captured %q passed %t verdict %q, want killed at its limit and no verdict",
			got.TimedOut, got.NotCaptured, got.Passed, got.Verdict)
	}
	if n := steps.startCount(); n != 0 {
		t.Fatalf("%d judge steps started, want none for a capture that was not captured", n)
	}
}

// TestEvidenceRunTwoNoVerdictEndsThenHoldsRecordsHolds gives two judges no verdict and a third that holds. The third
// judge's verdict is recorded, after three judges.
func TestEvidenceRunTwoNoVerdictEndsThenHoldsRecordsHolds(t *testing.T) {
	t.Parallel()
	repo, head := evidenceRunRepo(t, "exit 0\n")
	st := epicTestStore(t)
	evidenceRunSeed(t, st, repo, evidenceRunItem(), evidenceRunItem())
	steps := &evidenceRunSteps{judges: []evidenceRunJudge{
		evidenceRunNoVerdict("first"), evidenceRunNoVerdict("second"), evidenceRunHolds(),
	}}
	ev := evidenceRunNew(t, st, steps)

	_, got := evidenceRunOnce(t, ev, st, head, evidence.PhaseGreen)
	if got.Verdict != evidence.VerdictHolds || !got.Passed || got.Judges != 3 || steps.startCount() != 3 {
		t.Fatalf("run = verdict %q passed %t judges %d starts %d, want holds, passed, three judges",
			got.Verdict, got.Passed, got.Judges, steps.startCount())
	}
}

// TestEvidenceRunThreeNoVerdictEndsRecordNoVerdict gives three judges no verdict. The run records no verdict, its last
// end as the reason, and starts no fourth judge.
func TestEvidenceRunThreeNoVerdictEndsRecordNoVerdict(t *testing.T) {
	t.Parallel()
	repo, head := evidenceRunRepo(t, "exit 0\n")
	st := epicTestStore(t)
	evidenceRunSeed(t, st, repo, evidenceRunItem(), evidenceRunItem())
	steps := &evidenceRunSteps{judges: []evidenceRunJudge{
		evidenceRunNoVerdict("first"), evidenceRunNoVerdict("second"), evidenceRunNoVerdict("third"),
	}}
	ev := evidenceRunNew(t, st, steps)

	_, got := evidenceRunOnce(t, ev, st, head, evidence.PhaseGreen)
	starts := steps.startCount()
	const wantReason = "failed: third"
	if got.Verdict != "" || got.Passed || got.JudgeReason != wantReason || got.Judges != 3 || starts != 3 {
		t.Fatalf(
			"run = verdict %q passed %t reason %q judges %d starts %d, want no verdict, not passed, reason %q, three",
			got.Verdict, got.Passed, got.JudgeReason, got.Judges, starts, wantReason,
		)
	}
}

// TestEvidenceRunSuppliedReadsCommittedFiles runs a supplied item over a commit whose working tree has since changed.
// The kept bytes are the committed ones, and a file the commit does not hold is missing, so the capture exits 1.
func TestEvidenceRunSuppliedReadsCommittedFiles(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	head := stepGitTestCommit(t, repo, map[string]string{"report.txt": "committed\n"})
	stepGitTestWrite(t, repo, "report.txt", "uncommitted\n")
	st := epicTestStore(t)
	supplied := evidence.Item{
		Supplied: []string{"report.txt", "absent.txt"}, Conclusion: "the build passes", Judge: "reviewer",
	}
	evidenceRunSeed(t, st, repo, supplied, supplied)
	steps := &evidenceRunSteps{judges: []evidenceRunJudge{evidenceRunHolds()}}
	ev := evidenceRunNew(t, st, steps)

	started, got := evidenceRunOnce(t, ev, st, head, evidence.PhaseRed)
	evidenceRunWantStarted(t, started, "R1", head, evidence.PhaseRed, evidence.SourceSupplied)
	const committed = "committed\n"
	wantKept := store.EvidenceArtifact{
		Path: "report.txt", State: "kept", Hash: evidenceCaptureHash(committed),
		Blob: evidenceCaptureHash(committed) + ".txt", Size: int64(len(committed)),
	}
	wantMissing := store.EvidenceArtifact{Path: "absent.txt", State: "missing"}
	if len(got.Artifacts) != 2 || got.Artifacts[0] != wantKept || got.Artifacts[1] != wantMissing {
		t.Fatalf("artifacts = %+v, want %+v then %+v", got.Artifacts, wantKept, wantMissing)
	}
	if got.Exit != 1 || got.Verdict != evidence.VerdictHolds || got.Passed {
		t.Fatalf(
			"run = exit %d verdict %q passed %t, want exit 1, holds, not passed",
			got.Exit, got.Verdict, got.Passed,
		)
	}
	data, hash, err := ev.Artifact(t.Context(), got.ID, "report.txt")
	if err != nil {
		t.Fatalf("Artifact report.txt: %v", err)
	}
	if string(data) != committed || hash != wantKept.Hash {
		t.Fatalf(
			"Artifact report.txt = %q hash %s, want the committed %q hash %s",
			data, hash, committed, wantKept.Hash,
		)
	}
}

// TestEvidenceRunRefusesABadRequest asks for each kind of bad request. Each is refused, and no run is stored.
func TestEvidenceRunRefusesABadRequest(t *testing.T) {
	t.Parallel()
	repo, head := evidenceRunRepo(t, "exit 0\n")
	st := epicTestStore(t)
	item := evidenceRunItem()
	evidenceRunSeed(t, st, repo, item, item)
	ev := evidenceRunNew(t, st, &evidenceRunSteps{})
	refusals := []struct {
		name, epic, item, revision, phase string
		want                              error
	}{
		{"unknown epic", "no-such-epic", "R1", head, evidence.PhaseRed, store.ErrNoEpic},
		{"unknown requirement", evidenceRunEpic, "R9", head, evidence.PhaseRed, ErrEvidenceRefused},
		{"malformed item", evidenceRunEpic, "X1", head, evidence.PhaseRed, ErrEvidenceRefused},
		{"unknown task", evidenceRunEpic, "T9", head, evidence.PhaseRed, ErrEvidenceRefused},
		{"planned task item", evidenceRunEpic, "T1", head, evidence.PhaseRed, ErrEvidenceRefused},
		{"legacy requirement", evidenceRunEpic, "R2", head, evidence.PhaseRed, ErrEvidenceRefused},
		{"unknown phase", evidenceRunEpic, "R1", head, "blue", ErrEvidenceRefused},
		{"unresolvable revision", evidenceRunEpic, "R1", "no-such-revision", evidence.PhaseRed, ErrEvidenceRefused},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ev.Run(t.Context(), tc.epic, tc.item, tc.revision, tc.phase)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Run = %v, want an error matching %v", err, tc.want)
			}
			evidenceRunWantNoRuns(t, st)
		})
	}
}

// evidenceRunWantNoRuns fails the test unless st holds no evidence run of evidenceRunEpic.
func evidenceRunWantNoRuns(t *testing.T, st *store.Store) {
	t.Helper()
	runs, err := st.EvidenceRuns(t.Context(), evidenceRunEpic)
	if err != nil {
		t.Fatalf("EvidenceRuns: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("%d runs stored, want none", len(runs))
	}
}

// TestEvidenceArtifactReadsLogAndKeptArtifact reads a run's log and a kept artifact. It refuses an artifact the run did
// not keep, a name it does not list, a run with no log, and a run it does not have.
func TestEvidenceArtifactReadsLogAndKeptArtifact(t *testing.T) {
	t.Parallel()
	repo, head := evidenceRunRepo(t, "echo log text\nprintf 'kept\\n' > small.txt\nexit 0\n")
	silentHead := stepGitTestCommit(t, repo, map[string]string{"capture.sh": "exit 0\n"})
	st := epicTestStore(t)
	item := evidenceRunItem("small.txt", "absent.txt")
	evidenceRunSeed(t, st, repo, item, item)
	steps := &evidenceRunSteps{judges: []evidenceRunJudge{evidenceRunHolds(), evidenceRunHolds()}}
	ev := evidenceRunNew(t, st, steps)

	_, got := evidenceRunOnce(t, ev, st, head, evidence.PhaseGreen)
	_, quiet := evidenceRunOnce(t, ev, st, silentHead, evidence.PhaseGreen)
	evidenceRunWantLog(t, ev, got, "log text\n")
	evidenceRunWantArtifact(t, ev, got.ID, "small.txt", "kept\n")
	for _, name := range []string{"absent.txt", "not-listed.txt"} {
		if _, _, err := ev.Artifact(t.Context(), got.ID, name); !errors.Is(err, ErrNoEvidenceArtifact) {
			t.Fatalf("Artifact %s = %v, want an error matching ErrNoEvidenceArtifact", name, err)
		}
	}
	if _, _, err := ev.Artifact(t.Context(), quiet.ID, ""); !errors.Is(err, ErrNoEvidenceArtifact) {
		t.Fatalf("Artifact of a run with no log = %v, want an error matching ErrNoEvidenceArtifact", err)
	}
	if _, _, err := ev.Artifact(t.Context(), "no-such-run", ""); !errors.Is(err, store.ErrNoEvidenceRun) {
		t.Fatalf("Artifact of an unknown run = %v, want an error matching ErrNoEvidenceRun", err)
	}
	runs, err := ev.Runs(t.Context(), evidenceRunEpic)
	if err != nil || len(runs) != 2 {
		t.Fatalf("Runs = %d runs, err %v, want two", len(runs), err)
	}
}

// evidenceRunWantLog fails the test unless the log of run reads as want and hashes to the hash the run stores.
func evidenceRunWantLog(t *testing.T, ev *Evidence, run store.EvidenceRun, want string) {
	t.Helper()
	data, hash, err := ev.Artifact(t.Context(), run.ID, "")
	if err != nil {
		t.Fatalf("Artifact of the log: %v", err)
	}
	if string(data) != want || hash != run.LogHash || hash != evidenceCaptureHash(want) {
		t.Fatalf("log = %q hash %s, want %q hash %s", data, hash, want, evidenceCaptureHash(want))
	}
}

// evidenceRunWantArtifact fails the test unless the artifact of run id named name reads as want, hashed by its content.
func evidenceRunWantArtifact(t *testing.T, ev *Evidence, id, name, want string) {
	t.Helper()
	data, hash, err := ev.Artifact(t.Context(), id, name)
	if err != nil {
		t.Fatalf("Artifact %s: %v", name, err)
	}
	if string(data) != want || hash != evidenceCaptureHash(want) {
		t.Fatalf("artifact %s = %q hash %s, want %q hash %s", name, data, hash, want, evidenceCaptureHash(want))
	}
}

// TestEvidenceRunCloseRecordsNothingMore closes the runner while a judge is still running. The run keeps the judge step
// it recorded, and nothing more is recorded: it stays judging with no verdict.
func TestEvidenceRunCloseRecordsNothingMore(t *testing.T) {
	t.Parallel()
	repo, head := evidenceRunRepo(t, "exit 0\n")
	st := epicTestStore(t)
	evidenceRunSeed(t, st, repo, evidenceRunItem(), evidenceRunItem())
	steps := &evidenceRunSteps{judges: []evidenceRunJudge{{hang: true}}}
	ev := evidenceRunNew(t, st, steps)
	started, err := ev.Run(t.Context(), evidenceRunEpic, "R1", head, evidence.PhaseGreen)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	evidenceRunEventually(t, "the judge step is recorded", func() bool {
		run, readErr := st.EvidenceRun(t.Context(), started.ID)
		if readErr != nil {
			t.Fatalf("read evidence run: %v", readErr)
		}
		return run.JudgeStep != ""
	})

	ev.Close()
	got, err := st.EvidenceRun(t.Context(), started.ID)
	if err != nil {
		t.Fatalf("read evidence run after Close: %v", err)
	}
	if got.State != "judging" || got.Verdict != "" || got.Judges != 1 {
		t.Fatalf("run after Close = state %q verdict %q judges %d, want judging, no verdict, one judge",
			got.State, got.Verdict, got.Judges)
	}
}
