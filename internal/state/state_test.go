package state_test

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/paneregistry"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/state"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
	"github.com/joshsymonds/orrery/internal/tmuxtest"
)

// Every test server starts with -f /dev/null so the user's tmux config never applies.
const (
	noConfig    = "/dev/null"
	testCommand = "sleep 300"
)

// newServer returns a client for a unique private server name and kills that server at cleanup.
func newServer(t *testing.T) *tmuxctl.Client {
	t.Helper()
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("random server name: %v", err)
	}
	name := fmt.Sprintf("orrery-state-test-%d-%x", os.Getpid(), suffix)
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

// startPane returns a client whose server has one session with one pane, and that pane's ID.
func startPane(t *testing.T) (*tmuxctl.Client, string) {
	t.Helper()
	client := newServer(t)
	if _, err := client.Run(t.Context(), "-f", noConfig, "new-session", "-d", "-s", "s1",
		"-c", t.TempDir(), testCommand); err != nil {
		t.Fatalf("new-session: %v", err)
	}
	out, err := client.Run(t.Context(), "list-panes", "-t", "s1", "-F", "#{pane_id}")
	if err != nil {
		t.Fatalf("list-panes: %v", err)
	}
	return client, strings.Fields(out)[0]
}

// option reads pane-scoped option name; ok is false when it is unset.
func option(t *testing.T, client *tmuxctl.Client, pane, name string) (string, bool) {
	t.Helper()
	value, ok, err := client.PaneOption(t.Context(), pane, name)
	if err != nil {
		t.Fatalf("PaneOption(%s): %v", name, err)
	}
	return value, ok
}

// wantState checks the @orrery_state option of pane.
func wantState(t *testing.T, client *tmuxctl.Client, pane string, want protocol.State) {
	t.Helper()
	if got, _ := option(t, client, pane, protocol.OptionState); got != string(want) {
		t.Fatalf("%s = %q, want %q", protocol.OptionState, got, want)
	}
}

// wantQuestion checks the @orrery_question option of pane; want "" means unset.
func wantQuestion(t *testing.T, client *tmuxctl.Client, pane, want string) {
	t.Helper()
	got, ok := option(t, client, pane, protocol.OptionQuestion)
	if want == "" {
		if ok {
			t.Fatalf("%s = %q, want unset", protocol.OptionQuestion, got)
		}
		return
	}
	if !ok || got != want {
		t.Fatalf("%s = (%q, %v), want (%q, true)", protocol.OptionQuestion, got, ok, want)
	}
}

// wantOption checks pane-scoped option name; want "" means unset.
func wantOption(t *testing.T, client *tmuxctl.Client, pane, name, want string) {
	t.Helper()
	got, ok := option(t, client, pane, name)
	if want == "" {
		if ok {
			t.Fatalf("%s = %q, want unset", name, got)
		}
		return
	}
	if !ok || got != want {
		t.Fatalf("%s = (%q, %v), want (%q, true)", name, got, ok, want)
	}
}

// bashInput returns the tool_input of a Bash call that runs command.
func bashInput(command string) string {
	return `{"command":"` + command + `"}`
}

// writeInput returns the tool_input of a Write of content to synthetic.txt.
func writeInput(content string) string {
	return `{"file_path":"synthetic.txt","content":"` + content + `"}`
}

// applyBusy applies a busy registry update at at to pane, whose transcript is lines.
func applyBusy(t *testing.T, client *tmuxctl.Client, pane string, at time.Time, lines ...string) {
	t.Helper()
	applyPane(t, client, pane, state.Pane{Status: paneregistry.StatusBusy, Turn: registryTurn(t, lines...)}, at)
}

// waitingOn applies a waiting registry update at at to pane, with waitingFor as its reason. The turn started at unix
// second 1700000000, and its newest assistant line calls tool with input at at, so that call is pending.
func waitingOn(t *testing.T, client *tmuxctl.Client, pane, waitingFor, tool, input string, at time.Time) {
	t.Helper()
	turn := registryTurn(t,
		registryPrompt(1700000000, "go"),
		registryAssistant(int(at.Unix()), registryTool("c1", tool, input)),
	)
	applyPane(t, client, pane,
		state.Pane{Status: paneregistry.StatusWaiting, WaitingFor: waitingFor, Turn: turn}, at)
}

// waitingBash starts a pane whose turn started at unix second 1700000000, and waits at at for permission to run the
// synthetic Bash command.
func waitingBash(t *testing.T, at time.Time) (*tmuxctl.Client, string) {
	t.Helper()
	client, pane := startPane(t)
	applyBusy(t, client, pane, time.Unix(1700000000, 0), registryPrompt(1700000000, "go"))
	waitingOn(t, client, pane, paneregistry.WaitingPermission, "Bash", bashInput("echo synthetic"), at)
	return client, pane
}

// startTurn starts a turn at at with the synthetic Bash call in progress. A working pane holds no question, so the
// test sets one, to show that ending the turn clears it.
func startTurn(t *testing.T, client *tmuxctl.Client, pane string, at time.Time) {
	t.Helper()
	stamp := int(at.Unix())
	applyBusy(t, client, pane, at,
		registryPrompt(stamp, "go"),
		registryAssistant(stamp, registryTool("synthetic-1", "Bash", bashInput("echo synthetic"))),
	)
	if err := client.SetPaneOption(t.Context(), pane, protocol.OptionQuestion, "synthetic question"); err != nil {
		t.Fatalf("SetPaneOption(%s): %v", protocol.OptionQuestion, err)
	}
}

// endTurn applies an idle registry update at at, which ends the pane's turn.
func endTurn(t *testing.T, client *tmuxctl.Client, pane string, at time.Time) {
	t.Helper()
	applyPane(t, client, pane, state.Pane{Status: paneregistry.StatusIdle}, at)
}

// setWorking writes @orrery_state as working, for a pane whose @orrery_changed is unset or malformed, which no
// registry update leaves.
func setWorking(t *testing.T, client *tmuxctl.Client, pane string) {
	t.Helper()
	if err := client.SetPaneOption(t.Context(), pane, protocol.OptionState,
		string(protocol.StateWorking)); err != nil {
		t.Fatalf("SetPaneOption(%s): %v", protocol.OptionState, err)
	}
}

// endInterrupted runs state.EndInterruptedTurn and fails the test on error.
func endInterrupted(t *testing.T, client *tmuxctl.Client, pane string, interrupted int64, now time.Time) bool {
	t.Helper()
	ended, err := state.EndInterruptedTurn(t.Context(), client, pane, interrupted, now)
	if err != nil {
		t.Fatalf("EndInterruptedTurn(%d): %v", interrupted, err)
	}
	return ended
}

// captureQuestion starts a pane, applies a waiting update whose oldest pending call is tool with input, and returns the
// stored question.
func captureQuestion(t *testing.T, waitingFor, tool, input string) string {
	t.Helper()
	client, pane := startPane(t)
	waitingOn(t, client, pane, waitingFor, tool, input, time.Unix(1700000000, 0))
	wantState(t, client, pane, protocol.StateWaiting)
	got, ok := option(t, client, pane, protocol.OptionQuestion)
	if !ok {
		t.Fatalf("%s unset after waiting", protocol.OptionQuestion)
	}
	return got
}

// bashQuestion is the stored form of a question for the Bash call of command, before any truncation.
func bashQuestion(command string) string {
	return `{"tool":"Bash","input":{"command":"` + command + `"}}`
}

// truncatedMarker is the stored form of a question whose full encoding is full and exceeds the bound.
func truncatedMarker(tool, full string) string {
	sum := sha256.Sum256([]byte(full))
	return `{"tool":"` + tool + `","truncated":true,"sha256":"` + hex.EncodeToString(sum[:]) + `"}`
}

// toolRecord is the decoded @orrery_tool value the tool tests check.
type toolRecord struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Input string `json:"input"`
	Start int64  `json:"start"`
}

// readTool decodes the @orrery_tool option of pane; ok is false when it is unset.
func readTool(t *testing.T, client *tmuxctl.Client, pane string) (toolRecord, bool) {
	t.Helper()
	raw, ok := option(t, client, pane, protocol.OptionTool)
	if !ok {
		return toolRecord{}, false
	}
	var record toolRecord
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		t.Fatalf("%s = %q, does not decode: %v", protocol.OptionTool, raw, err)
	}
	return record, true
}

// wantToolID checks the id of the @orrery_tool option of pane; want "" means unset.
func wantToolID(t *testing.T, client *tmuxctl.Client, pane, want string) {
	t.Helper()
	record, ok := readTool(t, client, pane)
	if want == "" {
		if ok {
			t.Fatalf("%s = %+v, want unset", protocol.OptionTool, record)
		}
		return
	}
	if !ok || record.ID != want {
		t.Fatalf("%s id = (%q, %v), want (%q, true)", protocol.OptionTool, record.ID, ok, want)
	}
}

func TestQuestionCapture(t *testing.T) {
	const askInput = `{"questions": [{"question": "Pick one?", "header": "Choice", "multiSelect": false, ` +
		`"options": [{"label": "A", "description": "first"}, {"label": "B", "description": "second"}]}]}`
	const askWant = `{"tool":"AskUserQuestion","questions":[` +
		`{"question":"Pick one?","header":"Choice","multiSelect":false,` +
		`"options":[{"label":"A","description":"first"},{"label":"B","description":"second"}]}]}`

	t.Run("AskUserQuestion stores its questions compactly", func(t *testing.T) {
		if got := captureQuestion(t, paneregistry.WaitingInput, "AskUserQuestion", askInput); got != askWant {
			t.Fatalf("question = %q, want %q", got, askWant)
		}
	})

	t.Run("other tools store their input", func(t *testing.T) {
		const want = `{"tool":"Bash","input":{"command":"echo synthetic"}}`
		if got := captureQuestion(t, paneregistry.WaitingPermission, "Bash", bashInput("echo synthetic")); got != want {
			t.Fatalf("question = %q, want %q", got, want)
		}
	})

	t.Run("stored value at the 8192-byte bound is kept whole", func(t *testing.T) {
		command := strings.Repeat("a", 8192-len(bashQuestion("")))
		want := bashQuestion(command)
		if len(want) != 8192 {
			t.Fatalf("test fixture is %d bytes, want 8192", len(want))
		}
		if got := captureQuestion(t, paneregistry.WaitingPermission, "Bash", bashInput(command)); got != want {
			t.Fatalf("question at bound = %d bytes, want the whole value", len(got))
		}
	})

	t.Run("stored value over the 8192-byte bound is truncated", func(t *testing.T) {
		command := strings.Repeat("a", 8192-len(bashQuestion(""))+1)
		want := truncatedMarker("Bash", bashQuestion(command))
		if got := captureQuestion(t, paneregistry.WaitingPermission, "Bash", bashInput(command)); got != want {
			t.Fatalf("question over bound = %q, want %q", got, want)
		}
	})

	t.Run("oversized AskUserQuestion input is truncated", func(t *testing.T) {
		huge := strings.Repeat("x", 9000)
		full := `{"tool":"AskUserQuestion","questions":"` + huge + `"}`
		got := captureQuestion(t, paneregistry.WaitingInput, "AskUserQuestion", `{"questions":"`+huge+`"}`)
		if want := truncatedMarker("AskUserQuestion", full); got != want {
			t.Fatalf("question = %q, want %q", got, want)
		}
	})

	t.Run("distinct oversized prompts store distinct values", func(t *testing.T) {
		client, pane := startPane(t)
		first := strings.Repeat("a", 9000)
		second := strings.Repeat("b", 9000)
		firstFull := `{"tool":"Write","input":` + writeInput(first) + `}`
		secondFull := `{"tool":"Write","input":` + writeInput(second) + `}`

		waitingOn(t, client, pane, paneregistry.WaitingPermission, "Write", writeInput(first),
			time.Unix(1700000000, 0))
		firstValue, _ := option(t, client, pane, protocol.OptionQuestion)
		if want := truncatedMarker("Write", firstFull); firstValue != want {
			t.Fatalf("first question = %q, want %q", firstValue, want)
		}

		waitingOn(t, client, pane, paneregistry.WaitingPermission, "Write", writeInput(second),
			time.Unix(1700000001, 0))
		secondValue, _ := option(t, client, pane, protocol.OptionQuestion)
		if want := truncatedMarker("Write", secondFull); secondValue != want {
			t.Fatalf("second question = %q, want %q", secondValue, want)
		}
		if firstValue == secondValue {
			t.Fatalf("distinct oversized prompts both stored %q", firstValue)
		}

		waitingOn(t, client, pane, paneregistry.WaitingPermission, "Write", writeInput(first),
			time.Unix(1700000002, 0))
		repeated, _ := option(t, client, pane, protocol.OptionQuestion)
		if repeated != firstValue {
			t.Fatalf("repeated oversized prompt stored %q, want %q", repeated, firstValue)
		}
	})

	t.Run("a newer call replaces the question while waiting", func(t *testing.T) {
		client, pane := startPane(t)
		t0 := time.Unix(1700000000, 0)
		waitingOn(t, client, pane, paneregistry.WaitingPermission, "Bash", bashInput("echo synthetic"), t0)
		applyPane(t, client, pane, state.Pane{
			Status: paneregistry.StatusWaiting, WaitingFor: paneregistry.WaitingPermission,
			Turn: registryTurn(t,
				registryPrompt(1700000000, "go"),
				registryAssistant(1700000000, registryTool("c1", "Bash", bashInput("echo synthetic"))),
				registryResult(1700000001, "c1"),
				registryAssistant(1700000001, registryTool("c2", "AskUserQuestion", `{"questions":[]}`)),
			),
		}, t0.Add(time.Second))
		wantState(t, client, pane, protocol.StateWaiting)
		wantQuestion(t, client, pane, `{"tool":"AskUserQuestion","questions":[]}`)
	})

	t.Run("cleared when the pane is working again", func(t *testing.T) {
		client, pane := waitingBash(t, time.Unix(1700000001, 0))
		wantQuestion(t, client, pane, bashQuestion("echo synthetic"))

		applyBusy(t, client, pane, time.Unix(1700000002, 0),
			registryPrompt(1700000000, "go"),
			registryAssistant(1700000001, registryTool("c1", "Bash", bashInput("echo synthetic"))),
		)
		wantState(t, client, pane, protocol.StateWorking)
		wantQuestion(t, client, pane, "")
	})
}

func TestToolState(t *testing.T) {
	t0 := time.Unix(1700000000, 0)

	t.Run("an open call records its id, name, input and start", func(t *testing.T) {
		client, pane := startPane(t)
		applyBusy(t, client, pane, t0,
			registryPrompt(1700000000, "go"),
			registryAssistant(1700000001, registryTool("synthetic-1", "Bash", bashInput("echo synthetic"))),
		)
		record, ok := readTool(t, client, pane)
		want := toolRecord{ID: "synthetic-1", Name: "Bash", Input: "echo synthetic", Start: 1700000001}
		if !ok || record != want {
			t.Fatalf("%s = %+v (set %v), want %+v", protocol.OptionTool, record, ok, want)
		}
	})

	t.Run("multi-line command is stored as one clipped line", func(t *testing.T) {
		client, pane := startPane(t)
		// The newlines are JSON escapes in the transcript line.
		command := `first line\nsecond line ` + strings.Repeat("x", 250)
		applyBusy(t, client, pane, t0,
			registryPrompt(1700000000, "go"),
			registryAssistant(1700000000, registryTool("synthetic-1", "Bash", bashInput(command))),
		)
		raw, _ := option(t, client, pane, protocol.OptionTool)
		if strings.Contains(raw, "\n") {
			t.Fatalf("%s = %q, want one line", protocol.OptionTool, raw)
		}
		record, _ := readTool(t, client, pane)
		// 23 characters of the first two lines, then x up to the 200-character clip.
		want := "first line second line " + strings.Repeat("x", 177)
		if record.Input != want {
			t.Fatalf("input = %q, want %q", record.Input, want)
		}
	})

	t.Run("a result for the open call clears it", func(t *testing.T) {
		client, pane := startPane(t)
		prompt := registryPrompt(1700000000, "go")
		call := registryAssistant(1700000000, registryTool("synthetic-1", "Bash", bashInput("echo synthetic")))
		applyBusy(t, client, pane, t0, prompt, call)
		wantToolID(t, client, pane, "synthetic-1")

		applyBusy(t, client, pane, t0.Add(time.Second), prompt, call, registryResult(1700000001, "synthetic-1"))
		wantToolID(t, client, pane, "")
	})

	t.Run("a result for another call leaves it", func(t *testing.T) {
		client, pane := startPane(t)
		prompt := registryPrompt(1700000000, "go")
		call := registryAssistant(1700000000, registryTool("synthetic-1", "Bash", bashInput("echo synthetic")))
		applyBusy(t, client, pane, t0, prompt, call)

		applyBusy(t, client, pane, t0.Add(time.Second), prompt, call, registryResult(1700000001, "synthetic-2"))
		wantToolID(t, client, pane, "synthetic-1")
	})

	t.Run("parallel batch keeps the later tool until its own end", func(t *testing.T) {
		client, pane := startPane(t)
		prompt := registryPrompt(1700000000, "go")
		first := registryAssistant(1700000000, registryTool("synthetic-a", "Bash", bashInput("echo a")))
		second := registryAssistant(1700000001, registryTool("synthetic-b", "Bash", bashInput("echo b")))
		applyBusy(t, client, pane, t0, prompt, first)
		applyBusy(t, client, pane, t0.Add(time.Second), prompt, first, second)
		wantToolID(t, client, pane, "synthetic-b")

		applyBusy(t, client, pane, t0.Add(2*time.Second), prompt, first, second,
			registryResult(1700000002, "synthetic-a"))
		wantToolID(t, client, pane, "synthetic-b")

		applyBusy(t, client, pane, t0.Add(3*time.Second), prompt, first, second,
			registryResult(1700000002, "synthetic-a"), registryResult(1700000003, "synthetic-b"))
		wantToolID(t, client, pane, "")
	})
}

func TestEndInterruptedTurn(t *testing.T) {
	t0 := time.Unix(1700000000, 0)

	t.Run("interruption after the turn started ends it as a Stop does", func(t *testing.T) {
		client, pane := startPane(t)
		startTurn(t, client, pane, t0)
		wantToolID(t, client, pane, "synthetic-1")
		wantQuestion(t, client, pane, "synthetic question")

		now := t0.Add(2 * time.Second)
		if !endInterrupted(t, client, pane, t0.Unix()+1, now) {
			t.Fatal("EndInterruptedTurn one second after changed = false, want true")
		}
		wantState(t, client, pane, protocol.StateDone)
		if got, _ := option(t, client, pane, protocol.OptionChanged); got != fmt.Sprint(now.Unix()) {
			t.Fatalf("%s = %q, want the end time %d", protocol.OptionChanged, got, now.Unix())
		}
		wantToolID(t, client, pane, "")
		wantQuestion(t, client, pane, "")
	})

	t.Run("interruption at or before the turn start leaves the pane alone", func(t *testing.T) {
		for _, tc := range []struct {
			name        string
			interrupted int64
		}{
			{"no interruption", 0},
			{"interruption at changed", t0.Unix()},
			{"older interruption", t0.Unix() - 1},
		} {
			t.Run(tc.name, func(t *testing.T) {
				client, pane := startPane(t)
				startTurn(t, client, pane, t0)

				if endInterrupted(t, client, pane, tc.interrupted, t0.Add(2*time.Second)) {
					t.Fatalf("EndInterruptedTurn(%d) = true, want false", tc.interrupted)
				}
				wantState(t, client, pane, protocol.StateWorking)
				if got, _ := option(t, client, pane, protocol.OptionChanged); got != "1700000000" {
					t.Fatalf("%s = %q, want 1700000000", protocol.OptionChanged, got)
				}
				wantToolID(t, client, pane, "synthetic-1")
				wantQuestion(t, client, pane, "synthetic question")
			})
		}
	})

	t.Run("interruption from an earlier turn leaves a later turn alone", func(t *testing.T) {
		client, pane := startPane(t)
		startTurn(t, client, pane, t0)
		endTurn(t, client, pane, t0.Add(5*time.Second))
		startTurn(t, client, pane, t0.Add(10*time.Second))

		// The interruption is inside the first turn, before the second turn started.
		if endInterrupted(t, client, pane, t0.Unix()+3, t0.Add(12*time.Second)) {
			t.Fatal("EndInterruptedTurn on a later turn = true, want false")
		}
		wantState(t, client, pane, protocol.StateWorking)
		if got, _ := option(t, client, pane, protocol.OptionChanged); got != "1700000010" {
			t.Fatalf("%s = %q, want 1700000010", protocol.OptionChanged, got)
		}
		wantToolID(t, client, pane, "synthetic-1")
		wantQuestion(t, client, pane, "synthetic question")
	})

	t.Run("interruption before a re-prompt on a working pane leaves the new turn alone", func(t *testing.T) {
		client, pane := startPane(t)
		startTurn(t, client, pane, t0)
		// A re-prompt on a working pane changes no state, so @orrery_changed stays at the first prompt.
		applyBusy(t, client, pane, t0.Add(time.Minute),
			registryPrompt(1700000000, "go"),
			registryAssistant(1700000000, registryTool("synthetic-1", "Bash", bashInput("echo synthetic"))),
			registryPrompt(1700000060, "again"),
		)
		wantState(t, client, pane, protocol.StateWorking)

		// The interruption is after the first prompt and before the re-prompt, so it belongs to the first turn.
		if endInterrupted(t, client, pane, t0.Unix()+1, t0.Add(2*time.Minute)) {
			t.Fatal("EndInterruptedTurn before the re-prompt = true, want false")
		}
		wantState(t, client, pane, protocol.StateWorking)
		if got, _ := option(t, client, pane, protocol.OptionChanged); got != "1700000000" {
			t.Fatalf("%s = %q, want 1700000000", protocol.OptionChanged, got)
		}
		wantToolID(t, client, pane, "synthetic-1")
	})

	t.Run("waiting pane with interruption not after changed is left alone", func(t *testing.T) {
		client, pane := waitingBash(t, t0.Add(time.Second))
		wantQuestion(t, client, pane, bashQuestion("echo synthetic"))

		// The interruption is at the waiting pane's changed time, which is not later than it.
		if endInterrupted(t, client, pane, t0.Unix()+1, t0.Add(3*time.Second)) {
			t.Fatal("EndInterruptedTurn on a waiting pane = true, want false")
		}
		wantState(t, client, pane, protocol.StateWaiting)
		if got, _ := option(t, client, pane, protocol.OptionChanged); got != "1700000001" {
			t.Fatalf("%s = %q, want 1700000001", protocol.OptionChanged, got)
		}
		wantQuestion(t, client, pane, bashQuestion("echo synthetic"))
	})

	t.Run("done pane is left alone", func(t *testing.T) {
		client, pane := startPane(t)
		startTurn(t, client, pane, t0)
		endTurn(t, client, pane, t0.Add(4*time.Second))

		if endInterrupted(t, client, pane, t0.Unix()+5, t0.Add(6*time.Second)) {
			t.Fatal("EndInterruptedTurn on a done pane = true, want false")
		}
		wantState(t, client, pane, protocol.StateDone)
		if got, _ := option(t, client, pane, protocol.OptionChanged); got != "1700000004" {
			t.Fatalf("%s = %q, want the Stop time 1700000004", protocol.OptionChanged, got)
		}
	})

	t.Run("unset changed reads as zero", func(t *testing.T) {
		client, pane := startPane(t)
		setWorking(t, client, pane)

		if endInterrupted(t, client, pane, 0, t0) {
			t.Fatal("EndInterruptedTurn(0) with unset changed = true, want false")
		}
		wantState(t, client, pane, protocol.StateWorking)

		now := t0.Add(time.Second)
		if !endInterrupted(t, client, pane, 1, now) {
			t.Fatal("EndInterruptedTurn(1) with unset changed = false, want true")
		}
		wantState(t, client, pane, protocol.StateDone)
		if got, _ := option(t, client, pane, protocol.OptionChanged); got != fmt.Sprint(now.Unix()) {
			t.Fatalf("%s = %q, want %d", protocol.OptionChanged, got, now.Unix())
		}
	})

	t.Run("malformed changed is an error and writes nothing", func(t *testing.T) {
		client, pane := startPane(t)
		setWorking(t, client, pane)
		if err := client.SetPaneOption(t.Context(), pane, protocol.OptionChanged, "synthetic"); err != nil {
			t.Fatalf("SetPaneOption(%s): %v", protocol.OptionChanged, err)
		}

		if _, err := state.EndInterruptedTurn(t.Context(), client, pane, t0.Unix()+1, t0); err == nil {
			t.Fatal("EndInterruptedTurn with malformed changed: want error, got nil")
		}
		wantState(t, client, pane, protocol.StateWorking)
	})
}
