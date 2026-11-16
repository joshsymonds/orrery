package planner

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/route"
	"github.com/joshsymonds/orrery/internal/store"
)

// refusingMarkStore is the AttentionStore of the restart tests. While refuseTurn is set its MarkTold fails, and while
// refuseTell is set its MarkTellTold fails, as a kill between a route and its told marker leaves them. Its other
// methods are the real store's.
type refusingMarkStore struct {
	*store.Store
	refuseTurn bool
	refuseTell bool
}

// MarkTold marks the turn in the real store, or fails while refuseTurn is set.
func (s *refusingMarkStore) MarkTold(ctx context.Context, epic string, seq int64) error {
	if s.refuseTurn {
		return errors.New("mark refused")
	}
	return s.Store.MarkTold(ctx, epic, seq)
}

// MarkTellTold marks the tell in the real store, or fails while refuseTell is set.
func (s *refusingMarkStore) MarkTellTold(ctx context.Context, epic string, seq int64) error {
	if s.refuseTell {
		return errors.New("mark refused")
	}
	return s.Store.MarkTellTold(ctx, epic, seq)
}

// postedDesk is the DeskSink of the restart tests. It records each notice posted to it, and the desk is at hand at
// claimedAt, so a notice routed to it stays stored until its escalation, which never fires.
type postedDesk struct {
	posted []deskstream.Notice
}

// Post records dn.
func (d *postedDesk) Post(dn deskstream.Notice) {
	d.posted = append(d.posted, dn)
}

// Desk reports web input at claimedAt, so the desk is at hand.
func (d *postedDesk) Desk() int64 {
	return claimedAt().Unix()
}

// silentPhone is the PhoneSink of the restart tests. No notice escalates to it, so it is never handed a push.
type silentPhone struct{}

// Notify does nothing: no push is handed off.
func (silentPhone) Notify(context.Context, notice.Key, func(context.Context) (push.Notification, error), func()) {
}

// Seen does nothing.
func (silentPhone) Seen(context.Context, notice.Key, int64) {}

// discardLines is the Recorder of the restart tests, which read no notice log.
type discardLines struct{}

// Write discards line.
func (discardLines) Write(route.Line) error {
	return nil
}

// neverFires is the After of the restart tests. A timer it arms never runs.
func neverFires(time.Duration, func()) func() bool {
	return func() bool { return true }
}

// storedRouter returns a route.Router attached to s, as a started daemon has, that posts to desk.
func storedRouter(t *testing.T, s *store.Store, desk *postedDesk) *route.Router {
	t.Helper()
	router := route.New(route.Config{
		Now:        claimedAt,
		After:      neverFires,
		Desk:       desk,
		Phone:      silentPhone{},
		Subscribed: func() (bool, error) { return true, nil },
		Log:        discardLines{},
		Rebuild:    map[string]route.Rebuild{EpicSource: RestoreNotice},
		// The store-call bound never fires: a row write slower than the real bound under load would read as failed and
		// leave no row for the restarted router to restore.
		StoreAfter: neverFires,
	})
	router.Attach(context.Background(), s)
	return router
}

// refusedPass runs one pass of an Attention over refuse, routed to router, and waits for the routing it started.
func refusedPass(ctx context.Context, router *route.Router, refuse *refusingMarkStore) {
	NewAttention(AttentionConfig{Store: refuse, Router: router, Now: claimedAt}).pass(ctx)
	router.Wait()
}

func TestAttentionRoutesALimitOnceAcrossARestartThatFollowsTheRoute(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	turn := claimAttentionTurn(t, s)
	if err := s.FreeTurn(ctx, runnerEpicID, turn, store.TurnFree{
		Reason: "usage limit", Pause: true, Limit: true,
	}, claimedAt()); err != nil {
		t.Fatalf("FreeTurn: %v", err)
	}
	desk := &postedDesk{}
	refusedPass(ctx, storedRouter(t, s, desk), &refusingMarkStore{Store: s, refuseTurn: true})
	if len(desk.posted) != 1 {
		t.Fatalf("notices before the restart = %d, want exactly one", len(desk.posted))
	}

	restarted := storedRouter(t, s, desk)
	attentionOver(s, restarted).pass(ctx)
	restarted.Wait()

	if n := len(desk.posted); n != 1 {
		t.Errorf("notices after the restart = %d, want still one", n)
	}
	if turns := epicOf(t, s).Turns; len(turns) != 1 || !turns[0].Told {
		t.Errorf("turns = %+v, want one turn marked told", turns)
	}
}

func TestAttentionRoutesQuestionsOnceAcrossARestartThatFollowsTheRoute(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	turn := claimAttentionTurn(t, s)
	if err := s.CompleteTurn(ctx, runnerEpicID, turn, askingTurn(), claimedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	desk := &postedDesk{}
	refusedPass(ctx, storedRouter(t, s, desk), &refusingMarkStore{Store: s, refuseTurn: true})
	if len(desk.posted) != 1 {
		t.Fatalf("notices before the restart = %d, want exactly one", len(desk.posted))
	}

	restarted := storedRouter(t, s, desk)
	attentionOver(s, restarted).pass(ctx)
	restarted.Wait()

	if n := len(desk.posted); n != 1 {
		t.Errorf("notices after the restart = %d, want still one", n)
	}
	if turns := epicOf(t, s).Turns; len(turns) != 1 || !turns[0].Told {
		t.Errorf("turns = %+v, want one turn marked told", turns)
	}
}

func TestAttentionRoutesATellOnceAcrossARestartThatFollowsTheRoute(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	endWorkTurn(t, s, claimedAt())
	tellRunner(t, s, "tell", claimedAt().Add(time.Second))
	desk := &postedDesk{}
	refusedPass(ctx, storedRouter(t, s, desk), &refusingMarkStore{Store: s, refuseTell: true})
	if len(desk.posted) != 1 {
		t.Fatalf("notices before the restart = %d, want exactly one", len(desk.posted))
	}

	restarted := storedRouter(t, s, desk)
	attentionOver(s, restarted).pass(ctx)
	restarted.Wait()

	if n := len(desk.posted); n != 1 {
		t.Errorf("notices after the restart = %d, want still one", n)
	}
	if tells := untoldTellsOfRunner(t, s); len(tells) != 0 {
		t.Errorf("untold tells after the restart = %+v, want none", tells)
	}
}

func TestAttentionRoutesALaterTurnAfterARestartThatFollowedARoute(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	turn := claimAttentionTurn(t, s)
	if err := s.CompleteTurn(ctx, runnerEpicID, turn, askingTurn(), claimedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	desk := &postedDesk{}
	refusedPass(ctx, storedRouter(t, s, desk), &refusingMarkStore{Store: s, refuseTurn: true})
	restarted := storedRouter(t, s, desk)
	attentionOver(s, restarted).pass(ctx)
	restarted.Wait()
	if len(desk.posted) != 1 {
		t.Fatalf("notices after the restart = %d, want exactly one", len(desk.posted))
	}

	if _, err := s.AnswerQuestion(ctx, runnerEpicID, "answer-1", "use the store", 1, claimedAt()); err != nil {
		t.Fatalf("AnswerQuestion: %v", err)
	}
	later := claimAttentionTurn(t, s)
	if err := s.CompleteTurn(ctx, runnerEpicID, later, store.EpicPlan{
		Questions: []store.EpicQuestionInput{shapedQuestion("which-ticket", "which ticket?")}, Outcome: "wait",
	}, claimedAt()); err != nil {
		t.Fatalf("CompleteTurn of the later turn: %v", err)
	}

	attentionOver(s, restarted).pass(ctx)
	restarted.Wait()

	if n := len(desk.posted); n != 2 {
		t.Errorf("notices after the later turn = %d, want the later turn's notice routed", n)
	}
}
