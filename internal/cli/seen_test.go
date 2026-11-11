package cli_test

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/cli"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
	"github.com/joshsymonds/orrery/internal/tmuxtest"
)

// Every test server starts with -f /dev/null so the user's tmux config never applies.
const (
	seenNoConfig = "/dev/null"
	seenCommand  = "sleep 300"
)

// seenServer returns a client for a unique private server name and kills that server at cleanup.
func seenServer(t *testing.T) *tmuxctl.Client {
	t.Helper()
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("random server name: %v", err)
	}
	name := fmt.Sprintf("orrery-test-seen-%d-%x", os.Getpid(), suffix)
	client, err := tmuxctl.New(tmuxctl.Server{Name: name})
	if err != nil {
		t.Fatalf("New(Name=%q): %v", name, err)
	}
	t.Cleanup(func() {
		if err := tmuxtest.Stop(t, tmuxctl.Server{Name: name}); err != nil {
			t.Errorf("kill-server: %v", err)
		}
	})
	return client
}

func seenRun(t *testing.T, client *tmuxctl.Client, args ...string) string {
	t.Helper()
	out, err := client.Run(t.Context(), args...)
	if err != nil {
		t.Fatalf("Run(%v): %v", args, err)
	}
	return out
}

// seenStartPanes starts session s1 with two panes and returns their IDs.
func seenStartPanes(t *testing.T, client *tmuxctl.Client) (string, string) {
	t.Helper()
	dir := t.TempDir()
	seenRun(t, client, "-f", seenNoConfig, "new-session", "-d", "-s", "s1", "-c", dir, seenCommand)
	seenRun(t, client, "split-window", "-t", "s1", "-c", dir, seenCommand)
	ids := strings.Fields(seenRun(t, client, "list-panes", "-t", "s1", "-F", "#{pane_id}"))
	if len(ids) != 2 {
		t.Fatalf("pane IDs = %v, want 2", ids)
	}
	return ids[0], ids[1]
}

func TestSeen(t *testing.T) {
	client := seenServer(t)
	pane, other := seenStartPanes(t, client)

	before := time.Now().Unix()
	var out bytes.Buffer
	if err := cli.Seen(t.Context(), client, []string{"--pane", pane}, &out); err != nil {
		t.Fatalf("Seen: %v", err)
	}
	after := time.Now().Unix()

	if out.Len() != 0 {
		t.Fatalf("Seen printed %q, want nothing", out.String())
	}

	got, ok, err := client.PaneOption(t.Context(), pane, protocol.OptionSeen)
	if err != nil {
		t.Fatalf("PaneOption(%s): %v", protocol.OptionSeen, err)
	}
	if !ok {
		t.Fatalf("%s unset on %s after Seen", protocol.OptionSeen, pane)
	}
	ts, err := strconv.ParseInt(got, 10, 64)
	if err != nil {
		t.Fatalf("%s = %q, want decimal unix seconds: %v", protocol.OptionSeen, got, err)
	}
	if ts < before || ts > after {
		t.Fatalf("%s = %d, want between %d and %d", protocol.OptionSeen, ts, before, after)
	}

	_, ok, err = client.PaneOption(t.Context(), other, protocol.OptionSeen)
	if err != nil {
		t.Fatalf("PaneOption on other pane: %v", err)
	}
	if ok {
		t.Fatalf("%s set on %s, want it scoped to %s only", protocol.OptionSeen, other, pane)
	}
}

// seenWait bounds the wait for a control-mode client to appear in list-clients.
const seenWait = 5 * time.Second

// seenAttach starts a control-mode client of client's server attached to session, keeps its stdin open until
// cleanup, and waits until the server lists it. The client connects through the server's socket path.
func seenAttach(t *testing.T, client *tmuxctl.Client, session string) {
	t.Helper()
	socket := strings.TrimSpace(seenRun(t, client, "display", "-p", "#{socket_path}"))
	cmd := exec.CommandContext(t.Context(), "tmux", "-S", socket, "-C", "attach", "-t", session)
	cmd.Env = seenEnvWithout("TMUX")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("control client stdin: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start control client: %v", err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	deadline := time.Now().Add(seenWait)
	for strings.TrimSpace(seenRun(t, client, "list-clients", "-F", "#{client_name}")) == "" {
		if time.Now().After(deadline) {
			t.Fatalf("no client listed for session %s after %v", session, seenWait)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// seenEnvWithout returns the test process environment without the variable name, so a tmux client started
// from a test does not see an enclosing tmux session.
func seenEnvWithout(name string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, name+"=") {
			env = append(env, kv)
		}
	}
	return env
}

// seenWantUnset fails t when pane has a seen option.
func seenWantUnset(t *testing.T, client *tmuxctl.Client, pane string) {
	t.Helper()
	got, ok, err := client.PaneOption(t.Context(), pane, protocol.OptionSeen)
	if err != nil {
		t.Fatalf("PaneOption(%s): %v", protocol.OptionSeen, err)
	}
	if ok {
		t.Fatalf("%s on %s = %q, want unset", protocol.OptionSeen, pane, got)
	}
}

func TestSeenFocusMarksPaneShownByActiveClient(t *testing.T) {
	client := seenServer(t)
	pane, other := seenStartPanes(t, client)
	seenAttach(t, client, "s1")

	before := time.Now().Unix()
	var out bytes.Buffer
	if err := cli.Seen(t.Context(), client, []string{"--pane", pane, "--focus"}, &out); err != nil {
		t.Fatalf("Seen --focus: %v", err)
	}
	after := time.Now().Unix()

	if out.Len() != 0 {
		t.Fatalf("Seen --focus printed %q, want nothing", out.String())
	}
	got, ok, err := client.PaneOption(t.Context(), pane, protocol.OptionSeen)
	if err != nil {
		t.Fatalf("PaneOption(%s): %v", protocol.OptionSeen, err)
	}
	if !ok {
		t.Fatalf("%s unset on %s after Seen --focus with an active client", protocol.OptionSeen, pane)
	}
	ts, err := strconv.ParseInt(got, 10, 64)
	if err != nil {
		t.Fatalf("%s = %q, want decimal unix seconds: %v", protocol.OptionSeen, got, err)
	}
	if ts < before || ts > after {
		t.Fatalf("%s = %d, want between %d and %d", protocol.OptionSeen, ts, before, after)
	}
	seenWantUnset(t, client, other)
}

func TestSeenFocusLeavesPaneUnseenWithoutClient(t *testing.T) {
	client := seenServer(t)
	pane, _ := seenStartPanes(t, client)

	var out bytes.Buffer
	if err := cli.Seen(t.Context(), client, []string{"--pane", pane, "--focus"}, &out); err != nil {
		t.Fatalf("Seen --focus with no client: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("Seen --focus printed %q, want nothing", out.String())
	}
	seenWantUnset(t, client, pane)
}

func TestSeenFocusLeavesPaneUnseenWhenClientShowsAnotherWindow(t *testing.T) {
	client := seenServer(t)
	pane, _ := seenStartPanes(t, client)
	seenRun(t, client, "new-window", "-t", "s1", "-c", t.TempDir(), seenCommand)
	seenAttach(t, client, "s1")

	var out bytes.Buffer
	if err := cli.Seen(t.Context(), client, []string{"--pane", pane, "--focus"}, &out); err != nil {
		t.Fatalf("Seen --focus with client on another window: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("Seen --focus printed %q, want nothing", out.String())
	}
	seenWantUnset(t, client, pane)
}

func TestSeenRejectsBadArguments(t *testing.T) {
	client := seenServer(t)
	pane, _ := seenStartPanes(t, client)

	cases := []struct {
		name string
		args []string
	}{
		{name: "missing pane", args: nil},
		{name: "empty pane", args: []string{"--pane", ""}},
		{name: "flag without value", args: []string{"--pane"}},
		{name: "unknown flag", args: []string{"--bogus", "x"}},
		{name: "stray positional", args: []string{"--pane", pane, "extra"}},
		{name: "unknown pane", args: []string{"--pane", "%999999"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			err := cli.Seen(t.Context(), client, tc.args, &out)
			if err == nil {
				t.Fatalf("Seen(%v): want error, got nil", tc.args)
			}
			if out.Len() != 0 {
				t.Fatalf("Seen(%v) printed %q alongside its error", tc.args, out.String())
			}
		})
	}
}

func TestSeenUnknownPaneWrapsTmuxError(t *testing.T) {
	client := seenServer(t)
	seenStartPanes(t, client)

	err := cli.Seen(t.Context(), client, []string{"--pane", "%999999"}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("Seen with unknown pane: want error, got nil")
	}
	if !strings.Contains(err.Error(), "tmux:") {
		t.Fatalf("error %q does not carry the tmux failure", err)
	}
}
