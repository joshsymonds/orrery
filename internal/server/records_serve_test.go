package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	enginev1connect "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// recordsSession is the sessions row the fake Stage answers Designs.Continue with, and recordsCallerSession is the
// session a caller names in the header of its record call.
const (
	recordsSession       = "session-records"
	recordsCallerSession = "session-records-caller"
)

// recordsCalls holds the requests a fake Stage received, and the headers the Keep request carried.
type recordsCalls struct {
	keep       *enginev1.KeepRequest
	keepHeader http.Header
	strike     *enginev1.StrikeRequest
	shown      *enginev1.ShownRequest
	continued  *enginev1.ContinueDesignRequest
}

// recordsStage is a fake Stage that records the requests the record forwards send it, and answers the replies the
// record tests name: Strike removes R1, Shown marks F1 only, and ContinueDesign names recordsSession.
type recordsStage struct {
	enginev1connect.UnimplementedStageHandler

	mu    sync.Mutex
	calls recordsCalls
}

// Keep records the request and its headers.
func (s *recordsStage) Keep(
	_ context.Context,
	req *connect.Request[enginev1.KeepRequest],
) (*connect.Response[enginev1.KeepResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls.keep, s.calls.keepHeader = req.Msg, req.Header().Clone()
	return connect.NewResponse(&enginev1.KeepResponse{}), nil
}

// Strike records the request and answers the row R1 removed.
func (s *recordsStage) Strike(
	_ context.Context,
	req *connect.Request[enginev1.StrikeRequest],
) (*connect.Response[enginev1.StrikeResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls.strike = req.Msg
	return connect.NewResponse(&enginev1.StrikeResponse{Removed: []string{"R1"}}), nil
}

// Shown records the request and answers that only F1 was marked.
func (s *recordsStage) Shown(
	_ context.Context,
	req *connect.Request[enginev1.ShownRequest],
) (*connect.Response[enginev1.ShownResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls.shown = req.Msg
	return connect.NewResponse(&enginev1.ShownResponse{Rows: []string{"F1"}}), nil
}

// ContinueDesign records the request and answers the design with recordsSession.
func (s *recordsStage) ContinueDesign(
	_ context.Context,
	req *connect.Request[enginev1.ContinueDesignRequest],
) (*connect.Response[enginev1.ContinueDesignResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls.continued = req.Msg
	return connect.NewResponse(&enginev1.ContinueDesignResponse{
		Design:  req.Msg.GetDesign(),
		Session: recordsSession,
	}), nil
}

// received returns the requests the fake Stage has recorded so far.
func (s *recordsStage) received() recordsCalls {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// serveRecordsStage serves stage over a loopback listener until the test ends, and returns a generated Stage client
// for it.
func serveRecordsStage(t *testing.T, stage enginev1connect.StageHandler) enginev1connect.StageClient {
	t.Helper()
	mux := http.NewServeMux()
	path, handler := enginev1connect.NewStageHandler(stage)
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return enginev1connect.NewStageClient(http.DefaultClient, srv.URL)
}

// recordsContractB is the listing of contract B as seedContractHistory leaves it.
func recordsContractB() *orreryv1.Contract {
	return &orreryv1.Contract{
		Id: contractIDB, Repository: "/synthetic/repo", Title: "Synthetic two revised", Care: "serious",
		State: "accepted", Version: 2, OpenDecisions: 0, Conversation: contractWriterB,
		Created: contractTimeB, Changed: contractTimeBAcc,
	}
}

func TestRecordsServeKeepForwardsTheOwnersAddition(t *testing.T) {
	t.Parallel()
	st, path := openContractStore(t)
	seedContractHistory(t, st)
	stage := &recordsStage{}
	client := contractsClient(t, designsAnswerHandler(t, store.NewReader(path), serveRecordsStage(t, stage)))

	// The owner's call names a session in its header. serve forwards no header, so orreryd records the owner.
	req := connect.NewRequest(&orreryv1.ContractsKeepRequest{
		Id: contractIDB, Addition: "Synthetic addition.", Words: "Synthetic keep.",
	})
	req.Header().Set(protocol.HeaderSession, recordsCallerSession)
	reply, err := client.Keep(t.Context(), req)
	if err != nil {
		t.Fatalf("Keep: %v", err)
	}

	want := &enginev1.KeepRequest{
		Contract: contractIDB, Addition: "Synthetic addition.", Words: "Synthetic keep.",
		Maker: &enginev1.StageMaker{Kind: "owner_serve"},
	}
	calls := stage.received()
	if !proto.Equal(calls.keep, want) {
		t.Fatalf("Keep forwarded %v, want %v", calls.keep, want)
	}
	if session := calls.keepHeader.Get(protocol.HeaderSession); session != "" {
		t.Fatalf("Keep forwarded session header %q, want none", session)
	}
	if got := reply.Msg.GetContract(); !proto.Equal(got, recordsContractB()) {
		t.Fatalf("Keep answered %v, want the contract as the reader holds it %v", got, recordsContractB())
	}
}

func TestRecordsServeStrikeForwardsTheOwnersStriking(t *testing.T) {
	t.Parallel()
	st, path := openContractStore(t)
	seedContractHistory(t, st)
	stage := &recordsStage{}
	client := contractsClient(t, designsAnswerHandler(t, store.NewReader(path), serveRecordsStage(t, stage)))

	reply, err := client.Strike(t.Context(), connect.NewRequest(&orreryv1.ContractsStrikeRequest{
		Id: contractIDB, Addition: "Synthetic addition.", Words: "Synthetic strike.",
	}))
	if err != nil {
		t.Fatalf("Strike: %v", err)
	}

	want := &enginev1.StrikeRequest{
		Contract: contractIDB, Addition: "Synthetic addition.", Words: "Synthetic strike.",
		Maker: &enginev1.StageMaker{Kind: "owner_serve"},
	}
	if calls := stage.received(); !proto.Equal(calls.strike, want) {
		t.Fatalf("Strike forwarded %v, want %v", calls.strike, want)
	}
	if got := reply.Msg.GetRemoved(); !slices.Equal(got, []string{"R1"}) {
		t.Fatalf("Strike removed %v, want the rows the stage removed [R1]", got)
	}
	if got := reply.Msg.GetContract(); !proto.Equal(got, recordsContractB()) {
		t.Fatalf("Strike answered %v, want the contract as the reader holds it %v", got, recordsContractB())
	}
}

func TestRecordsServeShownForwardsTheRowsOfAVersion(t *testing.T) {
	t.Parallel()
	st, path := openContractStore(t)
	seedContractHistory(t, st)
	stage := &recordsStage{}
	client := contractsClient(t, designsAnswerHandler(t, store.NewReader(path), serveRecordsStage(t, stage)))

	reply, err := client.Shown(t.Context(), connect.NewRequest(&orreryv1.ContractsShownRequest{
		Id: contractIDB, Version: 2, Rows: []string{"F1", "R1"},
	}))
	if err != nil {
		t.Fatalf("Shown: %v", err)
	}

	want := &enginev1.ShownRequest{Contract: contractIDB, Version: 2, Rows: []string{"F1", "R1"}}
	if calls := stage.received(); !proto.Equal(calls.shown, want) {
		t.Fatalf("Shown forwarded %v, want %v", calls.shown, want)
	}
	if got := reply.Msg.GetRows(); !slices.Equal(got, []string{"F1"}) {
		t.Fatalf("Shown answered rows %v, want the rows orreryd marked [F1]", got)
	}
}

func TestRecordsServeContinueForwardsTheDesign(t *testing.T) {
	t.Parallel()
	st, path := openContractStore(t)
	seedDesignQuestion(t, st)
	stage := &recordsStage{}
	client := designsClient(t, designsAnswerHandler(t, store.NewReader(path), serveRecordsStage(t, stage)))

	reply, err := client.Continue(t.Context(), connect.NewRequest(&orreryv1.DesignsContinueRequest{
		Id: designAnswerDesign,
	}))
	if err != nil {
		t.Fatalf("Continue: %v", err)
	}

	want := &enginev1.ContinueDesignRequest{Design: designAnswerDesign}
	if calls := stage.received(); !proto.Equal(calls.continued, want) {
		t.Fatalf("Continue forwarded %v, want %v", calls.continued, want)
	}
	if got := reply.Msg.GetDesign().GetId(); got != designAnswerDesign {
		t.Fatalf("Continue answered design %q, want %q", got, designAnswerDesign)
	}
	if got := reply.Msg.GetDesign().GetTitle(); got != "Design answer" {
		t.Fatalf("Continue answered design title %q, want the title the reader holds", got)
	}
	if got := reply.Msg.GetSession(); got != recordsSession {
		t.Fatalf("Continue answered session %q, want %q", got, recordsSession)
	}
}

func TestRecordsServeWithoutStageAnswersUnavailable(t *testing.T) {
	t.Parallel()
	handler := designsAnswerHandler(t, nil, nil)
	contracts := contractsClient(t, handler)
	designs := designsClient(t, handler)

	calls := map[string]func() error{
		"Keep": func() error {
			_, err := contracts.Keep(t.Context(), connect.NewRequest(&orreryv1.ContractsKeepRequest{Id: contractIDB}))
			return err
		},
		"Strike": func() error {
			_, err := contracts.Strike(t.Context(), connect.NewRequest(&orreryv1.ContractsStrikeRequest{Id: contractIDB}))
			return err
		},
		"Shown": func() error {
			_, err := contracts.Shown(t.Context(), connect.NewRequest(&orreryv1.ContractsShownRequest{Id: contractIDB}))
			return err
		},
		"Continue": func() error {
			_, err := designs.Continue(t.Context(), connect.NewRequest(&orreryv1.DesignsContinueRequest{
				Id: designAnswerDesign,
			}))
			return err
		},
	}
	for name, call := range calls {
		if code := connect.CodeOf(call()); code != connect.CodeUnavailable {
			t.Fatalf("%s: code = %v, want %v", name, code, connect.CodeUnavailable)
		}
	}
}
