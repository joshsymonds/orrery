package server_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/conversation"
	"github.com/joshsymonds/orrery/internal/enginehost"
	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	enginev1connect "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/server"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

const (
	// echoSendAt is the unix second the echo tests' phone sends are recorded at, which their fake clock holds.
	echoSendAt = 1_800_000_000
	// echoPhoneText is the text the phone sends in the echo tests, and the text a desk prompt repeats.
	echoPhoneText = "synthetic phone prompt"
	// echoWebClient is the X-Orrery-Client of the orrery web sends of the echo tests, which is no phone client.
	echoWebClient = "orrery web"
	// echoWebText is the text orrery web sends in the echo tests.
	echoWebText = "synthetic web prompt"
	// echoReplyZero, echoReplyOne and echoReplyTwo are the texts of the replies the echo tests' transcripts hold.
	echoReplyZero = "synthetic reply zero"
	echoReplyOne  = "synthetic reply one"
	echoReplyTwo  = "synthetic reply two"
)

// echoClock is the fake clock of the echo tests: the unix second that a send is recorded at, which the test sets.
type echoClock struct {
	at atomic.Int64
}

// set sets the second the clock holds.
func (c *echoClock) set(at int64) {
	c.at.Store(at)
}

// echoPaneSends is the PaneSends client of the echo tests. Its Record stores each send in the store the way orreryd's
// PaneSends service does, stamped with the clock's time.
type echoPaneSends struct {
	writer *store.Store
	clock  *echoClock
}

// Record stores the text the request's client sent the request's pane, at the clock's time.
func (p echoPaneSends) Record(
	ctx context.Context,
	req *connect.Request[enginev1.PaneSendsRecordRequest],
) (*connect.Response[enginev1.PaneSendsRecordResponse], error) {
	at := time.Unix(p.clock.at.Load(), 0)
	err := p.writer.RecordPaneSend(ctx, req.Msg.GetPane(), req.Msg.GetText(), req.Msg.GetClient(), at)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&enginev1.PaneSendsRecordResponse{}), nil
}

// echoNoTimer is the timer of the echo tests. It never fires, so no keepalive is sent and no change feed retry runs.
func echoNoTimer(time.Duration) (<-chan time.Time, func() bool) {
	return make(chan time.Time), func() bool { return true }
}

// echoEnv is the setup an echo test shares: its tmux server, the store its sends are recorded in, the fake clock those
// sends are stamped with, and the change feed its watch streams wake on.
type echoEnv struct {
	tmux    *tmuxctl.Client
	path    string
	writer  *store.Store
	clock   *echoClock
	feed    *enginehost.ChangeFeed
	changes enginev1connect.ChangesClient
}

// newEchoEnv returns the setup of an echo test, with an empty store at a new path in t.TempDir.
func newEchoEnv(t *testing.T) echoEnv {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	feed, changes := startChanges(t, nil)
	return echoEnv{
		tmux:    newTmuxServer(t),
		path:    path,
		writer:  openEventStore(t, path),
		clock:   &echoClock{},
		feed:    feed,
		changes: changes,
	}
}

// serve serves the Panes API over the env's tmux, reading the store at reader and recording each send in the env's
// store, and returns the URL of the server.
func (e echoEnv) serve(t *testing.T, reader *store.Reader) string {
	t.Helper()
	handler, err := server.New(server.Config{
		Logins:    []string{allowedLogin},
		Tmux:      e.tmux,
		Push:      newPushStore(t),
		Sessions:  reader,
		Changes:   e.changes,
		NewTimer:  echoNoTimer,
		PaneSends: echoPaneSends{writer: e.writer, clock: e.clock},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv.URL
}

// pane starts a session whose pane is a live Claude session with the transcript at path, and returns the pane ID.
func (e echoEnv) pane(t *testing.T, path string) string {
	t.Helper()
	startSession(t, e.tmux, "echo", longCommand)
	pane := paneID(t, e.tmux, "echo")
	for name, value := range liveOptions(path) {
		setPaneOption(t, e.tmux, pane, name, value)
	}
	return pane
}

// echoPanes returns a Panes client of the server at base whose requests carry client as their X-Orrery-Client header.
func echoPanes(base, client string) orreryv1connect.PanesClient {
	httpClient := &http.Client{Transport: promptClientTransport{base: http.DefaultTransport, client: client}}
	return orreryv1connect.NewPanesClient(httpClient, base)
}

// echoSend sends text to pane through client's SendText, and fails the test when the send fails.
func echoSend(t *testing.T, client orreryv1connect.PanesClient, pane, text string) {
	t.Helper()
	req := connect.NewRequest(&orreryv1.SendTextRequest{Pane: pane, Text: text})
	if _, err := client.SendText(t.Context(), req); err != nil {
		t.Fatalf("Panes.SendText %q: %v", text, err)
	}
}

// echoTranscript writes a transcript holding lines, each with its newline, to a new file in t.TempDir. It returns the
// path and the line start of each line.
func echoTranscript(t *testing.T, lines ...string) (string, []int64) {
	t.Helper()
	var body string
	starts := make([]int64, 0, len(lines))
	for _, line := range lines {
		starts = append(starts, int64(len(body)))
		body += line
	}
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path, starts
}

// echoStamp returns the RFC 3339 timestamp of the unix second at.
func echoStamp(at int64) string {
	return time.Unix(at, 0).UTC().Format(time.RFC3339)
}

// echoPrompt returns the transcript line of a prompt with text, stamped at the unix second at.
func echoPrompt(text string, at int64) string {
	return fmt.Sprintf(
		`{"type":"user","timestamp":%q,"message":{"role":"user","content":%q}}`, echoStamp(at), text,
	) + "\n"
}

// echoReply returns the transcript line of a reply with text, stamped at the unix second at.
func echoReply(text string, at int64) string {
	return fmt.Sprintf(
		`{"type":"assistant","timestamp":%q,"message":{"content":[{"type":"text","text":%q}]}}`, echoStamp(at), text,
	) + "\n"
}

// echoWant is an item a read must return: its text and the client it carries.
type echoWant struct {
	text, client string
}

// assertEchoItems fails the test unless items hold the wants, in order, each with its text and its client.
func assertEchoItems(t *testing.T, items []*orreryv1.ConversationItem, want ...echoWant) {
	t.Helper()
	got := make([]echoWant, 0, len(items))
	for _, item := range items {
		got = append(got, echoWant{text: item.GetText(), client: item.GetClient()})
	}
	if !slices.Equal(got, want) {
		t.Fatalf("items = %+v, want %+v", got, want)
	}
}

// TestPromptClientPhoneSendMarksItsEchoOnEachRead checks that a prompt the phone sent carries orrery-android on every
// read that returns it: Conversation, the first delta of WatchConversation, and Older.
func TestPromptClientPhoneSendMarksItsEchoOnEachRead(t *testing.T) {
	t.Parallel()
	env := newEchoEnv(t)
	path, starts := echoTranscript(t,
		echoPrompt(echoPhoneText, echoSendAt+5),
		echoReply(echoReplyOne, echoSendAt+6),
		echoReply(echoReplyTwo, echoSendAt+7),
	)
	pane := env.pane(t, path)
	phone := echoPanes(env.serve(t, store.NewReader(env.path)), sendClient)
	env.clock.set(echoSendAt)
	echoSend(t, phone, pane, echoPhoneText)
	want := []echoWant{
		{echoPhoneText, sendClient},
		{echoReplyOne, ""},
		{echoReplyTwo, ""},
	}

	conv, err := phone.Conversation(t.Context(), connect.NewRequest(&orreryv1.ConversationRequest{Pane: pane}))
	if err != nil {
		t.Fatalf("Panes.Conversation: %v", err)
	}
	assertEchoItems(t, conv.Msg.GetItems(), want...)

	stream := openConversation(t, phone, &orreryv1.WatchConversationRequest{Pane: pane})
	first := stream.next(t)
	if !first.GetReset_() {
		t.Fatal("first delta is not a reset, want the reset that holds the whole conversation")
	}
	assertEchoItems(t, first.GetItems(), want...)

	older, err := phone.Older(t.Context(), connect.NewRequest(&orreryv1.OlderRequest{
		Pane: pane, Transcript: filepath.Base(path), Before: starts[1], Limit: 5,
	}))
	if err != nil {
		t.Fatalf("Panes.Older: %v", err)
	}
	assertEchoItems(t, older.Msg.GetItems(), echoWant{echoPhoneText, sendClient})
}

// echoItemsDelta returns the next delta of stream that holds items, skipping any state-only delta. It has no deadline
// of its own.
func echoItemsDelta(t *testing.T, stream *convStream) *orreryv1.WatchConversationResponse {
	t.Helper()
	for {
		if delta := stream.nextDelta(t); len(delta.GetItems()) > 0 {
			return delta
		}
	}
}

// TestPromptClientWatchDeltaMarksEchoOncePerSend checks the marking of the deltas of WatchConversation. The phone's
// echo arrives in a later delta and marks its prompt there. A desk prompt with the same text that comes in a still
// later delta, within the echo bound of the send, stays unmarked, because the send's echo is taken by the earlier
// prompt.
func TestPromptClientWatchDeltaMarksEchoOncePerSend(t *testing.T) {
	t.Parallel()
	env := newEchoEnv(t)
	path, _ := echoTranscript(t, echoReply(echoReplyZero, echoSendAt-30))
	pane := env.pane(t, path)
	phone := echoPanes(env.serve(t, store.NewReader(env.path)), sendClient)
	env.clock.set(echoSendAt)
	echoSend(t, phone, pane, echoPhoneText)

	stream := openConversation(t, phone, &orreryv1.WatchConversationRequest{Pane: pane})
	assertEchoItems(t, stream.next(t).GetItems(), echoWant{echoReplyZero, ""})

	appendTranscript(t, path, echoPrompt(echoPhoneText, echoSendAt+5))
	env.feed.Publish(0, []string{convChangedSession})
	assertEchoItems(t, echoItemsDelta(t, stream).GetItems(), echoWant{echoPhoneText, sendClient})

	appendTranscript(t, path,
		echoReply(echoReplyOne, echoSendAt+8)+echoPrompt(echoPhoneText, echoSendAt+700))
	env.feed.Publish(0, []string{convChangedSession})
	delta := echoItemsDelta(t, stream)
	if delta.GetReset_() {
		t.Fatal("an appended delta reset the stream")
	}
	assertEchoItems(t, delta.GetItems(), echoWant{echoReplyOne, ""}, echoWant{echoPhoneText, ""})
}

// TestPromptClientQueuedSendEchoedLaterIsMarked checks that a send the phone queued while Claude worked marks its echo
// when the echo comes a turn later, within the echo bound.
func TestPromptClientQueuedSendEchoedLaterIsMarked(t *testing.T) {
	t.Parallel()
	env := newEchoEnv(t)
	path, _ := echoTranscript(t,
		echoReply(echoReplyZero, echoSendAt-30),
		echoPrompt(echoPhoneText, echoSendAt+900),
		echoReply(echoReplyOne, echoSendAt+1000),
	)
	pane := env.pane(t, path)
	phone := echoPanes(env.serve(t, store.NewReader(env.path)), sendClient)
	env.clock.set(echoSendAt)
	echoSend(t, phone, pane, echoPhoneText)

	conv, err := phone.Conversation(t.Context(), connect.NewRequest(&orreryv1.ConversationRequest{Pane: pane}))
	if err != nil {
		t.Fatalf("Panes.Conversation: %v", err)
	}
	assertEchoItems(t, conv.Msg.GetItems(),
		echoWant{echoReplyZero, ""},
		echoWant{echoPhoneText, sendClient},
		echoWant{echoReplyOne, ""},
	)
}

// TestPromptClientDeskTextOutsideBoundAndWebSendAreNotPhone checks that a desk prompt with the phone's text, written
// after the echo bound, carries no client, and that a send from orrery web marks its echo orrery web, not the phone
// client.
func TestPromptClientDeskTextOutsideBoundAndWebSendAreNotPhone(t *testing.T) {
	t.Parallel()
	env := newEchoEnv(t)
	path, _ := echoTranscript(t,
		echoPrompt(echoPhoneText, echoSendAt+conversation.EchoBound+1),
		echoPrompt(echoWebText, echoSendAt+12),
	)
	pane := env.pane(t, path)
	base := env.serve(t, store.NewReader(env.path))
	env.clock.set(echoSendAt)
	echoSend(t, echoPanes(base, sendClient), pane, echoPhoneText)
	env.clock.set(echoSendAt + 10)
	echoSend(t, echoPanes(base, echoWebClient), pane, echoWebText)

	conv, err := echoPanes(base, sendClient).Conversation(
		t.Context(), connect.NewRequest(&orreryv1.ConversationRequest{Pane: pane}),
	)
	if err != nil {
		t.Fatalf("Panes.Conversation: %v", err)
	}
	assertEchoItems(t, conv.Msg.GetItems(),
		echoWant{echoPhoneText, ""},
		echoWant{echoWebText, echoWebClient},
	)
}

// TestPromptClientUnavailableStoreLeavesReadWithoutClients checks that a read whose store is unavailable still
// succeeds, with its items carrying no client.
func TestPromptClientUnavailableStoreLeavesReadWithoutClients(t *testing.T) {
	t.Parallel()
	env := newEchoEnv(t)
	path, _ := echoTranscript(t, echoPrompt(echoPhoneText, echoSendAt+5))
	pane := env.pane(t, path)
	missing := store.NewReader(filepath.Join(t.TempDir(), "missing.db"))
	phone := echoPanes(env.serve(t, missing), sendClient)

	conv, err := phone.Conversation(t.Context(), connect.NewRequest(&orreryv1.ConversationRequest{Pane: pane}))
	if err != nil {
		t.Fatalf("Panes.Conversation with an unavailable store: %v", err)
	}
	assertEchoItems(t, conv.Msg.GetItems(), echoWant{echoPhoneText, ""})
}
