package server

import (
	"context"
	"crypto/rand"
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

	"github.com/joshsymonds/orrery/internal/conversation"
	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	enginev1connect "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
	"github.com/joshsymonds/orrery/internal/tmuxtest"
	"github.com/joshsymonds/orrery/internal/waittest"
)

const (
	// paneFeedAt is the unix second the feed tests' reads are dated at.
	paneFeedAt = 1_700_000_000
	// paneFeedKeepalive is the keepalive interval the feed tests inject. Their fake timers never wait for it.
	paneFeedKeepalive = 5 * time.Second
	// paneFeedOrreryP and paneFeedOrreryQ are the @orrery_id of the two panes the feed tests stream, P and Q.
	paneFeedOrreryP = "orrery-feed-p"
	paneFeedOrreryQ = "orrery-feed-q"
	// paneFeedCommand and paneFeedSeconds keep a pane's session alive until its test server is killed at cleanup. They
	// are passed to tmux as separate arguments, so tmux execs the command with no shell between: the pane's command goes
	// from tmux's own forked child to paneFeedCommand once, and never changes again.
	paneFeedCommand = "sleep"
	paneFeedSeconds = "300"
	// paneFeedNoConfig keeps the user's tmux config away from the feed tests' servers.
	paneFeedNoConfig = "/dev/null"
	// paneFeedPrompt is the first line of every feed test's transcript.
	paneFeedPrompt = `{"type":"user","message":{"role":"user","content":"synthetic prompt"}}` + "\n"
)

// paneFeedTmux returns a client for a unique private tmux server, which is killed at cleanup.
func paneFeedTmux(t *testing.T) *tmuxctl.Client {
	t.Helper()
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("random server name: %v", err)
	}
	name := fmt.Sprintf("orrery-feed-%d-%x", os.Getpid(), suffix)
	tmux, err := tmuxctl.New(tmuxctl.Server{Name: name})
	if err != nil {
		t.Fatalf("New(Name=%q): %v", name, err)
	}
	t.Cleanup(func() {
		if err := tmuxtest.Stop(t, tmuxctl.Server{Name: name}); err != nil {
			t.Errorf("kill-server: %v", err)
		}
	})
	return tmux
}

// paneFeedPane starts a session named session on a pane running paneFeedCommand, sets the pane's state to working, its
// transcript to the file at path and its @orrery_id to id, waits until the pane's command is paneFeedCommand, and returns
// the pane's ID. A stream reads the pane's command as part of its state, so a pane still starting would change its state
// between two reads, and a stream would rightly send that change: the wait keeps the panes the tests stream unchanged.
func paneFeedPane(t *testing.T, tmux *tmuxctl.Client, session, path, id string) string {
	t.Helper()
	ctx := t.Context()
	if _, err := tmux.Run(
		ctx,
		"-f",
		paneFeedNoConfig,
		"new-session",
		"-d",
		"-s",
		session,
		"-c",
		t.TempDir(),
		paneFeedCommand,
		paneFeedSeconds,
	); err != nil {
		t.Fatalf("new-session %s: %v", session, err)
	}
	out, err := tmux.Run(ctx, "list-panes", "-t", session, "-F", "#{pane_id}")
	if err != nil {
		t.Fatalf("list-panes -t %s: %v", session, err)
	}
	pane := strings.TrimSpace(out)
	options := map[string]string{
		protocol.OptionState:      string(protocol.StateWorking),
		protocol.OptionTranscript: path,
		protocol.OptionID:         id,
	}
	for name, value := range options {
		if err := tmux.SetPaneOption(ctx, pane, name, value); err != nil {
			t.Fatalf("SetPaneOption(%s, %s): %v", pane, name, err)
		}
	}
	waittest.Until(t, nil, "pane "+pane+" runs "+paneFeedCommand, func() bool {
		command, err := tmux.Run(ctx, "display-message", "-p", "-t", pane, "#{pane_current_command}")
		if err != nil {
			t.Fatalf("read the command of pane %s: %v", pane, err)
		}
		return strings.TrimSpace(command) == paneFeedCommand
	})
	return pane
}

// paneFeedTranscript writes a transcript that holds the prompt and reply 1 to a new file in t.TempDir, and returns its
// path.
func paneFeedTranscript(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, []byte(paneFeedPrompt+paneFeedReply(1)), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path
}

// paneFeedReply returns the transcript line of synthetic reply n, with its newline.
func paneFeedReply(n int) string {
	return fmt.Sprintf(
		`{"type":"assistant","message":{"content":[{"type":"text","text":"synthetic reply %d"}]}}`, n) + "\n"
}

// paneFeedAppend appends text to the transcript at path.
func paneFeedAppend(t *testing.T, path, text string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open transcript: %v", err)
	}
	if _, err := file.WriteString(text); err != nil {
		t.Fatalf("append transcript: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close transcript: %v", err)
	}
}

// paneFeedEvents is the store reader of the Events server the feed tests run beside the panes. It holds no events, and
// it sends on reads each time a stream reads, so a test knows the stream has subscribed.
type paneFeedEvents struct {
	reads chan struct{}
}

// Events reports the read and returns an empty page.
func (e paneFeedEvents) Events(context.Context, int64, int) (store.EventPage, error) {
	e.reads <- struct{}{}
	return store.EventPage{}, nil
}

// paneFeedRig is the setup the feed tests share: the tmux server their panes run on, the fake Changes service their
// hub subscribes to, the retry timers of that hub and the keepalive timers of the pane streams, the step and read
// reports of the panes, and the URLs of a Panes server and an Events server that share the hub.
type paneFeedRig struct {
	tmux       *tmuxctl.Client
	changes    *eventsFeedChanges
	retries    *eventsFeedTimers
	keeps      *eventsFeedTimers
	steps      chan string // the pane of each step, sent before the step reads the pane
	reports    chan string // the pane of each read report
	eventReads chan struct{}
	panes      orreryv1connect.PanesClient
	eventsURL  string
}

// paneFeedSetup returns a rig whose Panes and Events servers share one hub, which subscribes to a fake Changes service.
// The servers close at cleanup.
func paneFeedSetup(t *testing.T) *paneFeedRig {
	t.Helper()
	rig := &paneFeedRig{
		tmux:       paneFeedTmux(t),
		changes:    &eventsFeedChanges{calls: make(chan *eventsFeedCall, eventsFeedBuffer)},
		retries:    &eventsFeedTimers{armed: make(chan eventsFeedTimer, eventsFeedBuffer)},
		keeps:      &eventsFeedTimers{armed: make(chan eventsFeedTimer, eventsFeedBuffer)},
		steps:      make(chan string, eventsFeedBuffer),
		reports:    make(chan string, eventsFeedBuffer),
		eventReads: make(chan struct{}, eventsFeedBuffer),
	}
	_, changesHandler := enginev1connect.NewChangesHandler(rig.changes)
	changesSrv := httptest.NewServer(changesHandler)
	t.Cleanup(changesSrv.Close)
	hub := newChangeHub(enginev1connect.NewChangesClient(http.DefaultClient, changesSrv.URL), rig.retries.newTimer)

	_, panesHandler := orreryv1connect.NewPanesHandler(panesServer{
		tmux:                  rig.tmux,
		newest:                conversation.NewNewest(),
		goals:                 conversation.NewGoals(),
		queues:                conversation.NewQueues(),
		readReport:            func(pane string, _ int64) { rig.reports <- pane },
		now:                   func() time.Time { return time.Unix(paneFeedAt, 0) },
		changes:               hub,
		newTimer:              rig.keeps.newTimer,
		conversationKeepalive: paneFeedKeepalive,
		sendTimeout:           defaultEventSendTimeout,
		stepped:               func(pane string) { rig.steps <- pane },
	})
	panesSrv := httptest.NewServer(withResponseWriter(panesHandler))
	t.Cleanup(panesSrv.Close)
	rig.panes = orreryv1connect.NewPanesClient(http.DefaultClient, panesSrv.URL)

	_, eventsHandler := orreryv1connect.NewEventsHandler(eventsServer{
		reader: paneFeedEvents{reads: rig.eventReads}, changes: hub, sendTimeout: defaultEventSendTimeout,
	})
	eventsSrv := httptest.NewServer(withResponseWriter(eventsHandler))
	t.Cleanup(eventsSrv.Close)
	rig.eventsURL = eventsSrv.URL
	return rig
}

// paneFeedStream is one WatchConversation stream a feed test opens. msgs carries each message and is closed when the
// stream ends, err is the reason it ended, and done closes once the goroutine that reads the stream has returned.
type paneFeedStream struct {
	msgs   chan *orreryv1.WatchConversationResponse
	done   chan struct{}
	cancel context.CancelFunc
	err    error
}

// paneFeedOpen opens WatchConversation on pane on the rig's server. The stream ends at cleanup.
func paneFeedOpen(t *testing.T, rig *paneFeedRig, pane string) *paneFeedStream {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	st := &paneFeedStream{
		msgs:   make(chan *orreryv1.WatchConversationResponse),
		done:   make(chan struct{}),
		cancel: cancel,
	}
	go st.run(ctx, rig.panes, &orreryv1.WatchConversationRequest{Pane: pane})
	t.Cleanup(func() { paneFeedStop(st) })
	return st
}

// run forwards the stream's messages to msgs, then closes msgs once err holds why the stream ended.
func (st *paneFeedStream) run(
	ctx context.Context,
	client orreryv1connect.PanesClient,
	req *orreryv1.WatchConversationRequest,
) {
	defer close(st.done)
	st.err = st.forward(ctx, client, req)
	close(st.msgs)
}

// forward sends each message of the stream to msgs until the stream ends or ctx ends, and returns why it ended.
func (st *paneFeedStream) forward(
	ctx context.Context,
	client orreryv1connect.PanesClient,
	req *orreryv1.WatchConversationRequest,
) error {
	stream, err := client.WatchConversation(ctx, connect.NewRequest(req))
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

// paneFeedStop ends st and waits until its goroutine has returned, so no message or read the stream makes is still on
// its way.
func paneFeedStop(st *paneFeedStream) {
	st.cancel()
	<-st.done
}

// paneFeedNext returns the next message of st, failing the test when the stream ends first.
func paneFeedNext(t *testing.T, st *paneFeedStream) *orreryv1.WatchConversationResponse {
	t.Helper()
	msg, ok := <-st.msgs
	if !ok {
		t.Fatalf("stream ended before the next message: %v", st.err)
	}
	return msg
}

// paneFeedAssertQuiet fails the test when st has a message ready to receive, or has ended.
func paneFeedAssertQuiet(t *testing.T, st *paneFeedStream) {
	t.Helper()
	select {
	case msg, ok := <-st.msgs:
		t.Fatalf("stream sent %v (open %t), want no message", msg, ok)
	default:
	}
}

// paneFeedIsKeepalive reports whether msg is a keepalive: a message with no reset, items or state.
func paneFeedIsKeepalive(msg *orreryv1.WatchConversationResponse) bool {
	return !msg.GetReset_() && len(msg.GetItems()) == 0 && msg.GetState() == nil
}

// paneFeedWantItems fails the test unless msg holds items with the texts want, in order.
func paneFeedWantItems(t *testing.T, msg *orreryv1.WatchConversationResponse, want ...string) {
	t.Helper()
	got := make([]string, 0, len(msg.GetItems()))
	for _, item := range msg.GetItems() {
		got = append(got, item.GetText())
	}
	if !slices.Equal(got, want) {
		t.Fatalf("items = %q, want %q", got, want)
	}
}

// paneFeedAwaitSteps receives n steps from rig and returns the pane each one reads. It has no deadline of its own.
func paneFeedAwaitSteps(rig *paneFeedRig, n int) []string {
	panes := make([]string, 0, n)
	for range n {
		panes = append(panes, <-rig.steps)
	}
	return panes
}

// paneFeedDrain returns the values waiting on ch, and takes them.
func paneFeedDrain(ch chan string) []string {
	var got []string
	for {
		select {
		case v := <-ch:
			got = append(got, v)
		default:
			return got
		}
	}
}

// paneFeedWantPanes fails the test unless got holds the panes want, in any order.
func paneFeedWantPanes(t *testing.T, got []string, want ...string) {
	t.Helper()
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("read panes %q, want %q", got, want)
	}
}

// TestPaneWatchStreamFeedIdleReadsNothing proves that a pane stream whose feed reports no change reads its pane only
// at open, and once more when the feed's opened message wakes it. The streams are stopped as the barrier: each returns
// only after its wait ends, so any read it made while open is counted before the check.
func TestPaneWatchStreamFeedIdleReadsNothing(t *testing.T) {
	t.Parallel()
	rig := paneFeedSetup(t)
	p := paneFeedPane(t, rig.tmux, "feed-p", paneFeedTranscript(t), paneFeedOrreryP)
	q := paneFeedPane(t, rig.tmux, "feed-q", paneFeedTranscript(t), paneFeedOrreryQ)
	sp := paneFeedOpen(t, rig, p)
	sq := paneFeedOpen(t, rig, q)
	paneFeedWantPanes(t, paneFeedAwaitSteps(rig, 2), p, q)
	paneFeedNext(t, sp)
	paneFeedNext(t, sq)

	call := <-rig.changes.calls
	call.msgs <- &enginev1.ChangesWatchResponse{Opened: true}
	paneFeedWantPanes(t, paneFeedAwaitSteps(rig, 2), p, q)

	paneFeedStop(sp)
	paneFeedStop(sq)
	paneFeedWantPanes(t, paneFeedDrain(rig.steps))
}

// TestPaneWatchStreamFeedWakesOnlyItsSession proves that a feed message naming a pane's @orrery_id wakes that pane's
// stream and no other: the pane is read once and sends one delta, and the other pane is neither read nor sent to.
func TestPaneWatchStreamFeedWakesOnlyItsSession(t *testing.T) {
	t.Parallel()
	rig := paneFeedSetup(t)
	pathP := paneFeedTranscript(t)
	p := paneFeedPane(t, rig.tmux, "feed-p", pathP, paneFeedOrreryP)
	q := paneFeedPane(t, rig.tmux, "feed-q", paneFeedTranscript(t), paneFeedOrreryQ)
	sp := paneFeedOpen(t, rig, p)
	sq := paneFeedOpen(t, rig, q)
	paneFeedWantPanes(t, paneFeedAwaitSteps(rig, 2), p, q)
	// A stream names its session after its first step, and before its first delta. Each delta is awaited here, so
	// the wake below reaches only streams that name their sessions.
	paneFeedNext(t, sp)
	paneFeedNext(t, sq)

	call := <-rig.changes.calls
	paneFeedAppend(t, pathP, paneFeedReply(2))
	call.msgs <- &enginev1.ChangesWatchResponse{Seq: 1, Sessions: []string{paneFeedOrreryP}}
	paneFeedWantItems(t, paneFeedNext(t, sp), "synthetic reply 2")
	paneFeedWantPanes(t, paneFeedDrain(rig.steps), p)
	paneFeedAssertQuiet(t, sp)
	paneFeedAssertQuiet(t, sq)
}

// TestPaneWatchStreamFeedReopenReadsOnce proves that when the feed's call ends, the retry timer reopens it, and the
// reopened call's opened message makes each stream read once from its position. A line appended while the feed is down
// reaches its pane's stream exactly once.
func TestPaneWatchStreamFeedReopenReadsOnce(t *testing.T) {
	t.Parallel()
	rig := paneFeedSetup(t)
	pathP := paneFeedTranscript(t)
	p := paneFeedPane(t, rig.tmux, "feed-p", pathP, paneFeedOrreryP)
	q := paneFeedPane(t, rig.tmux, "feed-q", paneFeedTranscript(t), paneFeedOrreryQ)
	sp := paneFeedOpen(t, rig, p)
	sq := paneFeedOpen(t, rig, q)
	paneFeedWantPanes(t, paneFeedAwaitSteps(rig, 2), p, q)
	paneFeedNext(t, sp)
	paneFeedNext(t, sq)

	first := <-rig.changes.calls
	close(first.end)
	retry := <-rig.retries.armed
	if retry.after != feedRetry {
		t.Fatalf("retry timer armed for %v, want %v", retry.after, feedRetry)
	}
	// The feed is down, so this line wakes no stream.
	paneFeedAppend(t, pathP, paneFeedReply(2))
	retry.fire <- time.Time{}

	reopened := <-rig.changes.calls
	reopened.msgs <- &enginev1.ChangesWatchResponse{Opened: true}
	paneFeedWantPanes(t, paneFeedAwaitSteps(rig, 2), p, q)
	paneFeedWantItems(t, paneFeedNext(t, sp), "synthetic reply 2")
	paneFeedWantPanes(t, paneFeedDrain(rig.steps))
	paneFeedAssertQuiet(t, sq)

	// Line 2 was delivered once, so the next change brings line 3 and no repeat of line 2.
	paneFeedAppend(t, pathP, paneFeedReply(3))
	reopened.msgs <- &enginev1.ChangesWatchResponse{Seq: 2, Sessions: []string{paneFeedOrreryP}}
	paneFeedWantItems(t, paneFeedNext(t, sp), "synthetic reply 3")
}

// TestPaneWatchStreamFeedKeepaliveDeadline proves that a pane stream sends its keepalive when its keepalive timer
// fires, and not before. The timer is armed for the keepalive interval after the first delta. When it fires, the stream
// sends one keepalive with the transcript and seq it holds, reports the pane read once, and reads no pane.
func TestPaneWatchStreamFeedKeepaliveDeadline(t *testing.T) {
	t.Parallel()
	rig := paneFeedSetup(t)
	path := paneFeedTranscript(t)
	p := paneFeedPane(t, rig.tmux, "feed-p", path, paneFeedOrreryP)
	st := paneFeedOpen(t, rig, p)
	paneFeedWantPanes(t, paneFeedAwaitSteps(rig, 1), p)
	first := paneFeedNext(t, st)
	paneFeedWantPanes(t, paneFeedDrain(rig.reports), p)

	keepalive := <-rig.keeps.armed
	if keepalive.after != paneFeedKeepalive {
		t.Fatalf("keepalive timer armed for %v, want %v", keepalive.after, paneFeedKeepalive)
	}
	paneFeedAssertQuiet(t, st)
	paneFeedWantPanes(t, paneFeedDrain(rig.steps))
	paneFeedWantPanes(t, paneFeedDrain(rig.reports))

	keepalive.fire <- time.Time{}
	msg := paneFeedNext(t, st)
	if !paneFeedIsKeepalive(msg) || msg.GetTranscript() != filepath.Base(path) || msg.GetSeq() != first.GetSeq() {
		t.Fatalf("keepalive transcript=%q seq=%d reset=%v items=%d state=%v, want a keepalive of %q at seq %d",
			msg.GetTranscript(), msg.GetSeq(), msg.GetReset_(), len(msg.GetItems()), msg.GetState(),
			filepath.Base(path), first.GetSeq())
	}
	paneFeedWantPanes(t, paneFeedDrain(rig.reports), p)
	paneFeedWantPanes(t, paneFeedDrain(rig.steps))
	if again := <-rig.keeps.armed; again.after != paneFeedKeepalive {
		t.Fatalf("keepalive timer re-armed for %v, want %v", again.after, paneFeedKeepalive)
	}
}

// TestPaneWatchStreamFeedOneSubscription proves that any number of pane streams, and an Events.Watch stream on the same
// hub, share one Changes.Watch call.
func TestPaneWatchStreamFeedOneSubscription(t *testing.T) {
	t.Parallel()
	rig := paneFeedSetup(t)
	p := paneFeedPane(t, rig.tmux, "feed-p", paneFeedTranscript(t), paneFeedOrreryP)
	q := paneFeedPane(t, rig.tmux, "feed-q", paneFeedTranscript(t), paneFeedOrreryQ)
	paneFeedOpen(t, rig, p)
	paneFeedOpen(t, rig, q)
	eventsFeedOpen(t, &eventsFeedRig{url: rig.eventsURL}, 0)
	// Each stream reads after it subscribes, so the reads show that three streams hold subscriptions. The one call is
	// announced once it has started.
	paneFeedWantPanes(t, paneFeedAwaitSteps(rig, 2), p, q)
	<-rig.eventReads
	<-rig.changes.calls
	if got := rig.changes.watches.Load(); got != 1 {
		t.Fatalf("Changes.Watch calls: %d, want 1 for three open streams", got)
	}
}
