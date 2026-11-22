package cli_test

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/cli"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// goneSentinel is what a test types into a pane after a refused call, to show that the call sent the pane nothing.
const goneSentinel = "sentinel"

// claudeGonePane starts a send pane, which shows an empty input box and writes what it receives to its file, and
// records claude as its @orrery_command. The pane's foreground is cat, so it reads as a pane whose Claude has gone.
func claudeGonePane(t *testing.T, client *tmuxctl.Client) (string, string) {
	t.Helper()
	pane, file := sendStartPane(t, client, sendBoxScreen())
	sendRun(t, client, "set-option", "-p", "-t", pane, protocol.OptionCommand, "claude")
	return pane, file
}

// awaitNothingSent types the sentinel into pane and requires it to be all the pane has received: a key or text that a
// refused call sent would have reached the pane ahead of it.
func awaitNothingSent(t *testing.T, client *tmuxctl.Client, pane, file string) {
	t.Helper()
	sendRun(t, client, "send-keys", "-t", pane, "-l", goneSentinel)
	sendAwaitInput(t, client, pane, file, goneSentinel)
}

// requireClaudeGone fails unless err wraps cli.ErrClaudeGone.
func requireClaudeGone(t *testing.T, what string, err error) {
	t.Helper()
	if !errors.Is(err, cli.ErrClaudeGone) {
		t.Fatalf("%s = %v, want an error wrapping ErrClaudeGone", what, err)
	}
}

func TestSendTextRefusesPaneWhoseClaudeIsGone(t *testing.T) {
	t.Parallel()
	client := sendServer(t)
	pane, file := claudeGonePane(t, client)
	err := cli.Send(t.Context(), client, []string{"--pane", pane, "--text", "synthetic text"}, io.Discard)
	requireClaudeGone(t, "Send --text", err)
	awaitNothingSent(t, client, pane, file)
}

func TestSendKeysRefusesPaneWhoseClaudeIsGone(t *testing.T) {
	t.Parallel()
	client := sendServer(t)
	pane, file := claudeGonePane(t, client)
	err := cli.Send(t.Context(), client, []string{"--pane", pane, "--keys", "Escape"}, io.Discard)
	requireClaudeGone(t, "Send --keys", err)
	awaitNothingSent(t, client, pane, file)
}

// TestSendIdleSendsNothingWhenClaudeIsGone gives the pane everything SendIdle types into, a done state and its input
// box on screen, so only the gone check holds the text back. A gone Claude is not idle: no error, nothing sent.
func TestSendIdleSendsNothingWhenClaudeIsGone(t *testing.T) {
	t.Parallel()
	client := sendServer(t)
	pane, file := claudeGonePane(t, client)
	sendRun(t, client, "set-option", "-p", "-t", pane, protocol.OptionState, string(protocol.StateDone))
	sent, err := cli.SendIdle(t.Context(), client, pane, "synthetic text")
	if sent || err != nil {
		t.Fatalf("SendIdle = (%v, %v), want (false, nil)", sent, err)
	}
	awaitNothingSent(t, client, pane, file)
}

func TestPullQueuedRefusesPaneWhoseClaudeIsGone(t *testing.T) {
	t.Parallel()
	client := sendServer(t)
	pane, file := claudeGonePane(t, client)
	transcript := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(transcript, nil, 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	text, err := cli.PullQueued(t.Context(), client, pane, transcript)
	requireClaudeGone(t, "PullQueued", err)
	if text != "" {
		t.Fatalf("PullQueued took %q, want nothing", text)
	}
	awaitNothingSent(t, client, pane, file)
}

// TestDeclineRefusesPaneWhoseClaudeIsGone declines a pane that has no question: the gone check comes before the
// question check, so the refusal names the gone Claude.
func TestDeclineRefusesPaneWhoseClaudeIsGone(t *testing.T) {
	t.Parallel()
	client := sendServer(t)
	pane, file := claudeGonePane(t, client)
	requireClaudeGone(t, "Decline", cli.Decline(t.Context(), client, pane, protocol.QuestionHash("question")))
	awaitNothingSent(t, client, pane, file)
}

// TestSendTextRefusesPaneAtItsShell sends to a pane whose foreground is a shell and that records no command, as a pane
// whose Claude died before it wrote one reads. The pane's sh signals a channel once it runs and stays the pane's
// foreground, since tmux wait-for is its child and read is a builtin; tmux keeps a signal no one waits for yet, so the
// wait returns once sh runs, and the send never meets the pane before its shell does.
func TestSendTextRefusesPaneAtItsShell(t *testing.T) {
	t.Parallel()
	client := sendServer(t)
	const channel = "orrery-send-test-sh"
	sendRun(t, client, "-f", sendNoConfig, "new-session", "-d", "-s", "s1", "-c", t.TempDir(),
		"sh", "-c", "tmux wait-for -S "+channel+"; read x")
	sendRun(t, client, "wait-for", channel)
	pane := strings.TrimSpace(sendRun(t, client, "list-panes", "-t", "s1", "-F", "#{pane_id}"))
	err := cli.Send(t.Context(), client, []string{"--pane", pane, "--text", "synthetic text"}, io.Discard)
	requireClaudeGone(t, "Send --text to a shell", err)
}

// TestSendTextSendsWhenRecordedCommandRuns records the pane's own foreground command, so its Claude reads as running,
// and the text is pasted as it is to any pane.
func TestSendTextSendsWhenRecordedCommandRuns(t *testing.T) {
	t.Parallel()
	client := sendServer(t)
	pane, file := sendStartPane(t, client, sendQuestion)
	sendRun(t, client, "set-option", "-p", "-t", pane, protocol.OptionCommand, "cat")
	err := cli.Send(t.Context(), client, []string{"--pane", pane, "--text", "synthetic text"}, io.Discard)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	sendAwaitInput(t, client, pane, file, "\x1b[200~synthetic text\x1b[201~\r")
}
