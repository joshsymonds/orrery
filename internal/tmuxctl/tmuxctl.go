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

// Column returns the server's store column: "-L <name>", "-S <path>", or "" for the default server. ServerOfColumn is
// its inverse.
func (s Server) Column() string {
	switch {
	case s.Name != "":
		return "-L " + s.Name
	case s.Socket != "":
		return "-S " + s.Socket
	default:
		return ""
	}
}

// ServerOfColumn returns the server that column names: "-L <name>" gives Name, "-S <path>" gives Socket, and "" gives
// the default server. Any other column, or a flag with an empty argument, is an error.
func ServerOfColumn(column string) (Server, error) {
	if column == "" {
		return Server{}, nil
	}
	if name, ok := strings.CutPrefix(column, "-L "); ok && name != "" {
		return Server{Name: name}, nil
	}
	if socket, ok := strings.CutPrefix(column, "-S "); ok && socket != "" {
		return Server{Socket: socket}, nil
	}
	return Server{}, fmt.Errorf("tmux server column %q: want \"\", \"-L <name>\" or \"-S <path>\"", column)
}

// Client runs tmux commands against one server.
type Client struct {
	serverArgs []string
}

// listFieldSeparator splits ListPanes fields. tmux prints it raw, and it cannot appear in a single-line option value.
const listFieldSeparator = "\x1f"

// listFieldCount is the number of fields in listFormat: one per Pane member other than Host, then the three Listed options.
const listFieldCount = 19

// listFormat is the list-panes format: one field per Pane member in declaration order, then the Listed options.
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
	"#{session_activity}" + listFieldSeparator +
	"#{" + protocol.OptionTool + "}" + listFieldSeparator +
	"#{" + protocol.OptionID + "}" + listFieldSeparator +
	"#{" + protocol.OptionTranscript + "}" + listFieldSeparator +
	"#{" + protocol.OptionTurnStart + "}"

// Listed is one pane from List: its Pane, plus the per-pane options serve reads with it.
type Listed struct {
	protocol.Pane
	// OrreryID is the pane's @orrery_id, or "" when unset.
	OrreryID string
	// Transcript is the pane's @orrery_transcript, or "" when unset.
	Transcript string
	// TurnStart is the unix seconds in the pane's @orrery_turn_start, or 0 when unset.
	TurnStart int64
}

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

// Literal returns value as a tmux argument that tmux reads back as value. tmux ends a command at an argument that ends
// in ";", dropping that ";", and reads a final "\;" as ";", so a final ";" is written as "\;".
func Literal(value string) string {
	if before, ok := strings.CutSuffix(value, ";"); ok {
		return before + `\;`
	}
	return value
}

// Batch runs cmds, each a tmux command and its arguments, as one tmux invocation, and returns its stdout: each
// command's output in turn. Every argument is passed through Literal, so tmux reads each as given, and the commands
// are joined with the ";" arguments that separate them. tmux runs the commands in order and stops at the first that
// fails, whose error Batch returns. With no commands it runs nothing.
func (c *Client) Batch(ctx context.Context, cmds ...[]string) (string, error) {
	if len(cmds) == 0 {
		return "", nil
	}
	var args []string
	for i, cmd := range cmds {
		if i > 0 {
			args = append(args, ";")
		}
		for _, arg := range cmd {
			args = append(args, Literal(arg))
		}
	}
	return c.Run(ctx, args...)
}

// OptionWrite is one change to a pane-scoped option: Name set to Value, or unset when Unset is true.
type OptionWrite struct {
	Name  string
	Value string
	Unset bool
}

// Set returns the write that sets the option name to value.
func Set(name, value string) OptionWrite {
	return OptionWrite{Name: name, Value: value}
}

// Unset returns the write that removes the option name.
func Unset(name string) OptionWrite {
	return OptionWrite{Name: name, Unset: true}
}

// WriteCommands returns the set-option command of each of writes on paneID, in order, for Batch.
func WriteCommands(paneID string, writes []OptionWrite) [][]string {
	cmds := make([][]string, 0, len(writes))
	for _, write := range writes {
		if write.Unset {
			cmds = append(cmds, []string{"set-option", "-p", "-u", "-t", paneID, write.Name})
			continue
		}
		cmds = append(cmds, []string{"set-option", "-p", "-t", paneID, write.Name, write.Value})
	}
	return cmds
}

// WritePaneOptions applies writes to paneID in order in one tmux invocation. With no writes it runs nothing.
func (c *Client) WritePaneOptions(ctx context.Context, paneID string, writes []OptionWrite) error {
	_, err := c.Batch(ctx, WriteCommands(paneID, writes)...)
	return err
}

// SetPaneOption sets the pane-scoped option name to value on paneID.
func (c *Client) SetPaneOption(ctx context.Context, paneID, name, value string) error {
	return c.WritePaneOptions(ctx, paneID, []OptionWrite{Set(name, value)})
}

// UnsetPaneOption removes the pane-scoped option name from paneID.
func (c *Client) UnsetPaneOption(ctx context.Context, paneID, name string) error {
	return c.WritePaneOptions(ctx, paneID, []OptionWrite{Unset(name)})
}

// valueSeparator splits the values PaneValues reads. tmux prints it raw, and it cannot appear in a single-line option
// value.
const valueSeparator = "\x1f"

// PaneValues reads the options names on paneID in one tmux invocation and returns their values in order. An unset
// option and one set to "" both read as "".
func (c *Client) PaneValues(ctx context.Context, paneID string, names ...string) ([]string, error) {
	out, err := c.Batch(ctx, ValuesCommand(paneID, names...))
	if err != nil {
		return nil, err
	}
	return ParseValues(out, len(names))
}

// ValuesCommand returns the display-message command that prints the options names of paneID on one line, for Batch,
// whose output ParseValues reads.
func ValuesCommand(paneID string, names ...string) []string {
	fields := make([]string, len(names))
	for i, name := range names {
		fields[i] = "#{" + name + "}"
	}
	return []string{"display-message", "-p", "-t", paneID, strings.Join(fields, valueSeparator)}
}

// ParseValues returns the count values the line of a ValuesCommand's output holds. A line with another count of
// values is an error.
func ParseValues(out string, count int) ([]string, error) {
	values := strings.Split(strings.TrimSuffix(out, "\n"), valueSeparator)
	if len(values) != count {
		return nil, fmt.Errorf("tmux: read %d option values, want %d", len(values), count)
	}
	return values, nil
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

// List returns every pane of every session on the server, without Host, with the options serve reads.
func (c *Client) List(ctx context.Context) ([]Listed, error) {
	out, err := c.Run(ctx, "list-panes", "-a", "-F", listFormat)
	if err != nil {
		return nil, err
	}

	var listed []Listed
	for line := range strings.SplitSeq(out, "\n") {
		if line == "" {
			continue
		}
		entry, parseErr := parseListed(line)
		if parseErr != nil {
			return nil, parseErr
		}
		listed = append(listed, entry)
	}
	return listed, nil
}

// ListPanes returns every pane of every session on the server, without Host.
func (c *Client) ListPanes(ctx context.Context) ([]protocol.Pane, error) {
	listed, err := c.List(ctx)
	if err != nil {
		return nil, err
	}

	var panes []protocol.Pane
	for _, entry := range listed {
		panes = append(panes, entry.Pane)
	}
	return panes, nil
}

// parseListed decodes one line of the listFormat output.
func parseListed(line string) (Listed, error) {
	fields := strings.Split(line, listFieldSeparator)
	if len(fields) != listFieldCount {
		return Listed{}, fmt.Errorf("list-panes: got %d fields, want %d", len(fields), listFieldCount)
	}

	windowIndex, err := strconv.Atoi(fields[2])
	if err != nil {
		return Listed{}, fmt.Errorf("list-panes window index %q: %w", fields[2], err)
	}
	paneIndex, err := strconv.Atoi(fields[3])
	if err != nil {
		return Listed{}, fmt.Errorf("list-panes pane index %q: %w", fields[3], err)
	}

	var state protocol.State
	if fields[6] != "" {
		state, err = protocol.ParseState(fields[6])
		if err != nil {
			return Listed{}, fmt.Errorf("pane %s: %w", fields[0], err)
		}
	}
	changed, err := parseTimestamp(fields[0], protocol.OptionChanged, fields[9])
	if err != nil {
		return Listed{}, err
	}
	seen, err := parseTimestamp(fields[0], protocol.OptionSeen, fields[10])
	if err != nil {
		return Listed{}, err
	}
	activity, err := parseTimestamp(fields[0], "session_activity", fields[14])
	if err != nil {
		return Listed{}, err
	}
	turnStart, err := parseTimestamp(fields[0], protocol.OptionTurnStart, fields[18])
	if err != nil {
		return Listed{}, err
	}

	return Listed{
		Pane: protocol.Pane{
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
			Tool:          fields[15],
		},
		OrreryID:   fields[16],
		Transcript: fields[17],
		TurnStart:  turnStart,
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
