package build

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// designGates are the review gates of the epic the design judge tests run under, so a design task past its last gate
// is at position 2.
var designGates = []string{"lint", "security"}

// designPassReturn is a design judge's done return that passes the design.
const designPassReturn = `{"status":"done","verdict":"pass",` +
	`"reasons":["every interface is present","none contradicts the contract"]}`

// designFailReturn is a design judge's done return that fails the design with two reasons.
const designFailReturn = `{"status":"done","verdict":"fail",` +
	`"reasons":["Store is missing Get","Put contradicts the contract"]}`

// designView returns the view of task 3, a design task at attempt 1 past its last review gate, built from base to rev,
// with step as its judge's step (nil for none) and reviews as the reviews it holds.
func designView(step *store.StepRow, reviews ...store.TaskReview) View {
	return View{
		Epic: decideEpic, ReviewGates: designGates, Step: step,
		Task: store.EpicTask{
			Seq: 3, Kind: store.TaskKindDesign, Title: "Design the store",
			Deliverable: "Put the Store interface in place",
			Build:       store.TaskBuild{Stage: StageDesign, Base: "base", Attempt: 1, Revision: "rev", GatePos: 2},
			Reviews:     reviews,
		},
	}
}

// judgeStep returns the step of a design judge that ended with end and kept result.
func judgeStep(end, result string) *store.StepRow {
	return &store.StepRow{ID: "judge-step", End: end, Result: result, Reason: "the judge's reason"}
}

// TestDesignJudgeDoneEndStartsOneReadOnlyJudgeStepAtRevision: a design task whose last review gate is done moves to its
// design stage, and the judge then starts one read-only step at the task's revision, under the verifier agent, whose
// brief names both checks.
func TestDesignJudgeDoneEndStartsOneReadOnlyJudgeStepAtRevision(t *testing.T) {
	t.Parallel()
	setup := newStartSetup(t)
	revision := buildCommit(t, setup.epic.Repository)
	build := store.TaskBuild{
		Stage: StageReview, Base: setup.base, Attempt: 1, Revision: revision, GatePos: int64(len(designGates)),
	}
	task := designView(nil).Task
	task.Build = build
	moved := decideReview(View{Epic: setup.epic.ID, ReviewGates: designGates, Task: task})
	if moved.Kind != ActMove || moved.Next.Stage != StageDesign {
		t.Fatalf("decideReview past the last gate of a design task = %s at stage %q, want a move to %q",
			moved.Kind, moved.Next.Stage, StageDesign)
	}
	view := View{Epic: setup.epic.ID, ReviewGates: designGates, Task: store.EpicTask{
		Seq: task.Seq, Kind: task.Kind, Title: task.Title, Deliverable: task.Deliverable, Build: moved.Next,
	}}
	started := decideDesign(view)
	if started.Kind != ActStartStep || started.Unit != StageDesign {
		t.Fatalf("decideDesign with no step = %s of unit %q, want a start of %q",
			started.Kind, started.Unit, StageDesign)
	}
	steps := &startSteps{}
	got, err := startRunner(setup, steps, &startEvidence{}).startStep(context.Background(), setup.epic, view, started)
	if err != nil {
		t.Fatalf("startStep(design) = %v, want nil", err)
	}
	start := onlyStart(t, steps)
	if got != started.ID || start.ID != started.ID {
		t.Errorf("design judge step id = %s, start id = %s, want %s", got, start.ID, started.ID)
	}
	if !start.ReadOnly || start.Revision != revision || start.Workspace != setup.epic.Repository {
		t.Errorf("design judge start ReadOnly, Revision, Workspace = %v, %s, %s, want read-only at %s in %s",
			start.ReadOnly, start.Revision, start.Workspace, revision, setup.epic.Repository)
	}
	if start.Agent != "claude-verify" {
		t.Errorf("design judge Agent = %q, want the roles file's verifier agent claude-verify", start.Agent)
	}
	for _, phrase := range []string{"exists as compiling code with stubs", "contradicts the contract"} {
		if !strings.Contains(start.Prompt, phrase) {
			t.Errorf("design judge prompt does not name a check: missing %q", phrase)
		}
	}
	if start.Schema != DesignJudgeSchema || start.DoneValue != "done" {
		t.Error("design judge start does not carry DesignJudgeSchema with done as its done value")
	}
}

// TestDesignJudgePassIsAccepted: a design judge that passes stores its review, and the next decision accepts the task.
func TestDesignJudgePassIsAccepted(t *testing.T) {
	t.Parallel()
	stored := decideDesign(designView(judgeStep("done", designPassReturn)))
	if stored.Kind != ActReview {
		t.Fatalf("decideDesign on a passing return = %s, want %s", stored.Kind, ActReview)
	}
	got := decideDesign(designView(judgeStep("done", designPassReturn), stored.Review))
	if got.Kind != ActAccept || got.Reason != ReviewReviewed {
		t.Errorf("decideDesign once the pass is stored = %s with reason %q, want %s with %q",
			got.Kind, got.Reason, ActAccept, ReviewReviewed)
	}
}

// TestDesignJudgeFailClimbsWithReasonsAndIsNotAccepted: a design judge that fails stores its review, and the next
// decision spends the attempt with the judge's reasons instead of accepting the task.
func TestDesignJudgeFailClimbsWithReasonsAndIsNotAccepted(t *testing.T) {
	t.Parallel()
	stored := decideDesign(designView(judgeStep("done", designFailReturn)))
	if stored.Kind != ActReview {
		t.Fatalf("decideDesign on a failing return = %s, want %s", stored.Kind, ActReview)
	}
	got := decideDesign(designView(judgeStep("done", designFailReturn), stored.Review))
	want := "design judge: Store is missing Get; Put contradicts the contract"
	if got.Kind != ActFail || got.Reason != want {
		t.Errorf("decideDesign once the fail is stored = %s with reason %q, want %s with %q",
			got.Kind, got.Reason, ActFail, want)
	}
}

// TestDesignJudgeStoresVerdictAndReasonsInReviews: the design review a judge's done return stores holds the attempt,
// the position past the last gate, the design gate, the step, and the verdict and reasons the judge gave.
func TestDesignJudgeStoresVerdictAndReasonsInReviews(t *testing.T) {
	t.Parallel()
	const position = int64(2)
	tests := []struct {
		name   string
		result string
		want   store.TaskReview
	}{
		{
			name:   "pass",
			result: designPassReturn,
			want: store.TaskReview{
				Attempt: 1, Position: position, Gate: "design", Step: "judge-step", Verdict: verdictHolds,
				Reason: "every interface is present; none contradicts the contract",
			},
		},
		{
			name:   "fail",
			result: designFailReturn,
			want: store.TaskReview{
				Attempt: 1, Position: position, Gate: "design", Step: "judge-step", Verdict: verdictFails,
				Reason: "Store is missing Get; Put contradicts the contract",
			},
		},
	}
	for _, tc := range tests {
		got := decideDesign(designView(judgeStep("done", tc.result)))
		if got.Kind != ActReview || !reflect.DeepEqual(got.Review, tc.want) {
			t.Errorf("%s: decideDesign review = %s %+v, want %s %+v", tc.name, got.Kind, got.Review, ActReview, tc.want)
		}
	}
}

// TestDesignJudgeNonDesignTaskIsAcceptedWithNoJudgeStep: a task of the ordinary kind past its last review gate is
// accepted as decideReview accepts it, and never moves to the design stage.
func TestDesignJudgeNonDesignTaskIsAcceptedWithNoJudgeStep(t *testing.T) {
	t.Parallel()
	build := store.TaskBuild{Stage: StageReview, Base: "base", Attempt: 1, Revision: "rev", GatePos: 2}
	got := decideReview(View{
		Epic: decideEpic, ReviewGates: designGates,
		Task: store.EpicTask{Seq: 3, Build: build},
	})
	if got.Kind != ActAccept || got.Reason != ReviewReviewed || got.Next.Stage != StageReview {
		t.Errorf("decideReview past the last gate of an ordinary task = %s, reason %q, stage %q; "+
			"want accept as reviewed at stage %q", got.Kind, got.Reason, got.Next.Stage, StageReview)
	}
}

// TestDesignJudgeNoDoneEndRerunsOnceThenSpends: a judge step that ends without a verdict reruns once, and the second
// such end spends the attempt with how the step ended.
func TestDesignJudgeNoDoneEndRerunsOnceThenSpends(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		step   *store.StepRow
		reason string
	}{
		{
			name: "returned", step: judgeStep(endReturned, `{"status":"blocked"}`),
			reason: "returned: the judge's reason",
		},
		{name: "done with no verdict", step: judgeStep("done", `{"status":"done","verdict":"maybe"}`),
			reason: "done: the judge's reason"},
		{name: "fail with no reasons", step: judgeStep("done", `{"status":"done","verdict":"fail","reasons":[]}`),
			reason: "done: the judge's reason"},
	}
	for _, tc := range tests {
		view := designView(tc.step)
		first := decideDesign(view)
		if first.Kind != ActMove || first.Next.Rerun != 1 || first.Next.NoVerdict != 1 {
			t.Errorf("%s: first no verdict = %s with rerun %d and no verdict %d, want a rerun once",
				tc.name, first.Kind, first.Next.Rerun, first.Next.NoVerdict)
		}
		view.Task.Build.NoVerdict = 1
		second := decideDesign(view)
		if second.Kind != ActFail || second.Reason != tc.reason {
			t.Errorf("%s: second no verdict = %s with reason %q, want %s with %q",
				tc.name, second.Kind, second.Reason, ActFail, tc.reason)
		}
	}
}

// TestDesignJudgeStoppedEndHoldsBuild: a judge step the owner stopped holds the design build stopped, as a review does.
func TestDesignJudgeStoppedEndHoldsBuild(t *testing.T) {
	t.Parallel()
	got := decideDesign(designView(judgeStep(endStopped, "")))
	if got.Kind != ActPause || got.Next.Held != HeldStopped {
		t.Errorf("decideDesign on a stopped judge = %s with held %q, want %s with held %q",
			got.Kind, got.Next.Held, ActPause, HeldStopped)
	}
}

// TestDesignJudgeReturnCheckRefusesFailWithNoReasons: the return check refuses a fail that gives no reason, and accepts
// a pass and a fail that gives one.
func TestDesignJudgeReturnCheckRefusesFailWithNoReasons(t *testing.T) {
	t.Parallel()
	if msgs := DesignJudgeCheck(json.RawMessage(`{"verdict":"fail","reasons":[]}`)); len(msgs) == 0 {
		t.Error("DesignJudgeCheck on a fail with no reasons = none, want a refusal")
	}
	if msgs := DesignJudgeCheck(json.RawMessage(designPassReturn)); len(msgs) != 0 {
		t.Errorf("DesignJudgeCheck on a pass = %v, want none", msgs)
	}
	if msgs := DesignJudgeCheck(json.RawMessage(designFailReturn)); len(msgs) != 0 {
		t.Errorf("DesignJudgeCheck on a fail with reasons = %v, want none", msgs)
	}
}

// TestDesignJudgeUnitIDsGiveStepID: the design unit has a step id derived from its attempt and rerun, and no run.
func TestDesignJudgeUnitIDsGiveStepID(t *testing.T) {
	t.Parallel()
	run, step := unitIDs("acme/epic", 3, store.TaskBuild{Stage: StageDesign, Attempt: 1, Rerun: 2})
	if run != "" || step != StepID("acme/epic", 3, StageDesign, 1, 2, 0, 0) {
		t.Errorf("unitIDs of the design unit = %q, %q; want no run and the design step id", run, step)
	}
}

// TestDesignJudgeMissingRoleFileIsRoleError: a design judge whose roles file is missing is a role error, and no step
// starts.
func TestDesignJudgeMissingRoleFileIsRoleError(t *testing.T) {
	t.Parallel()
	setup := newStartSetup(t)
	if err := os.Remove(setup.rolesPath); err != nil {
		t.Fatal(err)
	}
	steps := &startSteps{}
	_, err := startRunner(setup, steps, &startEvidence{}).startStep(
		context.Background(), setup.epic, designView(nil),
		Action{Kind: ActStartStep, Unit: StageDesign, ID: "id"},
	)
	var roleErr RoleError
	if !errors.As(err, &roleErr) {
		t.Fatalf("design judge startStep with no role file = %v, want a RoleError", err)
	}
	if len(steps.starts) != 0 {
		t.Errorf("Start calls = %d after a role file error, want none", len(steps.starts))
	}
}
