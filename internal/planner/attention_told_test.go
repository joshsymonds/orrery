package planner

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/route"
	"github.com/joshsymonds/orrery/internal/store"
)

// absentDesk is the DeskSink of the tests that route to the phone. The desk is never at hand, so a notice reaches the
// phone at once, and what it posts to the desk is dropped.
type absentDesk struct{}

// Post does nothing.
func (absentDesk) Post(deskstream.Notice) {}

// Desk reports the desk as absent.
func (absentDesk) Desk() (int64, bool) {
	return 0, false
}

// sendingPhone is the PhoneSink of the tests that route a push and then restart. Each push handed to it keeps its sent
// callback in handed, uncalled, so a test decides when the phone publishes it.
type sendingPhone struct {
	handed []func()
}

// Notify keeps sent.
func (p *sendingPhone) Notify(_ context.Context, _ notice.Key, _ func(context.Context) (push.Notification, error),
	sent func()) {
	p.handed = append(p.handed, sent)
}

// Seen does nothing.
func (p *sendingPhone) Seen(context.Context, notice.Key, int64) {}

// routerWith returns a route.Router attached to s, as a started daemon has, that posts to desk and hands pushes to
// phone.
func routerWith(t *testing.T, s *store.Store, desk route.DeskSink, phone route.PhoneSink) *route.Router {
	t.Helper()
	router := route.New(route.Config{
		Now:        claimedAt,
		After:      neverFires,
		Desk:       desk,
		Phone:      phone,
		Subscribed: func() (bool, error) { return true, nil },
		Log:        discardLines{},
		Rebuild:    map[string]route.Rebuild{EpicSource: RestoreNotice},
	})
	router.Attach(context.Background(), s)
	return router
}

// finishedAcrossRestart runs one attention pass over refuse, routed to the phone of a first router that is killed
// before the push's publish report. A restarted router resends the push, and the phone's publish report then deletes
// the row. It returns the restarted router and the phone that received the resend.
func finishedAcrossRestart(t *testing.T, s *store.Store, refuse *refusingMarkStore) (*route.Router, *sendingPhone) {
	t.Helper()
	ctx := context.Background()
	first := &sendingPhone{}
	refusedPass(ctx, routerWith(t, s, absentDesk{}, first), refuse)
	if len(first.handed) != 1 {
		t.Fatalf("pushes handed to the phone before the restart = %d, want one", len(first.handed))
	}

	resend := &sendingPhone{}
	restarted := routerWith(t, s, absentDesk{}, resend)
	if len(resend.handed) != 1 {
		t.Fatalf("pushes resent after the restart = %d, want one", len(resend.handed))
	}
	resend.handed[0]()
	restarted.Wait()
	rows, err := s.Notices(ctx)
	if err != nil {
		t.Fatalf("Notices: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("stored rows after the publish report = %d, want none", len(rows))
	}
	return restarted, resend
}

func TestRouterFirstRowWriteOfAMarkedNoticeLeavesItsTurnTold(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	turn := claimAttentionTurn(t, s)
	if err := s.CompleteTurn(ctx, runnerEpicID, turn, askingTurn(), claimedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	stored := epicNotice{
		ID: turnNoticeID(runnerEpicID, turn, needQuestions), Epic: runnerEpicID, Title: "Plan the runner",
		Body: "Q1: which store?",
	}
	data, err := json.Marshal(stored)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	incoming := attentionNotice(stored)
	incoming.Source = route.Source{Kind: EpicSource, Data: data}
	incoming.Told = &store.ToldMark{Epic: runnerEpicID, Seq: turn}
	router := routerWith(t, s, &postedDesk{}, &sendingPhone{})

	router.Notify(ctx, incoming)
	router.Wait()

	if turns := epicOf(t, s).Turns; len(turns) != 1 || !turns[0].Told {
		t.Errorf("turns after the marked notice's first row write = %+v, want the turn told", turns)
	}
}

func TestAttentionMarksTheLatestTurnToldInTheWriteOfItsNotice(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	turn := claimAttentionTurn(t, s)
	if err := s.CompleteTurn(ctx, runnerEpicID, turn, askingTurn(), claimedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}

	refusedPass(ctx, storedRouter(t, s, &postedDesk{}), &refusingMarkStore{Store: s, refuseTurn: true})

	if turns := epicOf(t, s).Turns; len(turns) != 1 || !turns[0].Told {
		t.Errorf("turns after a pass whose MarkTold is refused = %+v, want the turn told by its notice's write", turns)
	}
}

func TestAttentionMarksEachTellToldInTheWriteOfItsNotice(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	endWorkTurn(t, s, claimedAt())
	tellRunner(t, s, "tell", claimedAt().Add(time.Second))

	refusedPass(ctx, storedRouter(t, s, &postedDesk{}), &refusingMarkStore{Store: s, refuseTell: true})

	if tells := untoldTellsOfRunner(t, s); len(tells) != 0 {
		t.Errorf("untold tells after a pass whose MarkTellTold is refused = %+v, want none", tells)
	}
}

func TestAttentionDoesNotRouteATurnAgainAfterARestartFinishedItsNotice(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	turn := claimAttentionTurn(t, s)
	if err := s.CompleteTurn(ctx, runnerEpicID, turn, askingTurn(), claimedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	restarted, resend := finishedAcrossRestart(t, s, &refusingMarkStore{Store: s, refuseTurn: true})

	attentionOver(s, restarted).pass(ctx)
	restarted.Wait()

	if n := len(resend.handed); n != 1 {
		t.Errorf("pushes handed to the phone after the next pass = %d, want still one", n)
	}
}

func TestAttentionDoesNotRouteATellAgainAfterARestartFinishedItsNotice(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	endWorkTurn(t, s, claimedAt())
	tellRunner(t, s, "tell", claimedAt().Add(time.Second))
	restarted, resend := finishedAcrossRestart(t, s, &refusingMarkStore{Store: s, refuseTell: true})

	attentionOver(s, restarted).pass(ctx)
	restarted.Wait()

	if n := len(resend.handed); n != 1 {
		t.Errorf("pushes handed to the phone after the next pass = %d, want still one", n)
	}
	if tells := untoldTellsOfRunner(t, s); len(tells) != 0 {
		t.Errorf("untold tells after the next pass = %+v, want none", tells)
	}
}
