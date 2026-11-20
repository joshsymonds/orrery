package store

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// metAtBaseParkReason is the reason an evidence run at base parks with when its red run judged R1 holding at the base.
const metAtBaseParkReason = "workflow node red: R1 met at the epic's base main; you can retry (run the red check of R1 " +
	"again with a fresh judge), drop (drop R1 as already met, recorded as your decision, and go on without their red) " +
	"or end (end the epic)"

// metAtBaseQuestionR1 returns the open park question a store reads back as the first question of the epic, for a red run
// that judged R1 holding at base: the three choices retry, drop and end, which recommends retry.
func metAtBaseQuestionR1() EpicQuestion {
	const ids = "R1"
	return EpicQuestion{
		Seq: 1, Turn: 0, Text: metAtBaseParkReason, State: QuestionOpen, Label: ParkLabel,
		Choices: []EpicChoice{
			{ID: "retry", Does: "run the red check of " + ids + " again with a fresh judge"},
			{
				ID:   "drop",
				Does: "drop " + ids + " as already met, recorded as your decision, and go on without their red",
			},
			{ID: "end", Does: "end the epic"},
		},
		Recommended: "retry",
		Why: "the judge read the conclusion as already holding at the base, " +
			"and a fresh judge checks that reading before anything is dropped",
		Confidence: "medium",
	}
}

// parkRedMetAtBase starts the evidence run at red of claimEpic at attempt 1 and parks it as the interpreter does when that
// run judged R1 holding at base.
func parkRedMetAtBase(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	runs := []NodeRun{{Path: "red", ID: "id-red", Node: "red", Kind: "evidence", Attempt: 1}}
	if err := s.StartNodes(ctx, claimEpic, runs, nil, claimedAt()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}
	if err := s.ParkNodeMetAtBase(ctx, claimEpic, "red", 1, "failed", "R1 red: failed", metAtBaseParkReason,
		[]string{"R1"}, completedAt()); err != nil {
		t.Fatalf("ParkNodeMetAtBase: %v", err)
	}
}

// redRunOf returns the node run of claimEpic at red, the evidence node the met-at-base tests park.
func redRunOf(t *testing.T, s *Store) NodeRun {
	t.Helper()
	runs, err := s.NodeRuns(context.Background(), claimEpic)
	if err != nil {
		t.Fatalf("NodeRuns: %v", err)
	}
	for _, run := range runs {
		if run.Path == "red" {
			return run
		}
	}
	t.Fatalf("no node run at red")
	return NodeRun{}
}

// storedEpicRow returns the epic claimEpic as the store reads it back.
func storedEpicRow(t *testing.T, s *Store) EpicRow {
	t.Helper()
	row, err := s.Epic(context.Background(), claimEpic)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	return row
}

// TestMetAtBaseQuestionParkOpensThreeChoicesAndKeepsPhase checks that a red run judged holding at base parks its run and
// its epic in one write, opens the park question with retry, drop and end, keeps the run's path, attempt and the
// Requirement it found met, and leaves the phase as it was.
func TestMetAtBaseQuestionParkOpensThreeChoicesAndKeepsPhase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	parkRedMetAtBase(t, s)

	if run := redRunOf(t, s); run.State != NodeParked || run.Attempt != 1 || run.Outcome != "failed" ||
		run.Output != "R1 red: failed" {
		t.Errorf("run after the park = %+v, want parked at attempt 1 as failed", run)
	}
	row := storedEpicRow(t, s)
	if row.Parked != metAtBaseParkReason || row.Phase != "accepted" {
		t.Errorf("parked and phase after the park = %q and %q, want the reason and accepted", row.Parked, row.Phase)
	}
	if want := []EpicQuestion{metAtBaseQuestionR1()}; !reflect.DeepEqual(row.Questions, want) {
		t.Errorf("questions after the park = %+v, want %+v", row.Questions, want)
	}
	record, err := s.fixtureQueries().
		GetEpicMetAtBase(ctx, sqlcgen.GetEpicMetAtBaseParams{Epic: claimEpic, Question: 1})
	if err != nil {
		t.Fatalf("GetEpicMetAtBase: %v", err)
	}
	if record.Path != "red" || record.Attempt != 1 || record.Requirements != `["R1"]` {
		t.Errorf("kept park = %+v, want path red, attempt 1 and requirements [\"R1\"]", record)
	}
	tells, err := s.fixtureQueries().ListEpicTells(ctx, claimEpic)
	if err != nil {
		t.Fatalf("ListEpicTells: %v", err)
	}
	if len(tells) != 1 || tells[0].Text != metAtBaseParkReason || tells[0].Question != 1 {
		t.Errorf("tells after the park = %+v, want one tell with the reason that carries question 1", tells)
	}
}

// TestMetAtBaseQuestionRetryRerunsTheRunAtNextAttemptAndUnparks checks that answering retry turns the parked evidence
// run running at its next attempt with its work cleared, and ends the park.
func TestMetAtBaseQuestionRetryRerunsTheRunAtNextAttemptAndUnparks(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	parkRedMetAtBase(t, s)

	answer, err := s.AnswerQuestion(context.Background(), claimEpic, "D3", "", 1, ChoiceRetry, Maker{}, claimedAt())
	if err != nil {
		t.Fatalf("AnswerQuestion retry: %v", err)
	}
	if answer.Question != 1 || answer.ID != "D3" {
		t.Errorf("decision = %+v, want D3 answering question 1", answer)
	}
	run := redRunOf(t, s)
	if run.State != NodeRunning || run.Attempt != 2 || run.Outcome != "" || run.Output != "" {
		t.Errorf("run after retry = %+v, want running at attempt 2 with its outcome and output cleared", run)
	}
	row := storedEpicRow(t, s)
	if row.Parked != "" || row.Phase != "accepted" {
		t.Errorf("parked and phase after retry = %q and %q, want empty and accepted", row.Parked, row.Phase)
	}
	answered := metAtBaseQuestionR1()
	answered.State, answered.Choice = QuestionAnswered, "retry"
	if want := []EpicQuestion{answered}; !reflect.DeepEqual(row.Questions, want) {
		t.Errorf("questions after retry = %+v, want %+v", row.Questions, want)
	}
	for _, requirement := range row.Requirements {
		if requirement.DroppedBy != 0 {
			t.Errorf("requirement %s dropped by %d after retry, want none", requirement.ID, requirement.DroppedBy)
		}
	}
}

// TestMetAtBaseQuestionDropMarksRequirementsDroppedAndRerunsAndUnparks checks that answering drop marks each Requirement
// the park found met as dropped by the answer's decision, appends that decision, reruns the run at its next attempt and
// ends the park. A Requirement the park did not find met stays as it was.
func TestMetAtBaseQuestionDropMarksRequirementsDroppedAndRerunsAndUnparks(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	parkRedMetAtBase(t, s)

	answer, err := s.AnswerQuestion(context.Background(), claimEpic, "D3", "R1 is already met", 1, ChoiceDrop, Maker{},
		claimedAt())

	if err != nil {
		t.Fatalf("AnswerQuestion drop: %v", err)
	}
	row := storedEpicRow(t, s)
	dropped := make(map[string]int64)
	for _, requirement := range row.Requirements {
		dropped[requirement.ID] = requirement.DroppedBy
	}
	if dropped["R1"] != answer.Seq || dropped["R3"] != 0 {
		t.Errorf("dropped by = %+v, want R1 by decision %d and R3 by none", dropped, answer.Seq)
	}
	if last := row.Decisions[len(row.Decisions)-1]; !reflect.DeepEqual(last, answer) {
		t.Errorf("last decision = %+v, want the answer %+v", last, answer)
	}
	run := redRunOf(t, s)
	if run.State != NodeRunning || run.Attempt != 2 {
		t.Errorf("run after drop = %+v, want running at attempt 2", run)
	}
	if row.Parked != "" || row.Phase != "accepted" {
		t.Errorf("parked and phase after drop = %q and %q, want empty and accepted", row.Parked, row.Phase)
	}
	answered := metAtBaseQuestionR1()
	answered.State, answered.Choice = QuestionAnswered, "drop"
	if want := []EpicQuestion{answered}; !reflect.DeepEqual(row.Questions, want) {
		t.Errorf("questions after drop = %+v, want %+v", row.Questions, want)
	}
}

// TestMetAtBaseQuestionEndSetsPhaseAndStaysParked checks that answering end sets the epic's phase to met_at_base and
// leaves the run and the park as the park left them, so RetryEpic and RetryNode refuse it.
func TestMetAtBaseQuestionEndSetsPhaseAndStaysParked(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	parkRedMetAtBase(t, s)

	if _, err := s.AnswerQuestion(ctx, claimEpic, "D3", "", 1, ChoiceEnd, Maker{}, claimedAt()); err != nil {
		t.Fatalf("AnswerQuestion end: %v", err)
	}
	row := storedEpicRow(t, s)
	if row.Phase != phaseMetAtBase || row.Parked != metAtBaseParkReason {
		t.Errorf("phase and parked after end = %q and %q, want met_at_base and the park reason", row.Phase, row.Parked)
	}
	if run := redRunOf(t, s); run.State != NodeParked || run.Attempt != 1 {
		t.Errorf("run after end = %+v, want parked at attempt 1", run)
	}
	for _, requirement := range row.Requirements {
		if requirement.DroppedBy != 0 {
			t.Errorf("requirement %s dropped by %d after end, want none", requirement.ID, requirement.DroppedBy)
		}
	}
	want := "epic " + claimEpic + " is parked for good: " + metAtBaseParkReason
	requireRefusal(t, s.RetryEpic(ctx, claimEpic, Maker{By: MakerOwner}, time.Unix(1700000000, 0)), want)
	requireRefusal(t, s.RetryNode(ctx, claimEpic, "red", Maker{By: MakerOwner}, time.Unix(1700000000, 0)), want)
}

// TestMetAtBaseQuestionRetryEpicAndRetryNodeAreAcceptedAndRerunTheRun checks that RetryEpic and RetryNode on an epic
// parked by a red run judged holding at base are accepted, rerun that run at its next attempt, end the park and answer
// the open park question retry.
func TestMetAtBaseQuestionRetryEpicAndRetryNodeAreAcceptedAndRerunTheRun(t *testing.T) {
	t.Parallel()
	retries := map[string]func(s *Store) error{
		"RetryEpic": func(s *Store) error {
			return s.RetryEpic(context.Background(), claimEpic, Maker{By: MakerOwner}, time.Unix(1700000000, 0))
		},
		"RetryNode": func(s *Store) error {
			return s.RetryNode(context.Background(), claimEpic, "red", Maker{By: MakerOwner}, time.Unix(1700000000, 0))
		},
	}
	for name, retry := range retries {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := openEpicStore(t)
			createEpic(t, s, fullEpic(claimEpic))
			parkRedMetAtBase(t, s)

			if err := retry(s); err != nil {
				t.Fatalf("%s of the met-at-base park: %v", name, err)
			}
			run := redRunOf(t, s)
			if run.State != NodeRunning || run.Attempt != 2 {
				t.Errorf("run after %s = %+v, want running at attempt 2", name, run)
			}
			row := storedEpicRow(t, s)
			if row.Parked != "" || row.Phase != "accepted" {
				t.Errorf("parked and phase after %s = %q and %q, want empty and accepted", name, row.Parked, row.Phase)
			}
			answered := metAtBaseQuestionR1()
			answered.State, answered.Choice = QuestionAnswered, "retry"
			if want := []EpicQuestion{answered}; !reflect.DeepEqual(row.Questions, want) {
				t.Errorf("questions after %s = %+v, want %+v", name, row.Questions, want)
			}
		})
	}
}
