package planner

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/store"
)

// tellingStore is the AttentionStore of the test that refuses a tell's mark while failMark is set. Its other methods
// are the real store's.
type tellingStore struct {
	*store.Store
	failMark bool
}

// MarkTellTold marks the tell in the real store, or fails while failMark is set.
func (s *tellingStore) MarkTellTold(ctx context.Context, epic string, seq int64) error {
	if s.failMark {
		return errors.New("mark refused")
	}
	return s.Store.MarkTellTold(ctx, epic, seq)
}

// plannedRunnerTasks is the one task a turn of the runner epic plans, so the epic has a planned task its tells are
// written to.
func plannedRunnerTasks() []store.EpicTaskInput {
	return []store.EpicTaskInput{{
		Title: "Build it", Deliverable: "the runner", Files: []string{"internal/planner/runner.go"},
		Requirements: []string{"R1"}, Gate: "check", Evidence: runnerEvidenceItem(),
	}}
}

// endWorkTurn claims the pending turn of the runner epic and completes it with one planned task and no questions at
// ended.
func endWorkTurn(t *testing.T, s *store.Store, ended time.Time) {
	t.Helper()
	turn := claimAttentionTurn(t, s)
	plan := store.EpicPlan{Outcome: "work", Tasks: plannedRunnerTasks()}
	if err := s.CompleteTurn(context.Background(), runnerEpicID, turn, plan, ended); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
}

// tellRunner adds text to the runner epic as a tell written at at, through the write the ref check tells through, with
// task 1 kept at the build it has.
func tellRunner(t *testing.T, s *store.Store, text string, at time.Time) {
	t.Helper()
	build := store.TaskBuild{}
	if err := s.SetTaskBuildTelling(
		context.Background(), runnerEpicID, 1, build, build, []string{text}, at,
	); err != nil {
		t.Fatalf("SetTaskBuildTelling %q: %v", text, err)
	}
}

// untoldTellsOfRunner returns the tells of the runner epic that Attention reports as untold.
func untoldTellsOfRunner(t *testing.T, s *store.Store) []store.EpicTell {
	t.Helper()
	attention, err := s.Attention(context.Background())
	if err != nil {
		t.Fatalf("Attention: %v", err)
	}
	for _, entry := range attention {
		if entry.Epic == runnerEpicID {
			return entry.Tells
		}
	}
	t.Fatalf("Attention has no entry for %s", runnerEpicID)
	return nil
}

func TestAttentionRoutesEachUntoldTellAsTheEpicsNoticeInSeqOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	endWorkTurn(t, s, claimedAt())
	tellRunner(t, s, "first tell", claimedAt().Add(time.Second))
	tellRunner(t, s, "second tell", claimedAt().Add(2*time.Second))
	router := &fakeAttentionRouter{}

	attentionOver(s, router).pass(ctx)

	if len(router.notices) != 2 {
		t.Fatalf("notices after one pass = %d, want one per tell", len(router.notices))
	}
	for i, want := range []string{"first tell", "second tell"} {
		got := shownOf(t, router.notices[i])
		if got.key != (notice.Key{Epic: runnerEpicID}) || got.desk.Body != want {
			t.Errorf("notice %d = key %+v body %q, want key %+v body %q",
				i, got.key, got.desk.Body, notice.Key{Epic: runnerEpicID}, want)
		}
	}
	if tells := untoldTellsOfRunner(t, s); len(tells) != 0 {
		t.Errorf("untold tells after the pass = %+v, want none", tells)
	}
}

func TestAttentionDoesNotRouteAToldTellAgain(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	endWorkTurn(t, s, claimedAt())
	tellRunner(t, s, "only tell", claimedAt().Add(time.Second))
	router := &fakeAttentionRouter{}
	attention := attentionOver(s, router)

	attention.pass(ctx)
	attention.pass(ctx)
	attentionOver(s, router).pass(ctx)

	if n := len(router.notices); n != 1 {
		t.Errorf("notices after two passes and a restarted Attention = %d, want still one", n)
	}
}

func TestAttentionKeepsTheNoticeUpWhileTheLatestTellIsNewerThanTheLatestEndedTurn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	endWorkTurn(t, s, claimedAt())
	tellRunner(t, s, "tell after the turn", claimedAt().Add(time.Second))
	router := &fakeAttentionRouter{}

	attentionOver(s, router).pass(ctx)

	if len(router.notices) != 1 {
		t.Fatalf("notices after one pass = %d, want exactly one", len(router.notices))
	}
	if len(router.seens) != 0 {
		t.Errorf("seen reports = %+v, want none while the latest tell is newer than the latest turn's end",
			router.seens)
	}
}

func TestAttentionClearsTheNoticeOnceATurnEndsAfterTheLatestTell(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	// The tell needs a planned task, which only a turn that has ended plans. That earlier turn asks a question, and the
	// answer makes the turn that ends after the tell claimable.
	turn := claimAttentionTurn(t, s)
	if err := s.CompleteTurn(ctx, runnerEpicID, turn, store.EpicPlan{
		Questions: []store.EpicQuestionInput{shapedQuestion("which-store", "which store?")},
		Tasks:     plannedRunnerTasks(),
		Outcome:   "work",
	}, claimedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	if _, err := s.AnswerQuestion(ctx, runnerEpicID, "answer-1", "use the store", 1, claimedAt()); err != nil {
		t.Fatalf("AnswerQuestion: %v", err)
	}
	// The tell is newer than the earlier turn's end, so only the turn that ends after it clears the notice.
	tellRunner(t, s, "tell before the turn", claimedAt().Add(time.Second))
	endWorkTurn(t, s, claimedAt().Add(2*time.Second))
	router := &fakeAttentionRouter{}

	attentionOver(s, router).pass(ctx)

	if len(router.notices) != 1 {
		t.Fatalf("notices after one pass = %d, want the tell routed once", len(router.notices))
	}
	want := attentionSeen{key: notice.Key{Epic: runnerEpicID}, seen: claimedAt().Unix()}
	if len(router.seens) != 1 || router.seens[0] != want {
		t.Errorf("seen reports = %+v, want exactly [%+v]", router.seens, want)
	}
}

func TestAttentionRoutesATellAgainWhenMarkingItToldFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	endWorkTurn(t, s, claimedAt())
	tellRunner(t, s, "tell", claimedAt().Add(time.Second))
	failing := &tellingStore{Store: s, failMark: true}
	router := &fakeAttentionRouter{}
	attention := NewAttention(AttentionConfig{Store: failing, Router: router, Now: claimedAt})

	attention.pass(ctx)
	if len(router.notices) != 1 {
		t.Fatalf("notices after a pass whose mark fails = %d, want the tell routed once", len(router.notices))
	}
	failing.failMark = false
	attention.pass(ctx)

	if len(router.notices) != 2 {
		t.Fatalf("notices after the mark succeeds = %d, want the tell routed again", len(router.notices))
	}
	if got := shownOf(t, router.notices[1]).desk.Body; got != "tell" {
		t.Errorf("routed again with body %q, want %q", got, "tell")
	}
	if tells := untoldTellsOfRunner(t, s); len(tells) != 0 {
		t.Errorf("untold tells after the mark succeeds = %+v, want none", tells)
	}
}
