package cli_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/cli"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// paneWakeCallerPane starts a pane whose screen is screen on a private tmux server. It returns the server's client,
// the pane's ID, its input file, and the "-L <name>" column that a store row holds for that server. tmux creates a -L
// server's socket at a path whose base name is the name, so the column is read from the pane's socket_path.
func paneWakeCallerPane(t *testing.T, screen string) (*tmuxctl.Client, string, string, string) {
	t.Helper()
	client := sendServer(t)
	pane, file := sendStartPane(t, client, screen)
	socket := strings.TrimSpace(sendRun(t, client, "display-message", "-p", "-t", pane, "#{socket_path}"))
	return client, pane, file, "-L " + filepath.Base(socket)
}

// TestPaneWakeCallerTypesIntoDonePane requires WakePane, given the "-L <name>" column of a done pane that shows its
// input box, to type the text, press Enter once, and return true.
func TestPaneWakeCallerTypesIntoDonePane(t *testing.T) {
	t.Parallel()
	client, pane, file, column := paneWakeCallerPane(t, sendBoxScreen())
	sendSetState(t, client, pane, string(protocol.StateDone))

	sent, err := cli.WakePaneWithClock(t.Context(), &instantClock{}, column, pane, "synthetic text")
	if err != nil || !sent {
		t.Fatalf("WakePane = %v, %v; want true, nil", sent, err)
	}
	sendAwaitInput(t, client, pane, file, "synthetic text\r")
}

// TestPaneWakeCallerSendsNothingToWorkingPane requires WakePane, given the "-L <name>" column of a working pane that
// shows its input box, to return false and send no key.
func TestPaneWakeCallerSendsNothingToWorkingPane(t *testing.T) {
	t.Parallel()
	client, pane, file, column := paneWakeCallerPane(t, sendBoxScreen())
	sendSetState(t, client, pane, string(protocol.StateWorking))

	sent, err := cli.WakePaneWithClock(t.Context(), &instantClock{}, column, pane, "synthetic text")
	if err != nil || sent {
		t.Fatalf("WakePane = %v, %v; want false, nil", sent, err)
	}
	paneWakeAssertNothingSent(t, client, pane, file)
}

// TestPaneWakeCallerRefusesMalformedColumn requires WakePane, given a server column that is not "", "-L <name>", or
// "-S <path>", to return an error that names the pane, and send no key. The column is a bare name, the likeliest
// corruption of a "-L <name>" column. The pane itself is done with its input box shown, so only the column can stop
// the send. tmuxctl's column tests cover the other malformed forms.
func TestPaneWakeCallerRefusesMalformedColumn(t *testing.T) {
	t.Parallel()
	client, pane, file, _ := paneWakeCallerPane(t, sendBoxScreen())
	sendSetState(t, client, pane, string(protocol.StateDone))

	sent, err := cli.WakePaneWithClock(t.Context(), &instantClock{}, "orrery", pane, "synthetic text")
	if err == nil || sent {
		t.Fatalf("WakePane = %v, %v; want false with an error", sent, err)
	}
	if !strings.Contains(err.Error(), pane) {
		t.Fatalf("WakePane error %q does not name pane %s", err, pane)
	}
	paneWakeAssertNothingSent(t, client, pane, file)
}
