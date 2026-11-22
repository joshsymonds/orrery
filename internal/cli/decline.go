package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// ErrPaneInMode is returned by Decline when the pane is in a tmux mode, such as copy mode, which takes the
// Escape key in place of the pane.
var ErrPaneInMode = errors.New("pane is in a tmux mode")

// Decline declines the question on pane and ends its turn by sending Escape. It sends nothing unless pane is
// not in a tmux mode, Claude Code runs in its foreground, and sendCheckQuestion accepts questionHash for it. Every
// refusal names its reason.
func Decline(ctx context.Context, client *tmuxctl.Client, pane, questionHash string) error {
	out, err := client.Run(ctx, "display-message", "-p", "-t", pane, "#{pane_in_mode}")
	if err != nil {
		return fmt.Errorf("decline: %w", err)
	}
	if strings.TrimSuffix(out, "\n") != "0" {
		return fmt.Errorf("decline: %w", ErrPaneInMode)
	}
	if goneErr := requireClaude(ctx, client, pane, "decline"); goneErr != nil {
		return goneErr
	}
	if err = sendCheckQuestion(ctx, client, pane, questionHash); err != nil {
		return fmt.Errorf("decline: %w", err)
	}
	if _, err = client.Run(ctx, sendKeysCmd, "-t", pane, "Escape"); err != nil {
		return fmt.Errorf("decline: %w", err)
	}
	return nil
}
