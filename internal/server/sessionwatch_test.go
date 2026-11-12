package server_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
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

// stepSessionID is the id of the step session the not-found test writes. Step rows are never listed or watched.
const stepSessionID = "step-1"

// sessionsHandler returns the guarded handler that admits allowedLogin and reads sessions from reader, or from no
// store when reader is nil. Its WatchConversation polls every convPoll, sends a keepalive after convKeepalive, ends
// its streams when shutdown closes, and bounds each send by sendTimeout. A nil shutdown never closes, and a zero
// sendTimeout takes the default.
func sessionsHandler(
	t *testing.T,
	reader *store.Reader,
	shutdown <-chan struct{},
	sendTimeout time.Duration,
) http.Handler {
	t.Helper()
	handler, err := server.New(server.Config{
		Logins:                []string{allowedLogin},
		Tmux:                  newTmuxClient(t),
		Push:                  newPushStore(t),
		Sessions:              reader,
		ConversationPoll:      convPoll,
		ConversationKeepalive: convKeepalive,
		EventSendTimeout:      sendTimeout,
		Shutdown:              shutdown,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return handler
}

// sessionsWatchClient serves handler on an httptest server and returns a Sessions client for it.
func sessionsWatchClient(t *testing.T, handler http.Handler) orreryv1connect.SessionsClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return orreryv1connect.NewSessionsClient(guardedHTTPClient(), srv.URL)
}

// sessionStream reads one Sessions.WatchConversation stream on its own goroutine. Messages reach msgs one at a time,
// so a test that stops reading leaves the stream unread. last is the newest message next returned.
type sessionStream struct {
	msgs chan *orreryv1.SessionsWatchConversationResponse
	done chan struct{}
	err  error
	last *orreryv1.SessionsWatchConversationResponse
}

// openSessionStream starts WatchConversation for req. The stream's context is canceled at cleanup, and the reader
// exits before the test ends.
func openSessionStream(
	t *testing.T,
	client orreryv1connect.SessionsClient,
	req *orreryv1.SessionsWatchConversationRequest,
) *sessionStream {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	s := &sessionStream{
		msgs: make(chan *orreryv1.SessionsWatchConversationResponse),
		done: make(chan struct{}),
	}
	go s.run(ctx, client, req)
	t.Cleanup(func() {
		cancel()
		<-s.done
	})
	return s
}

// run calls WatchConversation for req, then forwards each message to msgs until the stream ends or ctx is canceled.
func (s *sessionStream) run(
	ctx context.Context,
	client orreryv1connect.SessionsClient,
	req *orreryv1.SessionsWatchConversationRequest,
) {
	defer close(s.done)
	defer close(s.msgs)
	stream, err := client.WatchConversation(ctx, connect.NewRequest(req))
	if err != nil {
		s.err = err
		return
	}
	for stream.Receive() {
		select {
		case s.msgs <- stream.Msg():
		case <-ctx.Done():
			_ = stream.Close()
			return
		}
	}
	s.err = stream.Err()
	_ = stream.Close()
}

// next returns the next message, failing the test when none arrives within watchWait or the stream ends first.
func (s *sessionStream) next(t *testing.T) *orreryv1.SessionsWatchConversationResponse {
	t.Helper()
	select {
	case msg, ok := <-s.msgs:
		if !ok {
			t.Fatalf("stream ended before the next message: %v", s.err)
		}
		s.last = msg
		return msg
	case <-time.After(watchWait):
		t.Fatalf("no message within %v", watchWait)
	}
	return nil
}

// nextDelta returns the next message that is not a keepalive. A keepalive has no items, no state, and the unfinished
// set of the message before it, so the stream sends one when no delta is due. The skipped keepalives share one
// watchWait: a stream that only ever sends keepalives fails the test instead of holding it.
func (s *sessionStream) nextDelta(t *testing.T) *orreryv1.SessionsWatchConversationResponse {
	t.Helper()
	deadline := time.Now().Add(watchWait)
	for {
		prev := s.last
		msg := s.next(t)
		keepalive := len(msg.GetItems()) == 0 && msg.GetState() == nil &&
			slices.EqualFunc(msg.GetUnfinished(), prev.GetUnfinished(), func(a, b *orreryv1.EngineText) bool {
				return proto.Equal(a, b)
			})
		if !keepalive {
			return msg
		}
		if time.Now().After(deadline) {
			t.Fatalf("no delta within %v, only keepalives", watchWait)
		}
	}
}

// end waits for the stream to end without sending another message, and returns the error it ended with.
func (s *sessionStream) end(t *testing.T) error {
	t.Helper()
	select {
	case msg, ok := <-s.msgs:
		if ok {
			t.Fatalf("stream sent %v, want it to end", msg)
		}
	case <-time.After(watchWait):
		t.Fatalf("stream did not end within %v", watchWait)
	}
	return s.err
}

// wantStreamMessage fails the test unless got equals want.
func wantStreamMessage(t *testing.T, got, want *orreryv1.SessionsWatchConversationResponse) {
	t.Helper()
	if !proto.Equal(got, want) {
		t.Fatalf("message: got %v, want %v", got, want)
	}
}

// sessionReq is the WatchConversation request for the engine session of the tests.
func sessionReq(id string, after int64, limit uint32) *orreryv1.SessionsWatchConversationRequest {
	return &orreryv1.SessionsWatchConversationRequest{Id: id, Limit: limit, After: after}
}

// replyBlock is a finished assistant text block of eventEngine whose text is "reply n".
func replyBlock(n int) store.EngineBlock {
	return store.EngineBlock{
		ID: eventEngine, Kind: "text", Role: "assistant", MessageID: fmt.Sprintf("msg-%d", n),
		Text: fmt.Sprintf("reply %d", n),
	}
}

// reply is the conversation item replyBlock(n) shows.
func reply(n int) *orreryv1.ConversationItem {
	return &orreryv1.ConversationItem{Kind: "reply", Time: eventAt, Text: fmt.Sprintf("reply %d", n)}
}

// engineSession is the headless session of eventEngine as addEngine writes it, in the given state.
func engineSession(state string) *orreryv1.HeadlessSession {
	return &orreryv1.HeadlessSession{
		Id: eventEngine, Dir: "/a", Label: "a · " + eventEngine, State: state, Changed: eventAt,
	}
}

// seedSessionReplies writes eventEngine with a prompt "fix the bug" and then replies 1 through count. The store then
// holds the prompt at seq 2 and reply n at seq n+2.
func seedSessionReplies(t *testing.T, s *store.Store, count int) {
	t.Helper()
	ops := []store.EngineOp{store.EnginePrompt{ID: eventEngine, Text: "fix the bug"}}
	for n := 1; n <= count; n++ {
		ops = append(ops, replyBlock(n))
	}
	applyEngineOps(t, s, ops...)
}

// TestSessionWatchFirstDeltaResetsWithNewestItems opens the stream at seq 0 with a limit of 2 over a session of
// three replies. The first delta is a reset with the two newest replies, the whole state, and the seq of the newest
// event.
func TestSessionWatchFirstDeltaResetsWithNewestItems(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	seedSessionReplies(t, s, 3)
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), nil, 0))

	stream := openSessionStream(t, client, sessionReq(eventEngine, 0, 2))
	wantStreamMessage(t, stream.next(t), &orreryv1.SessionsWatchConversationResponse{
		Transcript: eventEngine,
		Seq:        5,
		Reset_:     true,
		Items:      []*orreryv1.ConversationItem{reply(2), reply(3)},
		State: &orreryv1.ConversationState{
			Origin:  "fix the bug",
			Session: engineSession("idle"),
		},
	})
}

// TestSessionWatchResumeSendsItemsAfterSeq opens the stream after seq 3, the seq of reply 1. The first delta is not
// a reset: it carries the replies above seq 3 and the whole state.
func TestSessionWatchResumeSendsItemsAfterSeq(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	seedSessionReplies(t, s, 3)
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), nil, 0))

	stream := openSessionStream(t, client, sessionReq(eventEngine, 3, 0))
	wantStreamMessage(t, stream.next(t), &orreryv1.SessionsWatchConversationResponse{
		Transcript: eventEngine,
		Seq:        5,
		Items:      []*orreryv1.ConversationItem{reply(2), reply(3)},
		State: &orreryv1.ConversationState{
			Origin:  "fix the bug",
			Session: engineSession("idle"),
		},
	})
}

// TestSessionWatchSendsNewBlockAsDelta appends a reply after the first delta. The next delta carries only that reply:
// no reset, and no state, since the state did not change.
func TestSessionWatchSendsNewBlockAsDelta(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	seedSessionReplies(t, s, 3)
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), nil, 0))
	stream := openSessionStream(t, client, sessionReq(eventEngine, 0, 0))
	stream.next(t)

	applyEngineOps(t, s, replyBlock(4))
	wantStreamMessage(t, stream.nextDelta(t), &orreryv1.SessionsWatchConversationResponse{
		Transcript: eventEngine,
		Seq:        6,
		Items:      []*orreryv1.ConversationItem{reply(4)},
	})
}

// TestSessionWatchStreamsUnfinishedTextUntilItFinishes streams a block whose text grows: each change to its text is a
// delta with the new text in the unfinished set and no items. When the block finishes, its reply is the item and the
// unfinished set is empty.
func TestSessionWatchStreamsUnfinishedTextUntilItFinishes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	applyEngineOps(t, s, store.EngineText{ID: eventEngine, MessageID: "msg-1", Index: 0, Kind: "text", Text: "par"})
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), nil, 0))
	stream := openSessionStream(t, client, sessionReq(eventEngine, 0, 0))

	unfinished := func(text string) []*orreryv1.EngineText {
		return []*orreryv1.EngineText{{MessageId: "msg-1", Index: 0, Kind: "text", Text: text}}
	}
	wantStreamMessage(t, stream.next(t), &orreryv1.SessionsWatchConversationResponse{
		Transcript: eventEngine,
		Seq:        1,
		Reset_:     true,
		Unfinished: unfinished("par"),
		State:      &orreryv1.ConversationState{Session: engineSession("idle")},
	})

	applyEngineOps(t, s, store.EngineText{
		ID: eventEngine, MessageID: "msg-1", Index: 0, Kind: "text", Text: "partial",
	})
	wantStreamMessage(t, stream.nextDelta(t), &orreryv1.SessionsWatchConversationResponse{
		Transcript: eventEngine,
		Seq:        1,
		Unfinished: unfinished("partial"),
	})

	applyEngineOps(t, s,
		store.EngineBlock{ID: eventEngine, Kind: "text", Role: "assistant", MessageID: "msg-1", Text: "partial done"},
		store.EngineTextDone{ID: eventEngine, MessageID: "msg-1", Index: 0},
	)
	wantStreamMessage(t, stream.nextDelta(t), &orreryv1.SessionsWatchConversationResponse{
		Transcript: eventEngine,
		Seq:        2,
		Items: []*orreryv1.ConversationItem{
			{Kind: "reply", Time: eventAt, Text: "partial done"},
		},
	})
}

// TestSessionWatchCarriesOpenRequestWhileWaiting opens a permission request and sets the session to waiting. The
// state carries the open request, which the phone answers with its options.
func TestSessionWatchCarriesOpenRequestWhileWaiting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	applyEngineOps(t, s,
		store.EngineRequest{
			ID: eventEngine, Kind: "permission", RequestID: "req-1", ToolUseID: "toolu-1", ToolName: "Write",
			Subtype: "can_use_tool", Input: `{"path":"/x"}`,
		},
		store.EngineSet{ID: eventEngine, Changes: store.EngineChanges{
			State: optional("waiting"), Question: optional("Write?"),
		}},
	)
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), nil, 0))

	stream := openSessionStream(t, client, sessionReq(eventEngine, 0, 0))
	waiting := engineSession("waiting")
	waiting.Question = "Write?"
	wantStreamMessage(t, stream.next(t), &orreryv1.SessionsWatchConversationResponse{
		Transcript: eventEngine,
		Seq:        3,
		Reset_:     true,
		State: &orreryv1.ConversationState{
			Session: waiting,
			Request: &orreryv1.OpenRequest{
				Id: "req-1", Kind: "permission", Question: "Write", Options: []string{"allow", "deny"},
			},
		},
	})
}

// TestSessionWatchKeepaliveCarriesUnfinishedSet leaves the session unchanged after the first delta. The keepalive that
// follows carries no items and no state, and the unfinished set of the read it came from.
func TestSessionWatchKeepaliveCarriesUnfinishedSet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	applyEngineOps(t, s, store.EngineText{ID: eventEngine, MessageID: "msg-1", Index: 0, Kind: "text", Text: "held"})
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), nil, 0))
	stream := openSessionStream(t, client, sessionReq(eventEngine, 0, 0))
	stream.next(t)

	wantStreamMessage(t, stream.next(t), &orreryv1.SessionsWatchConversationResponse{
		Transcript: eventEngine,
		Seq:        1,
		Unfinished: []*orreryv1.EngineText{{MessageId: "msg-1", Index: 0, Kind: "text", Text: "held"}},
	})
}

// TestSessionWatchEndsOnShutdown holds a stream waiting for its next poll. Closing the shutdown signal must end the
// stream with Unavailable and the fixed shutdown message.
func TestSessionWatchEndsOnShutdown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	shutdown := make(chan struct{})
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), shutdown, 0))
	stream := openSessionStream(t, client, sessionReq(eventEngine, 0, 0))
	stream.next(t)

	close(shutdown)
	err := stream.end(t)
	wantCode(t, err, connect.CodeUnavailable)
	if !strings.Contains(err.Error(), "serve is shutting down") {
		t.Fatalf("shutdown error: got %v, want the fixed shutdown message", err)
	}
}

// writeLargeSession writes eventEngine with replies replies, each a deadlineSummaryBytes text, in one write.
func writeLargeSession(t *testing.T, s *store.Store, replies int) {
	t.Helper()
	ops := []store.EngineOp{store.EngineAdd{ID: eventEngine, Dir: "/a", State: "idle"}}
	for n := 1; n <= replies; n++ {
		ops = append(ops, store.EngineBlock{
			ID: eventEngine, Kind: "text", Role: "assistant", MessageID: fmt.Sprintf("msg-%d", n),
			Text: deadlineSummary(n),
		})
	}
	applyEngineOps(t, s, ops...)
}

// TestSessionWatchEndsStalledReaderAtSendDeadline holds a stream blocked in its reset on a client that stops
// reading. The send deadline must end that stream and its connection.
func TestSessionWatchEndsStalledReaderAtSendDeadline(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	writeLargeSession(t, s, convLargeReplies)
	url, closed, obs := startConvDeadlineServer(
		t,
		sessionsHandler(t, store.NewReader(path), nil, deadlineStalledSendTimeout),
	)

	dialed := make(chan net.Conn, 1)
	stalledHTTP := &http.Client{Transport: headerTransport{base: stalledTransport(dialed), login: allowedLogin}}
	stalled := openSessionStream(
		t,
		orreryv1connect.NewSessionsClient(stalledHTTP, url),
		sessionReq(eventEngine, 0, convLargeReplies),
	)
	stalledAddr := (<-dialed).LocalAddr().String()
	waitStalledWrite(t, obs)

	waitClosed(t, closed, stalledAddr)
	if err := stalled.end(t); err == nil {
		t.Fatal("stalled stream ended cleanly, want a transport error with no end-of-stream message")
	}
}

// TestSessionWatchShutdownEndsBlockedSendWithinGrace holds a stream blocked in its reset on a client that stopped
// reading, with a send deadline far above the shutdown grace. Closing the shutdown signal must end the blocked send,
// and the connection with it, within the shutdown grace.
func TestSessionWatchShutdownEndsBlockedSendWithinGrace(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	writeLargeSession(t, s, convLargeReplies)
	shutdown := make(chan struct{})
	url, closed, obs := startConvDeadlineServer(
		t,
		sessionsHandler(t, store.NewReader(path), shutdown, stallSendTimeout),
	)

	dialed := make(chan net.Conn, 1)
	stalledHTTP := &http.Client{Transport: headerTransport{base: stalledTransport(dialed), login: allowedLogin}}
	openSessionStream(
		t,
		orreryv1connect.NewSessionsClient(stalledHTTP, url),
		sessionReq(eventEngine, 0, convLargeReplies),
	)
	stalledAddr := (<-dialed).LocalAddr().String()
	waitStalledWrite(t, obs)

	close(shutdown)
	waitClosedWithin(t, closed, stalledAddr, closeBudget)
}

// writeStepSession writes a step session with an event, which Sessions.WatchConversation must not stream.
func writeStepSession(t *testing.T, s *store.Store) {
	t.Helper()
	applyEngineOps(t, s, store.EngineAdd{ID: stepSessionID, Dir: "/s", State: "idle", Kind: store.KindStep})
}

// TestSessionWatchStartErrors checks the answers a stream gives before its first delta: unavailable with no store,
// not_found for a missing session and a step session, invalid_argument for a limit above 100, and internal with no
// store detail for a store that cannot be read.
func TestSessionWatchStartErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	writeStepSession(t, s)
	garbage := filepath.Join(t.TempDir(), "orrery.db")
	if err := os.WriteFile(garbage, []byte("this file is not a sqlite database and has no header"), 0o600); err != nil {
		t.Fatalf("write garbage store: %v", err)
	}

	cases := []struct {
		name   string
		reader *store.Reader
		req    *orreryv1.SessionsWatchConversationRequest
		want   connect.Code
	}{
		{
			name:   "nil reader",
			reader: nil,
			req:    sessionReq(eventEngine, 0, 0),
			want:   connect.CodeUnavailable,
		},
		{
			name:   "missing store",
			reader: store.NewReader(filepath.Join(t.TempDir(), "absent", "orrery.db")),
			req:    sessionReq(eventEngine, 0, 0),
			want:   connect.CodeUnavailable,
		},
		{
			name:   "missing session",
			reader: store.NewReader(path),
			req:    sessionReq("engine-404", 0, 0),
			want:   connect.CodeNotFound,
		},
		{
			name:   "step session",
			reader: store.NewReader(path),
			req:    sessionReq(stepSessionID, 0, 0),
			want:   connect.CodeNotFound,
		},
		{
			name:   "limit above 100",
			reader: store.NewReader(path),
			req:    sessionReq(eventEngine, 0, 101),
			want:   connect.CodeInvalidArgument,
		},
		{
			name:   "store that cannot be read",
			reader: store.NewReader(garbage),
			req:    sessionReq(eventEngine, 0, 0),
			want:   connect.CodeInternal,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := sessionsWatchClient(t, sessionsHandler(t, tc.reader, nil, 0))
			stream := openSessionStream(t, client, tc.req)
			err := stream.end(t)
			wantCode(t, err, tc.want)
			if strings.Contains(err.Error(), "database") {
				t.Fatalf("stream echoed store detail: %v", err)
			}
		})
	}
}
