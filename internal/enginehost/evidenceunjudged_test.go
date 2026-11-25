package enginehost

import (
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// unjudgedTestSecond is the id of the brief the reuse tests attach after the first brief was judged.
const unjudgedTestSecond = "brief-2"

// unjudgedTestJudgeFirst stores captureTestEpic over repo at base with R1 holding briefJudgeItem, attaches a capture of the
// cat command at base to a green brief of R1 through ev, and waits for its judge, which holds for briefJudgeReason. The
// running gatherer stays running, so later briefs can be attached. It returns the capture rig and its Captures.
func unjudgedTestJudgeFirst(
	t *testing.T, st *store.Store, ev *Evidence, repo, base string,
) (captureTestRig, *Captures) {
	t.Helper()
	item := briefJudgeItem()
	briefJudgeSetUp(t, st, repo, base, item, item)
	briefJudgeAttach(t, st, ev, briefJudgeBrief{
		id: briefJudgeBriefID, name: "R1", phase: evidence.PhaseGreen, revision: base, item: item,
	})
	first := evidenceRunSettled(t, engineOf(ev), st, briefJudgeBriefID)
	if first.Verdict != evidence.VerdictHolds || first.Judges != 1 {
		t.Fatalf("first brief = verdict %q judges %d, want holds by one judge", first.Verdict, first.Judges)
	}
	return evidenceAttachCaptures(t, st, ev)
}

// unjudgedTestAttachSecond stores unjudgedTestSecond as a green brief of R1 at revision, has the running gatherer capture
// the cat command there for it, attaches that capture to it through ev, and returns the run once it has a verdict.
func unjudgedTestAttachSecond(
	t *testing.T, st *store.Store, ev *Evidence, rig captureTestRig, captures *Captures, revision string,
) store.EvidenceRun {
	t.Helper()
	briefJudgeAddBrief(t, st, briefJudgeBrief{
		id: unjudgedTestSecond, name: "R1", phase: evidence.PhaseGreen, revision: revision, item: briefJudgeItem(),
	})
	capture := evidenceAttachGathererCapture(t, rig, captures, revision, []string{unjudgedTestSecond})
	if _, err := ev.Attach(t.Context(), captureTestGatherer, capture.ID, []string{unjudgedTestSecond}); err != nil {
		t.Fatalf("Attach capture %s: %v", capture.ID, err)
	}
	return evidenceRunSettled(t, engineOf(ev), st, unjudgedTestSecond)
}

// unjudgedTestWantReused fails the test unless run took the first brief's verdict without a judge, with a reason that
// names the first brief and carries its reason, and only the first brief's judge started.
func unjudgedTestWantReused(t *testing.T, run store.EvidenceRun, steps *evidenceRunSteps) {
	t.Helper()
	if run.Verdict != evidence.VerdictHolds || !run.Passed || run.Judges != 0 {
		t.Fatalf("second brief = verdict %q passed %t judges %d, want holds, passed, no judge",
			run.Verdict, run.Passed, run.Judges)
	}
	for _, want := range []string{"reused the verdict of evidence run " + briefJudgeBriefID, briefJudgeReason} {
		if !strings.Contains(run.JudgeReason, want) {
			t.Errorf("reason %q lacks %q", run.JudgeReason, want)
		}
	}
	if n := steps.startCount(); n != 1 {
		t.Fatalf("%d judge steps started, want only the first brief's", n)
	}
}

// TestUnjudgedReusesTheVerdictOfTheSameCaptureAtTheSameRevision attaches the same command at the same revision to a second
// brief of the item, after the first was judged. Its output is the same, so the second takes the first's verdict without a
// judge.
func TestUnjudgedReusesTheVerdictOfTheSameCaptureAtTheSameRevision(t *testing.T) {
	t.Parallel()
	repo, base := captureTestRepo(t)
	st := epicTestStore(t)
	steps := &evidenceRunSteps{judges: []evidenceRunJudge{briefJudgeHolds(briefJudgeReason)}}
	ev := briefJudgeNew(t, st, steps, nil)
	rig, captures := unjudgedTestJudgeFirst(t, st, ev, repo, base)

	unjudgedTestWantReused(t, unjudgedTestAttachSecond(t, st, ev, rig, captures, base), steps)
}

// TestUnjudgedReusesTheVerdictAtARevisionWithTheSameTree captures the same command for a second brief at a commit whose
// tree is the first brief's revision's. The output is the same, so the second takes the first's verdict without a judge.
func TestUnjudgedReusesTheVerdictAtARevisionWithTheSameTree(t *testing.T) {
	t.Parallel()
	repo, base := captureTestRepo(t)
	stepGitTestRun(t, repo, "commit", "-q", "--allow-empty", "-m", "same tree")
	same := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	st := epicTestStore(t)
	steps := &evidenceRunSteps{judges: []evidenceRunJudge{briefJudgeHolds(briefJudgeReason)}}
	ev := briefJudgeNew(t, st, steps, nil)
	rig, captures := unjudgedTestJudgeFirst(t, st, ev, repo, base)

	unjudgedTestWantReused(t, unjudgedTestAttachSecond(t, st, ev, rig, captures, same), steps)
}

// TestUnjudgedJudgesARevisionWithAnotherTree captures the same command for a second brief at a commit that adds a file. The
// output is the same, but the tree is not, so the second brief is judged.
func TestUnjudgedJudgesARevisionWithAnotherTree(t *testing.T) {
	t.Parallel()
	repo, base := captureTestRepo(t)
	other := stepGitTestCommit(t, repo, map[string]string{"another": "file\n"})
	st := epicTestStore(t)
	steps := &evidenceRunSteps{judges: []evidenceRunJudge{
		briefJudgeHolds(briefJudgeReason), briefJudgeHolds("judged again"),
	}}
	ev := briefJudgeNew(t, st, steps, nil)
	rig, captures := unjudgedTestJudgeFirst(t, st, ev, repo, base)

	run := unjudgedTestAttachSecond(t, st, ev, rig, captures, other)
	if run.Judges != 1 || run.JudgeReason != "judged again" {
		t.Fatalf("second brief = judges %d reason %q, want its own judge's reason", run.Judges, run.JudgeReason)
	}
	if n := steps.startCount(); n != 2 {
		t.Fatalf("%d judge steps started, want one per brief", n)
	}
}

// TestUnjudgedReadoptRecordsAGreenNonZeroExitWithoutAJudge resumes a judging green run whose capture exited 2 and which
// holds no judge step. It is recorded fails without a judge, with the reason greenExitReason gives.
func TestUnjudgedReadoptRecordsAGreenNonZeroExitWithoutAJudge(t *testing.T) {
	t.Parallel()
	repo, head := evidenceRunRepo(t, "exit 2\n")
	st := epicTestStore(t)
	evidenceRunSeed(t, st, repo, evidenceRunItem(), evidenceRunItem())
	evidenceReadoptAdd(t, st, "exited", head, evidence.PhaseGreen, evidenceRunItem())
	if err := st.RecordEvidenceCapture(
		t.Context(), "exited", store.EvidenceCapture{Exit: 2, At: time.Unix(1700000001, 0)},
	); err != nil {
		t.Fatalf("RecordEvidenceCapture: %v", err)
	}
	steps := &evidenceRunSteps{}
	ev := evidenceRunNew(t, st, steps)

	if err := ev.Resume(t.Context()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	got := evidenceRunSettled(t, engineOf(ev), st, "exited")
	if got.Verdict != evidence.VerdictFails || got.Passed || got.Judges != 0 || got.JudgeReason != greenExitReason(2) {
		t.Fatalf("run = verdict %q passed %t judges %d reason %q, want fails, not passed, no judge, the exit reason",
			got.Verdict, got.Passed, got.Judges, got.JudgeReason)
	}
	if n := steps.startCount(); n != 0 {
		t.Fatalf("%d judge steps started, want none", n)
	}
}
