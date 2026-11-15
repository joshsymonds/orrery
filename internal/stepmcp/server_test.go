package stepmcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"reflect"
	"testing"
)

// servePipes serves server over pipes and returns the peer that drives it, with the function that cancels the
// serving. The test's cleanup cancels the serving and closes the pipes, and fails the test unless Serve returns nil.
// It has no deadline of its own.
func servePipes(t *testing.T, server *Server) (*peer, context.CancelFunc) {
	t.Helper()
	serverInput, clientInput := io.Pipe()
	outputRead, serverOutput := io.Pipe()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, serverInput, serverOutput) }()
	t.Cleanup(func() {
		cancel()
		_ = clientInput.Close()
		_ = outputRead.Close()
		err := <-done
		if err != nil {
			t.Errorf("Serve: %v", err)
		}
	})
	return &peer{
		t: t, input: clientInput, outputRead: outputRead,
		output: json.NewDecoder(bufio.NewReader(outputRead)),
	}, cancel
}

// initialize completes the handshake of p.
func initialize(p *peer) {
	p.call("initialize", map[string]any{
		"protocolVersion": "2025-11-25",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]string{"name": "test", "version": "1"},
	})
	p.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
}

// requireEmptyPingResult fails the test unless reply answers a ping with an empty result object.
func requireEmptyPingResult(t *testing.T, when string, reply rpcReply) {
	t.Helper()
	if reply.Error != nil {
		t.Fatalf("ping %s: JSON-RPC error %d: %s", when, reply.Error.Code, reply.Error.Message)
	}
	if string(reply.Result) != "{}" {
		t.Fatalf("ping %s result = %s, want an empty object", when, reply.Result)
	}
}

// TestPingGetsAnEmptyResultBeforeAndAfterInitialize checks that the server answers ping before the handshake and
// again once the handshake has completed, each time with an empty result object.
func TestPingGetsAnEmptyResultBeforeAndAfterInitialize(t *testing.T) {
	t.Parallel()
	server, err := NewServer(nil, nil, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	p, _ := servePipes(t, server)

	requireEmptyPingResult(t, "before initialize", p.call("ping", map[string]any{}))

	initialize(p)

	requireEmptyPingResult(t, "after initialize", p.call("ping", map[string]any{}))
}

// TestInitializeNamesTheServerAndAdvertisesOnlyTools checks the server identity and that the only capability it
// advertises is tools, without listChanged.
func TestInitializeNamesTheServerAndAdvertisesOnlyTools(t *testing.T) {
	t.Parallel()
	server, err := NewServer(nil, nil, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	p, _ := servePipes(t, server)

	reply := p.call("initialize", map[string]any{
		"protocolVersion": "2025-11-25",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]string{"name": "test", "version": "1"},
	})
	if reply.Error != nil {
		t.Fatalf("initialize: JSON-RPC error %d: %s", reply.Error.Code, reply.Error.Message)
	}
	var result struct {
		ProtocolVersion string         `json:"protocolVersion"`
		Capabilities    map[string]any `json:"capabilities"`
		ServerInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(reply.Result, &result); err != nil {
		t.Fatalf("decode initialize result: %v", err)
	}
	if result.ProtocolVersion != "2025-11-25" {
		t.Errorf("protocolVersion = %q, want 2025-11-25", result.ProtocolVersion)
	}
	if result.ServerInfo.Name != "orrery" || result.ServerInfo.Version != "1" {
		t.Errorf("serverInfo = %+v, want name orrery and version 1", result.ServerInfo)
	}
	if want := map[string]any{"tools": map[string]any{}}; !reflect.DeepEqual(result.Capabilities, want) {
		t.Errorf("capabilities = %v, want %v", result.Capabilities, want)
	}
}

// echoServer serves one tool, echo, whose single text block is the arguments it received.
func echoServer() *Server {
	return &Server{tools: []Tool{{
		Name:        "echo",
		Description: "returns its arguments",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(_ context.Context, call Call) (ToolResult, error) {
			return ToolResult{Content: []Content{{Type: textContentType, Text: string(call.Arguments)}}}, nil
		},
	}}}
}

// TestToolArgumentsAbsentAreAnEmptyObjectAndNonObjectsAreRefused checks that a call without arguments reaches the
// handler as an empty object, and that arguments which are not an object are refused as invalid params.
func TestToolArgumentsAbsentAreAnEmptyObjectAndNonObjectsAreRefused(t *testing.T) {
	t.Parallel()
	p, _ := servePipes(t, echoServer())
	initialize(p)

	absent := p.call("tools/call", map[string]any{"name": "echo"})
	if absent.Error != nil {
		t.Fatalf("tools/call without arguments: JSON-RPC error %d: %s", absent.Error.Code, absent.Error.Message)
	}
	var echoed struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(absent.Result, &echoed); err != nil {
		t.Fatalf("decode tools/call result: %v", err)
	}
	if len(echoed.Content) != 1 || echoed.Content[0].Text != "{}" {
		t.Fatalf("handler received %+v, want the text {}", echoed.Content)
	}

	refused := p.call("tools/call", map[string]any{"name": "echo", "arguments": []int{1}})
	if refused.Error == nil || refused.Error.Code != -32602 {
		t.Fatalf("tools/call with array arguments: reply %s, want JSON-RPC error -32602", refused.Result)
	}
}

// TestCancelEndsRunningToolHandler checks that canceling the context Serve runs under ends a running tool handler:
// the handler sees its context end and returns, and Serve returns nil once it has.
func TestCancelEndsRunningToolHandler(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	returned := make(chan struct{})
	server := &Server{tools: []Tool{{
		Name:        "block",
		Description: "blocks until its context ends",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(ctx context.Context, _ Call) (ToolResult, error) {
			close(started)
			<-ctx.Done()
			close(returned)
			return ToolResult{}, ctx.Err()
		},
	}}}
	p, cancel := servePipes(t, server)
	initialize(p)

	p.send(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "block", "arguments": map[string]any{}},
	})
	<-started
	cancel()
	<-returned
}
