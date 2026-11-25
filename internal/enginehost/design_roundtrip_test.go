package enginehost

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/stepmcp"
	"github.com/joshsymonds/orrery/internal/store"
)

// designMCPSession serves the Contracts and Designs services over st on a sessions socket, and returns the MCP client of
// a stepmcp server whose contract and design tools call them. Each call carries session as its caller. The serving and
// the connection end at cleanup.
func designMCPSession(t *testing.T, st *store.Store, session string) *mcp.ClientSession {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sessions.sock")
	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	cfg := ServeConfig{
		Contracts: contractTestContracts(st),
		Designs:   &Designs{Store: st, Now: designTestAt},
	}
	go func() { done <- ServeWith(ctx, ln, cfg) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("ServeWith after cleanup: %v", err)
		}
	})
	getenv := func(name string) string {
		if name == protocol.EnvSession {
			return session
		}
		return ""
	}
	client := unixHTTPClient(t, path)
	contracts := enginev1connect.NewContractsClient(client, "http://contracts.invalid",
		connect.WithInterceptors(stepmcp.CallerInterceptor(getenv)))
	designs := enginev1connect.NewDesignsClient(client, "http://designs.invalid",
		connect.WithInterceptors(stepmcp.CallerInterceptor(getenv)))
	server, err := stepmcp.NewServer(nil, nil, contracts, nil, stepmcp.WithDesigns(designs))
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return designMCPConnect(t, server)
}

// designMCPConnect serves server over in-memory pipes and returns the MCP client connected to it. The serving and the
// connection end at cleanup.
func designMCPConnect(t *testing.T, server *stepmcp.Server) *mcp.ClientSession {
	t.Helper()
	serverInput, clientOutput := io.Pipe()
	clientInput, serverOutput := io.Pipe()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, serverInput, serverOutput) }()
	t.Cleanup(func() {
		cancel()
		if err := clientOutput.Close(); err != nil {
			t.Errorf("close client output: %v", err)
		}
		if err := clientInput.Close(); err != nil {
			t.Errorf("close client input: %v", err)
		}
		if err := <-done; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	connected, err := client.Connect(ctx, &mcp.IOTransport{Reader: clientInput, Writer: clientOutput}, nil)
	if err != nil {
		t.Fatalf("MCP Connect: %v", err)
	}
	t.Cleanup(func() {
		if err := connected.Close(); err != nil {
			t.Errorf("MCP session Close: %v", err)
		}
	})
	return connected
}

// designCall calls the tool name over session with arguments, and returns its result. A refusal is a result with
// IsError set, not an error of the call.
func designCall(t *testing.T, session *mcp.ClientSession, name string, arguments map[string]any) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	return result
}

// designOutcome returns the text of the single text block of result, and whether the tool refused the call.
func designOutcome(t *testing.T, result *mcp.CallToolResult) (string, bool) {
	t.Helper()
	if len(result.Content) != 1 {
		t.Fatalf("content = %+v, want one block", result.Content)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content = %T, want a text block", result.Content[0])
	}
	return text.Text, result.IsError
}

// designReply decodes the text of result, which must not be a refusal, as the protobuf JSON of msg.
func designReply(t *testing.T, result *mcp.CallToolResult, msg proto.Message) {
	t.Helper()
	text, refused := designOutcome(t, result)
	if refused {
		t.Fatalf("tool refused: %s", text)
	}
	if err := protojson.Unmarshal([]byte(text), msg); err != nil {
		t.Fatalf("decode reply %s: %v", text, err)
	}
}

// designContract returns the contract with id in design, or nil when the design holds none.
func designContract(design *v1.StoredDesign, id string) *v1.StoredDesignContract {
	for _, contract := range design.GetContracts() {
		if contract.GetId() == id {
			return contract
		}
	}
	return nil
}

// writeDesignContractFile writes body as the contract file contract_propose reads, and returns its path.
func writeDesignContractFile(t *testing.T, body *v1.EpicContract) string {
	t.Helper()
	raw, err := protojson.Marshal(body)
	if err != nil {
		t.Fatalf("encode contract %s: %v", body.GetId(), err)
	}
	path := filepath.Join(t.TempDir(), "contract.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write contract file: %v", err)
	}
	return path
}

// TestDesignRoundTripMCP drives design_create, design_get, design_list and design_order over MCP through the real
// Designs service. The session creates a design, which get and list read back as created. Two contracts stored in the
// design are then ordered over MCP, the reply carries the edge, and get reads the same design. A get of an id no one
// holds and an order that closes a cycle are refused as tool errors.
func TestDesignRoundTripMCP(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	seedContractSessions(t, st)
	session := designMCPSession(t, st, contractTestHeadless)

	var created v1.CreateDesignResponse
	designReply(t, designCall(t, session, "design_create", map[string]any{
		"id": "design-one", "title": "Design one",
	}), &created)
	if got := created.GetDesign(); got.GetId() != "design-one" || got.GetTitle() != "Design one" ||
		got.GetConversation() != contractTestHeadless || got.GetState() != "no contracts yet" {
		t.Fatalf("design_create = %v, want design-one titled Design one in engine-1, with no contracts yet", got)
	}

	var read v1.GetDesignResponse
	designReply(t, designCall(t, session, "design_get", map[string]any{"id": "design-one"}), &read)
	if !proto.Equal(read.GetDesign(), created.GetDesign()) {
		t.Fatalf("design_get = %v, want the created design %v", read.GetDesign(), created.GetDesign())
	}

	var listed v1.ListDesignsResponse
	designReply(t, designCall(t, session, "design_list", map[string]any{}), &listed)
	if len(listed.GetDesigns()) != 1 || !proto.Equal(listed.GetDesigns()[0], created.GetDesign()) {
		t.Fatalf("design_list = %v, want the one created design", listed.GetDesigns())
	}

	const (
		first  = "synthetic-repo-3d4e5f6/epic-first"
		second = "synthetic-repo-4e5f6a7/epic-second"
	)
	storeDesignTestContract(t, st, "design-one", first)
	storeDesignTestContract(t, st, "design-one", second)

	var ordered v1.OrderDesignResponse
	designReply(t, designCall(t, session, "design_order", map[string]any{
		"design": "design-one", "contract": second, "after": first,
	}), &ordered)
	if got := designContract(ordered.GetDesign(), second).GetAfter(); !slices.Equal(got, []string{first}) {
		t.Fatalf("design_order %s starts after %v, want %s", second, got, first)
	}

	var reread v1.GetDesignResponse
	designReply(t, designCall(t, session, "design_get", map[string]any{"id": "design-one"}), &reread)
	if !proto.Equal(reread.GetDesign(), ordered.GetDesign()) {
		t.Fatalf("design_get after the order = %v, want the ordered design %v", reread.GetDesign(), ordered.GetDesign())
	}

	text, refused := designOutcome(t, designCall(t, session, "design_get", map[string]any{"id": "design-missing"}))
	if !refused || !strings.Contains(text, "design not found") {
		t.Errorf("design_get of a missing design = %q (refused %t), want a refusal naming it not found", text, refused)
	}
	text, refused = designOutcome(t, designCall(t, session, "design_order", map[string]any{
		"design": "design-one", "contract": first, "after": second,
	}))
	if !refused || !strings.Contains(text, "cycle") {
		t.Errorf("design_order closing a cycle = %q (refused %t), want a refusal naming the cycle", text, refused)
	}
}

// TestDesignRoundTripProposeOtherRepository proposes two contracts over MCP into one design. The second contract is in
// a repository other than the first's. Both join the design in the order they were proposed, and each keeps its own
// repository, so the design of two lists them and no design of one.
func TestDesignRoundTripProposeOtherRepository(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	seedContractSessions(t, st)
	session := designMCPSession(t, st, contractTestHeadless)
	var created v1.CreateDesignResponse
	designReply(t, designCall(t, session, "design_create", map[string]any{
		"id": "design-one", "title": "Design one",
	}), &created)

	repoA, repoB := stepGitTestRepo(t), stepGitTestRepo(t)
	first := contractTestBody(repoA)
	second := contractTestBody(repoB)
	second.Id = designTestSecondID
	for _, body := range []*v1.EpicContract{first, second} {
		var proposed v1.ProposeContractResponse
		designReply(t, designCall(t, session, "contract_propose", map[string]any{
			"id":     body.GetId(),
			"path":   writeDesignContractFile(t, body),
			"words":  contractTestWords,
			"design": "design-one",
		}), &proposed)
		if proposed.GetContract().GetId() != body.GetId() {
			t.Fatalf("contract_propose of %s = %v, want that contract", body.GetId(), proposed.GetContract())
		}
	}

	var read v1.GetDesignResponse
	designReply(t, designCall(t, session, "design_get", map[string]any{"id": "design-one"}), &read)
	want := []struct {
		id         string
		repository string
		position   int64
	}{
		{id: first.GetId(), repository: repoA, position: 1},
		{id: second.GetId(), repository: repoB, position: 2},
	}
	contracts := read.GetDesign().GetContracts()
	if len(contracts) != len(want) {
		t.Fatalf("design-one holds %d contracts, want %d", len(contracts), len(want))
	}
	for i, w := range want {
		got := contracts[i]
		if got.GetId() != w.id || got.GetRepository() != w.repository || got.GetPosition() != w.position ||
			got.GetState() != "proposed" {
			t.Errorf("design-one contract %d = %v, want %s in %s at position %d, proposed",
				i, got, w.id, w.repository, w.position)
		}
	}

	var listed v1.ListDesignsResponse
	designReply(t, designCall(t, session, "design_list", map[string]any{}), &listed)
	if len(listed.GetDesigns()) != 1 || listed.GetDesigns()[0].GetId() != "design-one" {
		t.Fatalf("design_list = %v, want only design-one, with no design of one for the contracts in it",
			listed.GetDesigns())
	}
}

// TestDesignOfOneMCP proposes a contract with no design over MCP. It reads through design_get by the contract's id, and
// through design_list, as a design of one: its id is the contract's, its title the contract's, and it holds that one
// contract at position 1.
func TestDesignOfOneMCP(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	seedContractSessions(t, st)
	session := designMCPSession(t, st, contractTestHeadless)
	body := contractTestBody(stepGitTestRepo(t))
	var proposed v1.ProposeContractResponse
	designReply(t, designCall(t, session, "contract_propose", map[string]any{
		"id": body.GetId(), "path": writeDesignContractFile(t, body), "words": contractTestWords,
	}), &proposed)

	var read v1.GetDesignResponse
	designReply(t, designCall(t, session, "design_get", map[string]any{"id": body.GetId()}), &read)
	got := read.GetDesign()
	if got.GetId() != body.GetId() || !got.GetOfOne() || got.GetTitle() != body.GetTitle() ||
		len(got.GetContracts()) != 1 {
		t.Fatalf("design_get = %v, want a design of one for %s titled %s", got, body.GetId(), body.GetTitle())
	}
	if only := got.GetContracts()[0]; only.GetId() != body.GetId() || only.GetPosition() != 1 ||
		only.GetRepository() != body.GetRepository() {
		t.Errorf("design of one holds %v, want %s at position 1 in %s", only, body.GetId(), body.GetRepository())
	}

	var listed v1.ListDesignsResponse
	designReply(t, designCall(t, session, "design_list", map[string]any{}), &listed)
	if len(listed.GetDesigns()) != 1 || !proto.Equal(listed.GetDesigns()[0], got) {
		t.Fatalf("design_list = %v, want the one design of one %v", listed.GetDesigns(), got)
	}
}
