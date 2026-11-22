package enginehost

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/taskspec"
)

// taskVersionRunID is the caller id of the task run the store-backed judge test runs.
const taskVersionRunID = "0f1e2d3c4b5a69788796a5b4c3d2e1f0"

// taskVersionRecordingSteps is an EvidenceSteps that records each StepStart it is asked to start, and delegates every call to
// the steps it wraps.
type taskVersionRecordingSteps struct {
	EvidenceSteps
	mu     sync.Mutex
	starts []StepStart
}

// Start records start, then starts it on the steps it wraps.
func (r *taskVersionRecordingSteps) Start(ctx context.Context, start StepStart) (string, error) {
	r.mu.Lock()
	r.starts = append(r.starts, start)
	r.mu.Unlock()
	return r.EvidenceSteps.Start(ctx, start)
}

// recorded returns the starts recorded so far, in order.
func (r *taskVersionRecordingSteps) recorded() []StepStart {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.starts)
}

// taskVersionAppliedEvidence returns version of a task that applies evidence item under the orchestrator's decision
// T1-request-1, stored and settled at a fixed time.
func taskVersionAppliedEvidence(version int64, item evidence.Item) store.TaskVersion {
	at := time.Unix(1700000600, 0)
	return store.TaskVersion{
		Version: version, Source: store.SourceOrchestrator, Decision: "T1-request-1", Step: "step-2",
		Reason: "the evidence moved", State: store.VersionApplied, Evidence: &item, Created: at, Settled: at,
	}
}

// taskVersionCheckingEvidence returns version of a task that changes its evidence to item and is checking.
func taskVersionCheckingEvidence(version int64, item evidence.Item) store.TaskVersion {
	return store.TaskVersion{
		Version: version, Source: store.SourceOrchestrator, Decision: "T1-request-1", Step: "step-2",
		Reason: "the evidence moved", State: store.VersionChecking, Evidence: &item,
		Created: time.Unix(1700000600, 0),
	}
}

// taskVersionItem returns the evidence item of evidenceRunItem, whose capture runs, with conclusion as its conclusion.
func taskVersionItem(conclusion string) evidence.Item {
	item := evidenceRunItem()
	item.Conclusion = conclusion
	return item
}

// TestTaskVersionJudgePromptRendersTheCurrentVersion: a green run of a task at version 2, which an applied orchestrator
// version changes the evidence of, is judged on version 2's conclusion, and its lines name version 2 as the one that
// governs.
func TestTaskVersionJudgePromptRendersTheCurrentVersion(t *testing.T) {
	t.Parallel()
	item := taskVersionItem("the build passes at the base")
	task := store.EpicTask{
		Seq: 1, Evidence: item, Version: 2, Versions: []store.TaskVersion{taskVersionAppliedEvidence(2, item)},
	}
	run := store.EvidenceRun{Item: "T1", Phase: evidence.PhaseGreen}

	got := judgeSpecLines(run, task)
	want := append(taskspec.Lines(task), "Conclusion: the conclusion you judge is version 2's evidence item, "+
		"the task's current spec, which governs where it differs from earlier versions.")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("judgeSpecLines = %q, want %q", got, want)
	}
	if joined := strings.Join(got, "\n"); !strings.Contains(joined, "version 2 governs") {
		t.Errorf("judgeSpecLines lacks the sentence that version 2 governs:\n%s", joined)
	}
}

// TestTaskVersionJudgePromptNamesTheCheckingVersionAtRed: while a task at version 1 has a checking version 2 that changes
// its evidence, its red run is judged on version 2's conclusion as the one its red checks at the base, and its green run
// gives no lines.
func TestTaskVersionJudgePromptNamesTheCheckingVersionAtRed(t *testing.T) {
	t.Parallel()
	task := store.EpicTask{
		Seq: 1, Evidence: taskVersionItem("the build passes"), Version: 1,
		Versions: []store.TaskVersion{taskVersionCheckingEvidence(2, taskVersionItem("the build passes at the base"))},
	}
	red := store.EvidenceRun{Item: "T1", Phase: evidence.PhaseRed}
	green := store.EvidenceRun{Item: "T1", Phase: evidence.PhaseGreen}

	wantRed := []string{
		"Conclusion: the conclusion you judge is version 2's evidence item, which this red run checks at " +
			"the task's base. Version 2 governs only once its red does not hold there.",
	}
	if got := judgeSpecLines(red, task); !reflect.DeepEqual(got, wantRed) {
		t.Errorf("red judgeSpecLines = %q, want %q", got, wantRed)
	}
	if got := judgeSpecLines(green, task); got != nil {
		t.Errorf("green judgeSpecLines = %q, want nil", got)
	}
}

// TestTaskVersionJudgePromptUnchangedAtThePlannersVersion: a task at version 1 with no checking version gives its judge no
// lines in either phase, so its prompt stays as it was before any version changed it.
func TestTaskVersionJudgePromptUnchangedAtThePlannersVersion(t *testing.T) {
	t.Parallel()
	task := store.EpicTask{Seq: 1, Evidence: taskVersionItem("the build passes"), Version: 1}
	for _, phase := range []string{evidence.PhaseRed, evidence.PhaseGreen} {
		run := store.EvidenceRun{Item: "T1", Phase: phase}
		if got := judgeSpecLines(run, task); got != nil {
			t.Errorf("phase %s judgeSpecLines = %q, want nil", phase, got)
		}
	}
}

// TestTaskVersionJudgeStartsOnTheCurrentVersionsConclusion stores a version that changes task 1's evidence, settles it
// applied, and runs the task's green. The judge step it starts opens with the task's spec lines and version 2's conclusion,
// then a blank line, then judgePrompt's prompt, and its agent gate asserts version 2's conclusion alone.
func TestTaskVersionJudgeStartsOnTheCurrentVersionsConclusion(t *testing.T) {
	t.Parallel()
	repo, head := evidenceRunRepo(t, "exit 0\n")
	st := epicTestStore(t)
	evidenceRunSeed(t, st, repo, evidenceRunItem(), evidenceRunItem())
	ctx := t.Context()
	version2 := taskVersionItem("the build passes at the base")
	err := st.RerunTask(ctx, evidenceRunEpic, 1, store.TaskBuild{}, store.TaskBuild{}, store.TaskVersionInput{
		Source: store.SourceOrchestrator, Step: "step-2", Reason: "the evidence moved", Evidence: &version2,
	}, store.TaskDecision{ID: "T1-request-1", Text: "T1 rerun: the evidence moved"}, time.Unix(1700000600, 0))
	if err != nil {
		t.Fatalf("RerunTask: %v", err)
	}
	err = st.SettleTaskVersion(ctx, evidenceRunEpic, 1, store.TaskBuild{}, store.TaskBuild{}, store.TaskVersionSettle{
		Version: 2, State: store.VersionApplied,
	}, time.Unix(1700000700, 0))
	if err != nil {
		t.Fatalf("SettleTaskVersion: %v", err)
	}
	row, err := st.Epic(ctx, evidenceRunEpic)
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
	ev := evidenceRunNew(t, st, steps)
	ev.now = func() time.Time { return time.Unix(1700000800, 0) }
	started, err := ev.RunTask(ctx, taskVersionRunID, evidenceRunEpic, 1, head, evidence.PhaseGreen)
	if err != nil {
		t.Fatalf("green RunTask: %v", err)
	}
	run := evidenceRunSettled(t, engineOf(ev), st, started.ID)

	starts := steps.recorded()
	if len(starts) != 1 {
		t.Fatalf("%d judge steps started, want one", len(starts))
	}
	start := starts[0]
	judged, err := judgePrompt(run, ev.blobs)
	if err != nil {
		t.Fatalf("judgePrompt: %v", err)
	}
	wantLines := append(taskspec.Lines(task), "Conclusion: the conclusion you judge is version 2's evidence item, "+
		"the task's current spec, which governs where it differs from earlier versions.")
	want := strings.Join(wantLines, "\n") + "\n\n" + judged
	if start.Prompt != want {
		t.Errorf("judge prompt = %q, want %q", start.Prompt, want)
	}
	if start.AgentGate == nil {
		t.Fatal("judge step has no agent gate")
	}
	wantAssertions := []gates.Assertion{{Text: version2.Conclusion, Kind: gates.KindMust}}
	if !reflect.DeepEqual(start.AgentGate.Assertions, wantAssertions) {
		t.Errorf("judge assertions = %+v, want version 2's conclusion %+v", start.AgentGate.Assertions, wantAssertions)
	}
}
