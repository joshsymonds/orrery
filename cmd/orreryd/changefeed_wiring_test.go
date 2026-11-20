package main

import (
	"context"
	"net"
	"net/http"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// Synthetic values only. The session and its task are invented.
const (
	changeFeedWiringSession = "changes-wiring-session"
	changeFeedWiringTask    = "changes-wiring-task"
	changeFeedWiringStamp   = 1_700_000_000
)

// changeFeedWiringClient returns a Changes client that dials the session socket at path.
func changeFeedWiringClient(t *testing.T, path string) enginev1connect.ChangesClient {
	t.Helper()
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", path)
		},
	}
	t.Cleanup(transport.CloseIdleConnections)
	return enginev1connect.NewChangesClient(&http.Client{Transport: transport}, "http://changes.invalid")
}

// TestOrrerydStreamFeedRelaysAppendedSessions checks that a --sessions orreryd relays a store write that appends an
// event for a session to the Changes.Watch streams on its session socket. The stream opens with the opened message,
// then receives a message that names the session and the seq of the event the write appended.
func TestOrrerydStreamFeedRelaysAppendedSessions(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	st, getenv := openLimitStore(t)
	path, err := store.Path(getenv)
	if err != nil {
		t.Fatalf("store path: %v", err)
	}
	host, socket := startNoticesHost(t, st, getenv, nil, nil)

	stream, err := changeFeedWiringClient(t, socket).Watch(ctx, connect.NewRequest(&enginev1.ChangesWatchRequest{}))
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if !stream.Receive() {
		t.Fatalf("Receive opened change: %v", stream.Err())
	}
	if !stream.Msg().GetOpened() {
		t.Fatalf("first watched change = %v, want the opened message", stream.Msg())
	}

	reader := store.NewReader(path)
	before, err := reader.Events(ctx, 0, 1)
	if err != nil {
		t.Fatalf("read newest event seq: %v", err)
	}
	if err := st.ApplyEngine(ctx, []store.EngineOp{
		store.EngineTask{ID: changeFeedWiringSession, TaskID: changeFeedWiringTask},
	}, time.Unix(changeFeedWiringStamp, 0)); err != nil {
		t.Fatalf("append event for %s: %v", changeFeedWiringSession, err)
	}

	if !stream.Receive() {
		t.Fatalf("Receive change: %v", stream.Err())
	}
	after, err := reader.Events(ctx, before.Newest, 10)
	if err != nil {
		t.Fatalf("read events after seq %d: %v", before.Newest, err)
	}
	msg := stream.Msg()
	if msg.GetSeq() != after.Newest {
		t.Errorf("watched change seq = %d, want %d, the seq of the appended event", msg.GetSeq(), after.Newest)
	}
	if want := []string{changeFeedWiringSession}; !slices.Equal(msg.GetSessions(), want) {
		t.Errorf("watched change sessions = %q, want %q", msg.GetSessions(), want)
	}

	// The watch ends before the host stops, so the session socket does not wait out its shutdown on it.
	cancel()
	if err := host.stop(); err != nil {
		t.Fatalf("stop sessions: %v", err)
	}
}
