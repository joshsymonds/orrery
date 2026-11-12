package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
	"github.com/joshsymonds/orrery/internal/tmuxtest"
)

// Every test server starts with -f /dev/null so the user's tmux config never applies.
const (
	noConfig = "/dev/null"
	// testCommand keeps each test pane alive until its server is killed.
	testCommand = "sleep 300"
	// ioTimeout bounds each wait for tmux to reach the expected state.
	ioTimeout = 5 * time.Second
	// pollInterval spaces the checks made while waiting for tmux.
	pollInterval = 20 * time.Millisecond
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

	_, outerClient := startServer(t, "outer")
	attach := "env -u TMUX tmux -L " + inner.Name + " attach -t s1"
	if _, err := outerClient.Run(ctx, "-f", noConfig, "new-session", "-d", "-s", "o1",
		"-c", t.TempDir(), attach); err != nil {
		t.Fatalf("outer new-session: %v", err)
	}
	outerPane, err := outerClient.Run(ctx, "list-panes", "-t", "o1", "-F", "#{pane_id}")
	if err != nil {
		t.Fatalf("outer list-panes: %v", err)
	}

	fx := wiringFixture{
		inner:     inner,
		client:    innerClient,
		pane:      strings.TrimSpace(pane),
		outer:     outerClient,
		outerPane: strings.TrimSpace(outerPane),
	}
	waitFor(t, "client attached to s1", func() (bool, error) {
		out, err := innerClient.Run(ctx, "list-clients", "-F", "#{client_name}")
		return strings.TrimSpace(out) != "", err
	})
	return fx
}

// typeIntoClient types one key through outerPane and waits until tmux records the client's input. It returns the
// client's newest activity from before the key. The wall clock must first pass that activity, so the new activity
// falls in a different whole second.
func (fx wiringFixture) typeIntoClient(t *testing.T) int64 {
	t.Helper()
	before := newestActivity(t, fx.client)
	waitFor(t, "wall clock to pass the client's last activity", func() (bool, error) {
		return time.Now().Unix() > before, nil
	})
	if _, err := fx.outer.Run(t.Context(), "send-keys", "-t", fx.outerPane, "-l", "x"); err != nil {
		t.Fatalf("send-keys to outer pane: %v", err)
	}
	waitFor(t, "client_activity to advance after typing", func() (bool, error) {
		return newestActivity(t, fx.client) > before, nil
	})
	return before
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

// waitFor polls check until it reports true, failing the test on its error or when ioTimeout elapses.
func waitFor(t *testing.T, what string, check func() (bool, error)) {
	t.Helper()
	deadline := time.Now().Add(ioTimeout)
	for {
		ok, err := check()
		if err != nil {
			t.Fatalf("waiting for %s: %v", what, err)
		}
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(pollInterval)
	}
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

// TestLocalInputReportsInputOnTmuxClient runs the router's LocalInput after input on an attached tmux client. It
// must report that client's newest activity with ok set.
func TestLocalInputReportsInputOnTmuxClient(t *testing.T) {
	fx := newWiringFixture(t)
	before := fx.typeIntoClient(t)

	input, ok, err := localInput(options{server: fx.inner})(t.Context())
	if err != nil {
		t.Fatalf("localInput: %v", err)
	}
	if !ok {
		t.Fatalf("localInput ok = false, want true: a client gave input")
	}
	if want := newestActivity(t, fx.client); input != want {
		t.Fatalf("localInput = %d, want the client's newest activity %d", input, want)
	}
	if input <= before {
		t.Fatalf("localInput = %d, want after the client's activity before typing (%d)", input, before)
	}
}
