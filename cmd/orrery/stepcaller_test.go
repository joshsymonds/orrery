package main

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// stepCallerID is the id callerSteps answers every StartStep with.
const stepCallerID = "0123456789abcdef0123456789abcdef"

// callerSteps is the step service behind the test session socket. StartStep records the request headers of each call
// and answers with stepCallerID. The other methods are not reached by these tests.
type callerSteps struct {
	enginev1connect.UnimplementedStepsHandler
	mu     sync.Mutex
	header http.Header
}

func (s *callerSteps) StartStep(
	_ context.Context, req *connect.Request[v1.StartStepRequest],
) (*connect.Response[v1.StartStepResponse], error) {
	s.mu.Lock()
	s.header = req.Header().Clone()
	s.mu.Unlock()
	return connect.NewResponse(&v1.StartStepResponse{Id: stepCallerID}), nil
}

// lastHeader returns the request headers of the most recent StartStep call.
func (s *callerSteps) lastHeader() http.Header {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.header
}

// TestStepCallerMCPStepStartSendsTheCallerHeaders runs `orrery --socket S mcp` in a session that orrery started and in
// a session in tmux, calls step_start over MCP, and checks that the step service receives the caller headers of that
// session.
func TestStepCallerMCPStepStartSendsTheCallerHeaders(t *testing.T) {
	t.Parallel()
	const tmuxValue = "/tmp/tmux-1000/orrery-test,4242,0"
	tests := []struct {
		name string
		env  map[string]string
		// want maps each caller header to its value. A header not named here must be absent.
		want map[string]string
	}{
		{
			name: "orrery session",
			env:  map[string]string{protocol.EnvSession: "session-7"},
			want: map[string]string{protocol.HeaderSession: "session-7"},
		},
		{
			name: "tmux and pane",
			env:  map[string]string{"TMUX": tmuxValue, "TMUX_PANE": "%3"},
			want: map[string]string{protocol.HeaderTmux: tmuxValue, protocol.HeaderTmuxPane: "%3"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			steps := &callerSteps{}
			socket := serveSessionSocket(t, func(mux *http.ServeMux) {
				path, handler := enginev1connect.NewStepsHandler(steps)
				mux.Handle(path, handler)
			})
			cmd := startCallerRun(t, envFrom(tc.env), "--socket", socket, "mcp")

			cmd.send(mcpInitialize)
			requireResult(t, cmd.receive(), 1)
			cmd.send(mcpInitialized)
			cmd.send(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"step_start",` +
				`"arguments":{"prompt":"check the change","workspace":"/work"}}}`)
			result := requireResult(t, cmd.receive(), 2)
			if !strings.Contains(string(result), stepCallerID) {
				t.Fatalf("step_start result %s does not name %s", result, stepCallerID)
			}

			if code := cmd.finish(); code != 0 {
				t.Fatalf("exit code = %d, want 0; stderr %q", code, cmd.stderr.String())
			}
			header := steps.lastHeader()
			if header == nil {
				t.Fatal("step service received no StartStep call")
			}
			for _, name := range []string{protocol.HeaderSession, protocol.HeaderTmux, protocol.HeaderTmuxPane} {
				if got := header.Get(name); got != tc.want[name] {
					t.Errorf("%s = %q, want %q", name, got, tc.want[name])
				}
			}
		})
	}
}
