package enginehost

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/joshsymonds/orrery/internal/cli"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/stepmcp"
	"github.com/joshsymonds/orrery/internal/store"
)

// callerlessMCPSession serves the MCP tools of stepmcp over the real Steps of f, through a session whose environment
// names session as its orrery session, and returns the MCP client connected to it. The connection ends at cleanup.
func callerlessMCPSession(t *testing.T, f *stepStartFixture, session string) *mcp.ClientSession {
	t.Helper()
	path, _ := startServerWithSteps(t, nil, f.steps)
	getenv := func(name string) string {
		if name == protocol.EnvSession {
			return session
		}
		return ""
	}
	steps := enginev1connect.NewStepsClient(cli.SessionHTTPClient(path), cli.SessionBaseURL,
		connect.WithInterceptors(stepmcp.CallerInterceptor(getenv)))
	server, err := stepmcp.NewServer(steps, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

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

// callerlessStart calls step_start over session to start a read-only step of the workspace, with a schema the start
// accepts, and returns the tool result.
func callerlessStart(t *testing.T, session *mcp.ClientSession, workspace string) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "step_start",
		Arguments: map[string]any{
			"prompt":    "look",
			"workspace": workspace,
			"readOnly":  true,
			"schema":    json.RawMessage(stepLoopSchema),
		},
	})
	if err != nil {
		t.Fatalf("CallTool step_start: %v", err)
	}
	return result
}

// callerlessText returns the text of the single text block of result.
func callerlessText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if len(result.Content) != 1 {
		t.Fatalf("step_start content = %+v, want one block", result.Content)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("step_start content = %T, want a text block", result.Content[0])
	}
	return text.Text
}

// TestCallerlessStartMCPUnknownSessionRefused calls step_start over MCP from a session that names a sessions row which
// does not exist. The real Steps refuses the start with the reason it is refused for, so the tool errors with that
// reason, and the start stores no step row and starts no step session.
func TestCallerlessStartMCPUnknownSessionRefused(t *testing.T) {
	t.Parallel()
	f := newStepStartFixture(t)
	session := callerlessMCPSession(t, f, "session-9")

	result := callerlessStart(t, session, stepGitTestRepo(t))

	if !result.IsError {
		t.Fatalf("step_start = %q, want a tool error for a caller with no row", callerlessText(t, result))
	}
	if text := callerlessText(t, result); !strings.Contains(text, "nowhere to go") {
		t.Fatalf("step_start error = %q, want it to name the reason, nowhere to go", text)
	}
	rows, err := f.store.OpenSteps(t.Context())
	if err != nil {
		t.Fatalf("OpenSteps: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("refused step_start stored %d step rows, want none", len(rows))
	}
	if n := f.sessions.startCalls(); n != 0 {
		t.Fatalf("refused step_start started %d step sessions, want none", n)
	}
}

// TestCallerlessStartMCPResolvingCallerStarts calls step_start over MCP from a session that names a sessions row which
// exists. The real Steps starts the step, which returns its id, and the stored row names that row as its caller.
func TestCallerlessStartMCPResolvingCallerStarts(t *testing.T) {
	t.Parallel()
	f := newStepStartFixture(t)
	engine := store.EngineAdd{ID: "session-7", Dir: "/work", State: "idle"}
	if err := f.store.ApplyEngine(t.Context(), []store.EngineOp{engine}, stepCallerAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
	session := callerlessMCPSession(t, f, "session-7")

	result := callerlessStart(t, session, stepGitTestRepo(t))

	if result.IsError {
		t.Fatalf("step_start error = %q, want a started step", callerlessText(t, result))
	}
	var started map[string]string
	if err := json.Unmarshal([]byte(callerlessText(t, result)), &started); err != nil {
		t.Fatalf("decode step_start result: %v", err)
	}
	id := started["id"]
	if id == "" {
		t.Fatalf("step_start result = %v, want a step id", started)
	}
	row, err := f.store.Step(t.Context(), id)
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	if row.Caller != "session-7" {
		t.Fatalf("stored caller = %q, want %q", row.Caller, "session-7")
	}
	if n := f.sessions.startCalls(); n != 1 {
		t.Fatalf("step sessions started = %d, want 1", n)
	}
}
