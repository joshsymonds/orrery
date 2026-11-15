package stepmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// textContentType is the Type of a text content block.
const textContentType = "text"

// Call contains one tool invocation.
type Call struct {
	// Arguments is the JSON object supplied by the client.
	Arguments json.RawMessage
}

// Content is one content block returned from a tool call.
type Content struct {
	// Type identifies the MCP content-block variant. The tools return text blocks.
	Type string
	// Text is the block's text.
	Text string
}

// ToolResult is the result of a tool call. A handler error is reported as a result with IsError set, so the model
// can observe and recover from the failure.
type ToolResult struct {
	// Content contains the result blocks presented to the client.
	Content []Content
	// IsError reports whether the tool execution failed.
	IsError bool
}

// ToolHandler executes one tool call. Its context ends when the call's request is canceled or the connection ends,
// so a handler must abandon its work once that context is done.
type ToolHandler func(context.Context, Call) (ToolResult, error)

// Tool declares a tool served by a Server.
type Tool struct {
	// Name is the stable MCP tool name.
	Name string
	// Description tells the model when and how to use the tool.
	Description string
	// InputSchema is the JSON Schema for the tool's arguments.
	InputSchema json.RawMessage
	// Handler runs when the connected client calls the tool.
	Handler ToolHandler
}

// Server serves a fixed set of tools over MCP. It holds no connection state, so one Server may serve several
// connections, each through its own Serve call.
type Server struct {
	tools []Tool
}

// Serve runs one newline-delimited JSON-RPC 2.0 MCP connection on input and output until input ends or ctx is
// canceled. It returns nil at the end of input and on cancellation. A canceled ctx ends the running tool handlers
// and Serve returns only after they have returned.
func (server *Server) Serve(ctx context.Context, input io.Reader, output io.Writer) error {
	select {
	case <-ctx.Done():
		return nil
	default:
	}
	transport := &mcp.IOTransport{Reader: readCloser{input}, Writer: writeCloser{output}}
	session, err := server.sdk(ctx).Connect(ctx, transport, nil)
	if err != nil {
		return fmt.Errorf("mcp: connect: %w", err)
	}
	var endErr error
	ended := make(chan struct{})
	go func() {
		endErr = session.Wait()
		close(ended)
	}()
	select {
	case <-ended:
		if endErr != nil {
			return fmt.Errorf("mcp: serve: %w", endErr)
		}
		return nil
	case <-ctx.Done():
		// Close waits for the running handlers, which end with ctx, then closes the input to stop reading. A failure
		// to close the input is an error. The connection's end after cancellation is not, so endErr is dropped.
		if closeErr := session.Close(); closeErr != nil {
			return fmt.Errorf("mcp: close input: %w", closeErr)
		}
		<-ended
		return nil
	}
}

// sdk returns the SDK server that serves one connection on ctx. Its tool handlers end with ctx.
func (server *Server) sdk(ctx context.Context) *mcp.Server {
	sdk := mcp.NewServer(&mcp.Implementation{Name: "orrery", Version: "1"}, &mcp.ServerOptions{
		Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}},
	})
	for _, tool := range server.tools {
		sdk.AddTool(&mcp.Tool{
			Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema,
		}, toolHandler(ctx, tool.Handler))
	}
	return sdk
}

// toolHandler adapts handler to the SDK's handler for raw arguments, on a connection that ends with connCtx. Absent
// arguments decode as an empty object, and arguments that are not an object are an invalid-params error. A handler
// error becomes a result with IsError set whose one text block is the error's text.
func toolHandler(connCtx context.Context, handler ToolHandler) mcp.ToolHandler {
	return func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		arguments := request.Params.Arguments
		if len(arguments) == 0 {
			arguments = json.RawMessage("{}")
		}
		if !isJSONObject(arguments) {
			return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "Tool arguments must be an object"}
		}
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		stop := context.AfterFunc(connCtx, cancel)
		defer stop()
		result, err := handler(ctx, Call{Arguments: arguments})
		if err != nil {
			result = ToolResult{Content: []Content{{Type: textContentType, Text: err.Error()}}, IsError: true}
		}
		return toCallToolResult(result), nil
	}
}

// isJSONObject reports whether raw is a JSON object.
func isJSONObject(raw json.RawMessage) bool {
	var object map[string]json.RawMessage
	return json.Unmarshal(raw, &object) == nil && object != nil
}

// toCallToolResult returns result as the SDK's tool result, with each block as a text content block.
func toCallToolResult(result ToolResult) *mcp.CallToolResult {
	content := make([]mcp.Content, 0, len(result.Content))
	for _, block := range result.Content {
		content = append(content, &mcp.TextContent{Text: block.Text})
	}
	return &mcp.CallToolResult{Content: content, IsError: result.IsError}
}

// readCloser is the connection's input. Close closes the input when it can be closed, which unblocks a read
// that is waiting for the peer.
type readCloser struct {
	io.Reader
}

// Close closes the input if it is an io.Closer.
func (input readCloser) Close() error {
	if closer, ok := input.Reader.(io.Closer); ok {
		if err := closer.Close(); err != nil {
			return fmt.Errorf("close input: %w", err)
		}
	}
	return nil
}

// writeCloser is the connection's output. Close leaves the output open, because the caller owns it.
type writeCloser struct {
	io.Writer
}

// Close does nothing.
func (writeCloser) Close() error {
	return nil
}
