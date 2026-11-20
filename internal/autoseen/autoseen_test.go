package autoseen_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/joshsymonds/orrery/internal/autoseen"
	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
	"github.com/joshsymonds/orrery/internal/tmuxtest"
)

// Every test server starts with -f /dev/null so the user's tmux config never applies.
const (
	noConfig    = "/dev/null"
	testCommand = "sleep 300"
	// typedKey is the key typeIntoClient types. The inner server binds it in its root table to signal typedChannel.
	typedKey     = "x"
	typedChannel = "typed"
	// staleOffset puts the observer's clock past the client's last input by more than the 30 s window.
	staleOffset = 31 * time.Second
)

// Synthetic Claude Code hook events.
const (
	userPromptSubmitHook = `{"hook_event_name":"UserPromptSubmit","prompt":"synthetic prompt"}`
	stopHook             = `{"hook_event_name":"Stop","stop_hook_active":false}`
)

// clock is the observer's now function. The test sets it while daemon goroutines read it.
type clock struct{ nanos atomic.Int64 }

func (c *clock) set(t time.Time) { c.nanos.Store(t.UnixNano()) }

func (c *clock) now() time.Time { return time.Unix(0, c.nanos.Load()) }

// seenReport is one call to the report function passed to autoseen.New.
type seenReport struct {
	pane string
	seen int64
}

// reports records the report calls. The observer makes them in the daemon's connection goroutine.
type reports struct {
	mu  sync.Mutex
	got []seenReport
}

func (r *reports) record(pane string, seen int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, seenReport{pane: pane, seen: seen})
}

func (r *reports) list() []seenReport {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.got)
}

// tmuxServer is a private tmux server under a unique -L name, killed at cleanup.
type tmuxServer struct {
	client *tmuxctl.Client
	server tmuxctl.Server
}

// startServer returns a private tmux server whose name starts with prefix and is unique to this run.
func startServer(t *testing.T, prefix string) tmuxServer {
	t.Helper()
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("random server name: %v", err)
	}
	server := tmuxctl.Server{Name: fmt.Sprintf("%s-%d-%x", prefix, os.Getpid(), suffix)}
	client, err := tmuxctl.New(server)
	if err != nil {
		t.Fatalf("New(%+v): %v", server, err)
	}
	t.Cleanup(func() {
		if err := tmuxtest.Stop(t, server); err != nil {
			t.Errorf("kill-server: %v", err)
		}
	})
	return tmuxServer{client: client, server: server}
}

// fixture is an inner private server with session s1 and two windows, each with one pane. A client
// attached to s1 is viewing window 0, which holds paneA; window 1 holds paneB. The client is a tmux
// attach running in outerPane on a second private server, so keys sent to outerPane reach the client.
type fixture struct {
	inner     tmuxServer
	outer     tmuxServer
	socket    string
	paneA     string
	paneB     string
	outerPane string
}

// newFixture builds the servers, attaches a client to s1, and waits until tmux lists it.
func newFixture(t *testing.T) fixture {
	t.Helper()
	ctx := t.Context()
	inner := startServer(t, "orrery-autoseen-inner")
	if _, err := inner.client.Run(ctx, "-f", noConfig, "new-session", "-d", "-s", "s1",
		"-c", t.TempDir(), testCommand); err != nil {
		t.Fatalf("new-session: %v", err)
	}
	if _, err := inner.client.Run(ctx, "new-window", "-d", "-t", "s1:1",
		"-c", t.TempDir(), testCommand); err != nil {
		t.Fatalf("new-window: %v", err)
	}
	panes, err := inner.client.Run(ctx, "list-panes", "-s", "-t", "s1", "-F", "#{window_index} #{pane_id}")
	if err != nil {
		t.Fatalf("list-panes: %v", err)
	}
	paneByWindow := make(map[string]string)
	for line := range strings.SplitSeq(strings.TrimSpace(panes), "\n") {
		window, pane, ok := strings.Cut(line, " ")
		if !ok {
			t.Fatalf("list-panes line %q: want window index and pane id", line)
		}
		paneByWindow[window] = pane
	}
	socket, err := inner.client.Run(ctx, "display", "-p", "#{socket_path}")
	if err != nil {
		t.Fatalf("display socket_path: %v", err)
	}

	// events is the directory the inner server's client-attached hook appends to. The outer command appends exited
	// to it when the attach ends, so an attach that fails wakes the wait for the client and fails the test.
	events := t.TempDir()
	if _, err := inner.client.Run(ctx, "set-hook", "-g", "client-attached",
		fmt.Sprintf("run-shell -b 'printf . >> %s'", filepath.Join(events, "attached"))); err != nil {
		t.Fatalf("set client-attached hook: %v", err)
	}
	// The root binding runs after tmux has handled each key the client types, so typeIntoClient waits on it.
	if _, err := inner.client.Run(ctx, "bind-key", "-T", "root", typedKey, "wait-for", "-S", typedChannel); err != nil {
		t.Fatalf("bind typed key: %v", err)
	}

	outer := startServer(t, "orrery-autoseen-outer")
	exited := filepath.Join(events, "exited")
	attach := "env -u TMUX tmux -L " + inner.server.Name + " attach -t s1; : > '" + exited + "'"
	if _, err := outer.client.Run(ctx, "-f", noConfig, "new-session", "-d", "-s", "o1",
		"-c", t.TempDir(), attach); err != nil {
		t.Fatalf("outer new-session: %v", err)
	}
	outerPane, err := outer.client.Run(ctx, "list-panes", "-t", "o1", "-F", "#{pane_id}")
	if err != nil {
		t.Fatalf("outer list-panes: %v", err)
	}

	fx := fixture{
		inner:     inner,
		outer:     outer,
		socket:    strings.TrimSpace(socket),
		paneA:     paneByWindow["0"],
		paneB:     paneByWindow["1"],
		outerPane: strings.TrimSpace(outerPane),
	}
	waitFor(t, events, "client attached to s1", func() (bool, error) {
		_, statErr := os.Stat(exited)
		if statErr == nil {
			return false, errors.New("attach ended before the client attached")
		}
		if !errors.Is(statErr, fs.ErrNotExist) {
			return false, statErr
		}
		out, err := inner.client.Run(ctx, "list-clients", "-F", "#{client_name}")
		return strings.TrimSpace(out) != "", err
	})
	return fx
}

// typeIntoClient types one key through the outer pane and returns once tmux has handled it. The inner server's root
// binding for the key signals only after tmux handles the client's input, which moves its client_activity, the value
// the observer reads.
func (fx fixture) typeIntoClient(t *testing.T) {
	t.Helper()
	ctx := t.Context()
	if _, err := fx.outer.client.Run(ctx, "send-keys", "-t", fx.outerPane, "-l", typedKey); err != nil {
		t.Fatalf("send-keys to outer pane: %v", err)
	}
	if _, err := fx.inner.client.Run(ctx, "wait-for", typedChannel); err != nil {
		t.Fatalf("wait for the typed key: %v", err)
	}
}

// watchMask is the inotify events a wait reads: an entry created, written, closed after writing, or moved in.
const watchMask = unix.IN_CREATE | unix.IN_MODIFY | unix.IN_CLOSE_WRITE | unix.IN_MOVED_TO

// waitFor checks check, then blocks until an entry of dir changes, and checks again after each change. It fails the
// test on check's error. The watch is added before the first check, so a change made after a check is an event the
// next read returns. It has no deadline of its own: tmux and the daemon run at the machine's pace, and the test
// binary's timeout ends a wait that never ends.
func waitFor(t *testing.T, dir, what string, check func() (bool, error)) {
	t.Helper()
	fd, err := unix.InotifyInit1(unix.IN_NONBLOCK | unix.IN_CLOEXEC)
	if err != nil {
		t.Fatalf("inotify: %v", err)
	}
	// The descriptor is non-blocking, so the runtime poller serves each read and Close ends a read that is waiting.
	inotify := os.NewFile(uintptr(fd), "wait watch")
	defer func() {
		if err := inotify.Close(); err != nil {
			t.Errorf("close wait watch: %v", err)
		}
	}()
	if _, err := unix.InotifyAddWatch(fd, dir, watchMask); err != nil {
		t.Fatalf("watch %s: %v", dir, err)
	}
	buf := make([]byte, 4096)
	for {
		ok, err := check()
		if err != nil {
			t.Fatalf("waiting for %s: %v", what, err)
		}
		if ok {
			return
		}
		if _, err := inotify.Read(buf); err != nil {
			t.Fatalf("read watch events: %v", err)
		}
	}
}

// serve listens on a fresh socket path, runs Serve with cfg in a goroutine, and returns the path. Cleanup
// cancels Serve and requires it to return nil.
func serve(t *testing.T, cfg daemon.Config) string {
	t.Helper()
	socketPath := filepath.Join(t.TempDir(), "orreryd.sock")
	ln, err := daemon.Listen(socketPath)
	if err != nil {
		t.Fatalf("Listen(%s): %v", socketPath, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- daemon.Serve(ctx, ln, cfg) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})
	return socketPath
}

// frameFor returns a frame for pane whose $TMUX names socket, the way the hook sends it.
func frameFor(hook, pane, socket string) protocol.HookFrame {
	return protocol.HookFrame{
		Hook:     json.RawMessage(hook),
		TmuxPane: pane,
		Tmux:     fmt.Sprintf("%s,1234,0", socket),
	}
}

// sendFrame sends frame to the daemon socket and returns once the daemon closes the connection. The
// daemon closes it only after the frame is handled, so this return is the synchronization point.
func sendFrame(t *testing.T, socketPath string, frame protocol.HookFrame) {
	t.Helper()
	var buf bytes.Buffer
	if err := protocol.WriteFrame(&buf, frame); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	var dialer net.Dialer
	conn, err := dialer.DialContext(t.Context(), "unix", socketPath)
	if err != nil {
		t.Fatalf("dial %s: %v", socketPath, err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close connection: %v", err)
		}
	}()
	if _, err := conn.Write(buf.Bytes()); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	// The daemon closes the connection once it has handled the frame, so the read returns at that close. It has no
	// deadline: handling the frame writes tmux at the machine's pace.
	_, _ = conn.Read(make([]byte, 1))
}

// wantOption checks pane-scoped option name on pane; want "" means unset.
func wantOption(t *testing.T, client *tmuxctl.Client, pane, name, want string) {
	t.Helper()
	got, ok, err := client.PaneOption(t.Context(), pane, name)
	if err != nil {
		t.Fatalf("PaneOption(%s, %s): %v", pane, name, err)
	}
	if want == "" {
		if ok {
			t.Fatalf("%s on %s = %q, want unset", name, pane, got)
		}
		return
	}
	if !ok || got != want {
		t.Fatalf("%s on %s = (%q, %v), want (%q, true)", name, pane, got, ok, want)
	}
}

// TestObserverMarksPaneSeenWhenClientIsViewingIt sends a prompt and a stop for the pane the client is
// viewing and for a pane in another window. Only the viewed pane becomes seen, and only at the stop.
func TestObserverMarksPaneSeenWhenClientIsViewingIt(t *testing.T) {
	fx := newFixture(t)
	var clk clock
	var rep reports
	socket := serve(t, daemon.Config{
		Observers: []daemon.Observer{autoseen.New(clk.now, nil, rep.record, nil)},
	})
	fx.typeIntoClient(t)

	sendFrame(t, socket, frameFor(userPromptSubmitHook, fx.paneA, fx.socket))
	wantOption(t, fx.inner.client, fx.paneA, protocol.OptionState, string(protocol.StateWorking))
	wantOption(t, fx.inner.client, fx.paneA, protocol.OptionSeen, "")

	stopped := time.Now()
	clk.set(stopped)
	sendFrame(t, socket, frameFor(stopHook, fx.paneA, fx.socket))
	wantOption(t, fx.inner.client, fx.paneA, protocol.OptionState, string(protocol.StateDone))
	wantOption(t, fx.inner.client, fx.paneA, protocol.OptionSeen, strconv.FormatInt(stopped.Unix(), 10))

	sendFrame(t, socket, frameFor(userPromptSubmitHook, fx.paneB, fx.socket))
	sendFrame(t, socket, frameFor(stopHook, fx.paneB, fx.socket))
	wantOption(t, fx.inner.client, fx.paneB, protocol.OptionState, string(protocol.StateDone))
	wantOption(t, fx.inner.client, fx.paneB, protocol.OptionSeen, "")

	want := []seenReport{{pane: fx.paneA, seen: stopped.Unix()}}
	if got := rep.list(); !slices.Equal(got, want) {
		t.Fatalf("reports = %v, want %v", got, want)
	}
}

// TestObserverLeavesPaneUnseenWhenClientInputIsStale stops the viewed pane with the observer's clock 31 s
// past the client's last input. The pane must stay unseen.
func TestObserverLeavesPaneUnseenWhenClientInputIsStale(t *testing.T) {
	fx := newFixture(t)
	var clk clock
	var rep reports
	socket := serve(t, daemon.Config{
		Observers: []daemon.Observer{autoseen.New(clk.now, nil, rep.record, nil)},
	})
	fx.typeIntoClient(t)

	sendFrame(t, socket, frameFor(userPromptSubmitHook, fx.paneA, fx.socket))
	clk.set(time.Now().Add(staleOffset))
	sendFrame(t, socket, frameFor(stopHook, fx.paneA, fx.socket))
	wantOption(t, fx.inner.client, fx.paneA, protocol.OptionState, string(protocol.StateDone))
	wantOption(t, fx.inner.client, fx.paneA, protocol.OptionSeen, "")
	if got := rep.list(); len(got) != 0 {
		t.Fatalf("reports = %v, want none", got)
	}
}
