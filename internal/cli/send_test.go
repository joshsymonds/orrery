package cli_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/cli"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

const (
	sendNoConfig = "/dev/null"
	// sendQuestion is printed by the pane program; it is synthetic.
	sendQuestion = "Continue with synthetic fixture?"
	sendPoll     = 20 * time.Millisecond
	sendDeadline = 5 * time.Second
)

// sendServer returns a client for a unique private server name and kills that server at cleanup.
func sendServer(t *testing.T) *tmuxctl.Client {
	t.Helper()
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("random server name: %v", err)
	}
	name := fmt.Sprintf("orrery-test-send-%d-%x", os.Getpid(), suffix)
	client, err := tmuxctl.New(tmuxctl.Server{Name: name})
	if err != nil {
		t.Fatalf("New(Name=%q): %v", name, err)
	}
	t.Cleanup(func() {
		if _, err := client.Run(context.Background(), "kill-server"); err != nil {
			t.Errorf("kill-server: %v", err)
		}
	})
	return client
}

func sendRun(t *testing.T, client *tmuxctl.Client, args ...string) string {
	t.Helper()
	out, err := client.Run(t.Context(), args...)
	if err != nil {
		t.Fatalf("Run(%v): %v", args, err)
	}
	return out
}

// sendPollUntil calls ready every sendPoll until it reports true, failing the test at sendDeadline.
func sendPollUntil(t *testing.T, what string, ready func() (bool, error)) {
	t.Helper()
	ticker := time.NewTicker(sendPoll)
	defer ticker.Stop()
	deadline := time.NewTimer(sendDeadline)
	defer deadline.Stop()
	for {
		done, err := ready()
		if err != nil {
			t.Fatalf("waiting for %s: %v", what, err)
		}
		if done {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// sendStartPane starts session s1 whose pane prints screen, enables bracketed paste, turns off the
// line discipline, prints "ready", and then runs cat into a file. It returns the pane ID and the file
// path; the pane is ready once "ready" is on screen. The screen is written to a file and printed with
// cat, so its text is never interpolated into the shell.
func sendStartPane(t *testing.T, client *tmuxctl.Client, screen string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "input")
	screenFile := filepath.Join(dir, "screen")
	if err := os.WriteFile(screenFile, []byte(screen+"\n"), 0o600); err != nil {
		t.Fatalf("write screen: %v", err)
	}
	script := fmt.Sprintf(
		`cat %s; printf "\033[?2004h"; stty -icanon -echo -icrnl; printf "ready\n"; exec cat > %s`,
		screenFile, file,
	)
	sendRun(t, client, "-f", sendNoConfig, "new-session", "-d", "-s", "s1", "-c", dir,
		"exec sh -c '"+script+"'")
	pane := strings.TrimSpace(sendRun(t, client, "list-panes", "-t", "s1", "-F", "#{pane_id}"))
	sendWaitScreen(t, client, pane, "ready")
	return pane, file
}

// sendWaitScreen waits until want appears in the pane's captured screen.
func sendWaitScreen(t *testing.T, client *tmuxctl.Client, pane, want string) {
	t.Helper()
	sendPollUntil(t, fmt.Sprintf("%q on %s", want, pane), func() (bool, error) {
		screen, err := client.Run(t.Context(), "capture-pane", "-p", "-J", "-t", pane)
		if err != nil {
			return false, err
		}
		return strings.Contains(screen, want), nil
	})
}

// sendAwaitInput waits until the file holds at least len(want) bytes, then requires it to equal want exactly.
func sendAwaitInput(t *testing.T, file, want string) {
	t.Helper()
	var got []byte
	sendPollUntil(t, fmt.Sprintf("%d input bytes in %s", len(want), file), func() (bool, error) {
		data, err := os.ReadFile(file)
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		got = data
		return len(data) >= len(want), nil
	})
	if string(got) != want {
		t.Fatalf("pane received %q, want %q", got, want)
	}
}

func sendSetQuestion(t *testing.T, client *tmuxctl.Client, pane, question string) {
	t.Helper()
	_, err := client.Run(t.Context(), "set-option", "-p", "-t", pane, protocol.OptionQuestion, question)
	if err != nil {
		t.Fatalf("set %s: %v", protocol.OptionQuestion, err)
	}
}

func TestSendTextPastesThenEnter(t *testing.T) {
	client := sendServer(t)
	pane, file := sendStartPane(t, client, sendQuestion)

	var out bytes.Buffer
	if err := cli.Send(t.Context(), client, []string{"--pane", pane, "--text", "synthetic text"}, &out); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("Send printed %q, want nothing", out.String())
	}
	sendAwaitInput(t, file, "\x1b[200~synthetic text\x1b[201~\r")
}

// sendBoxScreen returns a screen whose empty input box is 80 columns wide, below synthetic output.
func sendBoxScreen() string {
	rule := strings.Repeat("─", 80)
	return "synthetic output\n" + rule + "\n❯ \n" + rule + "\n  synthetic footer"
}

// sendSetState sets pane's @orrery_state option to state.
func sendSetState(t *testing.T, client *tmuxctl.Client, pane, state string) {
	t.Helper()
	sendRun(t, client, "set-option", "-p", "-t", pane, protocol.OptionState, state)
}

// sendTextAwait sends text with --text and requires the pane to receive want exactly, with no output.
func sendTextAwait(t *testing.T, client *tmuxctl.Client, pane, file, text, want string) {
	t.Helper()
	var out bytes.Buffer
	if err := cli.Send(t.Context(), client, []string{"--pane", pane, "--text", text}, &out); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("Send printed %q, want nothing", out.String())
	}
	sendAwaitInput(t, file, want)
}

// TestSendTextTypesIntoInputBox drives a pane in the working and done states whose screen shows the
// input box. The text is typed: line breaks arrive as C-j, and no paste markers appear.
func TestSendTextTypesIntoInputBox(t *testing.T) {
	for _, state := range []string{string(protocol.StateWorking), string(protocol.StateDone)} {
		t.Run(state, func(t *testing.T) {
			client := sendServer(t)
			pane, file := sendStartPane(t, client, sendBoxScreen())
			width := strings.TrimSpace(sendRun(t, client, "display-message", "-p", "-t", pane, "#{pane_width}"))
			if width != "80" {
				t.Fatalf("pane_width = %q, want 80", width)
			}
			sendSetState(t, client, pane, state)

			sendTextAwait(t, client, pane, file, "first line\nsecond line", "first line\nsecond line\r")
		})
	}
}

// TestSendTextTypedBytes checks the exact bytes a typed send delivers. Each text is invented to hit one
// rule: blank lines, line endings, length, the semicolon and dash handling of tmux, and UTF-8 cuts.
func TestSendTextTypedBytes(t *testing.T) {
	emoji := "😀"
	cases := []struct {
		name string
		text string
		want string
	}{
		{name: "blank line", text: "a\n\nb", want: "a\n\nb\r"},
		{name: "trailing newline", text: "a\n", want: "a\n\r"},
		{name: "CRLF and CR become LF", text: "a\r\nb\rc", want: "a\nb\nc\r"},
		{
			name: "3000-byte line",
			text: strings.Repeat("abcdefghij", 300),
			want: strings.Repeat("abcdefghij", 300) + "\r",
		},
		{
			name: "semicolon as the 400th byte",
			text: strings.Repeat("a", 399) + ";tail",
			want: strings.Repeat("a", 399) + ";tail\r",
		},
		{name: "line ending in semicolon", text: "cmd;", want: "cmd;\r"},
		{name: "line ending in backslash semicolon", text: `x\;`, want: `x\;` + "\r"},
		{name: "piece starting with dash", text: "-flag value", want: "-flag value\r"},
		{
			name: "cut before a dash",
			text: strings.Repeat("a", 400) + "-flag",
			want: strings.Repeat("a", 400) + "-flag\r",
		},
		{
			name: "emoji straddling byte 400",
			text: strings.Repeat("a", 398) + emoji + "tail",
			want: strings.Repeat("a", 398) + emoji + "tail\r",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := sendServer(t)
			pane, file := sendStartPane(t, client, sendBoxScreen())
			sendSetState(t, client, pane, string(protocol.StateDone))

			sendTextAwait(t, client, pane, file, tc.text, tc.want)
		})
	}
}

// TestSendTextPastesOutsideInputBox requires the paste path, with no typed bytes, when the state, the
// screen, or the pane mode is not one the typed path accepts.
func TestSendTextPastesOutsideInputBox(t *testing.T) {
	rule := strings.Repeat("─", 80)
	dialog := rule + "\n ☐ Header\n\nInvented question?\n\n❯ 1. Option\n   2. Other\n" +
		rule + "\n  4. Chat about this\n\nEnter to select · ↑/↓ to navigate · Esc to cancel"
	const paste = "\x1b[200~synthetic text\x1b[201~\r"
	cases := []struct {
		name   string
		screen string
		state  string // value of @orrery_state; empty means it is never set
		copy   bool   // enter copy mode before sending
		want   string // bytes the pane receives
	}{
		{name: "no state", screen: sendBoxScreen(), want: paste},
		{name: "waiting state", screen: sendBoxScreen(), state: string(protocol.StateWaiting), want: paste},
		{name: "done without box", screen: sendQuestion, state: string(protocol.StateDone), want: paste},
		{name: "done with question dialog", screen: dialog, state: string(protocol.StateDone), want: paste},
		// In copy mode the paste arrives as plain text with no bracket markers, and the Enter key is
		// consumed by copy mode, so the pane receives only the text.
		{
			name:   "done in copy mode",
			screen: sendBoxScreen(), state: string(protocol.StateDone), copy: true,
			want: "synthetic text",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := sendServer(t)
			pane, file := sendStartPane(t, client, tc.screen)
			if tc.state != "" {
				sendSetState(t, client, pane, tc.state)
			}
			if tc.copy {
				sendRun(t, client, "copy-mode", "-t", pane)
			}

			sendTextAwait(t, client, pane, file, "synthetic text", tc.want)
		})
	}
}

// TestSendTextPastesWhatTypingCannotCarry requires the paste path, with no typed bytes, on a pane whose
// input box is shown and whose state is done. Each text is invented to fail one typing rule: a control
// character other than the line feed, invalid UTF-8, and more than 200 typed invocations before Enter.
// Each want is what tmux's paste-buffer -p delivers today: it shows ESC and a stray 0xff byte in caret
// form and turns each LF into CR.
func TestSendTextPastesWhatTypingCannotCarry(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		paste string // the text as the pane receives it between the paste markers
	}{
		{name: "tab", text: "a\tb", paste: "a\tb"},
		{name: "escape", text: "a\x1bb", paste: "a^[b"},
		{name: "C1 next line U+0085", text: "a\u0085b", paste: "a\u0085b"},
		{name: "invalid UTF-8", text: "a\xffb", paste: "aM^?b"},
		{
			name:  "201 invocations",
			text:  strings.Repeat("a\n", 100) + "a",
			paste: strings.Repeat("a\r", 100) + "a",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := sendServer(t)
			pane, file := sendStartPane(t, client, sendBoxScreen())
			sendSetState(t, client, pane, string(protocol.StateDone))

			sendTextAwait(t, client, pane, file, tc.text, "\x1b[200~"+tc.paste+"\x1b[201~\r")
		})
	}
}

// TestSendTextTypesAtTypeLimit requires text of exactly 200 typed invocations to be typed: 99 line
// breaks as C-j and 101 literal pieces, with no paste markers.
func TestSendTextTypesAtTypeLimit(t *testing.T) {
	text := strings.Repeat("b\n", 99) + strings.Repeat("c", 401)
	client := sendServer(t)
	pane, file := sendStartPane(t, client, sendBoxScreen())
	sendSetState(t, client, pane, string(protocol.StateDone))

	sendTextAwait(t, client, pane, file, text, text+"\r")
}

// TestSendTextFinishesWhenCallerCancels starts a typed send of 20 short lines under a cancellable context
// and cancels it once the first bytes reach the pane's input file. The typed send runs on its own bounded
// context, so Send returns nil and the pane receives every typed byte, then Enter.
func TestSendTextFinishesWhenCallerCancels(t *testing.T) {
	lines := make([]string, 0, 20)
	for i := range 20 {
		lines = append(lines, fmt.Sprintf("synthetic line %02d", i))
	}
	text := strings.Join(lines, "\n")

	client := sendServer(t)
	pane, file := sendStartPane(t, client, sendBoxScreen())
	sendSetState(t, client, pane, string(protocol.StateDone))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- cli.Send(ctx, client, []string{"--pane", pane, "--text", text}, &bytes.Buffer{})
	}()

	sendPollUntil(t, "first typed bytes in "+file, func() (bool, error) {
		data, err := os.ReadFile(file)
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return len(data) > 0, nil
	})
	cancel()

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Send after caller canceled: %v", err)
		}
	case <-time.After(sendDeadline):
		t.Fatalf("Send did not return within %v", sendDeadline)
	}
	sendAwaitInput(t, file, text+"\r")
}

func TestSendKeysDeliversKeys(t *testing.T) {
	client := sendServer(t)
	pane, file := sendStartPane(t, client, sendQuestion)

	var out bytes.Buffer
	args := []string{"--pane", pane, "--keys", "a", "b", "Enter", "c"}
	if err := cli.Send(t.Context(), client, args, &out); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("Send printed %q, want nothing", out.String())
	}
	sendAwaitInput(t, file, "ab\rc")
}

func TestSendKeysAcceptsDashKey(t *testing.T) {
	client := sendServer(t)
	pane, file := sendStartPane(t, client, sendQuestion)

	args := []string{"--pane", pane, "--keys", "--", "-"}
	if err := cli.Send(t.Context(), client, args, &bytes.Buffer{}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	sendAwaitInput(t, file, "-")
}

func TestSendExpectQuestionMatchingSends(t *testing.T) {
	client := sendServer(t)
	pane, file := sendStartPane(t, client, sendQuestion)
	sendSetQuestion(t, client, pane, sendQuestion)

	args := []string{"--pane", pane, "--expect-question", protocol.QuestionHash(sendQuestion), "--keys", "k", "Enter"}
	var out bytes.Buffer
	if err := cli.Send(t.Context(), client, args, &out); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("Send printed %q, want nothing", out.String())
	}
	sendAwaitInput(t, file, "k\r")
}

// TestSendExpectQuestionFragmentDelivers stores a question shaped like state's @orrery_question value
// and prints only its display fragment on the pane. Each screen is written out by hand from the rule,
// not computed by the code under test.
func TestSendExpectQuestionFragmentDelivers(t *testing.T) {
	long := strings.Repeat("abcdefghij", 5) // 50 runes; its fragment is the first 40
	cases := []struct {
		name   string
		stored string // value of @orrery_question
		screen string // the only text the pane shows
	}{
		{
			name:   "bash command",
			stored: `{"tool":"Bash","input":{"command":"synthetic-run --check"}}`,
			screen: "synthetic-run --check",
		},
		{
			name:   "write file path",
			stored: `{"tool":"Write","input":{"file_path":"/synthetic/out/notes.txt","content":"synthetic body"}}`,
			screen: "/synthetic/out/notes.txt",
		},
		{
			name:   "fetch url",
			stored: `{"tool":"WebFetch","input":{"url":"https://synthetic.example/page"}}`,
			screen: "https://synthetic.example/page",
		},
		{
			name:   "first string field only",
			stored: `{"tool":"Mystery","input":{"count":3,"label":"synthetic label","note":"later"}}`,
			screen: "synthetic label",
		},
		{
			name:   "AskUserQuestion",
			stored: `{"tool":"AskUserQuestion","questions":[{"question":"Pick a synthetic option?","header":"Pick","options":[{"label":"One","description":"synthetic"}],"multiSelect":false}]}`,
			screen: "Pick a synthetic option?",
		},
		{
			name:   "truncated marker",
			stored: `{"tool":"Bash","truncated":true}`,
			screen: "Bash",
		},
		{
			name:   "multi-line command",
			stored: `{"tool":"Bash","input":{"command":"synthetic-first\nsynthetic-second"}}`,
			screen: "synthetic-first",
		},
		{
			name:   "fragment over 40 runes",
			stored: `{"tool":"Bash","input":{"command":"` + long + `"}}`,
			screen: long[:40],
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := sendServer(t)
			pane, file := sendStartPane(t, client, tc.screen)
			sendSetQuestion(t, client, pane, tc.stored)

			args := []string{
				"--pane", pane, "--expect-question", protocol.QuestionHash(tc.stored),
				"--keys", "k", "Enter",
			}
			var out bytes.Buffer
			if err := cli.Send(t.Context(), client, args, &out); err != nil {
				t.Fatalf("Send: %v", err)
			}
			if out.Len() != 0 {
				t.Fatalf("Send printed %q, want nothing", out.String())
			}
			sendAwaitInput(t, file, "k\r")
		})
	}
}

func TestSendExpectQuestionRefusesBeforeSending(t *testing.T) {
	cases := []struct {
		name     string
		question string // value of @orrery_question; empty means it is never set
		setIt    bool
		expect   string
	}{
		{
			name:     "wrong hash",
			question: sendQuestion, setIt: true,
			expect: protocol.QuestionHash("some other question"),
		},
		{
			name:     "question not visible",
			question: "Hidden synthetic question?", setIt: true,
			expect: protocol.QuestionHash("Hidden synthetic question?"),
		},
		{
			name:     "json fragment not visible",
			question: `{"tool":"Bash","input":{"command":"synthetic-hidden-command"}}`, setIt: true,
			expect: protocol.QuestionHash(`{"tool":"Bash","input":{"command":"synthetic-hidden-command"}}`),
		},
		{
			name:     "empty fragment",
			question: `{"tool":"","truncated":true}`, setIt: true,
			expect: protocol.QuestionHash(`{"tool":"","truncated":true}`),
		},
		{
			name:   "unset question",
			expect: protocol.QuestionHash(sendQuestion),
		},
		{
			name:     "empty question",
			question: "", setIt: true,
			expect: protocol.QuestionHash(""),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := sendServer(t)
			pane, file := sendStartPane(t, client, sendQuestion)
			if tc.setIt {
				sendSetQuestion(t, client, pane, tc.question)
			}

			var out bytes.Buffer
			args := []string{"--pane", pane, "--expect-question", tc.expect, "--keys", "x", "Enter"}
			err := cli.Send(t.Context(), client, args, &out)
			if !errors.Is(err, cli.ErrQuestionRefused) {
				t.Fatalf("Send error = %v, want ErrQuestionRefused", err)
			}
			if out.Len() != 0 {
				t.Fatalf("Send printed %q, want nothing", out.String())
			}

			// Anything the refused call delivered would precede this sentinel in the input file.
			sentinel := []string{"--pane", pane, "--keys", "o", "k", "Enter"}
			if err := cli.Send(t.Context(), client, sentinel, &out); err != nil {
				t.Fatalf("sentinel Send: %v", err)
			}
			sendAwaitInput(t, file, "ok\r")
		})
	}
}

func TestSendRejectsBadArguments(t *testing.T) {
	client := sendServer(t)
	pane, _ := sendStartPane(t, client, sendQuestion)

	cases := []struct {
		name string
		args []string
	}{
		{name: "missing pane", args: []string{"--text", "x"}},
		{name: "empty pane", args: []string{"--pane", "", "--text", "x"}},
		{name: "neither text nor keys", args: []string{"--pane", pane}},
		{name: "text and keys", args: []string{"--pane", pane, "--text", "x", "--keys", "a"}},
		{name: "keys without keys", args: []string{"--pane", pane, "--keys"}},
		{name: "text with positional", args: []string{"--pane", pane, "--text", "x", "extra"}},
		{name: "flag without value", args: []string{"--pane", pane, "--text"}},
		{name: "unknown flag", args: []string{"--pane", pane, "--bogus", "x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			err := cli.Send(t.Context(), client, tc.args, &out)
			if err == nil {
				t.Fatalf("Send(%v): want error, got nil", tc.args)
			}
			if errors.Is(err, cli.ErrQuestionRefused) {
				t.Fatalf("Send(%v) returned ErrQuestionRefused for a bad argument", tc.args)
			}
			if out.Len() != 0 {
				t.Fatalf("Send(%v) printed %q alongside its error", tc.args, out.String())
			}
		})
	}
}
