package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"github.com/joshsymonds/orrery/internal/enginehost"
	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// syntheticEpicID is the id of the epic the epic command tests name.
const syntheticEpicID = "synthetic-repo/alpha"

// epicCommandsFake is orreryd's Epics client for the forwarding tests; the calls serve does not forward answer
// unimplemented. Each command call records its request in calls, then fails with the next queued failure, or answers
// with the fake's replies. The handlers run on server goroutines, so the recorded calls are guarded.
type epicCommandsFake struct {
	enginev1connect.UnimplementedEpicsHandler
	mu       sync.Mutex
	calls    []proto.Message
	failures []error
	resumed  []string
	limited  []string
	decision *enginev1.EpicDecision
	warning  string

	// closeSessions is the Orrery-Session header each CloseEpic call carried, in order.
	closeSessions []string

	// moveSessions is the Orrery-Session header each MoveEpic call carried, in order.
	moveSessions []string

	// continueSession is the session the fake's ContinueEpic answers.
	continueSession string
}

func (f *epicCommandsFake) MoveEpic(
	_ context.Context,
	req *connect.Request[enginev1.MoveEpicRequest],
) (*connect.Response[enginev1.MoveEpicResponse], error) {
	f.mu.Lock()
	f.moveSessions = append(f.moveSessions, req.Header().Get(protocol.HeaderSession))
	f.mu.Unlock()
	if err := f.record(req.Msg); err != nil {
		return nil, err
	}
	return connect.NewResponse(&enginev1.MoveEpicResponse{}), nil
}

func (f *epicCommandsFake) ContinueEpic(
	_ context.Context,
	req *connect.Request[enginev1.ContinueEpicRequest],
) (*connect.Response[enginev1.ContinueEpicResponse], error) {
	if err := f.record(req.Msg); err != nil {
		return nil, err
	}
	return connect.NewResponse(&enginev1.ContinueEpicResponse{Session: f.continueSession}), nil
}

func (f *epicCommandsFake) PauseEpic(
	_ context.Context,
	req *connect.Request[enginev1.PauseEpicRequest],
) (*connect.Response[enginev1.PauseEpicResponse], error) {
	if err := f.record(req.Msg); err != nil {
		return nil, err
	}
	return connect.NewResponse(&enginev1.PauseEpicResponse{}), nil
}

func (f *epicCommandsFake) ResumeEpic(
	_ context.Context,
	req *connect.Request[enginev1.ResumeEpicRequest],
) (*connect.Response[enginev1.ResumeEpicResponse], error) {
	if err := f.record(req.Msg); err != nil {
		return nil, err
	}
	return connect.NewResponse(&enginev1.ResumeEpicResponse{Resumed: f.resumed, StillLimited: f.limited}), nil
}

func (f *epicCommandsFake) StopEpic(
	_ context.Context,
	req *connect.Request[enginev1.StopEpicRequest],
) (*connect.Response[enginev1.StopEpicResponse], error) {
	if err := f.record(req.Msg); err != nil {
		return nil, err
	}
	return connect.NewResponse(&enginev1.StopEpicResponse{}), nil
}

func (f *epicCommandsFake) RetryEpic(
	_ context.Context,
	req *connect.Request[enginev1.RetryEpicRequest],
) (*connect.Response[enginev1.RetryEpicResponse], error) {
	if err := f.record(req.Msg); err != nil {
		return nil, err
	}
	return connect.NewResponse(&enginev1.RetryEpicResponse{}), nil
}

func (f *epicCommandsFake) KickEpic(
	_ context.Context,
	req *connect.Request[enginev1.KickEpicRequest],
) (*connect.Response[enginev1.KickEpicResponse], error) {
	if err := f.record(req.Msg); err != nil {
		return nil, err
	}
	return connect.NewResponse(&enginev1.KickEpicResponse{}), nil
}

func (f *epicCommandsFake) AnswerEpic(
	_ context.Context,
	req *connect.Request[enginev1.AnswerEpicRequest],
) (*connect.Response[enginev1.AnswerEpicResponse], error) {
	if err := f.record(req.Msg); err != nil {
		return nil, err
	}
	return connect.NewResponse(&enginev1.AnswerEpicResponse{Decision: f.decision, Warning: f.warning}), nil
}

func (f *epicCommandsFake) SeeEpic(
	_ context.Context,
	req *connect.Request[enginev1.SeeEpicRequest],
) (*connect.Response[enginev1.SeeEpicResponse], error) {
	if err := f.record(req.Msg); err != nil {
		return nil, err
	}
	return connect.NewResponse(&enginev1.SeeEpicResponse{}), nil
}

func (f *epicCommandsFake) ArchiveRow(
	_ context.Context,
	req *connect.Request[enginev1.ArchiveRowRequest],
) (*connect.Response[enginev1.ArchiveRowResponse], error) {
	if err := f.record(req.Msg); err != nil {
		return nil, err
	}
	return connect.NewResponse(&enginev1.ArchiveRowResponse{}), nil
}

func (f *epicCommandsFake) UnarchiveRow(
	_ context.Context,
	req *connect.Request[enginev1.UnarchiveRowRequest],
) (*connect.Response[enginev1.UnarchiveRowResponse], error) {
	if err := f.record(req.Msg); err != nil {
		return nil, err
	}
	return connect.NewResponse(&enginev1.UnarchiveRowResponse{}), nil
}

func (f *epicCommandsFake) ReleaseEpic(
	_ context.Context,
	req *connect.Request[enginev1.ReleaseEpicRequest],
) (*connect.Response[enginev1.ReleaseEpicResponse], error) {
	if err := f.record(req.Msg); err != nil {
		return nil, err
	}
	return connect.NewResponse(&enginev1.ReleaseEpicResponse{}), nil
}

func (f *epicCommandsFake) CloseEpic(
	_ context.Context,
	req *connect.Request[enginev1.CloseEpicRequest],
) (*connect.Response[enginev1.CloseEpicResponse], error) {
	f.mu.Lock()
	f.closeSessions = append(f.closeSessions, req.Header().Get(protocol.HeaderSession))
	f.mu.Unlock()
	if err := f.record(req.Msg); err != nil {
		return nil, err
	}
	return connect.NewResponse(&enginev1.CloseEpicResponse{}), nil
}

// record appends req to the calls and returns the next queued failure, or nil when the call succeeds.
func (f *epicCommandsFake) record(req proto.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, req)
	if len(f.failures) == 0 {
		return nil
	}
	err := f.failures[0]
	f.failures = f.failures[1:]
	return err
}

// recorded returns the requests the fake has received, in order.
func (f *epicCommandsFake) recorded() []proto.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]proto.Message(nil), f.calls...)
}

// recordedCloseSessions returns the Orrery-Session header of each CloseEpic call, in order.
func (f *epicCommandsFake) recordedCloseSessions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.closeSessions...)
}

// recordedMoveSessions returns the Orrery-Session header of each MoveEpic call, in order.
func (f *epicCommandsFake) recordedMoveSessions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.moveSessions...)
}

// epicCommandServe serves newEpicsHandler over commands with the fixed clock, and returns a generated Epics client.
func epicCommandServe(t *testing.T, commands enginev1connect.EpicsClient) orreryv1connect.EpicsClient {
	t.Helper()
	path, handler := newEpicsHandler(t.TempDir(), nil, nil, commands, nil, nil, 0, epicsNow, newRetryClock())
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return orreryv1connect.NewEpicsClient(srv.Client(), srv.URL)
}

// epicCommandCase is one epic command. call sends it through an Epics client, and want is the one engine request it
// must forward: the named epic's id with the request's other fields, and nothing else.
type epicCommandCase struct {
	name string
	call func(context.Context, orreryv1connect.EpicsClient) error
	want proto.Message
}

// epicCommandCases returns the six epic commands, each naming syntheticEpicID.
func epicCommandCases() []epicCommandCase {
	return []epicCommandCase{
		{
			name: "seen",
			call: func(ctx context.Context, client orreryv1connect.EpicsClient) error {
				_, err := client.Seen(ctx, connect.NewRequest(&orreryv1.EpicsSeenRequest{Id: syntheticEpicID}))
				return err
			},
			want: &enginev1.SeeEpicRequest{Id: syntheticEpicID},
		},
		{
			name: "pause",
			call: func(ctx context.Context, client orreryv1connect.EpicsClient) error {
				_, err := client.Pause(ctx, connect.NewRequest(&orreryv1.EpicsPauseRequest{Id: syntheticEpicID}))
				return err
			},
			want: &enginev1.PauseEpicRequest{Id: syntheticEpicID},
		},
		{
			name: "resume",
			call: func(ctx context.Context, client orreryv1connect.EpicsClient) error {
				_, err := client.Resume(ctx, connect.NewRequest(&orreryv1.EpicsResumeRequest{Id: syntheticEpicID}))
				return err
			},
			want: &enginev1.ResumeEpicRequest{Id: syntheticEpicID},
		},
		{
			name: "stop",
			call: func(ctx context.Context, client orreryv1connect.EpicsClient) error {
				_, err := client.Stop(ctx, connect.NewRequest(&orreryv1.EpicsStopRequest{Id: syntheticEpicID}))
				return err
			},
			want: &enginev1.StopEpicRequest{Id: syntheticEpicID},
		},
		{
			name: "retry",
			call: func(ctx context.Context, client orreryv1connect.EpicsClient) error {
				_, err := client.Retry(ctx, connect.NewRequest(&orreryv1.EpicsRetryRequest{
					Id: syntheticEpicID, Node: "build",
				}))
				return err
			},
			want: &enginev1.RetryEpicRequest{Id: syntheticEpicID, Node: "build"},
		},
		{
			name: "answer",
			call: func(ctx context.Context, client orreryv1connect.EpicsClient) error {
				_, err := client.Answer(ctx, connect.NewRequest(&orreryv1.EpicsAnswerRequest{
					EpicId: syntheticEpicID, Id: "A1", Text: "Ship it.", Question: "Q1",
				}))
				return err
			},
			want: &enginev1.AnswerEpicRequest{EpicId: syntheticEpicID, Id: "A1", Text: "Ship it.", Question: "Q1"},
		},
	}
}

// assertForwarded fails t unless got is exactly the one engine request want.
func assertForwarded(t *testing.T, got []proto.Message, want proto.Message) {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("engine calls = %d, want exactly one", len(got))
	}
	if !proto.Equal(got[0], want) {
		t.Fatalf("engine request = %v, want %v", got[0], want)
	}
}

func TestEpicCommandsForwardOneCallForNamedEpic(t *testing.T) {
	t.Parallel()
	for _, tc := range epicCommandCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := &epicCommandsFake{}
			client := epicCommandServe(t, fake)
			if err := tc.call(t.Context(), client); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			assertForwarded(t, fake.recorded(), tc.want)
		})
	}
}

func TestEpicsResumeAnswersResumedAndLimitedSteps(t *testing.T) {
	t.Parallel()
	fake := &epicCommandsFake{
		resumed: []string{"synthetic-step-1", "synthetic-step-2"},
		limited: []string{"synthetic-step-3"},
	}
	client := epicCommandServe(t, fake)
	reply, err := client.Resume(t.Context(), connect.NewRequest(&orreryv1.EpicsResumeRequest{
		Id: syntheticEpicID,
	}))
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	want := &orreryv1.EpicsResumeResponse{
		Resumed:      []string{"synthetic-step-1", "synthetic-step-2"},
		StillLimited: []string{"synthetic-step-3"},
	}
	if !proto.Equal(reply.Msg, want) {
		t.Fatalf("Resume = %v, want %v", reply.Msg, want)
	}
}

func TestEpicsAnswerReplyCarriesRecordedDecision(t *testing.T) {
	t.Parallel()
	fake := &epicCommandsFake{
		decision: &enginev1.EpicDecision{Seq: 4, Id: "A1", Text: "Ship it.", Time: 1_700_000_000, Question: "Q1"},
		warning:  "synthetic warning",
	}
	client := epicCommandServe(t, fake)
	reply, err := client.Answer(t.Context(), connect.NewRequest(&orreryv1.EpicsAnswerRequest{
		EpicId: syntheticEpicID, Id: "A1", Text: "Ship it.", Question: "Q1",
	}))
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	want := &orreryv1.EpicsAnswerResponse{
		Decision: &orreryv1.EpicDecision{Seq: 4, Id: "A1", Text: "Ship it.", Time: 1_700_000_000, Question: "Q1"},
		Warning:  "synthetic warning",
	}
	if !proto.Equal(reply.Msg, want) {
		t.Fatalf("Answer = %v, want %v", reply.Msg, want)
	}
}

func TestEpicCommandsPassRefusalThrough(t *testing.T) {
	t.Parallel()
	refusals := []struct {
		code    connect.Code
		message string
	}{
		{connect.CodeFailedPrecondition, "question: Q1 is closed"},
		{connect.CodeNotFound, "epic_id: no epic " + syntheticEpicID},
	}
	for _, tc := range epicCommandCases() {
		for _, refusal := range refusals {
			t.Run(tc.name+"/"+refusal.code.String(), func(t *testing.T) {
				t.Parallel()
				refused := connect.NewError(refusal.code, errors.New(refusal.message))
				fake := &epicCommandsFake{failures: []error{refused}}
				client := epicCommandServe(t, fake)
				err := tc.call(t.Context(), client)
				var connectErr *connect.Error
				if !errors.As(err, &connectErr) {
					t.Fatalf("%s: error = %v, want a Connect error", tc.name, err)
				}
				if connectErr.Code() != refusal.code || connectErr.Message() != refusal.message {
					t.Fatalf("%s: error = %v %q, want %v %q",
						tc.name, connectErr.Code(), connectErr.Message(), refusal.code, refusal.message)
				}
				if n := len(fake.recorded()); n != 1 {
					t.Fatalf("engine calls = %d, want one: a refusal is not retried", n)
				}
			})
		}
	}
}

func TestEpicCommandsRetryOnlyWhenNeverConnected(t *testing.T) {
	t.Parallel()
	refusedDial := &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
	for _, tc := range epicCommandCases() {
		t.Run(tc.name+"/refused dial is retried", func(t *testing.T) {
			t.Parallel()
			fake := &epicCommandsFake{failures: []error{connect.NewError(connect.CodeUnavailable, refusedDial)}}
			client := epicCommandServe(t, fake)
			if err := tc.call(t.Context(), client); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			calls := fake.recorded()
			if len(calls) != 2 {
				t.Fatalf("engine calls = %d, want two: the refused dial then its retry", len(calls))
			}
			for _, call := range calls {
				if !proto.Equal(call, tc.want) {
					t.Fatalf("engine request = %v, want %v", call, tc.want)
				}
			}
		})
		t.Run(tc.name+"/connection cut mid-call is not retried", func(t *testing.T) {
			t.Parallel()
			cut := connect.NewError(connect.CodeUnavailable, errors.New("synthetic connection cut"))
			fake := &epicCommandsFake{failures: []error{cut}}
			client := epicCommandServe(t, fake)
			err := tc.call(t.Context(), client)
			if code := connect.CodeOf(err); code != connect.CodeUnavailable {
				t.Fatalf("%s: code = %v, want %v", tc.name, code, connect.CodeUnavailable)
			}
			if n := len(fake.recorded()); n != 1 {
				t.Fatalf("engine calls = %d, want one: a cut call may have been taken", n)
			}
		})
	}
}

func TestEpicCommandsNilClientAnswersUnavailable(t *testing.T) {
	t.Parallel()
	for _, tc := range epicCommandCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := epicCommandServe(t, nil)
			err := tc.call(t.Context(), client)
			if code := connect.CodeOf(err); code != connect.CodeUnavailable {
				t.Fatalf("%s: code = %v, want %v (error %v)", tc.name, code, connect.CodeUnavailable, err)
			}
		})
	}
}

// openEpicAnswerStore opens a store at a temporary path, as connect_test opens its session store, seeds it with the
// synthetic epic, and closes it when the test ends.
func openEpicAnswerStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	st, err := store.Open(ctx, path, storetest.NoSync())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("Close store: %v", err)
		}
	})
	created := time.Unix(1_700_000_000, 0)
	if err = st.CreateEpic(ctx, store.EpicRow{
		ID: syntheticEpicID, Repository: "/synthetic/repo", Title: "synthetic epic", Phase: "accepted",
		Created: created, Changed: created,
	}); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	return st
}

// serveEngineEpics serves orreryd's engine Epics service over st, with no other service, on a loopback listener until
// the test ends. It returns a generated Epics client for the listener.
func serveEngineEpics(t *testing.T, st *store.Store) enginev1connect.EpicsClient {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- enginehost.Serve(ctx, ln, nil, nil, st, nil, nil, nil, nil, nil, nil, nil) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})
	return enginev1connect.NewEpicsClient(http.DefaultClient, "http://"+ln.Addr().String())
}

func TestEpicsAnswerRepeatedIDReturnsFirstDecisionFromEngine(t *testing.T) {
	t.Parallel()
	client := epicCommandServe(t, serveEngineEpics(t, openEpicAnswerStore(t)))
	answer := func() *orreryv1.EpicDecision {
		t.Helper()
		reply, err := client.Answer(t.Context(), connect.NewRequest(&orreryv1.EpicsAnswerRequest{
			EpicId: syntheticEpicID, Id: "A1", Text: "Synthetic answer.",
		}))
		if err != nil {
			t.Fatalf("Answer: %v", err)
		}
		return reply.Msg.GetDecision()
	}
	first := answer()
	if first.GetSeq() != 1 || first.GetId() != "A1" || first.GetText() != "Synthetic answer." {
		t.Fatalf("first decision = %v, want seq 1 as A1 with %q", first, "Synthetic answer.")
	}
	if second := answer(); !proto.Equal(second, first) {
		t.Fatalf("repeated decision = %v, want the first decision %v", second, first)
	}
}

func TestEpicsAnswerOptionsForwardsQuestionAndChoice(t *testing.T) {
	t.Parallel()
	decision := &enginev1.EpicDecision{Seq: 2, Id: "A1", Text: "Synthetic answer.", Time: 1_700_000_000, Question: "Q1"}
	fake := &epicCommandsFake{decision: decision}
	client := epicCommandServe(t, fake)
	reply, err := client.Answer(t.Context(), connect.NewRequest(&orreryv1.EpicsAnswerRequest{
		EpicId: syntheticEpicID, Id: "A1", Text: "Synthetic answer.", Question: "Q1", Choice: "sqlite",
	}))
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	assertForwarded(t, fake.recorded(), &enginev1.AnswerEpicRequest{
		EpicId: syntheticEpicID, Id: "A1", Text: "Synthetic answer.", Question: "Q1", Choice: "sqlite",
	})
	want := &orreryv1.EpicDecision{Seq: 2, Id: "A1", Text: "Synthetic answer.", Time: 1_700_000_000, Question: "Q1"}
	if !proto.Equal(reply.Msg.GetDecision(), want) {
		t.Fatalf("decision = %v, want %v", reply.Msg.GetDecision(), want)
	}
}

func TestEpicsAnswerOptionsOtherWithTextIsRecordedByEngine(t *testing.T) {
	t.Parallel()
	st := openEpicAnswerStore(t)
	turn, err := st.ClaimTurn(t.Context(), syntheticEpicID, time.Unix(1_700_000_100, 0))
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	plan := store.EpicPlan{
		Outline: "Synthetic outline.",
		Outcome: "Synthetic outcome.",
		Questions: []store.EpicQuestionInput{{
			Label: "synthetic-question-one", Text: "Synthetic question one?",
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

	const text = "Synthetic own words."
	client := epicCommandServe(t, serveEngineEpics(t, st))
	reply, err := client.Answer(t.Context(), connect.NewRequest(&orreryv1.EpicsAnswerRequest{
		EpicId: syntheticEpicID, Id: "A1", Text: text, Question: "Q1", Choice: store.ChoiceOther,
	}))
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	decision := reply.Msg.GetDecision()
	if decision.GetSeq() != 1 || decision.GetQuestion() != "Q1" || decision.GetText() != text {
		t.Fatalf("decision = %v, want seq 1 answering Q1 with %q", decision, text)
	}

	epic, err := st.Epic(t.Context(), syntheticEpicID)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if len(epic.Questions) != 1 {
		t.Fatalf("questions = %v, want only Q1", epic.Questions)
	}
	if q := epic.Questions[0]; q.State != "answered" || q.Choice != store.ChoiceOther {
		t.Fatalf("question = %v, want Q1 answered with choice %q", q, store.ChoiceOther)
	}
	if len(epic.Decisions) != 1 || epic.Decisions[0].Question != 1 || epic.Decisions[0].Text != text {
		t.Fatalf("stored decisions = %v, want one decision answering question 1 with %q", epic.Decisions, text)
	}
}

// releaseCall returns a call that sends a release of syntheticEpicID naming the undo-none positions undoNone.
func releaseCall(undoNone []int32) func(context.Context, orreryv1connect.EpicsClient) error {
	return func(ctx context.Context, client orreryv1connect.EpicsClient) error {
		_, err := client.Release(ctx, connect.NewRequest(&orreryv1.EpicsReleaseRequest{
			Id: syntheticEpicID, UndoNone: undoNone,
		}))
		return err
	}
}

// closeCall returns a call that sends a close of syntheticEpicID with the kind and names no epic that replaces it.
func closeCall(kind string) func(context.Context, orreryv1connect.EpicsClient) error {
	return func(ctx context.Context, client orreryv1connect.EpicsClient) error {
		_, err := client.Close(ctx, connect.NewRequest(&orreryv1.EpicsCloseRequest{
			Id: syntheticEpicID, Kind: kind,
		}))
		return err
	}
}

// assertConnectError fails t unless err is a Connect error with code and message.
func assertConnectError(t *testing.T, err error, code connect.Code, message string) {
	t.Helper()
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("error = %v, want a Connect error", err)
	}
	if connectErr.Code() != code || connectErr.Message() != message {
		t.Fatalf("error = %v %q, want %v %q", connectErr.Code(), connectErr.Message(), code, message)
	}
}

// assertRetriedOnlyWhenNeverConnected runs call against a refused dial, which is retried so the engine sees two
// requests equal to want, and against a connection cut mid-call, which is answered and sent once.
func assertRetriedOnlyWhenNeverConnected(
	t *testing.T,
	call func(context.Context, orreryv1connect.EpicsClient) error,
	want proto.Message,
) {
	t.Helper()
	refusedDial := &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
	t.Run("refused dial is retried", func(t *testing.T) {
		t.Parallel()
		fake := &epicCommandsFake{failures: []error{connect.NewError(connect.CodeUnavailable, refusedDial)}}
		client := epicCommandServe(t, fake)
		if err := call(t.Context(), client); err != nil {
			t.Fatalf("call: %v", err)
		}
		calls := fake.recorded()
		if len(calls) != 2 {
			t.Fatalf("engine calls = %d, want two: the refused dial then its retry", len(calls))
		}
		for _, got := range calls {
			if !proto.Equal(got, want) {
				t.Fatalf("engine request = %v, want %v", got, want)
			}
		}
	})
	t.Run("connection cut mid-call is not retried", func(t *testing.T) {
		t.Parallel()
		cut := connect.NewError(connect.CodeUnavailable, errors.New("synthetic connection cut"))
		fake := &epicCommandsFake{failures: []error{cut}}
		client := epicCommandServe(t, fake)
		err := call(t.Context(), client)
		if code := connect.CodeOf(err); code != connect.CodeUnavailable {
			t.Fatalf("code = %v, want %v", code, connect.CodeUnavailable)
		}
		if n := len(fake.recorded()); n != 1 {
			t.Fatalf("engine calls = %d, want one: a cut call may have been taken", n)
		}
	})
}

func TestEpicsReleaseForwardsOneRequestAndAnswersEmpty(t *testing.T) {
	t.Parallel()
	fake := &epicCommandsFake{}
	client := epicCommandServe(t, fake)
	reply, err := client.Release(t.Context(), connect.NewRequest(&orreryv1.EpicsReleaseRequest{
		Id: syntheticEpicID, UndoNone: []int32{2},
	}))
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	assertForwarded(t, fake.recorded(), &enginev1.ReleaseEpicRequest{Id: syntheticEpicID, UndoNone: []int32{2}})
	if !proto.Equal(reply.Msg, &orreryv1.EpicsReleaseResponse{}) {
		t.Fatalf("Release = %v, want an empty response", reply.Msg)
	}
}

func TestEpicsReleaseForwardsUndoNoneAsSent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		undoNone []int32
	}{
		{name: "ended with gaps names none", undoNone: nil},
		{name: "repeated position", undoNone: []int32{2, 2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := &epicCommandsFake{}
			client := epicCommandServe(t, fake)
			if err := releaseCall(tc.undoNone)(t.Context(), client); err != nil {
				t.Fatalf("Release: %v", err)
			}
			assertForwarded(t, fake.recorded(), &enginev1.ReleaseEpicRequest{
				Id: syntheticEpicID, UndoNone: tc.undoNone,
			})
		})
	}
}

func TestEpicsReleaseRefusalReachesCallerAfterOneCall(t *testing.T) {
	t.Parallel()
	const message = "undo_none: position 2 is not an undo-none step"
	refused := connect.NewError(connect.CodeFailedPrecondition, errors.New(message))
	fake := &epicCommandsFake{failures: []error{refused}}
	client := epicCommandServe(t, fake)
	err := releaseCall([]int32{2})(t.Context(), client)
	assertConnectError(t, err, connect.CodeFailedPrecondition, message)
	if n := len(fake.recorded()); n != 1 {
		t.Fatalf("engine calls = %d, want one: a refusal is not retried", n)
	}
}

func TestEpicsReleaseRetryOnlyWhenNeverConnected(t *testing.T) {
	t.Parallel()
	want := &enginev1.ReleaseEpicRequest{Id: syntheticEpicID, UndoNone: []int32{2}}
	assertRetriedOnlyWhenNeverConnected(t, releaseCall([]int32{2}), want)
}

func TestEpicsReleaseNilClientAnswersUnavailable(t *testing.T) {
	t.Parallel()
	client := epicCommandServe(t, nil)
	err := releaseCall([]int32{2})(t.Context(), client)
	assertConnectError(t, err, connect.CodeUnavailable, epicCommandsUnavailable)
}

func TestEpicsCloseForwardsOneRequestAndAnswersEmpty(t *testing.T) {
	t.Parallel()
	fake := &epicCommandsFake{}
	client := epicCommandServe(t, fake)
	reply, err := client.Close(t.Context(), connect.NewRequest(&orreryv1.EpicsCloseRequest{
		Id: syntheticEpicID, Kind: "superseded", By: "synthetic-repo/beta",
	}))
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	assertForwarded(t, fake.recorded(), &enginev1.CloseEpicRequest{
		Id: syntheticEpicID, Kind: "superseded", By: "synthetic-repo/beta",
	})
	if !proto.Equal(reply.Msg, &orreryv1.EpicsCloseResponse{}) {
		t.Fatalf("Close = %v, want an empty response", reply.Msg)
	}
}

// Each close kind is forwarded unchanged with no Orrery-Session header, so orreryd records the owner as its closer.
func TestEpicsCloseForwardsKindsUnchangedWithoutSessionHeader(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		kind string
		by   string
	}{
		{name: "superseded with by", kind: "superseded", by: "synthetic-repo/beta"},
		{name: "shipped", kind: "shipped"},
		{name: "abandoned", kind: "abandoned"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := &epicCommandsFake{}
			client := epicCommandServe(t, fake)
			req := connect.NewRequest(&orreryv1.EpicsCloseRequest{Id: syntheticEpicID, Kind: tc.kind, By: tc.by})
			req.Header().Set(protocol.HeaderSession, "synthetic-session")
			if _, err := client.Close(t.Context(), req); err != nil {
				t.Fatalf("Close: %v", err)
			}
			assertForwarded(t, fake.recorded(), &enginev1.CloseEpicRequest{
				Id: syntheticEpicID, Kind: tc.kind, By: tc.by,
			})
			if sessions := fake.recordedCloseSessions(); len(sessions) != 1 || sessions[0] != "" {
				t.Fatalf("Orrery-Session headers = %q, want one empty header", sessions)
			}
		})
	}
}

func TestEpicsCloseRefusalReachesCallerAfterOneCall(t *testing.T) {
	t.Parallel()
	refusals := []struct {
		name    string
		call    func(context.Context, orreryv1connect.EpicsClient) error
		code    connect.Code
		message string
	}{
		{
			name:    "superseded without by",
			call:    closeCall("superseded"),
			code:    connect.CodeFailedPrecondition,
			message: "by: a superseded close names the epic that replaces it",
		},
		{
			name:    "unknown epic",
			call:    closeCall("abandoned"),
			code:    connect.CodeNotFound,
			message: "id: no epic " + syntheticEpicID,
		},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := &epicCommandsFake{failures: []error{connect.NewError(tc.code, errors.New(tc.message))}}
			client := epicCommandServe(t, fake)
			assertConnectError(t, tc.call(t.Context(), client), tc.code, tc.message)
			if n := len(fake.recorded()); n != 1 {
				t.Fatalf("engine calls = %d, want one: a refusal is not retried", n)
			}
		})
	}
}

func TestEpicsCloseRetryOnlyWhenNeverConnected(t *testing.T) {
	t.Parallel()
	want := &enginev1.CloseEpicRequest{Id: syntheticEpicID, Kind: "shipped"}
	assertRetriedOnlyWhenNeverConnected(t, closeCall("shipped"), want)
}

func TestEpicsCloseNilClientAnswersUnavailable(t *testing.T) {
	t.Parallel()
	client := epicCommandServe(t, nil)
	err := closeCall("abandoned")(t.Context(), client)
	assertConnectError(t, err, connect.CodeUnavailable, epicCommandsUnavailable)
}
