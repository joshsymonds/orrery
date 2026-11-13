package stepmcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/cli"
	"github.com/joshsymonds/orrery/internal/enginehost"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
)

// waitBeforeCut is how long a step_wait runs before its stream is cut.
const waitBeforeCut = 4 * time.Second

// maxRetryWait is the longest wait rpcretry.Do makes between two attempts.
const maxRetryWait = 5 * time.Second

// testClock is the retry clock of a test. Its time moves only when a retry waits or the test advances it, so a retry
// costs no real time. onWait, when set, runs with the number of waits so far after each wait is recorded.
type testClock struct {
	mu     sync.Mutex
	now    time.Time
	waits  int
	onWait func(waits int)
}

// newTestClock returns a clock at a fixed time.
func newTestClock() *testClock {
	return &testClock{now: time.Unix(1700000000, 0)}
}

// Now returns the clock's time.
func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// After records a wait of d, advances the clock by it, and returns a channel that has already fired.
func (c *testClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.waits++
	waits, now, onWait := c.waits, c.now, c.onWait
	c.mu.Unlock()
	if onWait != nil {
		onWait(waits)
	}
	fired := make(chan time.Time, 1)
	fired <- now
	return fired
}

// advance moves the clock forward by d, as the time a simulated call spends.
func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// waitCount returns how many waits the clock has recorded.
func (c *testClock) waitCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.waits
}

// retryWait is one WaitSteps request the service answered: its timeout in seconds and the clock's time on arrival.
type retryWait struct {
	seconds uint32
	at      time.Time
}

// retrySteps is the Steps service of the retry tests. It counts the calls each method receives, fails the calls that
// unavailable names with Unavailable, and records every WaitSteps request.
type retrySteps struct {
	enginev1connect.UnimplementedStepsHandler

	clock       *testClock
	unavailable func(method string, call int) bool

	mu    sync.Mutex
	calls map[string]int
	waits []retryWait
}

// newRetrySteps returns the Steps service of a retry test on clock. unavailable, when set, decides which calls fail
// with Unavailable.
func newRetrySteps(clock *testClock, unavailable func(method string, call int) bool) *retrySteps {
	return &retrySteps{clock: clock, unavailable: unavailable, calls: map[string]int{}}
}

// reach counts a call to method and returns the Unavailable error the test asked for, or nil.
func (s *retrySteps) reach(method string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls[method]++
	if s.unavailable != nil && s.unavailable(method, s.calls[method]) {
		return connect.NewError(connect.CodeUnavailable, errors.New("orreryd is restarting"))
	}
	return nil
}

// count returns how many calls method has received.
func (s *retrySteps) count(method string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[method]
}

// recordedWaits returns every WaitSteps request the service received.
func (s *retrySteps) recordedWaits() []retryWait {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.waits)
}

func (s *retrySteps) StartStep(
	context.Context,
	*connect.Request[v1.StartStepRequest],
) (*connect.Response[v1.StartStepResponse], error) {
	if err := s.reach("StartStep"); err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.StartStepResponse{Id: "s1"}), nil
}

func (s *retrySteps) StopStep(
	context.Context,
	*connect.Request[v1.StopStepRequest],
) (*connect.Response[v1.StopStepResponse], error) {
	if err := s.reach("StopStep"); err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.StopStepResponse{}), nil
}

func (s *retrySteps) StepStatus(
	_ context.Context,
	req *connect.Request[v1.StepStatusRequest],
) (*connect.Response[v1.StepStatusResponse], error) {
	if err := s.reach("StepStatus"); err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.StepStatusResponse{Step: &v1.Step{Id: req.Msg.GetId(), Status: "running"}}), nil
}

func (s *retrySteps) WaitSteps(
	_ context.Context,
	req *connect.Request[v1.WaitStepsRequest],
) (*connect.Response[v1.WaitStepsResponse], error) {
	s.mu.Lock()
	s.waits = append(s.waits, retryWait{seconds: req.Msg.GetTimeoutSeconds(), at: s.clock.Now()})
	s.mu.Unlock()
	if err := s.reach("WaitSteps"); err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.WaitStepsResponse{Ended: &v1.Step{Id: "s1", Status: "pass", End: "done"}}), nil
}

// dropper is the HTTP layer of a retry test. It reads the first drops requests to procedure and then closes each
// connection without answering, so the client sees the stream cut mid-call. Every other request reaches next.
type dropper struct {
	t         *testing.T
	procedure string
	drops     int
	onDrop    func()
	next      http.Handler

	mu       sync.Mutex
	requests int
}

func (d *dropper) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != d.procedure {
		d.next.ServeHTTP(w, r)
		return
	}
	d.mu.Lock()
	d.requests++
	drop := d.requests <= d.drops
	d.mu.Unlock()
	if !drop {
		d.next.ServeHTTP(w, r)
		return
	}
	if _, err := io.Copy(io.Discard, r.Body); err != nil {
		d.t.Errorf("read %s request: %v", d.procedure, err)
		return
	}
	if d.onDrop != nil {
		d.onDrop()
	}
	conn, _, err := http.NewResponseController(w).Hijack()
	if err != nil {
		d.t.Errorf("hijack %s: %v", d.procedure, err)
		return
	}
	_ = conn.Close()
}

// count returns how many requests to the dropper's procedure it has read.
func (d *dropper) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.requests
}

// stepsHandler returns the HTTP handler that answers service's Steps RPCs.
func stepsHandler(service enginev1connect.StepsHandler) http.Handler {
	_, handler := enginev1connect.NewStepsHandler(service)
	return handler
}

// serveSteps listens on the socket at path and serves handler there until the test ends. A socket file that a dead
// daemon left behind is replaced, as orreryd replaces it.
func serveSteps(t *testing.T, path string, handler http.Handler) {
	t.Helper()
	ln, err := enginehost.Listen(path)
	if err != nil {
		t.Errorf("listen %s: %v", path, err)
		return
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: testTimeout}
	go func() { _ = server.Serve(ln) }()
	t.Cleanup(func() { _ = server.Close() })
}

// refusedSocket returns the path of a socket file that nothing listens on, so a dial to it is refused.
func refusedSocket(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sessions.sock")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ln.SetUnlinkOnClose(false)
	if err = ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	return path
}

// retryClient returns the Steps client the session uses, dialing the socket at path.
func retryClient(path string) enginev1connect.StepsClient {
	return enginev1connect.NewStepsClient(cli.SessionHTTPClient(path), cli.SessionBaseURL)
}

// startRetryPeer serves handler on a new socket and returns an MCP peer whose step tools retry on clock.
func startRetryPeer(t *testing.T, handler http.Handler, clock *testClock) *peer {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sessions.sock")
	serveSteps(t, path, handler)
	return newRetryPeer(t, retryClient(path), clock)
}

// newRetryPeer serves an MCP server built over steps with clock as its retry clock, and completes the handshake.
func newRetryPeer(t *testing.T, steps enginev1connect.StepsClient, clock *testClock) *peer {
	t.Helper()
	server, err := newServer(steps, clock)
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}
	serverInput, clientInput := io.Pipe()
	outputRead, serverOutput := io.Pipe()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, serverInput, serverOutput) }()
	t.Cleanup(func() {
		cancel()
		_ = clientInput.Close()
		_ = outputRead.Close()
		if err := <-done; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})
	p := &peer{
		t: t, input: clientInput, outputRead: outputRead,
		output: json.NewDecoder(bufio.NewReader(outputRead)),
	}
	p.call("initialize", map[string]any{
		"protocolVersion": "2025-11-25",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]string{"name": "test", "version": "1"},
	})
	p.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	return p
}

// A start cut mid-call is a tool error, and the start is not repeated.
func TestStepStartCutMidCallIsToolErrorAndNeverRetried(t *testing.T) {
	clock := newTestClock()
	service := newRetrySteps(clock, nil)
	handler := &dropper{t: t, procedure: enginev1connect.StepsStartStepProcedure, drops: 1, next: stepsHandler(service)}
	p := startRetryPeer(t, handler, clock)

	outcome := p.tool("step_start", `{"prompt":"check","workspace":"/work"}`)
	if !outcome.isError {
		t.Fatalf("step_start = %q, want a tool error for a start cut mid-call", outcome.text)
	}
	if got := handler.count(); got != 1 {
		t.Errorf("StartStep requests = %d, want 1: a start cut mid-call is never retried", got)
	}
	if got := clock.waitCount(); got != 0 {
		t.Errorf("step_start waited %d times, want 0", got)
	}
}

// A start whose socket is refused until the second retry starts the step once, when the socket appears.
func TestStepStartStartsOnceWhenTheSocketAppearsAfterRefusedDials(t *testing.T) {
	clock := newTestClock()
	path := refusedSocket(t)
	service := newRetrySteps(clock, nil)
	handler := &dropper{t: t, next: stepsHandler(service)}
	clock.onWait = func(waits int) {
		if waits == 2 {
			serveSteps(t, path, handler)
		}
	}
	p := newRetryPeer(t, retryClient(path), clock)

	outcome := p.tool("step_start", `{"prompt":"check","workspace":"/work"}`)
	if outcome.isError {
		t.Fatalf("step_start reported an error: %s", outcome.text)
	}
	if got := decodeObject(t, outcome.text)["id"]; got != "s1" {
		t.Errorf("step_start id = %v, want s1", got)
	}
	if got := service.count("StartStep"); got != 1 {
		t.Errorf("StartStep calls = %d, want 1", got)
	}
	if got := clock.waitCount(); got != 2 {
		t.Errorf("retry waits = %d, want 2, one per refused dial", got)
	}
}

// A status that meets Unavailable once succeeds on the retry.
func TestStepStatusRetriesOnceAfterUnavailable(t *testing.T) {
	clock := newTestClock()
	service := newRetrySteps(clock, func(method string, call int) bool {
		return method == "StepStatus" && call == 1
	})
	p := startRetryPeer(t, &dropper{t: t, next: stepsHandler(service)}, clock)

	outcome := p.tool("step_status", `{"id":"s1"}`)
	if outcome.isError {
		t.Fatalf("step_status reported an error: %s", outcome.text)
	}
	if got := decodeObject(t, outcome.text)["id"]; got != "s1" {
		t.Errorf("step_status id = %v, want s1", got)
	}
	if got := service.count("StepStatus"); got != 2 {
		t.Errorf("StepStatus calls = %d, want 2: the Unavailable call is retried once", got)
	}
	if got := clock.waitCount(); got != 1 {
		t.Errorf("retry waits = %d, want 1", got)
	}
}

// A stop that meets Unavailable once succeeds on the retry.
func TestStepStopRetriesOnceAfterUnavailable(t *testing.T) {
	clock := newTestClock()
	service := newRetrySteps(clock, func(method string, call int) bool {
		return method == "StopStep" && call == 1
	})
	p := startRetryPeer(t, &dropper{t: t, next: stepsHandler(service)}, clock)

	outcome := p.tool("step_stop", `{"id":"s1"}`)
	if outcome.isError {
		t.Fatalf("step_stop reported an error: %s", outcome.text)
	}
	if got := decodeObject(t, outcome.text)["stopped"]; got != true {
		t.Errorf("step_stop stopped = %v, want true", got)
	}
	if got := service.count("StopStep"); got != 2 {
		t.Errorf("StopStep calls = %d, want 2: the Unavailable call is retried once", got)
	}
}

// A wait whose stream is cut after waitBeforeCut re-waits for the time its request has left, and still returns the
// step's end.
func TestStepWaitReWaitsForTheTimeLeftAfterTheStreamIsLost(t *testing.T) {
	clock := newTestClock()
	start := clock.Now()
	service := newRetrySteps(clock, nil)
	handler := &dropper{
		t:         t,
		procedure: enginev1connect.StepsWaitStepsProcedure,
		drops:     1,
		onDrop:    func() { clock.advance(waitBeforeCut) },
		next:      stepsHandler(service),
	}
	p := startRetryPeer(t, handler, clock)

	outcome := p.tool("step_wait", `{"ids":["s1"],"timeoutSeconds":10}`)
	if outcome.isError {
		t.Fatalf("step_wait reported an error: %s", outcome.text)
	}
	ended, ok := decodeObject(t, outcome.text)["ended"].(map[string]any)
	if !ok || ended["id"] != "s1" || ended["status"] != "pass" {
		t.Fatalf("step_wait ended = %v, want step s1 with status pass", decodeObject(t, outcome.text)["ended"])
	}
	waits := service.recordedWaits()
	if len(waits) != 1 {
		t.Fatalf("WaitSteps reached the daemon %d times, want 1: the retry, since the cut request never arrived",
			len(waits))
	}
	retry := waits[0]
	if retry.at.Before(start.Add(waitBeforeCut)) {
		t.Errorf("retry arrived at %v, before the cut %v later", retry.at, waitBeforeCut)
	}
	if retry.seconds >= 10 {
		t.Errorf("retry asked for %d seconds, want the time left of the 10-second wait", retry.seconds)
	}
	deadline := start.Add(10 * time.Second)
	end := retry.at.Add(time.Duration(retry.seconds) * time.Second)
	if end.Before(deadline) || end.Sub(deadline) >= time.Second {
		t.Errorf("retry ends at %v, want within a second after the deadline %v", end, deadline)
	}
}

// A wait with the daemon down keeps retrying only until its deadline, and then returns a tool error.
func TestStepWaitEndsAtItsDeadlineWhileTheDaemonIsDown(t *testing.T) {
	clock := newTestClock()
	start := clock.Now()
	service := newRetrySteps(clock, func(method string, _ int) bool { return method == "WaitSteps" })
	p := startRetryPeer(t, &dropper{t: t, next: stepsHandler(service)}, clock)

	outcome := p.tool("step_wait", `{"ids":["s1"],"timeoutSeconds":10}`)
	if !outcome.isError {
		t.Fatalf("step_wait = %q, want a tool error while the daemon is down", outcome.text)
	}
	deadline := start.Add(10 * time.Second)
	for _, wait := range service.recordedWaits() {
		if !wait.at.Before(deadline) && wait.seconds != 0 {
			t.Errorf("WaitSteps at %v asked for %d seconds, want 0 once the deadline %v has passed",
				wait.at, wait.seconds, deadline)
		}
	}
	if elapsed := clock.Now().Sub(start); elapsed >= 10*time.Second+maxRetryWait {
		t.Errorf("step_wait ended %v after it began, want about its 10-second deadline", elapsed)
	}
}
