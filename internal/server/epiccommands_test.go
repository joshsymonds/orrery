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
	go func() { done <- enginehost.Serve(ctx, ln, nil, nil, st, nil, nil, nil) }()
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
	cases := []struct {
		name   string
		choice string
		text   string
	}{
		{name: "declared choice", choice: "sqlite", text: "Synthetic answer."},
		{name: "other with text", choice: "other", text: "Synthetic own words."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			decision := &enginev1.EpicDecision{Seq: 2, Id: "A1", Text: tc.text, Time: 1_700_000_000, Question: "Q1"}
			fake := &epicCommandsFake{decision: decision}
			client := epicCommandServe(t, fake)
			reply, err := client.Answer(t.Context(), connect.NewRequest(&orreryv1.EpicsAnswerRequest{
				EpicId: syntheticEpicID, Id: "A1", Text: tc.text, Question: "Q1", Choice: tc.choice,
			}))
			if err != nil {
				t.Fatalf("Answer: %v", err)
			}
			assertForwarded(t, fake.recorded(), &enginev1.AnswerEpicRequest{
				EpicId: syntheticEpicID, Id: "A1", Text: tc.text, Question: "Q1", Choice: tc.choice,
			})
			want := &orreryv1.EpicDecision{Seq: 2, Id: "A1", Text: tc.text, Time: 1_700_000_000, Question: "Q1"}
			if !proto.Equal(reply.Msg.GetDecision(), want) {
				t.Fatalf("decision = %v, want %v", reply.Msg.GetDecision(), want)
			}
		})
	}
}
