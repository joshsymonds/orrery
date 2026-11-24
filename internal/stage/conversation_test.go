package stage

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/enginehost"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// conversationWorking is the working conversation of stageDesign, which the fixture gives it as its creating
// conversation. conversationOther is a session that is not the design's, and conversationNext is the session a
// continuation of the design starts.
const (
	conversationWorking = "conversation-one"
	conversationOther   = "session-conversation-other"
	conversationNext    = "session-conversation-next"
)

// fakeSessions is a SessionStarter that starts each session under id, and records each start and stop. A start stores
// the session's sessions row in the store, as the engine does, and then calls beforeReturn when it is set. stopCtxErr
// is the error of the context Stop was given.
type fakeSessions struct {
	store        *store.Store
	id           string
	err          error
	beforeReturn func()
	starts       []enginehost.StartRequest
	stops        []string
	stopCtxErr   error
}

// Start records req and returns the session's id, or the error the fake was given.
func (f *fakeSessions) Start(ctx context.Context, req enginehost.StartRequest) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.starts = append(f.starts, req)
	add := store.EngineAdd{ID: f.id, Dir: req.Dir}
	if err := f.store.ApplyEngine(ctx, []store.EngineOp{add}, stageResearchNow()); err != nil {
		return "", err
	}
	if f.beforeReturn != nil {
		f.beforeReturn()
	}
	return f.id, nil
}

// Stop records id and the error of ctx.
func (f *fakeSessions) Stop(ctx context.Context, id string) error {
	f.stops = append(f.stops, id)
	f.stopCtxErr = ctx.Err()
	return nil
}

// newConversationFixture returns the fixture of newStageResearchFixture with the sessions row of the working
// conversation, and a Stage service that starts its continuations through a fakeSessions.
func newConversationFixture(t *testing.T) (stageResearchFixture, *fakeSessions) {
	t.Helper()
	fx := newStageResearchFixture(t)
	seedRowsSession(t, fx)
	sessions := &fakeSessions{store: fx.store, id: conversationNext}
	fx.service = NewService(Config{
		Store: fx.store, Steps: fx.starter, Sessions: sessions, Now: stageResearchNow, Getenv: fx.getenv,
	})
	return fx, sessions
}

// seedConversationSession adds the sessions row id to the store of fx.
func seedConversationSession(t *testing.T, fx stageResearchFixture, id string) {
	t.Helper()
	add := store.EngineAdd{ID: id, Dir: "/synthetic/dir"}
	if err := fx.store.ApplyEngine(t.Context(), []store.EngineOp{add}, stageResearchNow()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
}

// continueFrom continues stageDesign through the service of fx, and returns the reply ContinueDesign gives, or the
// error it returns.
func continueFrom(t *testing.T, fx stageResearchFixture) (*v1.ContinueDesignResponse, error) {
	t.Helper()
	reply, err := fx.service.ContinueDesign(t.Context(), connect.NewRequest(&v1.ContinueDesignRequest{
		Design: stageDesign,
	}))
	if err != nil {
		return nil, err
	}
	return reply.Msg, nil
}

// TestRecordsConversationRefusesOtherSession checks that each write to a design or its contracts from a session that is
// not the design's working conversation is refused as permission denied, naming the working conversation, and stores
// nothing.
func TestRecordsConversationRefusesOtherSession(t *testing.T) {
	ctx := t.Context()
	fx, _ := newConversationFixture(t)
	seedConversationSession(t, fx, conversationOther)
	keepStrikeProposal(t, fx)
	recordApproach(t, fx, approachRequestOf(approachKeep, false))

	cases := []struct {
		name string
		call func(session string) error
	}{
		{"Approach", func(session string) error {
			_, err := fx.service.Approach(ctx, fromSession(connect.NewRequest(approachRequestOf(approachDrop, false)),
				session))
			return err
		}},
		{"Choose", func(session string) error {
			_, err := fx.service.Choose(ctx, fromSession(connect.NewRequest(&v1.ChooseRequest{
				Design: stageDesign, Approach: 1, Words: "Keep the scout.",
				Maker: &v1.StageMaker{Kind: store.StageMakerOwnerConversation, Quote: "Keep the scout."},
			}), session))
			return err
		}},
		{"Delegate", func(session string) error {
			_, err := fx.service.Delegate(ctx, fromSession(connect.NewRequest(&v1.DelegateRequest{
				Design: stageDesign, Kinds: []string{"approach"}, Message: "Pick the scout.",
			}), session))
			return err
		}},
		{"WriteRow", func(session string) error {
			_, err := writeRowFrom(t, fx, session, &v1.WriteRowRequest{
				Row: "title", Value: `"Changed"`, Maker: conversationMaker(),
			})
			return err
		}},
		{"Keep", func(session string) error {
			_, err := fx.service.Keep(ctx, fromSession(connect.NewRequest(&v1.KeepRequest{
				Contract: rowsContract, Addition: "A1", Words: "Keep the first", Maker: conversationMaker(),
			}), session))
			return err
		}},
		{"Strike", func(session string) error {
			_, err := fx.service.Strike(ctx, fromSession(connect.NewRequest(&v1.StrikeRequest{
				Contract: rowsContract, Addition: "A2", Words: "Strike the second", Maker: conversationMaker(),
			}), session))
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requireRefusal(t, tc.name, tc.call(conversationOther), connect.CodePermissionDenied, conversationWorking)
		})
	}

	approaches, err := fx.store.DesignApproaches(ctx, stageDesign)
	if err != nil {
		t.Fatalf("DesignApproaches: %v", err)
	}
	if len(approaches) != 1 {
		t.Fatalf("approaches = %+v, want the one recorded from the working conversation", approaches)
	}
	if writes := rowWritesOf(t, fx); len(writes) != 0 {
		t.Fatalf("row writes = %+v, want none from another session", writes)
	}
	if _, err := fx.store.DesignApproachChoice(ctx, stageDesign); !errors.Is(err, store.ErrNoApproachChoice) {
		t.Fatalf("DesignApproachChoice = %v, want no choice from another session", err)
	}
}

// TestRecordsConversationPassesOwnerServe checks that a write from the owner through serve, which resolves to no
// session, passes, as does a write from the design's working conversation.
func TestRecordsConversationPassesOwnerServe(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	fx, _ := newConversationFixture(t)
	keepStrikeProposal(t, fx)

	// The owner's calls name no session header, so they resolve to no session.
	if _, err := fx.service.Approach(ctx, connect.NewRequest(approachRequestOf(approachKeep, false))); err != nil {
		t.Fatalf("owner Approach: %v", err)
	}
	if _, err := fx.service.Choose(ctx, connect.NewRequest(&v1.ChooseRequest{
		Design: stageDesign, Approach: 1, Words: "Keep the scout.",
		Maker: &v1.StageMaker{Kind: store.StageMakerOwnerServe},
	})); err != nil {
		t.Fatalf("owner Choose: %v", err)
	}
	if _, err := writeRowFrom(t, fx, "", &v1.WriteRowRequest{
		Row: "title", Value: `"Owner titled"`, Words: "Title it",
	}); err != nil {
		t.Fatalf("owner WriteRow: %v", err)
	}
	if _, err := fx.service.Keep(ctx, connect.NewRequest(&v1.KeepRequest{
		Contract: rowsContract, Addition: "A1", Words: "Keep the first",
		Maker: &v1.StageMaker{Kind: store.StageMakerOwnerServe},
	})); err != nil {
		t.Fatalf("owner Keep: %v", err)
	}
	if _, err := fx.service.Strike(ctx, connect.NewRequest(&v1.StrikeRequest{
		Contract: rowsContract, Addition: "A2", Words: "Strike the second",
		Maker: &v1.StageMaker{Kind: store.StageMakerOwnerServe},
	})); err != nil {
		t.Fatalf("owner Strike: %v", err)
	}

	// The working conversation itself is admitted.
	if _, err := fx.service.Approach(ctx, fromSession(connect.NewRequest(approachRequestOf(approachDrop, false)),
		conversationWorking)); err != nil {
		t.Fatalf("working conversation Approach: %v", err)
	}
}

// TestRecordsConversationContinueStartsInWorkingDirectory checks that ContinueDesign starts a session in the directory
// of the working conversation's sessions row, with a prompt that names the design, its title, the conversation it
// continues and its log, and that the design then has the new session as its working conversation, with the continued
// line logged.
func TestRecordsConversationContinueStartsInWorkingDirectory(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	fx, sessions := newConversationFixture(t)
	recordApproach(t, fx, approachRequestOf(approachKeep, false))

	reply, err := continueFrom(t, fx)
	if err != nil {
		t.Fatalf("ContinueDesign: %v", err)
	}
	if reply.GetDesign() != stageDesign || reply.GetSession() != conversationNext {
		t.Fatalf("ContinueDesign = design %q session %q, want %q and %q",
			reply.GetDesign(), reply.GetSession(), stageDesign, conversationNext)
	}
	if len(sessions.starts) != 1 || sessions.starts[0].Dir != "/synthetic/dir" {
		t.Fatalf("starts = %+v, want one start in the working conversation's directory", sessions.starts)
	}
	for _, words := range []string{stageDesign, "Design one", conversationWorking, "approach recorded", approachKeep} {
		if !strings.Contains(sessions.starts[0].Prompt, words) {
			t.Fatalf("prompt = %q, want it to name %q", sessions.starts[0].Prompt, words)
		}
	}

	working, err := fx.store.DesignConversation(ctx, stageDesign)
	if err != nil || working != conversationNext {
		t.Fatalf("DesignConversation = %q, %v, want %q", working, err, conversationNext)
	}
	lines, err := fx.store.DesignLog(ctx, stageDesign)
	if err != nil {
		t.Fatalf("DesignLog: %v", err)
	}
	want := store.DesignLogLine{
		Design: stageDesign, Seq: 2, At: stageResearchNow().Unix(), Maker: store.LogMakerOrreryd, What: "continued",
		IDs: []string{conversationNext},
	}
	if len(lines) != 2 || !reflect.DeepEqual(lines[1], want) {
		t.Fatalf("log = %+v, want the continued line %+v last", lines, want)
	}
}

// TestRecordsConversationNewAdmittedOldRefused checks that once a design continues, the conversation it replaced is
// refused its writes, naming the new working conversation, and the new session is admitted.
func TestRecordsConversationNewAdmittedOldRefused(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	fx, _ := newConversationFixture(t)
	if _, err := continueFrom(t, fx); err != nil {
		t.Fatalf("ContinueDesign: %v", err)
	}

	_, err := fx.service.Approach(ctx, fromSession(connect.NewRequest(approachRequestOf(approachDrop, false)),
		conversationWorking))
	requireRefusal(t, "old conversation Approach", err, connect.CodePermissionDenied, conversationNext)

	reply, err := fx.service.Approach(ctx, fromSession(connect.NewRequest(approachRequestOf(approachDrop, false)),
		conversationNext))
	if err != nil {
		t.Fatalf("new conversation Approach: %v", err)
	}
	if got := reply.Msg.GetApproach().GetSession(); got != conversationNext {
		t.Fatalf("approach session = %q, want the new conversation %q", got, conversationNext)
	}
}

// TestRecordsConversationContinueStartFailureStoresNothing checks that a continuation whose start fails stores no
// working conversation and no continued line, and stops nothing.
func TestRecordsConversationContinueStartFailureStoresNothing(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	fx, sessions := newConversationFixture(t)
	sessions.err = errors.New("spawn refused")

	_, err := continueFrom(t, fx)
	requireRefusal(t, "ContinueDesign", err, connect.CodeUnavailable, "spawn refused")
	working, err := fx.store.DesignConversation(ctx, stageDesign)
	if err != nil || working != conversationWorking {
		t.Fatalf("DesignConversation = %q, %v, want the working conversation %q", working, err, conversationWorking)
	}
	lines, err := fx.store.DesignLog(ctx, stageDesign)
	if err != nil || len(lines) != 0 {
		t.Fatalf("log = %+v, %v, want none", lines, err)
	}
	if len(sessions.stops) != 0 {
		t.Fatalf("stops = %v, want none for a start that failed", sessions.stops)
	}
}

// TestRecordsConversationContinueStopsStartedWhenStoreFails checks that a continuation whose store write fails after
// the start stops the started session, though the continue's context has ended, and stores no working conversation.
func TestRecordsConversationContinueStopsStartedWhenStoreFails(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	fx, sessions := newConversationFixture(t)
	cancelled, cancel := context.WithCancel(ctx)
	sessions.beforeReturn = cancel

	_, err := fx.service.ContinueDesign(cancelled, connect.NewRequest(&v1.ContinueDesignRequest{Design: stageDesign}))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ContinueDesign = %v, want the cancelled store write", err)
	}
	if !reflect.DeepEqual(sessions.stops, []string{conversationNext}) || sessions.stopCtxErr != nil {
		t.Fatalf("stops = %v with context error %v, want the started session stopped with a live context",
			sessions.stops, sessions.stopCtxErr)
	}
	working, err := fx.store.DesignConversation(ctx, stageDesign)
	if err != nil || working != conversationWorking {
		t.Fatalf("DesignConversation = %q, %v, want the working conversation %q", working, err, conversationWorking)
	}
}

// TestRecordsConversationContinueNeedsSessionRow checks that a continuation of a design whose working conversation has
// no sessions row is refused as a failed precondition, and starts nothing.
func TestRecordsConversationContinueNeedsSessionRow(t *testing.T) {
	t.Parallel()
	fx, sessions := newConversationFixture(t)
	if err := fx.store.CreateDesign(t.Context(), store.Design{
		ID: "design-no-row", Title: "Design without a row", Conversation: "session-no-row",
		Created: stageResearchNow(),
	}); err != nil {
		t.Fatalf("CreateDesign: %v", err)
	}

	_, err := fx.service.ContinueDesign(t.Context(), connect.NewRequest(&v1.ContinueDesignRequest{
		Design: "design-no-row",
	}))
	requireRefusal(t, "ContinueDesign", err, connect.CodeFailedPrecondition, "session-no-row")
	if len(sessions.starts) != 0 {
		t.Fatalf("starts = %+v, want none", sessions.starts)
	}
}

// TestRecordsConversationContinueNeedsSessionStarter checks that a Stage service with no session starter refuses a
// continuation as unavailable, and stores nothing.
func TestRecordsConversationContinueNeedsSessionStarter(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	fx := newStageResearchFixture(t)
	seedRowsSession(t, fx)
	fx.service = NewService(Config{Store: fx.store, Steps: fx.starter, Now: stageResearchNow, Getenv: fx.getenv})

	_, err := continueFrom(t, fx)
	requireRefusal(t, "ContinueDesign", err, connect.CodeUnavailable, "no session starter")
	working, err := fx.store.DesignConversation(ctx, stageDesign)
	if err != nil || working != conversationWorking {
		t.Fatalf("DesignConversation = %q, %v, want the working conversation %q", working, err, conversationWorking)
	}
}
