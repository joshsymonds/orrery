// Package tmuxctl runs the tmux binary against a chosen server and reads and writes per-pane options.
package tmuxctl

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/joshsymonds/orrery/internal/protocol"
)

// Server selects the tmux server. Name adds -L <name>, Socket adds -S <path>; both empty means the default server.
type Server struct {
	Name   string
	Socket string
}

// Client runs tmux commands against one server.
type Client struct {
	serverArgs []string
}

// listFieldSeparator splits ListPanes fields. tmux prints it raw, and it cannot appear in a single-line option value.
const listFieldSeparator = "\x1f"

// listFieldCount is the number of fields in listFormat, one per Pane member other than Host.
const listFieldCount = 15

// listFormat is the list-panes format: one field per Pane member in declaration order.
const listFormat = "#{pane_id}" + listFieldSeparator +
	"#{session_name}" + listFieldSeparator +
	"#{window_index}" + listFieldSeparator +
	"#{pane_index}" + listFieldSeparator +
	"#{pane_current_path}" + listFieldSeparator +
	"#{pane_current_command}" + listFieldSeparator +
	"#{" + protocol.OptionState + "}" + listFieldSeparator +
	"#{" + protocol.OptionSummary + "}" + listFieldSeparator +
	"#{" + protocol.OptionLabel + "}" + listFieldSeparator +
	"#{" + protocol.OptionChanged + "}" + listFieldSeparator +
	"#{" + protocol.OptionSeen + "}" + listFieldSeparator +
	"#{" + protocol.OptionQuestion + "}" + listFieldSeparator +
	"#{" + protocol.OptionContext + "}" + listFieldSeparator +
	"#{" + protocol.OptionClaudeSession + "}" + listFieldSeparator +
	"#{session_activity}"

// New validates server and returns a Client for it.
func New(server Server) (*Client, error) {
	switch {
	case server.Name != "" && server.Socket != "":
		return nil, errors.New("tmux server: set Name or Socket, not both")
	case server.Name != "":
		return &Client{serverArgs: []string{"-L", server.Name}}, nil
	case server.Socket != "":
		return &Client{serverArgs: []string{"-S", server.Socket}}, nil
	default:
		return &Client{}, nil
	}
}

// Run runs tmux with the server flags followed by args and returns stdout.
func (c *Client) Run(ctx context.Context, args ...string) (string, error) {
	argv := make([]string, 0, len(c.serverArgs)+len(args))
	argv = append(argv, c.serverArgs...)
	argv = append(argv, args...)

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "tmux", argv...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("tmux: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// SetPaneOption sets the pane-scoped option name to value on paneID.
func (c *Client) SetPaneOption(ctx context.Context, paneID, name, value string) error {
	_, err := c.Run(ctx, "set-option", "-p", "-t", paneID, name, value)
	return err
}

// UnsetPaneOption removes the pane-scoped option name from paneID.
func (c *Client) UnsetPaneOption(ctx context.Context, paneID, name string) error {
	_, err := c.Run(ctx, "set-option", "-p", "-u", "-t", paneID, name)
	return err
}

// PaneOption returns the pane-scoped option name on paneID; ok is false when it is unset.
// With -q and -v, tmux prints nothing for an unset option and a bare newline for one set to "".
func (c *Client) PaneOption(ctx context.Context, paneID, name string) (string, bool, error) {
	out, err := c.Run(ctx, "show-options", "-p", "-q", "-v", "-t", paneID, name)
	if err != nil {
		return "", false, err
	}
	if out == "" {
		return "", false, nil
	}
	return strings.TrimSuffix(out, "\n"), true, nil
}

// ListPanes returns every pane of every session on the server, without Host.
func (c *Client) ListPanes(ctx context.Context) ([]protocol.Pane, error) {
	out, err := c.Run(ctx, "list-panes", "-a", "-F", listFormat)
	if err != nil {
		return nil, err
	}

	var panes []protocol.Pane
	for line := range strings.SplitSeq(out, "\n") {
		if line == "" {
			continue
		}
		pane, parseErr := parsePane(line)
		if parseErr != nil {
			return nil, parseErr
		}
		panes = append(panes, pane)
	}
	return panes, nil
}

// parsePane decodes one line of the listFormat output.
func parsePane(line string) (protocol.Pane, error) {
	fields := strings.Split(line, listFieldSeparator)
	if len(fields) != listFieldCount {
		return protocol.Pane{}, fmt.Errorf("list-panes: got %d fields, want %d", len(fields), listFieldCount)
	}

	windowIndex, err := strconv.Atoi(fields[2])
	if err != nil {
		return protocol.Pane{}, fmt.Errorf("list-panes window index %q: %w", fields[2], err)
	}
	paneIndex, err := strconv.Atoi(fields[3])
	if err != nil {
		return protocol.Pane{}, fmt.Errorf("list-panes pane index %q: %w", fields[3], err)
	}

	var state protocol.State
	if fields[6] != "" {
		state, err = protocol.ParseState(fields[6])
		if err != nil {
			return protocol.Pane{}, fmt.Errorf("pane %s: %w", fields[0], err)
		}
	}
	changed, err := parseTimestamp(fields[0], protocol.OptionChanged, fields[9])
	if err != nil {
		return protocol.Pane{}, err
	}
	seen, err := parseTimestamp(fields[0], protocol.OptionSeen, fields[10])
	if err != nil {
		return protocol.Pane{}, err
	}
	activity, err := parseTimestamp(fields[0], "session_activity", fields[14])
	if err != nil {
		return protocol.Pane{}, err
	}

	return protocol.Pane{
		ID:            fields[0],
		Session:       fields[1],
		WindowIndex:   windowIndex,
		PaneIndex:     paneIndex,
		Cwd:           fields[4],
		Command:       fields[5],
		State:         state,
		Summary:       fields[7],
		Label:         fields[8],
		Changed:       changed,
		Seen:          seen,
		Question:      fields[11],
		Context:       fields[12],
		ClaudeSession: fields[13],
		Activity:      activity,
	}, nil
}

// parseTimestamp returns the integer value of an option, or 0 when the option is unset.
func parseTimestamp(paneID, name, value string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("pane %s %s %q: %w", paneID, name, value, err)
	}
	return n, nil
}
