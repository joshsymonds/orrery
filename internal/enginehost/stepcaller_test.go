package enginehost

import (
	"maps"
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// stepCallerAt is the time the rows the step caller tests store are written at.
func stepCallerAt() time.Time { return time.Unix(1700003000, 0) }

// requireRefusedStart asserts that StartStep refuses req as a failed precondition that names the reason, and that the
// refusal stores no step row and starts no step session.
func requireRefusedStart(
	t *testing.T, f *stepStartFixture, client enginev1connect.StepsClient, req *connect.Request[v1.StartStepRequest],
) {
	t.Helper()
	_, err := client.StartStep(t.Context(), req)
	if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
		t.Fatalf("StartStep code = %v, want %v (error %v)", got, connect.CodeFailedPrecondition, err)
	}
	if !strings.Contains(err.Error(), "nowhere to go") {
		t.Fatalf("StartStep error = %v, want it to name the reason, nowhere to go", err)
	}
	open, err := f.store.OpenSteps(t.Context())
	if err != nil {
		t.Fatalf("OpenSteps: %v", err)
	}
	if len(open) != 0 {
		t.Fatalf("refused start stored %d step rows, want none", len(open))
	}
	if n := f.sessions.startCalls(); n != 0 {
		t.Fatalf("refused start started %d step sessions, want none", n)
	}
}

// TestCallerlessStartStepRecordsTheRowOfItsCallerOrIsRefused starts a step over the Steps service with the caller
// headers of each case. A caller that resolves to a sessions row starts the step, and the stored row and StepStatus both
// name that row as the caller. A caller that resolves to no row, and a call with no caller headers, is refused with
// ErrNoCaller as a failed precondition, and stores and starts nothing.
func TestCallerlessStartStepRecordsTheRowOfItsCallerOrIsRefused(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// caller stores the rows the case needs and returns the caller headers of the StartStep call, and the id of the
		// row the started step must name as its caller, "" when none.
		caller func(t *testing.T, f *stepStartFixture) (http.Header, string)
		// refused is true when the case's start must be refused and start nothing.
		refused bool
	}{
		{
			name: "orrery session names its own row",
			caller: func(t *testing.T, f *stepStartFixture) (http.Header, string) {
				t.Helper()
				engine := store.EngineAdd{ID: "session-7", Dir: "/work", State: "idle"}
				if err := f.store.ApplyEngine(t.Context(), []store.EngineOp{engine}, stepCallerAt()); err != nil {
					t.Fatalf("ApplyEngine: %v", err)
				}
				return http.Header{protocol.HeaderSession: {"session-7"}}, "session-7"
			},
		},
		{
			name: "tmux pane names its pane row",
			caller: func(t *testing.T, f *stepStartFixture) (http.Header, string) {
				t.Helper()
				pane := store.Pane{ID: "orrery-1", Pane: "%7"}
				if err := f.store.SyncPane(t.Context(), "-S /tmp/tmux-1000/orrery", pane, stepCallerAt()); err != nil {
					t.Fatalf("SyncPane: %v", err)
				}
				return http.Header{
					protocol.HeaderTmux:     {"/tmp/tmux-1000/orrery,4242,0"},
					protocol.HeaderTmuxPane: {"%7"},
				}, "orrery-1"
			},
		},
		{
			name: "step that is itself the caller",
			caller: func(t *testing.T, f *stepStartFixture) (http.Header, string) {
				t.Helper()
				id, err := f.steps.Start(t.Context(), stepStartReadOnly(stepGitTestRepo(t), ""))
				if err != nil {
					t.Fatalf("Start caller step: %v", err)
				}
				return http.Header{protocol.HeaderSession: {id}}, id
			},
		},
		{
			name: "session with no row is refused",
			caller: func(_ *testing.T, _ *stepStartFixture) (http.Header, string) {
				return http.Header{protocol.HeaderSession: {"session-9"}}, ""
			},
			refused: true,
		},
		{
			name: "no caller headers is refused",
			caller: func(_ *testing.T, _ *stepStartFixture) (http.Header, string) {
				return nil, ""
			},
			refused: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newStepStartFixture(t)
			headers, want := tc.caller(t, f)
			path, _ := startServerWithSteps(t, nil, f.steps)
			client := newStepClient(t, path)

			req := connect.NewRequest(&v1.StartStepRequest{
				Agent:     "reviewer",
				Prompt:    "look",
				Schema:    stepLoopSchema,
				Workspace: stepGitTestRepo(t),
				ReadOnly:  true,
			})
			maps.Copy(req.Header(), headers)
			if tc.refused {
				requireRefusedStart(t, f, client, req)
				return
			}
			started, err := client.StartStep(t.Context(), req)
			if err != nil {
				t.Fatalf("StartStep: %v", err)
			}
			id := started.Msg.GetId()

			row, err := f.store.Step(t.Context(), id)
			if err != nil {
				t.Fatalf("Step: %v", err)
			}
			if row.Caller != want {
				t.Fatalf("stored caller = %q, want %q", row.Caller, want)
			}
			status, err := client.StepStatus(t.Context(), connect.NewRequest(&v1.StepStatusRequest{Id: id}))
			if err != nil {
				t.Fatalf("StepStatus: %v", err)
			}
			if got := status.Msg.GetStep().GetCaller(); got != want {
				t.Fatalf("StepStatus caller = %q, want %q", got, want)
			}
		})
	}
}
