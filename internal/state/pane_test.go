package state_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/paneregistry"
	"github.com/joshsymonds/orrery/internal/panetranscript"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/state"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// registryStamp returns the RFC 3339 timestamp of the unix second s, in UTC.
func registryStamp(s int) string {
	return time.Unix(int64(s), 0).UTC().Format(time.RFC3339)
}

// registryPrompt returns a prompt line at second s.
func registryPrompt(s int, text string) string {
	return `{"type":"user","timestamp":"` + registryStamp(s) + `","message":{"role":"user","content":"` + text + `"}}`
}

// registryResult returns a user line at second s that answers the tool call id.
func registryResult(s int, id string) string {
	return `{"type":"user","timestamp":"` + registryStamp(s) + `","message":{"role":"user","content":[` +
		`{"type":"tool_result","tool_use_id":"` + id + `","content":"ok"}]}}`
}

// registryAssistant returns an assistant line at second s whose content holds blocks, in order.
func registryAssistant(s int, blocks ...string) string {
	return `{"type":"assistant","timestamp":"` + registryStamp(s) + `","message":{"role":"assistant","content":[` +
		strings.Join(blocks, ",") + `]}}`
}

// registryText returns a text content block.
func registryText(text string) string {
	return `{"type":"text","text":"` + text + `"}`
}

// registryTool returns a tool_use content block that calls name with input, a JSON object.
func registryTool(id, name, input string) string {
	return `{"type":"tool_use","id":"` + id + `","name":"` + name + `","input":` + input + `}`
}

// registryTurn writes lines to a transcript under t.TempDir() and returns the turn that a panetranscript.Reader reads
// from it.
func registryTurn(t *testing.T, lines ...string) panetranscript.Turn {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	turn, err := panetranscript.NewReader(path).Read()
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}
	return turn
}

// applyPane applies update to pane through state.ApplyPane at now, and checks that the reported bool matches the
// transition.
func applyPane(
	t *testing.T, client *tmuxctl.Client, pane string, update state.Pane, now time.Time,
) (state.Transition, bool) {
	t.Helper()
	transition, changed, _, err := state.ApplyPane(t.Context(), client, pane, update, now, nil, nil)
	if err != nil {
		t.Fatalf("ApplyPane: %v", err)
	}
	if changed != (transition.From != transition.To) {
		t.Fatalf("ApplyPane changed = %v for transition %+v", changed, transition)
	}
	return transition, changed
}

// wantTransition checks that got is the transition from one state to another.
func wantTransition(t *testing.T, got state.Transition, from, to protocol.State) {
	t.Helper()
	if want := (state.Transition{From: from, To: to}); got != want {
		t.Fatalf("transition = %+v, want %+v", got, want)
	}
}

func TestPaneRegistryStateBusyMakesWorking(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	client, pane := startPane(t)
	turn := registryTurn(t,
		registryPrompt(1, "go"),
		registryAssistant(2, registryTool("t1", "Bash", `{"command":"make test"}`)),
	)

	transition, changed := applyPane(t, client, pane, state.Pane{Status: paneregistry.StatusBusy, Turn: turn}, t0)
	wantTransition(t, transition, "", protocol.StateWorking)
	if !changed {
		t.Fatal("busy from no state reported no change")
	}
	wantState(t, client, pane, protocol.StateWorking)
	wantOption(t, client, pane, protocol.OptionTool, `{"id":"t1","name":"Bash","input":"make test","start":2}`)
	wantOption(t, client, pane, protocol.OptionTurnStart, "1")
	wantOption(t, client, pane, protocol.OptionChanged, "1700000000")
	wantQuestion(t, client, pane, "")
}

func TestPaneRegistryStateRepeatedBusyKeepsChangedTime(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	client, pane := startPane(t)
	first := registryTurn(t, registryPrompt(1, "go"))
	applyPane(t, client, pane, state.Pane{Status: paneregistry.StatusBusy, Turn: first}, t0)

	second := registryTurn(t, registryPrompt(1, "go"), registryPrompt(5, "again"))
	transition, changed := applyPane(t, client, pane, state.Pane{Status: paneregistry.StatusBusy, Turn: second},
		t0.Add(time.Minute))
	wantTransition(t, transition, protocol.StateWorking, protocol.StateWorking)
	if changed {
		t.Fatal("busy on a working pane reported a change")
	}
	wantOption(t, client, pane, protocol.OptionChanged, "1700000000")
	wantOption(t, client, pane, protocol.OptionTurnStart, "5")
}

func TestPaneRegistryStateWaitingMakesWaiting(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	client, pane := startPane(t)
	applyPane(t, client, pane, state.Pane{
		Status: paneregistry.StatusBusy, Turn: registryTurn(t, registryPrompt(1, "go")),
	}, t0)

	turn := registryTurn(t,
		registryPrompt(1, "go"),
		registryAssistant(2, registryTool("t1", "Bash", `{"command":"ls"}`)),
	)
	transition, _ := applyPane(t, client, pane, state.Pane{
		Status: paneregistry.StatusWaiting, WaitingFor: paneregistry.WaitingPermission, Turn: turn,
	}, t0.Add(time.Second))
	wantTransition(t, transition, protocol.StateWorking, protocol.StateWaiting)
	wantState(t, client, pane, protocol.StateWaiting)
}

func TestPaneRegistryStateIdleOrShellMakesDone(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	for _, status := range []string{paneregistry.StatusIdle, paneregistry.StatusShell} {
		t.Run(status, func(t *testing.T) {
			client, pane := startPane(t)
			applyPane(t, client, pane, state.Pane{
				Status: paneregistry.StatusBusy, Turn: registryTurn(t, registryPrompt(1, "go")),
			}, t0)

			// The finished turn still shows an open call, which a done pane must not keep.
			turn := registryTurn(t,
				registryPrompt(1, "go"),
				registryAssistant(2, registryText("final"), registryTool("t1", "Bash", `{"command":"ls"}`)),
			)
			transition, changed := applyPane(t, client, pane, state.Pane{Status: status, Turn: turn},
				t0.Add(time.Second))
			wantTransition(t, transition, protocol.StateWorking, protocol.StateDone)
			if !changed {
				t.Fatal("ending a working pane reported no change")
			}
			wantState(t, client, pane, protocol.StateDone)
			wantOption(t, client, pane, protocol.OptionTool, "")
			wantQuestion(t, client, pane, "")
		})
	}
}

func TestPaneRegistryStateIdleLeavesNoStateOrDoneAlone(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	idle := state.Pane{Status: paneregistry.StatusIdle, Turn: registryTurn(t, registryPrompt(1, "go"))}

	t.Run("no state", func(t *testing.T) {
		client, pane := startPane(t)
		transition, changed := applyPane(t, client, pane, idle, t0)
		wantTransition(t, transition, "", "")
		if changed {
			t.Fatal("idle on a pane with no state reported a change")
		}
		wantState(t, client, pane, "")
		wantOption(t, client, pane, protocol.OptionChanged, "")
	})

	t.Run("done", func(t *testing.T) {
		client, pane := startPane(t)
		applyPane(t, client, pane, state.Pane{
			Status: paneregistry.StatusBusy, Turn: registryTurn(t, registryPrompt(1, "go")),
		}, t0)
		applyPane(t, client, pane, idle, t0.Add(time.Second))
		wantState(t, client, pane, protocol.StateDone)

		transition, changed := applyPane(t, client, pane, idle, t0.Add(5*time.Second))
		wantTransition(t, transition, protocol.StateDone, protocol.StateDone)
		if changed {
			t.Fatal("idle on a done pane reported a change")
		}
		wantState(t, client, pane, protocol.StateDone)
		wantOption(t, client, pane, protocol.OptionChanged, "1700000001")
	})
}

func TestPaneRegistryStatePermissionQuestionIsOldestPendingCall(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	client, pane := startPane(t)
	turn := registryTurn(t,
		registryPrompt(1, "go"),
		registryAssistant(2,
			registryTool("a", "Bash", `{"command":"ls"}`),
			registryTool("b", "Grep", `{"pattern":"x"}`),
			registryTool("c", "Edit", `{"file_path":"/f"}`),
		),
		registryResult(3, "a"),
	)

	applyPane(t, client, pane, state.Pane{
		Status: paneregistry.StatusWaiting, WaitingFor: paneregistry.WaitingPermission, Turn: turn,
	}, t0)
	wantState(t, client, pane, protocol.StateWaiting)
	wantQuestion(t, client, pane, `{"tool":"Grep","input":{"pattern":"x"}}`)
	wantOption(t, client, pane, protocol.OptionTool, `{"id":"c","name":"Edit","input":"/f","start":2}`)
}

func TestPaneRegistryStatePermissionWithNoPendingCallHasNoQuestion(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	client, pane := startPane(t)
	pending := registryTurn(t,
		registryPrompt(1, "go"),
		registryAssistant(2, registryTool("a", "Bash", `{"command":"ls"}`)),
	)
	applyPane(t, client, pane, state.Pane{
		Status: paneregistry.StatusWaiting, WaitingFor: paneregistry.WaitingPermission, Turn: pending,
	}, t0)
	wantQuestion(t, client, pane, `{"tool":"Bash","input":{"command":"ls"}}`)

	answered := registryTurn(t,
		registryPrompt(1, "go"),
		registryAssistant(2, registryText("done with that")),
	)
	transition, _ := applyPane(t, client, pane, state.Pane{
		Status: paneregistry.StatusWaiting, WaitingFor: paneregistry.WaitingPermission, Turn: answered,
	}, t0.Add(time.Second))
	wantTransition(t, transition, protocol.StateWaiting, protocol.StateWaiting)
	wantState(t, client, pane, protocol.StateWaiting)
	wantQuestion(t, client, pane, "")
}

func TestPaneRegistryStateInputNeededCarriesAskUserQuestion(t *testing.T) {
	t0 := time.Unix(1700000000, 0)

	t.Run("questions of the oldest pending AskUserQuestion", func(t *testing.T) {
		client, pane := startPane(t)
		turn := registryTurn(t,
			registryPrompt(1, "go"),
			registryAssistant(2,
				registryTool("q1", "AskUserQuestion", `{"questions":[{"question":"First?","multiSelect":false}]}`),
				registryTool("q2", "AskUserQuestion", `{"questions":[{"question":"Second?","multiSelect":false}]}`),
			),
		)
		applyPane(t, client, pane, state.Pane{
			Status: paneregistry.StatusWaiting, WaitingFor: paneregistry.WaitingInput, Turn: turn,
		}, t0)
		wantState(t, client, pane, protocol.StateWaiting)
		wantQuestion(t, client, pane,
			`{"tool":"AskUserQuestion","questions":[{"question":"First?","multiSelect":false}]}`)
	})

	t.Run("no AskUserQuestion pending", func(t *testing.T) {
		client, pane := startPane(t)
		turn := registryTurn(t,
			registryPrompt(1, "go"),
			registryAssistant(2, registryTool("a", "Bash", `{"command":"ls"}`)),
		)
		applyPane(t, client, pane, state.Pane{
			Status: paneregistry.StatusWaiting, WaitingFor: paneregistry.WaitingInput, Turn: turn,
		}, t0)
		wantState(t, client, pane, protocol.StateWaiting)
		wantQuestion(t, client, pane, "")
	})
}

func TestPaneRegistryStateDialogOpenChangesNothing(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	client, pane := startPane(t)
	first := registryTurn(t,
		registryPrompt(1, "go"),
		registryAssistant(2, registryTool("a", "Bash", `{"command":"ls"}`)),
	)
	applyPane(t, client, pane, state.Pane{
		Status: paneregistry.StatusWaiting, WaitingFor: paneregistry.WaitingPermission, Turn: first,
	}, t0)
	wantQuestion(t, client, pane, `{"tool":"Bash","input":{"command":"ls"}}`)

	// The dialog's turn differs from the first in every field it could write.
	dialog := registryTurn(t,
		registryPrompt(3, "again"),
		registryAssistant(4, registryTool("b", "Grep", `{"pattern":"y"}`)),
	)
	transition, changed := applyPane(t, client, pane, state.Pane{
		Status: paneregistry.StatusWaiting, WaitingFor: paneregistry.WaitingDialog, Turn: dialog,
	}, t0.Add(time.Second))
	if changed || transition != (state.Transition{}) {
		t.Fatalf("dialog open changed = %v, transition = %+v, want no change", changed, transition)
	}
	wantState(t, client, pane, protocol.StateWaiting)
	wantQuestion(t, client, pane, `{"tool":"Bash","input":{"command":"ls"}}`)
	wantOption(t, client, pane, protocol.OptionTool, `{"id":"a","name":"Bash","input":"ls","start":2}`)
	wantOption(t, client, pane, protocol.OptionTurnStart, "1")
	wantOption(t, client, pane, protocol.OptionChanged, "1700000000")
}

func TestPaneRegistryStateOtherWaitingHasNoQuestion(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	for _, waitingFor := range []string{"", "elicitation"} {
		t.Run(waitingFor, func(t *testing.T) {
			client, pane := startPane(t)
			turn := registryTurn(t,
				registryPrompt(1, "go"),
				registryAssistant(2, registryTool("a", "Bash", `{"command":"ls"}`)),
			)
			applyPane(t, client, pane, state.Pane{
				Status: paneregistry.StatusWaiting, WaitingFor: waitingFor, Turn: turn,
			}, t0)
			wantState(t, client, pane, protocol.StateWaiting)
			wantQuestion(t, client, pane, "")
		})
	}
}

func TestPaneRegistryStateLongQuestionIsTruncated(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	client, pane := startPane(t)
	input := `{"command":"` + strings.Repeat("x", 9000) + `"}`
	turn := registryTurn(t,
		registryPrompt(1, "go"),
		registryAssistant(2, registryTool("a", "Bash", input)),
	)
	applyPane(t, client, pane, state.Pane{
		Status: paneregistry.StatusWaiting, WaitingFor: paneregistry.WaitingPermission, Turn: turn,
	}, t0)

	full := `{"tool":"Bash","input":` + input + `}`
	sum := sha256.Sum256([]byte(full))
	wantQuestion(t, client, pane, `{"tool":"Bash","truncated":true,"sha256":"`+hex.EncodeToString(sum[:])+`"}`)
}

func TestPaneRegistryStateUnboundClearsPane(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	client, pane := startPane(t)
	applyPane(t, client, pane, state.Pane{
		Status: paneregistry.StatusWaiting, WaitingFor: paneregistry.WaitingPermission,
		Turn: registryTurn(t,
			registryPrompt(1, "go"),
			registryAssistant(2, registryTool("a", "Bash", `{"command":"ls"}`)),
		),
	}, t0)
	for _, name := range []string{protocol.OptionClaudeSession, protocol.OptionTranscript,
		protocol.OptionSummary, protocol.OptionLabel, protocol.OptionSeen} {
		if err := client.SetPaneOption(t.Context(), pane, name, "set"); err != nil {
			t.Fatalf("SetPaneOption(%s): %v", name, err)
		}
	}

	transition, changed := applyPane(t, client, pane, state.Pane{Unbound: true}, t0.Add(time.Second))
	wantTransition(t, transition, protocol.StateWaiting, "")
	if !changed {
		t.Fatal("unbinding a waiting pane reported no change")
	}
	wantState(t, client, pane, "")
	for _, name := range []string{
		protocol.OptionChanged, protocol.OptionSeen, protocol.OptionQuestion, protocol.OptionSummary,
		protocol.OptionLabel, protocol.OptionTool, protocol.OptionTurnStart,
		protocol.OptionClaudeSession, protocol.OptionTranscript,
	} {
		wantOption(t, client, pane, name, "")
	}
}

func TestPaneRegistryStateUnknownStatusErrorsAndWritesNothing(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	client, pane := startPane(t)

	_, _, _, err := state.ApplyPane(t.Context(), client, pane,
		state.Pane{Status: "sleeping", Turn: registryTurn(t, registryPrompt(1, "go"))}, t0, nil, nil)
	if err == nil {
		t.Fatal("ApplyPane with status sleeping returned no error")
	}
	wantState(t, client, pane, "")
	wantOption(t, client, pane, protocol.OptionTurnStart, "")
}
