package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/route"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// The reasons a restored notice ends with, spelled as the router writes them.
const (
	reasonChanged = "changed"
	reasonSeen    = "seen"
)

// TestRestoredPaneEnds restarts orreryd while a deciding waiting notice's pane has ended. The restore ends the notice
// `changed` when its pane is gone or in another state, and `seen` when the pane was seen at or after the notice's time.
// Each ends with one line and sends nothing. A pane seen before the notice's time resumes.
func TestRestoredPaneEnds(t *testing.T) {
	t.Run("a pane killed while orreryd was down ends changed", endsKilledPane)
	t.Run("a pane whose server is not running ends changed", endsStoppedServer)
	t.Run("a pane in another state ends changed", endsOtherState)
	t.Run("a pane seen at the notice's time ends seen", endsSeenAt)
	t.Run("a pane seen after the notice's time ends seen", endsSeenAfter)
	t.Run("a pane seen before the notice's time resumes", resumesSeenBefore)
}

// startWaitingPane starts a pane that is waiting, with the text its notifications read, in the project folder under
// home. It returns the pane's server, a client for it and the pane's id.
func startWaitingPane(t *testing.T, home string) (tmuxctl.Server, *tmuxctl.Client, string) {
	t.Helper()
	server, client, pane := startPane(t, paneCwd(t, home))
	setPaneText(t, client, pane)
	setPaneState(t, client, pane, protocol.StateWaiting)
	return server, client, pane
}

// setPaneSeen stores the seen time, in unix seconds, that a client's viewing of pane writes.
func setPaneSeen(t *testing.T, client *tmuxctl.Client, pane string, seen int64) {
	t.Helper()
	if err := client.SetPaneOption(t.Context(), pane, protocol.OptionSeen, strconv.FormatInt(seen, 10)); err != nil {
		t.Fatalf("set %s: %v", protocol.OptionSeen, err)
	}
}

// restartDeciding stores the row that Notify leaves for a deciding waiting notice on pane of server, runs change while
// orreryd is down, and restarts orreryd over the store. It returns the store and the sinks the restart wrote to.
func restartDeciding(
	t *testing.T, home string, server tmuxctl.Server, pane string, change func(),
) (*store.Store, *paneSinks) {
	t.Helper()
	transition := paneTransition(server, pane, protocol.StateWaiting, filepath.Join(home, paneProject), paneWaitingHook)
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openPaneStore(t, path)
	if err := s.PutNotice(t.Context(), paneRow(pane, protocol.StateWaiting, phaseDeciding,
		dueAt(paneClock, 0), sourceOf(t, transition))); err != nil {
		t.Fatalf("put deciding row: %v", err)
	}
	s = reopenPaneStore(t, s, path)
	change()

	sinks := newPaneSinks(paneClock+decidingRestartSecs, false)
	hub := startProcess(t, sinks, s, home, &paneJudgeCaller{})
	t.Cleanup(hub.router.Close)
	hub.router.Wait()
	return s, sinks
}

// checkEnded asserts that the restart ended the notice on pane in state with reason: one line with route none, no desk
// post or phone push, and no row left.
func checkEnded(t *testing.T, s *store.Store, sinks *paneSinks, pane, reason string) {
	t.Helper()
	restartAt := paneClock + decidingRestartSecs
	sinks.check(t,
		[]route.Line{expectedLine(restartAt, pane, protocol.StateWaiting, route.None, reason)},
		nil,
		nil)
	checkRows(t, s)
}

// endsKilledPane restarts orreryd after the pane was killed. A second session keeps the server running, so the restart
// reads a server that no longer has the pane.
func endsKilledPane(t *testing.T) {
	home := t.TempDir()
	server, client, pane := startWaitingPane(t, home)
	if _, err := client.Run(t.Context(), "-f", noConfig, "new-session", "-d", "-s", "keep", "-c", home,
		testCommand); err != nil {
		t.Fatalf("new-session: %v", err)
	}
	s, sinks := restartDeciding(t, home, server, pane, func() {
		if _, err := client.Run(t.Context(), "kill-pane", "-t", pane); err != nil {
			t.Fatalf("kill-pane: %v", err)
		}
	})
	checkEnded(t, s, sinks, pane, reasonChanged)
}

// endsStoppedServer restarts orreryd over a pane whose server is not running. No test starts this server, so the
// restart finds no server, as it would once the pane's server had stopped.
func endsStoppedServer(t *testing.T) {
	home := t.TempDir()
	server := tmuxctl.Server{Name: fmt.Sprintf("orrery-pane-ends-stopped-%d", os.Getpid())}
	const pane = "%0"
	s, sinks := restartDeciding(t, home, server, pane, func() {})
	checkEnded(t, s, sinks, pane, reasonChanged)
}

// endsOtherState restarts orreryd after the pane moved on from waiting to working.
func endsOtherState(t *testing.T) {
	home := t.TempDir()
	server, client, pane := startWaitingPane(t, home)
	s, sinks := restartDeciding(t, home, server, pane, func() {
		setPaneState(t, client, pane, protocol.StateWorking)
	})
	checkEnded(t, s, sinks, pane, reasonChanged)
}

// restartSeen restarts orreryd after the pane of a deciding waiting notice was seen at seen, unix seconds. It returns
// the pane's id with the store and sinks of the restart.
func restartSeen(t *testing.T, seen int64) (string, *store.Store, *paneSinks) {
	t.Helper()
	home := t.TempDir()
	server, client, pane := startWaitingPane(t, home)
	s, sinks := restartDeciding(t, home, server, pane, func() { setPaneSeen(t, client, pane, seen) })
	return pane, s, sinks
}

// endsSeenAt restarts orreryd after the pane was seen at the notice's time.
func endsSeenAt(t *testing.T) {
	pane, s, sinks := restartSeen(t, paneClock)
	checkEnded(t, s, sinks, pane, reasonSeen)
}

// endsSeenAfter restarts orreryd after the pane was seen a second after the notice's time.
func endsSeenAfter(t *testing.T) {
	pane, s, sinks := restartSeen(t, paneClock+1)
	checkEnded(t, s, sinks, pane, reasonSeen)
}

// resumesSeenBefore restarts orreryd after the pane was seen a second before the notice's time. The notice resumes as
// it does when its pane was never seen: it is decided away and goes to the desk and the phone.
func resumesSeenBefore(t *testing.T) {
	pane, s, sinks := restartSeen(t, paneClock-1)
	restartAt := paneClock + decidingRestartSecs
	sinks.check(t,
		[]route.Line{expectedLine(restartAt, pane, protocol.StateWaiting, route.DeskPhone, reasonAway)},
		[]deskstream.Notice{expectedPost(protocol.StateWaiting, paneQuestion)},
		[]push.Notification{expectedPush(protocol.StateWaiting)})
	sinks.publish()
	checkRows(t, s)
}
