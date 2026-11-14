package enginehost

import (
	"database/sql"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// The synthetic times of the planner turns the tests run, in unix seconds.
const (
	turnStarted   = 1700000100
	turnCompleted = 1700000200
	turnRestarted = 1700000300
	turnFreed     = 1700000400
)

// plannedReturn returns a valid planner return of two tasks and one question.
func plannedReturn() store.EpicPlan {
	return store.EpicPlan{
		Decisions: []string{"Synthetic planned decision one.", "Synthetic planned decision two."},
		Tasks: []store.EpicTaskInput{
			{
				Title: "Synthetic task one.", Deliverable: "Synthetic deliverable one.",
				Files: []string{"a.go", "b.go"}, Requirements: []string{"R1", "R2"}, Check: "go test ./a",
			},
			{
				Title: "Synthetic task two.", Deliverable: "Synthetic deliverable two.",
				Files: []string{"c.go"}, Requirements: []string{"R3"}, Check: "go test ./c",
			},
		},
		Outline:   "Synthetic outline.",
		Questions: []string{"Synthetic question one?"},
		Outcome:   "Synthetic outcome.",
	}
}

// openPlannerStore opens a store in a temporary directory, as epicTestStore does, and returns it with its file path.
func openPlannerStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	st, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return st, path
}

// insertPendingTrigger writes a pending trigger with id on epicServiceID into the store file at path. The store
// adds only the created trigger, and a completed turn consumes it, so a second planner turn needs this row.
func insertPendingTrigger(t *testing.T, path, id string) {
	t.Helper()
	dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: "_busy_timeout=200&_journal_mode=WAL"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open store file: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close store file: %v", err)
		}
	}()
	if _, err = db.ExecContext(t.Context(),
		`INSERT INTO epic_triggers (epic, id, cause, state, turn, created) VALUES (?, ?, ?, 'pending', 0, ?)`,
		epicServiceID, id, "synthetic trigger", turnStarted,
	); err != nil {
		t.Fatalf("insert trigger %s: %v", id, err)
	}
}

// wantProtos fails the test unless got and want hold equal messages in the same order.
func wantProtos[M proto.Message](t *testing.T, name string, got, want []M) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %d messages, want %d: %v", name, len(got), len(want), got)
	}
	for i := range want {
		if !proto.Equal(got[i], want[i]) {
			t.Fatalf("%s[%d] = %v, want %v", name, i, got[i], want[i])
		}
	}
}

func TestEpicGetReturnsCompletedTurnPausedAndParkedEpic(t *testing.T) {
	ctx := t.Context()
	st, path := openPlannerStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))

	first, err := st.ClaimTurn(ctx, epicServiceID, time.Unix(turnStarted, 0))
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err = st.SetTurnStep(ctx, epicServiceID, first.Seq, "step-1"); err != nil {
		t.Fatalf("SetTurnStep: %v", err)
	}
	if err = st.CompleteTurn(ctx, epicServiceID, first.Seq, plannedReturn(), time.Unix(turnCompleted, 0)); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	insertPendingTrigger(t, path, "t2")
	second, err := st.ClaimTurn(ctx, epicServiceID, time.Unix(turnRestarted, 0))
	if err != nil {
		t.Fatalf("ClaimTurn of second turn: %v", err)
	}
	free := store.TurnFree{Reason: "rate limited", Pause: true, Park: "needs a human"}
	if err = st.FreeTurn(ctx, epicServiceID, second.Seq, free, time.Unix(turnFreed, 0)); err != nil {
		t.Fatalf("FreeTurn: %v", err)
	}

	got := getEpicOK(t, client)
	if !got.GetPaused() {
		t.Fatalf("paused = false, want true")
	}
	if got.GetParked() != "needs a human" {
		t.Fatalf("parked = %q, want %q", got.GetParked(), "needs a human")
	}
	wantProtos(t, "turns", got.GetTurns(), []*v1.EpicTurn{
		{
			Seq: 1, Step: "step-1", State: "succeeded", Outline: "Synthetic outline.", Outcome: "Synthetic outcome.",
			Decisions: plannedReturn().Decisions, Started: turnStarted, Ended: turnCompleted,
		},
		{Seq: 2, State: "free", Reason: "rate limited", Started: turnRestarted, Ended: turnFreed},
	})
	wantProtos(t, "tasks", got.GetTasks(), []*v1.EpicTask{
		{
			Id: "T1", Turn: 1, Title: "Synthetic task one.", Deliverable: "Synthetic deliverable one.",
			Files: []string{"a.go", "b.go"}, Requirements: []string{"R1", "R2"}, Check: "go test ./a", Status: "open",
		},
		{
			Id: "T2", Turn: 1, Title: "Synthetic task two.", Deliverable: "Synthetic deliverable two.",
			Files: []string{"c.go"}, Requirements: []string{"R3"}, Check: "go test ./c", Status: "open",
		},
	})
	wantProtos(t, "questions", got.GetQuestions(), []*v1.EpicQuestion{
		{Id: "Q1", Turn: 1, Text: "Synthetic question one?", State: "open"},
	})
}

func TestEpicGetRunningTurnHasNoEnd(t *testing.T) {
	st, _ := openPlannerStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	if _, err := st.ClaimTurn(t.Context(), epicServiceID, time.Unix(turnStarted, 0)); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}

	wantProtos(t, "turns", getEpicOK(t, client).GetTurns(), []*v1.EpicTurn{{
		Seq: 1, State: "running", Started: turnStarted, Ended: 0,
	}})
}

func TestEpicGetWithNoPlannerStateIsEmpty(t *testing.T) {
	client := newEpicsClient(t, startEpicServer(t, epicTestStore(t)))
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))

	got := getEpicOK(t, client)
	if got.GetPaused() || got.GetParked() != "" {
		t.Fatalf("paused = %t parked = %q, want false and empty", got.GetPaused(), got.GetParked())
	}
	wantProtos(t, "turns", got.GetTurns(), []*v1.EpicTurn(nil))
	wantProtos(t, "tasks", got.GetTasks(), []*v1.EpicTask(nil))
	wantProtos(t, "questions", got.GetQuestions(), []*v1.EpicQuestion(nil))
}
