package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/joshsymonds/orrery/internal/autoseen"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// Seen implements `orrery seen --pane ID`, setting the pane's @orrery_seen option to the current unix time.
// With --focus it sets the option only when a client shows the pane's window and had input within the
// autoseen activity window; otherwise it writes nothing.
func Seen(ctx context.Context, client *tmuxctl.Client, args []string, _ io.Writer) error {
	flags := flag.NewFlagSet("seen", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	pane := flags.String("pane", "", "pane ID to mark seen")
	focus := flags.Bool("focus", false, "mark seen only while a recently active client shows the pane's window")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("seen: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("seen: unexpected argument %q", flags.Arg(0))
	}
	if *pane == "" {
		return errors.New("seen: --pane is required")
	}

	now := time.Now()
	if *focus {
		viewing, err := autoseen.Viewing(ctx, client, *pane, now)
		if err != nil {
			return fmt.Errorf("seen: %w", err)
		}
		if !viewing {
			return nil
		}
	}
	if err := client.SetPaneOption(ctx, *pane, protocol.OptionSeen, strconv.FormatInt(now.Unix(), 10)); err != nil {
		return fmt.Errorf("seen: %w", err)
	}
	return nil
}
