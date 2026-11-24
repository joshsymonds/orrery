package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	enginev1connect "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

const (
	// eventsFeedPane is the row id of the pane the feed tests write.
	eventsFeedPane = "orrery-1"
	// eventsFeedAt is the unix second the feed tests' events are stored at.
	eventsFeedAt = 1_700_000_000
	// eventsFeedBuffer is the capacity of the feed tests' channels that record each read, call and timer.
	eventsFeedBuffer = 64
)

// eventsFeedReader is the reader the feed tests give eventsServer. It counts each read and reports each one complete on
// reads, so a test waits for the reads it expects to finish, and then checks the count.
type eventsFeedReader struct {
	reader *store.Reader
	count  atomic.Int64
	reads  chan struct{}
}

// Events counts the read, reads the page from the store, and reports the read complete.
func (r *eventsFeedReader) Events(ctx context.Context, after int64, limit int) (store.EventPage, error) {
	r.count.Add(1)
	page, err := r.reader.Events(ctx, after, limit)
	r.reads <- struct{}{}
	return page, err
}

// eventsFeedChanges is the Changes service the feed tests serve in place of orreryd's. It counts each Watch call and
// announces each one on calls. A call delivers the messages the test sends on it, and ends when the test closes its end
// or when its context ends.
type eventsFeedChanges struct {
	watches atomic.Int64
	calls   chan *eventsFeedCall
}

// eventsFeedCall is one Changes.Watch call that eventsFeedChanges serves.
type eventsFeedCall struct {
	msgs chan *enginev1.ChangesWatchResponse
	end  chan struct{}
}

// Watch serves one call: it announces the call, then delivers each message the test sends on it until the call ends.
func (c *eventsFeedChanges) Watch(
	ctx context.Context,
	_ *connect.Request[enginev1.ChangesWatchRequest],
	stream *connect.ServerStream[enginev1.ChangesWatchResponse],
) error {
	c.watches.Add(1)
	call := &eventsFeedCall{msgs: make(chan *enginev1.ChangesWatchResponse), end: make(chan struct{})}
	select {
	case c.calls <- call:
	case <-ctx.Done():
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-call.end:
			return errors.New("change feed is down")
		case msg := <-call.msgs:
			if err := stream.Send(msg); err != nil {
				return fmt.Errorf("send change: %w", err)
			}
		}
	}
}

// eventsFeedTimer is a retry timer the feed tests' NewTimer armed: its duration, and the channel the test fires it on.
type eventsFeedTimer struct {
	after time.Duration
	fire  chan time.Time
}

// eventsFeedTimers is the NewTimer of the feed tests. It arms no real timer: each timer it arms is sent on armed, and
// the test fires the timer by sending on its fire channel.
type eventsFeedTimers struct {
	armed chan eventsFeedTimer
}

// newTimer arms a timer for after, which the test fires.
func (f *eventsFeedTimers) newTimer(after time.Duration) (<-chan time.Time, func() bool) {
	fire := make(chan time.Time, 1)
	f.armed <- eventsFeedTimer{after: after, fire: fire}
	return fire, func() bool { return true }
}

// eventsFeedRig is the setup the feed tests share: a store writer, the counting reader over its file, the Changes
// service and timers the hub runs on, and the URL of an Events server that reads through the counting reader and whose
// streams the hub wakes.
type eventsFeedRig struct {
	store   *store.Store
	reader  *eventsFeedReader
	changes *eventsFeedChanges
	timers  *eventsFeedTimers
	url     string
}

// eventsFeedSetup returns a rig over a new store. Its servers close at cleanup.
func eventsFeedSetup(t *testing.T) *eventsFeedRig {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	s, err := store.Open(t.Context(), path, storetest.NoSync())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close store: %v", err)
		}
	})
	rig := &eventsFeedRig{
		store: s,
		reader: &eventsFeedReader{
			reader: store.NewReader(path), reads: make(chan struct{}, eventsFeedBuffer),
		},
		changes: &eventsFeedChanges{calls: make(chan *eventsFeedCall, eventsFeedBuffer)},
		timers:  &eventsFeedTimers{armed: make(chan eventsFeedTimer, eventsFeedBuffer)},
	}
	_, changesHandler := enginev1connect.NewChangesHandler(rig.changes)
	changesSrv := httptest.NewServer(changesHandler)
	t.Cleanup(changesSrv.Close)
	hub := newChangeHub(enginev1connect.NewChangesClient(http.DefaultClient, changesSrv.URL), rig.timers.newTimer)
	_, eventsHandler := orreryv1connect.NewEventsHandler(eventsServer{
		reader: rig.reader, changes: hub, sendTimeout: defaultEventSendTimeout,
	})
	eventsSrv := httptest.NewServer(withResponseWriter(eventsHandler))
	t.Cleanup(eventsSrv.Close)
	rig.url = eventsSrv.URL
	return rig
}

// eventsFeedStream is one Events.Watch stream the feed tests open. msgs carries each message and is closed when the
// stream ends, err is the reason it ended, and done closes once the goroutine that reads the stream has returned.
type eventsFeedStream struct {
	msgs   chan *orreryv1.WatchResponse
	done   chan struct{}
	cancel context.CancelFunc
	err    error
}

// eventsFeedOpen opens Events.Watch after the cursor on the rig's server. The stream ends at cleanup.
func eventsFeedOpen(t *testing.T, rig *eventsFeedRig, after int64) *eventsFeedStream {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	st := &eventsFeedStream{msgs: make(chan *orreryv1.WatchResponse), done: make(chan struct{}), cancel: cancel}
	events := orreryv1connect.NewEventsClient(http.DefaultClient, rig.url)
	go st.run(ctx, events, after)
	t.Cleanup(func() {
		cancel()
		<-st.done
	})
	return st
}

// run forwards the stream's messages to msgs, then closes msgs once err holds why the stream ended.
func (st *eventsFeedStream) run(ctx context.Context, events orreryv1connect.EventsClient, after int64) {
	defer close(st.done)
	st.err = st.forward(ctx, events, after)
	close(st.msgs)
}

// forward sends each message of the stream to msgs until the stream ends or ctx ends, and returns why it ended.
func (st *eventsFeedStream) forward(ctx context.Context, events orreryv1connect.EventsClient, after int64) error {
	stream, err := events.Watch(ctx, connect.NewRequest(&orreryv1.WatchRequest{After: after}))
	if err != nil {
		return fmt.Errorf("open Watch: %w", err)
	}
	for stream.Receive() {
		select {
		case st.msgs <- stream.Msg():
		case <-ctx.Done():
			return errors.Join(stream.Err(), stream.Close())
		}
	}
	return errors.Join(stream.Err(), stream.Close())
}

// eventsFeedNext returns the next message of st, failing the test when the stream ends first.
func eventsFeedNext(t *testing.T, st *eventsFeedStream) *orreryv1.WatchResponse {
	t.Helper()
	msg, ok := <-st.msgs
	if !ok {
		t.Fatalf("stream ended before the next message: %v", st.err)
	}
	return msg
}

// eventsFeedWantSeq fails the test unless the next message of st has seq want.
func eventsFeedWantSeq(t *testing.T, st *eventsFeedStream, want int64) {
	t.Helper()
	if got := eventsFeedNext(t, st).GetSeq(); got != want {
		t.Fatalf("message: seq %d, want %d", got, want)
	}
}

// eventsFeedAddPane writes the pane of the feed tests, which appends one session_added event.
func eventsFeedAddPane(t *testing.T, s *store.Store) {
	t.Helper()
	pane := store.Pane{ID: eventsFeedPane, Pane: "%1"}
	if err := s.SyncServer(t.Context(), "", "-L a", []store.Pane{pane}, time.Unix(eventsFeedAt, 0)); err != nil {
		t.Fatalf("SyncServer: %v", err)
	}
}

// eventsFeedChange sets the state of the pane to state-i, which appends one session_changed event.
func eventsFeedChange(t *testing.T, s *store.Store, i int) {
	t.Helper()
	pane := store.Pane{ID: eventsFeedPane, Pane: "%1", Fields: store.Fields{State: fmt.Sprintf("state-%d", i)}}
	if err := s.SyncPane(t.Context(), "-L a", pane, time.Unix(eventsFeedAt, 0)); err != nil {
		t.Fatalf("SyncPane state-%d: %v", i, err)
	}
}

// eventsFeedChangeMsg is the change the feed reports after a write appended events through seq to the pane.
func eventsFeedChangeMsg(seq int64) *enginev1.ChangesWatchResponse {
	return &enginev1.ChangesWatchResponse{Seq: seq, Sessions: []string{eventsFeedPane}}
}

// eventsFeedAwaitReads receives n completed reads from r, so the test knows each of them has finished.
func eventsFeedAwaitReads(r *eventsFeedReader, n int) {
	for range n {
		<-r.reads
	}
}

// TestEventsWatchStreamFeedIdleReadsNothing proves that a stream whose feed reports no change reads the store only
// when it opens. The streams are closed as the barrier: each returns only after its wait ends, so any read it made
// while open is counted before the check.
func TestEventsWatchStreamFeedIdleReadsNothing(t *testing.T) {
	t.Parallel()
	rig := eventsFeedSetup(t)
	eventsFeedAddPane(t, rig.store)
	eventsFeedChange(t, rig.store, 1)
	const streams = 5
	opened := make([]*eventsFeedStream, 0, streams)
	for range streams {
		opened = append(opened, eventsFeedOpen(t, rig, 2))
	}
	eventsFeedAwaitReads(rig.reader, streams)
	for _, st := range opened {
		st.cancel()
		<-st.done
	}
	if got := rig.reader.count.Load(); got != streams {
		t.Fatalf("store reads: %d, want %d, one opening read per stream and none while idle", got, streams)
	}
}

// TestEventsWatchStreamFeedFiveCursorsEachGetEventOnce proves that streams at different cursors each receive an
// appended event exactly once, from the one change the feed reports for it.
func TestEventsWatchStreamFeedFiveCursorsEachGetEventOnce(t *testing.T) {
	t.Parallel()
	rig := eventsFeedSetup(t)
	eventsFeedAddPane(t, rig.store)
	for i := 1; i <= 4; i++ {
		eventsFeedChange(t, rig.store, i)
	}
	// Seqs 1 through 5 are stored. A stream at cursor c replays seqs c+1 through 5, then waits for a change.
	const newest = 5
	streams := make([]*eventsFeedStream, 0, newest)
	for after := range int64(newest) {
		st := eventsFeedOpen(t, rig, after)
		streams = append(streams, st)
		for seq := after + 1; seq <= newest; seq++ {
			eventsFeedWantSeq(t, st, seq)
		}
	}

	call := <-rig.changes.calls
	eventsFeedChange(t, rig.store, 5)
	call.msgs <- eventsFeedChangeMsg(6)
	for _, st := range streams {
		eventsFeedWantSeq(t, st, 6)
	}
	// Each stream has sent seq 6 once, so the next change brings seq 7 and no repeat of 6.
	eventsFeedChange(t, rig.store, 6)
	call.msgs <- eventsFeedChangeMsg(7)
	for _, st := range streams {
		eventsFeedWantSeq(t, st, 7)
	}
}

// TestEventsWatchStreamFeedOneSubscription proves that any number of open streams share one Changes.Watch call.
func TestEventsWatchStreamFeedOneSubscription(t *testing.T) {
	t.Parallel()
	rig := eventsFeedSetup(t)
	eventsFeedAddPane(t, rig.store)
	const streams = 5
	for range streams {
		eventsFeedOpen(t, rig, 1)
	}
	// Each stream reads after it subscribes, so five reads mean five subscriptions. The one call is announced once it
	// has started.
	eventsFeedAwaitReads(rig.reader, streams)
	<-rig.changes.calls
	if got := rig.changes.watches.Load(); got != 1 {
		t.Fatalf("Changes.Watch calls: %d, want 1 for %d open streams", got, streams)
	}
}

// TestEventsWatchStreamFeedReopenReadsOnce proves that when the feed's call ends, its retry timer reopens the call, and
// the reopened call's opened message makes each stream read once from its cursor. An event appended while the feed is
// down reaches each stream exactly once.
func TestEventsWatchStreamFeedReopenReadsOnce(t *testing.T) {
	t.Parallel()
	rig := eventsFeedSetup(t)
	eventsFeedAddPane(t, rig.store)
	eventsFeedChange(t, rig.store, 1)
	const streams = 2
	opened := make([]*eventsFeedStream, 0, streams)
	for range streams {
		opened = append(opened, eventsFeedOpen(t, rig, 2))
	}
	eventsFeedAwaitReads(rig.reader, streams)

	first := <-rig.changes.calls
	close(first.end)
	timer := <-rig.timers.armed
	if timer.after != feedRetry {
		t.Fatalf("retry timer armed for %v, want %v", timer.after, feedRetry)
	}
	// The feed is down, so this append wakes no stream.
	eventsFeedChange(t, rig.store, 2)
	timer.fire <- time.Time{}

	reopened := <-rig.changes.calls
	reopened.msgs <- &enginev1.ChangesWatchResponse{Opened: true}
	for _, st := range opened {
		eventsFeedWantSeq(t, st, 3)
	}
	if got, want := rig.reader.count.Load(), int64(2*streams); got != want {
		t.Fatalf("store reads: %d, want %d: one opening read per stream, then one read per stream after the reopen",
			got, want)
	}

	// Seq 3 was delivered once, so the next change brings seq 4 and no repeat of 3.
	eventsFeedChange(t, rig.store, 3)
	reopened.msgs <- eventsFeedChangeMsg(4)
	for _, st := range opened {
		eventsFeedWantSeq(t, st, 4)
	}
}
