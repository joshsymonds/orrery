package server_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/server"
	"github.com/joshsymonds/orrery/internal/store"
)

const (
	// eventsWatchPath is the Connect path of Events.Watch.
	eventsWatchPath = "/" + orreryv1connect.EventsName + "/Watch"
	// eventAt is the unix second every test event is stored at.
	eventAt = 1_700_000_000
	// eventPoll is the Watch poll interval the tests inject.
	eventPoll = 10 * time.Millisecond
	// watchWait bounds every wait on a stream, so a stalled stream fails its test instead of hanging the run.
	watchWait = 10 * time.Second
	// replayCount is more events than one page holds (eventPageSize is 100), so a replay reads several pages.
	replayCount = 250
	// eventPane is the row id of the pane every test event is about.
	eventPane = "orrery-1"
	// eventEngine is the row id of the engine session the engine-event tests write.
	eventEngine = "engine-1"
)

// guardedHandler returns the guarded handler that admits allowedLogin and reads events and sessions from the
// store at path, polling every eventPoll.
func guardedHandler(t *testing.T, path string) http.Handler {
	t.Helper()
	handler, err := server.New(server.Config{
		Logins:    []string{allowedLogin},
		Tmux:      newTmuxClient(t),
		Push:      newPushStore(t),
		Sessions:  store.NewReader(path),
		EventPoll: eventPoll,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return handler
}

// startServer serves guardedHandler for the store at path on an httptest server that closes at cleanup, and
// returns its URL.
func startServer(t *testing.T, path string) string {
	t.Helper()
	srv := httptest.NewServer(guardedHandler(t, path))
	t.Cleanup(srv.Close)
	return srv.URL
}

// guardedHTTPClient returns an HTTP client whose requests carry the login and client headers the guard requires.
func guardedHTTPClient() *http.Client {
	return &http.Client{Transport: headerTransport{base: http.DefaultTransport, login: allowedLogin}}
}

// openEventStore opens the writer for the store at path and closes it at cleanup.
func openEventStore(t *testing.T, path string) *store.Store {
	t.Helper()
	s, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close store: %v", err)
		}
	})
	return s
}

// writeState changes the state of eventPane to state-i, which appends one session_changed event.
func writeState(t *testing.T, s *store.Store, i int) {
	t.Helper()
	pane := store.Pane{ID: eventPane, Pane: "%1", Fields: store.Fields{State: fmt.Sprintf("state-%d", i)}}
	if err := s.SyncPane(t.Context(), "-L a", pane, time.Unix(eventAt, 0)); err != nil {
		t.Fatalf("SyncPane state-%d: %v", i, err)
	}
}

// seedEvents writes the row of eventPane, which appends one session_added event, then count state changes. The
// store then holds count+1 events with seqs 1 through count+1.
func seedEvents(t *testing.T, s *store.Store, count int) {
	t.Helper()
	pane := store.Pane{ID: eventPane, Pane: "%1"}
	if err := s.SyncServer(t.Context(), "-L a", []store.Pane{pane}, time.Unix(eventAt, 0)); err != nil {
		t.Fatalf("SyncServer: %v", err)
	}
	for i := 1; i <= count; i++ {
		writeState(t, s, i)
	}
}

// watch runs one Watch call and reads its stream on one goroutine. The generated client's Watch call returns only once
// the server sends a message or ends the stream, so the call itself runs on that goroutine, and a test waits on the
// stream with a deadline.
type watch struct {
	stream *connect.ServerStreamForClient[orreryv1.WatchResponse]
	msgs   chan *orreryv1.WatchResponse
	done   chan struct{}
	err    error
}

// openWatch starts Watch after the cursor. The stream's context is canceled at cleanup, and the stream is closed
// once its reader has exited.
func openWatch(t *testing.T, events orreryv1connect.EventsClient, after int64) *watch {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	w := &watch{msgs: make(chan *orreryv1.WatchResponse), done: make(chan struct{})}
	go w.run(ctx, events, after)
	t.Cleanup(func() {
		cancel()
		<-w.done
		if w.stream == nil {
			return
		}
		if err := w.stream.Close(); err != nil {
			t.Errorf("close Watch stream: %v", err)
		}
	})
	return w
}

// run calls Watch after the cursor, then forwards each received message to msgs until the stream ends or ctx is
// canceled. It records the error the call or the stream ended with.
func (w *watch) run(ctx context.Context, events orreryv1connect.EventsClient, after int64) {
	defer close(w.done)
	defer close(w.msgs)
	stream, err := events.Watch(ctx, connect.NewRequest(&orreryv1.WatchRequest{After: after}))
	if err != nil {
		w.err = err
		return
	}
	w.stream = stream
	for stream.Receive() {
		select {
		case w.msgs <- stream.Msg():
		case <-ctx.Done():
			return
		}
	}
	w.err = stream.Err()
}

// next returns the next message, failing the test when none arrives within watchWait or the stream ends first.
func (w *watch) next(t *testing.T) *orreryv1.WatchResponse {
	t.Helper()
	select {
	case msg, ok := <-w.msgs:
		if !ok {
			t.Fatalf("stream ended before the next message: %v", w.err)
		}
		return msg
	case <-time.After(watchWait):
		t.Fatalf("no message within %v", watchWait)
	}
	return nil
}

// finish waits for the stream to end without sending another message, and returns the error it ended with.
func (w *watch) finish(t *testing.T) error {
	t.Helper()
	select {
	case msg, ok := <-w.msgs:
		if ok {
			t.Fatalf("stream sent %v, want it to end", msg)
		}
	case <-time.After(watchWait):
		t.Fatalf("stream did not end within %v", watchWait)
	}
	return w.err
}

// wantCode fails the test unless err carries the Connect code want.
func wantCode(t *testing.T, err error, want connect.Code) {
	t.Helper()
	if got := connect.CodeOf(err); got != want {
		t.Fatalf("stream error code: got %v (%v), want %v", got, err, want)
	}
}

// optional returns a pointer to value, the presence a SessionChanged field needs when it is set.
func optional(value string) *string {
	return &value
}

func TestWatchReplaysMoreThanOnePageThenFollows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	seedEvents(t, s, replayCount)
	events := orreryv1connect.NewEventsClient(guardedHTTPClient(), startServer(t, path))

	w := openWatch(t, events, 0)
	for i := range replayCount + 1 {
		msg := w.next(t)
		if msg.GetSeq() != int64(i+1) {
			t.Fatalf("replayed message %d: seq %d, want %d", i, msg.GetSeq(), i+1)
		}
	}

	writeState(t, s, replayCount+1)
	msg := w.next(t)
	if want := int64(replayCount + 2); msg.GetSeq() != want {
		t.Fatalf("followed message: seq %d, want %d", msg.GetSeq(), want)
	}
	if got := msg.GetSessionChanged().GetState(); got != fmt.Sprintf("state-%d", replayCount+1) {
		t.Fatalf("followed message state: got %q", got)
	}
}

func TestWatchAfterCursorResumesAtNextSeq(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	seedEvents(t, s, 5)
	events := orreryv1connect.NewEventsClient(guardedHTTPClient(), startServer(t, path))

	w := openWatch(t, events, 3)
	for _, want := range []int64{4, 5, 6} {
		if got := w.next(t).GetSeq(); got != want {
			t.Fatalf("resumed message: seq %d, want %d", got, want)
		}
	}

	writeState(t, s, 6)
	if got := w.next(t).GetSeq(); got != 7 {
		t.Fatalf("message after the replay: seq %d, want 7 with nothing earlier", got)
	}
}

func TestWatchAfterListReceivesOnlyLaterWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	seedEvents(t, s, 3)
	url := startServer(t, path)
	sessions := orreryv1connect.NewSessionsClient(guardedHTTPClient(), url)
	events := orreryv1connect.NewEventsClient(guardedHTTPClient(), url)

	list, err := sessions.List(t.Context(), connect.NewRequest(&orreryv1.ListRequest{}))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if list.Msg.GetSeq() != 4 {
		t.Fatalf("List seq: got %d, want 4", list.Msg.GetSeq())
	}

	w := openWatch(t, events, list.Msg.GetSeq())
	writeState(t, s, 4)
	if got := w.next(t).GetSeq(); got != 5 {
		t.Fatalf("first message after List: seq %d, want 5 with nothing earlier", got)
	}
}

func TestWatchRefusesCursorPastEnd(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	seedEvents(t, s, 2)
	events := orreryv1connect.NewEventsClient(guardedHTTPClient(), startServer(t, path))

	w := openWatch(t, events, 4)
	wantCode(t, w.finish(t), connect.CodeOutOfRange)
}

func TestWatchRefusesCursorOlderThanPrunedLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	seedEvents(t, s, 3)
	// A sync a retention period and an hour later prunes every event written at eventAt.
	later := time.Unix(eventAt, 0).Add(store.EventRetention + time.Hour)
	pane := store.Pane{ID: eventPane, Pane: "%1", Fields: store.Fields{State: "fresh"}}
	if err := s.SyncServer(t.Context(), "-L a", []store.Pane{pane}, later); err != nil {
		t.Fatalf("SyncServer after retention: %v", err)
	}
	events := orreryv1connect.NewEventsClient(guardedHTTPClient(), startServer(t, path))

	w := openWatch(t, events, 0)
	wantCode(t, w.finish(t), connect.CodeOutOfRange)
}

func TestWatchDecodesEachKind(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	ctx := t.Context()
	at := time.Unix(eventAt, 0)
	if err := s.SyncServer(ctx, "-L a", []store.Pane{{ID: eventPane, Pane: "%1"}}, at); err != nil {
		t.Fatalf("SyncServer added: %v", err)
	}
	if err := s.SyncPane(ctx, "-L a", store.Pane{
		ID: eventPane, Pane: "%1", Fields: store.Fields{State: "working", Label: "one"},
	}, at); err != nil {
		t.Fatalf("SyncPane both fields: %v", err)
	}
	if err := s.SyncPane(ctx, "-L a", store.Pane{
		ID: eventPane, Pane: "%1", Fields: store.Fields{State: "idle", Label: "one"},
	}, at); err != nil {
		t.Fatalf("SyncPane state only: %v", err)
	}
	if err := s.SyncPane(ctx, "-L a", store.Pane{
		ID: eventPane, Pane: "%1", Fields: store.Fields{State: "idle"},
	}, at); err != nil {
		t.Fatalf("SyncPane label cleared: %v", err)
	}
	if err := s.SyncServer(ctx, "-L a", nil, at); err != nil {
		t.Fatalf("SyncServer removed: %v", err)
	}
	events := orreryv1connect.NewEventsClient(guardedHTTPClient(), startServer(t, path))

	want := []*orreryv1.WatchResponse{
		{Seq: 1, SessionId: eventPane, At: eventAt, Event: &orreryv1.WatchResponse_SessionAdded{
			SessionAdded: &orreryv1.SessionAdded{Server: "-L a", Pane: "%1"},
		}},
		{Seq: 2, SessionId: eventPane, At: eventAt, Event: &orreryv1.WatchResponse_SessionChanged{
			SessionChanged: &orreryv1.SessionChanged{State: optional("working"), Label: optional("one")},
		}},
		{Seq: 3, SessionId: eventPane, At: eventAt, Event: &orreryv1.WatchResponse_SessionChanged{
			SessionChanged: &orreryv1.SessionChanged{State: optional("idle")},
		}},
		{Seq: 4, SessionId: eventPane, At: eventAt, Event: &orreryv1.WatchResponse_SessionChanged{
			SessionChanged: &orreryv1.SessionChanged{Label: optional("")},
		}},
		{Seq: 5, SessionId: eventPane, At: eventAt, Event: &orreryv1.WatchResponse_SessionRemoved{
			SessionRemoved: &orreryv1.SessionRemoved{},
		}},
	}
	w := openWatch(t, events, 0)
	for i, wantMsg := range want {
		if got := w.next(t); !proto.Equal(got, wantMsg) {
			t.Fatalf("event %d: got %v, want %v", i+1, got, wantMsg)
		}
	}
}

// addEngine writes engine session eventEngine, which appends one engine_added event.
func addEngine(t *testing.T, s *store.Store) {
	t.Helper()
	ops := []store.EngineOp{store.EngineAdd{ID: eventEngine, Dir: "/a", State: "idle"}}
	if err := s.ApplyEngine(t.Context(), ops, time.Unix(eventAt, 0)); err != nil {
		t.Fatalf("ApplyEngine add: %v", err)
	}
}

// promptEngine appends count engine_prompt events for eventEngine in one store write.
func promptEngine(t *testing.T, s *store.Store, count int) {
	t.Helper()
	ops := make([]store.EngineOp, 0, count)
	for range count {
		ops = append(ops, store.EnginePrompt{ID: eventEngine, Text: "hi"})
	}
	if err := s.ApplyEngine(t.Context(), ops, time.Unix(eventAt, 0)); err != nil {
		t.Fatalf("ApplyEngine prompts: %v", err)
	}
}

func TestWatchSendsEngineEventsBetweenPaneEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	seedEvents(t, s, 0)
	addEngine(t, s)
	writeState(t, s, 1)
	promptEngine(t, s, 2)
	writeState(t, s, 2)
	events := orreryv1connect.NewEventsClient(guardedHTTPClient(), startServer(t, path))

	at := int64(eventAt)
	paneAdded := &orreryv1.WatchResponse{
		Seq:       1,
		SessionId: eventPane,
		At:        at,
		Event: &orreryv1.WatchResponse_SessionAdded{
			SessionAdded: &orreryv1.SessionAdded{Server: "-L a", Pane: "%1"},
		},
	}
	engineAdded := &orreryv1.WatchResponse{
		Seq:       2,
		SessionId: eventEngine,
		At:        at,
		Event: &orreryv1.WatchResponse_EngineAdded{
			EngineAdded: &orreryv1.EngineAdded{Dir: "/a", State: "idle"},
		},
	}
	paneState := func(seq int64, state string) *orreryv1.WatchResponse {
		return &orreryv1.WatchResponse{
			Seq:       seq,
			SessionId: eventPane,
			At:        at,
			Event: &orreryv1.WatchResponse_SessionChanged{
				SessionChanged: &orreryv1.SessionChanged{State: optional(state)},
			},
		}
	}
	prompt := func(seq int64) *orreryv1.WatchResponse {
		return &orreryv1.WatchResponse{
			Seq:       seq,
			SessionId: eventEngine,
			At:        at,
			Event: &orreryv1.WatchResponse_EnginePrompt{
				EnginePrompt: &orreryv1.EnginePrompt{Text: "hi"},
			},
		}
	}
	want := []*orreryv1.WatchResponse{
		paneAdded,
		engineAdded,
		paneState(3, "state-1"),
		prompt(4),
		prompt(5),
		paneState(6, "state-2"),
	}

	w := openWatch(t, events, 0)
	for _, wantMsg := range want {
		wantMessage(t, w.next(t), wantMsg)
	}
}

func TestWatchReachesPaneEventsAfterMoreThanOnePageOfEngineEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	seedEvents(t, s, 0)
	addEngine(t, s)
	promptEngine(t, s, replayCount)
	writeState(t, s, 1)
	events := orreryv1connect.NewEventsClient(guardedHTTPClient(), startServer(t, path))

	w := openWatch(t, events, 0)
	if got := w.next(t).GetSeq(); got != 1 {
		t.Fatalf("first message: seq %d, want 1", got)
	}
	// The engine events, the engine_added and replayCount prompts, are sent in seq order across more than one page.
	for want := int64(2); want <= replayCount+2; want++ {
		if got := w.next(t).GetSeq(); got != want {
			t.Fatalf("engine event: seq %d, want %d", got, want)
		}
	}
	if got := w.next(t).GetSeq(); got != replayCount+3 {
		t.Fatalf("pane event after engine events: seq %d, want %d", got, replayCount+3)
	}

	writeState(t, s, 2)
	if got := w.next(t).GetSeq(); got != replayCount+4 {
		t.Fatalf("followed pane event: seq %d, want %d", got, replayCount+4)
	}
}

func TestWatchUnavailableWhenStoreIsMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent", "orrery.db")
	events := orreryv1connect.NewEventsClient(guardedHTTPClient(), startServer(t, path))

	w := openWatch(t, events, 0)
	wantCode(t, w.finish(t), connect.CodeUnavailable)
}

func TestWatchUnavailableWhenStoreIsRemoved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	seq := seedSessionStore(t, path)
	events := orreryv1connect.NewEventsClient(guardedHTTPClient(), startServer(t, path))

	w := openWatch(t, events, 0)
	for range seq {
		w.next(t)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove store: %v", err)
	}
	wantCode(t, w.finish(t), connect.CodeUnavailable)
}

func TestWatchRequiresGuard(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	seedSessionStore(t, path)
	handler := guardedHandler(t, path)

	cases := []struct {
		name  string
		login string
		ctype string
		key   bool
	}{
		{name: "no login", login: "", ctype: "application/connect+proto", key: true},
		{name: "other login", login: otherLogin, ctype: "application/connect+proto", key: true},
		{name: "no client header", login: allowedLogin, ctype: "application/connect+proto", key: false},
		{name: "text content type", login: allowedLogin, ctype: "text/plain", key: true},
	}
	for _, tc := range cases {
		rec := serve(t, handler, http.MethodPost, eventsWatchPath,
			connectHeader(tc.login, tc.ctype, tc.key), "")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s: status = %d, want 403", tc.name, rec.Code)
		}
	}

	// The admitted request carries the same headers the refused ones lack. Its context is already canceled, so
	// the handler returns at once instead of streaming.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, eventsWatchPath, strings.NewReader(""))
	req.Header = connectHeader(allowedLogin, "application/connect+proto", true)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code == http.StatusForbidden {
		t.Fatal("admitted Watch request: status = 403, want the Connect handler to answer")
	}
}

func TestNewRejectsNegativeEventPoll(t *testing.T) {
	_, err := server.New(server.Config{
		Logins:    []string{allowedLogin},
		Tmux:      newTmuxClient(t),
		Push:      newPushStore(t),
		EventPoll: -time.Millisecond,
	})
	if err == nil {
		t.Fatal("New with a negative EventPoll: no error")
	}
}
