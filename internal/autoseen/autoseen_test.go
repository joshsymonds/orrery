package autoseen_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
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
	// ioTimeout bounds each socket exchange and each wait for tmux to reach the expected state.
	ioTimeout = 5 * time.Second
	// pollInterval spaces the checks made while waiting for tmux to reach the expected state.
	pollInterval = 20 * time.Millisecond
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

	outer := startServer(t, "orrery-autoseen-outer")
	attach := "env -u TMUX tmux -L " + inner.server.Name + " attach -t s1"
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
	waitFor(t, "client attached to s1", func() (bool, error) {
		out, err := inner.client.Run(ctx, "list-clients", "-F", "#{client_name}")
		return strings.TrimSpace(out) != "", err
	})
	return fx
}

// typeIntoClient types one key through the outer pane and waits until tmux records the client's input,
// which moves its client_activity. The wall clock must first pass the client's last recorded activity,
// so that the new activity is a different whole second.
func (fx fixture) typeIntoClient(t *testing.T) {
	t.Helper()
	ctx := t.Context()
	baseline := lastActivity(t, fx.inner.client)
	waitFor(t, "wall clock to pass the client's last activity", func() (bool, error) {
		return time.Now().Unix() > baseline, nil
	})
	if _, err := fx.outer.client.Run(ctx, "send-keys", "-t", fx.outerPane, "-l", "x"); err != nil {
		t.Fatalf("send-keys to outer pane: %v", err)
	}
	waitFor(t, "client_activity to advance after typing", func() (bool, error) {
		out, err := fx.inner.client.Run(ctx, "list-clients", "-F", "#{client_activity}")
		if err != nil {
			return false, err
		}
		return latestActivity(out) > baseline, nil
	})
}

// lastActivity returns the latest client_activity among the clients of srv.
func lastActivity(t *testing.T, client *tmuxctl.Client) int64 {
	t.Helper()
	out, err := client.Run(t.Context(), "list-clients", "-F", "#{client_activity}")
	if err != nil {
		t.Fatalf("list-clients: %v", err)
	}
	return latestActivity(out)
}

// latestActivity returns the largest value in the newline-separated client_activity output.
func latestActivity(out string) int64 {
	var latest int64
	for line := range strings.SplitSeq(out, "\n") {
		n, err := strconv.ParseInt(strings.TrimSpace(line), 10, 64)
		if err != nil {
			continue
		}
		latest = max(latest, n)
	}
	return latest
}

// waitFor polls check until it reports true, failing the test on its error or when ioTimeout elapses.
func waitFor(t *testing.T, what string, check func() (bool, error)) {
	t.Helper()
	deadline := time.NewTimer(ioTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		ok, err := check()
		if err != nil {
			t.Fatalf("waiting for %s: %v", what, err)
		}
		if ok {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("timed out waiting for %s", what)
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
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve: %v", err)
			}
		case <-time.After(ioTimeout):
			t.Errorf("Serve did not return after cancel")
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
	ctx, cancel := context.WithTimeout(t.Context(), ioTimeout)
	defer cancel()
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", socketPath)
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
	if err := conn.SetReadDeadline(time.Now().Add(ioTimeout)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	_, err = conn.Read(make([]byte, 1))
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		t.Fatalf("daemon did not close the connection: %v", err)
	}
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
		Observers: []daemon.Observer{autoseen.New(clk.now, nil, rep.record)},
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
		Observers: []daemon.Observer{autoseen.New(clk.now, nil, rep.record)},
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
