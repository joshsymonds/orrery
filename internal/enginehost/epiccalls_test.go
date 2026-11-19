package enginehost

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// epicCallAt is the synthetic time the interceptor's clock reads.
func epicCallAt() time.Time { return time.Date(2026, time.November, 16, 9, 30, 0, 0, time.UTC) }

// epicCallSession is the session the caller headers of the tests name.
const epicCallSession = "session-7"

// epicCall is one call a fakeEpicRecorder was asked to record.
type epicCall struct {
	epic   string
	caller store.EpicCaller
	call   string
	at     time.Time
}

// fakeEpicRecorder records each call it is given, and fails each record with err when err is set.
type fakeEpicRecorder struct {
	mu    sync.Mutex
	err   error
	calls []epicCall
}

func (r *fakeEpicRecorder) RecordEpicCall(
	_ context.Context, epic string, caller store.EpicCaller, call string, at time.Time,
) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, epicCall{epic: epic, caller: caller, call: call, at: at})
	if r.err != nil {
		return false, r.err
	}
	return true, nil
}

// recorded returns the calls recorded so far.
func (r *fakeEpicRecorder) recorded() []epicCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.calls)
}

// epicCallFixture serves the Epics service over httptest with EpicCallInterceptor recording into rec at epicCallTime,
// over an empty store. It returns the store and the client that dials the server.
func epicCallFixture(t *testing.T, rec EpicCallRecorder) (*store.Store, enginev1connect.EpicsClient) {
	t.Helper()
	st := epicTestStore(t)
	interceptor := EpicCallInterceptor(rec, epicCallAt, nil, slog.New(slog.DiscardHandler))
	path, handler := enginev1connect.NewEpicsHandler(&epicService{epics: st}, connect.WithInterceptors(interceptor))
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return st, enginev1connect.NewEpicsClient(srv.Client(), srv.URL)
}

// wantEpicCall asserts that got holds exactly want.
func wantEpicCall(t *testing.T, got []epicCall, want epicCall) {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("recorded %d calls, want 1: %+v", len(got), got)
	}
	if got[0].epic != want.epic || got[0].caller != want.caller ||
		got[0].call != want.call || !got[0].at.Equal(want.at) {
		t.Fatalf("recorded %+v, want %+v", got[0], want)
	}
}

// wantNoEpicCall asserts that rec has recorded nothing, after the call named by what.
func wantNoEpicCall(t *testing.T, rec *fakeEpicRecorder, what string) {
	t.Helper()
	if got := rec.recorded(); len(got) != 0 {
		t.Fatalf("after %s recorded %+v, want nothing", what, got)
	}
}

func TestEpicCallCreateRecordsTheContractsEpic(t *testing.T) {
	t.Parallel()
	rec := &fakeEpicRecorder{}
	_, client := epicCallFixture(t, rec)
	req := connect.NewRequest(fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	req.Header().Set(protocol.HeaderSession, epicCallSession)

	if _, err := client.CreateEpic(t.Context(), req); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	wantEpicCall(t, rec.recorded(), epicCall{
		epic: epicServiceID, caller: store.EpicCaller{Session: epicCallSession}, call: "epic_create", at: epicCallAt(),
	})
}

func TestEpicCallGetRecordsTheSessionOfItsCaller(t *testing.T) {
	t.Parallel()
	rec := &fakeEpicRecorder{}
	_, client := epicCallFixture(t, rec)
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))

	req := connect.NewRequest(&v1.GetEpicRequest{Id: epicServiceID})
	req.Header().Set(protocol.HeaderSession, epicCallSession)
	if _, err := client.GetEpic(t.Context(), req); err != nil {
		t.Fatalf("GetEpic: %v", err)
	}
	wantEpicCall(t, rec.recorded(), epicCall{
		epic: epicServiceID, caller: store.EpicCaller{Session: epicCallSession}, call: "epic_get", at: epicCallAt(),
	})
}

func TestEpicCallAnswerRecordsTheTmuxPaneOfItsCaller(t *testing.T) {
	t.Parallel()
	rec := &fakeEpicRecorder{}
	_, client := epicCallFixture(t, rec)
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))

	req := connect.NewRequest(&v1.AnswerEpicRequest{EpicId: epicServiceID, Id: "D4", Text: "Synthetic note."})
	req.Header().Set(protocol.HeaderTmux, "/tmp/tmux-1000/orrery,4242,0")
	req.Header().Set(protocol.HeaderTmuxPane, "%7")
	if _, err := client.AnswerEpic(t.Context(), req); err != nil {
		t.Fatalf("AnswerEpic: %v", err)
	}
	wantEpicCall(t, rec.recorded(), epicCall{
		epic:   epicServiceID,
		caller: store.EpicCaller{Server: "-S /tmp/tmux-1000/orrery", Pane: "%7"},
		call:   "epic_answer",
		at:     epicCallAt(),
	})
}

func TestEpicKickIsRecordedAsEpicKick(t *testing.T) {
	t.Parallel()
	rec := &fakeEpicRecorder{}
	_, client := epicCallFixture(t, rec)
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))

	req := connect.NewRequest(&v1.KickEpicRequest{Id: epicServiceID, Text: "Synthetic kick."})
	req.Header().Set(protocol.HeaderSession, epicCallSession)
	if _, err := client.KickEpic(t.Context(), req); err != nil {
		t.Fatalf("KickEpic: %v", err)
	}
	wantEpicCall(t, rec.recorded(), epicCall{
		epic: epicServiceID, caller: store.EpicCaller{Session: epicCallSession}, call: "epic_kick", at: epicCallAt(),
	})
}

func TestCloseEpicRPCIsRecordedAsEpicClose(t *testing.T) {
	t.Parallel()
	rec := &fakeEpicRecorder{}
	_, client := epicCallFixture(t, rec)
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))

	req := connect.NewRequest(&v1.CloseEpicRequest{Id: epicServiceID, Kind: "abandoned"})
	req.Header().Set(protocol.HeaderSession, epicCallSession)
	if _, err := client.CloseEpic(t.Context(), req); err != nil {
		t.Fatalf("CloseEpic: %v", err)
	}
	wantEpicCall(t, rec.recorded(), epicCall{
		epic: epicServiceID, caller: store.EpicCaller{Session: epicCallSession}, call: "epic_close", at: epicCallAt(),
	})
}

func TestEpicCallRecordsNothingForRefusedOrUnnamedCalls(t *testing.T) {
	t.Parallel()
	rec := &fakeEpicRecorder{}
	_, client := epicCallFixture(t, rec)
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))

	refused := connect.NewRequest(&v1.GetEpicRequest{Id: "no-such-epic"})
	refused.Header().Set(protocol.HeaderSession, epicCallSession)
	if _, err := client.GetEpic(t.Context(), refused); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("GetEpic of a missing epic = %v, want NotFound", err)
	}
	wantNoEpicCall(t, rec, "refused GetEpic")

	listed := connect.NewRequest(&v1.ListEpicsRequest{})
	listed.Header().Set(protocol.HeaderSession, epicCallSession)
	if _, err := client.ListEpics(t.Context(), listed); err != nil {
		t.Fatalf("ListEpics: %v", err)
	}
	wantNoEpicCall(t, rec, "ListEpics")

	if _, err := client.GetEpic(t.Context(), connect.NewRequest(&v1.GetEpicRequest{Id: epicServiceID})); err != nil {
		t.Fatalf("GetEpic without caller headers: %v", err)
	}
	wantNoEpicCall(t, rec, "GetEpic without caller headers")
}

func TestEpicCallRecorderErrorLeavesTheAnswerUnchanged(t *testing.T) {
	t.Parallel()
	failing := &fakeEpicRecorder{err: errors.New("store closed")}
	st, client := epicCallFixture(t, failing)
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	want := getEpicOK(t, newEpicsClient(t, startEpicServer(t, st)))

	req := connect.NewRequest(&v1.GetEpicRequest{Id: epicServiceID})
	req.Header().Set(protocol.HeaderSession, epicCallSession)
	got, err := client.GetEpic(t.Context(), req)
	if err != nil {
		t.Fatalf("GetEpic with a failing recorder: %v", err)
	}
	if !proto.Equal(got.Msg.GetEpic(), want) {
		t.Fatalf("GetEpic = %v, want the epic %v", got.Msg.GetEpic(), want)
	}
	if n := len(failing.recorded()); n != 1 {
		t.Fatalf("recorder was asked %d times, want 1", n)
	}
}
