package server_test

import (
	"net/http"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// interruptTool is the @orrery_tool value of a Bash call that a turn started and no end event has cleared.
const interruptTool = `{"id":"synthetic-1","name":"Bash","input":"echo synthetic","start":1794301200}`

// interruptLine returns the user line Claude Code writes when the person interrupts a turn, stamped stamp.
func interruptLine(stamp string) string {
	return `{"type":"user","timestamp":"` + stamp + `","message":{"content":[{"type":"text",` +
		`"text":"[Request interrupted by user]"}]}}`
}

// listedPane returns the pane with id from the Panes.List answer through handler.
func listedPane(t *testing.T, handler http.Handler, id string) *orreryv1.Pane {
	t.Helper()
	for _, pane := range listPanes(t, handler) {
		if pane.GetId() == id {
			return pane
		}
	}
	t.Fatalf("pane %s is not in Panes.List", id)
	return nil
}

// paneOption returns the value of option on pane, and whether the option is set.
func paneOption(t *testing.T, tmux *tmuxctl.Client, pane, name string) (string, bool) {
	t.Helper()
	value, ok, err := tmux.PaneOption(t.Context(), pane, name)
	if err != nil {
		t.Fatalf("PaneOption(%s, %s): %v", pane, name, err)
	}
	return value, ok
}

// workingTurnOptions returns the options of a working pane whose turn started at changed, with interruptTool
// running and its transcript at path.
func workingTurnOptions(path string, changed int64) map[string]string {
	options := liveOptions(path)
	options[protocol.OptionChanged] = strconv.FormatInt(changed, 10)
	options[protocol.OptionTool] = interruptTool
	return options
}

func TestServePanesInterruptEndsTurn(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	path := writeNewestTranscript(t,
		newestToolLine(newestStamp0900),
		interruptLine(newestStamp0905))
	pane := conversationPane(t, tmux, workingTurnOptions(path, newestUnix0900))
	handler := newHandler(t, tmux)

	before := time.Now()
	listed := listedPane(t, handler, pane)
	if listed.GetState() != string(protocol.StateDone) {
		t.Fatalf("state = %q, want %q", listed.GetState(), protocol.StateDone)
	}
	if listed.GetTool() != "" {
		t.Fatalf("tool = %q, want empty", listed.GetTool())
	}
	if listed.GetChanged() <= newestUnix0900 || listed.GetChanged() < before.Unix() {
		t.Fatalf("changed = %d, want a time after %d and not before %d",
			listed.GetChanged(), newestUnix0900, before.Unix())
	}
	if got, _ := paneOption(t, tmux, pane, protocol.OptionState); got != string(protocol.StateDone) {
		t.Fatalf("%s = %q, want %q", protocol.OptionState, got, protocol.StateDone)
	}
	if got, _ := paneOption(t, tmux, pane, protocol.OptionChanged); got != strconv.FormatInt(listed.GetChanged(), 10) {
		t.Fatalf("%s = %q, want %d", protocol.OptionChanged, got, listed.GetChanged())
	}
	if _, ok := paneOption(t, tmux, pane, protocol.OptionTool); ok {
		t.Fatalf("%s is set, want unset", protocol.OptionTool)
	}

	again := listedPane(t, handler, pane)
	if again.GetState() != string(protocol.StateDone) || again.GetChanged() != listed.GetChanged() {
		t.Fatalf("second listing state = %q changed = %d, want %q changed = %d",
			again.GetState(), again.GetChanged(), protocol.StateDone, listed.GetChanged())
	}
}

func TestServePanesInterruptOlderLeavesLaterTurn(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	// The interruption is older than the turn's changed time, so it belongs to an earlier turn.
	path := writeNewestTranscript(t,
		newestToolLine(newestStamp0900),
		interruptLine(newestStamp0900))
	pane := conversationPane(t, tmux, workingTurnOptions(path, newestUnix0905))

	listed := listedPane(t, newHandler(t, tmux), pane)
	if listed.GetState() != string(protocol.StateWorking) {
		t.Fatalf("state = %q, want %q", listed.GetState(), protocol.StateWorking)
	}
	if listed.GetTool() != interruptTool {
		t.Fatalf("tool = %q, want %q", listed.GetTool(), interruptTool)
	}
	if listed.GetChanged() != newestUnix0905 {
		t.Fatalf("changed = %d, want %d", listed.GetChanged(), newestUnix0905)
	}
	if got, _ := paneOption(t, tmux, pane, protocol.OptionState); got != string(protocol.StateWorking) {
		t.Fatalf("%s = %q, want %q", protocol.OptionState, got, protocol.StateWorking)
	}
	if got, _ := paneOption(t, tmux, pane, protocol.OptionChanged); got != strconv.FormatInt(newestUnix0905, 10) {
		t.Fatalf("%s = %q, want %d", protocol.OptionChanged, got, newestUnix0905)
	}
}

func TestServePanesInterruptMissingTranscript(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	absent := filepath.Join(t.TempDir(), "absent.jsonl")
	pane := conversationPane(t, tmux, workingTurnOptions(absent, newestUnix0905))

	listed := listedPane(t, newHandler(t, tmux), pane)
	if listed.GetState() != string(protocol.StateWorking) {
		t.Fatalf("state = %q, want %q", listed.GetState(), protocol.StateWorking)
	}
	if listed.GetTool() != interruptTool {
		t.Fatalf("tool = %q, want %q", listed.GetTool(), interruptTool)
	}
}
