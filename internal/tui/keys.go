package tui

import (
	"context"
	"fmt"
	"io"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/joshsymonds/orrery/internal/cli"
	"github.com/joshsymonds/orrery/internal/keymap"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/shell"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
	"github.com/joshsymonds/orrery/internal/view"
)

// enterKey is the name of the Enter key. Enter keeps the filter in filter input and runs the
// action of the cursor's pane outside it.
const enterKey = "enter"

// Cursor steps: one row down and one row up.
const (
	nextRow = 1
	prevRow = -1
)

// keyPressed applies a key press. An open help overlay closes on any key and runs nothing
// else. Filter input takes every key. Otherwise the key runs the action keymap binds to it on
// the tree.
func (m model) keyPressed(msg tea.KeyPressMsg) (model, tea.Cmd) {
	if m.help {
		m.help = false
		return m, nil
	}
	if m.filtering {
		return m.filterKey(msg)
	}
	action, ok := keymap.Lookup(keymap.Tree, msg.String())
	if !ok {
		return m, nil
	}
	switch action {
	case keymap.ActDown:
		return m.move(nextRow)
	case keymap.ActUp:
		return m.move(prevRow)
	case keymap.ActNextUnseen:
		return m.nextUnseen()
	case keymap.ActFilter:
		m.filtering = true
		return m, nil
	case keymap.ActHelp:
		m.help = true
		return m, nil
	case keymap.ActQuit:
		return m, tea.Quit
	case keymap.ActSwitch, keymap.ActMarkSeen, keymap.ActNewClaude, keymap.ActShell, keymap.ActPicker:
		return m.paneAction(action)
	case keymap.ActClosePane, keymap.ActCloseProject, keymap.ActTree:
		// The tree has no behavior for these yet. ActTree is a tmux binding.
		return m, nil
	}
	return m, nil
}

// paneAction runs action on the pane under the cursor. With no row shown it does nothing.
func (m model) paneAction(action keymap.Action) (model, tea.Cmd) {
	cur, ok := m.current()
	if !ok {
		return m, nil
	}
	tty := m.clientTTY
	switch action {
	case keymap.ActSwitch:
		return m, m.action(func(ctx context.Context, client *tmuxctl.Client) error {
			return switchAndMark(ctx, client, tty, cur.ID)
		}, true)
	case keymap.ActMarkSeen:
		return m, m.action(func(ctx context.Context, client *tmuxctl.Client) error {
			return markSeen(ctx, client, cur.ID)
		}, false)
	case keymap.ActNewClaude:
		return m, m.action(func(ctx context.Context, client *tmuxctl.Client) error {
			return newClaudeWindow(ctx, client, cur.Session, cur.Cwd)
		}, false)
	case keymap.ActShell:
		return m, m.action(func(ctx context.Context, client *tmuxctl.Client) error {
			return shell.Open(ctx, client, cur.Cwd, tty)
		}, true)
	case keymap.ActPicker:
		m.result = Result{Action: ActionIntent, Dir: cur.Cwd}
		return m, tea.Quit
	case keymap.ActDown, keymap.ActUp, keymap.ActNextUnseen, keymap.ActFilter, keymap.ActHelp, keymap.ActQuit,
		keymap.ActClosePane, keymap.ActCloseProject, keymap.ActTree:
		// keyPressed routes only the pane actions here.
		return m, nil
	}
	return m, nil
}

// action runs fn against the invoking client's server in a command. Its outcome arrives as an
// actionMsg, which quits the tree when quit is set.
func (m model) action(fn func(context.Context, *tmuxctl.Client) error, quit bool) tea.Cmd {
	invoke := m.invoke
	return func() tea.Msg {
		return actionMsg{err: invoke(fn), quit: quit}
	}
}

// switchAndMark switches the client at tty to paneID, then marks the pane seen. A pane is
// marked only once the client has been switched to it.
func switchAndMark(ctx context.Context, client *tmuxctl.Client, tty, paneID string) error {
	if _, err := client.Run(ctx, "switch-client", "-c", tty, "-t", paneID); err != nil {
		return fmt.Errorf("switch client: %w", err)
	}
	return markSeen(ctx, client, paneID)
}

// markSeen sets the @orrery_seen option of paneID to the current time.
func markSeen(ctx context.Context, client *tmuxctl.Client, paneID string) error {
	if err := cli.Seen(ctx, client, []string{"--pane", paneID}, io.Discard); err != nil {
		return fmt.Errorf("mark seen: %w", err)
	}
	return nil
}

// newClaudeWindow adds a window running claude to session, started in dir.
func newClaudeWindow(ctx context.Context, client *tmuxctl.Client, session, dir string) error {
	_, err := client.Run(ctx, "new-window", "-t", "="+session+":",
		"-c", shell.TmuxDirArg(dir), "claude")
	if err != nil {
		return fmt.Errorf("new claude window: %w", err)
	}
	return nil
}

// filterKey edits the query. Esc clears it, Enter keeps it, Backspace deletes its last
// character, and any other typed text is appended.
func (m model) filterKey(msg tea.KeyPressMsg) (model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.filtering, m.query = false, ""
	case enterKey:
		m.filtering = false
	case "backspace":
		if runes := []rune(m.query); len(runes) > 0 {
			m.query = string(runes[:len(runes)-1])
		}
	default:
		if msg.Text == "" {
			return m, nil
		}
		m.query += msg.Text
	}
	m = m.sync()
	return m, m.previewCmd()
}

// previewCmd captures the pane under the cursor, or returns nil when no row is shown.
func (m model) previewCmd() tea.Cmd {
	cur, ok := m.current()
	if !ok {
		return nil
	}
	return m.loadPreview(cur.ID)
}

// move steps the cursor by step rows, clamped to the first and last visible rows.
func (m model) move(step int) (model, tea.Cmd) {
	rows := m.rows()
	if len(rows) == 0 {
		return m, nil
	}
	return m.selectRow(rows, min(max(m.locate(rows)+step, 0), len(rows)-1))
}

// nextUnseen moves the cursor to the next unseen row after it, wrapping around. It does not
// move when no row is unseen.
func (m model) nextUnseen() (model, tea.Cmd) {
	rows := m.rows()
	if len(rows) == 0 {
		return m, nil
	}
	start := m.locate(rows)
	for offset := range rows {
		i := (start + 1 + offset) % len(rows)
		if view.Unseen(rows[i]) {
			return m.selectRow(rows, i)
		}
	}
	return m, nil
}

// selectRow puts the cursor on rows[i] and captures its preview.
func (m model) selectRow(rows []protocol.Pane, i int) (model, tea.Cmd) {
	m.cursor, m.cursorID = i, rows[i].ID
	return m, m.loadPreview(rows[i].ID)
}

// visible returns the groups shown under the query, in their display order. A row is shown
// when its label, summary, or session, or its group's Dir, contains the query, ignoring case.
// A group with no shown row is dropped.
func (m model) visible() []view.Group {
	needle := strings.ToLower(m.query)
	var groups []view.Group
	for _, group := range m.groups {
		if strings.Contains(strings.ToLower(group.Dir), needle) {
			groups = append(groups, group)
			continue
		}
		var panes []protocol.Pane
		for _, pane := range group.Panes {
			if matches(pane, needle) {
				panes = append(panes, pane)
			}
		}
		if len(panes) > 0 {
			groups = append(groups, view.Group{Dir: group.Dir, Panes: panes})
		}
	}
	return groups
}

// matches reports whether the label, summary, or session of p contains needle, which is
// lowercase.
func matches(p protocol.Pane, needle string) bool {
	for _, field := range []string{p.Label, p.Summary, p.Session} {
		if strings.Contains(strings.ToLower(field), needle) {
			return true
		}
	}
	return false
}
