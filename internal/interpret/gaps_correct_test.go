package interpret

import (
	"database/sql"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// gapsCorrectionID is the decision id the owner's correction to the gaps question is recorded under, gapsCorrection is
// the text the owner gives, and gapsTriggerID is the id of the trigger the correction adds.
const (
	gapsCorrectionID = "gaps-correction-1"
	gapsCorrection   = "fix the budget lines"
	gapsTriggerID    = "gaps:" + gapsCorrectionID
)

// pendingTrigger is a pending trigger of the test epic: its id and its cause.
type pendingTrigger struct {
	id, cause string
}

// pendingTriggersOf returns the pending triggers of the test epic in id order, read from the store's file at path.
func pendingTriggersOf(t *testing.T, path string) []pendingTrigger {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open store file: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close store file: %v", err)
		}
	}()
	const query = "SELECT id, cause FROM epic_triggers WHERE epic = ? AND state = 'pending' ORDER BY id"
	rows, err := db.QueryContext(t.Context(), query, runEpic)
	if err != nil {
		t.Fatalf("query triggers: %v", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("close trigger rows: %v", err)
		}
	}()
	var out []pendingTrigger
	for rows.Next() {
		var trigger pendingTrigger
		if err := rows.Scan(&trigger.id, &trigger.cause); err != nil {
			t.Fatalf("scan trigger: %v", err)
		}
		out = append(out, trigger)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read triggers: %v", err)
	}
	return out
}

// TestOwnerCorrectStartsReleasePlanGapQuestion: an epic that ended with gaps has its release run running, which waits for
// the owner. The owner answers its gaps question with correct: the correction is held as the pending trigger
// gaps:<decision id>, whose cause holds the text, and the next pass starts the release's replan plan at the release's
// attempt, without a trigger of its own. The pending triggers are then the epic's creation, the owner's answer and the
// correction, and no release trigger, and the release run keeps running.
func TestOwnerCorrectStartsReleasePlanGapQuestion(t *testing.T) {
	t.Parallel()
	w := newReplanWorld(t)
	w.pass()
	if run := runAt(t, w.s, releaseNodePath); run.State != store.NodeRunning {
		t.Fatalf("release run = %+v, want running before the epic ends with gaps", run)
	}
	ctx := t.Context()
	gaps := store.GapsQuestion{
		Gaps: []string{"the budget lines are missing"}, Recommended: store.ChoiceCorrect, Why: "the plan can add them",
	}
	if err := w.s.EndEpicWithGaps(ctx, runEpic, "the epic ended with gaps", gaps, nil, runClock()); err != nil {
		t.Fatalf("EndEpicWithGaps: %v", err)
	}
	open, err := w.s.OpenQuestions(ctx, runEpic)
	if err != nil || len(open) != 1 {
		t.Fatalf("open questions = %+v and %v, want the one gaps question", open, err)
	}
	if _, err = w.s.AnswerQuestion(
		ctx, runEpic, gapsCorrectionID, gapsCorrection, open[0].Seq, store.ChoiceCorrect, store.Maker{}, runClock(),
	); err != nil {
		t.Fatalf("AnswerQuestion: %v", err)
	}
	if logged := w.pass(); logged != "" {
		t.Fatalf("pass logged: %s", logged)
	}

	plan := runAt(t, w.s, replanPlanPath)
	if plan.State != store.NodeRunning || plan.Kind != kindPlan || plan.Node != "plan" {
		t.Errorf("replan plan = %+v, want the plan running under the release at attempt 0", plan)
	}
	if run := runAt(t, w.s, releaseNodePath); run.State != store.NodeRunning || run.Attempt != 0 {
		t.Errorf("release run = %+v, want it running at attempt 0", run)
	}

	// The creation of the epic and the owner's answer each leave a pending trigger that no planner turn claims in this
	// world. Beside them only the correction's trigger is pending, so no release trigger is.
	triggers := pendingTriggersOf(t, w.path)
	ids := make([]string, 0, len(triggers))
	causes := make(map[string]string, len(triggers))
	for _, trigger := range triggers {
		ids = append(ids, trigger.id)
		causes[trigger.id] = trigger.cause
	}
	if want := []string{"answer:" + gapsCorrectionID, "created", gapsTriggerID}; !slices.Equal(ids, want) {
		t.Fatalf("pending triggers = %q, want %q", ids, want)
	}
	if !strings.Contains(causes[gapsTriggerID], gapsCorrection) {
		t.Errorf("trigger %s cause = %q, want it to hold %q", gapsTriggerID, causes[gapsTriggerID], gapsCorrection)
	}
}
