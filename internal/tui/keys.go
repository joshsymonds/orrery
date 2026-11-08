package tui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/joshsymonds/orrery/internal/cli"
	"github.com/joshsymonds/orrery/internal/protocol"
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

// keyPressed applies a key press. Filter input takes every key. Otherwise the keys move the
// cursor, jump to the next unseen row, start filter input, act on the pane under the cursor,
// or close the tree.
func (m model) keyPressed(msg tea.KeyPressMsg) (model, tea.Cmd) {
	if m.filtering {
		return m.filterKey(msg)
	}
	switch msg.String() {
	case "j", "down":
		return m.move(nextRow)
	case "k", "up":
		return m.move(prevRow)
	case "n":
		return m.nextUnseen()
	case "/":
		m.filtering = true
		return m, nil
	case "esc", "q":
		return m, tea.Quit
	case enterKey, "x", "c", "s", "N":
		return m.paneKey(msg.String())
	}
	return m, nil
}

// paneKey runs the action of key on the pane under the cursor. With no row shown it does nothing.
func (m model) paneKey(key string) (model, tea.Cmd) {
	cur, ok := m.current()
	if !ok {
		return m, nil
	}
	tty := m.clientTTY
	switch key {
	case enterKey:
		return m, m.action(func(ctx context.Context, client *tmuxctl.Client) error {
			return switchAndMark(ctx, client, tty, cur.ID)
		}, true)
	case "x":
		return m, m.action(func(ctx context.Context, client *tmuxctl.Client) error {
			return markSeen(ctx, client, cur.ID)
		}, false)
	case "c":
		return m, m.action(func(ctx context.Context, client *tmuxctl.Client) error {
			return newClaudeWindow(ctx, client, cur.Session, cur.Cwd)
		}, false)
	case "s":
		return m, m.action(func(ctx context.Context, client *tmuxctl.Client) error {
			return openShell(ctx, client, tty, cur.Cwd)
		}, true)
	case "N":
		m.result = Result{Action: ActionIntent, Dir: cur.Cwd}
		return m, tea.Quit
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
	if _, err := client.Run(ctx, "new-window", "-t", "="+session+":", "-c", dir, "claude"); err != nil {
		return fmt.Errorf("new claude window: %w", err)
	}
	return nil
}

// openShell shows the popup shell for dir on the client at tty. A popup on tty attaches the
// shell session for dir with the server's socket and TMUX unset, so that the attach is not
// nested inside the session the popup is drawn over. The session starts in dir, so the popup
// takes no directory, and dir stays out of the run-shell command.
func openShell(ctx context.Context, client *tmuxctl.Client, tty, dir string) error {
	name := shellSessionName(dir)
	if err := ensureShellSession(ctx, client, name, dir); err != nil {
		return err
	}
	out, err := client.Run(ctx, "display-message", "-p", "#{socket_path}")
	if err != nil {
		return fmt.Errorf("socket path: %w", err)
	}
	popup := tmuxCommand("display-popup", "-c", tty, "-E",
		"env", "-u", "TMUX", "tmux", "-S", strings.TrimSpace(out), "attach-session", "-t", "="+name)
	if _, err = client.Run(ctx, "run-shell", "-b", "-C", popup); err != nil {
		return fmt.Errorf("open popup shell: %w", err)
	}
	return nil
}

// ensureShellSession creates the detached session name in dir unless it already exists.
func ensureShellSession(ctx context.Context, client *tmuxctl.Client, name, dir string) error {
	if _, err := client.Run(ctx, "has-session", "-t", "="+name); err == nil {
		return nil
	}
	if _, err := client.Run(ctx, "new-session", "-d", "-s", name, "-c", dir); err != nil {
		return fmt.Errorf("new shell session: %w", err)
	}
	return nil
}

// shellSessionName returns the shell session name for dir: the shell prefix and the first
// twelve hex digits of the SHA-256 of dir.
func shellSessionName(dir string) string {
	sum := sha256.Sum256([]byte(dir))
	return protocol.ShellSessionPrefix + hex.EncodeToString(sum[:])[:12]
}

// tmuxCommand returns a tmux command string: name, then each argument as one quoted word, so
// that a space, a quote, or a $ in an argument cannot split or expand inside the command.
func tmuxCommand(name string, args ...string) string {
	words := make([]string, 0, 1+len(args))
	words = append(words, name)
	for _, arg := range args {
		words = append(words, tmuxWord(arg))
	}
	return strings.Join(words, " ")
}

// tmuxWord quotes value as one word of a tmux command string. Single quotes are literal in
// tmux, so a single quote inside value is written as '"'"', which joins to one word. Every '#'
// is doubled, because run-shell -C format-expands its command and "##" expands to "#", so the
// word reaches the command as written and no format in it runs.
func tmuxWord(value string) string {
	escaped := strings.ReplaceAll(value, "#", "##")
	return "'" + strings.ReplaceAll(escaped, "'", `'"'"'`) + "'"
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
