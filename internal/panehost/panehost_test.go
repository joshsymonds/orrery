package panehost_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/node"
	"github.com/joshsymonds/orrery/internal/panehost"
	"github.com/joshsymonds/orrery/internal/paneregistry"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
	"github.com/joshsymonds/orrery/internal/tmuxtest"
)

// noConfig is the tmux config that startServer gives tmux in place of the user's.
const noConfig = "/dev/null"

// testCommand is the command of each test pane. It outlives the test, so the pane stays.
const testCommand = "sleep 300"

// synthetic is the pid of the Claude process that the registry fixture's entry names.
const synthetic = 4242

// fakeProc is a paneregistry.Proc in which every pid is alive, and every process has env as its environment.
type fakeProc struct {
	env map[string]string
}

// Alive reports that every pid is alive.
func (fakeProc) Alive(int) bool { return true }

// Environ returns the environment that every process has.
func (p fakeProc) Environ(int) (map[string]string, error) { return p.env, nil }

// fixedNow is the clock of each test, so no state change is stamped with a real time.
func fixedNow() time.Time { return time.Unix(1, 0) }

// startServer starts a private tmux server, kills it at cleanup, and returns it by its socket, as a $TMUX names it, and
// the id of its one pane.
func startServer(t *testing.T) (tmuxctl.Server, string) {
	t.Helper()
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("random server name: %v", err)
	}
	named := tmuxctl.Server{Name: fmt.Sprintf("orrery-panehost-test-%d-%x", os.Getpid(), suffix)}
	client, err := tmuxctl.New(named)
	if err != nil {
		t.Fatalf("New(%+v): %v", named, err)
	}
	t.Cleanup(func() {
		if err := tmuxtest.Stop(t, named); err != nil {
			t.Errorf("kill-server: %v", err)
		}
	})
	if _, err = client.Run(t.Context(), "-f", noConfig, "new-session", "-d", "-s", "s1",
		"-c", t.TempDir(), testCommand); err != nil {
		t.Fatalf("new-session: %v", err)
	}
	socket, err := client.Run(t.Context(), "display", "-p", "#{socket_path}")
	if err != nil {
		t.Fatalf("display socket_path: %v", err)
	}
	pane, err := client.Run(t.Context(), "list-panes", "-t", "s1", "-F", "#{pane_id}")
	if err != nil {
		t.Fatalf("list-panes: %v", err)
	}
	return tmuxctl.Server{Socket: strings.TrimSpace(socket)}, strings.TrimSpace(pane)
}

// runHost starts a Host over cfg, and runs it until the test ends.
func runHost(t *testing.T, cfg panehost.Config) *panehost.Host {
	t.Helper()
	host, err := panehost.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- host.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run: %v", err)
		}
	})
	return host
}

// watchEvents watches host until the test ends, and returns the channel that each event of the watch arrives on.
func watchEvents(t *testing.T, host *panehost.Host) <-chan node.PaneEvent {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	events := make(chan node.PaneEvent)
	done := make(chan error, 1)
	go func() {
		done <- host.Watch(ctx, func(event node.PaneEvent) error {
			select {
			case events <- event:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Errorf("Watch: %v", err)
		}
	})
	return events
}

// writeEntry writes entry into dir as the registry file <pid>.json. It renames the file into place, so the watch
// reads the entry whole.
func writeEntry(t *testing.T, dir string, entry paneregistry.Entry) {
	t.Helper()
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal entry: %v", err)
	}
	name := filepath.Join(dir, fmt.Sprintf("%d.json", entry.PID))
	if err = os.WriteFile(name+".tmp", data, 0o600); err != nil {
		t.Fatalf("write entry: %v", err)
	}
	if err = os.Rename(name+".tmp", name); err != nil {
		t.Fatalf("rename entry: %v", err)
	}
}

// hasPane reports whether listing holds pane.
func hasPane(listing []node.PaneSnapshot, pane string) bool {
	for _, snapshot := range listing {
		if snapshot.Pane == pane {
			return true
		}
	}
	return false
}

// TestPaneHostListsWithIDs checks that the first event of a watch is a full listing of the host's panes, and that each
// listed pane carries an @orrery_id, which tmux holds too: one minted for a pane that had none.
func TestPaneHostListsWithIDs(t *testing.T) {
	server, pane := startServer(t)
	host := runHost(t, panehost.Config{
		Name:          "synthetic-host",
		Dir:           t.TempDir(),
		Home:          t.TempDir(),
		Proc:          fakeProc{},
		DefaultServer: server,
		Ticks:         make(chan time.Time),
		Now:           fixedNow,
	})

	first := <-watchEvents(t, host)
	if !first.Full {
		t.Fatalf("first event = %+v, want a full listing", first)
	}
	if len(first.Listing) != 1 || first.Listing[0].Pane != pane {
		t.Fatalf("listing = %+v, want only pane %s", first.Listing, pane)
	}
	id := first.Listing[0].ID
	client, err := tmuxctl.New(server)
	if err != nil {
		t.Fatalf("New(%+v): %v", server, err)
	}
	got, ok, err := client.PaneOption(t.Context(), pane, protocol.OptionID)
	if err != nil {
		t.Fatalf("read %s: %v", protocol.OptionID, err)
	}
	if id == "" || !ok || got != id {
		t.Errorf("listed id = %q, tmux %s = %q (set %v), want the listed id", id, protocol.OptionID, got, ok)
	}
}

// TestPaneHostTransition checks that a registry entry that turns waiting sends its pane's snapshot, with a transition
// to waiting.
func TestPaneHostTransition(t *testing.T) {
	server, pane := startServer(t)
	dir := t.TempDir()
	host := runHost(t, panehost.Config{
		Name: "synthetic-host",
		Dir:  dir,
		Home: t.TempDir(),
		Proc: fakeProc{env: map[string]string{
			"TMUX":      server.Socket + ",1,0",
			"TMUX_PANE": pane,
		}},
		DefaultServer: server,
		Ticks:         make(chan time.Time),
		Now:           fixedNow,
	})
	events := watchEvents(t, host)
	if first := <-events; !first.Full {
		t.Fatalf("first event = %+v, want a full listing", first)
	}

	writeEntry(t, dir, paneregistry.Entry{
		PID:        synthetic,
		SessionID:  "synthetic-session",
		Cwd:        t.TempDir(),
		Entrypoint: paneregistry.EntrypointCLI,
		Status:     paneregistry.StatusWaiting,
		WaitingFor: paneregistry.WaitingPermission,
	})
	event := <-events
	if event.Pane == nil || event.Pane.Pane != pane {
		t.Fatalf("event = %+v, want the snapshot of pane %s", event, pane)
	}
	if event.Transition == nil || event.Transition.To != protocol.StateWaiting {
		t.Fatalf("event transition = %+v, want a transition to waiting", event.Transition)
	}
	if got := event.Pane.Fields.State; got != string(protocol.StateWaiting) {
		t.Errorf("snapshot state = %q, want %q", got, protocol.StateWaiting)
	}
}

// TestPaneHostRelisting checks that a tick sends each watcher a fresh full listing, which leaves out a pane that was
// killed.
func TestPaneHostRelisting(t *testing.T) {
	server, first := startServer(t)
	client, err := tmuxctl.New(server)
	if err != nil {
		t.Fatalf("New(%+v): %v", server, err)
	}
	split, err := client.Run(t.Context(), "split-window", "-P", "-F", "#{pane_id}", "-t", first, testCommand)
	if err != nil {
		t.Fatalf("split-window: %v", err)
	}
	second := strings.TrimSpace(split)
	ticks := make(chan time.Time)
	host := runHost(t, panehost.Config{
		Name:          "synthetic-host",
		Dir:           t.TempDir(),
		Home:          t.TempDir(),
		Proc:          fakeProc{},
		DefaultServer: server,
		Ticks:         ticks,
		Now:           fixedNow,
	})
	events := watchEvents(t, host)

	listed := <-events
	if !listed.Full || !hasPane(listed.Listing, first) || !hasPane(listed.Listing, second) {
		t.Fatalf("first event = %+v, want a full listing of panes %s and %s", listed, first, second)
	}
	if _, err = client.Run(t.Context(), "kill-pane", "-t", second); err != nil {
		t.Fatalf("kill-pane: %v", err)
	}
	ticks <- time.Time{}
	relisted := <-events
	if !relisted.Full || !hasPane(relisted.Listing, first) || hasPane(relisted.Listing, second) {
		t.Errorf("tick event = %+v, want a full listing of pane %s without pane %s", relisted, first, second)
	}
}
