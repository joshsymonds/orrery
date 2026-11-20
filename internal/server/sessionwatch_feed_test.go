package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	enginev1connect "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

const (
	// sessionFeedS and sessionFeedT are the two headless sessions the feed tests write and watch.
	sessionFeedS = "feed-s"
	sessionFeedT = "feed-t"
	// sessionFeedAt is the unix second the feed tests' events are stored at, and the second the clock starts at.
	sessionFeedAt = 1_700_000_000
	// sessionFeedBuffer is the capacity of the channel that announces each step the feed tests' streams take.
	sessionFeedBuffer = 64
)

// sessionFeedReads counts the steps the feed tests' streams take. Its stepped is the stepped seam of sessionsServer: it
// counts each step by session before the step reads the store, and announces the session on steps, so a test can wait
// for a step to begin.
type sessionFeedReads struct {
	mu     sync.Mutex
	counts map[string]int
	steps  chan string
}

// stepped counts a step of the session id and announces it.
func (r *sessionFeedReads) stepped(id string) {
	r.mu.Lock()
	r.counts[id]++
	r.mu.Unlock()
	r.steps <- id
}

// count returns how many steps of the session id have begun.
func (r *sessionFeedReads) count(id string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.counts[id]
}

// sessionFeedRig is the setup the session feed tests share: a store writer, the Changes service and timers the hub runs
// on, the clock the background window is read against, the step counts of the streams, and the URL of a server that
// serves Sessions and Events over one hub.
type sessionFeedRig struct {
	store   *store.Store
	clock   *atomic.Int64
	reads   *sessionFeedReads
	changes *eventsFeedChanges
	timers  *eventsFeedTimers
	url     string
}

// sessionFeedSetup returns a rig over a new store. Its servers close at cleanup.
func sessionFeedSetup(t *testing.T) *sessionFeedRig {
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
	changes := &eventsFeedChanges{calls: make(chan *eventsFeedCall, eventsFeedBuffer)}
	_, changesHandler := enginev1connect.NewChangesHandler(changes)
	changesSrv := httptest.NewServer(changesHandler)
	t.Cleanup(changesSrv.Close)
	timers := &eventsFeedTimers{armed: make(chan eventsFeedTimer, eventsFeedBuffer)}
	hub := newChangeHub(enginev1connect.NewChangesClient(http.DefaultClient, changesSrv.URL), timers.newTimer)
	clock := &atomic.Int64{}
	clock.Store(sessionFeedAt)
	reads := &sessionFeedReads{counts: map[string]int{}, steps: make(chan string, sessionFeedBuffer)}
	sessionsPath, sessionsHandler := orreryv1connect.NewSessionsHandler(sessionsServer{
		reader:      store.NewReader(path),
		now:         func() time.Time { return time.Unix(clock.Load(), 0) },
		changes:     hub,
		newTimer:    timers.newTimer,
		keepalive:   defaultConversationKeepalive,
		sendTimeout: defaultEventSendTimeout,
		stepped:     reads.stepped,
	})
	eventsPath, eventsHandler := orreryv1connect.NewEventsHandler(eventsServer{
		reader: store.NewReader(path), changes: hub, sendTimeout: defaultEventSendTimeout,
	})
	mux := http.NewServeMux()
	mux.Handle(sessionsPath, sessionsHandler)
	mux.Handle(eventsPath, eventsHandler)
	srv := httptest.NewServer(withResponseWriter(mux))
	t.Cleanup(srv.Close)
	return &sessionFeedRig{store: s, clock: clock, reads: reads, changes: changes, timers: timers, url: srv.URL}
}

// sessionFeedApply applies ops to the store in one write, stamped at the unix second at.
func sessionFeedApply(t *testing.T, s *store.Store, at int64, ops ...store.EngineOp) {
	t.Helper()
	if err := s.ApplyEngine(t.Context(), ops, time.Unix(at, 0)); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
}

// sessionFeedAddSession writes the headless session id in dir, stamped at sessionFeedAt.
func sessionFeedAddSession(t *testing.T, s *store.Store, id, dir string) {
	t.Helper()
	sessionFeedApply(t, s, sessionFeedAt, store.EngineAdd{ID: id, Dir: dir, State: "idle"})
}

// sessionFeedReply appends reply n of sessionFeedS, stamped at sessionFeedAt.
func sessionFeedReply(t *testing.T, s *store.Store, n int) {
	t.Helper()
	sessionFeedApply(t, s, sessionFeedAt, store.EngineBlock{
		ID:        sessionFeedS,
		Kind:      "text",
		Role:      "assistant",
		MessageID: fmt.Sprintf("msg-%d", n),
		Text:      fmt.Sprintf("reply %d", n),
	})
}

// sessionFeedReplyItem is the conversation item of reply n, as sessionFeedReply writes it.
func sessionFeedReplyItem(n int) *orreryv1.ConversationItem {
	return &orreryv1.ConversationItem{Kind: "reply", Time: sessionFeedAt, Text: fmt.Sprintf("reply %d", n)}
}

// sessionFeedHeadless is the headless session of the feed tests as sessionFeedAddSession writes it: labeled label, in
// dir, and changed at sessionFeedAt.
func sessionFeedHeadless(id, dir, label string) *orreryv1.HeadlessSession {
	return &orreryv1.HeadlessSession{Id: id, Dir: dir, Label: label, State: "idle", Changed: sessionFeedAt}
}

// sessionFeedChange is the message the change feed sends after a write appended events through seq to the sessions.
func sessionFeedChange(seq int64, sessions ...string) *enginev1.ChangesWatchResponse {
	return &enginev1.ChangesWatchResponse{Seq: seq, Sessions: sessions}
}

// sessionFeedStream is one WatchConversation stream the feed tests open. msgs carries each message and is closed when
// the stream ends, err is the reason it ended, and done closes once the goroutine that reads the stream has returned.
type sessionFeedStream struct {
	msgs   chan *orreryv1.SessionsWatchConversationResponse
	done   chan struct{}
	cancel context.CancelFunc
	err    error
}

// sessionFeedOpen opens WatchConversation for the session id on the rig's server. The stream ends at cleanup.
func sessionFeedOpen(t *testing.T, rig *sessionFeedRig, id string) *sessionFeedStream {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	st := &sessionFeedStream{
		msgs: make(chan *orreryv1.SessionsWatchConversationResponse), done: make(chan struct{}), cancel: cancel,
	}
	sessions := orreryv1connect.NewSessionsClient(http.DefaultClient, rig.url)
	go st.run(ctx, sessions, id)
	t.Cleanup(func() {
		cancel()
		<-st.done
	})
	return st
}

// run forwards the stream's messages to msgs, then closes msgs once err holds why the stream ended.
func (st *sessionFeedStream) run(ctx context.Context, sessions orreryv1connect.SessionsClient, id string) {
	defer close(st.done)
	st.err = st.forward(ctx, sessions, id)
	close(st.msgs)
}

// forward sends each message of the stream to msgs until the stream ends or ctx ends, and returns why it ended.
func (st *sessionFeedStream) forward(ctx context.Context, sessions orreryv1connect.SessionsClient, id string) error {
	stream, err := sessions.WatchConversation(
		ctx,
		connect.NewRequest(&orreryv1.SessionsWatchConversationRequest{Id: id}),
	)
	if err != nil {
		return fmt.Errorf("open WatchConversation: %w", err)
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

// sessionFeedNext returns the next message of st, failing the test when the stream ends first.
func sessionFeedNext(t *testing.T, st *sessionFeedStream) *orreryv1.SessionsWatchConversationResponse {
	t.Helper()
	msg, ok := <-st.msgs
	if !ok {
		t.Fatalf("stream ended before the next message: %v", st.err)
	}
	return msg
}

// sessionFeedWant fails the test unless the next message of st is want.
func sessionFeedWant(t *testing.T, st *sessionFeedStream, want *orreryv1.SessionsWatchConversationResponse) {
	t.Helper()
	if got := sessionFeedNext(t, st); !proto.Equal(got, want) {
		t.Fatalf("message: got %v, want %v", got, want)
	}
}

// sessionFeedNoMessage fails the test if st has a message ready. It does not wait for one.
func sessionFeedNoMessage(t *testing.T, st *sessionFeedStream) {
	t.Helper()
	select {
	case msg := <-st.msgs:
		t.Fatalf("stream sent %v, want no message", msg)
	default:
	}
}

// sessionFeedAwaitSteps receives the steps of the two streams a test opens, so the test knows each has begun its read.
func sessionFeedAwaitSteps(rig *sessionFeedRig) {
	for range 2 {
		<-rig.reads.steps
	}
}

// sessionFeedAwaitTimer returns the next timer the server arms for after. The timers it arms for other durations before
// it are dropped.
func sessionFeedAwaitTimer(rig *sessionFeedRig, after time.Duration) eventsFeedTimer {
	for {
		timer := <-rig.timers.armed
		if timer.after == after {
			return timer
		}
	}
}

// sessionFeedShellTask is the engine_task op of the shell task the background test runs, with the given status.
func sessionFeedShellTask(status string) store.EngineTask {
	return store.EngineTask{
		ID: sessionFeedS, TaskID: "task-shell", ToolUseID: "tool-shell", Description: "Run the build", Type: "shell",
		Status: status,
	}
}

// TestSessionWatchStreamFeedIdleReadsNothing proves that streams whose sessions the feed never names read only when
// they open and when the feed's opened message wakes them. Nothing is published and no timer fires after that, so no
// further read happens. The streams are closed as the barrier: each returns only after its wait ends, so any read it
// made while open is counted before the check.
func TestSessionWatchStreamFeedIdleReadsNothing(t *testing.T) {
	t.Parallel()
	rig := sessionFeedSetup(t)
	sessionFeedAddSession(t, rig.store, sessionFeedS, "/s")
	sessionFeedAddSession(t, rig.store, sessionFeedT, "/t")
	s := sessionFeedOpen(t, rig, sessionFeedS)
	tt := sessionFeedOpen(t, rig, sessionFeedT)
	sessionFeedAwaitSteps(rig)

	call := <-rig.changes.calls
	call.msgs <- &enginev1.ChangesWatchResponse{Opened: true}
	sessionFeedAwaitSteps(rig)
	for _, st := range []*sessionFeedStream{s, tt} {
		st.cancel()
		<-st.done
	}
	for _, id := range []string{sessionFeedS, sessionFeedT} {
		if got := rig.reads.count(id); got != 2 {
			t.Fatalf("reads of %s: %d, want 2, one opening read and one read for the opened message", id, got)
		}
	}
}

// TestSessionWatchStreamFeedWakesOnlyItsSession proves that a change to one session wakes only the stream of that
// session: its stream reads once and sends the new item, and the other stream reads nothing and sends nothing.
func TestSessionWatchStreamFeedWakesOnlyItsSession(t *testing.T) {
	t.Parallel()
	rig := sessionFeedSetup(t)
	sessionFeedAddSession(t, rig.store, sessionFeedS, "/s")
	sessionFeedAddSession(t, rig.store, sessionFeedT, "/t")
	s := sessionFeedOpen(t, rig, sessionFeedS)
	tt := sessionFeedOpen(t, rig, sessionFeedT)
	sessionFeedWant(t, s, &orreryv1.SessionsWatchConversationResponse{
		Transcript: sessionFeedS, Seq: 1, Reset_: true,
		State: &orreryv1.ConversationState{Session: sessionFeedHeadless(sessionFeedS, "/s", "s")},
	})
	sessionFeedWant(t, tt, &orreryv1.SessionsWatchConversationResponse{
		Transcript: sessionFeedT, Seq: 2, Reset_: true,
		State: &orreryv1.ConversationState{Session: sessionFeedHeadless(sessionFeedT, "/t", "t")},
	})

	call := <-rig.changes.calls
	sessionFeedReply(t, rig.store, 1)
	call.msgs <- sessionFeedChange(3, sessionFeedS)
	sessionFeedWant(t, s, &orreryv1.SessionsWatchConversationResponse{
		Transcript: sessionFeedS, Seq: 3, Items: []*orreryv1.ConversationItem{sessionFeedReplyItem(1)},
	})
	// The message names only S, so the feed wakes no other stream: T's only read is its opening one, and it sends nothing.
	if got := rig.reads.count(sessionFeedS); got != 2 {
		t.Fatalf("reads of %s: %d, want 2, one opening read and one for the change", sessionFeedS, got)
	}
	if got := rig.reads.count(sessionFeedT); got != 1 {
		t.Fatalf("reads of %s: %d, want 1, its opening read only", sessionFeedT, got)
	}
	sessionFeedNoMessage(t, tt)
}

// TestSessionWatchStreamFeedReopenReadsOnce proves that when the feed's call ends, its retry timer reopens the call,
// and the reopened call's opened message makes each stream read once. A change appended while the feed is down reaches
// its stream exactly once.
func TestSessionWatchStreamFeedReopenReadsOnce(t *testing.T) {
	t.Parallel()
	rig := sessionFeedSetup(t)
	sessionFeedAddSession(t, rig.store, sessionFeedS, "/s")
	sessionFeedAddSession(t, rig.store, sessionFeedT, "/t")
	s := sessionFeedOpen(t, rig, sessionFeedS)
	tt := sessionFeedOpen(t, rig, sessionFeedT)
	sessionFeedWant(t, s, &orreryv1.SessionsWatchConversationResponse{
		Transcript: sessionFeedS, Seq: 1, Reset_: true,
		State: &orreryv1.ConversationState{Session: sessionFeedHeadless(sessionFeedS, "/s", "s")},
	})
	sessionFeedWant(t, tt, &orreryv1.SessionsWatchConversationResponse{
		Transcript: sessionFeedT, Seq: 2, Reset_: true,
		State: &orreryv1.ConversationState{Session: sessionFeedHeadless(sessionFeedT, "/t", "t")},
	})
	sessionFeedAwaitSteps(rig)

	first := <-rig.changes.calls
	close(first.end)
	retry := sessionFeedAwaitTimer(rig, feedRetry)
	// The feed is down, so this append wakes no stream.
	sessionFeedReply(t, rig.store, 1)
	retry.fire <- time.Time{}

	reopened := <-rig.changes.calls
	reopened.msgs <- &enginev1.ChangesWatchResponse{Opened: true}
	sessionFeedAwaitSteps(rig)
	for _, id := range []string{sessionFeedS, sessionFeedT} {
		if got := rig.reads.count(id); got != 2 {
			t.Fatalf("reads of %s: %d, want 2, one opening read and one read after the reopen", id, got)
		}
	}
	sessionFeedWant(t, s, &orreryv1.SessionsWatchConversationResponse{
		Transcript: sessionFeedS, Seq: 3, Items: []*orreryv1.ConversationItem{sessionFeedReplyItem(1)},
	})
	sessionFeedNoMessage(t, tt)

	// Reply 1 was delivered once, so the next change brings reply 2 and no repeat of reply 1.
	sessionFeedReply(t, rig.store, 2)
	reopened.msgs <- sessionFeedChange(4, sessionFeedS)
	sessionFeedWant(t, s, &orreryv1.SessionsWatchConversationResponse{
		Transcript: sessionFeedS, Seq: 4, Items: []*orreryv1.ConversationItem{sessionFeedReplyItem(2)},
	})
}

// TestSessionWatchStreamFeedKeepaliveDeadline proves that a stream sends no keepalive before its keepalive timer fires,
// that the timer is armed for the keepalive duration after each message, and that the keepalive, when the timer fires,
// carries the transcript, seq, unfinished and queued sets and makes no read.
func TestSessionWatchStreamFeedKeepaliveDeadline(t *testing.T) {
	t.Parallel()
	rig := sessionFeedSetup(t)
	sessionFeedAddSession(t, rig.store, sessionFeedS, "/s")
	s := sessionFeedOpen(t, rig, sessionFeedS)
	sessionFeedWant(t, s, &orreryv1.SessionsWatchConversationResponse{
		Transcript: sessionFeedS, Seq: 1, Reset_: true,
		State: &orreryv1.ConversationState{Session: sessionFeedHeadless(sessionFeedS, "/s", "s")},
	})
	if timer := <-rig.timers.armed; timer.after != defaultConversationKeepalive {
		t.Fatalf("keepalive timer armed for %v, want %v", timer.after, defaultConversationKeepalive)
	}

	// The next message is a delta, not a keepalive, so the stream sent none while its timer was armed.
	sessionFeedReply(t, rig.store, 1)
	call := <-rig.changes.calls
	call.msgs <- sessionFeedChange(2, sessionFeedS)
	sessionFeedWant(t, s, &orreryv1.SessionsWatchConversationResponse{
		Transcript: sessionFeedS, Seq: 2, Items: []*orreryv1.ConversationItem{sessionFeedReplyItem(1)},
	})
	timer := <-rig.timers.armed
	if timer.after != defaultConversationKeepalive {
		t.Fatalf("keepalive timer re-armed for %v, want %v", timer.after, defaultConversationKeepalive)
	}

	timer.fire <- time.Time{}
	sessionFeedWant(t, s, &orreryv1.SessionsWatchConversationResponse{Transcript: sessionFeedS, Seq: 2})
	if got := rig.reads.count(sessionFeedS); got != 2 {
		t.Fatalf(
			"reads of %s: %d, want 2: the opening read and the read for the change, none for the keepalive",
			sessionFeedS,
			got,
		)
	}
}

// TestSessionWatchStreamFeedBackgroundWindowDeadline proves that a stream whose ended background task is shown arms
// its background timer for the moment the task's window closes, and that when the timer fires the stream reads once
// and sends a delta without the task.
func TestSessionWatchStreamFeedBackgroundWindowDeadline(t *testing.T) {
	t.Parallel()
	rig := sessionFeedSetup(t)
	const ended = sessionFeedAt + 10
	sessionFeedAddSession(t, rig.store, sessionFeedS, "/s")
	sessionFeedApply(t, rig.store, sessionFeedAt, sessionFeedShellTask("running"))
	sessionFeedApply(t, rig.store, ended, sessionFeedShellTask("done"))
	rig.clock.Store(ended + 60)

	s := sessionFeedOpen(t, rig, sessionFeedS)
	sessionFeedWant(t, s, &orreryv1.SessionsWatchConversationResponse{
		Transcript: sessionFeedS, Seq: 3, Reset_: true,
		State: &orreryv1.ConversationState{
			Session: sessionFeedHeadless(sessionFeedS, "/s", "s"),
			Background: []*orreryv1.BackgroundTask{{
				Id: "task-shell", ToolUseId: "tool-shell", Description: "Run the build", Type: "shell",
				Status: "done", Started: sessionFeedAt, Ended: ended,
			}},
		},
	})

	// The clock stands at ended+60, so the window closes at ended+61: one second from now.
	background := sessionFeedAwaitTimer(rig, time.Second)
	rig.clock.Store(ended + 61)
	background.fire <- time.Time{}
	sessionFeedWant(t, s, &orreryv1.SessionsWatchConversationResponse{
		Transcript: sessionFeedS, Seq: 3,
		State: &orreryv1.ConversationState{Session: sessionFeedHeadless(sessionFeedS, "/s", "s")},
	})
	if got := rig.reads.count(sessionFeedS); got != 2 {
		t.Fatalf("reads of %s: %d, want 2: the opening read and one read when the window closes", sessionFeedS, got)
	}
}

// TestSessionWatchStreamFeedOneSubscription proves that any number of session streams and Events.Watch streams on the
// server share one Changes.Watch call.
func TestSessionWatchStreamFeedOneSubscription(t *testing.T) {
	t.Parallel()
	rig := sessionFeedSetup(t)
	sessionFeedAddSession(t, rig.store, sessionFeedS, "/s")
	sessionFeedAddSession(t, rig.store, sessionFeedT, "/t")

	// Each stream's first message follows its subscription, so once each has sent one, all of them are subscribed.
	// The events stream reads the log from its start, so its first two messages are the two sessions' adds.
	for _, id := range []string{sessionFeedS, sessionFeedS, sessionFeedT} {
		sessionFeedNext(t, sessionFeedOpen(t, rig, id))
	}
	events := orreryv1connect.NewEventsClient(http.DefaultClient, rig.url)
	ctx, cancel := context.WithCancel(t.Context())
	stream, err := events.Watch(ctx, connect.NewRequest(&orreryv1.WatchRequest{After: 0}))
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	t.Cleanup(cancel)
	for want := int64(1); want <= 2; want++ {
		if !stream.Receive() {
			t.Fatalf("events stream ended: %v", stream.Err())
		}
		if got := stream.Msg().GetSeq(); got != want {
			t.Fatalf("event seq %d, want %d", got, want)
		}
	}

	<-rig.changes.calls
	if got := rig.changes.watches.Load(); got != 1 {
		t.Fatalf("Changes.Watch calls: %d, want 1 for three session streams and an events stream", got)
	}
}
