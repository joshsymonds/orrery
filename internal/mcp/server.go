// Package mcp provides a small, reusable Model Context Protocol server.
//
// The package uses only the Go standard library. Each Server is immutable after
// construction and Serve keeps all connection state local, so one server may serve
// multiple independent connections.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

const (
	jsonRPCVersion = "2.0"

	// ProtocolVersion is the MCP revision implemented by this package.
	ProtocolVersion = "2025-11-25"

	// ParseErrorCode is the JSON-RPC code for malformed JSON.
	ParseErrorCode = -32700
	// InvalidRequestCode is the JSON-RPC code for an invalid request envelope.
	InvalidRequestCode = -32600
	// MethodNotFoundCode is the JSON-RPC code for an unknown method.
	MethodNotFoundCode = -32601
	// InvalidParamsCode is the JSON-RPC code for invalid method parameters.
	InvalidParamsCode = -32602
	// InternalErrorCode is the JSON-RPC code for an unexpected server failure.
	InternalErrorCode = -32603
	// ServerBusyCode is the implementation-defined JSON-RPC server error reported
	// when a tool call arrives with MaxConcurrentCalls handlers already running.
	ServerBusyCode = -32000

	// MaxMessageBytes is the largest single message the transport reads from a peer,
	// counting the newline that terminates it. The peer owns both the pipe and, for a
	// server spawned by the Claude CLI, the model output crossing it, so the framing
	// needs a ceiling of its own: without one a peer that never sends a newline grows
	// the read buffer until the process dies. One mebibyte sits an order of magnitude
	// above the largest payload any tool accepts, leaving the JSON-RPC envelope and
	// JSON string escaping room to spare.
	MaxMessageBytes = 1 << 20

	// MaxConcurrentCalls is the number of tool handlers one connection runs at once.
	// The ceiling is generous for honest traffic while denying a peer the ability to
	// multiply handler goroutines. Calls beyond it are refused rather than queued, so
	// the loop that reads messages never blocks waiting for a free slot.
	MaxConcurrentCalls = 8
)

// errMessageTooLarge reports a peer message longer than MaxMessageBytes. The
// framing drops the offending message and keeps reading, so the connection
// answers it with a protocol error instead of ending.
var errMessageTooLarge = errors.New("mcp: message exceeds maximum size")

// ToolHandler executes one MCP tool call. Its context ends with the connection, and
// Serve does not return until every handler it started has returned, so a handler
// must abandon its work once that context is done.
type ToolHandler func(context.Context, Call) (ToolResult, error)

// Tool declares a tool exposed by a Server.
type Tool struct {
	// Name is the stable MCP tool name.
	Name string `json:"name"`
	// Description tells the model when and how to use the tool.
	Description string `json:"description"`
	// InputSchema is the JSON Schema for the tool's arguments.
	InputSchema json.RawMessage `json:"inputSchema"`
	// Handler runs when the connected client calls the tool.
	Handler ToolHandler `json:"-"`
}

// Content is one content block returned from a tool call.
type Content struct {
	// Type identifies the MCP content-block variant, such as "text" or "image".
	Type string `json:"type"`
	// Text contains text content when Type is "text".
	Text string `json:"text,omitempty"`
	// Data contains base64-encoded data for binary content variants.
	Data string `json:"data,omitempty"`
	// MIMEType identifies the media type of Data.
	MIMEType string `json:"mimeType,omitempty"`
}

// ToolResult is the MCP result of a tool call. Handler errors are mapped to a text
// result with IsError set, allowing the model to observe and recover from tool failures.
type ToolResult struct {
	// Content contains the result blocks presented to the client.
	Content []Content `json:"content"`
	// IsError reports whether the tool execution failed.
	IsError bool `json:"isError,omitempty"`
}

// Call contains one tool invocation.
type Call struct {
	// Arguments is the JSON object supplied by the client.
	Arguments json.RawMessage
}

// RPCError is a JSON-RPC error returned by an MCP peer.
type RPCError struct {
	// Code is the JSON-RPC numeric error code.
	Code int `json:"code"`
	// Message is the short error description.
	Message string `json:"message"`
	// Data carries optional peer-specific error details.
	Data json.RawMessage `json:"data,omitempty"`
}

// Error implements error.
func (rpcError RPCError) Error() string {
	if len(rpcError.Data) == 0 {
		return fmt.Sprintf("MCP JSON-RPC error %d: %s", rpcError.Code, rpcError.Message)
	}
	return fmt.Sprintf(
		"MCP JSON-RPC error %d: %s (%s)",
		rpcError.Code,
		rpcError.Message,
		rpcError.Data,
	)
}

// Server is an immutable collection of declaratively registered MCP tools.
type Server struct {
	name    string
	version string
	tools   []Tool
	byName  map[string]Tool
}

// NewServer validates and constructs a reusable MCP server. Tool names must be
// unique, handlers must be non-nil, and each input schema must be a JSON object
// whose top-level type is "object".
func NewServer(name, version string, tools ...Tool) (*Server, error) {
	if name == "" {
		return nil, errors.New("mcp: server name is required")
	}
	if version == "" {
		return nil, errors.New("mcp: server version is required")
	}
	server := &Server{
		name: name, version: version,
		tools: make([]Tool, 0, len(tools)), byName: make(map[string]Tool, len(tools)),
	}
	for index, tool := range tools {
		if tool.Name == "" {
			return nil, fmt.Errorf("mcp: tool %d has no name", index)
		}
		if tool.Handler == nil {
			return nil, fmt.Errorf("mcp: tool %q has no handler", tool.Name)
		}
		if _, exists := server.byName[tool.Name]; exists {
			return nil, fmt.Errorf("mcp: duplicate tool name %q", tool.Name)
		}
		if err := validateToolInputSchema(tool.InputSchema); err != nil {
			return nil, fmt.Errorf("mcp: tool %q input schema: %w", tool.Name, err)
		}
		tool.InputSchema = bytes.Clone(tool.InputSchema)
		server.tools = append(server.tools, tool)
		server.byName[tool.Name] = tool
	}
	return server, nil
}

// Serve runs one newline-delimited JSON-RPC 2.0 MCP connection until input closes or
// ctx is canceled. It returns nil for EOF and intentional context cancellation. A
// message longer than MaxMessageBytes is refused as a parse error without ending the
// connection, and at most MaxConcurrentCalls tool handlers run at once.
func (server *Server) Serve(ctx context.Context, input io.Reader, output io.Writer) error {
	if err := ctx.Err(); err != nil {
		return nil
	}
	connectionCtx, cancel := context.WithCancel(ctx)
	peer := &connection{
		cancel: cancel, server: server,
		encoder:   json.NewEncoder(output),
		callSlots: make(chan struct{}, MaxConcurrentCalls),
	}
	defer cancel()

	reads := make(chan readResult)
	go readMessages(connectionCtx, input, reads)
	for {
		select {
		case <-connectionCtx.Done():
			if closer, ok := input.(io.Closer); ok {
				_ = closer.Close()
			}
			peer.handlers.Wait()
			return peer.connectionError()
		case read := <-reads:
			switch {
			case errors.Is(read.err, errMessageTooLarge):
				peer.rejectOversizedMessage()
			case read.err != nil:
				cancel()
				peer.handlers.Wait()
				if errors.Is(read.err, io.EOF) {
					return peer.connectionError()
				}
				return errors.Join(read.err, peer.connectionError())
			default:
				peer.handleLine(connectionCtx, read.message)
			}
		}
	}
}

type readResult struct {
	message []byte
	err     error
}

func readMessages(ctx context.Context, input io.Reader, output chan<- readResult) {
	frames := &messageReader{reader: bufio.NewReader(input)}
	for {
		message, err := frames.read()
		message = bytes.TrimSpace(message)
		if len(message) != 0 && !sendReadResult(ctx, output, readResult{message: message}) {
			return
		}
		if err == nil {
			continue
		}
		if !sendReadResult(ctx, output, readResult{err: err}) {
			return
		}
		// An oversized message is one message's protocol error, not the end of the
		// connection: its bytes are gone and the next message still frames.
		if !errors.Is(err, errMessageTooLarge) {
			return
		}
	}
}

// messageReader frames newline-delimited peer messages, bounding each one at
// MaxMessageBytes. It never buffers more than one bounded message, so a peer that
// withholds a newline cannot grow the connection's memory.
type messageReader struct {
	reader *bufio.Reader
	// dropping records an oversized message reported before its remaining bytes
	// arrived. They are discarded before the next message is framed.
	dropping bool
}

// read returns the next message. It reports errMessageTooLarge as soon as a message
// passes MaxMessageBytes, without waiting for the newline that would end it.
func (frames *messageReader) read() ([]byte, error) {
	if frames.dropping {
		if err := frames.dropMessage(); err != nil {
			return nil, err
		}
	}
	var message []byte
	for {
		chunk, err := frames.reader.ReadSlice('\n')
		if len(message)+len(chunk) > MaxMessageBytes {
			// A chunk that found the newline completed the oversized message, so
			// only a buffer-full chunk leaves bytes behind to drop.
			frames.dropping = errors.Is(err, bufio.ErrBufferFull)
			return nil, errMessageTooLarge
		}
		message = append(message, chunk...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil {
			return message, fmt.Errorf("mcp: read MCP input: %w", err)
		}
		return message, nil
	}
}

// dropMessage discards what is left of an oversized message, leaving the reader on
// the first byte of the next one.
func (frames *messageReader) dropMessage() error {
	for {
		_, err := frames.reader.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		frames.dropping = false
		if err != nil {
			return fmt.Errorf("mcp: read MCP input: %w", err)
		}
		return nil
	}
}

func sendReadResult(ctx context.Context, output chan<- readResult, result readResult) bool {
	select {
	case output <- result:
		return true
	case <-ctx.Done():
		return false
	}
}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

type rpcOutput struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

type connectionState uint8

const (
	connectionUninitialized connectionState = iota
	connectionInitializing
	connectionReady
)

type connection struct {
	cancel  context.CancelFunc
	server  *Server
	encoder *json.Encoder

	writeMu  sync.Mutex
	writeErr error
	handlers sync.WaitGroup
	// callSlots holds one token per running tool handler, capping them at
	// MaxConcurrentCalls.
	callSlots chan struct{}

	stateMu sync.RWMutex
	state   connectionState
}

func (connection *connection) handleLine(ctx context.Context, line []byte) {
	if !json.Valid(line) {
		connection.writeError(json.RawMessage("null"), ParseErrorCode, "Parse error")
		return
	}
	var message rpcMessage
	if err := json.Unmarshal(line, &message); err != nil {
		connection.writeError(json.RawMessage("null"), InvalidRequestCode, "Invalid Request")
		return
	}
	if message.JSONRPC != jsonRPCVersion {
		connection.writeError(validErrorID(message.ID), InvalidRequestCode, "Invalid Request")
		return
	}
	if message.Method != "" {
		if len(message.ID) != 0 && !validRequestID(message.ID) {
			connection.writeError(json.RawMessage("null"), InvalidRequestCode, "Invalid Request")
			return
		}
		connection.handleRequest(ctx, message)
		return
	}
	// A message without a method is a response to a request this server never
	// sends, so it is consumed without a reply. Only a line that is not a response
	// at all, with no id or with neither result nor error, is an invalid request.
	if len(message.ID) == 0 || (len(message.Result) == 0 && message.Error == nil) {
		connection.writeError(json.RawMessage("null"), InvalidRequestCode, "Invalid Request")
	}
}

func validRequestID(id json.RawMessage) bool {
	var value any
	if json.Unmarshal(id, &value) != nil {
		return false
	}
	switch value.(type) {
	case string, float64:
		return true
	default:
		return false
	}
}

func validErrorID(id json.RawMessage) json.RawMessage {
	if validRequestID(id) {
		return id
	}
	return json.RawMessage("null")
}

func (connection *connection) handleRequest(ctx context.Context, message rpcMessage) {
	switch message.Method {
	case "initialize":
		connection.handleInitialize(message)
	case "notifications/initialized":
		connection.markInitialized()
	case "tools/list":
		if connection.requireReady(message.ID) {
			connection.handleToolsList(message.ID)
		}
	case "tools/call":
		if connection.requireReady(message.ID) {
			connection.handleToolsCall(ctx, message)
		}
	default:
		if len(message.ID) != 0 {
			connection.writeError(message.ID, MethodNotFoundCode, "Method not found")
		}
	}
}

func (connection *connection) handleInitialize(message rpcMessage) {
	if len(message.ID) == 0 {
		return
	}
	var params struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(message.Params, &params); err != nil || params.ProtocolVersion == "" {
		connection.writeError(message.ID, InvalidParamsCode, "Invalid initialize parameters")
		return
	}
	connection.stateMu.Lock()
	if connection.state != connectionUninitialized {
		connection.stateMu.Unlock()
		connection.writeError(message.ID, InvalidRequestCode, "Connection already initialized")
		return
	}
	connection.state = connectionInitializing
	connection.stateMu.Unlock()

	connection.write(rpcOutput{
		JSONRPC: jsonRPCVersion, ID: message.ID,
		Result: map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities": map[string]any{
				"tools": map[string]any{"listChanged": false},
			},
			"serverInfo": map[string]string{
				"name": connection.server.name, "version": connection.server.version,
			},
		},
	})
}

func (connection *connection) markInitialized() {
	connection.stateMu.Lock()
	if connection.state == connectionInitializing {
		connection.state = connectionReady
	}
	connection.stateMu.Unlock()
}

func (connection *connection) requireReady(id json.RawMessage) bool {
	connection.stateMu.RLock()
	ready := connection.state == connectionReady
	connection.stateMu.RUnlock()
	if !ready && len(id) != 0 {
		connection.writeError(id, InvalidRequestCode, "Connection is not initialized")
	}
	return ready
}

func (connection *connection) handleToolsList(id json.RawMessage) {
	tools := make([]Tool, len(connection.server.tools))
	copy(tools, connection.server.tools)
	connection.write(rpcOutput{
		JSONRPC: jsonRPCVersion, ID: id,
		Result: map[string]any{"tools": tools},
	})
}

func (connection *connection) handleToolsCall(ctx context.Context, message rpcMessage) {
	if len(message.ID) == 0 {
		return
	}
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(message.Params, &params); err != nil || params.Name == "" {
		connection.writeError(message.ID, InvalidParamsCode, "Invalid tool call parameters")
		return
	}
	if len(params.Arguments) == 0 {
		params.Arguments = json.RawMessage("{}")
	}
	if err := validateJSONObject(params.Arguments); err != nil {
		connection.writeError(message.ID, InvalidParamsCode, "Tool arguments must be an object")
		return
	}
	tool, exists := connection.server.byName[params.Name]
	if !exists {
		connection.writeError(message.ID, InvalidParamsCode, "Unknown tool")
		return
	}
	if !connection.acquireCall() {
		connection.writeError(message.ID, ServerBusyCode, fmt.Sprintf(
			"Server already has %d tool calls in flight",
			MaxConcurrentCalls,
		))
		return
	}
	arguments := bytes.Clone(params.Arguments)
	id := bytes.Clone(message.ID)
	connection.handlers.Add(1)
	go func() {
		defer connection.handlers.Done()
		defer connection.releaseCall()
		result, err := tool.Handler(ctx, Call{Arguments: arguments})
		if err != nil {
			result = ToolResult{
				Content: []Content{{Type: "text", Text: err.Error()}}, IsError: true,
			}
		}
		if result.Content == nil {
			result.Content = []Content{}
		}
		connection.write(rpcOutput{JSONRPC: jsonRPCVersion, ID: id, Result: result})
	}()
}

// acquireCall reserves one of the connection's MaxConcurrentCalls handler slots,
// reporting false when every slot is already taken.
func (connection *connection) acquireCall() bool {
	select {
	case connection.callSlots <- struct{}{}:
		return true
	default:
		return false
	}
}

// releaseCall returns a slot reserved by acquireCall.
func (connection *connection) releaseCall() {
	<-connection.callSlots
}

// rejectOversizedMessage answers a message the framing refused to buffer. Those
// bytes never reached the JSON parser and carry no id the peer could be held to, so
// the reply is the parse error any unreadable input earns.
func (connection *connection) rejectOversizedMessage() {
	connection.writeError(json.RawMessage("null"), ParseErrorCode, fmt.Sprintf(
		"Message exceeds maximum of %d bytes",
		MaxMessageBytes,
	))
}

func (connection *connection) writeError(id json.RawMessage, code int, message string) {
	connection.write(rpcOutput{
		JSONRPC: jsonRPCVersion, ID: id, Error: &RPCError{Code: code, Message: message},
	})
}

func (connection *connection) write(message rpcOutput) {
	connection.writeMu.Lock()
	defer connection.writeMu.Unlock()
	if connection.writeErr != nil {
		return
	}
	if err := connection.encoder.Encode(message); err != nil {
		connection.writeErr = fmt.Errorf("mcp: write MCP output: %w", err)
		connection.cancel()
	}
}

func (connection *connection) connectionError() error {
	connection.writeMu.Lock()
	defer connection.writeMu.Unlock()
	return connection.writeErr
}

func validateJSONObject(raw json.RawMessage) error {
	_, err := decodeJSONObject(raw)
	return err
}

// validateToolInputSchema enforces what real MCP clients need from a registered
// tool: a schema whose own top-level type is "object", because that is what a
// client reads to build the argument form. A schema that only declares object
// shapes further down — under oneOf, say — leaves the client with nothing to
// render, so the tool must not be registrable.
func validateToolInputSchema(raw json.RawMessage) error {
	schema, err := decodeJSONObject(raw)
	if err != nil {
		return err
	}
	if schema["type"] != "object" {
		return errors.New(`top-level type must be "object"`)
	}
	return nil
}

func decodeJSONObject(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 || !json.Valid(raw) {
		return nil, errors.New("must be valid JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode JSON: %w", err)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("must be a JSON object")
	}
	return object, nil
}
