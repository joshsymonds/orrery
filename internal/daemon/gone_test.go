package daemon_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// setOptions sets each of options on pane.
func setOptions(t *testing.T, client *tmuxctl.Client, pane string, options map[string]string) {
	t.Helper()
	for name, value := range options {
		if err := client.SetPaneOption(t.Context(), pane, name, value); err != nil {
			t.Fatalf("SetPaneOption(%s, %s): %v", pane, name, err)
		}
	}
}

// TestTidyEndsSessionWhoseClaudeIsGone tidies a server with two working panes. One records claude as its command
// while it runs sleep, as a pane reads once its Claude was killed; the other records nothing and runs sleep, which is
// not a shell. The first tidy ends the first pane's session as the registry's unbinding does, tells the
// observers, and stores the pane ended; the other pane keeps its session.
func TestTidyEndsSessionWhoseClaudeIsGone(t *testing.T) {
	srv := startTidyServer(t)
	gone := srv.pane
	live, err := srv.client.Run(t.Context(), "new-window", "-d", "-P", "-F", "#{pane_id}", "-t", "s1", "sleep", "300")
	if err != nil {
		t.Fatalf("new-window: %v", err)
	}
	live = strings.TrimSpace(live)
	setOptions(t, srv.client, gone, map[string]string{
		protocol.OptionState:   string(protocol.StateWorking),
		protocol.OptionLabel:   "synthetic label",
		protocol.OptionCommand: "claude",
	})
	setOptions(t, srv.client, live, map[string]string{
		protocol.OptionState: string(protocol.StateWorking),
		protocol.OptionLabel: "synthetic label",
	})

	var mu sync.Mutex
	var seen []daemon.Transition
	tidied := make(chan struct{}, 1)
	st, db := openFrameStore(t)
	serve(t, daemon.Config{
		Server: srv.server,
		Mirror: daemon.NewMirror(st, tidyClock, nil),
		Observers: []daemon.Observer{func(transition daemon.Transition) {
			mu.Lock()
			defer mu.Unlock()
			seen = append(seen, transition)
		}},
		TidyTicks: make(chan time.Time),
		AfterTidy: func(context.Context) {
			select {
			case tidied <- struct{}{}:
			default:
			}
		},
	})
	<-tidied

	for _, name := range []string{protocol.OptionState, protocol.OptionLabel, protocol.OptionCommand} {
		if value, ok, err := srv.client.PaneOption(t.Context(), gone, name); err != nil || ok {
			t.Errorf("gone pane %s = (%q, %v, %v), want unset", name, value, ok, err)
		}
	}
	if value, _, err := srv.client.PaneOption(t.Context(), live, protocol.OptionState); err != nil ||
		value != string(protocol.StateWorking) {
		t.Errorf("live pane %s = (%q, %v), want working", protocol.OptionState, value, err)
	}

	mu.Lock()
	transitions := seen
	mu.Unlock()
	if len(transitions) != 1 {
		t.Fatalf("observers saw %+v, want one transition", transitions)
	}
	if got := transitions[0]; got.PaneID != gone || got.From != protocol.StateWorking || got.To != "" ||
		got.Server != srv.server {
		t.Fatalf("transition = pane %s server %+v %q -> %q, want pane %s server %+v working -> \"\"",
			got.PaneID, got.Server, got.From, got.To, gone, srv.server)
	}

	states := map[string]string{}
	for _, row := range listSessions(t, db) {
		states[row.Pane] = row.State
	}
	if state, ok := states[gone]; !ok || state != "" {
		t.Errorf("gone pane's row state = (%q, %v), want (\"\", true)", state, ok)
	}
	if state := states[live]; state != string(protocol.StateWorking) {
		t.Errorf("live pane's row state = %q, want working", state)
	}
}
