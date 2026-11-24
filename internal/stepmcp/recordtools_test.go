package stepmcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"github.com/joshsymonds/orrery/internal/cli"
	"github.com/joshsymonds/orrery/internal/enginehost"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
)

// recordFake is a Stage and Contracts handler that records each record RPC's request in arrival order and answers it
// with an empty response. Every other RPC stays unimplemented.
type recordFake struct {
	enginev1connect.UnimplementedStageHandler
	enginev1connect.UnimplementedContractsHandler

	mu       sync.Mutex
	requests []proto.Message
}

// record appends msg to the requests the fake received.
func (f *recordFake) record(msg proto.Message) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, msg)
}

// last returns the request the fake received last, and fails the test when it received none.
func (f *recordFake) last(t *testing.T) proto.Message {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) == 0 {
		t.Fatal("no request arrived")
	}
	return f.requests[len(f.requests)-1]
}

func (f *recordFake) Approach(
	_ context.Context, req *connect.Request[v1.ApproachRequest],
) (*connect.Response[v1.ApproachResponse], error) {
	f.record(req.Msg)
	return connect.NewResponse(&v1.ApproachResponse{}), nil
}

func (f *recordFake) Choose(
	_ context.Context, req *connect.Request[v1.ChooseRequest],
) (*connect.Response[v1.ChooseResponse], error) {
	f.record(req.Msg)
	return connect.NewResponse(&v1.ChooseResponse{}), nil
}

func (f *recordFake) Delegate(
	_ context.Context, req *connect.Request[v1.DelegateRequest],
) (*connect.Response[v1.DelegateResponse], error) {
	f.record(req.Msg)
	return connect.NewResponse(&v1.DelegateResponse{}), nil
}

func (f *recordFake) WriteRow(
	_ context.Context, req *connect.Request[v1.WriteRowRequest],
) (*connect.Response[v1.WriteRowResponse], error) {
	f.record(req.Msg)
	return connect.NewResponse(&v1.WriteRowResponse{}), nil
}

func (f *recordFake) Keep(
	_ context.Context, req *connect.Request[v1.KeepRequest],
) (*connect.Response[v1.KeepResponse], error) {
	f.record(req.Msg)
	return connect.NewResponse(&v1.KeepResponse{}), nil
}

func (f *recordFake) Strike(
	_ context.Context, req *connect.Request[v1.StrikeRequest],
) (*connect.Response[v1.StrikeResponse], error) {
	f.record(req.Msg)
	return connect.NewResponse(&v1.StrikeResponse{}), nil
}

func (f *recordFake) AcceptContract(
	_ context.Context, req *connect.Request[v1.AcceptContractRequest],
) (*connect.Response[v1.AcceptContractResponse], error) {
	f.record(req.Msg)
	return connect.NewResponse(&v1.AcceptContractResponse{}), nil
}

func (f *recordFake) ProposeWorking(
	_ context.Context, req *connect.Request[v1.ProposeWorkingRequest],
) (*connect.Response[v1.ProposeWorkingResponse], error) {
	f.record(req.Msg)
	return connect.NewResponse(&v1.ProposeWorkingResponse{}), nil
}

// startRecordServer serves the Stage and Contracts services with fake on a sessions socket in a temporary directory. It
// mounts each service's handler as ServeWith does, since the contracts service takes a concrete store rather than a
// handler. It returns the clients that dial the socket, and the server stops at cleanup.
func startRecordServer(t *testing.T, fake *recordFake) (enginev1connect.StageClient, enginev1connect.ContractsClient) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sessions.sock")
	ln, err := enginehost.Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	mux := http.NewServeMux()
	stagePath, stageHandler := enginev1connect.NewStageHandler(fake)
	mux.Handle(stagePath, stageHandler)
	contractsPath, contractsHandler := enginev1connect.NewContractsHandler(fake)
	mux.Handle(contractsPath, contractsHandler)
	srv := &http.Server{Handler: mux}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	t.Cleanup(func() {
		if err := srv.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
		if err := <-done; !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("Serve: %v", err)
		}
	})
	stage := enginev1connect.NewStageClient(cli.SessionHTTPClient(path), cli.SessionBaseURL)
	contracts := enginev1connect.NewContractsClient(cli.SessionHTTPClient(path), cli.SessionBaseURL)
	return stage, contracts
}

// callTool runs the tool named name of tools with the JSON arguments args, as the server would, and fails the test when
// the call errs or the tool answers with a tool error.
func callTool(t *testing.T, tools []Tool, name, args string) {
	t.Helper()
	for _, tool := range tools {
		if tool.Name != name {
			continue
		}
		result, err := tool.Handler(t.Context(), Call{Arguments: json.RawMessage(args)})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if result.IsError {
			t.Fatalf("%s answered a tool error: %+v", name, result.Content)
		}
		return
	}
	t.Fatalf("no tool named %s", name)
}

// TestRecordsTools checks that each record tool sends its arguments to its RPC, and that contract_accept sends the
// maker it is given and sends none when it is given none.
func TestRecordsTools(t *testing.T) {
	fake := &recordFake{}
	stage, contracts := startRecordServer(t, fake)
	records, err := recordTools(stage, contracts)
	if err != nil {
		t.Fatalf("recordTools: %v", err)
	}
	accepts, err := contractTools(contracts)
	if err != nil {
		t.Fatalf("contractTools: %v", err)
	}
	tools := slices.Concat(records, accepts)

	t.Run("design_approach", func(t *testing.T) {
		callTool(t, tools, "design_approach", `{"design":"design-1","name":"split the store","shape":"two tables",`+
			`"cost":"a migration","wins":"reads","loses":"writes","reconsider":"when writes dominate",`+
			`"research":["step-1"],"recommended":true,"reason":"reads dominate"}`)
		assertRequest(t, fake, &v1.ApproachRequest{
			Design: "design-1", Name: "split the store", Shape: "two tables", Cost: "a migration", Wins: "reads",
			Loses: "writes", Reconsider: "when writes dominate", Research: []string{"step-1"}, Recommended: true,
			Reason: "reads dominate",
		})
	})

	t.Run("design_choose", func(t *testing.T) {
		callTool(t, tools, "design_choose", `{"design":"design-1","approach":2,"words":"go with two tables",`+
			`"maker":"conversation","reason":"the owner left it open","delegation":"pick the store shape"}`)
		assertRequest(t, fake, &v1.ChooseRequest{
			Design:   "design-1",
			Approach: 2,
			Words:    "go with two tables",
			Maker: &v1.StageMaker{
				Kind:       "conversation",
				Reason:     "the owner left it open",
				Delegation: "pick the store shape",
			},
		})
	})

	t.Run("design_delegate", func(t *testing.T) {
		callTool(t, tools, "design_delegate",
			`{"design":"design-1","kinds":["approach","question"],"message":"decide the rest"}`)
		assertRequest(t, fake, &v1.DelegateRequest{
			Design: "design-1", Kinds: []string{"approach", "question"}, Message: "decide the rest",
		})
	})

	t.Run("contract_row", func(t *testing.T) {
		callTool(t, tools, "contract_row", `{"contract":"contract-1","design":"design-1","row":"F2",`+
			`"value":"reads are cached","depends":"F1","words":"keep reads fast","maker":"owner_conversation",`+
			`"quote":"reads should be fast"}`)
		assertRequest(t, fake, &v1.WriteRowRequest{
			Contract: "contract-1", Design: "design-1", Row: "F2", Value: "reads are cached", Depends: "F1",
			Words: "keep reads fast", Maker: &v1.StageMaker{Kind: "owner_conversation", Quote: "reads should be fast"},
		})
	})

	t.Run("contract_keep", func(t *testing.T) {
		callTool(t, tools, "contract_keep",
			`{"contract":"contract-1","addition":"F4 the cache","words":"keep it","maker":"owner_serve"}`)
		assertRequest(t, fake, &v1.KeepRequest{
			Contract: "contract-1", Addition: "F4 the cache", Words: "keep it",
			Maker: &v1.StageMaker{Kind: "owner_serve"},
		})
	})

	t.Run("contract_strike", func(t *testing.T) {
		callTool(t, tools, "contract_strike", `{"contract":"contract-1","addition":"F4 the cache",`+
			`"words":"strike it","maker":"conversation","reason":"the owner did not mention it",`+
			`"delegation":"decide the small ones"}`)
		assertRequest(t, fake, &v1.StrikeRequest{
			Contract: "contract-1", Addition: "F4 the cache", Words: "strike it",
			Maker: &v1.StageMaker{
				Kind: "conversation", Reason: "the owner did not mention it", Delegation: "decide the small ones",
			},
		})
	})

	t.Run("contract_propose_working", func(t *testing.T) {
		callTool(t, tools, "contract_propose_working", `{"id":"contract-1","words":"propose the draft"}`)
		assertRequest(t, fake, &v1.ProposeWorkingRequest{Id: "contract-1", Words: "propose the draft"})
	})

	t.Run("contract_accept_maker", func(t *testing.T) {
		callTool(t, tools, "contract_accept", `{"id":"contract-1","words":"accepted"}`)
		assertRequest(t, fake, &v1.AcceptContractRequest{Id: "contract-1", Words: "accepted"})

		callTool(t, tools, "contract_accept", `{"id":"contract-1","words":"accepted","maker":"owner_conversation",`+
			`"quote":"yes, accept it"}`)
		assertRequest(t, fake, &v1.AcceptContractRequest{
			Id:    "contract-1",
			Words: "accepted",
			Maker: &v1.StageMaker{Kind: "owner_conversation", Quote: "yes, accept it"},
		})
	})
}

// assertRequest fails the test unless the last request fake received equals want.
func assertRequest(t *testing.T, fake *recordFake, want proto.Message) {
	t.Helper()
	if got := fake.last(t); !proto.Equal(got, want) {
		t.Errorf("request = %v, want %v", got, want)
	}
}
