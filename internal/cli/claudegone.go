package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// ErrClaudeGone is returned when Claude Code no longer runs in the pane's foreground (see protocol.ClaudeGone). Text
// or keys sent then would reach whatever holds the pane now, such as the shell Claude ran from, which runs typed text
// as a command, so nothing is sent.
var ErrClaudeGone = errors.New("claude is not running in the pane")

// claudeFieldSeparator splits the fields claudeRuns reads. tmux prints it raw, and no command name holds it.
const claudeFieldSeparator = "\x1f"

// claudeFormat is the display-message format claudeRuns reads: the pane's foreground command, then its
// @orrery_command.
const claudeFormat = "#{pane_current_command}" + claudeFieldSeparator + "#{" + protocol.OptionCommand + "}"

// claudeRuns reports whether Claude Code runs in pane's foreground, as protocol.ClaudeGone decides from the pane's
// command and its @orrery_command, read in one tmux invocation.
func claudeRuns(ctx context.Context, client *tmuxctl.Client, pane string) (bool, error) {
	out, err := client.Run(ctx, "display-message", "-p", "-t", pane, claudeFormat)
	if err != nil {
		return false, fmt.Errorf("read pane %s command: %w", pane, err)
	}
	current, recorded, ok := strings.Cut(strings.TrimSuffix(out, "\n"), claudeFieldSeparator)
	if !ok {
		return false, fmt.Errorf("unexpected pane command format %q", out)
	}
	return !protocol.ClaudeGone(current, recorded), nil
}

// requireClaude returns an error, prefixed with verb, unless Claude Code runs in pane's foreground. It wraps
// ErrClaudeGone when Claude does not.
func requireClaude(ctx context.Context, client *tmuxctl.Client, pane, verb string) error {
	runs, err := claudeRuns(ctx, client, pane)
	if err != nil {
		return fmt.Errorf("%s: %w", verb, err)
	}
	if !runs {
		return fmt.Errorf("%s: %w", verb, ErrClaudeGone)
	}
	return nil
}
