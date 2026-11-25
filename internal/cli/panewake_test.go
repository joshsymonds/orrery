package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/cli"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// paneWakeQuestionDialog returns a screen showing a question dialog 80 columns wide, with no input box.
func paneWakeQuestionDialog() string {
	rule := strings.Repeat("─", 80)
	return rule + "\n ☐ Header\n\nInvented question?\n\n❯ 1. Option\n   2. Other\n" +
		rule + "\n  4. Chat about this\n\nEnter to select · ↑/↓ to navigate · Esc to cancel"
}

// paneWakePermissionDialog returns a screen showing a permission dialog 80 columns wide, with no input box.
func paneWakePermissionDialog() string {
	rule := strings.Repeat("─", 80)
	return "❯ earlier prompt\n" + rule + "\n Create file\n" +
		"\n Do you want to create synthetic.txt?\n ❯ 1. Yes\n   2. No\n Esc to cancel · Tab to amend"
}

// paneWakeTrustDialog returns a screen showing a folder trust dialog, with no input box.
func paneWakeTrustDialog() string {
	rule := strings.Repeat("─", 80)
	return rule + "\nTrust this folder?\n❯ 1. Yes\n  2. No\nEnter to confirm"
}

// paneWakeAssertNothingSent sends "ok" and Enter through Send and requires the pane to receive exactly
// that. SendIdle returns only after it has sent its keys, so anything it sent precedes the sentinel.
func paneWakeAssertNothingSent(t *testing.T, client *tmuxctl.Client, pane, file string) {
	t.Helper()
	var out bytes.Buffer
	if err := cli.Send(t.Context(), client, []string{"--pane", pane, "--keys", "o", "k", "Enter"}, &out); err != nil {
		t.Fatalf("sentinel Send: %v", err)
	}
	sendAwaitInput(t, client, pane, file, "ok\r")
}

// TestPaneWakeSendsNothingUnlessDoneWithInputBox requires SendIdle to report false and send no key while the
// pane is working or waiting, is done without an input box, shows a question, permission, or folder trust
// dialog in any state, has no state, or is in copy mode.
func TestPaneWakeSendsNothingUnlessDoneWithInputBox(t *testing.T) {
	t.Parallel()
	question, permission, trust := paneWakeQuestionDialog(), paneWakePermissionDialog(), paneWakeTrustDialog()
	working, waiting, done := string(protocol.StateWorking), string(protocol.StateWaiting), string(protocol.StateDone)
	cases := []struct {
		name   string
		screen string
		state  string // value of @orrery_state; empty means it is never set
		copy   bool   // enter copy mode before sending
	}{
		{name: "working with input box", screen: sendBoxScreen(), state: working},
		{name: "waiting with input box", screen: sendBoxScreen(), state: waiting},
		{name: "no state with input box", screen: sendBoxScreen()},
		{name: "done without input box", screen: sendQuestion, state: done},
		{name: "working with question dialog", screen: question, state: working},
		{name: "waiting with question dialog", screen: question, state: waiting},
		{name: "done with question dialog", screen: question, state: done},
		{name: "working with permission dialog", screen: permission, state: working},
		{name: "done with permission dialog", screen: permission, state: done},
		{name: "working with folder trust dialog", screen: trust, state: working},
		{name: "done with folder trust dialog", screen: trust, state: done},
		{name: "done in copy mode", screen: sendBoxScreen(), state: done, copy: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := sendServer(t)
			pane, file := sendStartPane(t, client, tc.screen)
			if tc.state != "" {
				sendSetState(t, client, pane, tc.state)
			}
			if tc.copy {
				sendRun(t, client, "copy-mode", "-t", pane)
			}

			sent, err := cli.SendIdle(t.Context(), client, pane, "synthetic text")
			if err != nil || sent {
				t.Fatalf("SendIdle = %v, %v; want false, nil", sent, err)
			}
			if tc.copy {
				// Copy mode would consume the sentinel's keys, so leave it first.
				sendRun(t, client, "copy-mode", "-q", "-t", pane)
			}
			paneWakeAssertNothingSent(t, client, pane, file)
		})
	}
}

// TestPaneWakeTypesIntoDonePane requires SendIdle to type the text, with line breaks as C-j and no paste
// markers, then press Enter once, when the pane is done with its input box shown.
func TestPaneWakeTypesIntoDonePane(t *testing.T) {
	t.Parallel()
	client := sendServer(t)
	pane, file := sendStartPane(t, client, sendBoxScreen())
	sendSetState(t, client, pane, string(protocol.StateDone))

	sent, err := cli.SendIdleWithClock(t.Context(), &instantClock{}, client, pane, "first line\nsecond line")
	if err != nil || !sent {
		t.Fatalf("SendIdle = %v, %v; want true, nil", sent, err)
	}
	sendAwaitInput(t, client, pane, file, "first line\nsecond line\r")
}

// TestPaneWakeTypesIntoHeldPane requires SendIdle to type into a working pane whose @orrery_held is set, since its
// turn ended while a background agent runs and no turn runs, as it types into a done pane.
func TestPaneWakeTypesIntoHeldPane(t *testing.T) {
	t.Parallel()
	client := sendServer(t)
	pane, file := sendStartPane(t, client, sendBoxScreen())
	sendSetState(t, client, pane, string(protocol.StateWorking))
	sendRun(t, client, "set-option", "-p", "-t", pane, protocol.OptionHeld, "1700000000")

	sent, err := cli.SendIdleWithClock(t.Context(), &instantClock{}, client, pane, "held text")
	if err != nil || !sent {
		t.Fatalf("SendIdle = %v, %v; want true, nil", sent, err)
	}
	sendAwaitInput(t, client, pane, file, "held text\r")
}

// TestPaneWakeRefusesUntypableText requires SendIdle to return an error and send no key for text that cannot
// be typed exactly, even when the pane is done with its input box shown. Each text is invented to fail one
// typing rule: a control character other than the line feed, invalid UTF-8, and more than 200 typed
// invocations before Enter.
func TestPaneWakeRefusesUntypableText(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		text string
	}{
		{name: "tab", text: "a\tb"},
		{name: "invalid UTF-8", text: "a\xffb"},
		{name: "201 invocations", text: strings.Repeat("a\n", 100) + "a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := sendServer(t)
			pane, file := sendStartPane(t, client, sendBoxScreen())
			sendSetState(t, client, pane, string(protocol.StateDone))

			sent, err := cli.SendIdle(t.Context(), client, pane, tc.text)
			if err == nil || sent {
				t.Fatalf("SendIdle = %v, %v; want false with an error", sent, err)
			}
			paneWakeAssertNothingSent(t, client, pane, file)
		})
	}
}
