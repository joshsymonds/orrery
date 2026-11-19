package interpret

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
)

// ladderConcludeFailure is the failure the gapped task of the test climbs to the gap on.
const ladderConcludeFailure = "the parser stays red"

// ladderConcludePasses bounds the passes ladderConcludeFinish gives until the conclusion run ends, so a flow that never
// ends its conclusion fails the test rather than looping.
const ladderConcludePasses = 4

// ladderConcludePlanned opens a store, creates the test epic on it, and completes the epic's one planner turn with a task
// that holds R1 and R2. It returns the store.
func ladderConcludePlanned(t *testing.T) *store.Store {
	t.Helper()
	s := openRunStore(t)
	createRunEpic(t, s, store.EpicWorkflow{})
	task := runTask("gapped", "internal/interpret/runner.go")
	task.Requirements = []string{"R1", "R2"}
	completeTurn(t, s, store.EpicPlan{
		Decisions: []string{}, Tasks: []store.EpicTaskInput{task}, Outline: "one task", Outcome: "complete",
	})
	return s
}

// ladderConcludeReview returns the action of a review that leaves no gap of its own: its verdicts all hold, and its
// ledger is empty.
func ladderConcludeReview() kinds.Action {
	return kinds.Action{
		Kind: kinds.ActEnd, Output: "security: holds\ntests: holds",
		Verdicts: []store.ReviewVerdict{
			{Position: 1, Reviewer: "security", Verdict: "holds"},
			{Position: 2, Reviewer: "tests", Verdict: "holds"},
		},
	}
}

// ladderConcludeEnded reports whether the conclusion run of the test epic has ended.
func ladderConcludeEnded(t *testing.T, s *store.Store) bool {
	t.Helper()
	for _, run := range runsOf(t, s) {
		if run.Path == concludeNodeKind {
			return run.State == store.NodeEnded
		}
	}
	return false
}

// ladderConcludeFinish runs the test epic's review, then its conclusion, over the registered conclude kind. It freezes
// the review as the review's prepare does, and passes until the conclusion run has ended.
func ladderConcludeFinish(t *testing.T, s *store.Store) {
	t.Helper()
	ctx := context.Background()
	runner := runnerReviewRunner(s, runnerReviewers{}, s)
	runner.kinds[reviewNodeKind] = runnerWaitsForFreeze(ladderConcludeReview())
	flow := runnerFlow(reviewNodeKind, concludeNodeKind)
	runner.passFlow(ctx, store.EpicRow{ID: runEpic}, flow)
	runnerFreezeReview(t, s)
	for range ladderConcludePasses {
		runner.passFlow(ctx, store.EpicRow{ID: runEpic}, flow)
		if ladderConcludeEnded(t, s) {
			return
		}
	}
	t.Fatalf("conclusion run did not end within %d passes", ladderConcludePasses)
}

// TestTaskLadderConcludeStoresGapEnding checks, over a real store, that a task climbed to the gap ends the epic itself in
// ended_with_gaps, with one ending that names the task and each Requirement it holds. The control leaves its task open,
// so the same review ends the epic clean and tells no gap.
func TestTaskLadderConcludeStoresGapEnding(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := ladderConcludePlanned(t)
	tasks := epicOf(t, s).Tasks
	if len(tasks) != 1 {
		t.Fatalf("epic has %d tasks, want 1", len(tasks))
	}
	task := tasks[0]
	from := task.Build
	move := store.RungMove{MovedBy: store.MovedByUnit, Cause: ladderConcludeFailure, To: store.GapRung}
	to := store.ClimbedBuild(from, store.GapRung, ladderConcludeFailure)
	if err := s.ClimbTask(ctx, runEpic, task.Seq, from, to, move, nil, nil, time.Unix(1700003000, 0)); err != nil {
		t.Fatalf("ClimbTask: %v", err)
	}
	if status := epicOf(t, s).Tasks[0].Status; status != store.TaskGap {
		t.Fatalf("task status = %q, want %q", status, store.TaskGap)
	}
	ladderConcludeFinish(t, s)

	if phase := epicOf(t, s).Phase; phase != "ended_with_gaps" {
		t.Errorf("epic phase = %q, want ended_with_gaps", phase)
	}
	want := kinds.EndingBody("ended_with_gaps", []string{
		fmt.Sprintf("T%d ended as a gap holding R1, R2: %s", task.Seq, ladderConcludeFailure),
	})
	tells := epicTellTexts(t, s)
	if !slices.Equal(tells, []string{want}) {
		t.Fatalf("tells = %q, want the one ending %q", tells, want)
	}
	for _, name := range []string{fmt.Sprintf("T%d", task.Seq), "R1", "R2"} {
		if !strings.Contains(tells[0], name) {
			t.Errorf("ending %q does not name %s", tells[0], name)
		}
	}
	if run := runAt(t, s, concludeNodeKind); run.State != store.NodeEnded || run.Outcome != "passed" {
		t.Errorf("conclude run = %+v, want ended passed", run)
	}

	control := ladderConcludePlanned(t)
	ladderConcludeFinish(t, control)
	if phase := epicOf(t, control).Phase; phase != "clean" {
		t.Errorf("control epic phase = %q, want clean", phase)
	}
	for _, tell := range epicTellTexts(t, control) {
		if strings.Contains(tell, "ended as a gap") {
			t.Errorf("control tell %q names a gap", tell)
		}
	}
}
