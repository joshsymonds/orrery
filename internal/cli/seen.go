package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// Seen implements `orrery seen --pane ID`, setting the pane's @orrery_seen option to the current unix time.
func Seen(ctx context.Context, client *tmuxctl.Client, args []string, _ io.Writer) error {
	flags := flag.NewFlagSet("seen", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	pane := flags.String("pane", "", "pane ID to mark seen")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("seen: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("seen: unexpected argument %q", flags.Arg(0))
	}
	if *pane == "" {
		return errors.New("seen: --pane is required")
	}

	now := strconv.FormatInt(time.Now().Unix(), 10)
	if err := client.SetPaneOption(ctx, *pane, protocol.OptionSeen, now); err != nil {
		return fmt.Errorf("seen: %w", err)
	}
	return nil
}
