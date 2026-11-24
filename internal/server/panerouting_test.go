package server_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	enginev1connect "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/holderhost"
	"github.com/joshsymonds/orrery/internal/node"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/server"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
	"github.com/joshsymonds/orrery/internal/waittest"
)

const (
	// routingController is the controller's orrery name, which its own host is served on, and routingHost is the orrery
	// name of the host whose agent the routing tests reach.
	routingController = "a"
	routingHost       = "b"
	// routingPane is the pane that each host's tmux server starts fourth, so its ID is the same on both hosts.
	routingPane = "%3"
	// routedText is the text the routing tests send, and routedPaste is what a paste of it leaves in an input pane: the
	// text between bracketed-paste markers, then Enter.
	routedText  = "routed text"
	routedPaste = "\x1b[200~" + routedText + "\x1b[201~\r"
	// routedSeen is the seen time a MarkSeen of the routing tests sets, and routedSeenBefore the one the controller's
	// own pane starts with, so each test can tell whether a seen time moved.
	routedSeen       = 1794301100
	routedSeenBefore = "1700000000"
)

// neverTimer is a NewTimer whose timers never fire, so no keepalive of the routing tests waits on the clock.
func neverTimer(time.Duration) (<-chan time.Time, func() bool) {
	return make(chan time.Time), func() bool { return true }
}

// quietClock is the clock of the routing tests' clients. Its timers never fire, so no call of a client is bounded by
// the clock, and each call waits only on its answer.
type quietClock struct{}

// After returns a channel that never receives.
func (quietClock) After(time.Duration) <-chan time.Time {
	return make(chan time.Time)
}

// routingWhoIs names the agent's own address as host, and every other address as the controller. Both are signed in as
// the login the controller and the agent share.
type routingWhoIs struct {
	own  string
	host string
}

// WhoIs names addr's node as its peer.
func (w routingWhoIs) WhoIs(_ context.Context, addr string) (*apitype.WhoIsResponse, error) {
	name := routingController
	if addr == w.own {
		name = w.host
	}
	return &apitype.WhoIsResponse{
		Node:        &tailcfg.Node{ComputedName: name},
		UserProfile: &tailcfg.UserProfile{LoginName: allowedLogin},
	}, nil
}

// routingPanes is the node.HostPanes of a routing test's agent, which serves its machine's panes through handler. No
// routing test watches the agent's panes or asks it for its load.
type routingPanes struct {
	handler http.Handler
}

// Handler returns the handler that serves the panes.
func (p routingPanes) Handler() http.Handler {
	return p.handler
}

// Watch waits for its context, since no routing test watches the panes.
func (routingPanes) Watch(ctx context.Context, _ func(node.PaneEvent) error) error {
	<-ctx.Done()
	return fmt.Errorf("the routing test's watch ends: %w", ctx.Err())
}

// Load answers an empty load, since no routing test asks for one.
func (routingPanes) Load(context.Context) (node.Load, error) {
	return node.Load{}, nil
}

// routingHolders is the holderhost.Host of a routing test's agent. No Panes call reaches a holder, so it has no
// methods.
type routingHolders struct {
	holderhost.Host
}

// startRoutingAgent serves an agent for routingHost, whose panes are the panes of the tmux server tmux, on a loopback
// listener until the test ends. It returns the client that a controller reaches the agent through.
func startRoutingAgent(t *testing.T, tmux *tmuxctl.Client) *node.Client {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	_, handler := server.HostPanesHandler(server.HostPanesConfig{Tmux: tmux, Host: routingHost, NewTimer: neverTimer})
	agent, err := node.NewAgent(node.AgentConfig{
		Controller: routingController,
		WhoIs:      routingWhoIs{own: ln.Addr().String(), host: routingHost},
		Holders:    routingHolders{},
		Panes:      routingPanes{handler: handler},
	})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() { served <- agent.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		if serveErr := <-served; serveErr != nil {
			t.Errorf("serve the agent of %s: %v", routingHost, serveErr)
		}
	})
	return node.NewClient(node.ClientConfig{
		Name:    routingHost,
		Address: ln.Addr().String(),
		Dial:    (&net.Dialer{}).DialContext,
		Clock:   quietClock{},
	})
}

// routingClient serves a controller over the tmux server tmux, with the settings of cfg, which the routing tests name
// the controller's own host in, and returns the Panes client that reaches it.
func routingClient(t *testing.T, tmux *tmuxctl.Client, cfg server.Config) orreryv1connect.PanesClient {
	t.Helper()
	cfg.Logins = []string{allowedLogin}
	cfg.Tmux = tmux
	cfg.Push = newPushStore(t)
	cfg.HostName = routingController
	cfg.NewTimer = neverTimer
	handler, err := server.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return panesClient(t, handler)
}

// routingScreen is the screen of a routing test's pane on host: the host's name, then an idle input box that shows
// queued messages, as Claude Code draws it while a pull can take them back.
func routingScreen(host string) string {
	rule := strings.Repeat("─", typingWidth)
	return "synthetic screen of " + host + "\n" + rule + "\n❯ " + pqDim + "\n" + rule
}

// startRoutingPane starts sessions until the pane of the fourth is routingPane, with routingScreen of host on its
// screen and the command that Claude Code runs recorded on it, so the sends and pulls of the routing tests find a live
// Claude. It returns the pane and the file its input is recorded in.
func startRoutingPane(t *testing.T, tmux *tmuxctl.Client, host string) (string, string) {
	t.Helper()
	for i := range 3 {
		startSession(t, tmux, fmt.Sprintf("s%d", i), longCommand)
	}
	pane, file := startInputPane(t, tmux, "s3", routingScreen(host))
	if pane != routingPane {
		t.Fatalf("the fourth pane on host %s is %s, want %s", host, pane, routingPane)
	}
	setRoutedOptions(t, tmux, pane, map[string]string{protocol.OptionCommand: "cat"})
	return pane, file
}

// setRoutedOptions sets each of options on pane.
func setRoutedOptions(t *testing.T, tmux *tmuxctl.Client, pane string, options map[string]string) {
	t.Helper()
	for name, value := range options {
		if err := tmux.SetPaneOption(t.Context(), pane, name, value); err != nil {
			t.Fatalf("SetPaneOption(%s, %s): %v", pane, name, err)
		}
	}
}

// awaitRoutedInput waits until the input file bFile, of the pane a routing test sends to on host b, satisfies ok, and
// returns its content. It fails at once when the controller's own pane, whose input file is aFile, receives input, or
// when ended closes, since the input ok waits for can then no longer arrive. So a call routed to the wrong pane fails
// the test rather than leaving it to wait.
func awaitRoutedInput(t *testing.T, ended <-chan struct{}, aFile, bFile string, ok func(string) bool) string {
	t.Helper()
	var data []byte
	waittest.UntilIdle(t, func() bool {
		own, err := os.ReadFile(aFile)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("read input: %v", err)
		}
		select {
		case <-ended:
			return true
		default:
		}
		return len(own) > 0
	}, "the routed input in "+bFile, func() bool {
		var err error
		data, err = os.ReadFile(bFile)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("read input: %v", err)
		}
		return ok(string(data))
	})
	return string(data)
}

// awaitRoutedBytes waits until bFile, as awaitRoutedInput waits, holds want, and fails unless it holds exactly want.
func awaitRoutedBytes(t *testing.T, ended <-chan struct{}, aFile, bFile, want string) {
	t.Helper()
	got := awaitRoutedInput(t, ended, aFile, bFile, func(s string) bool { return len(s) >= len(want) })
	if got != want {
		t.Fatalf("input in %s = %q, want %q", bFile, got, want)
	}
}

// routedOption returns the value of option name on pane, which is "" when it is unset.
func routedOption(t *testing.T, tmux *tmuxctl.Client, pane, name string) string {
	t.Helper()
	value, _, err := tmux.PaneOption(t.Context(), pane, name)
	if err != nil {
		t.Fatalf("read %s of %s: %v", name, pane, err)
	}
	return value
}

// routedOptions returns the values of the options names on pane, keyed by name.
func routedOptions(t *testing.T, tmux *tmuxctl.Client, pane string, names ...string) map[string]string {
	t.Helper()
	values := make(map[string]string, len(names))
	for _, name := range names {
		values[name] = routedOption(t, tmux, pane, name)
	}
	return values
}

// routedScreenOf returns the visible text of pane.
func routedScreenOf(t *testing.T, tmux *tmuxctl.Client, pane string) string {
	t.Helper()
	screen, err := tmux.Run(t.Context(), "capture-pane", "-p", "-J", "-t", pane)
	if err != nil {
		t.Fatalf("capture-pane %s: %v", pane, err)
	}
	return screen
}

// routedPrompt returns the transcript line of a prompt the person typed, whose content is text.
func routedPrompt(text string) string {
	return fmt.Sprintf(`{"type":"user","timestamp":%q,"message":{"content":%q}}`, newestStamp0900, text)
}

// routedCall is one Panes call of the routing tests. It names host, and returns the error the call answers with.
type routedCall struct {
	name string
	call func(ctx context.Context, panes orreryv1connect.PanesClient, host string) error
}

// routedCalls returns one call of each Panes method that names a host, each on routingPane.
func routedCalls() []routedCall {
	return []routedCall{
		{"Screen", func(ctx context.Context, panes orreryv1connect.PanesClient, host string) error {
			_, err := panes.Screen(ctx, connect.NewRequest(&orreryv1.ScreenRequest{Pane: routingPane, Host: host}))
			return err
		}},
		{"Conversation", func(ctx context.Context, panes orreryv1connect.PanesClient, host string) error {
			_, err := panes.Conversation(ctx, connect.NewRequest(&orreryv1.ConversationRequest{
				Pane: routingPane, Host: host,
			}))
			return err
		}},
		{"SendText", func(ctx context.Context, panes orreryv1connect.PanesClient, host string) error {
			_, err := panes.SendText(ctx, connect.NewRequest(&orreryv1.SendTextRequest{
				Pane: routingPane, Text: routedText, Host: host,
			}))
			return err
		}},
		{"SendKeys", func(ctx context.Context, panes orreryv1connect.PanesClient, host string) error {
			_, err := panes.SendKeys(ctx, connect.NewRequest(&orreryv1.SendKeysRequest{
				Pane: routingPane, Keys: []string{"Escape"}, Host: host,
			}))
			return err
		}},
		{"MarkSeen", func(ctx context.Context, panes orreryv1connect.PanesClient, host string) error {
			_, err := panes.MarkSeen(ctx, connect.NewRequest(&orreryv1.MarkSeenRequest{Pane: routingPane, Host: host}))
			return err
		}},
		{"PullQueued", func(ctx context.Context, panes orreryv1connect.PanesClient, host string) error {
			_, err := panes.PullQueued(ctx, connect.NewRequest(&orreryv1.PullQueuedRequest{
				Pane: routingPane, Host: host,
			}))
			return err
		}},
		{"Older", func(ctx context.Context, panes orreryv1connect.PanesClient, host string) error {
			_, err := panes.Older(ctx, connect.NewRequest(&orreryv1.OlderRequest{
				Pane: routingPane, Transcript: "transcript.jsonl", Host: host,
			}))
			return err
		}},
		{"WatchConversation", routedWatchError},
	}
}

// routedWatchError opens a WatchConversation of routingPane that names host, and returns the error it answers with. A
// stream that delivers a delta has no error here, so the caller sees the call as having succeeded.
func routedWatchError(ctx context.Context, panes orreryv1connect.PanesClient, host string) error {
	stream, err := panes.WatchConversation(ctx, connect.NewRequest(&orreryv1.WatchConversationRequest{
		Pane: routingPane, Host: host,
	}))
	if err != nil {
		return err
	}
	stream.Receive()
	streamErr := stream.Err()
	if closeErr := stream.Close(); closeErr != nil {
		return errors.Join(streamErr, closeErr)
	}
	return streamErr
}

// requireRoutedError requires err to be a Connect error with code, whose message contains want. what names the call.
func requireRoutedError(t *testing.T, what string, err error, code connect.Code, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s succeeded, want %v with %q", what, code, want)
	}
	if got := connect.CodeOf(err); got != code || !strings.Contains(err.Error(), want) {
		t.Fatalf("%s = %v, want %v with %q", what, err, code, want)
	}
}

// reportCalls records each report that a routing test's controller makes. A report is noted without blocking, so no
// report waits on the test.
type reportCalls struct {
	mu    sync.Mutex
	calls []string
}

// note records call.
func (r *reportCalls) note(call string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call)
}

// noted returns the calls recorded so far.
func (r *reportCalls) noted() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.calls)
}

// routingPaneSends is the PaneSends client of a routing test's controller. It notes each send that it records.
type routingPaneSends struct {
	calls *reportCalls
}

// Record notes the send to the pane it names.
func (p routingPaneSends) Record(
	_ context.Context, req *connect.Request[enginev1.PaneSendsRecordRequest],
) (*connect.Response[enginev1.PaneSendsRecordResponse], error) {
	p.calls.note("send to " + req.Msg.GetPane())
	return connect.NewResponse(&enginev1.PaneSendsRecordResponse{}), nil
}

// Compile-time check that routingPaneSends is the PaneSends client a controller takes.
var _ enginev1connect.PaneSendsClient = routingPaneSends{}

// TestPaneRoutingReachesHost names host b in each routed Panes call on %3. Each call acts on b's %3 through b's agent,
// and the controller's own %3 keeps its screen, options and seen time. The controller reports no read and no seen time,
// and records no send, for a call to a remote host.
func TestPaneRoutingReachesHost(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	a := newTmuxServer(t)
	b := newTmuxServer(t)
	aPane, aFile := startRoutingPane(t, a, routingController)
	bPane, bFile := startRoutingPane(t, b, routingHost)
	bEnded := paneExited(t, b, bPane)

	names := []string{protocol.OptionState, protocol.OptionTranscript, protocol.OptionLabel, protocol.OptionSeen,
		protocol.OptionCommand}
	aTranscript := writeNewestTranscript(t, routedPrompt("a prompt"))
	setRoutedOptions(t, a, aPane, map[string]string{
		protocol.OptionState:      string(protocol.StateWorking),
		protocol.OptionTranscript: aTranscript,
		protocol.OptionLabel:      "a label",
		protocol.OptionSeen:       routedSeenBefore,
	})
	aOptions := routedOptions(t, a, aPane, names...)
	aScreen := routedScreenOf(t, a, aPane)

	reports := &reportCalls{}
	agent := startRoutingAgent(t, b)
	client := routingClient(t, a, server.Config{
		Hosts:      []*node.Client{agent},
		ReportSeen: func(pane string, _ int64) error { reports.note("seen " + pane); return nil },
		ReportRead: func(pane string, _ int64) { reports.note("read " + pane) },
		PaneSends:  routingPaneSends{calls: reports},
	})

	if _, err := client.SendText(ctx, connect.NewRequest(&orreryv1.SendTextRequest{
		Pane: routingPane, Text: routedText, Host: routingHost,
	})); err != nil {
		t.Fatalf("SendText to %s on %s: %v", routingPane, routingHost, err)
	}
	awaitRoutedBytes(t, bEnded, aFile, bFile, routedPaste)

	if _, err := client.SendKeys(ctx, connect.NewRequest(&orreryv1.SendKeysRequest{
		Pane: routingPane, Keys: []string{"Escape"}, Host: routingHost,
	})); err != nil {
		t.Fatalf("SendKeys to %s on %s: %v", routingPane, routingHost, err)
	}
	awaitRoutedBytes(t, bEnded, aFile, bFile, routedPaste+"\x1b")

	readUpTo := int64(routedSeen)
	if _, err := client.MarkSeen(ctx, connect.NewRequest(&orreryv1.MarkSeenRequest{
		Pane: routingPane, ReadUpTo: &readUpTo, Host: routingHost,
	})); err != nil {
		t.Fatalf("MarkSeen of %s on %s: %v", routingPane, routingHost, err)
	}
	if got := routedOption(t, b, bPane, protocol.OptionSeen); got != fmt.Sprint(routedSeen) {
		t.Fatalf("seen of %s on %s = %q, want %d", bPane, routingHost, got, routedSeen)
	}

	screen, err := client.Screen(ctx, connect.NewRequest(&orreryv1.ScreenRequest{Pane: routingPane, Host: routingHost}))
	if err != nil {
		t.Fatalf("Screen of %s on %s: %v", routingPane, routingHost, err)
	}
	if shown := string(screen.Msg.GetScreen()); !strings.Contains(shown, "synthetic screen of "+routingHost) {
		t.Fatalf("Screen of %s on %s = %q, want the screen of b", routingPane, routingHost, shown)
	}

	bTranscript := writeNewestTranscript(t, routedPrompt("b older prompt"), routedPrompt("b newer prompt"))
	setRoutedOptions(t, b, bPane, liveOptions(bTranscript))
	conv, err := client.Conversation(ctx, connect.NewRequest(&orreryv1.ConversationRequest{
		Pane: routingPane, Host: routingHost,
	}))
	if err != nil {
		t.Fatalf("Conversation of %s on %s: %v", routingPane, routingHost, err)
	}
	if texts := itemTexts(conv.Msg.GetItems()); !slices.Contains(texts, "b newer prompt") ||
		slices.Contains(texts, "a prompt") {
		t.Fatalf("Conversation of %s on %s = %q, want b's transcript", routingPane, routingHost, texts)
	}

	older, err := client.Older(ctx, connect.NewRequest(&orreryv1.OlderRequest{
		Pane: routingPane, Transcript: filepath.Base(bTranscript),
		Before: int64(len(routedPrompt("b older prompt")) + 1), Host: routingHost,
	}))
	if err != nil {
		t.Fatalf("Older of %s on %s: %v", routingPane, routingHost, err)
	}
	if texts := itemTexts(older.Msg.GetItems()); !slices.Equal(texts, []string{"b older prompt"}) {
		t.Fatalf("Older of %s on %s = %q, want the prompt before the newest line", routingPane, routingHost, texts)
	}

	stream, err := client.WatchConversation(ctx, connect.NewRequest(&orreryv1.WatchConversationRequest{
		Pane: routingPane, Host: routingHost,
	}))
	if err != nil {
		t.Fatalf("WatchConversation of %s on %s: %v", routingPane, routingHost, err)
	}
	if !stream.Receive() {
		t.Fatalf("WatchConversation of %s on %s receives no delta: %v", routingPane, routingHost, stream.Err())
	}
	first := stream.Msg()
	if first.GetState().GetPane().GetId() != routingPane ||
		!slices.Contains(itemTexts(first.GetItems()), "b newer prompt") {
		t.Fatalf("first delta of %s on %s = %v, want b's conversation", routingPane, routingHost, first)
	}
	if err = stream.Close(); err != nil {
		t.Fatalf("close the WatchConversation of %s on %s: %v", routingPane, routingHost, err)
	}

	pullTranscript := writeNewestTranscript(t, pqLine(t, "enqueue", "alpha line", ""))
	setRoutedOptions(t, b, bPane, map[string]string{protocol.OptionTranscript: pullTranscript})
	pullEnded, pulled := waittest.Closed(pullCall(ctx, client, routingHost))
	// The pull answers only after the test appends its popAll, so a pull that ends first has failed.
	awaitRoutedInput(t, waittest.Any(pullEnded, bEnded), aFile, bFile,
		func(s string) bool { return strings.Contains(s, pqUp) })
	pqAppend(t, pullTranscript, pqLine(t, "popAll", "alpha line", ""))
	if got := pqWait(t, pulled); got.err != nil {
		t.Fatalf("PullQueued of %s on %s: %v", routingPane, routingHost, got.err)
	} else if got.text != "alpha line" {
		t.Fatalf("PullQueued of %s on %s = %q, want %q", routingPane, routingHost, got.text, "alpha line")
	}
	awaitRoutedBytes(t, bEnded, aFile, bFile,
		routedPaste+"\x1b"+pqUp+strings.Repeat(pqDel, len("alpha line")))

	if got := routedOptions(t, a, aPane, names...); !maps.Equal(got, aOptions) {
		t.Fatalf("options of %s on %s = %v, want %v unchanged", aPane, routingController, got, aOptions)
	}
	if got := routedScreenOf(t, a, aPane); got != aScreen {
		t.Fatalf("screen of %s on %s changed to %q", aPane, routingController, got)
	}
	pqAssertQuiet(t, a, aPane, aFile)

	if calls := reports.noted(); len(calls) != 0 {
		t.Fatalf("the controller reports %v for calls to host %s, want none", calls, routingHost)
	}

	listed, err := agent.Panes().List(ctx, connect.NewRequest(&orreryv1.PanesListRequest{}))
	if err != nil {
		t.Fatalf("List of the agent of %s: %v", routingHost, err)
	}
	for _, pane := range listed.Msg.GetPanes() {
		if pane.GetId() == routingPane {
			if pane.GetHost() != routingHost {
				t.Fatalf("the agent lists %s with host %q, want %q", routingPane, pane.GetHost(), routingHost)
			}
			return
		}
	}
	t.Fatalf("the agent of %s does not list %s", routingHost, routingPane)
}

// pullCall runs Panes.PullQueued of routingPane that names host in a goroutine, and returns the channel its result
// arrives on.
func pullCall(ctx context.Context, panes orreryv1connect.PanesClient, host string) <-chan pqResult {
	ch := make(chan pqResult, 1)
	go func() {
		resp, err := panes.PullQueued(ctx, connect.NewRequest(&orreryv1.PullQueuedRequest{
			Pane: routingPane, Host: host,
		}))
		if err != nil {
			ch <- pqResult{err: err}
			return
		}
		ch <- pqResult{text: resp.Msg.GetText()}
	}()
	return ch
}

// TestPaneRoutingUnknownHost names a host that no agent serves, in each routed Panes call, with the controller's hosts
// set to one agent and to none. Each call fails not_found, and sends nothing.
func TestPaneRoutingUnknownHost(t *testing.T) {
	t.Parallel()

	a := newTmuxServer(t)
	b := newTmuxServer(t)
	startRoutingPane(t, a, routingController)
	startRoutingPane(t, b, routingHost)
	agent := startRoutingAgent(t, b)
	const unknown = "c"

	for _, hosts := range []struct {
		name  string
		agent []*node.Client
	}{
		{"hosts configured", []*node.Client{agent}},
		{"no hosts configured", nil},
	} {
		client := routingClient(t, a, server.Config{Hosts: hosts.agent})
		for _, call := range routedCalls() {
			err := call.call(t.Context(), client, unknown)
			requireRoutedError(t, fmt.Sprintf("%s: %s naming %s", hosts.name, call.name, unknown), err,
				connect.CodeNotFound, "no agent for host "+unknown)
		}
	}
}

// TestPaneRoutingUnreachableHost names a host whose agent cannot be dialed. Its call fails unavailable, since the host
// did not answer, and the dial failure is what the controller reports.
func TestPaneRoutingUnreachableHost(t *testing.T) {
	t.Parallel()

	a := newTmuxServer(t)
	startRoutingPane(t, a, routingController)
	down := node.NewClient(node.ClientConfig{
		Name:    routingHost,
		Address: "127.0.0.1:1",
		Dial: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("synthetic dial failure")
		},
		Clock: quietClock{},
	})
	client := routingClient(t, a, server.Config{Hosts: []*node.Client{down}})

	_, err := client.Screen(t.Context(), connect.NewRequest(&orreryv1.ScreenRequest{
		Pane: routingPane, Host: routingHost,
	}))
	requireRoutedError(t, "Screen naming an unreachable host", err, connect.CodeUnavailable, "host unreachable")
}

// TestPaneRoutingNoHost makes each routed Panes call without naming a host. With no other host configured, the
// controller serves the call on its own host, and lists its panes under its own name. Once an agent is configured, each
// such call fails invalid_argument, and the controller's own host is still named.
func TestPaneRoutingNoHost(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	a := newTmuxServer(t)
	b := newTmuxServer(t)
	aPane, aFile := startRoutingPane(t, a, routingController)
	startRoutingPane(t, b, routingHost)

	alone := routingClient(t, a, server.Config{})
	screen, err := alone.Screen(ctx, connect.NewRequest(&orreryv1.ScreenRequest{Pane: routingPane}))
	if err != nil {
		t.Fatalf("Screen of %s naming no host: %v", routingPane, err)
	}
	if shown := string(screen.Msg.GetScreen()); !strings.Contains(shown, "synthetic screen of "+routingController) {
		t.Fatalf("Screen of %s naming no host = %q, want the screen of a", routingPane, shown)
	}
	listed, err := alone.List(ctx, connect.NewRequest(&orreryv1.PanesListRequest{}))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, pane := range listed.Msg.GetPanes() {
		if pane.GetId() == routingPane && pane.GetHost() != routingController {
			t.Fatalf("List names %s with host %q, want %q", routingPane, pane.GetHost(), routingController)
		}
	}

	agent := startRoutingAgent(t, b)
	withAgent := routingClient(t, a, server.Config{Hosts: []*node.Client{agent}})
	for _, call := range routedCalls() {
		err := call.call(ctx, withAgent, "")
		requireRoutedError(t, call.name+" naming no host", err, connect.CodeInvalidArgument, "pane call names no host")
	}
	pqAssertQuiet(t, a, aPane, aFile)

	if _, err = withAgent.Screen(ctx, connect.NewRequest(&orreryv1.ScreenRequest{
		Pane: routingPane, Host: routingController,
	})); err != nil {
		t.Fatalf("Screen of %s naming the controller's host %s: %v", routingPane, routingController, err)
	}
}
