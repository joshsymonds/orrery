package server_test

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/server"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// convLargeReplies is the number of replies writeLargeConversation writes. Each reply is deadlineSummaryBytes long, so
// the reset that opens a stream holds far more than the socket buffers.
const convLargeReplies = 40

// writeLargeConversation writes a synthetic transcript of replies replies, each with a deadlineSummaryBytes text, to
// a new file in t.TempDir and returns its path.
func writeLargeConversation(t *testing.T, replies int) string {
	t.Helper()
	lines := []string{`{"type":"user","message":{"role":"user","content":"` + synthOrigin + `"}}`}
	for n := 1; n <= replies; n++ {
		lines = append(lines, `{"type":"assistant","message":{"content":[{"type":"text","text":"`+
			deadlineSummary(n)+`"}]}}`)
	}
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path
}

// convEndHandler builds the guarded handler that admits allowedLogin and routes to tmux. Its WatchConversation
// streams end when shutdown closes, and a send that stays blocked past sendTimeout ends its stream. A nil shutdown
// never closes, and a zero sendTimeout takes the default.
func convEndHandler(
	t *testing.T,
	tmux *tmuxctl.Client,
	shutdown <-chan struct{},
	sendTimeout time.Duration,
) http.Handler {
	t.Helper()
	handler, err := server.New(server.Config{
		Logins:                []string{allowedLogin},
		Tmux:                  tmux,
		Push:                  newPushStore(t),
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

// startConvDeadlineServer serves handler over connections whose write buffers are deadlineSocketBuffer bytes. It
// returns the URL, a channel that receives the remote address of each connection as it closes, and the listener that
// observes the writes on each connection.
func startConvDeadlineServer(t *testing.T, handler http.Handler) (string, <-chan string, *observedListener) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	obs := &observedListener{Listener: smallBufferListener{Listener: listener}}
	closed := make(chan string, deadlineConnCapacity)
	srv := &httptest.Server{
		Listener: obs,
		Config: &http.Server{Handler: handler, ConnState: func(conn net.Conn, state http.ConnState) {
			if state == http.StateClosed {
				closed <- conn.RemoteAddr().String()
			}
		}},
	}
	srv.Start()
	t.Cleanup(srv.Close)
	return srv.URL, closed, obs
}

// waitStalledWrite fails the test unless a write on obs stays in progress with no byte completing for stallSamples
// consecutive event ticks. Such a write is a send the client is not reading.
func waitStalledWrite(t *testing.T, obs *observedListener) {
	t.Helper()
	timeout := time.After(watchWait)
	tick := time.NewTicker(eventPoll)
	defer tick.Stop()
	last, stable := int64(-1), 0
	for stable < stallSamples {
		select {
		case <-timeout:
			t.Fatalf("no send blocked within %v", watchWait)
		case <-tick.C:
		}
		writing, written := obs.writeState()
		if writing && written == last {
			stable++
		} else {
			stable = 0
		}
		last = written
	}
}

// waitClosedWithin fails the test unless the connection with remote address addr closes within budget.
func waitClosedWithin(t *testing.T, closed <-chan string, addr string, budget time.Duration) {
	t.Helper()
	timeout := time.After(budget)
	for {
		select {
		case got := <-closed:
			if got == addr {
				return
			}
		case <-timeout:
			t.Fatalf("connection %s still open %v after shutdown, want it to end within the shutdown grace",
				addr, budget)
		}
	}
}

// TestWatchConversationEndsOnShutdown holds a conversation stream waiting for its next poll. Closing the shutdown
// signal must end the stream with Unavailable and the fixed shutdown message.
func TestWatchConversationEndsOnShutdown(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	path := writeConversation(t, 2)
	pane := conversationPane(t, tmux, liveOptions(path))
	shutdown := make(chan struct{})
	client := panesClient(t, convEndHandler(t, tmux, shutdown, 0))
	stream := openConversation(t, client, &orreryv1.WatchConversationRequest{Pane: pane, Limit: 2})
	stream.next(t)
	stream.settle(t)

	close(shutdown)
	wantShutdownError(t, stream.end(t))
}

// TestWatchConversationEndsStalledReaderAtSendDeadline holds one conversation stream blocked in a send on a client
// that stops reading. Its send deadline must end that stream, and a second stream on the same server must keep
// receiving deltas while the first is blocked.
func TestWatchConversationEndsStalledReaderAtSendDeadline(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	path := writeLargeConversation(t, convLargeReplies)
	pane := conversationPane(t, tmux, liveOptions(path))
	url, closed, obs := startConvDeadlineServer(t, convEndHandler(t, tmux, nil, deadlineStalledSendTimeout))
	req := &orreryv1.WatchConversationRequest{Pane: pane, Limit: convLargeReplies}

	// The stalled connection is the only one accepted while the hold is on: the healthy stream dials after the stalled
	// send is blocked. The stalled send's deadline stays unapplied until the healthy stream has been checked, and
	// release applies it as already due.
	obs.setHold(true)
	dialed := make(chan net.Conn, 1)
	stalledHTTP := &http.Client{Transport: headerTransport{base: stalledTransport(dialed), login: allowedLogin}}
	stalled := openConversation(t, orreryv1connect.NewPanesClient(stalledHTTP, url), req)
	stalledAddr := (<-dialed).LocalAddr().String()
	waitStalledWrite(t, obs)
	obs.setHold(false)

	healthy := openConversation(t, orreryv1connect.NewPanesClient(guardedHTTPClient(), url), req)
	if first := healthy.next(t); !first.GetReset_() {
		t.Fatalf("healthy first delta reset=%v, want a reset", first.GetReset_())
	}
	appendTranscript(t, path, replyLine(convLargeReplies+1))
	assertTexts(t, healthy.nextDelta(t).GetItems(), "synthetic reply 41")
	assertStillOpen(t, closed, stalledAddr)

	if err := obs.releaseAll(); err != nil {
		t.Fatalf("release stalled connection: %v", err)
	}
	waitClosed(t, closed, stalledAddr)
	if err := stalled.end(t); err == nil {
		t.Fatal("stalled stream ended cleanly, want a transport error with no end-of-stream message")
	}
}

// TestWatchConversationShutdownEndsBlockedSendWithinGrace holds a conversation stream blocked in a send on a client
// that stopped reading, with a send deadline far above the shutdown grace. Closing the shutdown signal must end the
// blocked send, and the connection with it, within the shutdown grace.
func TestWatchConversationShutdownEndsBlockedSendWithinGrace(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	path := writeLargeConversation(t, convLargeReplies)
	pane := conversationPane(t, tmux, liveOptions(path))
	shutdown := make(chan struct{})
	url, closed, obs := startConvDeadlineServer(t, convEndHandler(t, tmux, shutdown, stallSendTimeout))

	dialed := make(chan net.Conn, 1)
	stalledHTTP := &http.Client{Transport: headerTransport{base: stalledTransport(dialed), login: allowedLogin}}
	openConversation(t, orreryv1connect.NewPanesClient(stalledHTTP, url),
		&orreryv1.WatchConversationRequest{Pane: pane, Limit: convLargeReplies})
	stalledAddr := (<-dialed).LocalAddr().String()
	waitStalledWrite(t, obs)

	close(shutdown)
	waitClosedWithin(t, closed, stalledAddr, closeBudget)
}
