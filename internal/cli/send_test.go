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

// sendStartPane starts session s1 whose pane prints sendQuestion, enables bracketed paste, turns
// off the line discipline, prints "ready", and then runs cat into a file. It returns the pane ID and
// the file path; the pane is ready once "ready" is on screen.
func sendStartPane(t *testing.T, client *tmuxctl.Client) (string, string) {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "input")
	script := fmt.Sprintf(
		`printf "%s\n"; printf "\033[?2004h"; stty -icanon -echo -icrnl; printf "ready\n"; exec cat > %s`,
		sendQuestion, file,
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
	pane, file := sendStartPane(t, client)

	var out bytes.Buffer
	if err := cli.Send(t.Context(), client, []string{"--pane", pane, "--text", "synthetic text"}, &out); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("Send printed %q, want nothing", out.String())
	}
	sendAwaitInput(t, file, "\x1b[200~synthetic text\x1b[201~\r")
}

func TestSendKeysDeliversKeys(t *testing.T) {
	client := sendServer(t)
	pane, file := sendStartPane(t, client)

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
	pane, file := sendStartPane(t, client)

	args := []string{"--pane", pane, "--keys", "--", "-"}
	if err := cli.Send(t.Context(), client, args, &bytes.Buffer{}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	sendAwaitInput(t, file, "-")
}

func TestSendExpectQuestionMatchingSends(t *testing.T) {
	client := sendServer(t)
	pane, file := sendStartPane(t, client)
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
			pane, file := sendStartPane(t, client)
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
	pane, _ := sendStartPane(t, client)

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
