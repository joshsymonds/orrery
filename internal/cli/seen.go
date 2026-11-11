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

// SeenReporter tells orreryd that pane was seen at seen, the time Seen wrote to the pane's option.
type SeenReporter func(pane string, seen int64) error

// Seen implements `orrery seen --pane ID`, setting the pane's @orrery_seen option to the current unix time.
// With --focus it sets the option only when a client shows the pane's window and had input within the
// autoseen activity window; otherwise it writes nothing. After it writes the option it calls report with the
// pane and that time; a nil report is skipped.
func Seen(ctx context.Context, client *tmuxctl.Client, report SeenReporter, args []string, _ io.Writer) error {
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
	seen := now.Unix()
	if err := client.SetPaneOption(ctx, *pane, protocol.OptionSeen, strconv.FormatInt(seen, 10)); err != nil {
		return fmt.Errorf("seen: %w", err)
	}
	if report != nil {
		if err := report(*pane, seen); err != nil {
			return fmt.Errorf("seen: report: %w", err)
		}
	}
	return nil
}
