package cli

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/rpcretry"
)

// resumeWatch scripts one Watch call of resumeEvents. A call with openErr fails before it sends a message. Any other
// call sends each message of the log after its cursor, up to seq through, and then ends with endErr, or cleanly when
// endErr is nil.
type resumeWatch struct {
	openErr error
	through int64
	endErr  error
}

// resumeEvents is the serve stand-in of these tests. Its Sessions handler lists listSeq. Its Events handler answers
// the scripted calls in order and records the cursor of each one. Once the script runs out it fails to open with
// Unavailable, as a serve that is down does. It stamps each message it sends with the time on clock.
type resumeEvents struct {
	orreryv1connect.UnimplementedSessionsHandler
	orreryv1connect.UnimplementedEventsHandler

	listSeq int64
	log     []*orreryv1.WatchResponse
	calls   []resumeWatch
	clock   *retryClock

	mu       sync.Mutex
	afters   []int64
	lastSent time.Time
}

// List answers the newest seq.
func (f *resumeEvents) List(
	context.Context,
	*connect.Request[orreryv1.ListRequest],
) (*connect.Response[orreryv1.ListResponse], error) {
	return connect.NewResponse(&orreryv1.ListResponse{Seq: f.listSeq}), nil
}

// Watch records the cursor it was opened from and runs the next scripted call.
func (f *resumeEvents) Watch(
	_ context.Context,
	req *connect.Request[orreryv1.WatchRequest],
	stream *connect.ServerStream[orreryv1.WatchResponse],
) error {
	after := req.Msg.GetAfter()
	f.mu.Lock()
	n := len(f.afters)
	f.afters = append(f.afters, after)
	f.mu.Unlock()
	if n >= len(f.calls) {
		return connect.NewError(connect.CodeUnavailable, errors.New("serve is down"))
	}
	call := f.calls[n]
	if call.openErr != nil {
		return call.openErr
	}
	for _, msg := range f.log {
		if msg.GetSeq() <= after || msg.GetSeq() > call.through {
			continue
		}
		if err := stream.Send(msg); err != nil {
			return err
		}
		f.mu.Lock()
		f.lastSent = f.clock.Now()
		f.mu.Unlock()
	}
	return call.endErr
}

// watchAfters returns the cursor of each Watch call, in order.
func (f *resumeEvents) watchAfters() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.afters)
}

// sentAt returns the clock time at which the last message was sent.
func (f *resumeEvents) sentAt() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastSent
}

// resumeServe serves fake's Sessions and Events handlers on an httptest server and returns its URL.
func resumeServe(t *testing.T, fake *resumeEvents) string {
	t.Helper()
	mux := http.NewServeMux()
	sessionsPath, sessionsHandler := orreryv1connect.NewSessionsHandler(fake)
	mux.Handle(sessionsPath, sessionsHandler)
	eventsPath, eventsHandler := orreryv1connect.NewEventsHandler(fake)
	mux.Handle(eventsPath, eventsHandler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

// resumeWatchRun runs `session --url url watch s1` with clock as its retry clock. It returns what the command wrote and
// the error it returned.
func resumeWatchRun(t *testing.T, clock *retryClock, url string) (string, error) {
	t.Helper()
	var out strings.Builder
	socket := filepath.Join(t.TempDir(), "sessions.sock")
	err := session(t.Context(), clock, socket, []string{"--url", url, "watch", "s1"}, &out, nil)
	return out.String(), err
}

// resumeAdded returns an engine_added event for session id in the running state, stored at seq.
func resumeAdded(seq int64, id string) *orreryv1.WatchResponse {
	return &orreryv1.WatchResponse{
		Seq:       seq,
		SessionId: id,
		Event: &orreryv1.WatchResponse_EngineAdded{
			EngineAdded: &orreryv1.EngineAdded{Dir: "/work", State: "running"},
		},
	}
}

// resumeState returns an engine_changed event for session id that sets its state to state, stored at seq.
func resumeState(seq int64, id, state string) *orreryv1.WatchResponse {
	return &orreryv1.WatchResponse{
		Seq:       seq,
		SessionId: id,
		Event: &orreryv1.WatchResponse_EngineChanged{
			EngineChanged: &orreryv1.EngineChanged{State: new(state)},
		},
	}
}

// checkResumeLines checks that out holds one compact protojson line for each message in want, in order.
func checkResumeLines(t *testing.T, out string, want ...*orreryv1.WatchResponse) {
	t.Helper()
	var lines []string
	if out != "" {
		lines = strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	}
	if len(lines) != len(want) {
		t.Fatalf("watch wrote %d lines, want %d: %q", len(lines), len(want), out)
	}
	for i, line := range lines {
		var got orreryv1.WatchResponse
		if err := protojson.Unmarshal([]byte(line), &got); err != nil {
			t.Fatalf("line %d %q does not decode: %v", i, line, err)
		}
		if !proto.Equal(&got, want[i]) {
			t.Errorf("line %d = %v, want %v", i, &got, want[i])
		}
	}
}

func TestSessionURLWatchResumesAfterUnavailableEnd(t *testing.T) {
	// The newest seq is 2, so the stop at seq 5 ends watch. The first stream ends with Unavailable after seq 4, while
	// s1 is still running. The second stream opens after seq 4 and delivers seq 5, which stops s1.
	added := resumeAdded(1, "s1")
	running := resumeState(3, "s1", "running")
	stopped := resumeState(5, "s1", "stopped")
	clock := newRetryClock()
	fake := &resumeEvents{
		listSeq: 2,
		log: []*orreryv1.WatchResponse{
			added,
			resumeAdded(2, "s2"),
			running,
			resumeState(4, "s2", "running"),
			stopped,
		},
		calls: []resumeWatch{
			{through: 4, endErr: connect.NewError(connect.CodeUnavailable, errors.New("serve restarted"))},
			{through: 5},
		},
		clock: clock,
	}
	out, err := resumeWatchRun(t, clock, resumeServe(t, fake))
	if err != nil {
		t.Fatalf("session watch: %v", err)
	}
	if got, want := fake.watchAfters(), []int64{0, 4}; !slices.Equal(got, want) {
		t.Errorf("Watch cursors = %v, want %v", got, want)
	}
	checkResumeLines(t, out, added, running, stopped)
}

func TestSessionURLWatchEndingWithOtherCodeFails(t *testing.T) {
	// A stream that ends with a code other than Unavailable is an error, and watch does not reopen it.
	added := resumeAdded(1, "s1")
	clock := newRetryClock()
	fake := &resumeEvents{
		listSeq: 0,
		log:     []*orreryv1.WatchResponse{added},
		calls: []resumeWatch{
			{through: 1, endErr: connect.NewError(connect.CodeInternal, errors.New("store closed"))},
		},
		clock: clock,
	}
	out, err := resumeWatchRun(t, clock, resumeServe(t, fake))
	if err == nil || !strings.Contains(err.Error(), "store closed") {
		t.Fatalf("session watch returned %v, want the error the stream ended with", err)
	}
	if got, want := fake.watchAfters(), []int64{0}; !slices.Equal(got, want) {
		t.Errorf("Watch cursors = %v, want %v", got, want)
	}
	checkResumeLines(t, out, added)
}

func TestSessionURLWatchReopensWhenOpenIsUnavailable(t *testing.T) {
	// The first Watch fails to open with Unavailable before any message, so watch reopens from cursor 0 and reads the
	// stop at seq 2.
	added := resumeAdded(1, "s1")
	stopped := resumeState(2, "s1", "stopped")
	clock := newRetryClock()
	fake := &resumeEvents{
		listSeq: 2,
		log:     []*orreryv1.WatchResponse{added, stopped},
		calls: []resumeWatch{
			{openErr: connect.NewError(connect.CodeUnavailable, errors.New("serve restarted"))},
			{through: 2},
		},
		clock: clock,
	}
	out, err := resumeWatchRun(t, clock, resumeServe(t, fake))
	if err != nil {
		t.Fatalf("session watch: %v", err)
	}
	if got, want := fake.watchAfters(), []int64{0, 0}; !slices.Equal(got, want) {
		t.Errorf("Watch cursors = %v, want %v", got, want)
	}
	checkResumeLines(t, out, added, stopped)
}

func TestSessionURLWatchGivesUpWindowAfterLastMessage(t *testing.T) {
	// The first Watch fails to open, so the retry clock moves by one wait before the second stream delivers seqs 1 to 4
	// and ends with Unavailable. Every later open fails too. watch keeps reopening after seq 4 until rpcretry.Window has
	// passed since seq 4 was sent, not since watch began, and then returns the error.
	clock := newRetryClock()
	fake := &resumeEvents{
		listSeq: 2,
		log: []*orreryv1.WatchResponse{
			resumeAdded(1, "s1"),
			resumeAdded(2, "s2"),
			resumeState(3, "s1", "running"),
			resumeState(4, "s2", "running"),
		},
		calls: []resumeWatch{
			{openErr: connect.NewError(connect.CodeUnavailable, errors.New("serve is down"))},
			{through: 4, endErr: connect.NewError(connect.CodeUnavailable, errors.New("serve is down"))},
		},
		clock: clock,
	}
	_, err := resumeWatchRun(t, clock, resumeServe(t, fake))
	if err == nil || !strings.Contains(err.Error(), "serve is down") {
		t.Fatalf("session watch returned %v, want the Unavailable error", err)
	}
	afters := fake.watchAfters()
	if len(afters) < 3 {
		t.Fatalf("Watch cursors = %v, want watch to reopen after the stream that delivered seq 4", afters)
	}
	for i, after := range afters[2:] {
		if after != 4 {
			t.Errorf("reopen %d cursor = %d, want 4", i, after)
		}
	}
	if waited := clock.Now().Sub(fake.sentAt()); waited != rpcretry.Window {
		t.Errorf("watch gave up %v after seq 4 was sent, want rpcretry.Window %v", waited, rpcretry.Window)
	}
}

func TestSessionURLWatchKeepsSessionStateAcrossReopen(t *testing.T) {
	// The newest seq is 3. Seq 2 stops s1 before the newest seq, so it does not end watch, and the stream then ends with
	// Unavailable. The reopened stream opens after seq 2 and delivers seq 3, which is past the newest seq. s1 is still
	// stopped, so watch returns there. A watch that forgot the stop would read on and never return.
	added := resumeAdded(1, "s1")
	stopped := resumeState(2, "s1", "stopped")
	clock := newRetryClock()
	fake := &resumeEvents{
		listSeq: 3,
		log:     []*orreryv1.WatchResponse{added, stopped, resumeAdded(3, "s2")},
		calls: []resumeWatch{
			{through: 2, endErr: connect.NewError(connect.CodeUnavailable, errors.New("serve restarted"))},
			{through: 3},
		},
		clock: clock,
	}
	out, err := resumeWatchRun(t, clock, resumeServe(t, fake))
	if err != nil {
		t.Fatalf("session watch: %v", err)
	}
	if got, want := fake.watchAfters(), []int64{0, 2}; !slices.Equal(got, want) {
		t.Errorf("Watch cursors = %v, want %v", got, want)
	}
	checkResumeLines(t, out, added, stopped)
}
