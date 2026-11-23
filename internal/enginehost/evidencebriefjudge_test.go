package enginehost

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"github.com/joshsymonds/orrery/internal/evidence"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/taskspec"
)

// briefJudgeBriefID is the id of the brief the brief tests attach a capture to.
const briefJudgeBriefID = "brief-1"

// briefJudgeReason is the reason a judge that holds gives for the conclusion the brief tests judge.
const briefJudgeReason = "the marker names head"

// briefJudgeEarlierReason is the reason the earlier green run of the earlier-runs test gives.
const briefJudgeEarlierReason = "the marker names base"

// briefJudgeBrief is a brief a test attaches a capture to: its id, the evidence item it answers by name and phase, the revision it
// is at, and the spec it keeps.
type briefJudgeBrief struct {
	id       string
	name     string
	phase    string
	revision string
	item     evidence.Item
}

// briefJudgeItem returns the evidence item the brief tests judge: its capture keeps the marker file, and its conclusion is that
// the marker says head.
func briefJudgeItem() evidence.Item {
	return evidence.Item{
		Capture: evidenceAttachCat(), Limit: 60, Artifacts: []string{"marker"},
		Conclusion: "the marker says head", Judge: "reviewer",
	}
}

// briefJudgeSetUp stores captureTestEpic in st over repo at base, with Requirement R1 holding requirement and a planned task 1
// holding task.
func briefJudgeSetUp(t *testing.T, st *store.Store, repo, base string, requirement, task evidence.Item) {
	t.Helper()
	created := time.Unix(1700000000, 0)
	if err := st.CreateEpic(t.Context(), store.EpicRow{
		ID: captureTestEpic, Repository: repo, Base: base, Title: "Synthetic brief judge epic",
		Intent: "Synthetic intent.", Care: "serious", Phase: "accepted", Created: created, Changed: created,
		Requirements: []store.EpicRequirement{{
			ID: "R1", Text: "Synthetic requirement.", Evidence: "Synthetic evidence.", Kind: "test", Item: requirement,
		}},
	}); err != nil {
		t.Fatalf("store CreateEpic: %v", err)
	}
	turn, err := st.ClaimTurn(t.Context(), captureTestEpic, created)
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
	if err = st.CompleteTurn(t.Context(), captureTestEpic, turn.Seq, plan, created); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
}

// briefJudgeAddBrief stores brief as a waiting captured run of captureTestEpic.
func briefJudgeAddBrief(t *testing.T, st *store.Store, brief briefJudgeBrief) {
	t.Helper()
	if err := st.AddEvidenceRun(t.Context(), store.EvidenceRun{
		ID: brief.id, Epic: captureTestEpic, Item: brief.name, Revision: brief.revision, Phase: brief.phase,
		Source: evidence.SourceCaptured, State: store.RunWaiting, Spec: brief.item, Created: time.Unix(1700000000, 0),
	}); err != nil {
		t.Fatalf("AddEvidenceRun %s: %v", brief.id, err)
	}
}

// briefJudgeEarlier stores a green run of R1 at revision, created before any brief, that ended holds for briefJudgeEarlierReason.
func briefJudgeEarlier(t *testing.T, st *store.Store, revision string, item evidence.Item) {
	t.Helper()
	const id = "earlier-run"
	if err := st.AddEvidenceRun(t.Context(), store.EvidenceRun{
		ID: id, Epic: captureTestEpic, Item: "R1", Revision: revision, Phase: evidence.PhaseGreen,
		Source: evidence.SourceCaptured, Spec: item, Created: time.Unix(1699999000, 0),
	}); err != nil {
		t.Fatalf("AddEvidenceRun %s: %v", id, err)
	}
	if err := st.RecordEvidenceCapture(
		t.Context(),
		id,
		store.EvidenceCapture{At: time.Unix(1699999001, 0)},
	); err != nil {
		t.Fatalf("RecordEvidenceCapture %s: %v", id, err)
	}
	if err := st.RecordEvidenceVerdict(t.Context(), id, store.EvidenceVerdict{
		Verdict: evidence.VerdictHolds, Reason: briefJudgeEarlierReason, Passed: true, At: time.Unix(1699999002, 0),
	}); err != nil {
		t.Fatalf("RecordEvidenceVerdict %s: %v", id, err)
	}
}

// briefJudgeNew returns an Evidence over st and steps that attaches briefs, whose After is after, which nil leaves as time.After.
// Close runs when the test ends.
func briefJudgeNew(
	t *testing.T, st *store.Store, steps EvidenceSteps, after func(time.Duration) <-chan time.Time,
) *Evidence {
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
		After:        after,
		Briefs:       st,
	})
	t.Cleanup(ev.Close)
	return ev
}

// briefJudgeAttach stores brief as a waiting run, has the running gatherer capture the cat command at brief's revision for it, and
// attaches that capture to it through ev. It returns the capture's id.
func briefJudgeAttach(t *testing.T, st *store.Store, ev *Evidence, brief briefJudgeBrief) string {
	t.Helper()
	briefJudgeAddBrief(t, st, brief)
	evidenceAttachGather(t, st)
	rig, captures := evidenceAttachCaptures(t, st)
	capture := evidenceAttachGathererCapture(t, rig, captures, brief.revision, []string{brief.id})
	if _, err := ev.Attach(t.Context(), captureTestGatherer, capture.ID, []string{brief.id}); err != nil {
		t.Fatalf("Attach capture %s: %v", capture.ID, err)
	}
	return capture.ID
}

// briefJudgeHoldsResult returns the return a judge keeps when its conclusion holds, with reason as its first result's evidence.
func briefJudgeHoldsResult(reason string) string {
	return fmt.Sprintf(
		`{"status":"done","results":[{"assertion":"the marker says head","kind":"must","result":"holds","evidence":%q}]}`,
		reason,
	)
}

// briefJudgeHolds is a judge that ends done with reason as its first result's evidence: its conclusion holds.
func briefJudgeHolds(reason string) evidenceRunJudge {
	return evidenceRunJudge{end: store.StepRow{End: stepLoopEndDone, Result: briefJudgeHoldsResult(reason)}}
}

// briefJudgeStoppedByPause is a judge that ends stopped, with the epic of captureTestEpic paused in st as it starts.
func briefJudgeStoppedByPause(t *testing.T, st *store.Store) evidenceRunJudge {
	t.Helper()
	return evidenceRunJudge{
		end: store.StepRow{End: stepStoppedEnd},
		beforeEnd: func() {
			if err := st.PauseEpic(
				t.Context(),
				captureTestEpic,
				store.Maker{By: store.MakerOwner},
				time.Unix(1700000000, 0),
			); err != nil {
				t.Errorf("PauseEpic: %v", err)
			}
		},
	}
}

// briefJudgeResumeEpic waits until a judge of the brief waits on its paused epic, and fails unless exactly one judge has started
// by then. It then resumes the epic in st and ends that wait.
func briefJudgeResumeEpic(
	t *testing.T, eng engine, st *store.Store, waits *evidenceRunWaits, steps *evidenceRunSteps,
) {
	t.Helper()
	evidenceRunEventually(t, eng, "a judge waits on the paused epic", func() bool { return len(waits.announced) > 0 })
	<-waits.announced
	if n := steps.startCount(); n != 1 {
		t.Fatalf("%d judge steps started while the epic is paused, want one", n)
	}
	if err := st.ResumeEpic(
		t.Context(),
		captureTestEpic,
		store.Maker{By: store.MakerOwner},
		time.Unix(1700000000, 0),
	); err != nil {
		t.Fatalf("ResumeEpic: %v", err)
	}
	waits.ticks <- time.Time{}
}

// TestBriefJudgeAttachStartsExactlyOneJudge attaches a capture to one brief. The brief is judged once, and holds.
func TestBriefJudgeAttachStartsExactlyOneJudge(t *testing.T) {
	t.Parallel()
	repo, base := captureTestRepo(t)
	st := epicTestStore(t)
	item := briefJudgeItem()
	briefJudgeSetUp(t, st, repo, base, item, item)
	steps := &evidenceRunSteps{judges: []evidenceRunJudge{evidenceRunHolds()}}
	ev := briefJudgeNew(t, st, steps, nil)

	briefJudgeAttach(t, st, ev, briefJudgeBrief{
		id: briefJudgeBriefID, name: "R1", phase: evidence.PhaseGreen, revision: base, item: item,
	})
	run := evidenceRunSettled(t, engineOf(ev), st, briefJudgeBriefID)
	if n := steps.startCount(); n != 1 || run.Judges != 1 {
		t.Fatalf("%d judge steps started and run judges %d, want one of each", n, run.Judges)
	}
}

// TestBriefJudgePromptNamesTheCapture attaches a capture the running gatherer made for a brief. The judge's prompt names the
// capture's id, argv, commit and runner, and the step that asked for it with its role.
func TestBriefJudgePromptNamesTheCapture(t *testing.T) {
	t.Parallel()
	repo, base := captureTestRepo(t)
	st := epicTestStore(t)
	item := briefJudgeItem()
	briefJudgeSetUp(t, st, repo, base, item, item)
	steps := &taskVersionRecordingSteps{
		EvidenceSteps: &evidenceRunSteps{judges: []evidenceRunJudge{evidenceRunHolds()}},
	}
	ev := briefJudgeNew(t, st, steps, nil)
	capture := briefJudgeAttach(t, st, ev, briefJudgeBrief{
		id: briefJudgeBriefID, name: "R1", phase: evidence.PhaseGreen, revision: base, item: item,
	})
	evidenceRunSettled(t, engineOf(ev), st, briefJudgeBriefID)

	starts := steps.recorded()
	if len(starts) != 1 {
		t.Fatalf("%d judge steps started, want one", len(starts))
	}
	for _, line := range []string{
		"Capture: " + capture,
		`Capture command: ["/bin/sh","-c","cat marker"]`,
		"Capture commit: " + base,
		"Capture runner: " + localRunnerName,
		"Asking step: " + captureTestGatherer,
		"Asking role: " + captureRoleGatherer,
	} {
		if !strings.Contains(starts[0].Prompt, line) {
			t.Errorf("judge prompt lacks the line %q:\n%s", line, starts[0].Prompt)
		}
	}
}

// TestBriefJudgeCaptureLinesNameLocalWhenNoRunnerIsNamed gives the capture lines of a capture that names no runner. The runner
// line names local.
func TestBriefJudgeCaptureLinesNameLocalWhenNoRunnerIsNamed(t *testing.T) {
	t.Parallel()
	lines, err := judgeCaptureLines(store.EvidenceRun{ID: briefJudgeBriefID}, store.Capture{
		ID:     "capture-1",
		Argv:   []string{"/bin/sh", "-c", "cat marker"},
		Commit: "abc",
		Step:   "step-1",
		Role:   captureRoleStep,
	})
	if err != nil {
		t.Fatalf("judgeCaptureLines: %v", err)
	}
	if !slices.Contains(lines, "Capture runner: "+localRunnerName) {
		t.Fatalf("capture lines = %q, want a runner line naming %s", lines, localRunnerName)
	}
}

// TestBriefJudgeNoVerdictIsFollowedByAnother attaches a capture to a brief whose first judge ends with no verdict. A second judge
// starts, holds, and is the verdict recorded.
func TestBriefJudgeNoVerdictIsFollowedByAnother(t *testing.T) {
	t.Parallel()
	repo, base := captureTestRepo(t)
	st := epicTestStore(t)
	item := briefJudgeItem()
	briefJudgeSetUp(t, st, repo, base, item, item)
	steps := &evidenceRunSteps{judges: []evidenceRunJudge{evidenceRunNoVerdict("first"), evidenceRunHolds()}}
	ev := briefJudgeNew(t, st, steps, nil)

	briefJudgeAttach(t, st, ev, briefJudgeBrief{
		id: briefJudgeBriefID, name: "R1", phase: evidence.PhaseGreen, revision: base, item: item,
	})
	run := evidenceRunSettled(t, engineOf(ev), st, briefJudgeBriefID)
	if run.Verdict != evidence.VerdictHolds || run.Judges != 2 || steps.startCount() != 2 {
		t.Fatalf("run = verdict %q judges %d starts %d, want holds after two judges, each started once",
			run.Verdict, run.Judges, steps.startCount())
	}
}

// TestBriefJudgeWaitsWhileTheEpicIsPaused attaches a capture while the epic is paused as its first judge starts. The second
// judge waits until the epic is resumed, then starts and holds.
func TestBriefJudgeWaitsWhileTheEpicIsPaused(t *testing.T) {
	t.Parallel()
	repo, base := captureTestRepo(t)
	st := epicTestStore(t)
	item := briefJudgeItem()
	briefJudgeSetUp(t, st, repo, base, item, item)
	steps := &evidenceRunSteps{judges: []evidenceRunJudge{briefJudgeStoppedByPause(t, st), evidenceRunHolds()}}
	waits := newEvidenceRunWaits()
	ev := briefJudgeNew(t, st, steps, waits.after)

	briefJudgeAttach(t, st, ev, briefJudgeBrief{
		id: briefJudgeBriefID, name: "R1", phase: evidence.PhaseGreen, revision: base, item: item,
	})
	briefJudgeResumeEpic(t, engineOf(ev), st, waits, steps)
	run := evidenceRunSettled(t, engineOf(ev), st, briefJudgeBriefID)
	if run.Verdict != evidence.VerdictHolds || run.Judges != 2 || steps.startCount() != 2 {
		t.Fatalf("run = verdict %q judges %d starts %d, want holds after two judges, each started once",
			run.Verdict, run.Judges, steps.startCount())
	}
}

// TestBriefJudgeGreenRequirementPromptHasEarlierRuns attaches a capture at a newer commit to a green Requirement brief, after an
// earlier green run of the same item at an older commit that held. The prompt carries the earlier run's revision and verdict.
func TestBriefJudgeGreenRequirementPromptHasEarlierRuns(t *testing.T) {
	t.Parallel()
	repo, base := captureTestRepo(t)
	newer := stepGitTestCommit(t, repo, map[string]string{"marker": "newer\n"})
	st := epicTestStore(t)
	item := briefJudgeItem()
	briefJudgeSetUp(t, st, repo, base, item, item)
	briefJudgeEarlier(t, st, base, item)
	steps := &taskVersionRecordingSteps{
		EvidenceSteps: &evidenceRunSteps{judges: []evidenceRunJudge{evidenceRunHolds()}},
	}
	ev := briefJudgeNew(t, st, steps, nil)

	briefJudgeAttach(t, st, ev, briefJudgeBrief{
		id: briefJudgeBriefID, name: "R1", phase: evidence.PhaseGreen, revision: newer, item: item,
	})
	evidenceRunSettled(t, engineOf(ev), st, briefJudgeBriefID)

	starts := steps.recorded()
	if len(starts) != 1 {
		t.Fatalf("%d judge steps started, want one", len(starts))
	}
	for _, line := range []string{
		"Earlier judged runs of this evidence item, oldest first:",
		"- revision " + base + ": verdict holds, reason: " + briefJudgeEarlierReason,
	} {
		if !strings.Contains(starts[0].Prompt, line) {
			t.Errorf("judge prompt lacks the line %q:\n%s", line, starts[0].Prompt)
		}
	}
}

// TestBriefJudgeTaskRunPromptHasVersionLines attaches a capture to a brief of task 1, which an applied version 2 changed. The
// judge's prompt opens with the task's spec lines and version 2's conclusion.
func TestBriefJudgeTaskRunPromptHasVersionLines(t *testing.T) {
	t.Parallel()
	repo, base := captureTestRepo(t)
	st := epicTestStore(t)
	item := briefJudgeItem()
	briefJudgeSetUp(t, st, repo, base, item, item)
	ctx := t.Context()
	version2 := taskVersionItem("the build passes at the base")
	if err := st.RerunTask(ctx, captureTestEpic, 1, store.TaskBuild{}, store.TaskBuild{}, store.TaskVersionInput{
		Source: store.SourceOrchestrator, Step: "step-2", Reason: "the evidence moved", Evidence: &version2,
	}, store.TaskDecision{
		ID: "T1-request-1", Text: "T1 rerun: the evidence moved",
	}, time.Unix(1700000600, 0)); err != nil {
		t.Fatalf("RerunTask: %v", err)
	}
	if err := st.SettleTaskVersion(
		ctx,
		captureTestEpic,
		1,
		store.TaskBuild{},
		store.TaskBuild{},
		store.TaskVersionSettle{
			Version: 2, State: store.VersionApplied,
		},
		time.Unix(1700000700, 0),
	); err != nil {
		t.Fatalf("SettleTaskVersion: %v", err)
	}
	row, err := st.Epic(ctx, captureTestEpic)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if len(row.Tasks) != 1 || row.Tasks[0].Version != 2 {
		t.Fatalf("tasks = %+v, want one task at version 2", row.Tasks)
	}
	task := row.Tasks[0]
	steps := &taskVersionRecordingSteps{
		EvidenceSteps: &evidenceRunSteps{judges: []evidenceRunJudge{evidenceRunHolds()}},
	}
	ev := briefJudgeNew(t, st, steps, nil)

	briefJudgeAttach(t, st, ev, briefJudgeBrief{
		id: briefJudgeBriefID, name: "T1", phase: evidence.PhaseGreen, revision: base, item: version2,
	})
	evidenceRunSettled(t, engineOf(ev), st, briefJudgeBriefID)

	starts := steps.recorded()
	if len(starts) != 1 {
		t.Fatalf("%d judge steps started, want one", len(starts))
	}
	wantLines := append(taskspec.Lines(task), "Conclusion: the conclusion you judge is version 2's evidence item, "+
		"the task's current spec, which governs where it differs from earlier versions.")
	prefix := strings.Join(wantLines, "\n") + "\n\n"
	if !strings.HasPrefix(starts[0].Prompt, prefix) {
		t.Errorf("judge prompt does not open with the version lines:\nwant prefix %q\ngot %q", prefix, starts[0].Prompt)
	}
}

// TestBriefJudgeRecordsVerdictPassedAndReason attaches a capture to a green brief whose judge holds with a reason. The run records
// holds, passed, and the judge's reason.
func TestBriefJudgeRecordsVerdictPassedAndReason(t *testing.T) {
	t.Parallel()
	repo, base := captureTestRepo(t)
	st := epicTestStore(t)
	item := briefJudgeItem()
	briefJudgeSetUp(t, st, repo, base, item, item)
	steps := &evidenceRunSteps{judges: []evidenceRunJudge{briefJudgeHolds(briefJudgeReason)}}
	ev := briefJudgeNew(t, st, steps, nil)

	briefJudgeAttach(t, st, ev, briefJudgeBrief{
		id: briefJudgeBriefID, name: "R1", phase: evidence.PhaseGreen, revision: base, item: item,
	})
	run := evidenceRunSettled(t, engineOf(ev), st, briefJudgeBriefID)
	if run.Verdict != evidence.VerdictHolds || !run.Passed || run.JudgeReason != briefJudgeReason {
		t.Fatalf("run = verdict %q passed %t reason %q, want holds, passed, reason %q",
			run.Verdict, run.Passed, run.JudgeReason, briefJudgeReason)
	}
}

// TestBriefJudgeRestartResumesTheJudge attaches a capture to a brief whose judge never ends, and closes the runner while the
// judge is under way. A restarted runner resumes the judge that started, without starting another, and records its verdict.
func TestBriefJudgeRestartResumesTheJudge(t *testing.T) {
	t.Parallel()
	repo, base := captureTestRepo(t)
	st := epicTestStore(t)
	item := briefJudgeItem()
	briefJudgeSetUp(t, st, repo, base, item, item)
	first := &evidenceRunSteps{judges: []evidenceRunJudge{{hang: true}}}
	ev := briefJudgeNew(t, st, first, nil)

	briefJudgeAttach(t, st, ev, briefJudgeBrief{
		id: briefJudgeBriefID, name: "R1", phase: evidence.PhaseGreen, revision: base, item: item,
	})
	engineOf(ev).until(t, "the judge step is recorded", func() bool {
		run, err := st.EvidenceRun(t.Context(), briefJudgeBriefID)
		if err != nil {
			t.Fatalf("EvidenceRun brief-1: %v", err)
		}
		return run.JudgeStep != ""
	})
	ev.Close()

	second := &evidenceIDSteps{ended: map[string]store.StepRow{
		"judge-1": {ID: "judge-1", End: stepLoopEndDone, Result: briefJudgeHoldsResult(briefJudgeReason)},
	}}
	resumed := briefJudgeNew(t, st, second, nil)
	if err := resumed.Resume(t.Context()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	run := evidenceRunSettled(t, engineOf(resumed), st, briefJudgeBriefID)
	if run.JudgeStep != "judge-1" || run.Judges != 1 || run.Verdict != evidence.VerdictHolds {
		t.Fatalf("run = judge %q judges %d verdict %q, want judge-1 once with holds",
			run.JudgeStep, run.Judges, run.Verdict)
	}
	if ids := second.startIDs(); len(ids) != 0 {
		t.Fatalf("restart started judges %q, want none: it resumes judge-1", ids)
	}
}

// TestBriefJudgeEndedRunReadsBackThroughEpicFlowAndListEvidence attaches a capture to a brief, judges it, and reads the ended
// run back through EpicFlow and the ListEvidence RPC. Both carry the run's verdict, passed, reason, judge, end and artifact.
func TestBriefJudgeEndedRunReadsBackThroughEpicFlowAndListEvidence(t *testing.T) {
	t.Parallel()
	repo, base := captureTestRepo(t)
	st := epicTestStore(t)
	item := briefJudgeItem()
	briefJudgeSetUp(t, st, repo, base, item, item)
	steps := &evidenceRunSteps{judges: []evidenceRunJudge{briefJudgeHolds(briefJudgeReason)}}
	ev := briefJudgeNew(t, st, steps, nil)

	capture := briefJudgeAttach(t, st, ev, briefJudgeBrief{
		id: briefJudgeBriefID, name: "R1", phase: evidence.PhaseGreen, revision: base, item: item,
	})
	evidenceRunSettled(t, engineOf(ev), st, briefJudgeBriefID)

	flow, err := st.EpicFlow(t.Context(), captureTestEpic)
	if err != nil {
		t.Fatalf("EpicFlow: %v", err)
	}
	var flowRun store.EvidenceRun
	found := false
	for _, run := range flow.Evidence {
		if run.ID == briefJudgeBriefID {
			flowRun, found = run, true
		}
	}
	if !found {
		t.Fatalf("EpicFlow evidence = %+v, want brief-1", flow.Evidence)
	}
	if flowRun.State != "ended" || flowRun.Capture != capture || flowRun.Verdict != evidence.VerdictHolds ||
		!flowRun.Passed || flowRun.JudgeReason != briefJudgeReason || flowRun.JudgeStep == "" || flowRun.Judges != 1 ||
		flowRun.Ended.IsZero() || len(flowRun.Artifacts) != 1 || flowRun.Artifacts[0].State != "kept" {
		t.Fatalf("EpicFlow run = state %q capture %q verdict %q passed %t reason %q judge %q judges %d artifacts %+v, "+
			"want ended, capture %q, holds, passed, reason %q, one judge, and the marker kept",
			flowRun.State, flowRun.Capture, flowRun.Verdict, flowRun.Passed, flowRun.JudgeReason, flowRun.JudgeStep,
			flowRun.Judges, flowRun.Artifacts, capture, briefJudgeReason)
	}

	client := evidenceServiceNewClient(t, evidenceServiceStartServer(t, ev))
	resp, err := client.ListEvidence(t.Context(), connect.NewRequest(&v1.ListEvidenceRequest{Epic: captureTestEpic}))
	if err != nil {
		t.Fatalf("ListEvidence: %v", err)
	}
	var msg *v1.EvidenceRun
	for _, run := range resp.Msg.GetRuns() {
		if run.GetId() == briefJudgeBriefID {
			msg = run
		}
	}
	if msg == nil {
		t.Fatalf("ListEvidence runs = %v, want brief-1", resp.Msg.GetRuns())
	}
	want, err := evidenceServiceRunMessage(flowRun)
	if err != nil {
		t.Fatalf("evidenceServiceRunMessage: %v", err)
	}
	if !proto.Equal(msg, want) {
		t.Fatalf("ListEvidence run = %v, want the EpicFlow run's message %v", msg, want)
	}
}
