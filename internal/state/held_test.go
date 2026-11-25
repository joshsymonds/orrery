package state_test

import (
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/paneregistry"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/state"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// heldStopped returns an assistant line at second s whose content holds blocks, in order, and whose message's
// stop_reason is stop, as Claude Code writes each line of a finished message.
func heldStopped(s int, stop string, blocks ...string) string {
	return `{"type":"assistant","timestamp":"` + registryStamp(s) + `","message":{"role":"assistant","stop_reason":"` +
		stop + `","content":[` + strings.Join(blocks, ",") + `]}}`
}

// heldLaunch returns the lines of a turn that starts at second 1, launches a background agent at second 2, has its
// launch answered at second 3 and ends at second 4 with the agent still running.
func heldLaunch() []string {
	return []string{
		registryPrompt(1, "go"),
		heldStopped(2, "tool_use",
			registryTool("a1", "Agent", `{"prompt":"search the tree","run_in_background":true}`)),
		registryResult(3, "a1"),
		heldStopped(4, "end_turn", registryText("The agent is searching.")),
	}
}

// heldNotification returns the prompt line at second s that Claude Code writes when the background agent a1 ends.
func heldNotification(s int) string {
	return registryPrompt(s, "<task-notification><task-id>a1</task-id><status>completed</status></task-notification>")
}

// applyStatus applies an update of status to pane at at, whose transcript is lines.
func applyStatus(
	t *testing.T, client *tmuxctl.Client, pane, status string, at time.Time, lines ...string,
) state.Transition {
	t.Helper()
	transition, _ := applyPane(t, client, pane, state.Pane{Status: status, Turn: registryTurn(t, lines...)}, at)
	return transition
}

// holdPane starts a pane and applies a busy update at at whose transcript is heldLaunch, which holds it.
func holdPane(t *testing.T, at time.Time) (*tmuxctl.Client, string) {
	t.Helper()
	client, pane := startPane(t)
	applyStatus(t, client, pane, paneregistry.StatusBusy, at, heldLaunch()...)
	wantState(t, client, pane, protocol.StateWorking)
	wantOption(t, client, pane, protocol.OptionHeld, "4")
	return client, pane
}

func TestPaneHeldWhenTurnEndsWhileBusy(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	client, pane := startPane(t)
	transition := applyStatus(t, client, pane, paneregistry.StatusBusy, t0, heldLaunch()[:2]...)
	wantTransition(t, transition, "", protocol.StateWorking)
	wantOption(t, client, pane, protocol.OptionHeld, "")

	transition = applyStatus(t, client, pane, paneregistry.StatusBusy, t0.Add(time.Second), heldLaunch()...)
	wantTransition(t, transition, protocol.StateWorking, protocol.StateWorking)
	wantState(t, client, pane, protocol.StateWorking)
	wantOption(t, client, pane, protocol.OptionHeld, "4")
	wantOption(t, client, pane, protocol.OptionTool, "")
	wantOption(t, client, pane, protocol.OptionChanged, "1700000000")
}

func TestPaneHeldEndsWhenTheTasksTurnStarts(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	client, pane := holdPane(t, t0)

	lines := append(heldLaunch(), heldNotification(20))
	transition := applyStatus(t, client, pane, paneregistry.StatusBusy, t0.Add(20*time.Second), lines...)
	wantTransition(t, transition, protocol.StateWorking, protocol.StateWorking)
	wantOption(t, client, pane, protocol.OptionHeld, "")
	wantOption(t, client, pane, protocol.OptionTurnStart, "20")

	lines = append(lines, heldStopped(22, "end_turn", registryText("The agent found it.")))
	transition = applyStatus(t, client, pane, paneregistry.StatusIdle, t0.Add(22*time.Second), lines...)
	wantTransition(t, transition, protocol.StateWorking, protocol.StateDone)
	wantOption(t, client, pane, protocol.OptionHeld, "")
	wantOption(t, client, pane, protocol.OptionChanged, "1700000022")
}

func TestPaneHeldIdleMakesDone(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	client, pane := holdPane(t, t0)

	transition := applyStatus(t, client, pane, paneregistry.StatusIdle, t0.Add(time.Minute), heldLaunch()...)
	wantTransition(t, transition, protocol.StateWorking, protocol.StateDone)
	wantOption(t, client, pane, protocol.OptionHeld, "")
}

func TestPaneNotHeldWhileTurnRuns(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	cases := []struct {
		name  string
		lines []string
	}{
		{"prompt", []string{registryPrompt(1, "go")}},
		{"tool call", heldLaunch()[:2]},
		{"tool result", heldLaunch()[:3]},
		{"message with no stop", []string{registryPrompt(1, "go"), registryAssistant(2, registryText("streaming"))}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, pane := startPane(t)
			applyStatus(t, client, pane, paneregistry.StatusBusy, t0, tc.lines...)
			wantState(t, client, pane, protocol.StateWorking)
			wantOption(t, client, pane, protocol.OptionHeld, "")
		})
	}
}

func TestPaneHeldAtInterruptionWhileBusy(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	client, pane := startPane(t)
	lines := []string{
		registryPrompt(1, "go"),
		registryAssistant(2, registryText("half a reply")),
		`{"type":"user","timestamp":"` + registryStamp(3) + `","message":{"role":"user","content":[` +
			registryText("[Request interrupted by user]") + `]}}`,
	}
	applyStatus(t, client, pane, paneregistry.StatusBusy, t0, lines...)
	wantState(t, client, pane, protocol.StateWorking)
	wantOption(t, client, pane, protocol.OptionHeld, "3")
}

func TestPaneHeldUnsetWhileWaitingAndBackAfter(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	client, pane := holdPane(t, t0)

	applyPane(t, client, pane, state.Pane{
		Status: paneregistry.StatusWaiting, WaitingFor: paneregistry.WaitingPermission,
		Turn: registryTurn(t, heldLaunch()...),
	}, t0.Add(time.Second))
	wantState(t, client, pane, protocol.StateWaiting)
	wantOption(t, client, pane, protocol.OptionHeld, "")

	applyStatus(t, client, pane, paneregistry.StatusBusy, t0.Add(2*time.Second), heldLaunch()...)
	wantState(t, client, pane, protocol.StateWorking)
	wantOption(t, client, pane, protocol.OptionHeld, "4")
}

func TestPaneHeldKeptByDialogOpen(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	client, pane := holdPane(t, t0)

	applyPane(t, client, pane, state.Pane{
		Status: paneregistry.StatusWaiting, WaitingFor: paneregistry.WaitingDialog,
		Turn: registryTurn(t, heldLaunch()...),
	}, t0.Add(time.Second))
	wantState(t, client, pane, protocol.StateWorking)
	wantOption(t, client, pane, protocol.OptionHeld, "4")
}

func TestPaneHeldUnsetWhenUnbound(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	client, pane := holdPane(t, t0)

	applyPane(t, client, pane, state.Pane{Unbound: true}, t0.Add(time.Second))
	wantOption(t, client, pane, protocol.OptionState, "")
	wantOption(t, client, pane, protocol.OptionHeld, "")
}

func TestEndInterruptedTurnUnsetsHeld(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	client, pane := holdPane(t, t0)

	if !endInterrupted(t, client, pane, t0.Unix()+30, t0.Add(time.Minute)) {
		t.Fatal("EndInterruptedTurn on a held pane with a later interruption = false, want true")
	}
	wantState(t, client, pane, protocol.StateDone)
	wantOption(t, client, pane, protocol.OptionHeld, "")
}
