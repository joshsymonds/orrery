package server_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/server"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

const (
	// convPoll is the WatchConversation poll interval the tests inject.
	convPoll = 10 * time.Millisecond
	// convKeepalive is the WatchConversation keepalive interval the tests inject.
	convKeepalive = 100 * time.Millisecond
	// convContextWindow is the context window every conversation state reports.
	convContextWindow = 1_000_000
	// convGoal is the goal condition the state-change test appends.
	convGoal = "synthetic conversation goal"
)

// convHandler builds the guarded handler that admits allowedLogin and routes to tmux, polling each conversation
// every convPoll and keeping it alive every convKeepalive.
func convHandler(t *testing.T, tmux *tmuxctl.Client) http.Handler {
	t.Helper()
	handler, err := server.New(server.Config{
		Logins:                []string{allowedLogin},
		Tmux:                  tmux,
		Push:                  newPushStore(t),
		ConversationPoll:      convPoll,
		ConversationKeepalive: convKeepalive,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return handler
}

// convStream runs one WatchConversation call on its own goroutine and forwards each message to msgs. The call's
// error is set before msgs closes, so a reader that sees msgs closed also sees err.
type convStream struct {
	msgs chan *orreryv1.WatchConversationResponse
	done chan struct{}
	err  error
	// command is the pane command of the last state the test has read from the stream.
	command string
}

// openConversation starts WatchConversation with req. The stream's context is canceled at cleanup, and the call is
// waited for once its goroutine has exited.
func openConversation(
	t *testing.T,
	client orreryv1connect.PanesClient,
	req *orreryv1.WatchConversationRequest,
) *convStream {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	s := &convStream{msgs: make(chan *orreryv1.WatchConversationResponse), done: make(chan struct{})}
	go s.run(ctx, client, req)
	t.Cleanup(func() {
		cancel()
		<-s.done
	})
	return s
}

// run calls WatchConversation with req, then forwards each received message to msgs until the stream ends or ctx is
// canceled.
func (s *convStream) run(
	ctx context.Context,
	client orreryv1connect.PanesClient,
	req *orreryv1.WatchConversationRequest,
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
func (s *convStream) next(t *testing.T) *orreryv1.WatchConversationResponse {
	t.Helper()
	select {
	case msg, ok := <-s.msgs:
		if !ok {
			t.Fatalf("stream ended before the next message: %v", s.err)
		}
		if state := msg.GetState(); state != nil {
			s.command = state.GetPane().GetCommand()
		}
		return msg
	case <-time.After(watchWait):
		t.Fatalf("no message within %v", watchWait)
	}
	return nil
}

// isKeepalive reports whether msg is a keepalive: a delta with no reset, items or state.
func isKeepalive(msg *orreryv1.WatchConversationResponse) bool {
	return !msg.GetReset_() && len(msg.GetItems()) == 0 && msg.GetState() == nil
}

// nextDelta returns the next message that is not a keepalive, failing the test when none arrives within watchWait.
func (s *convStream) nextDelta(t *testing.T) *orreryv1.WatchConversationResponse {
	t.Helper()
	deadline := time.Now().Add(watchWait)
	for time.Now().Before(deadline) {
		if msg := s.next(t); !isKeepalive(msg) {
			return msg
		}
	}
	t.Fatalf("no delta within %v", watchWait)
	return nil
}

// settle reads the messages the stream sends while the pane's session is still starting, up to the first keepalive
// sent after the stream reports the pane running the program longCommand starts. A pane reports its command only after
// its shell has exec'd it. The pane's command and session activity can each change once after the session starts, and
// each change is a state delta, so a test that expects a quiet stream settles first.
func (s *convStream) settle(t *testing.T) {
	t.Helper()
	ready := strings.Fields(longCommand)[0]
	deadline := time.Now().Add(watchWait)
	for time.Now().Before(deadline) {
		if msg := s.next(t); s.command == ready && isKeepalive(msg) {
			return
		}
	}
	t.Fatalf("pane never reported %q within %v", ready, watchWait)
}

// end waits for the stream to end, skipping any message it sends first, and returns the error it ended with. It
// fails the test when the stream does not end within watchWait.
func (s *convStream) end(t *testing.T) error {
	t.Helper()
	deadline := time.After(watchWait)
	for {
		select {
		case _, ok := <-s.msgs:
			if !ok {
				return s.err
			}
		case <-deadline:
			t.Fatalf("stream did not end within %v", watchWait)
		}
	}
}

// itemTexts returns the text of each item, in order.
func itemTexts(items []*orreryv1.ConversationItem) []string {
	texts := make([]string, 0, len(items))
	for _, item := range items {
		texts = append(texts, item.GetText())
	}
	return texts
}

// assertTexts fails the test unless items hold the texts want, in order.
func assertTexts(t *testing.T, items []*orreryv1.ConversationItem, want ...string) {
	t.Helper()
	if got := itemTexts(items); !slices.Equal(got, want) {
		t.Fatalf("items = %q, want %q", got, want)
	}
}

// replyLine returns a transcript line holding synthetic reply n, with its newline.
func replyLine(n int) string {
	return fmt.Sprintf(
		`{"type":"assistant","message":{"content":[{"type":"text","text":"synthetic reply %d"}]}}`, n) + "\n"
}

// fileSize returns the size of the file at path.
func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat transcript: %v", err)
	}
	return info.Size()
}

// setPaneOption sets option to value on pane.
func setPaneOption(t *testing.T, tmux *tmuxctl.Client, pane, option, value string) {
	t.Helper()
	if err := tmux.SetPaneOption(t.Context(), pane, option, value); err != nil {
		t.Fatalf("SetPaneOption(%s, %s): %v", pane, option, err)
	}
}

func TestWatchConversationGrowingTranscript(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	path := writeConversation(t, 2)
	pane := conversationPane(t, tmux, liveOptions(path))
	client := panesClient(t, convHandler(t, tmux))
	stream := openConversation(t, client, &orreryv1.WatchConversationRequest{Pane: pane, Limit: 2})
	id := filepath.Base(path)

	first := stream.next(t)
	if !first.GetReset_() || first.GetTranscript() != id || first.GetSeq() != fileSize(t, path) {
		t.Fatalf("first delta reset=%v transcript=%q seq=%d, want a reset of %q at seq %d",
			first.GetReset_(), first.GetTranscript(), first.GetSeq(), id, fileSize(t, path))
	}
	assertTexts(t, first.GetItems(), "synthetic reply 1", "synthetic reply 2")
	state := first.GetState()
	if state.GetOrigin() != synthOrigin || state.GetContext().GetWindow() != convContextWindow {
		t.Fatalf("first state origin=%q window=%d, want origin %q and window %d",
			state.GetOrigin(), state.GetContext().GetWindow(), synthOrigin, convContextWindow)
	}
	if state.GetPane().GetId() != pane || state.GetPane().GetState() != string(protocol.StateWorking) {
		t.Fatalf("state pane = %v, want pane %s in state %q", state.GetPane(), pane, protocol.StateWorking)
	}
	stream.settle(t)

	appendTranscript(t, path, replyLine(3))
	delta := stream.nextDelta(t)
	if delta.GetReset_() || delta.GetTranscript() != id || delta.GetState() != nil {
		t.Fatalf("appended delta reset=%v transcript=%q state=%v, want a plain delta of %q",
			delta.GetReset_(), delta.GetTranscript(), delta.GetState(), id)
	}
	assertTexts(t, delta.GetItems(), "synthetic reply 3")
	seq := delta.GetSeq()
	if seq != fileSize(t, path) {
		t.Fatalf("appended delta seq = %d, want %d", seq, fileSize(t, path))
	}

	appendTranscript(t, path, replyLine(4)+replyLine(5))
	var texts []string
	for len(texts) < 2 {
		delta = stream.nextDelta(t)
		if delta.GetReset_() {
			t.Fatal("a growing transcript sent a reset")
		}
		texts = append(texts, itemTexts(delta.GetItems())...)
		seq = delta.GetSeq()
	}
	if want := []string{"synthetic reply 4", "synthetic reply 5"}; !slices.Equal(texts, want) {
		t.Fatalf("appended items = %q, want %q", texts, want)
	}
	if seq != fileSize(t, path) {
		t.Fatalf("seq after two lines = %d, want %d", seq, fileSize(t, path))
	}

	partial := replyLine(6)
	cut := len(partial) - 1
	appendTranscript(t, path, partial[:cut])
	quiet := stream.next(t)
	if quiet.GetSeq() != seq || len(quiet.GetItems()) != 0 || quiet.GetState() != nil || quiet.GetReset_() {
		t.Fatalf("partial line moved the stream: seq=%d items=%d state=%v reset=%v, want seq %d and no change",
			quiet.GetSeq(), len(quiet.GetItems()), quiet.GetState(), quiet.GetReset_(), seq)
	}

	appendTranscript(t, path, partial[cut:])
	delta = stream.nextDelta(t)
	assertTexts(t, delta.GetItems(), "synthetic reply 6")
	if delta.GetSeq() != fileSize(t, path) {
		t.Fatalf("completed line seq = %d, want %d", delta.GetSeq(), fileSize(t, path))
	}
}

func TestWatchConversationResume(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	path := writeConversation(t, 2)
	pane := conversationPane(t, tmux, liveOptions(path))
	client := panesClient(t, convHandler(t, tmux))
	id := filepath.Base(path)
	first := openConversation(t, client, &orreryv1.WatchConversationRequest{Pane: pane, Limit: 2}).next(t)
	held := first.GetSeq()

	appendTranscript(t, path, replyLine(3))

	t.Run("held transcript and seq", func(t *testing.T) {
		t.Parallel()
		stream := openConversation(t, client, &orreryv1.WatchConversationRequest{
			Pane: pane, Limit: 2, Transcript: id, After: held,
		})
		resumed := stream.next(t)
		if resumed.GetReset_() || resumed.GetTranscript() != id || resumed.GetSeq() != fileSize(t, path) {
			t.Fatalf("resume reset=%v transcript=%q seq=%d, want no reset of %q at seq %d",
				resumed.GetReset_(), resumed.GetTranscript(), resumed.GetSeq(), id, fileSize(t, path))
		}
		assertTexts(t, resumed.GetItems(), "synthetic reply 3")
		state := resumed.GetState()
		if state == nil {
			t.Fatal("resume carries no state, want the whole state on the first delta")
		}
		if state.GetOrigin() != synthOrigin || state.GetContext().GetWindow() != convContextWindow {
			t.Fatalf("resume state origin=%q window=%d, want origin %q and window %d",
				state.GetOrigin(), state.GetContext().GetWindow(), synthOrigin, convContextWindow)
		}
	})

	t.Run("wrong transcript id", func(t *testing.T) {
		t.Parallel()
		stream := openConversation(t, client, &orreryv1.WatchConversationRequest{
			Pane: pane, Limit: 2, Transcript: "other.jsonl", After: held,
		})
		reset := stream.next(t)
		if !reset.GetReset_() || reset.GetSeq() != fileSize(t, path) {
			t.Fatalf("wrong id reset=%v seq=%d, want a reset at seq %d",
				reset.GetReset_(), reset.GetSeq(), fileSize(t, path))
		}
		assertTexts(t, reset.GetItems(), "synthetic reply 2", "synthetic reply 3")
	})

	t.Run("seq not at a line start", func(t *testing.T) {
		t.Parallel()
		stream := openConversation(t, client, &orreryv1.WatchConversationRequest{
			Pane: pane, Limit: 2, Transcript: id, After: held + 1,
		})
		reset := stream.next(t)
		if !reset.GetReset_() || reset.GetSeq() != fileSize(t, path) {
			t.Fatalf("bad seq reset=%v seq=%d, want a reset at seq %d",
				reset.GetReset_(), reset.GetSeq(), fileSize(t, path))
		}
		assertTexts(t, reset.GetItems(), "synthetic reply 2", "synthetic reply 3")
	})
}

func TestWatchConversationSwitchedTranscript(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	first := writeConversation(t, 2)
	pane := conversationPane(t, tmux, liveOptions(first))
	client := panesClient(t, convHandler(t, tmux))
	stream := openConversation(t, client, &orreryv1.WatchConversationRequest{Pane: pane, Limit: 2})
	stream.next(t)
	stream.settle(t)

	second := writeConversation(t, 3)
	renamed := filepath.Join(filepath.Dir(second), "second.jsonl")
	if err := os.Rename(second, renamed); err != nil {
		t.Fatalf("rename transcript: %v", err)
	}
	setPaneOption(t, tmux, pane, protocol.OptionTranscript, renamed)

	reset := stream.nextDelta(t)
	if !reset.GetReset_() || reset.GetTranscript() != "second.jsonl" || reset.GetSeq() != fileSize(t, renamed) {
		t.Fatalf("switched reset=%v transcript=%q seq=%d, want a reset of second.jsonl at seq %d",
			reset.GetReset_(), reset.GetTranscript(), reset.GetSeq(), fileSize(t, renamed))
	}
	assertTexts(t, reset.GetItems(), "synthetic reply 2", "synthetic reply 3")
	if reset.GetState() == nil {
		t.Fatal("switched reset carries no state")
	}

	appendTranscript(t, renamed, replyLine(4))
	grown := stream.nextDelta(t)
	if grown.GetReset_() || grown.GetTranscript() != "second.jsonl" {
		t.Fatalf("growth after the switch reset=%v transcript=%q, want a plain delta of second.jsonl",
			grown.GetReset_(), grown.GetTranscript())
	}
	assertTexts(t, grown.GetItems(), "synthetic reply 4")
}

func TestWatchConversationContextFigure(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	path := writeConversation(t, 1)
	pane := conversationPane(t, tmux, liveOptions(path))
	client := panesClient(t, convHandler(t, tmux))
	stream := openConversation(t, client, &orreryv1.WatchConversationRequest{Pane: pane})
	stream.next(t)
	stream.settle(t)

	line := `{"type":"assistant","message":{"usage":{"input_tokens":1234},` +
		`"content":[{"type":"text","text":"synthetic reply 2"}]}}` + "\n"
	appendTranscript(t, path, line)
	delta := stream.nextDelta(t)
	assertTexts(t, delta.GetItems(), "synthetic reply 2")
	figure := delta.GetState().GetContext()
	if figure.GetTokens() != 1234 || figure.GetWindow() != convContextWindow {
		t.Fatalf("context figure tokens=%d window=%d, want tokens 1234 and window %d",
			figure.GetTokens(), figure.GetWindow(), convContextWindow)
	}
}

func TestWatchConversationStateChanges(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	path := writeConversation(t, 1)
	pane := conversationPane(t, tmux, liveOptions(path))
	client := panesClient(t, convHandler(t, tmux))
	stream := openConversation(t, client, &orreryv1.WatchConversationRequest{Pane: pane})
	first := stream.next(t)
	stream.settle(t)

	appendTranscript(t, path, goalRecord(convGoal, false))
	goal := stream.nextDelta(t)
	if goal.GetReset_() || goal.GetState().GetGoal() != convGoal || len(goal.GetItems()) != 0 {
		t.Fatalf("goal delta reset=%v goal=%q items=%d, want state with goal %q and no items",
			goal.GetReset_(), goal.GetState().GetGoal(), len(goal.GetItems()), convGoal)
	}
	if goal.GetSeq() <= first.GetSeq() {
		t.Fatalf("goal delta seq = %d, want past %d", goal.GetSeq(), first.GetSeq())
	}

	appendTranscript(t, path, queueLine("enqueue", synthQueued, "2026-11-11T00:00:00Z", ""))
	queued := stream.nextDelta(t)
	if waiting := queued.GetState().GetWaiting(); len(waiting) != 1 || waiting[0].GetText() != synthQueued {
		t.Fatalf("queue delta waiting = %v, want one message %q", waiting, synthQueued)
	}

	setPaneOption(t, tmux, pane, protocol.OptionState, string(protocol.StateDone))
	paneState := stream.nextDelta(t)
	if paneState.GetState().GetPane().GetState() != string(protocol.StateDone) {
		t.Fatalf("pane delta pane state = %q, want %q",
			paneState.GetState().GetPane().GetState(), protocol.StateDone)
	}
	if waiting := paneState.GetState().GetWaiting(); len(waiting) != 0 {
		t.Fatalf("done pane waiting = %v, want none (idle lists only canceled messages)", waiting)
	}
	if paneState.GetSeq() != queued.GetSeq() {
		t.Fatalf("pane delta seq = %d, want unchanged %d", paneState.GetSeq(), queued.GetSeq())
	}
}

func TestWatchConversationKeepalive(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	path := writeConversation(t, 1)
	pane := conversationPane(t, tmux, liveOptions(path))
	client := panesClient(t, convHandler(t, tmux))
	stream := openConversation(t, client, &orreryv1.WatchConversationRequest{Pane: pane})
	first := stream.next(t)

	stream.settle(t)
	for range 2 {
		msg := stream.next(t)
		if msg.GetTranscript() != filepath.Base(path) || msg.GetSeq() != first.GetSeq() || !isKeepalive(msg) {
			t.Fatalf("quiet stream sent transcript=%q seq=%d items=%d state=%v reset=%v, want a keepalive at seq %d",
				msg.GetTranscript(), msg.GetSeq(), len(msg.GetItems()), msg.GetState(), msg.GetReset_(), first.GetSeq())
		}
	}
}

func TestWatchConversationStartErrors(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	path := writeConversation(t, 1)
	missing := filepath.Join(t.TempDir(), "missing.jsonl")
	sessions := map[string]map[string]string{
		"live":          liveOptions(path),
		"unlive":        {protocol.OptionTranscript: path},
		"untranscribed": {protocol.OptionState: string(protocol.StateWorking)},
		"unfound":       liveOptions(missing),
	}
	ids := make(map[string]string, len(sessions))
	for name, options := range sessions {
		startSession(t, tmux, name, longCommand)
		ids[name] = paneID(t, tmux, name)
		for option, value := range options {
			setPaneOption(t, tmux, ids[name], option, value)
		}
	}
	client := panesClient(t, convHandler(t, tmux))

	cases := []struct {
		name    string
		req     *orreryv1.WatchConversationRequest
		code    connect.Code
		message string
	}{
		{"limit above 100", &orreryv1.WatchConversationRequest{Pane: ids["live"], Limit: 101},
			connect.CodeInvalidArgument, "bad request query"},
		{"malformed pane id", &orreryv1.WatchConversationRequest{Pane: "abc"},
			connect.CodeInvalidArgument, "malformed pane id"},
		{"absent pane", &orreryv1.WatchConversationRequest{Pane: "%999999"},
			connect.CodeNotFound, "pane not found"},
		{"no live session", &orreryv1.WatchConversationRequest{Pane: ids["unlive"]},
			connect.CodeNotFound, "no live session"},
		{"no transcript", &orreryv1.WatchConversationRequest{Pane: ids["untranscribed"]},
			connect.CodeNotFound, "no transcript"},
		{"transcript file missing", &orreryv1.WatchConversationRequest{Pane: ids["unfound"]},
			connect.CodeNotFound, "transcript not found"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			stream := openConversation(t, client, c.req)
			assertConnectError(t, stream.end(t), c.code, c.message)
		})
	}
}

func TestWatchConversationEndsMidStream(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		change func(t *testing.T, tmux *tmuxctl.Client, pane, path string)
		code   connect.Code
		// messages are the messages the stream may end with. A pane that vanishes between the pane list and the
		// transcript read ends with "no transcript" rather than "pane not found", and both are not_found.
		messages []string
	}{
		{"transcript removed",
			func(t *testing.T, _ *tmuxctl.Client, _, path string) {
				t.Helper()
				if err := os.Remove(path); err != nil {
					t.Fatalf("remove transcript: %v", err)
				}
			},
			connect.CodeNotFound, []string{"transcript not found"}},
		{"transcript option cleared",
			func(t *testing.T, tmux *tmuxctl.Client, pane, _ string) {
				t.Helper()
				setPaneOption(t, tmux, pane, protocol.OptionTranscript, "")
			},
			connect.CodeNotFound, []string{"no transcript"}},
		{"pane state cleared",
			func(t *testing.T, tmux *tmuxctl.Client, pane, _ string) {
				t.Helper()
				setPaneOption(t, tmux, pane, protocol.OptionState, "")
			},
			connect.CodeNotFound, []string{"no live session"}},
		{"pane closed",
			func(t *testing.T, tmux *tmuxctl.Client, pane, _ string) {
				t.Helper()
				// A second session keeps the tmux server running, so the pane is absent rather than the server.
				startSession(t, tmux, "keeper", longCommand)
				if _, err := tmux.Run(t.Context(), "kill-pane", "-t", pane); err != nil {
					t.Fatalf("kill-pane: %v", err)
				}
			},
			connect.CodeNotFound, []string{"pane not found", "no transcript"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			tmux := newTmuxServer(t)
			path := writeConversation(t, 1)
			pane := conversationPane(t, tmux, liveOptions(path))
			client := panesClient(t, convHandler(t, tmux))
			stream := openConversation(t, client, &orreryv1.WatchConversationRequest{Pane: pane})
			stream.next(t)

			c.change(t, tmux, pane, path)
			err := stream.end(t)
			var connectErr *connect.Error
			if !errors.As(err, &connectErr) || connectErr.Code() != c.code ||
				!slices.Contains(c.messages, connectErr.Message()) {
				t.Fatalf("error = %v, want code %v with one of the messages %q", err, c.code, c.messages)
			}
		})
	}
}

func TestWatchConversationEndsOnCancel(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	path := writeConversation(t, 1)
	pane := conversationPane(t, tmux, liveOptions(path))
	handler := convHandler(t, tmux)
	ended := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r)
		close(ended)
	}))
	t.Cleanup(srv.Close)
	httpClient := &http.Client{Transport: headerTransport{base: http.DefaultTransport, login: allowedLogin}}
	client := orreryv1connect.NewPanesClient(httpClient, srv.URL)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := client.WatchConversation(ctx, connect.NewRequest(&orreryv1.WatchConversationRequest{Pane: pane}))
	if err != nil {
		t.Fatalf("WatchConversation: %v", err)
	}
	if !stream.Receive() {
		t.Fatalf("no first delta: %v", stream.Err())
	}
	cancel()
	select {
	case <-ended:
	case <-time.After(watchWait):
		t.Fatalf("the handler did not return within %v of the client canceling", watchWait)
	}
	_ = stream.Close()
}

func TestNewRejectsNegativeConversationTimings(t *testing.T) {
	t.Parallel()

	cases := map[string]server.Config{
		"poll":      {ConversationPoll: -time.Millisecond},
		"keepalive": {ConversationKeepalive: -time.Millisecond},
	}
	for name, cfg := range cases {
		cfg.Logins = []string{allowedLogin}
		cfg.Tmux = newTmuxClient(t)
		cfg.Push = newPushStore(t)
		if _, err := server.New(cfg); err == nil {
			t.Fatalf("New with a negative conversation %s: no error", name)
		}
	}
}
