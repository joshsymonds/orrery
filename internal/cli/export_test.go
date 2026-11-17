package cli

import (
	"context"
	"io"

	"github.com/joshsymonds/orrery/internal/rpcretry"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// SendWithClock is Send with clock timing the typed send's pauses, so the cli_test package drives them without
// sleeping.
func SendWithClock(
	ctx context.Context, clock rpcretry.Clock, client *tmuxctl.Client, args []string, out io.Writer,
) error {
	return sendWithClock(ctx, clock, client, args, out)
}

// SendIdleWithClock is SendIdle with clock timing the typed send's pauses, so the cli_test package drives them without
// sleeping.
func SendIdleWithClock(
	ctx context.Context, clock rpcretry.Clock, client *tmuxctl.Client, pane, text string,
) (bool, error) {
	return sendIdleWithClock(ctx, clock, client, pane, text)
}

// WakePaneWithClock is WakePane with clock timing the typed send's pauses, so the cli_test package drives them without
// sleeping.
func WakePaneWithClock(
	ctx context.Context, clock rpcretry.Clock, server, pane, text string,
) (bool, error) {
	return wakePaneWithClock(ctx, clock, server, pane, text)
}

// PaneWakeText is paneWakeText, the text that WakePane types for a text, so the cli_test package checks it directly.
func PaneWakeText(pane, text string) string {
	return paneWakeText(pane, text)
}

// PaneWakeCutLine is the line that holds where a cut text lost its middle.
const PaneWakeCutLine = paneWakeCutLine

// SendTypeable is sendTypeable, so the cli_test package checks that a text types exactly into pane.
func SendTypeable(pane, text string) bool {
	return sendTypeable(pane, text)
}

// PullQueuedWithClock is PullQueued with clock timing the wait for the popAll records, so the cli_test package
// decides when that wait ends.
func PullQueuedWithClock(
	ctx context.Context, clock rpcretry.Clock, client *tmuxctl.Client, pane, transcript string,
) (string, error) {
	return pullQueuedWithClock(ctx, clock, client, pane, transcript)
}
