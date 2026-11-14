package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/rpcretry"
)

// resumeWait bounds one Events call, so a call that never returns fails its test instead of hanging the run.
const resumeWait = 10 * time.Second

// watchStep is one scripted Watch call. It sends a message for each seq it is given, then returns its end error.
type watchStep func(stream *connect.ServerStream[orreryv1.WatchResponse]) error

// sendThen returns a step that sends a message for each of seqs, then returns end, which ends the stream.
func sendThen(end error, seqs ...int64) watchStep {
	return func(stream *connect.ServerStream[orreryv1.WatchResponse]) error {
		for _, seq := range seqs {
			if err := stream.Send(&orreryv1.WatchResponse{Seq: seq}); err != nil {
				return err
			}
		}
		return end
	}
}

// unavailableError is the error a stream ends with when serve restarts or shuts down.
func unavailableError() error {
	return connect.NewError(connect.CodeUnavailable, errors.New("serve restarted"))
}

// scriptedEvents answers the Watch calls in order from steps. A call past the steps ends with Unavailable and sends
// nothing. It records the after cursor of each call it answers.
type scriptedEvents struct {
	orreryv1connect.UnimplementedEventsHandler

	steps []watchStep

	mu     sync.Mutex
	afters []int64
}

// Watch answers the next scripted call.
func (s *scriptedEvents) Watch(
	_ context.Context,
	req *connect.Request[orreryv1.WatchRequest],
	stream *connect.ServerStream[orreryv1.WatchResponse],
) error {
	s.mu.Lock()
	call := len(s.afters)
	s.afters = append(s.afters, req.Msg.GetAfter())
	s.mu.Unlock()
	if call >= len(s.steps) {
		return unavailableError()
	}
	return s.steps[call](stream)
}

// Afters returns the after cursor of each Watch call answered so far, in call order.
func (s *scriptedEvents) Afters() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.afters)
}

// listedSessions answers List with seq as the newest seq of the log.
type listedSessions struct {
	orreryv1connect.UnimplementedSessionsHandler

	seq int64
}

// List returns the newest seq this fake was built with.
func (l listedSessions) List(
	context.Context,
	*connect.Request[orreryv1.ListRequest],
) (*connect.Response[orreryv1.ListResponse], error) {
	return connect.NewResponse(&orreryv1.ListResponse{Seq: l.seq}), nil
}

// cutFirstEvents answers the first n requests of next by closing their connection before any response, so each of those
// calls fails to open with a connection cut.
func cutFirstEvents(t *testing.T, n int32, next http.Handler) http.Handler {
	t.Helper()
	var requests atomic.Int32
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) > n {
			next.ServeHTTP(w, r)
			return
		}
		conn, _, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		_ = conn.Close()
	})
}

// serveResume serves a Sessions list whose newest seq is newest, and events as the Events service. The first cuts
// Watch requests are closed before any response. It returns the URL of the server, which closes at cleanup.
func serveResume(t *testing.T, newest int64, events *scriptedEvents, cuts int32) string {
	t.Helper()
	mux := http.NewServeMux()
	sessionsPath, sessions := orreryv1connect.NewSessionsHandler(listedSessions{seq: newest})
	mux.Handle(sessionsPath, sessions)
	eventsPath, eventsHandler := orreryv1connect.NewEventsHandler(events)
	mux.Handle(eventsPath, cutFirstEvents(t, cuts, eventsHandler))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

// runResume runs Events with args, writing to out, under a context that ends after resumeWait.
func runResume(t *testing.T, out io.Writer, args ...string) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), resumeWait)
	defer cancel()
	return Events(ctx, args, out)
}

// resumeSeqs decodes each line of out as a WatchResponse and returns the seqs in line order.
func resumeSeqs(t *testing.T, out *bytes.Buffer) []int64 {
	t.Helper()
	text := strings.TrimSuffix(out.String(), "\n")
	if text == "" {
		return nil
	}
	var seqs []int64
	for line := range strings.SplitSeq(text, "\n") {
		var msg orreryv1.WatchResponse
		if err := protojson.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("decode line %q: %v", line, err)
		}
		seqs = append(seqs, msg.GetSeq())
	}
	return seqs
}

// steppedClock is a retry clock that starts at its zero time and moves forward by step on every wait. It never sleeps.
type steppedClock struct {
	now  time.Time
	step time.Duration
}

// Now returns the clock's time.
func (c *steppedClock) Now() time.Time {
	return c.now
}

// After moves the clock forward by its step and returns a channel that has already fired.
func (c *steppedClock) After(time.Duration) <-chan time.Time {
	c.now = c.now.Add(c.step)
	fired := make(chan time.Time, 1)
	fired <- c.now
	return fired
}

// EventsOnSteppedClock runs Events on a clock that moves forward by step on every retry wait. The cli_test package
// uses it to drive the retry window without sleeping.
func EventsOnSteppedClock(ctx context.Context, step time.Duration, args []string, out io.Writer) error {
	return eventsWithClock(ctx, &steppedClock{step: step}, args, out)
}

func TestEventsResumesAfterUnavailableFromLastSeq(t *testing.T) {
	events := &scriptedEvents{steps: []watchStep{
		sendThen(unavailableError(), 5),
		sendThen(nil, 6, 7),
	}}
	url := serveResume(t, 7, events, 0)

	var out bytes.Buffer
	if err := runResume(t, &out, "--url", url, "--after", "4"); err != nil {
		t.Fatalf("Events: %v", err)
	}
	if got := resumeSeqs(t, &out); !slices.Equal(got, []int64{5, 6, 7}) {
		t.Errorf("Events wrote seqs %v, want 5, 6 and 7 once each", got)
	}
	// Without --follow the run stops at the newest seq, so there is no third Watch call.
	if got := events.Afters(); !slices.Equal(got, []int64{4, 5}) {
		t.Errorf("Watch calls started after %v, want 4 and then 5", got)
	}
}

func TestEventsRetriesStreamCutBeforeMessage(t *testing.T) {
	events := &scriptedEvents{steps: []watchStep{sendThen(nil, 5)}}
	url := serveResume(t, 5, events, 1)

	var out bytes.Buffer
	if err := runResume(t, &out, "--url", url, "--after", "4"); err != nil {
		t.Fatalf("Events: %v", err)
	}
	if got := resumeSeqs(t, &out); !slices.Equal(got, []int64{5}) {
		t.Errorf("Events wrote seqs %v, want 5", got)
	}
	// The cut call never reaches the script, so the one answered call shows the retry resumed after 4.
	if got := events.Afters(); !slices.Equal(got, []int64{4}) {
		t.Errorf("Watch calls answered after %v, want 4", got)
	}
}

// reopenClock is a retry clock that counts its waits, and runs reopen on each one before the retry it times.
type reopenClock struct {
	reopen func()
	waits  int
}

// Now returns the zero time, so the retry window never passes.
func (c *reopenClock) Now() time.Time {
	return time.Time{}
}

// After counts the wait, runs reopen, and returns a channel that has already fired.
func (c *reopenClock) After(time.Duration) <-chan time.Time {
	c.waits++
	c.reopen()
	fired := make(chan time.Time, 1)
	fired <- time.Time{}
	return fired
}

func TestEventsRetriesWatchThatFailsToOpen(t *testing.T) {
	events := &scriptedEvents{steps: []watchStep{sendThen(nil, 5)}}
	first, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := first.Addr().String()
	mux := http.NewServeMux()
	sessionsPath, sessions := orreryv1connect.NewSessionsHandler(listedSessions{seq: 5})
	mux.Handle(sessionsPath, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The client must drop this connection and close the listener, so the Watch call dials a closed port.
		w.Header().Set("Connection", "close")
		sessions.ServeHTTP(w, r)
		_ = first.Close()
	}))
	eventsPath, eventsHandler := orreryv1connect.NewEventsHandler(events)
	mux.Handle(eventsPath, eventsHandler)
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(first) }()
	t.Cleanup(func() { _ = srv.Close() })

	// The retry clock listens on the same address again during its wait, so the retry dials a live server.
	clock := &reopenClock{reopen: func() {
		second, listenErr := net.Listen("tcp", addr)
		if listenErr != nil {
			t.Errorf("reopen %s: %v", addr, listenErr)
			return
		}
		again := &http.Server{Handler: mux}
		go func() { _ = again.Serve(second) }()
		t.Cleanup(func() { _ = again.Close() })
	}}
	ctx, cancel := context.WithTimeout(t.Context(), resumeWait)
	defer cancel()
	var out bytes.Buffer
	if err := eventsWithClock(ctx, clock, []string{"--url", "http://" + addr, "--after", "4"}, &out); err != nil {
		t.Fatalf("Events: %v", err)
	}
	if got := resumeSeqs(t, &out); !slices.Equal(got, []int64{5}) {
		t.Errorf("Events wrote seqs %v, want 5", got)
	}
	if clock.waits != 1 {
		t.Errorf("Events waited %d times before the retry, want once", clock.waits)
	}
	// The refused dial never reaches the script, so the one answered call shows the retry resumed after 4.
	if got := events.Afters(); !slices.Equal(got, []int64{4}) {
		t.Errorf("Watch calls answered after %v, want 4", got)
	}
}

func TestEventsEndWithOtherCodeFailsWithoutResume(t *testing.T) {
	events := &scriptedEvents{steps: []watchStep{
		sendThen(connect.NewError(connect.CodeInternal, errors.New("store broke")), 5),
		sendThen(nil, 6, 7),
	}}
	url := serveResume(t, 7, events, 0)

	var out bytes.Buffer
	err := runResume(t, &out, "--url", url, "--after", "4")
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("Events returned %v, want the Internal end of the stream", err)
	}
	if got := resumeSeqs(t, &out); !slices.Equal(got, []int64{5}) {
		t.Errorf("Events wrote seqs %v, want 5", got)
	}
	if got := events.Afters(); !slices.Equal(got, []int64{4}) {
		t.Errorf("Watch calls answered after %v, want only 4", got)
	}
}

func TestEventsRetryWindowRunsFromLastMessage(t *testing.T) {
	// Each wait moves the clock half the window. The window measured from the start of the call ends after the third
	// message, so only a window measured from the last message lets the fourth message through.
	events := &scriptedEvents{steps: []watchStep{
		sendThen(unavailableError(), 5),
		sendThen(unavailableError(), 6),
		sendThen(unavailableError(), 7),
		sendThen(unavailableError(), 8),
	}}
	url := serveResume(t, 4, events, 0)

	ctx, cancel := context.WithTimeout(t.Context(), resumeWait)
	defer cancel()
	var out bytes.Buffer
	err := EventsOnSteppedClock(ctx, rpcretry.Window/2, []string{"--url", url, "--after", "4", "--follow"}, &out)
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("Events returned %v, want the Unavailable end once the window passed", err)
	}
	if got := resumeSeqs(t, &out); !slices.Equal(got, []int64{5, 6, 7, 8}) {
		t.Errorf("Events wrote seqs %v, want 5 through 8 once each", got)
	}
	if got := events.Afters(); !slices.Equal(got, []int64{4, 5, 6, 7, 8, 8}) {
		t.Errorf("Watch calls answered after %v, want 4 through 8, then 8 again for the last retry", got)
	}
}
