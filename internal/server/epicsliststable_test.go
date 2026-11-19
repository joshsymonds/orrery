package server

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// TestEpicsListAnswersTheSameEpicsWhileNothingChanges lists the epics three times over a record root and orreryd's
// Epics service on a real store, with nothing written between the lists, and requires every epic to be answered the
// same each time. The page asks Epics.Get for a live epic only when its listed message differs from the one it last
// fetched the epic against, so a field that moved on its own here would make every list refetch the epic. The record
// epic is live by its running steps, holds tasks and an unreadable effort, and so carries a problem; the stored epic
// has asked a question and runs a step.
func TestEpicsListAnswersTheSameEpicsWhileNothingChanges(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeHead(t, root, "repoa", "record", `{"outcome":""}`, epicsNow().Add(-2*time.Hour))
	recordDir := root + "/repoa/record"
	writeEffortState(t, recordDir, "1", `{"tasks":[{"slug":"one","subject":"One","status":"done"},`+
		`{"slug":"two","subject":"Two","status":"in_progress"}]}`)
	writeEffortState(t, recordDir, "2", `{not json`)

	st := openEpicAnswerStore(t)
	turn, err := st.ClaimTurn(t.Context(), syntheticEpicID, time.Unix(1_700_000_100, 0))
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	plan := store.EpicPlan{
		Outline: "Synthetic outline.",
		Outcome: "Synthetic outcome.",
		Questions: []store.EpicQuestionInput{{
			Label: "synthetic-question", Text: "Synthetic question?",
			Choices: []store.EpicChoice{
				{ID: "synthetic-a", Does: "Synthetic choice A."},
				{ID: "synthetic-b", Does: "Synthetic choice B."},
			},
			Recommended: "synthetic-a", Why: "Synthetic reason.", Confidence: "low",
		}},
	}
	if err = st.CompleteTurn(t.Context(), syntheticEpicID, turn.Seq, plan, time.Unix(1_700_000_200, 0)); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}

	gateFailed := []*enginev1.StepCheck{
		{
			Round:  1,
			Name:   "gate",
			Ok:     false,
			Detail: "synthetic failure",
			Stdout: "synthetic out",
			Stderr: "synthetic err",
		},
		{Round: 2, Name: "gate", Ok: true},
	}
	steps := &epicsFake{steps: []*enginev1.Step{
		{
			Id: "rec-b", Epic: "repoa/record", Task: "two", Agent: "implementer", Started: epicsAgo(time.Hour),
			Doing: "synthetic doing", Stage: "checks", StageStarted: epicsAgo(time.Minute), Checks: gateFailed,
			Verdicts: []*enginev1.StepVerdict{{Round: 1, Kind: "judge", Verdict: "unmet", Detail: "synthetic"}},
		},
		{Id: "rec-a", Epic: "repoa/record", Task: "one", Started: epicsAgo(time.Hour), Held: true},
		{
			Id: "rec-old", Epic: "repoa/record", Task: "one", End: "done", Started: epicsAgo(3 * time.Hour),
			Ended: epicsAgo(2 * time.Hour), Result: `{"summary":"synthetic","red_evidence":"red"}`,
		},
		{Id: "db-run", Epic: syntheticEpicID, Stage: "turn", Started: epicsAgo(time.Hour), ReadOnly: true},
	}}
	client := epicStoreServe(t, root, steps, serveEngineEpics(t, st))

	first := epicsList(t, client)
	if len(first) != 2 {
		t.Fatalf("epics = %v, want the record epic and the stored epic", epicIDs(first))
	}
	for _, epic := range first {
		if !epic.GetLive() {
			t.Fatalf("%s is not live, so the page would never ask for it: %v", epic.GetId(), epic)
		}
	}
	for list := 2; list <= 3; list++ {
		again := epicsList(t, client)
		if len(again) != len(first) {
			t.Fatalf("list %d = %v, want %v", list, epicIDs(again), epicIDs(first))
		}
		for i := range first {
			if !proto.Equal(again[i], first[i]) {
				t.Errorf("list %d answers %s as\n%v\nwant the first list's\n%v", list, first[i].GetId(), again[i],
					first[i])
			}
		}
	}
}
