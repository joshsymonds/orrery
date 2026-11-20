package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
	"github.com/joshsymonds/orrery/internal/tmuxtest"
)

// Every test server starts with -f /dev/null so the user's tmux config never applies.
const (
	noConfig = "/dev/null"
	// testCommand keeps each test pane alive until its server is killed.
	testCommand = "sleep 300"
	// attachedSignal is the wait-for channel the attach signals on the inner server, and typedSignal the one a typed key
	// does.
	attachedSignal = "attached"
	typedSignal    = "typed"
)

// wiringFixture is a private server s1 whose window holds pane, with a client attached to s1. The client is a tmux
// attach running in outerPane on a second private server, so keys sent to outerPane reach the client.
type wiringFixture struct {
	inner     tmuxctl.Server
	client    *tmuxctl.Client
	pane      string
	outer     *tmuxctl.Client
	outerPane string
}

// startServer returns a private tmux server named for role, killed at cleanup, and a client for it.
func startServer(t *testing.T, role string) (tmuxctl.Server, *tmuxctl.Client) {
	t.Helper()
	server := tmuxctl.Server{Name: fmt.Sprintf("orrery-route-wiring-%s-%d", role, os.Getpid())}
	client, err := tmuxctl.New(server)
	if err != nil {
		t.Fatalf("New(%+v): %v", server, err)
	}
	t.Cleanup(func() {
		if err := tmuxtest.Stop(t, server); err != nil {
			t.Errorf("kill-server: %v", err)
		}
	})
	return server, client
}

// newWiringFixture starts the servers, attaches a client to s1, and waits until tmux lists it.
func newWiringFixture(t *testing.T) wiringFixture {
	t.Helper()
	// Run the servers and their clients under a UTF-8 locale, as the read-report test in cmd/orrery does (ccd7c25).
	t.Setenv("LC_ALL", "C.UTF-8")
	ctx := t.Context()

	inner, innerClient := startServer(t, "inner")
	if _, err := innerClient.Run(ctx, "-f", noConfig, "new-session", "-d", "-s", "s1",
		"-c", t.TempDir(), testCommand); err != nil {
		t.Fatalf("new-session: %v", err)
	}
	pane, err := innerClient.Run(ctx, "list-panes", "-t", "s1", "-F", "#{pane_id}")
	if err != nil {
		t.Fatalf("list-panes: %v", err)
	}

	// The hook runs on the inner server when the client attaches. The outer pane's command signals the same channel
	// when the attach fails, so the wait below ends either way, and the list of clients decides which it was.
	if _, err := innerClient.Run(ctx, "set-hook", "-g", "client-attached", "wait-for -S "+attachedSignal); err != nil {
		t.Fatalf("set client-attached hook: %v", err)
	}
	_, outerClient := startServer(t, "outer")
	attach := "env -u TMUX tmux -L " + inner.Name + " attach -t s1 || env -u TMUX tmux -L " + inner.Name +
		" wait-for -S " + attachedSignal
	if _, err := outerClient.Run(ctx, "-f", noConfig, "new-session", "-d", "-s", "o1",
		"-c", t.TempDir(), attach); err != nil {
		t.Fatalf("outer new-session: %v", err)
	}
	outerPane, err := outerClient.Run(ctx, "list-panes", "-t", "o1", "-F", "#{pane_id}")
	if err != nil {
		t.Fatalf("outer list-panes: %v", err)
	}

	if _, err := innerClient.Run(ctx, "wait-for", attachedSignal); err != nil {
		t.Fatalf("wait for the client to attach to s1: %v", err)
	}
	out, err := innerClient.Run(ctx, "list-clients", "-F", "#{client_name}")
	if err != nil {
		t.Fatalf("list-clients: %v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatalf("no client attached to s1: the attach in the outer pane failed")
	}

	return wiringFixture{
		inner:     inner,
		client:    innerClient,
		pane:      strings.TrimSpace(pane),
		outer:     outerClient,
		outerPane: strings.TrimSpace(outerPane),
	}
}

// typeIntoClient types one key through outerPane and waits until tmux has handled it. The inner server runs wait-for -S
// for the key from its root table, which fires only after tmux has taken the client's input. The recheck then needs the
// client's newest activity to be no older than before the key.
func (fx wiringFixture) typeIntoClient(t *testing.T) {
	t.Helper()
	ctx := t.Context()
	if _, err := fx.client.Run(ctx, "bind", "-n", "x", "wait-for", "-S", typedSignal); err != nil {
		t.Fatalf("bind the typed key: %v", err)
	}
	before := newestActivity(t, fx.client)
	if _, err := fx.outer.Run(ctx, "send-keys", "-t", fx.outerPane, "-l", "x"); err != nil {
		t.Fatalf("send-keys to outer pane: %v", err)
	}
	if _, err := fx.client.Run(ctx, "wait-for", typedSignal); err != nil {
		t.Fatalf("wait for the typed key: %v", err)
	}
	if newest := newestActivity(t, fx.client); newest < before {
		t.Fatalf("client_activity = %d after typing, want at least %d", newest, before)
	}
}

// newestActivity returns the largest client_activity among the clients of client's server.
func newestActivity(t *testing.T, client *tmuxctl.Client) int64 {
	t.Helper()
	out, err := client.Run(t.Context(), "list-clients", "-F", "#{client_activity}")
	if err != nil {
		t.Fatalf("list-clients: %v", err)
	}
	var newest int64
	for field := range strings.FieldsSeq(out) {
		activity, err := strconv.ParseInt(field, 10, 64)
		if err != nil {
			t.Fatalf("client_activity %q: %v", field, err)
		}
		newest = max(newest, activity)
	}
	return newest
}

// TestRelayWatchingReportsPaneWithClientInput runs the relay's watching check for a pane that a client shows with
// recent input and that no read report names. Only the autoseen.Viewing check can make it report true.
func TestRelayWatchingReportsPaneWithClientInput(t *testing.T) {
	fx := newWiringFixture(t)
	fx.typeIntoClient(t)

	r := &relay{reads: newReadTimes()}
	watching, err := r.watching(t.Context(), daemon.Transition{PaneID: fx.pane, Server: fx.inner})
	if err != nil {
		t.Fatalf("watching: %v", err)
	}
	if !watching {
		t.Fatalf("watching(%s) = false, want true: a client shows the pane with recent input", fx.pane)
	}
}
