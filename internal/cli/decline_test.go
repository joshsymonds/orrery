package cli_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/cli"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// declineQuestion is a synthetic question stored on the pane and shown on its screen; declineOther is a
// different synthetic question, whose hash is stale for the pane.
const (
	declineQuestion = "Apply synthetic patch?"
	declineOther    = "Delete synthetic cache?"
)

// declineSentinel sends "ok" and Enter with cli.Send, then requires the pane to receive exactly "ok\r". Anything a
// refused Decline delivered would precede it in the input file.
func declineSentinel(t *testing.T, client *tmuxctl.Client, pane, file string) {
	t.Helper()
	var out bytes.Buffer
	if err := cli.Send(t.Context(), client, []string{"--pane", pane, "--keys", "o", "k", "Enter"}, &out); err != nil {
		t.Fatalf("sentinel Send: %v", err)
	}
	sendAwaitInput(t, client, pane, file, "ok\r")
}

func TestDeclinePaneSendsEscape(t *testing.T) {
	t.Parallel()
	client := sendServer(t)
	pane, file := sendStartPane(t, client, declineQuestion)
	sendSetQuestion(t, client, pane, declineQuestion)

	if err := cli.Decline(t.Context(), client, pane, protocol.QuestionHash(declineQuestion)); err != nil {
		t.Fatalf("Decline: %v", err)
	}
	sendAwaitInput(t, client, pane, file, "\x1b")
	t.Logf("decline sent Escape: pane %s received \\x1b after the expect-question guard accepted hash %s",
		pane, protocol.QuestionHash(declineQuestion))
}

func TestDeclinePaneRefusesStaleHash(t *testing.T) {
	t.Parallel()
	client := sendServer(t)
	pane, file := sendStartPane(t, client, declineQuestion)
	sendSetQuestion(t, client, pane, declineQuestion)

	err := cli.Decline(t.Context(), client, pane, protocol.QuestionHash(declineOther))
	if !errors.Is(err, cli.ErrQuestionRefused) {
		t.Fatalf("Decline error = %v, want ErrQuestionRefused", err)
	}
	if !strings.Contains(err.Error(), "hash does not match") {
		t.Fatalf("Decline error %q does not name the hash mismatch", err)
	}
	t.Logf("decline refused: %v", err)
	declineSentinel(t, client, pane, file)
	t.Logf("decline sent nothing: pane %s received only the sentinel \"ok\\r\"", pane)
}

func TestDeclinePaneRefusesNoQuestion(t *testing.T) {
	t.Parallel()
	client := sendServer(t)
	pane, file := sendStartPane(t, client, declineQuestion)

	err := cli.Decline(t.Context(), client, pane, protocol.QuestionHash(declineQuestion))
	if !errors.Is(err, cli.ErrQuestionRefused) {
		t.Fatalf("Decline error = %v, want ErrQuestionRefused", err)
	}
	if !strings.Contains(err.Error(), "pane has no question") {
		t.Fatalf("Decline error %q does not name the missing question", err)
	}
	t.Logf("decline refused: %v", err)
	declineSentinel(t, client, pane, file)
	t.Logf("decline sent nothing: pane %s received only the sentinel \"ok\\r\"", pane)
}

func TestDeclinePaneRefusesCopyMode(t *testing.T) {
	t.Parallel()
	client := sendServer(t)
	pane, _ := sendStartPane(t, client, declineQuestion)
	sendSetQuestion(t, client, pane, declineQuestion)
	sendRun(t, client, "copy-mode", "-t", pane)

	err := cli.Decline(t.Context(), client, pane, protocol.QuestionHash(declineQuestion))
	if !errors.Is(err, cli.ErrPaneInMode) {
		t.Fatalf("Decline error = %v, want ErrPaneInMode", err)
	}
	if !strings.Contains(err.Error(), "in a tmux mode") {
		t.Fatalf("Decline error %q does not name the mode", err)
	}
	t.Logf("decline refused: %v", err)

	// Copy mode would leave on Escape, so a still-set mode shows that no Escape was sent.
	mode := strings.TrimSpace(sendRun(t, client, "display-message", "-p", "-t", pane, "#{pane_in_mode}"))
	if mode != "1" {
		t.Fatalf("pane_in_mode = %q after Decline, want 1", mode)
	}
	t.Logf("decline sent nothing: pane %s pane_in_mode is still 1, so no Escape reached copy mode", pane)
}
