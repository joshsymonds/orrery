package stepmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
)

// heldDaemon is the fake daemon for the cancel tests. Each call it serves reports its key on entered, then holds until
// release is closed. A call that is released reports its key on finished and returns nil. A call whose context ends
// first reports its key on ended and returns the context's error. It embeds the three clients, and implements only the
// calls the tests hold; no test calls the rest.
type heldDaemon struct {
	enginev1connect.StepsClient
	enginev1connect.EpicsClient
	enginev1connect.EvidenceClient
	entered  chan string
	finished chan string
	ended    chan string
	release  chan struct{}
	// contexts receives the context of each call, just before its key on entered.
	contexts chan context.Context
}

func newHeldDaemon() *heldDaemon {
	return &heldDaemon{
		entered:  make(chan string, 16),
		finished: make(chan string, 16),
		ended:    make(chan string, 16),
		release:  make(chan struct{}),
		contexts: make(chan context.Context, 16),
	}
}

// hold reports key on entered, then returns nil once release is closed, or returns ctx's error once ctx ends first.
func (d *heldDaemon) hold(ctx context.Context, key string) error {
	d.contexts <- ctx
	d.entered <- key
	select {
	case <-d.release:
		d.finished <- key
		return nil
	case <-ctx.Done():
		d.ended <- key
		return ctx.Err()
	}
}

func (d *heldDaemon) StartStep(
	ctx context.Context, _ *connect.Request[v1.StartStepRequest],
) (*connect.Response[v1.StartStepResponse], error) {
	if err := d.hold(ctx, "StartStep"); err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.StartStepResponse{Id: "step-1"}), nil
}

func (d *heldDaemon) StopStep(
	ctx context.Context, _ *connect.Request[v1.StopStepRequest],
) (*connect.Response[v1.StopStepResponse], error) {
	if err := d.hold(ctx, "StopStep"); err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.StopStepResponse{}), nil
}

func (d *heldDaemon) ResumeStep(
	ctx context.Context, _ *connect.Request[v1.ResumeStepRequest],
) (*connect.Response[v1.ResumeStepResponse], error) {
	if err := d.hold(ctx, "ResumeStep"); err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.ResumeStepResponse{}), nil
}

// WaitSteps holds under the step ids it waits on, so a test can tell the waits apart.
func (d *heldDaemon) WaitSteps(
	ctx context.Context, req *connect.Request[v1.WaitStepsRequest],
) (*connect.Response[v1.WaitStepsResponse], error) {
	if err := d.hold(ctx, strings.Join(req.Msg.GetIds(), ",")); err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.WaitStepsResponse{}), nil
}

func (d *heldDaemon) CreateEpic(
	ctx context.Context, _ *connect.Request[v1.CreateEpicRequest],
) (*connect.Response[v1.CreateEpicResponse], error) {
	if err := d.hold(ctx, "CreateEpic"); err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.CreateEpicResponse{Epic: &v1.StoredEpic{}}), nil
}

func (d *heldDaemon) AnswerEpic(
	ctx context.Context, _ *connect.Request[v1.AnswerEpicRequest],
) (*connect.Response[v1.AnswerEpicResponse], error) {
	if err := d.hold(ctx, "AnswerEpic"); err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.AnswerEpicResponse{}), nil
}

func (d *heldDaemon) PauseEpic(
	ctx context.Context, _ *connect.Request[v1.PauseEpicRequest],
) (*connect.Response[v1.PauseEpicResponse], error) {
	if err := d.hold(ctx, "PauseEpic"); err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.PauseEpicResponse{Epic: &v1.StoredEpic{}}), nil
}

func (d *heldDaemon) ResumeEpic(
	ctx context.Context, _ *connect.Request[v1.ResumeEpicRequest],
) (*connect.Response[v1.ResumeEpicResponse], error) {
	if err := d.hold(ctx, "ResumeEpic"); err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.ResumeEpicResponse{}), nil
}

func (d *heldDaemon) StopEpic(
	ctx context.Context, _ *connect.Request[v1.StopEpicRequest],
) (*connect.Response[v1.StopEpicResponse], error) {
	if err := d.hold(ctx, "StopEpic"); err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.StopEpicResponse{Epic: &v1.StoredEpic{}}), nil
}

func (d *heldDaemon) CloseEpic(
	ctx context.Context, _ *connect.Request[v1.CloseEpicRequest],
) (*connect.Response[v1.CloseEpicResponse], error) {
	if err := d.hold(ctx, "CloseEpic"); err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.CloseEpicResponse{Epic: &v1.StoredEpic{}}), nil
}

func (d *heldDaemon) RetryEpic(
	ctx context.Context, _ *connect.Request[v1.RetryEpicRequest],
) (*connect.Response[v1.RetryEpicResponse], error) {
	if err := d.hold(ctx, "RetryEpic"); err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.RetryEpicResponse{Epic: &v1.StoredEpic{}}), nil
}

func (d *heldDaemon) FollowEpics(
	ctx context.Context, _ *connect.Request[v1.FollowEpicsRequest],
) (*connect.Response[v1.FollowEpicsResponse], error) {
	if err := d.hold(ctx, "FollowEpics"); err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.FollowEpicsResponse{}), nil
}

func (d *heldDaemon) ReleaseEpic(
	ctx context.Context, _ *connect.Request[v1.ReleaseEpicRequest],
) (*connect.Response[v1.ReleaseEpicResponse], error) {
	if err := d.hold(ctx, "ReleaseEpic"); err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.ReleaseEpicResponse{Epic: &v1.StoredEpic{}}), nil
}

func (d *heldDaemon) RunEvidence(
	ctx context.Context, _ *connect.Request[v1.RunEvidenceRequest],
) (*connect.Response[v1.RunEvidenceResponse], error) {
	if err := d.hold(ctx, "RunEvidence"); err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.RunEvidenceResponse{Run: &v1.EvidenceRun{}}), nil
}

// clientConn is a go-sdk client connected over pipes to a Serve. Calling cancel ends the serving's context, and closing
// input ends the server's input. served closes once Serve has returned, and serveErr holds Serve's result from then on.
type clientConn struct {
	session  *mcp.ClientSession
	input    *io.PipeWriter
	cancel   context.CancelFunc
	served   chan struct{}
	serveErr error
}

// serveClient serves server over pipes and connects a go-sdk client to it. The test's cleanup cancels the serving,
// closes the pipes, and fails the test unless Serve returns nil. It has no deadline of its own.
func serveClient(t *testing.T, server *Server) *clientConn {
	t.Helper()
	serverInput, clientInput := io.Pipe()
	outputRead, serverOutput := io.Pipe()
	ctx, cancel := context.WithCancel(t.Context())
	conn := &clientConn{input: clientInput, cancel: cancel, served: make(chan struct{})}
	go func() {
		conn.serveErr = server.Serve(ctx, serverInput, serverOutput)
		close(conn.served)
	}()
	t.Cleanup(func() {
		cancel()
		_ = clientInput.Close()
		_ = outputRead.Close()
		<-conn.served
		if conn.serveErr != nil {
			t.Errorf("Serve: %v", conn.serveErr)
		}
	})
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), &mcp.IOTransport{Reader: outputRead, Writer: clientInput}, nil)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	conn.session = session
	return conn
}

// start runs a tools/call of name with arguments, given as JSON object text, through the client in its own goroutine.
// The client stops waiting once ctx is canceled and sends the cancel notification itself, so the returned channel
// reports ctx's error then. Otherwise it reports the call's JSON-RPC error, or nil.
func (c *clientConn) start(ctx context.Context, name, arguments string) <-chan error {
	outcome := make(chan error, 1)
	go func() {
		_, err := c.session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: json.RawMessage(arguments)})
		outcome <- err
	}()
	return outcome
}

// receive returns the next value on ch; its last argument names the event. It has no deadline of its own.
func receive[T any](t *testing.T, ch <-chan T, _ string) T {
	t.Helper()
	return <-ch
}

// TestCancelFreesAWaitSlotForANinthCall checks that with eight step_wait calls held on one connection, a cancel for one
// ends its context and frees its slot, so a ninth call runs instead of being refused with -32000.
func TestCancelFreesAWaitSlotForANinthCall(t *testing.T) {
	t.Parallel()
	daemon := newHeldDaemon()
	server, err := NewServer(daemon, daemon, daemon)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	freed := make(chan struct{}, 16)
	server.slotFreed = func() { freed <- struct{}{} }
	conn := serveClient(t, server)

	const ceiling = 8
	const canceledIndex = 2
	const canceledStep = "step-3"
	canceled, cancelCanceled := context.WithCancel(t.Context())
	defer cancelCanceled()
	for n := range ceiling {
		ctx := t.Context()
		if n == canceledIndex {
			ctx = canceled
		}
		conn.start(ctx, "step_wait", fmt.Sprintf(`{"ids":["step-%d"]}`, n+1))
	}
	for range ceiling {
		receive(t, daemon.entered, "a step_wait call starts")
	}

	cancelCanceled()
	if key := receive(t, daemon.ended, "the canceled step_wait's context ends"); key != canceledStep {
		t.Fatalf("context ended for %s, want %s", key, canceledStep)
	}
	// The handler returns once its context ends, and its slot frees just after, which slotFreed reports.
	receive(t, freed, "the canceled call's slot frees")

	ninth := conn.start(t.Context(), "step_wait", `{"ids":["step-9"]}`)
	select {
	case key := <-daemon.entered:
		if key != "step-9" {
			t.Fatalf("ninth call reached the daemon for %s, want step-9", key)
		}
	case err := <-ninth:
		t.Fatalf("ninth call returned before it reached the daemon: %v", err)
	}
}

// TestCancelLeavesAMutatingCallRunning checks that a cancel for a call to each mutating tool does not reach the call's
// context: the daemon sees no cancellation, and the call's daemon call finishes once it is released.
func TestCancelLeavesAMutatingCallRunning(t *testing.T) {
	t.Parallel()
	const epicID = "demo-0a1b2c3/epic-login"
	cases := []struct {
		tool      string
		arguments string
	}{
		{tool: "step_start", arguments: `{"prompt":"p","workspace":"/work/demo"}`},
		{tool: "step_stop", arguments: `{"id":"step-1"}`},
		{tool: "step_resume", arguments: `{"id":"step-1"}`},
		{tool: "epic_create", arguments: `{"contract":{"id":"` + epicID + `"}}`},
		{tool: "epic_answer", arguments: `{"epicId":"` + epicID + `","id":"D3","text":"Yes."}`},
		{tool: "epic_pause", arguments: `{"id":"` + epicID + `"}`},
		{tool: "epic_resume", arguments: `{"id":"` + epicID + `"}`},
		{tool: "epic_stop", arguments: `{"id":"` + epicID + `"}`},
		{tool: "epic_retry", arguments: `{"id":"` + epicID + `"}`},
		{
			tool: "evidence_run",
			arguments: `{"epic":"` + epicID + `","item":"R1",` +
				`"revision":"0123456789abcdef0123456789abcdef01234567","phase":"green"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			t.Parallel()
			daemon := newHeldDaemon()
			server, err := NewServer(daemon, daemon, daemon)
			if err != nil {
				t.Fatalf("NewServer: %v", err)
			}
			conn := serveClient(t, server)

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			conn.start(ctx, tc.tool, tc.arguments)
			callCtx := receive(t, daemon.contexts, "the call's context reaches the daemon")
			receive(t, daemon.entered, "the call reaches the daemon")
			cancel()
			// The cancel is sent from the client's own goroutine. A ping's reply comes after the cancel on the wire, and
			// the server handles a notification before the next message, so once the ping is answered the cancel has
			// been applied. A context a cancel reaches has ended at once, so the call's context shows it.
			if err = conn.session.Ping(t.Context(), nil); err != nil {
				t.Fatalf("%s: ping after the cancel: %v", tc.tool, err)
			}
			if err := callCtx.Err(); err != nil {
				t.Fatalf("%s: the call's context ended after a cancel: %v", tc.tool, err)
			}

			close(daemon.release)
			receive(t, daemon.finished, "the daemon call finishes once released")
		})
	}
}

// TestConnectionEndEndsADetachedCall checks that a detached mutating call still ends with its connection: when the
// input closes, or when Serve's context is canceled, the call's context ends and Serve returns.
func TestConnectionEndEndsADetachedCall(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		end  func(conn *clientConn)
	}{
		{name: "input closes", end: func(conn *clientConn) { _ = conn.input.Close() }},
		{name: "serve context is canceled", end: func(conn *clientConn) { conn.cancel() }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			daemon := newHeldDaemon()
			server, err := NewServer(daemon, daemon, daemon)
			if err != nil {
				t.Fatalf("NewServer: %v", err)
			}
			conn := serveClient(t, server)

			conn.start(t.Context(), "step_start", `{"prompt":"p","workspace":"/work/demo"}`)
			receive(t, daemon.entered, "the call reaches the daemon")
			tc.end(conn)
			receive(t, daemon.ended, "the connection's end ends the held call's context")
			receive(t, conn.served, "Serve returns once the connection ends")
			if conn.serveErr != nil {
				t.Errorf("Serve: %v", conn.serveErr)
			}
		})
	}
}
