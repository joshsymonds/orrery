package planner

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// steererSession is the session that steers the runner epic in the tests that steer it.
const steererSession = "engine-1"

// escalationReason is the reason the tests escalate their question for.
const escalationReason = "synthetic reason"

// askedTellText is the text of the tell that carries the orchestrator's question in the tests that ask one.
const askedTellText = "the capture is broken"

// oneQuestionBody is the body of the notice of a turn that asks one question, which-store: its head and advice lines.
const oneQuestionBody = `Q1 which-store: which store?
  first: Synthetic first choice.
  second: Synthetic second choice.
  other: answer in your own words
  recommended: first (medium): Synthetic reason.`

// escalatedOneQuestionBody is the body of the escalation of which-store by the steerer, for escalationReason.
const escalatedOneQuestionBody = "escalated by conversation engine-1: synthetic reason\n" + oneQuestionBody

// askedTellBody is the body of the tell that carries an orchestrator's question to the owner, capture-broken: the
// tell's text, then the question's advice.
const askedTellBody = askedTellText + "\n" + `Q1 capture-broken:
  first: Synthetic first choice.
  second: Synthetic second choice.
  other: answer in your own words
  recommended: first (medium): Synthetic reason.`

// escalatedAskedBody is the body of the escalation of capture-broken by the steerer, for escalationReason.
const escalatedAskedBody = "escalated by conversation engine-1: synthetic reason\n" + `Q1 capture-broken: which capture?
  first: Synthetic first choice.
  second: Synthetic second choice.
  other: answer in your own words
  recommended: first (medium): Synthetic reason.`

// steerRunner adds steererSession as an engine session in state, then makes it steer the runner epic, so the steerer
// has a sessions row in that state.
func steerRunner(t *testing.T, s *store.Store, state string) {
	t.Helper()
	if err := s.ApplyEngine(
		context.Background(),
		[]store.EngineOp{store.EngineAdd{ID: steererSession, Dir: runnerRepo, State: state}},
		claimedAt(),
	); err != nil {
		t.Fatalf("ApplyEngine add %s: %v", steererSession, err)
	}
	moveRunner(t, s, steererSession)
}

// moveRunner hands the runner epic to session, as the owner does.
func moveRunner(t *testing.T, s *store.Store, session string) {
	t.Helper()
	if _, err := s.MoveEpic(
		context.Background(), runnerEpicID, session, store.Maker{By: store.MakerOwner}, claimedAt(),
	); err != nil {
		t.Fatalf("MoveEpic to %s: %v", session, err)
	}
}

// steerRunnerByPane makes a pane the steerer of the runner epic, the pane of a sessions row that SyncServer can remove.
func steerRunnerByPane(t *testing.T, s *store.Store) {
	t.Helper()
	if err := s.SyncPane(context.Background(), "-L orrery", store.Pane{
		ID: "orrery-1", Pane: "%1", Fields: store.Fields{State: "idle"},
	}, claimedAt()); err != nil {
		t.Fatalf("SyncPane: %v", err)
	}
	moveRunner(t, s, "orrery-1")
}

// removeRunnerPanes takes every pane off the server of the runner's steerer, so its sessions row is removed.
func removeRunnerPanes(t *testing.T, s *store.Store) {
	t.Helper()
	if err := s.SyncServer(context.Background(), "-L orrery", nil, claimedAt().Add(time.Minute)); err != nil {
		t.Fatalf("SyncServer: %v", err)
	}
}

// runnerEntry returns the attention entry of the runner epic.
func runnerEntry(t *testing.T, s *store.Store) store.EpicAttention {
	t.Helper()
	attention, err := s.Attention(context.Background())
	if err != nil {
		t.Fatalf("Attention: %v", err)
	}
	for _, entry := range attention {
		if entry.Epic == runnerEpicID {
			return entry
		}
	}
	t.Fatalf("Attention has no entry for %s", runnerEpicID)
	return store.EpicAttention{}
}

// askOneQuestion completes the claimed turn of the runner epic with one question, which-store, and no task.
func askOneQuestion(t *testing.T, s *store.Store) {
	t.Helper()
	turn := claimAttentionTurn(t, s)
	plan := store.EpicPlan{
		Questions: []store.EpicQuestionInput{shapedQuestion("which-store", "which store?")}, Outcome: "wait",
	}
	if err := s.CompleteTurn(context.Background(), runnerEpicID, turn, plan, claimedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
}

// askRunnerOwner asks the owner a question, capture-broken, on task 1 of the runner epic, as an orchestrator does: the
// task holds on the question, and a tell with askedTellText carries it. The task must be built by endWorkTurn first.
func askRunnerOwner(t *testing.T, s *store.Store) {
	t.Helper()
	question := shapedQuestion("capture-broken", "which capture?")
	to := store.TaskBuild{Held: "asked", Decisions: 1}
	decision := store.TaskDecision{ID: "T1-request-1", Text: "T1 rerun: synthetic reason"}
	if err := s.AskTask(
		context.Background(), runnerEpicID, 1, store.TaskBuild{}, to, question, askedTellText, decision,
		claimedAt().Add(time.Second),
	); err != nil {
		t.Fatalf("AskTask: %v", err)
	}
}

// escalateRunnerQuestion escalates the question Q1 of the runner epic, for escalationReason, made by maker.
func escalateRunnerQuestion(t *testing.T, s *store.Store, maker store.Maker) {
	t.Helper()
	if _, err := s.EscalateQuestion(
		context.Background(), runnerEpicID, 1, escalationReason, maker, claimedAt(),
	); err != nil {
		t.Fatalf("EscalateQuestion Q1: %v", err)
	}
}

// attendRunner passes attention once over s and returns the router that received its notices.
func attendRunner(s *store.Store) *fakeAttentionRouter {
	router := &fakeAttentionRouter{}
	attentionOver(s, router).pass(context.Background())
	return router
}

// requireOneQuestionNotice passes attention once and fails the test unless exactly one notice routes, the question
// notice of oneQuestionBody. what names the case in the failure message.
func requireOneQuestionNotice(t *testing.T, s *store.Store, what string) {
	t.Helper()
	router := attendRunner(s)
	if len(router.notices) != 1 {
		t.Fatalf("notices for %s = %d, want exactly one", what, len(router.notices))
	}
	if got := shownOf(t, router.notices[0]).desk.Body; got != oneQuestionBody {
		t.Errorf("question notice body = %q, want %q", got, oneQuestionBody)
	}
}

func TestAttentionEscalate_HeardQuestionNotNotified(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steerRunner(t, s, "working")
	askOneQuestion(t, s)

	router := attendRunner(s)

	if n := len(router.notices); n != 0 {
		t.Fatalf("notices for a question on a heard epic = %d, want none", n)
	}
	if !runnerEntry(t, s).Latest.Told {
		t.Errorf("the turn that asked the question is not marked told, want it marked told")
	}
}

func TestAttentionEscalate_NoSteererNotifies(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	askOneQuestion(t, s)

	requireOneQuestionNotice(t, s, "a question on an epic with no steerer")
}

func TestAttentionEscalate_SteererRowGoneNotifies(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steerRunnerByPane(t, s)
	removeRunnerPanes(t, s)
	askOneQuestion(t, s)

	requireOneQuestionNotice(t, s, "a question whose steerer's row is gone")
}

func TestAttentionEscalate_SteererStoppedNotifies(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steerRunner(t, s, "stopped")
	askOneQuestion(t, s)

	requireOneQuestionNotice(t, s, "a question whose steerer is stopped")
}

func TestAttentionEscalate_EscalatedQuestionNotifiesOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steerRunner(t, s, "working")
	askOneQuestion(t, s)
	escalateRunnerQuestion(t, s, store.Maker{By: store.MakerConversation, Session: steererSession})
	router := &fakeAttentionRouter{}
	attention := attentionOver(s, router)

	attention.pass(ctx)

	if len(router.notices) != 1 {
		t.Fatalf("notices after one pass = %d, want exactly one escalation notice", len(router.notices))
	}
	got := shownOf(t, router.notices[0])
	if got.desk.Body != escalatedOneQuestionBody {
		t.Errorf("escalation notice body = %q, want %q", got.desk.Body, escalatedOneQuestionBody)
	}
	if got.desk.Block != blockQuestion || got.desk.BlockRef != "Q1" {
		t.Errorf("escalation notice opens on %q %q, want question Q1", got.desk.Block, got.desk.BlockRef)
	}
	if !runnerEntry(t, s).Open[0].EscalationTold {
		t.Errorf("the escalation is not marked told, want it marked told")
	}

	attention.pass(ctx)
	attentionOver(s, router).pass(ctx)

	if n := len(router.notices); n != 1 {
		t.Errorf("notices after a second pass and a restarted Attention = %d, want still one", n)
	}
}

func TestAttentionEscalate_LateEscalationAfterToldNotifiesOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	askOneQuestion(t, s)
	router := &fakeAttentionRouter{}
	attention := attentionOver(s, router)
	attention.pass(ctx)
	if len(router.notices) != 1 {
		t.Fatalf("notices before the escalation = %d, want the turn's notice only", len(router.notices))
	}
	escalateRunnerQuestion(t, s, store.Maker{By: store.MakerOwner})

	attention.pass(ctx)

	if len(router.notices) != 2 {
		t.Fatalf("notices after the escalation = %d, want one escalation notice", len(router.notices))
	}
	want := "escalated by the owner: synthetic reason\n" + oneQuestionBody
	if got := shownOf(t, router.notices[1]).desk.Body; got != want {
		t.Errorf("escalation notice body = %q, want %q", got, want)
	}
	if !runnerEntry(t, s).Open[0].EscalationTold {
		t.Errorf("the escalation is not marked told, want it marked told")
	}

	attention.pass(ctx)

	if n := len(router.notices); n != 2 {
		t.Errorf("notices after a second pass = %d, want still two", n)
	}
}

func TestAttentionEscalate_HeardQuestionTellNotNotified(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steerRunner(t, s, "working")
	endWorkTurn(t, s, claimedAt())
	askRunnerOwner(t, s)

	router := attendRunner(s)

	if n := len(router.notices); n != 0 {
		t.Fatalf("notices for an orchestrator's question on a heard epic = %d, want none", n)
	}
	if tells := untoldTellsOfRunner(t, s); len(tells) != 0 {
		t.Errorf("untold tells after the pass = %+v, want the question's tell marked told", tells)
	}
}

func TestAttentionEscalate_EscalatedQuestionTellNotifies(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steerRunner(t, s, "working")
	endWorkTurn(t, s, claimedAt())
	askRunnerOwner(t, s)
	escalateRunnerQuestion(t, s, store.Maker{By: store.MakerConversation, Session: steererSession})

	router := attendRunner(s)

	// A tell routes before an escalation, so the tell's notice comes first.
	if len(router.notices) != 2 {
		t.Fatalf("notices for an escalated question's tell = %d, want the tell and the escalation", len(router.notices))
	}
	if got := shownOf(t, router.notices[0]).desk.Body; got != askedTellBody {
		t.Errorf("tell notice body = %q, want %q", got, askedTellBody)
	}
	if got := shownOf(t, router.notices[1]).desk.Body; got != escalatedAskedBody {
		t.Errorf("escalation notice body = %q, want %q", got, escalatedAskedBody)
	}
}

func TestAttentionEscalate_EscalationMarkIsWrittenWithItsNoticeRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	askOneQuestion(t, s)
	escalateRunnerQuestion(t, s, store.Maker{By: store.MakerOwner})

	// A notice row whose told mark names the question's escalation marks it in the write that stores the row.
	if err := s.PutNotice(ctx, store.NoticeRow{
		ID: 1, Epic: runnerEpicID, Told: &store.ToldMark{Epic: runnerEpicID, Seq: 1, Escalation: true},
	}); err != nil {
		t.Fatalf("PutNotice: %v", err)
	}
	if !runnerEntry(t, s).Open[0].EscalationTold {
		t.Errorf("the escalation is not marked told by its notice row, want it marked told")
	}

	// A mark that names no question fails the write, and stores no row.
	err := s.PutNotice(ctx, store.NoticeRow{
		ID: 2, Epic: runnerEpicID, Told: &store.ToldMark{Epic: runnerEpicID, Seq: 9, Escalation: true},
	})
	if !errors.Is(err, store.ErrNoQuestion) {
		t.Errorf("PutNotice marking a question that does not exist = %v, want ErrNoQuestion", err)
	}
	notices, err := s.Notices(ctx)
	if err != nil {
		t.Fatalf("Notices: %v", err)
	}
	if len(notices) != 1 {
		t.Errorf("stored notices after the refused write = %d, want only the first", len(notices))
	}
}

func TestAttentionEscalate_ParkLimitAndPlainTellUnchanged(t *testing.T) {
	t.Parallel()
	t.Run("park", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		s := openRunnerStore(t)
		createRunnerEpic(t, s)
		steerRunner(t, s, "working")
		turn := claimAttentionTurn(t, s)
		if err := s.FreeTurn(ctx, runnerEpicID, turn, store.TurnFree{
			Reason: "credit ran out", Park: "credit ran out",
		}, claimedAt()); err != nil {
			t.Fatalf("FreeTurn: %v", err)
		}

		router := attendRunner(s)

		if len(router.notices) != 1 {
			t.Fatalf("notices for a park on a heard epic = %d, want exactly one", len(router.notices))
		}
		if got := shownOf(t, router.notices[0]).desk.Body; got != creditParkBody {
			t.Errorf("park notice body = %q, want %q", got, creditParkBody)
		}
	})

	t.Run("limit", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		s := openRunnerStore(t)
		createRunnerEpic(t, s)
		steerRunner(t, s, "working")
		turn := claimAttentionTurn(t, s)
		if err := s.FreeTurn(ctx, runnerEpicID, turn, store.TurnFree{
			Reason: "usage limit", Pause: true, Resets: time.Unix(limitResetsUnix, 0),
		}, claimedAt()); err != nil {
			t.Fatalf("FreeTurn: %v", err)
		}

		router := attendRunner(s)

		if len(router.notices) != 1 {
			t.Fatalf("notices for a limit on a heard epic = %d, want exactly one", len(router.notices))
		}
		want := "usage limit, resets 2023-11-14T23:13:20Z"
		if got := shownOf(t, router.notices[0]).push.Summary; got != want {
			t.Errorf("limit notice summary = %q, want %q", got, want)
		}
	})

	t.Run("plain_tell", func(t *testing.T) {
		t.Parallel()
		s := openRunnerStore(t)
		createRunnerEpic(t, s)
		steerRunner(t, s, "working")
		endWorkTurn(t, s, claimedAt())
		tellRunner(t, s, "plain tell", claimedAt().Add(time.Second))

		router := attendRunner(s)

		if len(router.notices) != 1 {
			t.Fatalf("notices for a tell with no question on a heard epic = %d, want exactly one", len(router.notices))
		}
		if got := shownOf(t, router.notices[0]).desk.Body; got != "plain tell" {
			t.Errorf("tell notice body = %q, want %q", got, "plain tell")
		}
	})
}
