package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

const testTimeout = 5 * time.Second

type testPeer struct {
	input  *io.PipeWriter
	output *json.Decoder
	cancel context.CancelFunc
	done   <-chan error
}

func newTestPeer(t *testing.T, server *Server) *testPeer {
	t.Helper()
	serverInput, clientInput := io.Pipe()
	clientOutput, serverOutput := io.Pipe()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx, serverInput, serverOutput)
	}()
	peer := &testPeer{
		input: clientInput, output: json.NewDecoder(clientOutput), cancel: cancel, done: done,
	}
	t.Cleanup(func() {
		cancel()
		_ = clientInput.Close()
		_ = clientOutput.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve: %v", err)
			}
		case <-time.After(testTimeout):
			t.Error("Serve did not stop")
		}
	})
	return peer
}

func newTestServer(t *testing.T, tools ...Tool) *Server {
	t.Helper()
	server, err := NewServer("test-server", "1.0.0", tools...)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return server
}

func (peer *testPeer) send(t *testing.T, value any) {
	t.Helper()
	if err := json.NewEncoder(peer.input).Encode(value); err != nil {
		t.Fatalf("send MCP message: %v", err)
	}
}

func (peer *testPeer) sendRaw(t *testing.T, value string) {
	t.Helper()
	if _, err := io.WriteString(peer.input, value+"\n"); err != nil {
		t.Fatalf("send raw MCP message: %v", err)
	}
}

func (peer *testPeer) receive(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	received := make(chan map[string]json.RawMessage, 1)
	errs := make(chan error, 1)
	go func() {
		var message map[string]json.RawMessage
		if err := peer.output.Decode(&message); err != nil {
			errs <- err
			return
		}
		received <- message
	}()
	select {
	case message := <-received:
		return message
	case err := <-errs:
		t.Fatalf("receive MCP message: %v", err)
	case <-time.After(testTimeout):
		t.Fatal("timed out receiving MCP message")
	}
	return nil
}

func (peer *testPeer) initialize(t *testing.T) {
	t.Helper()
	peer.send(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo": map[string]any{
				"name": "test-client", "version": "1.0.0",
			},
		},
	})
	response := peer.receive(t)
	assertJSONID(t, response, "1")
	if _, exists := response["error"]; exists {
		t.Fatalf("initialize error = %s", response["error"])
	}
	peer.send(t, map[string]any{
		"jsonrpc": "2.0", "method": "notifications/initialized",
	})
}

func TestServerInitializeHandshake(t *testing.T) {
	peer := newTestPeer(t, newTestServer(t))
	peer.send(t, map[string]any{
		"jsonrpc": "2.0", "id": "init-1", "method": "initialize",
		"params": map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "client", "version": "2.1.212"},
		},
	})

	message := peer.receive(t)
	assertJSONID(t, message, `"init-1"`)
	var result struct {
		ProtocolVersion string `json:"protocolVersion"`
		Capabilities    struct {
			Tools struct {
				ListChanged bool `json:"listChanged"`
			} `json:"tools"`
		} `json:"capabilities"`
		ServerInfo struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(message["result"], &result); err != nil {
		t.Fatalf("decode initialize result: %v", err)
	}
	if result.ProtocolVersion != ProtocolVersion || result.ServerInfo.Name != "test-server" ||
		result.ServerInfo.Version != "1.0.0" || result.Capabilities.Tools.ListChanged {
		t.Fatalf("initialize result = %+v", result)
	}
}

func TestServerListsTools(t *testing.T) {
	inputSchema := json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}}}`)
	peer := newTestPeer(t, newTestServer(t, Tool{
		Name: "greet", Description: "Greet someone.", InputSchema: inputSchema,
		Handler: func(context.Context, Call) (ToolResult, error) { return ToolResult{}, nil },
	}))
	peer.initialize(t)
	peer.send(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": map[string]any{},
	})

	message := peer.receive(t)
	var result struct {
		Tools []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(message["result"], &result); err != nil {
		t.Fatalf("decode tools/list result: %v", err)
	}
	if len(result.Tools) != 1 || result.Tools[0].Name != "greet" ||
		result.Tools[0].Description != "Greet someone." ||
		!jsonEqual(result.Tools[0].InputSchema, inputSchema) {
		t.Fatalf("tools/list result = %s", message["result"])
	}
}

func TestNewServerRejectsSchemaWithoutObjectType(t *testing.T) {
	_, err := NewServer("test-server", "1.0.0", Tool{
		Name: "incompatible", InputSchema: json.RawMessage(`{"oneOf":[{"type":"object"}]}`),
		Handler: func(context.Context, Call) (ToolResult, error) { return ToolResult{}, nil },
	})
	if err == nil || !strings.Contains(err.Error(), `top-level type must be "object"`) {
		t.Fatalf("NewServer error = %v, want client-compatible object-type rejection", err)
	}
}

func TestServerCallsToolAndMapsHandlerError(t *testing.T) {
	peer := newTestPeer(t, newTestServer(t,
		Tool{
			Name: "echo", Description: "Echo input.", InputSchema: json.RawMessage(`{"type":"object"}`),
			Handler: func(_ context.Context, call Call) (ToolResult, error) {
				return ToolResult{Content: []Content{{Type: "text", Text: string(call.Arguments)}}}, nil
			},
		},
		Tool{
			Name: "fail", Description: "Fail.", InputSchema: json.RawMessage(`{"type":"object"}`),
			Handler: func(context.Context, Call) (ToolResult, error) {
				return ToolResult{}, errors.New("handler failed")
			},
		},
	))
	peer.initialize(t)

	peer.send(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "echo", "arguments": map[string]any{"value": "hello"}},
	})
	message := peer.receive(t)
	var result ToolResult
	if err := json.Unmarshal(message["result"], &result); err != nil {
		t.Fatalf("decode tools/call result: %v", err)
	}
	if result.IsError || len(result.Content) != 1 || result.Content[0].Type != "text" ||
		!jsonEqual(json.RawMessage(result.Content[0].Text), json.RawMessage(`{"value":"hello"}`)) {
		t.Fatalf("tools/call result = %+v", result)
	}

	peer.send(t, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "fail", "arguments": map[string]any{}},
	})
	message = peer.receive(t)
	if err := json.Unmarshal(message["result"], &result); err != nil {
		t.Fatalf("decode failed tools/call result: %v", err)
	}
	if !result.IsError || !slices.Equal(result.Content, []Content{{Type: "text", Text: "handler failed"}}) {
		t.Fatalf("failed tools/call result = %+v", result)
	}
}

func TestServerReportsMalformedInputAndContinues(t *testing.T) {
	peer := newTestPeer(t, newTestServer(t))
	peer.sendRaw(t, `{not-json}`)
	message := peer.receive(t)
	if string(message["id"]) != "null" {
		t.Fatalf("parse error id = %s, want null", message["id"])
	}
	var rpcErr RPCError
	if err := json.Unmarshal(message["error"], &rpcErr); err != nil {
		t.Fatalf("decode parse error: %v", err)
	}
	if rpcErr.Code != ParseErrorCode || rpcErr.Message != "Parse error" {
		t.Fatalf("parse error = %+v", rpcErr)
	}

	peer.initialize(t)
}

func TestServerRefusesRequestsBeforeInitialization(t *testing.T) {
	peer := newTestPeer(t, newTestServer(t, Tool{
		Name: "echo", Description: "Echo input.", InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(context.Context, Call) (ToolResult, error) { return ToolResult{}, nil },
	}))
	peer.send(t, map[string]any{
		"jsonrpc": "2.0", "id": "early", "method": "tools/list", "params": map[string]any{},
	})
	message := peer.receive(t)
	assertJSONID(t, message, `"early"`)
	assertRPCError(t, message, InvalidRequestCode, "Connection is not initialized")
}

func TestServerIgnoresPeerResponseLines(t *testing.T) {
	peer := newTestPeer(t, newTestServer(t))
	peer.initialize(t)
	// The peer's responses answer requests this server never sent. The writes run
	// beside the receive: a server that replied would block on its output pipe, and
	// the receive below then reports that reply instead of the test hanging.
	go func() {
		_, _ = io.WriteString(peer.input, `{"jsonrpc":"2.0","id":"peer-1","result":{"ok":true}}`+"\n")
		_, _ = io.WriteString(peer.input,
			`{"jsonrpc":"2.0","id":"peer-2","error":{"code":-32000,"message":"peer"}}`+"\n")
		_ = json.NewEncoder(peer.input).Encode(map[string]any{
			"jsonrpc": "2.0", "id": "after-response", "method": "tools/list",
			"params": map[string]any{},
		})
	}()

	message := peer.receive(t)
	assertJSONID(t, message, `"after-response"`)
	if _, exists := message["error"]; exists {
		t.Fatalf("request after peer responses error = %s", message["error"])
	}
}

func TestServerRejectsOversizedMessageAndKeepsStreamUsable(t *testing.T) {
	peer := newTestPeer(t, newTestServer(t))
	peer.initialize(t)

	peer.sendRaw(t, paddedRequest(t, "at-limit", MaxMessageBytes))
	message := peer.receive(t)
	assertJSONID(t, message, `"at-limit"`)
	if _, exists := message["error"]; exists {
		t.Fatalf("request at the size limit error = %s", message["error"])
	}

	peer.sendRaw(t, paddedRequest(t, "over-limit", MaxMessageBytes+1))
	message = peer.receive(t)
	assertJSONID(t, message, "null")
	assertRPCError(
		t,
		message,
		ParseErrorCode,
		fmt.Sprintf("Message exceeds maximum of %d bytes", MaxMessageBytes),
	)

	peer.send(t, map[string]any{
		"jsonrpc": "2.0", "id": "after-oversized", "method": "tools/list",
		"params": map[string]any{},
	})
	message = peer.receive(t)
	assertJSONID(t, message, `"after-oversized"`)
	if _, exists := message["error"]; exists {
		t.Fatalf("request after an oversized message error = %s", message["error"])
	}
}

func TestServerBoundsMemoryWhileFramingUnterminatedMessage(t *testing.T) {
	const (
		unterminatedBytes = 16 << 20
		chunkBytes        = 64 << 10
		maxRetainedBytes  = 4 << 20
	)
	peer := newTestPeer(t, newTestServer(t))
	peer.initialize(t)

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	chunk := bytes.Repeat([]byte("p"), chunkBytes)
	for written := 0; written < unterminatedBytes; written += len(chunk) {
		if _, err := peer.input.Write(chunk); err != nil {
			t.Fatalf("write unterminated message: %v", err)
		}
	}

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	if after.HeapAlloc > before.HeapAlloc+maxRetainedBytes {
		t.Fatalf(
			"heap grew from %d to %d bytes while framing %d unterminated bytes, want at most +%d",
			before.HeapAlloc, after.HeapAlloc, unterminatedBytes, maxRetainedBytes,
		)
	}

	assertRPCError(
		t,
		peer.receive(t),
		ParseErrorCode,
		fmt.Sprintf("Message exceeds maximum of %d bytes", MaxMessageBytes),
	)

	// The unterminated message ends at its newline; the next line frames normally,
	// and receiving its result proves the oversized bytes cost exactly one error.
	peer.sendRaw(t, "")
	peer.send(t, map[string]any{
		"jsonrpc": "2.0", "id": "after-unterminated", "method": "tools/list",
		"params": map[string]any{},
	})
	message := peer.receive(t)
	assertJSONID(t, message, `"after-unterminated"`)
	if _, exists := message["error"]; exists {
		t.Fatalf("request after an unterminated message error = %s", message["error"])
	}
}

func TestServerRefusesToolCallsBeyondConcurrencyCeiling(t *testing.T) {
	entered := make(chan struct{}, 2*MaxConcurrentCalls)
	release := make(chan struct{})
	peer := newTestPeer(t, newTestServer(t, Tool{
		Name: "block", Description: "Block until released.",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(ctx context.Context, _ Call) (ToolResult, error) {
			entered <- struct{}{}
			select {
			case <-release:
				return ToolResult{Content: []Content{{Type: "text", Text: "released"}}}, nil
			case <-ctx.Done():
				return ToolResult{}, ctx.Err()
			}
		},
	}))
	peer.initialize(t)

	for index := range MaxConcurrentCalls {
		peer.send(t, blockingCall(fmt.Sprintf("in-flight-%d", index)))
	}
	for range MaxConcurrentCalls {
		select {
		case <-entered:
		case <-time.After(testTimeout):
			t.Fatal("in-flight tool call never started")
		}
	}

	peer.send(t, blockingCall("refused"))
	message := peer.receive(t)
	assertJSONID(t, message, `"refused"`)
	assertRPCError(
		t,
		message,
		ServerBusyCode,
		fmt.Sprintf("Server already has %d tool calls in flight", MaxConcurrentCalls),
	)
	select {
	case <-entered:
		t.Fatal("refused tool call ran its handler")
	default:
	}

	close(release)
	for range MaxConcurrentCalls {
		if _, exists := peer.receive(t)["result"]; !exists {
			t.Fatal("in-flight tool call did not return a result")
		}
	}

	peer.send(t, blockingCall("after-release"))
	message = peer.receive(t)
	assertJSONID(t, message, `"after-release"`)
	if _, exists := message["error"]; exists {
		t.Fatalf("tool call after released slots error = %s", message["error"])
	}
}

func TestServerReclaimsHandlerGoroutineWhenContextEnds(t *testing.T) {
	entered := make(chan struct{})
	finished := make(chan struct{})
	server := newTestServer(t, Tool{
		Name: "wait", Description: "Wait for cancellation.",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(ctx context.Context, _ Call) (ToolResult, error) {
			close(entered)
			<-ctx.Done()
			close(finished)
			return ToolResult{}, ctx.Err()
		},
	})
	serverInput, clientInput := io.Pipe()
	clientOutput, serverOutput := io.Pipe()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	baseline := runtime.NumGoroutine()
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx, serverInput, serverOutput)
	}()
	peer := &testPeer{
		input: clientInput, output: json.NewDecoder(clientOutput), cancel: cancel, done: done,
	}
	peer.initialize(t)
	peer.send(t, map[string]any{
		"jsonrpc": "2.0", "id": "waiting", "method": "tools/call",
		"params": map[string]any{"name": "wait", "arguments": map[string]any{}},
	})
	select {
	case <-entered:
	case <-time.After(testTimeout):
		t.Fatal("tool handler never started")
	}

	cancel()
	select {
	case <-finished:
	case <-time.After(testTimeout):
		t.Fatal("tool handler did not observe cancellation")
	}
	// Serve joins the handlers it started, so the connection cannot end while this
	// one still owes the peer a response no reader has taken yet.
	select {
	case err := <-done:
		t.Fatalf("Serve returned %v while a handler was still answering", err)
	default:
	}
	assertJSONID(t, peer.receive(t), `"waiting"`)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(testTimeout):
		t.Fatal("Serve did not return after cancellation")
	}
	assertGoroutinesSettle(t, baseline)
}

func paddedRequest(t *testing.T, id string, total int) string {
	t.Helper()
	const template = `{"jsonrpc":"2.0","id":%q,"method":"tools/list","params":{},"padding":%q}`
	framed := len(fmt.Sprintf(template, id, "")) + 1
	if framed > total {
		t.Fatalf("padded request is at least %d bytes, want %d", framed, total)
	}
	return fmt.Sprintf(template, id, strings.Repeat("p", total-framed))
}

func blockingCall(id string) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0", "id": id, "method": "tools/call",
		"params": map[string]any{"name": "block", "arguments": map[string]any{}},
	}
}

func assertRPCError(
	t *testing.T,
	message map[string]json.RawMessage,
	code int,
	text string,
) {
	t.Helper()
	var rpcErr RPCError
	if err := json.Unmarshal(message["error"], &rpcErr); err != nil {
		t.Fatalf("decode error from %v: %v", message, err)
	}
	if rpcErr.Code != code || rpcErr.Message != text {
		t.Fatalf("error = %+v, want code %d and message %q", rpcErr, code, text)
	}
}

// assertGoroutinesSettle waits for the process goroutine count to fall back to
// baseline. A handler that outlived its context holds a goroutine open and the
// count never settles.
func assertGoroutinesSettle(t *testing.T, baseline int) {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for {
		count := runtime.NumGoroutine()
		if count <= baseline {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutines = %d, want at most the %d running before Serve", count, baseline)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func assertJSONID(t *testing.T, message map[string]json.RawMessage, want string) {
	t.Helper()
	if got := string(message["id"]); got != want {
		t.Fatalf("message id = %s, want %s (message: %v)", got, want, message)
	}
}

func jsonEqual(left, right json.RawMessage) bool {
	var leftValue, rightValue any
	return json.Unmarshal(left, &leftValue) == nil && json.Unmarshal(right, &rightValue) == nil &&
		valuesEqual(leftValue, rightValue)
}

func valuesEqual(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && slices.Equal(leftJSON, rightJSON)
}
