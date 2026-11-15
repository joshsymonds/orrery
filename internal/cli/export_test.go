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

// PullQueuedWithClock is PullQueued with clock timing the wait for the popAll records, so the cli_test package
// decides when that wait ends.
func PullQueuedWithClock(
	ctx context.Context, clock rpcretry.Clock, client *tmuxctl.Client, pane, transcript string,
) (string, error) {
	return pullQueuedWithClock(ctx, clock, client, pane, transcript)
}
