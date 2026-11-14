package cli

import (
	"context"
	"errors"
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

// retryServe serves engine on a session socket in a new directory and returns the socket's path. The first cuts
// requests to procedure lose their responses.
func retryServe(t *testing.T, engine *retryEngine, procedure string, cuts int) string {
	t.Helper()
	socket := protocol.SessionSocketPath(filepath.Join(t.TempDir(), "orreryd.sock"))
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
	return socket
}

func TestSessionStartRetriesWithItsIDAfterACutCall(t *testing.T) {
	engine := &retryEngine{}
	socket := retryServe(t, engine, enginev1connect.EngineStartProcedure, 1)
	var out strings.Builder
	args := []string{"start", "sub/dir", "first prompt"}
	if err := session(t.Context(), newRetryClock(), socket, args, &out, nil); err != nil {
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
	socket := retryServe(t, engine, enginev1connect.EngineSayProcedure, 1)
	var out strings.Builder
	args := []string{"say", "s1", "hello there"}
	if err := session(t.Context(), newRetryClock(), socket, args, &out, nil); err != nil {
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
	socket := retryServe(t, engine, enginev1connect.EngineStopProcedure, 1)
	var out strings.Builder
	if err := session(t.Context(), newRetryClock(), socket, []string{"stop", "s1"}, &out, nil); err != nil {
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
	socket := retryServe(t, engine, enginev1connect.EngineStartProcedure, 0)
	clock := newRetryClock()
	begin := clock.Now()
	err := session(t.Context(), clock, socket, []string{"start", "sub/dir", "first prompt"}, &strings.Builder{}, nil)
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
