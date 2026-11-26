package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"tailscale.com/tailcfg"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/hostrelay"
	"github.com/joshsymonds/orrery/internal/hosts"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// localControllerName is the orrery name of the controller these tests run as. Its desk stream names it as the host of
// each line.
const localControllerName = "controller"

// localSeen is one seen report that reached the controller's router: the key the router was given, and the seen time.
type localSeen struct {
	key  notice.Key
	seen int64
}

// localRouter is the controller's notice router. Seen sends each report on seen, which the test reads. The other Router
// methods are not reached by these tests.
type localRouter struct {
	enginehost.Router
	seen chan localSeen
}

// Seen sends the report to the test. It gives up when ctx ends, so a test that has stopped reading holds no handler.
func (r *localRouter) Seen(ctx context.Context, key notice.Key, seen int64) {
	select {
	case r.seen <- localSeen{key: key, seen: seen}:
	case <-ctx.Done():
	}
}

// localCommands is the controller's session commands. Start answers with the id the client chose, as a session that
// the controller starts under that id does, and Say and Stop record what they were asked. The other Commands are not
// reached by these tests.
type localCommands struct {
	enginehost.Commands
	mu      sync.Mutex
	started []enginehost.StartRequest
	said    []enginehost.SayRequest
	stopped []string
}

// Start records the request and answers with its id.
func (c *localCommands) Start(_ context.Context, req enginehost.StartRequest) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.started = append(c.started, req)
	return req.ID, nil
}

// Say records the request.
func (c *localCommands) Say(_ context.Context, req enginehost.SayRequest) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.said = append(c.said, req)
	return nil
}

// Stop records the id.
func (c *localCommands) Stop(_ context.Context, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopped = append(c.stopped, id)
	return nil
}

// record returns the requests and ids that the commands have recorded so far.
func (c *localCommands) record() ([]enginehost.StartRequest, []enginehost.SayRequest, []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.started), slices.Clone(c.said), slices.Clone(c.stopped)
}

// watchSignal is a streaming interceptor that closes signal once a stream has sent its first message. The first message
// of WatchDesk is the hello line, which the Fleet service sends only once its stream has subscribed to the desk, so a
// notice posted after signal is closed reaches the stream.
type watchSignal struct {
	signal chan struct{}
	once   sync.Once
}

// WrapUnary returns next, since the signal is for streams only.
func (w *watchSignal) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return next
}

// WrapStreamingClient returns next, since the controller's handlers are the ones that send.
func (w *watchSignal) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

// WrapStreamingHandler returns next with a connection whose first successful send closes the signal.
func (w *watchSignal) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		return next(ctx, &sentSignal{StreamingHandlerConn: conn, watch: w})
	}
}

// sentSignal is a stream handler's connection, which reports each successful send to its watchSignal.
type sentSignal struct {
	connect.StreamingHandlerConn
	watch *watchSignal
}

// Send sends msg, and closes the watchSignal's signal once a send succeeds.
func (s *sentSignal) Send(msg any) error {
	if err := s.StreamingHandlerConn.Send(msg); err != nil {
		return fmt.Errorf("send on the stream: %w", err)
	}
	s.watch.once.Do(func() { close(s.watch.signal) })
	return nil
}

// localController is the controller these tests run as. Its relay admits agent b of hosts.toml, and its handler serves
// the session commands and the Fleet service, with the router and the desk hub. Its unary calls are recorded in calls,
// and watching closes once its first desk stream has subscribed.
type localController struct {
	relayAddr string
	calls     *nodeCalls
	commands  *localCommands
	router    *localRouter
	desk      *deskstream.Hub
	watching  <-chan struct{}
}

// startLocalController starts the controller on a free loopback address. Its relay runs until the test ends, and its
// desk hub is closed at the end too.
func startLocalController(t *testing.T) *localController {
	t.Helper()
	relayAddr := freeNodeAddr(t)
	whois := nodeWhoIs{
		relayAddr: relayAddr,
		relay:     nodeAnswer(&tailcfg.Node{ComputedName: "orreryd-box"}),
		peer:      nodeAnswer(&tailcfg.Node{Name: "b.tail1234.ts.net.", ComputedName: "b"}),
	}
	watch := &watchSignal{signal: make(chan struct{})}
	ctrl := &localController{
		relayAddr: relayAddr,
		calls:     &nodeCalls{},
		commands:  &localCommands{},
		router:    &localRouter{seen: make(chan localSeen, 1)},
		desk:      deskstream.NewHub(localControllerName, nil),
		watching:  watch.signal,
	}
	t.Cleanup(ctrl.desk.Close)
	handler := enginehost.NewHandler(enginehost.ServeConfig{
		Commands: ctrl.commands,
		Fleet:    &enginehost.Fleet{Router: ctrl.router, Desk: ctrl.desk},
		Options:  []connect.HandlerOption{connect.WithInterceptors(ctrl.calls.interceptor(), watch)},
	})
	relay, err := hostrelay.Listen(t.Context(), hostrelay.Config{
		Address: relayAddr,
		Hosts:   []hosts.Host{{Name: "b", Address: "b.tail1234.ts.net:7443"}},
		WhoIs:   whois,
		Handler: handler,
		Secret:  nodeSecret,
	})
	if err != nil {
		t.Fatal(err)
	}
	relayCtx, cancel := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() { served <- relay.Serve(relayCtx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-served; err != nil {
			t.Error(err)
		}
	})
	return ctrl
}

// readDeskLine reads the next line that the desk stream writes, and decodes it. It fails when the stream ends first.
func readDeskLine(t *testing.T, lines *bufio.Scanner) deskstream.Line {
	t.Helper()
	if !lines.Scan() {
		t.Fatalf("the notices stream ended: %v", lines.Err())
	}
	var line deskstream.Line
	if err := json.Unmarshal(lines.Bytes(), &line); err != nil {
		t.Fatalf("decode the notices line %q: %v", lines.Text(), err)
	}
	return line
}

// buildLocalOrrery builds orrery into a directory of its own, and returns the path of the binary. The desk runs its own
// orrery as a process, which a test binary cannot stand in for, so the desk test runs this binary.
func buildLocalOrrery(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "orrery")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", bin, "github.com/joshsymonds/orrery/cmd/orrery")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build orrery: %v\n%s", err, out)
	}
	return bin
}

// TestNodeLocalVerbsSocketsAreOwnerOnly checks that agent b's daemon socket and notices.sock exist as sockets that only
// their owner can use, as sessions.sock does.
func TestNodeLocalVerbsSocketsAreOwnerOnly(t *testing.T) {
	t.Parallel()
	ctrl := startLocalController(t)
	daemonSocket := startNodeForwardAgent(t, ctrl.relayAddr)
	for _, sock := range []string{daemonSocket, deskstream.SocketPath(daemonSocket)} {
		info, err := os.Stat(sock)
		if err != nil {
			t.Fatalf("stat %s: %v", sock, err)
		}
		if info.Mode().Type() != fs.ModeSocket {
			t.Fatalf("%s is %v, want a socket", sock, info.Mode().Type())
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("%s mode = %o, want 600", sock, perm)
		}
	}
}

// TestNodeLocalVerbsSessionsAskAnswersOn checks that asking agent b's daemon socket whether sessions run is answered
// on. It is the question the CLI asks before it retries a session command that never connected.
func TestNodeLocalVerbsSessionsAskAnswersOn(t *testing.T) {
	t.Parallel()
	ctrl := startLocalController(t)
	daemonSocket := startNodeForwardAgent(t, ctrl.relayAddr)
	conn, err := (&net.Dialer{}).DialContext(t.Context(), "unix", daemonSocket)
	if err != nil {
		t.Fatalf("dial the daemon socket: %v", err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := protocol.WriteFrame(conn, protocol.HookFrame{Ask: protocol.SessionsAsk}); err != nil {
		t.Fatalf("ask the daemon socket: %v", err)
	}
	on, err := protocol.ReadSessionsAnswer(conn)
	if err != nil {
		t.Fatalf("read the answer: %v", err)
	}
	if !on {
		t.Fatal("sessions answer is off, want on")
	}
}

// TestNodeLocalVerbsSessionStartSayStop checks that `orrery session` start, say and stop run on agent b reach the
// controller as host b. Each reaches the controller's session command with the arguments the CLI gave it, and the
// controller's answer comes back to the CLI.
func TestNodeLocalVerbsSessionStartSayStop(t *testing.T) {
	t.Parallel()
	ctrl := startLocalController(t)
	daemonSocket := startNodeForwardAgent(t, ctrl.relayAddr)
	getenv := forwardGetenv(daemonSocket)
	dir := t.TempDir()
	code, stdout, stderr := runNodeCLI(t, getenv, "session", "start", dir, "first prompt")
	if code != 0 {
		t.Fatalf("session start: exit code = %d, want 0; stderr %q", code, stderr)
	}
	id := strings.TrimSpace(stdout)
	if id == "" {
		t.Fatal("session start printed no id")
	}
	code, _, stderr = runNodeCLI(t, getenv, "session", "say", id, "a follow-up")
	if code != 0 {
		t.Fatalf("session say: exit code = %d, want 0; stderr %q", code, stderr)
	}
	code, _, stderr = runNodeCLI(t, getenv, "session", "stop", id)
	if code != 0 {
		t.Fatalf("session stop: exit code = %d, want 0; stderr %q", code, stderr)
	}
	started, said, stopped := ctrl.commands.record()
	if len(started) != 1 || started[0].ID != id || started[0].Dir != dir || started[0].Prompt != "first prompt" {
		t.Fatalf("controller started %+v, want session %s in %s with the first prompt", started, id, dir)
	}
	if len(said) != 1 || said[0].ID != id || said[0].Text != "a follow-up" {
		t.Fatalf("controller said %+v, want a follow-up to session %s", said, id)
	}
	if !slices.Equal(stopped, []string{id}) {
		t.Fatalf("controller stopped %q, want [%s]", stopped, id)
	}
	requireForwardCalls(t, ctrl.calls,
		nodeCall{procedure: enginev1connect.EngineStartProcedure, host: "b"},
		nodeCall{procedure: enginev1connect.EngineSayProcedure, host: "b"},
		nodeCall{procedure: enginev1connect.EngineStopProcedure, host: "b"},
	)
}

// TestNodeLocalVerbsSeenReportArrivesAsHostB checks that `orrery seen` run on agent b reports the pane seen through the
// daemon socket, and that the controller's router records the pane under host b.
func TestNodeLocalVerbsSeenReportArrivesAsHostB(t *testing.T) {
	t.Parallel()
	ctrl := startLocalController(t)
	daemonSocket := startNodeForwardAgent(t, ctrl.relayAddr)
	server := serveNewTmux(t)
	pane := serveStartPane(t, server)
	code, _, stderr := runNodeCLI(t, forwardGetenv(daemonSocket), "-L", server.Name, "seen", "--pane", pane)
	if code != 0 {
		t.Fatalf("seen: exit code = %d, want 0; stderr %q", code, stderr)
	}
	got := <-ctrl.router.seen
	if want := (notice.Key{Pane: pane, Host: "b"}); got.key != want {
		t.Fatalf("router recorded key %+v, want %+v", got.key, want)
	}
	if got.seen == 0 {
		t.Fatal("router recorded seen time 0")
	}
	requireForwardCalls(t, ctrl.calls, nodeCall{procedure: enginev1connect.FleetReportSeenProcedure, host: "b"})
}

// TestNodeLocalVerbsNoticesStreamTheController checks that `orrery notices` run on agent b prints the controller's desk
// stream: its hello line, then each notice that the controller posts, each naming the controller as its host.
func TestNodeLocalVerbsNoticesStreamTheController(t *testing.T) {
	t.Parallel()
	ctrl := startLocalController(t)
	daemonSocket := startNodeForwardAgent(t, ctrl.relayAddr)
	stdinR, stdinW := mustPipe(t)
	t.Cleanup(func() { closeFile(t, stdinR) })
	stdoutR, stdoutW := io.Pipe()
	done := make(chan int, 1)
	go func() {
		done <- run([]string{"notices"}, stdinR, stdoutW, io.Discard, forwardGetenv(daemonSocket))
		if err := stdoutW.Close(); err != nil {
			t.Error(err)
		}
	}()
	lines := bufio.NewScanner(stdoutR)
	hello := readDeskLine(t, lines)
	if hello.Kind != deskstream.KindHello || hello.Host != localControllerName {
		t.Fatalf("first line = %+v, want the hello line of %s", hello, localControllerName)
	}
	ctrl.desk.Post(deskstream.Notice{State: "done", Title: "notices-title", Body: "notices-body"})
	line := readDeskLine(t, lines)
	want := deskstream.Line{
		Kind:  deskstream.KindNotice,
		Host:  localControllerName,
		State: "done",
		Title: "notices-title",
		Body:  "notices-body",
	}
	if line != want {
		t.Fatalf("notice line = %+v, want %+v", line, want)
	}
	closeFile(t, stdinW)
	for lines.Scan() {
		continue
	}
	if err := lines.Err(); err != nil {
		t.Fatalf("read the notices stream: %v", err)
	}
	if code := <-done; code != 0 {
		t.Fatalf("orrery notices: exit code = %d, want 0", code)
	}
}

// TestNodeLocalVerbsDeskNamesController runs `orrery desk` on agent b, naming the controller as a host. The desk's
// local link runs `orrery notices` on b, so the controller's notice reaches the desk, which shows it as a popup. The
// desk logs each popup it fails to show, since its PATH holds no notify-send, and the test reads that log for the
// notice's title.
func TestNodeLocalVerbsDeskNamesController(t *testing.T) {
	t.Parallel()
	ctrl := startLocalController(t)
	daemonSocket := startNodeForwardAgent(t, ctrl.relayAddr)
	orrery := buildLocalOrrery(t)
	cmd := exec.CommandContext(t.Context(), orrery, "--socket", daemonSocket, "desk",
		"--sound", filepath.Join(t.TempDir(), "sound.wav"), "--host", localControllerName)
	cmd.Env = []string{"PATH=" + t.TempDir()}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start orrery desk: %v", err)
	}
	// The controller sends the first line of a desk stream only once the stream has subscribed to the desk, so a notice
	// posted after this reaches the desk's local link.
	<-ctrl.watching
	ctrl.desk.Post(deskstream.Notice{State: "done", Title: "desk-notice-title", Body: "desk-notice-body"})
	logs := bufio.NewScanner(stderr)
	found := false
	for logs.Scan() {
		text := logs.Text()
		if strings.Contains(text, "show popup") && strings.Contains(text, "title=desk-notice-title") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("orrery desk logged no popup for the controller's notice; last read error: %v", logs.Err())
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("interrupt orrery desk: %v", err)
	}
	if _, err := io.Copy(io.Discard, stderr); err != nil {
		t.Fatalf("drain the desk's log: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("orrery desk after an interrupt: %v", err)
	}
}
