// Package cli implements the orrery subcommands.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// List writes every pane of client's server to out as one compact JSON array followed by a newline.
// It accepts only --json, and prints [] when no server is running.
func List(ctx context.Context, client *tmuxctl.Client, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("list", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	asJSON := flags.Bool("json", false, "print the panes as a JSON array")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("orrery list: %w", err)
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("orrery list: unexpected argument %q", flags.Arg(0))
	}
	if !*asJSON {
		return errors.New("orrery list: --json is required")
	}

	panes, err := client.ListPanes(ctx)
	if err != nil && !listNotRunning(err) {
		return fmt.Errorf("orrery list: %w", err)
	}

	host, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("orrery list: hostname: %w", err)
	}
	result := make([]protocol.Pane, 0, len(panes))
	for _, pane := range panes {
		pane.Host = host
		result = append(result, pane)
	}

	if err = json.NewEncoder(out).Encode(result); err != nil {
		return fmt.Errorf("orrery list: encode: %w", err)
	}
	return nil
}

// listNotRunning reports whether err is tmux saying that no server is running on its socket.
// A missing socket file makes tmux print "error connecting to <socket> (No such file or directory)".
func listNotRunning(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "no server running on") ||
		(strings.Contains(msg, "error connecting to") && strings.Contains(msg, "(No such file or directory)"))
}
