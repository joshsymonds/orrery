package state_test

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// Synthetic Claude Code hook events.
const (
	userPromptSubmitHook = `{"hook_event_name":"UserPromptSubmit","prompt":"synthetic prompt"}`
	preToolUseHook       = `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"echo synthetic"}}`
	postToolUseHook      = `{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"echo synthetic"}}`
	postToolFailureHook  = `{"hook_event_name":"PostToolUseFailure","tool_name":"Bash","tool_input":{"command":"false"}}`
	permissionHook       = `{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"echo synthetic"}}`
	stopHook             = `{"hook_event_name":"Stop","stop_hook_active":false}`
	sessionEndHook       = `{"hook_event_name":"SessionEnd","reason":"other"}`
	permissionPromptHook = `{"hook_event_name":"Notification","notification_type":"permission_prompt","message":"synthetic"}`
	elicitationHook      = `{"hook_event_name":"Notification","notification_type":"elicitation_dialog","message":"synthetic"}`
	agentInputHook       = `{"hook_event_name":"Notification","notification_type":"agent_needs_input","message":"synthetic"}`
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

// apply runs one hook event through state.Apply and checks that the reported bool matches the transition.
func apply(t *testing.T, client *tmuxctl.Client, pane, hook string, now time.Time) (state.Transition, bool) {
	t.Helper()
	transition, changed, err := state.Apply(t.Context(), client, pane, json.RawMessage(hook), now)
	if err != nil {
		t.Fatalf("Apply(%s): %v", hook, err)
	}
	if changed != (transition.From != transition.To) {
		t.Fatalf("Apply(%s) changed = %v for transition %+v", hook, changed, transition)
	}
	return transition, changed
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

func TestPaneStateTransitions(t *testing.T) {
	t0 := time.Unix(1700000000, 0)

	t.Run("mapped events", func(t *testing.T) {
		cases := []struct {
			name string
			hook string
			want protocol.State
		}{
			{"UserPromptSubmit", userPromptSubmitHook, protocol.StateWorking},
			{"PreToolUse", preToolUseHook, protocol.StateWorking},
			{"PostToolUse", postToolUseHook, protocol.StateWorking},
			{"PostToolUseFailure", postToolFailureHook, protocol.StateWorking},
			{"PermissionRequest", permissionHook, protocol.StateWaiting},
			{"Notification permission_prompt", permissionPromptHook, protocol.StateWaiting},
			{"Notification elicitation_dialog", elicitationHook, protocol.StateWaiting},
			{"Notification agent_needs_input", agentInputHook, protocol.StateWaiting},
			{"Stop", stopHook, protocol.StateDone},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				client, pane := startPane(t)
				transition, changed := apply(t, client, pane, tc.hook, t0)
				if transition != (state.Transition{From: "", To: tc.want}) || !changed {
					t.Fatalf("transition = %+v changed = %v, want {From:\"\" To:%q} true", transition, changed, tc.want)
				}
				wantState(t, client, pane, tc.want)
				if got, _ := option(t, client, pane, protocol.OptionChanged); got != "1700000000" {
					t.Fatalf("%s = %q, want 1700000000", protocol.OptionChanged, got)
				}
			})
		}
	})

	t.Run("changed moves only on change", func(t *testing.T) {
		client, pane := startPane(t)
		apply(t, client, pane, userPromptSubmitHook, t0)

		transition, changed := apply(t, client, pane, postToolUseHook, t0.Add(10*time.Second))
		if transition != (state.Transition{From: protocol.StateWorking, To: protocol.StateWorking}) || changed {
			t.Fatalf("working->working transition = %+v changed = %v", transition, changed)
		}
		if got, _ := option(t, client, pane, protocol.OptionChanged); got != "1700000000" {
			t.Fatalf("%s after same-state event = %q, want 1700000000", protocol.OptionChanged, got)
		}

		transition, changed = apply(t, client, pane, stopHook, t0.Add(20*time.Second))
		if transition != (state.Transition{From: protocol.StateWorking, To: protocol.StateDone}) || !changed {
			t.Fatalf("working->done transition = %+v changed = %v", transition, changed)
		}
		if got, _ := option(t, client, pane, protocol.OptionChanged); got != "1700000020" {
			t.Fatalf("%s after change = %q, want 1700000020", protocol.OptionChanged, got)
		}
	})

	t.Run("approve then run", func(t *testing.T) {
		client, pane := startPane(t)
		steps := []struct {
			hook     string
			from, to protocol.State
		}{
			{preToolUseHook, "", protocol.StateWorking},
			{permissionHook, protocol.StateWorking, protocol.StateWaiting},
			{postToolUseHook, protocol.StateWaiting, protocol.StateWorking},
			{stopHook, protocol.StateWorking, protocol.StateDone},
		}
		for _, step := range steps {
			transition, changed := apply(t, client, pane, step.hook, t0)
			if transition != (state.Transition{From: step.from, To: step.to}) || changed != (step.from != step.to) {
				t.Fatalf("Apply(%s) = %+v changed = %v, want {%q %q} changed %v",
					step.hook, transition, changed, step.from, step.to, step.from != step.to)
			}
			wantState(t, client, pane, step.to)
		}
	})

	t.Run("SessionEnd unsets all options", func(t *testing.T) {
		client, pane := startPane(t)
		apply(t, client, pane, userPromptSubmitHook, t0)
		for name, value := range map[string]string{
			protocol.OptionSeen:     "1700000001",
			protocol.OptionQuestion: "synthetic question",
			protocol.OptionSummary:  "synthetic summary",
			protocol.OptionLabel:    "synthetic label",
		} {
			if err := client.SetPaneOption(t.Context(), pane, name, value); err != nil {
				t.Fatalf("SetPaneOption(%s): %v", name, err)
			}
		}

		transition, changed := apply(t, client, pane, sessionEndHook, t0.Add(30*time.Second))
		if transition != (state.Transition{From: protocol.StateWorking, To: ""}) || !changed {
			t.Fatalf("SessionEnd transition = %+v changed = %v", transition, changed)
		}
		// The six per-pane options SessionEnd unsets.
		for _, name := range []string{
			protocol.OptionState,
			protocol.OptionChanged,
			protocol.OptionSeen,
			protocol.OptionQuestion,
			protocol.OptionSummary,
			protocol.OptionLabel,
		} {
			if got, ok := option(t, client, pane, name); ok {
				t.Fatalf("%s after SessionEnd = %q, want unset", name, got)
			}
		}
	})

	t.Run("SessionEnd without state reports no change", func(t *testing.T) {
		client, pane := startPane(t)
		transition, changed := apply(t, client, pane, sessionEndHook, t0)
		if transition != (state.Transition{}) || changed {
			t.Fatalf("SessionEnd on fresh pane = %+v changed = %v, want zero and false", transition, changed)
		}
	})

	t.Run("unhandled events change nothing", func(t *testing.T) {
		client, pane := startPane(t)
		apply(t, client, pane, userPromptSubmitHook, t0)

		unhandled := []string{
			`{"hook_event_name":"SessionStart","source":"synthetic"}`,
			`{"hook_event_name":"PreCompact","trigger":"manual"}`,
			`{"hook_event_name":"Notification","notification_type":"idle_prompt","message":"synthetic"}`,
			`{"hook_event_name":"Notification","message":"synthetic"}`,
			`{"tool_name":"Bash"}`,
			`{}`,
			`null`,
		}
		for _, hook := range unhandled {
			transition, changed, err := state.Apply(
				t.Context(), client, pane, json.RawMessage(hook), t0.Add(time.Minute))
			if err != nil || transition != (state.Transition{}) || changed {
				t.Fatalf("Apply(%s) = (%+v, %v, %v), want zero, false, nil", hook, transition, changed, err)
			}
		}
		wantState(t, client, pane, protocol.StateWorking)
		if got, _ := option(t, client, pane, protocol.OptionChanged); got != "1700000000" {
			t.Fatalf("%s after unhandled events = %q, want 1700000000", protocol.OptionChanged, got)
		}
	})

	t.Run("unhandled events make no tmux call", func(t *testing.T) {
		client, err := tmuxctl.New(tmuxctl.Server{Socket: filepath.Join(t.TempDir(), "absent.sock")})
		if err != nil {
			t.Fatalf("New(Socket): %v", err)
		}
		hook := json.RawMessage(`{"hook_event_name":"PreCompact"}`)
		transition, changed, err := state.Apply(t.Context(), client, "%0", hook, t0)
		if err != nil || transition != (state.Transition{}) || changed {
			t.Fatalf("unhandled Apply against absent server = (%+v, %v, %v), want zero, false, nil",
				transition, changed, err)
		}
	})

	t.Run("malformed hook is an error", func(t *testing.T) {
		client, pane := startPane(t)
		_, _, err := state.Apply(t.Context(), client, pane, json.RawMessage(`{"hook_event_name":`), t0)
		if err == nil {
			t.Fatal("Apply(malformed JSON): want error, got nil")
		}
	})
}

// TestDonePaneIgnoresLateToolEvents replays a permission-answered turn whose Stop is applied before a
// tool event, the order that left a live session's pane working after its turn ended.
func TestDonePaneIgnoresLateToolEvents(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	replayTurn := func(t *testing.T, client *tmuxctl.Client, pane string) {
		t.Helper()
		steps := []struct {
			hook string
			at   time.Duration
		}{
			{userPromptSubmitHook, 0},
			{preToolUseHook, time.Second},
			{permissionHook, 2 * time.Second},
			{permissionPromptHook, 3 * time.Second},
			{stopHook, 4 * time.Second},
		}
		for _, step := range steps {
			apply(t, client, pane, step.hook, t0.Add(step.at))
		}
		wantState(t, client, pane, protocol.StateDone)
	}

	t.Run("late tool events leave the pane done", func(t *testing.T) {
		late := []struct {
			name string
			hook string
		}{
			{"PreToolUse", preToolUseHook},
			{"PostToolUse", postToolUseHook},
			{"PostToolUseFailure", postToolFailureHook},
		}
		for _, tc := range late {
			t.Run(tc.name, func(t *testing.T) {
				client, pane := startPane(t)
				replayTurn(t, client, pane)

				transition, changed := apply(t, client, pane, tc.hook, t0.Add(5*time.Second))
				if transition != (state.Transition{From: protocol.StateDone, To: protocol.StateDone}) || changed {
					t.Fatalf("late %s transition = %+v changed = %v, want done to done and false",
						tc.name, transition, changed)
				}
				wantState(t, client, pane, protocol.StateDone)
				if got, _ := option(t, client, pane, protocol.OptionChanged); got != "1700000004" {
					t.Fatalf("%s after late %s = %q, want the Stop time 1700000004",
						protocol.OptionChanged, tc.name, got)
				}
				wantQuestion(t, client, pane, "")
			})
		}
	})

	t.Run("UserPromptSubmit after Stop starts working", func(t *testing.T) {
		client, pane := startPane(t)
		replayTurn(t, client, pane)

		transition, changed := apply(t, client, pane, userPromptSubmitHook, t0.Add(5*time.Second))
		if transition != (state.Transition{From: protocol.StateDone, To: protocol.StateWorking}) || !changed {
			t.Fatalf("UserPromptSubmit after Stop transition = %+v changed = %v", transition, changed)
		}
		wantState(t, client, pane, protocol.StateWorking)
		if got, _ := option(t, client, pane, protocol.OptionChanged); got != "1700000005" {
			t.Fatalf("%s after UserPromptSubmit = %q, want 1700000005", protocol.OptionChanged, got)
		}
	})

	t.Run("PermissionRequest and waiting Notification after Stop still apply", func(t *testing.T) {
		for _, hook := range []string{permissionHook, permissionPromptHook} {
			client, pane := startPane(t)
			replayTurn(t, client, pane)

			transition, changed := apply(t, client, pane, hook, t0.Add(5*time.Second))
			if transition != (state.Transition{From: protocol.StateDone, To: protocol.StateWaiting}) || !changed {
				t.Fatalf("Apply(%s) after Stop transition = %+v changed = %v", hook, transition, changed)
			}
			wantState(t, client, pane, protocol.StateWaiting)
		}
	})
}

// setPaneState writes @orrery_state directly, for a pane whose history is not replayed.
func setPaneState(t *testing.T, client *tmuxctl.Client, pane string, want protocol.State) {
	t.Helper()
	if err := client.SetPaneOption(t.Context(), pane, protocol.OptionState, string(want)); err != nil {
		t.Fatalf("SetPaneOption(%s): %v", protocol.OptionState, err)
	}
}

// TestDonePaneToolEventWindow checks where a tool event stops being late: at the window's edge and
// after it, a done pane moves to working, and an unset or malformed @orrery_changed is handled.
func TestDonePaneToolEventWindow(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	stopAt := t0.Add(4 * time.Second)
	tools := []struct {
		name string
		hook string
	}{
		{"PreToolUse", preToolUseHook},
		{"PostToolUse", postToolUseHook},
		{"PostToolUseFailure", postToolFailureHook},
	}

	// donePane returns a pane whose turn ended at stopAt.
	donePane := func(t *testing.T) (*tmuxctl.Client, string) {
		t.Helper()
		client, pane := startPane(t)
		apply(t, client, pane, userPromptSubmitHook, t0)
		apply(t, client, pane, stopHook, stopAt)
		wantState(t, client, pane, protocol.StateDone)
		return client, pane
	}

	for _, offset := range []time.Duration{5 * time.Second, 60 * time.Second} {
		for _, tc := range tools {
			t.Run(fmt.Sprintf("%s at Stop plus %v", tc.name, offset), func(t *testing.T) {
				client, pane := donePane(t)
				at := stopAt.Add(offset)

				transition, changed := apply(t, client, pane, tc.hook, at)
				if transition != (state.Transition{From: protocol.StateDone, To: protocol.StateWorking}) || !changed {
					t.Fatalf("%s transition = %+v changed = %v, want done to working and true",
						tc.name, transition, changed)
				}
				wantState(t, client, pane, protocol.StateWorking)
				if got, _ := option(t, client, pane, protocol.OptionChanged); got != fmt.Sprint(at.Unix()) {
					t.Fatalf("%s = %q, want the event time %d", protocol.OptionChanged, got, at.Unix())
				}
			})
		}
	}

	t.Run("tool event just inside the window leaves the pane done", func(t *testing.T) {
		client, pane := donePane(t)
		at := stopAt.Add(5*time.Second - time.Second)

		transition, changed := apply(t, client, pane, postToolUseHook, at)
		if transition != (state.Transition{From: protocol.StateDone, To: protocol.StateDone}) || changed {
			t.Fatalf("inside-window transition = %+v changed = %v, want done to done and false", transition, changed)
		}
		wantState(t, client, pane, protocol.StateDone)
	})

	t.Run("absent @orrery_changed moves a done pane to working", func(t *testing.T) {
		client, pane := startPane(t)
		setPaneState(t, client, pane, protocol.StateDone)

		transition, changed := apply(t, client, pane, preToolUseHook, t0)
		if transition != (state.Transition{From: protocol.StateDone, To: protocol.StateWorking}) || !changed {
			t.Fatalf("transition = %+v changed = %v, want done to working and true", transition, changed)
		}
		wantState(t, client, pane, protocol.StateWorking)
		if got, _ := option(t, client, pane, protocol.OptionChanged); got != "1700000000" {
			t.Fatalf("%s = %q, want 1700000000", protocol.OptionChanged, got)
		}
	})

	t.Run("malformed @orrery_changed is an error", func(t *testing.T) {
		client, pane := startPane(t)
		setPaneState(t, client, pane, protocol.StateDone)
		if err := client.SetPaneOption(t.Context(), pane, protocol.OptionChanged, "synthetic"); err != nil {
			t.Fatalf("SetPaneOption(%s): %v", protocol.OptionChanged, err)
		}

		_, _, err := state.Apply(t.Context(), client, pane, json.RawMessage(preToolUseHook), t0)
		if err == nil {
			t.Fatal("Apply with malformed @orrery_changed: want error, got nil")
		}
		wantState(t, client, pane, protocol.StateDone)
	})
}

// captureQuestion starts a pane, applies one PermissionRequest hook, and returns the stored question.
func captureQuestion(t *testing.T, hook string) string {
	t.Helper()
	client, pane := startPane(t)
	apply(t, client, pane, hook, time.Unix(1700000000, 0))
	wantState(t, client, pane, protocol.StateWaiting)
	got, ok := option(t, client, pane, protocol.OptionQuestion)
	if !ok {
		t.Fatalf("%s unset after PermissionRequest", protocol.OptionQuestion)
	}
	return got
}

// bashPermission builds a PermissionRequest for Bash whose command is command.
func bashPermission(command string) string {
	return `{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"` + command + `"}}`
}

// Stored form of bashPermission(command), before any truncation.
func bashQuestion(command string) string {
	return `{"tool":"Bash","input":{"command":"` + command + `"}}`
}

// writePermission builds a PermissionRequest for Write whose content is content.
func writePermission(content string) string {
	return `{"hook_event_name":"PermissionRequest","tool_name":"Write","tool_input":{"file_path":"synthetic.txt","content":"` +
		content + `"}}`
}

// truncatedMarker is the stored form of a question whose full encoding is full and exceeds the bound.
func truncatedMarker(tool, full string) string {
	sum := sha256.Sum256([]byte(full))
	return `{"tool":"` + tool + `","truncated":true,"sha256":"` + hex.EncodeToString(sum[:]) + `"}`
}

func TestQuestionCapture(t *testing.T) {
	const askHook = `{"hook_event_name":"PermissionRequest","tool_name":"AskUserQuestion","tool_input":{
  "questions": [
    {"question": "Pick one?", "header": "Choice", "multiSelect": false,
     "options": [{"label": "A", "description": "first"}, {"label": "B", "description": "second"}]}
  ]
}}`
	const askWant = `{"tool":"AskUserQuestion","questions":[` +
		`{"question":"Pick one?","header":"Choice","multiSelect":false,` +
		`"options":[{"label":"A","description":"first"},{"label":"B","description":"second"}]}]}`

	t.Run("AskUserQuestion stores its questions compactly", func(t *testing.T) {
		if got := captureQuestion(t, askHook); got != askWant {
			t.Fatalf("question = %q, want %q", got, askWant)
		}
	})

	t.Run("other tools store their input", func(t *testing.T) {
		const want = `{"tool":"Bash","input":{"command":"echo synthetic"}}`
		if got := captureQuestion(t, permissionHook); got != want {
			t.Fatalf("question = %q, want %q", got, want)
		}
	})

	t.Run("stored value at the 8192-byte bound is kept whole", func(t *testing.T) {
		command := strings.Repeat("a", 8192-len(bashQuestion("")))
		want := bashQuestion(command)
		if len(want) != 8192 {
			t.Fatalf("test fixture is %d bytes, want 8192", len(want))
		}
		if got := captureQuestion(t, bashPermission(command)); got != want {
			t.Fatalf("question at bound = %d bytes, want the whole value", len(got))
		}
	})

	t.Run("stored value over the 8192-byte bound is truncated", func(t *testing.T) {
		command := strings.Repeat("a", 8192-len(bashQuestion(""))+1)
		want := truncatedMarker("Bash", bashQuestion(command))
		if got := captureQuestion(t, bashPermission(command)); got != want {
			t.Fatalf("question over bound = %q, want %q", got, want)
		}
	})

	t.Run("oversized AskUserQuestion input is truncated", func(t *testing.T) {
		huge := strings.Repeat("x", 9000)
		hook := `{"hook_event_name":"PermissionRequest","tool_name":"AskUserQuestion",` +
			`"tool_input":{"questions":"` + huge + `"}}`
		full := `{"tool":"AskUserQuestion","questions":"` + huge + `"}`
		if got, want := captureQuestion(t, hook), truncatedMarker("AskUserQuestion", full); got != want {
			t.Fatalf("question = %q, want %q", got, want)
		}
	})

	t.Run("distinct oversized prompts store distinct values", func(t *testing.T) {
		client, pane := startPane(t)
		first := strings.Repeat("a", 9000)
		second := strings.Repeat("b", 9000)
		firstFull := `{"tool":"Write","input":{"file_path":"synthetic.txt","content":"` + first + `"}}`
		secondFull := `{"tool":"Write","input":{"file_path":"synthetic.txt","content":"` + second + `"}}`

		apply(t, client, pane, writePermission(first), time.Unix(1700000000, 0))
		firstValue, _ := option(t, client, pane, protocol.OptionQuestion)
		if want := truncatedMarker("Write", firstFull); firstValue != want {
			t.Fatalf("first question = %q, want %q", firstValue, want)
		}

		apply(t, client, pane, writePermission(second), time.Unix(1700000001, 0))
		secondValue, _ := option(t, client, pane, protocol.OptionQuestion)
		if want := truncatedMarker("Write", secondFull); secondValue != want {
			t.Fatalf("second question = %q, want %q", secondValue, want)
		}
		if firstValue == secondValue {
			t.Fatalf("distinct oversized prompts both stored %q", firstValue)
		}

		apply(t, client, pane, writePermission(first), time.Unix(1700000002, 0))
		repeated, _ := option(t, client, pane, protocol.OptionQuestion)
		if repeated != firstValue {
			t.Fatalf("repeated oversized prompt stored %q, want %q", repeated, firstValue)
		}
	})

	t.Run("notification-driven waiting leaves the question unchanged", func(t *testing.T) {
		client, pane := startPane(t)
		apply(t, client, pane, permissionHook, time.Unix(1700000000, 0))
		apply(t, client, pane, permissionPromptHook, time.Unix(1700000001, 0))
		wantState(t, client, pane, protocol.StateWaiting)
		wantQuestion(t, client, pane, `{"tool":"Bash","input":{"command":"echo synthetic"}}`)
	})

	t.Run("notification-driven waiting from working sets no question", func(t *testing.T) {
		client, pane := startPane(t)
		apply(t, client, pane, preToolUseHook, time.Unix(1700000000, 0))
		apply(t, client, pane, elicitationHook, time.Unix(1700000001, 0))
		wantState(t, client, pane, protocol.StateWaiting)
		wantQuestion(t, client, pane, "")
	})

	t.Run("a newer PermissionRequest replaces the question while waiting", func(t *testing.T) {
		client, pane := startPane(t)
		apply(t, client, pane, permissionHook, time.Unix(1700000000, 0))
		replacement := `{"hook_event_name":"PermissionRequest","tool_name":"AskUserQuestion",` +
			`"tool_input":{"questions":[]}}`
		apply(t, client, pane, replacement, time.Unix(1700000001, 0))
		wantQuestion(t, client, pane, `{"tool":"AskUserQuestion","questions":[]}`)
	})

	t.Run("cleared on next working event", func(t *testing.T) {
		for _, working := range []string{userPromptSubmitHook, preToolUseHook, postToolUseHook, postToolFailureHook} {
			client, pane := startPane(t)
			apply(t, client, pane, permissionHook, time.Unix(1700000000, 0))
			wantQuestion(t, client, pane, `{"tool":"Bash","input":{"command":"echo synthetic"}}`)
			apply(t, client, pane, working, time.Unix(1700000001, 0))
			wantState(t, client, pane, protocol.StateWorking)
			wantQuestion(t, client, pane, "")
		}
	})

	t.Run("cleared on done", func(t *testing.T) {
		client, pane := startPane(t)
		apply(t, client, pane, permissionHook, time.Unix(1700000000, 0))
		apply(t, client, pane, stopHook, time.Unix(1700000001, 0))
		wantState(t, client, pane, protocol.StateDone)
		wantQuestion(t, client, pane, "")
	})
}

// recordSession runs one hook through state.RecordSession and fails the test on error.
func recordSession(t *testing.T, client *tmuxctl.Client, pane, hook string) {
	t.Helper()
	if err := state.RecordSession(t.Context(), client, pane, json.RawMessage(hook)); err != nil {
		t.Fatalf("RecordSession(%s): %v", hook, err)
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

// wantSession checks @orrery_claude_session and @orrery_transcript of pane.
func wantSession(t *testing.T, client *tmuxctl.Client, pane, session, transcript string) {
	t.Helper()
	wantOption(t, client, pane, protocol.OptionClaudeSession, session)
	wantOption(t, client, pane, protocol.OptionTranscript, transcript)
}

func TestRecordSession(t *testing.T) {
	const (
		sessionHook = `{"hook_event_name":"PreToolUse","tool_name":"Bash",` +
			`"session_id":"synthetic-session","transcript_path":"/synthetic/transcript.jsonl"}`
		emptySessionHook = `{"hook_event_name":"PreToolUse","session_id":"","transcript_path":""}`
		plainHook        = `{"hook_event_name":"PreToolUse","tool_name":"Bash"}`
		sessionEndOther  = `{"hook_event_name":"SessionEnd","reason":"other",` +
			`"session_id":"synthetic-session","transcript_path":"/synthetic/transcript.jsonl"}`
		sessionEndClear = `{"hook_event_name":"SessionEnd","reason":"clear"}`
		newlineHook     = `{"hook_event_name":"PreToolUse","session_id":"synthetic\nsession",` +
			`"transcript_path":"/synthetic/\ntranscript.jsonl"}`
	)
	t0 := time.Unix(1700000000, 0)

	t.Run("sets session and transcript from the hook", func(t *testing.T) {
		client, pane := startPane(t)
		recordSession(t, client, pane, sessionHook)
		wantSession(t, client, pane, "synthetic-session", "/synthetic/transcript.jsonl")
	})

	t.Run("a hook without them or with empty values keeps them", func(t *testing.T) {
		client, pane := startPane(t)
		recordSession(t, client, pane, sessionHook)
		recordSession(t, client, pane, plainHook)
		wantSession(t, client, pane, "synthetic-session", "/synthetic/transcript.jsonl")
		recordSession(t, client, pane, emptySessionHook)
		wantSession(t, client, pane, "synthetic-session", "/synthetic/transcript.jsonl")
	})

	t.Run("SessionEnd reason other keeps them", func(t *testing.T) {
		client, pane := startPane(t)
		recordSession(t, client, pane, sessionHook)
		apply(t, client, pane, sessionEndOther, t0)
		wantSession(t, client, pane, "synthetic-session", "/synthetic/transcript.jsonl")
		recordSession(t, client, pane, sessionEndOther)
		wantSession(t, client, pane, "synthetic-session", "/synthetic/transcript.jsonl")
	})

	t.Run("SessionEnd reason clear unsets them", func(t *testing.T) {
		client, pane := startPane(t)
		recordSession(t, client, pane, sessionHook)
		wantSession(t, client, pane, "synthetic-session", "/synthetic/transcript.jsonl")
		recordSession(t, client, pane, sessionEndClear)
		wantSession(t, client, pane, "", "")
	})

	t.Run("a newline becomes a space", func(t *testing.T) {
		client, pane := startPane(t)
		recordSession(t, client, pane, newlineHook)
		wantSession(t, client, pane, "synthetic session", "/synthetic/ transcript.jsonl")
	})

	t.Run("bad hook is an error and writes nothing", func(t *testing.T) {
		cases := []struct {
			name string
			hook string
		}{
			{"malformed JSON", `{"hook_event_name":`},
			{"non-string session_id", `{"hook_event_name":"PreToolUse","session_id":5,` +
				`"transcript_path":"/synthetic/new.jsonl"}`},
			{"non-string transcript_path", `{"hook_event_name":"PreToolUse","session_id":"synthetic-new",` +
				`"transcript_path":7}`},
			{"non-string hook_event_name", `{"hook_event_name":5,"session_id":"synthetic-new"}`},
			{"non-string reason", `{"hook_event_name":"SessionEnd","reason":["clear"]}`},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				client, pane := startPane(t)
				recordSession(t, client, pane, sessionHook)
				err := state.RecordSession(t.Context(), client, pane, json.RawMessage(tc.hook))
				if err == nil {
					t.Fatalf("RecordSession(%s) error = nil, want error", tc.hook)
				}
				wantSession(t, client, pane, "synthetic-session", "/synthetic/transcript.jsonl")
			})
		}
	})
}

// toolRecord is the decoded @orrery_tool value the tool tests check.
type toolRecord struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Input string `json:"input"`
	Start int64  `json:"start"`
}

// toolHook returns a synthetic hook for event with a Bash command. An empty id omits tool_use_id.
func toolHook(t *testing.T, event, id, command string) string {
	t.Helper()
	hook := map[string]any{
		"hook_event_name": event,
		"tool_name":       "Bash",
		"tool_input":      map[string]string{"command": command},
	}
	if id != "" {
		hook["tool_use_id"] = id
	}
	data, err := json.Marshal(hook)
	if err != nil {
		t.Fatalf("marshal %s hook: %v", event, err)
	}
	return string(data)
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

func TestToolState(t *testing.T) {
	t0 := time.Unix(1700000000, 0)

	t.Run("PreToolUse records id, name, input and start", func(t *testing.T) {
		client, pane := startPane(t)
		apply(t, client, pane, toolHook(t, "PreToolUse", "synthetic-1", "echo synthetic"), t0)
		record, ok := readTool(t, client, pane)
		want := toolRecord{ID: "synthetic-1", Name: "Bash", Input: "echo synthetic", Start: t0.Unix()}
		if !ok || record != want {
			t.Fatalf("%s = %+v (set %v), want %+v", protocol.OptionTool, record, ok, want)
		}
	})

	t.Run("multi-line command is stored as one clipped line", func(t *testing.T) {
		client, pane := startPane(t)
		command := "first line\nsecond line " + strings.Repeat("x", 250)
		apply(t, client, pane, toolHook(t, "PreToolUse", "synthetic-1", command), t0)
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

	t.Run("PostToolUse and PostToolUseFailure with the same id clear it", func(t *testing.T) {
		for _, event := range []string{"PostToolUse", "PostToolUseFailure"} {
			t.Run(event, func(t *testing.T) {
				client, pane := startPane(t)
				apply(t, client, pane, toolHook(t, "PreToolUse", "synthetic-1", "echo synthetic"), t0)
				wantToolID(t, client, pane, "synthetic-1")
				apply(t, client, pane, toolHook(t, event, "synthetic-1", "echo synthetic"), t0.Add(time.Second))
				wantToolID(t, client, pane, "")
			})
		}
	})

	t.Run("PostToolUse and PostToolUseFailure with another or no id leave it", func(t *testing.T) {
		for _, event := range []string{"PostToolUse", "PostToolUseFailure"} {
			for _, id := range []string{"synthetic-2", ""} {
				t.Run(fmt.Sprintf("%s id %q", event, id), func(t *testing.T) {
					client, pane := startPane(t)
					apply(t, client, pane, toolHook(t, "PreToolUse", "synthetic-1", "echo synthetic"), t0)
					wantToolID(t, client, pane, "synthetic-1")
					apply(t, client, pane, toolHook(t, event, id, "echo synthetic"), t0.Add(time.Second))
					wantToolID(t, client, pane, "synthetic-1")
				})
			}
		}
	})

	t.Run("parallel batch keeps the later tool until its own end", func(t *testing.T) {
		client, pane := startPane(t)
		apply(t, client, pane, toolHook(t, "PreToolUse", "synthetic-a", "echo a"), t0)
		apply(t, client, pane, toolHook(t, "PreToolUse", "synthetic-b", "echo b"), t0.Add(time.Second))
		wantToolID(t, client, pane, "synthetic-b")

		apply(t, client, pane, toolHook(t, "PostToolUse", "synthetic-a", "echo a"), t0.Add(2*time.Second))
		wantToolID(t, client, pane, "synthetic-b")

		apply(t, client, pane, toolHook(t, "PostToolUse", "synthetic-b", "echo b"), t0.Add(3*time.Second))
		wantToolID(t, client, pane, "")
	})

	t.Run("Stop clears it", func(t *testing.T) {
		client, pane := startPane(t)
		apply(t, client, pane, toolHook(t, "PreToolUse", "synthetic-1", "echo synthetic"), t0)
		wantToolID(t, client, pane, "synthetic-1")
		apply(t, client, pane, stopHook, t0.Add(time.Second))
		wantToolID(t, client, pane, "")
	})

	t.Run("SessionEnd clears it", func(t *testing.T) {
		client, pane := startPane(t)
		apply(t, client, pane, toolHook(t, "PreToolUse", "synthetic-1", "echo synthetic"), t0)
		wantToolID(t, client, pane, "synthetic-1")
		apply(t, client, pane, sessionEndHook, t0.Add(time.Second))
		wantToolID(t, client, pane, "")
	})

	t.Run("late tool events inside the grace window write nothing", func(t *testing.T) {
		client, pane := startPane(t)
		apply(t, client, pane, userPromptSubmitHook, t0)
		apply(t, client, pane, stopHook, t0.Add(4*time.Second))
		wantState(t, client, pane, protocol.StateDone)
		// A tool that started before the Stop, stored by hand so a late event that wrote would show.
		stored := `{"id":"synthetic-1","name":"Bash","input":"echo synthetic","start":1700000000}`
		if err := client.SetPaneOption(t.Context(), pane, protocol.OptionTool, stored); err != nil {
			t.Fatalf("SetPaneOption(%s): %v", protocol.OptionTool, err)
		}
		apply(t, client, pane, toolHook(t, "PreToolUse", "synthetic-2", "echo late"), t0.Add(5*time.Second))
		apply(t, client, pane, toolHook(t, "PostToolUse", "synthetic-1", "echo synthetic"), t0.Add(6*time.Second))
		if got, _ := option(t, client, pane, protocol.OptionTool); got != stored {
			t.Fatalf("%s after late events = %q, want unchanged %q", protocol.OptionTool, got, stored)
		}
	})

	t.Run("PreToolUse without tool_use_id leaves the option unchanged", func(t *testing.T) {
		client, pane := startPane(t)
		apply(t, client, pane, toolHook(t, "PreToolUse", "", "echo synthetic"), t0)
		wantToolID(t, client, pane, "")

		apply(t, client, pane, toolHook(t, "PreToolUse", "synthetic-1", "echo synthetic"), t0.Add(time.Second))
		wantToolID(t, client, pane, "synthetic-1")
		empty := `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"",` +
			`"tool_input":{"command":"echo empty"}}`
		apply(t, client, pane, empty, t0.Add(2*time.Second))
		wantToolID(t, client, pane, "synthetic-1")
	})

	t.Run("a stored value that does not decode never matches", func(t *testing.T) {
		client, pane := startPane(t)
		if err := client.SetPaneOption(t.Context(), pane, protocol.OptionTool, "not json"); err != nil {
			t.Fatalf("SetPaneOption(%s): %v", protocol.OptionTool, err)
		}
		apply(t, client, pane, toolHook(t, "PostToolUse", "synthetic-1", "echo synthetic"), t0)
		if got, _ := option(t, client, pane, protocol.OptionTool); got != "not json" {
			t.Fatalf("%s = %q, want unchanged", protocol.OptionTool, got)
		}
	})

	t.Run("a non-string tool_use_id is an error and writes nothing", func(t *testing.T) {
		client, pane := startPane(t)
		hook := `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":7}`
		if _, _, err := state.Apply(t.Context(), client, pane, json.RawMessage(hook), t0); err == nil {
			t.Fatalf("Apply(%s) error = nil, want error", hook)
		}
		wantToolID(t, client, pane, "")
		wantState(t, client, pane, "")
	})
}
