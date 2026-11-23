package enginehost

import (
	"database/sql"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"github.com/joshsymonds/orrery/internal/evidence"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
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
				Files: []string{"a.go", "b.go"}, Requirements: []string{"R1", "R2"}, Gate: "check",
				Evidence: evidence.Item{
					Capture: []string{"scripts/check"}, Limit: 60, Artifacts: []string{"out/one.txt"},
					Conclusion: "Synthetic conclusion one.", Judge: "Synthetic judge one.",
				},
			},
			{
				Title: "Synthetic task two.", Deliverable: "Synthetic deliverable two.",
				Files: []string{"c.go"}, Requirements: []string{"R3"}, Gate: "race",
				Evidence: evidence.Item{
					Supplied: []string{"docs/two.md"}, Conclusion: "Synthetic conclusion two.",
					Judge: "Synthetic judge two.",
				},
			},
		},
		Outline:   "Synthetic outline.",
		Questions: []store.EpicQuestionInput{syntheticQuestion("synthetic-question-one", "Synthetic question one?")},
		Outcome:   "Synthetic outcome.",
	}
}

// syntheticQuestion returns a question with the label and text given that passes store.QuestionRefusals: two synthetic
// choices, the first recommended with a low confidence.
func syntheticQuestion(label, text string) store.EpicQuestionInput {
	return store.EpicQuestionInput{
		Label: label, Text: text,
		Choices: []store.EpicChoice{
			{ID: "synthetic-a", Does: "Synthetic choice A."},
			{ID: "synthetic-b", Does: "Synthetic choice B."},
		},
		Recommended: "synthetic-a", Why: "Synthetic reason.", Confidence: "low",
	}
}

// openPlannerStore opens a store in a temporary directory, as epicTestStore does, and returns it with its file path.
func openPlannerStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	st, err := store.Open(t.Context(), path, storetest.NoSync())
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

// execStoreFile runs query with args on the store file at path through its own connection, beside the store that
// holds it open.
func execStoreFile(t *testing.T, path, query string, args ...any) {
	t.Helper()
	dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: "_busy_timeout=200&_journal_mode=WAL"}).String()
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open store file: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close store file: %v", err)
		}
	}()
	if _, err = db.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

// insertPendingTrigger writes a pending trigger with id on epicServiceID into the store file at path. The store
// adds only the created trigger, and a completed turn consumes it, so a second planner turn needs this row.
func insertPendingTrigger(t *testing.T, path, id string) {
	t.Helper()
	execStoreFile(t, path,
		`INSERT INTO epic_triggers (epic, id, cause, state, turn, created) VALUES (?, ?, ?, 'pending', 0, ?)`,
		epicServiceID, id, "synthetic trigger", turnStarted,
	)
}

// setQuestionAnswer marks question seq of epicServiceID answered with choice in the store file at path. AnswerEpic
// names no choice yet, so the row is written directly, the way an answer with a declared choice leaves it.
func setQuestionAnswer(t *testing.T, path string, seq int64, choice string) {
	t.Helper()
	execStoreFile(t, path,
		`UPDATE epic_questions SET state = 'answered', choice = ? WHERE epic = ? AND seq = ?`,
		choice, epicServiceID, seq,
	)
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
	t.Parallel()
	ctx := t.Context()
	st, path := openPlannerStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createEpicOK(t, st, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))

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
			Files: []string{"a.go", "b.go"}, Requirements: []string{"R1", "R2"}, Gate: "check",
			Evidence: &v1.EvidenceItem{
				Capture: []string{"scripts/check"}, Limit: 60, Artifacts: []string{"out/one.txt"},
				Conclusion: "Synthetic conclusion one.", Judge: "Synthetic judge one.",
			},
			Status: "open", Version: 1, Source: "planner",
		},
		{
			Id: "T2", Turn: 1, Title: "Synthetic task two.", Deliverable: "Synthetic deliverable two.",
			Files: []string{"c.go"}, Requirements: []string{"R3"}, Gate: "race",
			Evidence: &v1.EvidenceItem{
				Supplied: []string{"docs/two.md"}, Conclusion: "Synthetic conclusion two.",
				Judge: "Synthetic judge two.",
			},
			Status: "open", Version: 1, Source: "planner",
		},
	})
	wantProtos(t, "questions", got.GetQuestions(), []*v1.EpicQuestion{
		syntheticQuestionMessage("Q1", 1, "synthetic-question-one", "Synthetic question one?"),
		parkQuestionMessage("Q2", "needs a human"),
	})
}

// syntheticQuestionMessage returns the EpicQuestion message of the open question id, asked at turn, that
// syntheticQuestion(label, text) stores.
func syntheticQuestionMessage(id string, turn int64, label, text string) *v1.EpicQuestion {
	return &v1.EpicQuestion{
		Id: id, Turn: turn, Text: text, State: "open", Label: label,
		Choices: []*v1.EpicQuestionChoice{
			{Id: "synthetic-a", Does: "Synthetic choice A."},
			{Id: "synthetic-b", Does: "Synthetic choice B."},
		},
		Recommendation: &v1.EpicRecommendation{Choice: "synthetic-a", Why: "Synthetic reason.", Confidence: "low"},
	}
}

// parkQuestionMessage returns the EpicQuestion message of the open park question id with text, which a park that is not
// final opens: one declared choice, retry, recommended with a low confidence.
func parkQuestionMessage(id, text string) *v1.EpicQuestion {
	return &v1.EpicQuestion{
		Id: id, Turn: 0, Text: text, State: "open", Label: "park",
		Choices: []*v1.EpicQuestionChoice{{
			Id:   "retry",
			Does: "end the park and start a turn on the waiting triggers, as orrery epic retry does",
		}},
		Recommendation: &v1.EpicRecommendation{
			Choice:     "retry",
			Why:        "no model judges a park; retry runs the epic again on its waiting triggers",
			Confidence: "low",
		},
	}
}

// TestEpicGetShowsMetAtBaseQuestionWithItsThreeChoices checks that a red run judged holding at base parks the epic with
// a park question that offers retry, drop and end over the Requirement it found met, and recommends retry. R4: the park
// used to be final, with no choice and no recommendation, and set the phase to met_at_base.
func TestEpicGetShowsMetAtBaseQuestionWithItsThreeChoices(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, _ := openPlannerStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createEpicOK(t, st, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	runs := []store.NodeRun{{Path: "red", ID: "id-red", Node: "red", Kind: "evidence", Attempt: 1}}
	if err := st.StartNodes(ctx, epicServiceID, runs, nil, time.Unix(turnStarted, 0)); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}
	const reason = "workflow node red: R1 met at the base; you can retry, drop or end"
	if err := st.ParkNodeMetAtBase(ctx, epicServiceID, "red", 1, "failed", "R1 red: failed", reason,
		[]string{"R1"}, time.Unix(turnFreed, 0)); err != nil {
		t.Fatalf("ParkNodeMetAtBase: %v", err)
	}

	got := getEpicOK(t, client)
	if got.GetParked() != reason || got.GetPhase() != "accepted" {
		t.Fatalf("parked and phase = %q and %q, want %q and accepted", got.GetParked(), got.GetPhase(), reason)
	}
	wantProtos(t, "questions", got.GetQuestions(), []*v1.EpicQuestion{{
		Id: "Q1", Turn: 0, Text: reason, State: "open", Label: "park",
		Choices: []*v1.EpicQuestionChoice{
			{Id: "retry", Does: "run the red check of R1 again with a fresh judge"},
			{Id: "drop", Does: "drop R1 as already met, recorded as your decision, and go on without their red"},
			{Id: "end", Does: "end the epic"},
		},
		Recommendation: &v1.EpicRecommendation{
			Choice: "retry",
			Why: "the judge read the conclusion as already holding at the base, " +
				"and a fresh judge checks that reading before anything is dropped",
			Confidence: "medium",
		},
	}})
}

func TestEpicGetShowsQuestionReplacedByAnother(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, path := openPlannerStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createEpicOK(t, st, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	askQuestions(t, st, "Synthetic question one?")
	insertPendingTrigger(t, path, "t2")
	second, err := st.ClaimTurn(ctx, epicServiceID, time.Unix(turnRestarted, 0))
	if err != nil {
		t.Fatalf("ClaimTurn of second turn: %v", err)
	}
	replacing := syntheticQuestion("synthetic-question-two", "Synthetic question two?")
	replacing.Replaces = "Q1"
	plan := plannedReturn()
	plan.Questions = []store.EpicQuestionInput{replacing}
	if err = st.CompleteTurn(ctx, epicServiceID, second.Seq, plan, time.Unix(turnFreed, 0)); err != nil {
		t.Fatalf("CompleteTurn of second turn: %v", err)
	}

	replaced := syntheticQuestionMessage("Q1", 1, "synthetic-question-1", "Synthetic question one?")
	replaced.State, replaced.ReplacedBy = "replaced", "Q2"
	successor := syntheticQuestionMessage("Q2", 2, "synthetic-question-two", "Synthetic question two?")
	successor.Replaces = "Q1"
	wantProtos(t, "questions", getEpicOK(t, client).GetQuestions(), []*v1.EpicQuestion{replaced, successor})
}

func TestEpicFollowsCountsAnsweredRecommendationsPerLabel(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, path := openPlannerStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createEpicOK(t, st, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	turn, err := st.ClaimTurn(ctx, epicServiceID, time.Unix(turnStarted, 0))
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	plan := plannedReturn()
	plan.Questions = []store.EpicQuestionInput{
		syntheticQuestion("which-store", "Which store holds the plan?"),
		syntheticQuestion("tick-rate", "How often does the runner tick?"),
		syntheticQuestion("tick-rate", "How often does the planner tick?"),
		syntheticQuestion("tick-rate", "How often does the notifier tick?"),
	}
	if err = st.CompleteTurn(ctx, epicServiceID, turn.Seq, plan, time.Unix(turnCompleted, 0)); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	// Q1 and Q3 are answered as recommended, Q2 with the other declared choice, and Q4 stays open.
	answers := []struct {
		seq    int64
		choice string
	}{{1, "synthetic-a"}, {2, "synthetic-b"}, {3, "synthetic-a"}}
	for _, answer := range answers {
		setQuestionAnswer(t, path, answer.seq, answer.choice)
	}

	reply, err := client.FollowEpics(ctx, connect.NewRequest(&v1.FollowEpicsRequest{}))
	if err != nil {
		t.Fatalf("FollowEpics: %v", err)
	}
	wantProtos(t, "follows", reply.Msg.GetFollows(), []*v1.EpicFollow{
		{Label: "tick-rate", Answered: 2, Followed: 1},
		{Label: "which-store", Answered: 1, Followed: 1},
	})
}

func TestEpicFollowsOfEmptyStoreIsNone(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	reply, err := client.FollowEpics(t.Context(), connect.NewRequest(&v1.FollowEpicsRequest{}))
	if err != nil {
		t.Fatalf("FollowEpics: %v", err)
	}
	wantProtos(t, "follows", reply.Msg.GetFollows(), []*v1.EpicFollow(nil))
}

func TestEpicGetRunningTurnHasNoEnd(t *testing.T) {
	t.Parallel()
	st, _ := openPlannerStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createEpicOK(t, st, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	if _, err := st.ClaimTurn(t.Context(), epicServiceID, time.Unix(turnStarted, 0)); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}

	wantProtos(t, "turns", getEpicOK(t, client).GetTurns(), []*v1.EpicTurn{{
		Seq: 1, State: "running", Started: turnStarted, Ended: 0,
	}})
}

func TestEpicGetWithNoPlannerStateIsEmpty(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createEpicOK(t, st, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))

	got := getEpicOK(t, client)
	if got.GetPaused() || got.GetParked() != "" {
		t.Fatalf("paused = %t parked = %q, want false and empty", got.GetPaused(), got.GetParked())
	}
	wantProtos(t, "turns", got.GetTurns(), []*v1.EpicTurn(nil))
	wantProtos(t, "tasks", got.GetTasks(), []*v1.EpicTask(nil))
	wantProtos(t, "questions", got.GetQuestions(), []*v1.EpicQuestion(nil))
}
