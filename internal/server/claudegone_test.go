package server_test

import (
	"errors"
	"testing"

	"connectrpc.com/connect"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// TestPanesRefuseAPaneWhoseClaudeIsGone sends text, keys and a pull to a live pane with a queued message whose
// @orrery_command records claude while its foreground is the test's cat, as a pane reads once its Claude was killed.
// Each is refused with aborted and the reason, and no key reaches the pane.
func TestPanesRefuseAPaneWhoseClaudeIsGone(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	transcript := writeNewestTranscript(t, pqLine(t, "enqueue", "alpha line", ""))
	pane, file := pqPane(t, tmux, "s1", pqDim, transcript)
	if err := tmux.SetPaneOption(t.Context(), pane, protocol.OptionCommand, "claude"); err != nil {
		t.Fatalf("SetPaneOption(%s): %v", protocol.OptionCommand, err)
	}
	client := panesClient(t, newHandler(t, tmux))

	refused := func(what string, err error) {
		t.Helper()
		var connectErr *connect.Error
		if !errors.As(err, &connectErr) {
			t.Fatalf("%s error = %v, want a *connect.Error", what, err)
		}
		if connectErr.Code() != connect.CodeAborted || connectErr.Message() != "claude is not running in the pane" {
			t.Fatalf("%s = %v %q, want aborted \"claude is not running in the pane\"", what,
				connectErr.Code(), connectErr.Message())
		}
	}
	_, err := client.SendText(t.Context(), connect.NewRequest(&orreryv1.SendTextRequest{Pane: pane, Text: synthText}))
	refused("Panes.SendText", err)
	_, err = client.SendKeys(t.Context(),
		connect.NewRequest(&orreryv1.SendKeysRequest{Pane: pane, Keys: []string{"Escape"}}))
	refused("Panes.SendKeys", err)
	_, err = client.PullQueued(t.Context(), connect.NewRequest(&orreryv1.PullQueuedRequest{Pane: pane}))
	refused("Panes.PullQueued", err)
	pqAssertQuiet(t, tmux, pane, file)
}
