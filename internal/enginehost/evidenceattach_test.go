package enginehost

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// evidenceAttachOtherEpic is the second epic of the repository, whose capture the earlier-capture test attaches.
const evidenceAttachOtherEpic = "attach-other-epic"

// evidenceAttachRecordedID is the id of the capture evidenceAttachRecorded stores.
const evidenceAttachRecordedID = "attach-recorded"

// evidenceAttachCat returns the argv of the captures the attach tests run: it prints the marker file, which holds the name
// of the commit the file is at.
func evidenceAttachCat() []string {
	return []string{"/bin/sh", "-c", "cat marker"}
}

// evidenceAttachNew returns an Evidence over st and steps that attaches the briefs of st, under a scratch state directory.
// Close runs when the test ends.
func evidenceAttachNew(t *testing.T, st *store.Store, steps EvidenceSteps) *Evidence {
	t.Helper()
	ev := NewEvidence(EvidenceConfig{
		Store:    st,
		Steps:    steps,
		StateDir: t.TempDir(),
		Briefs:   st,
	})
	t.Cleanup(ev.Close)
	return ev
}

// evidenceAttachAddEpic stores epic id in st, over repo at base.
func evidenceAttachAddEpic(t *testing.T, st *store.Store, id, repo, base string) {
	t.Helper()
	created := time.Unix(1700000000, 0)
	if err := st.CreateEpic(t.Context(), store.EpicRow{
		ID: id, Repository: repo, Base: base, Title: "Synthetic attach epic", Intent: "Synthetic intent.",
		Care: "serious", Phase: "accepted", Created: created, Changed: created,
	}); err != nil {
		t.Fatalf("store CreateEpic %s: %v", id, err)
	}
}

// evidenceAttachAddBrief stores id as a waiting captured brief of epic at base. Its item runs the cat command for 60
// seconds and keeps the marker file.
func evidenceAttachAddBrief(t *testing.T, st *store.Store, epic, id, base string) {
	t.Helper()
	if err := st.AddEvidenceRun(t.Context(), store.EvidenceRun{
		ID: id, Epic: epic, Item: "R1", Revision: base, Phase: evidence.PhaseRed,
		Source: evidence.SourceCaptured, State: store.RunWaiting,
		Spec: evidence.Item{
			Capture: evidenceAttachCat(), Limit: 60, Artifacts: []string{"marker"},
			Conclusion: "the marker says head", Judge: "reviewer",
		},
		Created: time.Unix(1700000000, 0),
	}); err != nil {
		t.Fatalf("AddEvidenceRun %s: %v", id, err)
	}
}

// evidenceAttachSeed stores epic captureTestEpic in st, over repo at base, with count waiting briefs, and returns their ids
// in order.
func evidenceAttachSeed(t *testing.T, st *store.Store, repo, base string, count int) []string {
	t.Helper()
	evidenceAttachAddEpic(t, st, captureTestEpic, repo, base)
	briefs := make([]string, 0, count)
	for i := range count {
		id := fmt.Sprintf("brief-%d", i+1)
		evidenceAttachAddBrief(t, st, captureTestEpic, id, base)
		briefs = append(briefs, id)
	}
	return briefs
}

// evidenceAttachGather starts captureTestGatherer as the gatherer of captureTestEpic in st, which takes each of the epic's
// waiting briefs.
func evidenceAttachGather(t *testing.T, st *store.Store) {
	t.Helper()
	if _, err := st.StartEvidenceGatherer(
		t.Context(),
		captureTestEpic,
		captureTestGatherer,
		time.Unix(1700000000, 0),
	); err != nil {
		t.Fatalf("StartEvidenceGatherer %s: %v", captureTestGatherer, err)
	}
}

// evidenceAttachCaptures returns a rig over st and the Captures of it, where captureTestGatherer is a running step as well as
// captureTestStep, so either may ask for a capture. The rig keeps its blobs under the state directory of ev, which judges the
// briefs the captures answer and reads their logs there.
func evidenceAttachCaptures(t *testing.T, st *store.Store, ev *Evidence) (captureTestRig, *Captures) {
	t.Helper()
	rig := captureTestNewRig(t, captureTestStore{Store: st, gatherer: captureTestGatherer}, "")
	rig.stateDir = evidenceRunStateDir(ev)
	rig.steps[captureTestGatherer] = store.StepRow{ID: captureTestGatherer}
	return rig, rig.captures(t)
}

// evidenceAttachAsk has step ask for a capture of argv at commit, which answers briefs and keeps files, and waits until it has
// ended. It returns the capture as stored.
func evidenceAttachAsk(
	t *testing.T, rig captureTestRig, c *Captures, step, commit string, argv, briefs, files []string,
) store.Capture {
	t.Helper()
	started, err := c.Capture(t.Context(), step, CaptureArgs{Commit: commit, Argv: argv, Briefs: briefs, Files: files})
	if err != nil {
		t.Fatalf("Capture by %s: %v", step, err)
	}
	captureTestIdle(t, c)
	return captureTestStored(t, rig.st, started.ID)
}

// evidenceAttachGathererCapture has the running gatherer capture the cat command at commit for briefs, keeping the marker
// file, and waits until it has ended.
func evidenceAttachGathererCapture(
	t *testing.T, rig captureTestRig, c *Captures, commit string, briefs []string,
) store.Capture {
	t.Helper()
	return evidenceAttachAsk(t, rig, c, captureTestGatherer, commit, evidenceAttachCat(), briefs, []string{"marker"})
}

// evidenceAttachRecorded stores a capture of briefs at commit, asked for by captureTestGatherer, that ended as end says, and
// returns its id. It stands for an end a real command is not made to reach in a test, such as a timeout, which waits on the
// gate's limit.
func evidenceAttachRecorded(
	t *testing.T, st *store.Store, repo, commit string, briefs []string, end store.EvidenceCapture,
) string {
	t.Helper()
	if err := st.AddCapture(t.Context(), store.Capture{
		ID: evidenceAttachRecordedID, Epic: captureTestEpic, Repository: repo, Step: captureTestGatherer,
		Role: captureRoleGatherer, Runner: localRunnerName, Commit: commit, Argv: evidenceAttachCat(), Briefs: briefs,
		Limit: 60, Created: time.Unix(1700000000, 0),
	}); err != nil {
		t.Fatalf("AddCapture %s: %v", evidenceAttachRecordedID, err)
	}
	end.At = time.Unix(1700000001, 0)
	if err := st.RecordCaptureEnd(t.Context(), evidenceAttachRecordedID, end); err != nil {
		t.Fatalf("RecordCaptureEnd %s: %v", evidenceAttachRecordedID, err)
	}
	return evidenceAttachRecordedID
}

// evidenceAttachWantUnattached fails the test unless the run with id is stored in state, with no capture attached.
func evidenceAttachWantUnattached(t *testing.T, st *store.Store, id, state string) {
	t.Helper()
	run, err := st.EvidenceRun(t.Context(), id)
	if err != nil {
		t.Fatalf("EvidenceRun %s: %v", id, err)
	}
	if run.State != state || run.Capture != "" {
		t.Fatalf("run %s = state %q capture %q, want state %q and no capture", id, run.State, run.Capture, state)
	}
}

// evidenceAttachWantNoJudge waits until ev has no work under way, so a judge its attach started has either started or never
// will. It then fails the test unless the run with id is ended not captured with reason, attached to capture, with no judge
// step started for it.
func evidenceAttachWantNoJudge(t *testing.T, ev *Evidence, steps *evidenceRunSteps, id, capture, reason string) {
	t.Helper()
	engineOf(ev).until(t, "the attach's work ends", ev.workFinished)
	run, err := ev.store.EvidenceRun(t.Context(), id)
	if err != nil {
		t.Fatalf("EvidenceRun %s: %v", id, err)
	}
	if run.State != "ended" || run.Capture != capture || run.NotCaptured != reason || run.Verdict != "" ||
		run.Passed || run.JudgeStep != "" || run.Judges != 0 {
		t.Fatalf("run %s = state %q capture %q not captured %q verdict %q passed %t judge %q judges %d, "+
			"want ended, capture %q, not captured %q and no judge",
			id, run.State, run.Capture, run.NotCaptured, run.Verdict, run.Passed, run.JudgeStep, run.Judges,
			capture, reason)
	}
	if n := steps.startCount(); n != 0 {
		t.Fatalf("%d judge steps started for a capture that captured nothing, want none", n)
	}
}

// TestEvidenceAttachOneCaptureAttachesToTwoBriefs has the running gatherer capture for two briefs, then attaches that capture
// to both. Each brief takes the capture, keeps its marker artifact, and is judged once.
func TestEvidenceAttachOneCaptureAttachesToTwoBriefs(t *testing.T) {
	t.Parallel()
	repo, base := captureTestRepo(t)
	st := epicTestStore(t)
	briefs := evidenceAttachSeed(t, st, repo, base, 2)
	evidenceAttachGather(t, st)
	steps := &evidenceRunSteps{judges: []evidenceRunJudge{evidenceRunHolds(), evidenceRunHolds()}}
	ev := evidenceAttachNew(t, st, steps)
	rig, captures := evidenceAttachCaptures(t, st, ev)
	capture := evidenceAttachGathererCapture(t, rig, captures, base, briefs)

	attached, err := ev.Attach(t.Context(), captureTestGatherer, capture.ID, briefs)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if len(attached) != 2 {
		t.Fatalf("Attach returned %d runs, want 2", len(attached))
	}
	for _, run := range attached {
		if run.Capture != capture.ID || run.State != "judging" {
			t.Fatalf("attached run %s = capture %q state %q, want capture %q judging",
				run.ID, run.Capture, run.State, capture.ID)
		}
	}
	for _, id := range briefs {
		run := evidenceRunSettled(t, engineOf(ev), st, id)
		if run.Capture != capture.ID || run.Verdict != "holds" {
			t.Fatalf("run %s = capture %q verdict %q, want capture %q holds", id, run.Capture, run.Verdict, capture.ID)
		}
		if len(run.Artifacts) != 1 || run.Artifacts[0].Path != "marker" || run.Artifacts[0].State != "kept" {
			t.Fatalf("run %s artifacts = %+v, want the marker kept", id, run.Artifacts)
		}
	}
	if n := steps.startCount(); n != 2 {
		t.Fatalf("%d judge steps started, want one for each brief", n)
	}
}

// TestEvidenceAttachEarlierCaptureAttachesToABrief attaches a capture that another epic of the same repository made at the
// same commit, to a brief that capture does not name. The brief takes the earlier capture and is judged on it.
func TestEvidenceAttachEarlierCaptureAttachesToABrief(t *testing.T) {
	t.Parallel()
	repo, base := captureTestRepo(t)
	st := epicTestStore(t)
	briefs := evidenceAttachSeed(t, st, repo, base, 1)
	evidenceAttachAddEpic(t, st, evidenceAttachOtherEpic, repo, base)
	evidenceAttachAddBrief(t, st, evidenceAttachOtherEpic, "other-brief", base)
	evidenceAttachGather(t, st)
	steps := &evidenceRunSteps{judges: []evidenceRunJudge{evidenceRunHolds()}}
	ev := evidenceAttachNew(t, st, steps)
	rig, captures := evidenceAttachCaptures(t, st, ev)
	earlier := evidenceAttachAsk(t, rig, captures, captureTestStep, base, evidenceAttachCat(),
		[]string{"other-brief"}, []string{"marker"})

	if _, err := ev.Attach(t.Context(), captureTestGatherer, earlier.ID, briefs); err != nil {
		t.Fatalf("Attach earlier capture %s: %v", earlier.ID, err)
	}
	run := evidenceRunSettled(t, engineOf(ev), st, briefs[0])
	if run.Capture != earlier.ID || run.Verdict != "holds" {
		t.Fatalf("run %s = capture %q verdict %q, want the earlier capture %q holds",
			briefs[0], run.Capture, run.Verdict, earlier.ID)
	}
	if n := steps.startCount(); n != 1 {
		t.Fatalf("%d judge steps started, want one", n)
	}
}

// TestEvidenceAttachRefusesACallerThatIsNotTheRunningGatherer tries to attach a capture to briefs as a running step that is
// not their gatherer, and as a caller that is no step. Each is refused with ErrAttachRefused, and the briefs stay gathering
// with no capture.
func TestEvidenceAttachRefusesACallerThatIsNotTheRunningGatherer(t *testing.T) {
	t.Parallel()
	repo, base := captureTestRepo(t)
	st := epicTestStore(t)
	briefs := evidenceAttachSeed(t, st, repo, base, 1)
	evidenceAttachGather(t, st)
	steps := &evidenceRunSteps{}
	ev := evidenceAttachNew(t, st, steps)
	rig, captures := evidenceAttachCaptures(t, st, ev)
	capture := evidenceAttachGathererCapture(t, rig, captures, base, briefs)

	for _, caller := range []string{captureTestStep, "no-such-step"} {
		_, err := ev.Attach(t.Context(), caller, capture.ID, briefs)
		if !errors.Is(err, ErrAttachRefused) {
			t.Fatalf("Attach by %s = %v, want ErrAttachRefused", caller, err)
		}
	}
	evidenceAttachWantUnattached(t, st, briefs[0], store.RunGathering)
	if n := steps.startCount(); n != 0 {
		t.Fatalf("%d judge steps started by refused attaches, want none", n)
	}
}

// TestEvidenceAttachRefusesACaptureAtAnotherCommit has the running gatherer capture at one commit for briefs that are at an
// older one. The attach is refused with ErrAttachRefused, and the briefs stay gathering with no capture.
func TestEvidenceAttachRefusesACaptureAtAnotherCommit(t *testing.T) {
	t.Parallel()
	repo, base := captureTestRepo(t)
	newer := stepGitTestCommit(t, repo, map[string]string{"marker": "newer\n"})
	st := epicTestStore(t)
	briefs := evidenceAttachSeed(t, st, repo, base, 1)
	evidenceAttachGather(t, st)
	steps := &evidenceRunSteps{}
	ev := evidenceAttachNew(t, st, steps)
	rig, captures := evidenceAttachCaptures(t, st, ev)
	capture := evidenceAttachGathererCapture(t, rig, captures, newer, briefs)

	_, err := ev.Attach(t.Context(), captureTestGatherer, capture.ID, briefs)
	if !errors.Is(err, ErrAttachRefused) {
		t.Fatalf("Attach of a capture at %s to briefs at %s = %v, want ErrAttachRefused", newer, base, err)
	}
	evidenceAttachWantUnattached(t, st, briefs[0], store.RunGathering)
	if n := steps.startCount(); n != 0 {
		t.Fatalf("%d judge steps started by a refused attach, want none", n)
	}
}

// TestEvidenceAttachTimedOutCaptureEndsNotCapturedWithoutAJudge attaches a capture that timed out at its limit. The run ends
// not captured with the reason a timed-out capture gives, and no judge step is started for it.
func TestEvidenceAttachTimedOutCaptureEndsNotCapturedWithoutAJudge(t *testing.T) {
	t.Parallel()
	repo, base := captureTestRepo(t)
	st := epicTestStore(t)
	briefs := evidenceAttachSeed(t, st, repo, base, 1)
	evidenceAttachGather(t, st)
	capture := evidenceAttachRecorded(t, st, repo, base, briefs, store.EvidenceCapture{TimedOut: true})
	steps := &evidenceRunSteps{}
	ev := evidenceAttachNew(t, st, steps)

	attached, err := ev.Attach(t.Context(), captureTestGatherer, capture, briefs)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if len(attached) != 1 || attached[0].State != "ended" || attached[0].NotCaptured != evidenceCaptureKilled ||
		!attached[0].TimedOut {
		t.Fatalf("attached = %+v, want one run ended, timed out and not captured %q", attached, evidenceCaptureKilled)
	}
	evidenceAttachWantNoJudge(t, ev, steps, briefs[0], capture, evidenceCaptureKilled)
}

// TestEvidenceAttachExitEnvironmentCaptureEndsNotCapturedWithoutAJudge attaches a capture that exited EnvironmentExit. The run
// ends not captured with store.NotCapturedEnvironment, and no judge step is started for it. The capture stores no reason of
// its own, so the reason is the attach's.
func TestEvidenceAttachExitEnvironmentCaptureEndsNotCapturedWithoutAJudge(t *testing.T) {
	t.Parallel()
	repo, base := captureTestRepo(t)
	st := epicTestStore(t)
	briefs := evidenceAttachSeed(t, st, repo, base, 1)
	evidenceAttachGather(t, st)
	capture := evidenceAttachRecorded(t, st, repo, base, briefs, store.EvidenceCapture{Exit: store.EnvironmentExit})
	steps := &evidenceRunSteps{}
	ev := evidenceAttachNew(t, st, steps)

	attached, err := ev.Attach(t.Context(), captureTestGatherer, capture, briefs)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if len(attached) != 1 || attached[0].State != "ended" || attached[0].Exit != store.EnvironmentExit {
		t.Fatalf("attached = %+v, want one run ended with exit %d", attached, store.EnvironmentExit)
	}
	evidenceAttachWantNoJudge(t, ev, steps, briefs[0], capture, store.NotCapturedEnvironment)
}
