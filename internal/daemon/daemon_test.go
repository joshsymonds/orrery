package daemon_test

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
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// Every test server starts with -f /dev/null so the user's tmux config never applies.
const (
	noConfig    = "/dev/null"
	testCommand = "sleep 300"
	// ioTimeout bounds each socket exchange and each wait on a daemon goroutine.
	ioTimeout = 5 * time.Second
)

// Synthetic Claude Code hook events.
const (
	userPromptSubmitHook = `{"hook_event_name":"UserPromptSubmit","prompt":"synthetic prompt"}`
	permissionHook       = `{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"echo synthetic"}}`
	postToolUseHook      = `{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"echo synthetic"}}`
	stopHook             = `{"hook_event_name":"Stop","stop_hook_active":false}`
	sessionEndHook       = `{"hook_event_name":"SessionEnd","reason":"other"}`
)

// testServer is a private tmux server with one session whose pane runs testCommand.
type testServer struct {
	client *tmuxctl.Client
	server tmuxctl.Server
	socket string
	pane   string
}

// startServer starts a private tmux server under a unique name, kills it at cleanup, and returns it.
func startServer(t *testing.T) testServer {
	t.Helper()
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("random server name: %v", err)
	}
	server := tmuxctl.Server{Name: fmt.Sprintf("orrery-daemon-test-%d-%x", os.Getpid(), suffix)}
	client, err := tmuxctl.New(server)
	if err != nil {
		t.Fatalf("New(%+v): %v", server, err)
	}
	t.Cleanup(func() {
		if _, err := client.Run(context.Background(), "kill-server"); err != nil {
			t.Errorf("kill-server: %v", err)
		}
	})
	if _, err := client.Run(t.Context(), "-f", noConfig, "new-session", "-d", "-s", "s1",
		"-c", t.TempDir(), testCommand); err != nil {
		t.Fatalf("new-session: %v", err)
	}
	out, err := client.Run(t.Context(), "list-panes", "-t", "s1", "-F", "#{pane_id}")
	if err != nil {
		t.Fatalf("list-panes: %v", err)
	}
	socket, err := client.Run(t.Context(), "display", "-p", "#{socket_path}")
	if err != nil {
		t.Fatalf("display socket_path: %v", err)
	}
	return testServer{
		client: client,
		server: server,
		socket: strings.TrimSpace(socket),
		pane:   strings.Fields(out)[0],
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

// sendFrame encodes frame and sends it to the daemon socket.
func sendFrame(t *testing.T, socketPath string, frame protocol.HookFrame) {
	t.Helper()
	var buf bytes.Buffer
	if err := protocol.WriteFrame(&buf, frame); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	sendBytes(t, socketPath, buf.Bytes())
}

// sendBytes writes payload to the daemon socket and returns once the daemon closes the connection. The
// daemon closes it only after the frame is handled, so this return is the synchronization point. A reset
// from unread input also counts as closed; only a read deadline is a failure.
func sendBytes(t *testing.T, socketPath string, payload []byte) {
	t.Helper()
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
	if _, err := conn.Write(payload); err != nil {
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

// drain returns every transition already delivered to ch without blocking.
func drain(ch <-chan daemon.Transition) []daemon.Transition {
	var got []daemon.Transition
	for {
		select {
		case transition := <-ch:
			got = append(got, transition)
		default:
			return got
		}
	}
}

// option reads pane-scoped option name on srv; ok is false when it is unset.
func option(t *testing.T, srv testServer, name string) (string, bool) {
	t.Helper()
	value, ok, err := srv.client.PaneOption(t.Context(), srv.pane, name)
	if err != nil {
		t.Fatalf("PaneOption(%s): %v", name, err)
	}
	return value, ok
}

// wantOption checks pane-scoped option name on srv; want "" means unset.
func wantOption(t *testing.T, srv testServer, name, want string) {
	t.Helper()
	got, ok := option(t, srv, name)
	if want == "" {
		if ok {
			t.Fatalf("%s = %q, want unset", name, got)
		}
		return
	}
	if !ok || got != want {
		t.Fatalf("%s = (%q, %v), want (%q, true)", name, got, ok, want)
	}
}

func TestDaemonAppliesFrames(t *testing.T) {
	srv := startServer(t)
	socket := serve(t, daemon.Config{})

	steps := []struct {
		name     string
		hook     string
		state    string
		question string
	}{
		{"prompt submitted", userPromptSubmitHook, "working", ""},
		{"permission requested", permissionHook, "waiting", `{"tool":"Bash","input":{"command":"echo synthetic"}}`},
		{"tool finished", postToolUseHook, "working", ""},
		{"stopped", stopHook, "done", ""},
		{"session ended", sessionEndHook, "", ""},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			sendFrame(t, socket, frameFor(step.hook, srv.pane, srv.socket))
			wantOption(t, srv, protocol.OptionState, step.state)
			wantOption(t, srv, protocol.OptionQuestion, step.question)
		})
	}
}

func TestDaemonObservers(t *testing.T) {
	srv := startServer(t)
	transitions := make(chan daemon.Transition, 16)
	socket := serve(t, daemon.Config{
		Observers: []daemon.Observer{func(transition daemon.Transition) { transitions <- transition }},
	})

	frame := frameFor(userPromptSubmitHook, srv.pane, srv.socket)
	sendFrame(t, socket, frame)
	got := drain(transitions)
	if len(got) != 1 {
		t.Fatalf("observed %d transitions after first event, want 1: %+v", len(got), got)
	}
	first := got[0]
	if first.PaneID != srv.pane || first.Server != (tmuxctl.Server{Socket: srv.socket}) ||
		first.From != "" || first.To != protocol.StateWorking {
		t.Fatalf("transition = %+v, want pane %s, server socket %s, from \"\" to working", first, srv.pane, srv.socket)
	}
	if first.Frame.TmuxPane != frame.TmuxPane || first.Frame.Tmux != frame.Tmux ||
		string(first.Frame.Hook) != userPromptSubmitHook {
		t.Fatalf("transition frame = %+v, want %+v", first.Frame, frame)
	}

	sendFrame(t, socket, frame)
	if got := drain(transitions); len(got) != 0 {
		t.Fatalf("same-state event notified %d observers, want none: %+v", len(got), got)
	}

	sendFrame(t, socket, frameFor("{\"hook_event_name\":\"PreCompact\"}", srv.pane, srv.socket))
	if got := drain(transitions); len(got) != 0 {
		t.Fatalf("unhandled event notified %d observers, want none: %+v", len(got), got)
	}

	sendFrame(t, socket, frameFor(permissionHook, srv.pane, srv.socket))
	sendFrame(t, socket, frameFor(stopHook, srv.pane, srv.socket))
	sendFrame(t, socket, frameFor(sessionEndHook, srv.pane, srv.socket))
	got = drain(transitions)
	wantPairs := [][2]protocol.State{
		{protocol.StateWorking, protocol.StateWaiting},
		{protocol.StateWaiting, protocol.StateDone},
		{protocol.StateDone, ""},
	}
	if len(got) != len(wantPairs) {
		t.Fatalf("observed %d transitions, want %d: %+v", len(got), len(wantPairs), got)
	}
	for i, want := range wantPairs {
		if got[i].From != want[0] || got[i].To != want[1] {
			t.Fatalf("transition %d = %s -> %s, want %s -> %s", i, got[i].From, got[i].To, want[0], want[1])
		}
	}
}

func TestDaemonServerOverride(t *testing.T) {
	srv := startServer(t)
	transitions := make(chan daemon.Transition, 4)
	missing := filepath.Join(t.TempDir(), "absent.sock")
	socket := serve(t, daemon.Config{
		Server:    srv.server,
		Observers: []daemon.Observer{func(transition daemon.Transition) { transitions <- transition }},
	})

	sendFrame(t, socket, frameFor(userPromptSubmitHook, srv.pane, missing))
	got := drain(transitions)
	if len(got) != 1 {
		t.Fatalf("observed %d transitions, want 1: %+v", len(got), got)
	}
	if got[0].Server != srv.server {
		t.Fatalf("transition server = %+v, want configured %+v", got[0].Server, srv.server)
	}
	wantOption(t, srv, protocol.OptionState, "working")
}

func TestDaemonDropsBadFrames(t *testing.T) {
	srv := startServer(t)
	transitions := make(chan daemon.Transition, 4)
	socket := serve(t, daemon.Config{
		Observers: []daemon.Observer{func(transition daemon.Transition) { transitions <- transition }},
	})

	bad := []struct {
		name    string
		payload string
	}{
		{"malformed JSON", "{not json\n"},
		{"hook is not an object", fmt.Sprintf(`{"tmuxPane":%q,"tmux":%q,"hook":"text"}`+"\n", srv.pane, srv.socket)},
		{"hook field has the wrong type", fmt.Sprintf(
			`{"tmuxPane":%q,"tmux":%q,"hook":{"hook_event_name":5}}`+"\n", srv.pane, srv.socket)},
		{"empty tmuxPane", fmt.Sprintf(`{"tmuxPane":"","tmux":%q,"hook":%s}`+"\n", srv.socket, userPromptSubmitHook)},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			sendBytes(t, socket, []byte(tc.payload))
			if got := drain(transitions); len(got) != 0 {
				t.Fatalf("bad frame notified observers: %+v", got)
			}
			wantOption(t, srv, protocol.OptionState, "")
		})
	}

	sendFrame(t, socket, frameFor(userPromptSubmitHook, srv.pane, srv.socket))
	if got := drain(transitions); len(got) != 1 {
		t.Fatalf("good frame after bad frames observed %d transitions, want 1", len(got))
	}
	wantOption(t, srv, protocol.OptionState, "working")
}

func TestListen(t *testing.T) {
	t.Run("parent_dir", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "run", "orrery")
		ln, err := daemon.Listen(filepath.Join(dir, "orreryd.sock"))
		if err != nil {
			t.Fatalf("Listen: %v", err)
		}
		t.Cleanup(func() {
			if err := ln.Close(); err != nil {
				t.Errorf("close listener: %v", err)
			}
		})
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("stat parent: %v", err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Fatalf("parent mode = %v, want 0700", info.Mode().Perm())
		}
	})

	t.Run("stale", func(t *testing.T) {
		socketPath := filepath.Join(t.TempDir(), "orreryd.sock")
		stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
		if err != nil {
			t.Fatalf("ListenUnix: %v", err)
		}
		stale.SetUnlinkOnClose(false)
		if err := stale.Close(); err != nil {
			t.Fatalf("close stale listener: %v", err)
		}

		ln, err := daemon.Listen(socketPath)
		if err != nil {
			t.Fatalf("Listen over stale socket: %v", err)
		}
		t.Cleanup(func() {
			if err := ln.Close(); err != nil {
				t.Errorf("close listener: %v", err)
			}
		})
		var dialer net.Dialer
		conn, err := dialer.DialContext(t.Context(), "unix", socketPath)
		if err != nil {
			t.Fatalf("dial replaced socket: %v", err)
		}
		if err := conn.Close(); err != nil {
			t.Errorf("close probe connection: %v", err)
		}
	})

	t.Run("live", func(t *testing.T) {
		socketPath := filepath.Join(t.TempDir(), "orreryd.sock")
		live, err := daemon.Listen(socketPath)
		if err != nil {
			t.Fatalf("Listen: %v", err)
		}
		t.Cleanup(func() {
			if err := live.Close(); err != nil {
				t.Errorf("close listener: %v", err)
			}
		})
		if second, err := daemon.Listen(socketPath); err == nil {
			_ = second.Close()
			t.Fatal("Listen over a live socket succeeded, want error")
		}
	})

	t.Run("regular_file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "orreryd.sock")
		if err := os.WriteFile(path, []byte("synthetic"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if _, err := daemon.Listen(path); err == nil {
			t.Fatal("Listen over a regular file succeeded, want error")
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "synthetic" {
			t.Fatalf("regular file after Listen = %q, %v; want unchanged", data, err)
		}
	})

	t.Run("serve_cancel", func(t *testing.T) {
		ln, err := daemon.Listen(filepath.Join(t.TempDir(), "orreryd.sock"))
		if err != nil {
			t.Fatalf("Listen: %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- daemon.Serve(ctx, ln, daemon.Config{}) }()
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("Serve after cancel: %v", err)
			}
		case <-time.After(ioTimeout):
			t.Fatal("Serve did not return after cancel")
		}
	})
}
