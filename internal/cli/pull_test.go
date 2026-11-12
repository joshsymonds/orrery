package cli_test

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/cli"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// pullDimPlaceholder is the dim placeholder Claude Code shows in an empty input box while messages are queued.
const pullDimPlaceholder = "\x1b[2mPress up to edit queued messages\x1b[0m"

// pullUp, pullDown and pullBackspace are the bytes the fake pane's cat receives for the keys PullQueued sends.
const (
	pullUp        = "\x1b[A"
	pullDown      = "\x1b[B"
	pullBackspace = "\x7f"
	// pullQuiet is how long a negative assertion waits for a stray key to reach the pane.
	pullQuiet = 200 * time.Millisecond
)

// pullScreen returns a synthetic screen: output, then the optional above line, then an 80-column input box holding
// box after its prompt mark, then a footer. The box is synthetic; the rules and prompt mark match Claude Code's layout.
func pullScreen(above, box string) string {
	rule := strings.Repeat("─", 80)
	screen := "synthetic output\n"
	if above != "" {
		screen += above + "\n"
	}
	return screen + rule + "\n❯ " + box + "\n" + rule + "\n  synthetic footer"
}

// pullResult is what a PullQueued call returned.
type pullResult struct {
	text string
	err  error
}

// pullStart runs PullQueued in a goroutine, because the fake pane only records Up after the call sends it.
func pullStart(t *testing.T, client *tmuxctl.Client, pane, transcript string) <-chan pullResult {
	t.Helper()
	ch := make(chan pullResult, 1)
	go func() {
		text, err := cli.PullQueued(t.Context(), client, pane, transcript)
		ch <- pullResult{text: text, err: err}
	}()
	return ch
}

// pullWait returns the result of the PullQueued call that pullStart started, failing the test at sendDeadline.
func pullWait(t *testing.T, ch <-chan pullResult) (string, error) {
	t.Helper()
	select {
	case r := <-ch:
		return r.text, r.err
	case <-time.After(sendDeadline):
		t.Fatalf("PullQueued did not return")
		return "", nil
	}
}

// pullAwaitUp waits until the Up key has reached the pane, which records every byte it receives in file.
func pullAwaitUp(t *testing.T, file string) {
	t.Helper()
	sendPollUntil(t, "Up on the pane", func() (bool, error) {
		data, err := os.ReadFile(file)
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return strings.Contains(string(data), pullUp), nil
	})
}

// pullWritePopped appends one popAll queue-operation record per content to transcript, oldest first.
func pullWritePopped(t *testing.T, transcript string, contents ...string) {
	t.Helper()
	var b strings.Builder
	for _, content := range contents {
		raw, err := json.Marshal(content)
		if err != nil {
			t.Fatalf("marshal content: %v", err)
		}
		b.WriteString(`{"type":"queue-operation","operation":"popAll","content":` + string(raw) + "}\n")
	}
	f, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open transcript: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(b.String()); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
}

// pullTranscript returns an empty transcript file in a fresh directory.
func pullTranscript(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path
}

// TestPullQueuedTakesQueuedMessages covers an empty box with two queued messages: the Up key is sent, the two popAll
// records come back, and the pane receives one Backspace per rune of the joined text.
func TestPullQueuedTakesQueuedMessages(t *testing.T) {
	client := sendServer(t)
	pane, file := sendStartPane(t, client, pullScreen("", pullDimPlaceholder))
	transcript := pullTranscript(t)

	ch := pullStart(t, client, pane, transcript)
	pullAwaitUp(t, file)
	pullWritePopped(t, transcript, "first", "second")

	text, err := pullWait(t, ch)
	if err != nil {
		t.Fatalf("PullQueued: %v", err)
	}
	if text != "first\nsecond" {
		t.Fatalf("PullQueued = %q, want %q", text, "first\nsecond")
	}
	want := pullUp + strings.Repeat(pullBackspace, len("first\nsecond"))
	sendAwaitInput(t, file, want)
}

// TestPullQueuedBusyInputSendsNothing covers each state that refuses the pull: typed text in the box, a pane in a
// tmux mode, and a pane with no input box on screen. Each returns ErrInputBusy and no key reaches the pane. A typed X
// then has to arrive as the first byte the pane records, which proves no Up preceded it.
func TestPullQueuedBusyInputSendsNothing(t *testing.T) {
	cases := []struct {
		name   string
		screen string
		mode   bool
	}{
		{name: "typed text", screen: pullScreen("", "typed words")},
		{name: "tmux mode", screen: pullScreen("", pullDimPlaceholder), mode: true},
		{name: "no input box", screen: "synthetic output\nno box here"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := sendServer(t)
			pane, file := sendStartPane(t, client, tc.screen)
			if tc.mode {
				sendRun(t, client, "copy-mode", "-t", pane)
			}
			transcript := pullTranscript(t)

			ch := pullStart(t, client, pane, transcript)
			out, err := pullWait(t, ch)
			if !errors.Is(err, cli.ErrInputBusy) {
				t.Fatalf("PullQueued = %q, %v; want ErrInputBusy", out, err)
			}
			if out != "" {
				t.Fatalf("PullQueued returned %q with ErrInputBusy, want nothing", out)
			}

			sendRun(t, client, "copy-mode", "-q", "-t", pane)
			sendRun(t, client, "send-keys", "-t", pane, "-l", "--", "X")
			sendAwaitInput(t, file, "X")
		})
	}
}

// TestPullQueuedNothingQueued covers Up on a box with nothing queued. Without the History indicator the call returns
// ErrNothingQueued and sends no Down. With the indicator it sends one Down to restore the empty box.
func TestPullQueuedNothingQueued(t *testing.T) {
	cases := []struct {
		name     string
		screen   string
		wantDown bool
	}{
		{name: "history shown", screen: pullScreen("\x1b[2mHistory 3/3\x1b[0m", pullDimPlaceholder), wantDown: true},
		{name: "no history", screen: pullScreen("", pullDimPlaceholder)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := sendServer(t)
			pane, file := sendStartPane(t, client, tc.screen)
			transcript := pullTranscript(t)

			ch := pullStart(t, client, pane, transcript)
			text, err := pullWait(t, ch)
			if !errors.Is(err, cli.ErrNothingQueued) {
				t.Fatalf("PullQueued = %q, %v; want ErrNothingQueued", text, err)
			}
			if text != "" {
				t.Fatalf("PullQueued returned %q with ErrNothingQueued, want nothing", text)
			}

			want := pullUp
			if tc.wantDown {
				want += pullDown
			}
			sendAwaitInput(t, file, want)
			time.Sleep(pullQuiet)
			sendAwaitInput(t, file, want)
		})
	}
}
