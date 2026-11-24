package stepmcp

import (
	"context"
	"slices"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
)

// fakeDesigns answers the design commands with a stored design that carries the request's id, and records each request.
type fakeDesigns struct {
	mu      sync.Mutex
	creates []*v1.CreateDesignRequest
	gets    []*v1.GetDesignRequest
	lists   []*v1.ListDesignsRequest
	orders  []*v1.OrderDesignRequest
}

func (f *fakeDesigns) CreateDesign(
	_ context.Context,
	req *connect.Request[v1.CreateDesignRequest],
) (*connect.Response[v1.CreateDesignResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.creates = append(f.creates, req.Msg)
	return connect.NewResponse(&v1.CreateDesignResponse{Design: &v1.StoredDesign{Id: req.Msg.GetId()}}), nil
}

func (f *fakeDesigns) GetDesign(
	_ context.Context,
	req *connect.Request[v1.GetDesignRequest],
) (*connect.Response[v1.GetDesignResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets = append(f.gets, req.Msg)
	return connect.NewResponse(&v1.GetDesignResponse{Design: &v1.StoredDesign{Id: req.Msg.GetId()}}), nil
}

func (f *fakeDesigns) ListDesigns(
	_ context.Context,
	req *connect.Request[v1.ListDesignsRequest],
) (*connect.Response[v1.ListDesignsResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lists = append(f.lists, req.Msg)
	return connect.NewResponse(&v1.ListDesignsResponse{Designs: []*v1.StoredDesign{{Id: "design-listed"}}}), nil
}

func (f *fakeDesigns) OrderDesign(
	_ context.Context,
	req *connect.Request[v1.OrderDesignRequest],
) (*connect.Response[v1.OrderDesignResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.orders = append(f.orders, req.Msg)
	return connect.NewResponse(&v1.OrderDesignResponse{Design: &v1.StoredDesign{Id: req.Msg.GetDesign()}}), nil
}

// fakeDesignCalls is a copy of what the fake recorded.
type fakeDesignCalls struct {
	creates []*v1.CreateDesignRequest
	gets    []*v1.GetDesignRequest
	lists   []*v1.ListDesignsRequest
	orders  []*v1.OrderDesignRequest
}

func (f *fakeDesigns) calls() fakeDesignCalls {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fakeDesignCalls{
		creates: slices.Clone(f.creates),
		gets:    slices.Clone(f.gets),
		lists:   slices.Clone(f.lists),
		orders:  slices.Clone(f.orders),
	}
}

// designPeer serves an MCP server whose design tools call designs, and completes the handshake. A nil designs answers
// each design tool with a tool error.
func designPeer(t *testing.T, designs enginev1connect.DesignsClient) *peer {
	t.Helper()
	server, err := NewServer(nil, nil, nil, nil, WithDesigns(designs))
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	p, _ := servePipes(t, server)
	initialize(p)
	return p
}

// decodeDesignReply decodes a tool's text as the protobuf JSON of msg. It fails the test when the text does not decode.
func decodeDesignReply(t *testing.T, text string, msg proto.Message) {
	t.Helper()
	if err := protojson.Unmarshal([]byte(text), msg); err != nil {
		t.Fatalf("decode reply %s: %v", text, err)
	}
}

// TestDesignToolsArguments checks that each design tool sends its arguments to the Designs client, and that its reply
// is the client's answer, as protobuf JSON.
func TestDesignToolsArguments(t *testing.T) {
	t.Parallel()
	fake := &fakeDesigns{}
	p := designPeer(t, fake)
	const (
		first  = "synthetic-repo-1b2c3d4/epic-first"
		second = "synthetic-repo-2c3d4e5/epic-second"
	)

	created := p.tool("design_create", `{"id":"design-one","title":"Design one"}`)
	got := p.tool("design_get", `{"id":"design-one"}`)
	listed := p.tool("design_list", `{}`)
	ordered := p.tool("design_order", `{"design":"design-one","contract":"`+second+`","after":"`+first+`"}`)
	for name, outcome := range map[string]toolOutcome{
		"design_create": created, "design_get": got, "design_list": listed, "design_order": ordered,
	} {
		if outcome.isError {
			t.Fatalf("%s refused: %s", name, outcome.text)
		}
	}

	var createReply v1.CreateDesignResponse
	decodeDesignReply(t, created.text, &createReply)
	if createReply.GetDesign().GetId() != "design-one" {
		t.Errorf("design_create reply = %v, want the client's design-one", &createReply)
	}
	var getReply v1.GetDesignResponse
	decodeDesignReply(t, got.text, &getReply)
	if getReply.GetDesign().GetId() != "design-one" {
		t.Errorf("design_get reply = %v, want the client's design-one", &getReply)
	}
	var listReply v1.ListDesignsResponse
	decodeDesignReply(t, listed.text, &listReply)
	if len(listReply.GetDesigns()) != 1 || listReply.GetDesigns()[0].GetId() != "design-listed" {
		t.Errorf("design_list reply = %v, want the client's one design", &listReply)
	}
	var orderReply v1.OrderDesignResponse
	decodeDesignReply(t, ordered.text, &orderReply)
	if orderReply.GetDesign().GetId() != "design-one" {
		t.Errorf("design_order reply = %v, want the client's design-one", &orderReply)
	}

	calls := fake.calls()
	if len(calls.creates) != 1 || calls.creates[0].GetId() != "design-one" ||
		calls.creates[0].GetTitle() != "Design one" {
		t.Errorf("create requests = %v, want one for design-one titled Design one", calls.creates)
	}
	if len(calls.gets) != 1 || calls.gets[0].GetId() != "design-one" {
		t.Errorf("get requests = %v, want one for design-one", calls.gets)
	}
	if len(calls.lists) != 1 {
		t.Errorf("list requests = %d, want 1", len(calls.lists))
	}
	if len(calls.orders) != 1 || calls.orders[0].GetDesign() != "design-one" ||
		calls.orders[0].GetContract() != second || !slices.Equal(calls.orders[0].GetAfter(), []string{first}) {
		t.Errorf("order requests = %v, want one making %s start after %s in design-one", calls.orders, second, first)
	}
}

// TestDesignToolsWithoutClientAnswerUnavailable checks that with no Designs client each design tool answers with a tool
// error that says the tools have no orreryd connection.
func TestDesignToolsWithoutClientAnswerUnavailable(t *testing.T) {
	t.Parallel()
	p := designPeer(t, nil)
	calls := []struct {
		name      string
		arguments string
	}{
		{name: "design_create", arguments: `{"id":"design-one","title":"Design one"}`},
		{name: "design_get", arguments: `{"id":"design-one"}`},
		{name: "design_list", arguments: `{}`},
		{name: "design_order", arguments: `{"design":"design-one","contract":"c2","after":"c1"}`},
	}
	for _, call := range calls {
		outcome := p.tool(call.name, call.arguments)
		if !outcome.isError || outcome.text != designUnavailableText {
			t.Errorf("%s = %q (error %t), want the unavailable tool error", call.name, outcome.text, outcome.isError)
		}
	}
}

// TestDesignToolsWriteToolsAreMutating checks that the design writes are mutating tools, whose cancel does not reach
// their daemon call, and that the reads are not.
func TestDesignToolsWriteToolsAreMutating(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"design_create", "design_order"} {
		if !mutatingTool(name) {
			t.Errorf("%s is not a mutating tool", name)
		}
	}
	for _, name := range []string{"design_get", "design_list"} {
		if mutatingTool(name) {
			t.Errorf("%s is a mutating tool", name)
		}
	}
}
