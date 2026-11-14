package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/rpcretry"
)

// hexIDPattern matches a client-chosen id: 32 lowercase hex digits.
var hexIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// retryClock is the retry clock of these tests. Its time moves only when a retry waits, so a retry costs no real time.
type retryClock struct {
	mu  sync.Mutex
	now time.Time
}

// newRetryClock returns a clock at a fixed time.
func newRetryClock() *retryClock {
	return &retryClock{now: time.Unix(1700000000, 0)}
}

// Now returns the clock's time.
func (c *retryClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// After advances the clock by d and returns a channel that has already fired.
func (c *retryClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	c.mu.Unlock()
	fired := make(chan time.Time, 1)
	fired <- now
	return fired
}

// retryEngine is the Engine of these tests. It records every Start, Say, and Stop it receives. Each Start is answered
// with startErr when startErr is set.
type retryEngine struct {
	enginev1connect.UnimplementedEngineHandler
	startErr error

	mu     sync.Mutex
	starts []*v1.StartRequest
	says   []*v1.SayRequest
	stops  []*v1.StopRequest
}

func (e *retryEngine) Start(
	_ context.Context,
	req *connect.Request[v1.StartRequest],
) (*connect.Response[v1.StartResponse], error) {
	e.mu.Lock()
	e.starts = append(e.starts, req.Msg)
	e.mu.Unlock()
	if e.startErr != nil {
		return nil, e.startErr
	}
	return connect.NewResponse(&v1.StartResponse{Id: req.Msg.GetId()}), nil
}

func (e *retryEngine) Say(
	_ context.Context,
	req *connect.Request[v1.SayRequest],
) (*connect.Response[v1.SayResponse], error) {
	e.mu.Lock()
	e.says = append(e.says, req.Msg)
	e.mu.Unlock()
	return connect.NewResponse(&v1.SayResponse{}), nil
}

func (e *retryEngine) Stop(
	_ context.Context,
	req *connect.Request[v1.StopRequest],
) (*connect.Response[v1.StopResponse], error) {
	e.mu.Lock()
	e.stops = append(e.stops, req.Msg)
	e.mu.Unlock()
	return connect.NewResponse(&v1.StopResponse{}), nil
}

// snapshot returns copies of the Start, Say, and Stop requests received so far.
func (e *retryEngine) snapshot() ([]*v1.StartRequest, []*v1.SayRequest, []*v1.StopRequest) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.starts), slices.Clone(e.says), slices.Clone(e.stops)
}

// cutFirst returns a handler that lets every request reach next, except that the first n requests to procedure reach
// next and then lose their responses: the connection closes before an answer is written. The client sees each of them
// cut mid-call.
func cutFirst(t *testing.T, next http.Handler, procedure string, n int) http.Handler {
	t.Helper()
	var mu sync.Mutex
	cut := 0
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != procedure {
			next.ServeHTTP(w, r)
			return
		}
		mu.Lock()
		cutThis := cut < n
		if cutThis {
			cut++
		}
		mu.Unlock()
		if !cutThis {
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(httptest.NewRecorder(), r)
		conn, _, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		_ = conn.Close()
	})
}

// retryServe serves engine on the session socket beside a daemon socket in a new directory and returns the daemon
// socket's path. The first cuts requests to procedure lose their responses.
func retryServe(t *testing.T, engine *retryEngine, procedure string, cuts int) string {
	t.Helper()
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	serveSessions(t, daemon, engine, procedure, cuts)
	return daemon
}

// serveSessions serves engine on the session socket beside daemon. The first cuts requests to procedure lose their
// responses.
func serveSessions(t *testing.T, daemon string, engine *retryEngine, procedure string, cuts int) {
	t.Helper()
	socket := protocol.SessionSocketPath(daemon)
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen on %s: %v", socket, err)
	}
	path, handler := enginev1connect.NewEngineHandler(engine)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := &http.Server{Handler: cutFirst(t, mux, procedure, cuts)}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		if err := srv.Close(); err != nil {
			t.Errorf("close session server: %v", err)
		}
	})
}

// daemonMode is how the fake daemon of these tests meets a sessions question.
type daemonMode int

// The fake daemon answers off, answers on, closes with no answer, or has no socket at all.
const (
	daemonOff daemonMode = iota
	daemonOn
	daemonSilent
	daemonAbsent
)

// fakeDaemon is the daemon socket of these tests. It takes each connection's sessions question, counts it, and then
// answers it with off or on, or closes the connection with no answer, as its mode says.
type fakeDaemon struct {
	mode daemonMode

	mu    sync.Mutex
	asked int
}

// serveDaemon serves a fakeDaemon on the daemon socket path daemon. Its listener closes at cleanup.
func serveDaemon(t *testing.T, daemon string, mode daemonMode) *fakeDaemon {
	t.Helper()
	ln, err := net.Listen("unix", daemon)
	if err != nil {
		t.Fatalf("listen on %s: %v", daemon, err)
	}
	fake := &fakeDaemon{mode: mode}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go fake.answer(conn)
		}
	}()
	t.Cleanup(func() {
		if err := ln.Close(); err != nil {
			t.Errorf("close daemon listener: %v", err)
		}
	})
	return fake
}

// answer reads one frame from conn. A sessions question is counted, and then answered with off or on, or left
// unanswered, as the mode says.
func (d *fakeDaemon) answer(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	frame, err := protocol.ReadFrame(conn)
	if err != nil || frame.Ask != protocol.SessionsAsk {
		return
	}
	d.mu.Lock()
	d.asked++
	d.mu.Unlock()
	switch d.mode {
	case daemonOff:
		_ = protocol.WriteSessionsAnswer(conn, false)
	case daemonOn:
		_ = protocol.WriteSessionsAnswer(conn, true)
	case daemonSilent, daemonAbsent:
	}
}

// questions returns the number of sessions questions the daemon has counted.
func (d *fakeDaemon) questions() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.asked
}

// waitClock is a retry clock that runs onFirstWait before its first wait advances the clock.
type waitClock struct {
	*retryClock
	onFirstWait func()
	once        sync.Once
}

// After runs onFirstWait on the first call, then advances the clock as retryClock does.
func (c *waitClock) After(d time.Duration) <-chan time.Time {
	c.once.Do(c.onFirstWait)
	return c.retryClock.After(d)
}

// retryingVerb is a session verb that retries an Unavailable answer: the arguments that reach the retry and the Engine
// procedure the verb calls.
type retryingVerb struct {
	verb      string
	args      []string
	procedure string
}

// retryingVerbs returns the session verbs that retry an Unavailable answer.
func retryingVerbs() []retryingVerb {
	return []retryingVerb{
		{
			verb:      "start",
			args:      []string{"start", "sub/dir", "first prompt"},
			procedure: enginev1connect.EngineStartProcedure,
		},
		{
			verb:      "say",
			args:      []string{"say", "s1", "hello there"},
			procedure: enginev1connect.EngineSayProcedure,
		},
		{
			verb:      "stop",
			args:      []string{"stop", "s1"},
			procedure: enginev1connect.EngineStopProcedure,
		},
	}
}

func TestSessionOffRefusesAtOnce(t *testing.T) {
	t.Parallel()
	for _, tc := range retryingVerbs() {
		t.Run(tc.verb, func(t *testing.T) {
			t.Parallel()
			daemon := filepath.Join(t.TempDir(), "orreryd.sock")
			fake := serveDaemon(t, daemon, daemonOff)
			clock := newRetryClock()
			begin := clock.Now()
			err := session(t.Context(), clock, daemon, tc.args, &strings.Builder{}, nil)
			want := fmt.Sprintf("session %s: orreryd takes no session commands on %s; start it with --sessions",
				tc.verb, protocol.SessionSocketPath(daemon))
			if err == nil || err.Error() != want {
				t.Errorf("error = %v, want %q", err, want)
			}
			if got := clock.Now().Sub(begin); got != 0 {
				t.Errorf("retry clock advanced %v, want no wait", got)
			}
			if got := fake.questions(); got != 1 {
				t.Errorf("daemon counted %d sessions questions, want 1", got)
			}
		})
	}
}

func TestSessionRetriesUntilSessionsSocketListens(t *testing.T) {
	t.Parallel()
	for _, tc := range retryingVerbs() {
		t.Run(tc.verb+"/on", func(t *testing.T) {
			t.Parallel()
			runRetryUntilListen(t, tc.args, tc.procedure, daemonOn)
		})
		t.Run(tc.verb+"/silent", func(t *testing.T) {
			t.Parallel()
			runRetryUntilListen(t, tc.args, tc.procedure, daemonSilent)
		})
		t.Run(tc.verb+"/absent", func(t *testing.T) {
			t.Parallel()
			runRetryUntilListen(t, tc.args, tc.procedure, daemonAbsent)
		})
	}
}

// runRetryUntilListen runs args with the daemon answering on, silent (closing each sessions question with no answer),
// or absent.
// The session socket starts listening when the retry clock first waits, and the verb must then succeed with one
// Engine call.
func runRetryUntilListen(t *testing.T, args []string, procedure string, mode daemonMode) {
	t.Helper()
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	var fake *fakeDaemon
	if mode != daemonAbsent {
		fake = serveDaemon(t, daemon, mode)
	}
	engine := &retryEngine{}
	clock := &waitClock{retryClock: newRetryClock()}
	clock.onFirstWait = func() { serveSessions(t, daemon, engine, procedure, 0) }
	var out strings.Builder
	if err := session(t.Context(), clock, daemon, args, &out, nil); err != nil {
		t.Fatalf("session %s after the retry: %v", args[0], err)
	}
	if fake != nil {
		if got := fake.questions(); got != 1 {
			t.Errorf("daemon counted %d sessions questions, want 1: the first attempt's", got)
		}
	}
	starts, says, stops := engine.snapshot()
	if got := len(starts) + len(says) + len(stops); got != 1 {
		t.Fatalf("Engine received %d requests, want 1 after the retry", got)
	}
	if args[0] == "start" {
		if want := starts[0].GetId() + "\n"; out.String() != want {
			t.Errorf("start printed %q, want its id %q", out.String(), want)
		}
	}
}

func TestSessionStartRetriesWithItsIDAfterACutCall(t *testing.T) {
	engine := &retryEngine{}
	daemon := retryServe(t, engine, enginev1connect.EngineStartProcedure, 1)
	var out strings.Builder
	args := []string{"start", "sub/dir", "first prompt"}
	if err := session(t.Context(), newRetryClock(), daemon, args, &out, nil); err != nil {
		t.Fatalf("session start after a cut call: %v", err)
	}
	starts, _, _ := engine.snapshot()
	if len(starts) != 2 {
		t.Fatalf("Start received %d requests, want 2: the cut one and its retry", len(starts))
	}
	id := starts[0].GetId()
	if !hexIDPattern.MatchString(id) {
		t.Errorf("first Start id = %q, want 32 lowercase hex digits", id)
	}
	if got := starts[1].GetId(); got != id {
		t.Errorf("retried Start id = %q, want the first Start's id %q", got, id)
	}
	if want := id + "\n"; out.String() != want {
		t.Errorf("start printed %q, want %q", out.String(), want)
	}
}

func TestSessionSayRetriesWithItsPromptIDAfterACutCall(t *testing.T) {
	engine := &retryEngine{}
	daemon := retryServe(t, engine, enginev1connect.EngineSayProcedure, 1)
	var out strings.Builder
	args := []string{"say", "s1", "hello there"}
	if err := session(t.Context(), newRetryClock(), daemon, args, &out, nil); err != nil {
		t.Fatalf("session say after a cut call: %v", err)
	}
	_, says, _ := engine.snapshot()
	if len(says) != 2 {
		t.Fatalf("Say received %d requests, want 2: the cut one and its retry", len(says))
	}
	id := says[0].GetPromptId()
	if !hexIDPattern.MatchString(id) {
		t.Errorf("first Say prompt_id = %q, want 32 lowercase hex digits", id)
	}
	for i, say := range says {
		if say.GetPromptId() != id {
			t.Errorf("Say %d prompt_id = %q, want the first Say's prompt_id %q", i, say.GetPromptId(), id)
		}
		if say.GetId() != "s1" || say.GetText() != "hello there" {
			t.Errorf("Say %d = id %q text %q, want id s1 text %q", i, say.GetId(), say.GetText(), "hello there")
		}
	}
	if out.Len() != 0 {
		t.Errorf("say printed %q, want nothing", out.String())
	}
}

func TestSessionStopSucceedsAfterOneUnavailable(t *testing.T) {
	engine := &retryEngine{}
	daemon := retryServe(t, engine, enginev1connect.EngineStopProcedure, 1)
	var out strings.Builder
	if err := session(t.Context(), newRetryClock(), daemon, []string{"stop", "s1"}, &out, nil); err != nil {
		t.Fatalf("session stop after one Unavailable: %v", err)
	}
	_, _, stops := engine.snapshot()
	if len(stops) != 2 {
		t.Fatalf("Stop received %d requests, want 2: the cut one and its retry", len(stops))
	}
	for i, stop := range stops {
		if stop.GetId() != "s1" {
			t.Errorf("Stop %d id = %q, want s1", i, stop.GetId())
		}
	}
	if out.Len() != 0 {
		t.Errorf("stop printed %q, want nothing", out.String())
	}
}

func TestSessionStartFailsAfterTheWindow(t *testing.T) {
	engine := &retryEngine{startErr: connect.NewError(connect.CodeUnavailable, errors.New("orreryd is restarting"))}
	daemon := retryServe(t, engine, enginev1connect.EngineStartProcedure, 0)
	clock := newRetryClock()
	begin := clock.Now()
	err := session(t.Context(), clock, daemon, []string{"start", "sub/dir", "first prompt"}, &strings.Builder{}, nil)
	if err == nil {
		t.Fatal("session start answered Unavailable on every attempt returned nil")
	}
	if want := "session start: orreryd is restarting"; err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
	if got := clock.Now().Sub(begin); got != rpcretry.Window {
		t.Errorf("retries spanned %v on the clock, want the window %v", got, rpcretry.Window)
	}
	starts, _, _ := engine.snapshot()
	if len(starts) < 2 {
		t.Fatalf("Start received %d requests, want retries within the window", len(starts))
	}
	for i, start := range starts {
		if start.GetId() != starts[0].GetId() {
			t.Errorf("Start %d id = %q, want the first Start's id %q", i, start.GetId(), starts[0].GetId())
		}
	}
}
