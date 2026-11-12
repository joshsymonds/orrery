package desk

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// NewestInput returns the newest client_activity, in unix seconds, among the clients of client's tmux server: the
// last time a person gave any attached client input. ok is false when no client is attached.
func NewestInput(ctx context.Context, client *tmuxctl.Client) (input int64, ok bool, err error) {
	out, err := client.Run(ctx, "list-clients", "-F", "#{client_activity}")
	if err != nil {
		return 0, false, fmt.Errorf("list clients: %w", err)
	}
	return newestActivity(out)
}

// newestActivity returns the largest of the unix-second lines in out. ok is false when out has no lines.
func newestActivity(out string) (input int64, ok bool, err error) {
	for line := range strings.SplitSeq(out, "\n") {
		if line == "" {
			continue
		}
		activity, parseErr := strconv.ParseInt(line, 10, 64)
		if parseErr != nil {
			return 0, false, fmt.Errorf("client activity %q: %w", line, parseErr)
		}
		if !ok || activity > input {
			input = activity
			ok = true
		}
	}
	return input, ok, nil
}
