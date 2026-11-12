package tui

import (
	"context"
	"fmt"
	"io"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/joshsymonds/orrery/internal/cli"
	"github.com/joshsymonds/orrery/internal/closer"
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
// else. Filter input takes every key. Otherwise the key runs the action keymap binds to it: in
// the pools view poolsAction runs it, and on the tree treeAction does.
func (m model) keyPressed(msg tea.KeyPressMsg) (model, tea.Cmd) {
	if m.help {
		m.help = false
		return m, nil
	}
	if m.closing != nil {
		return m.closeKey(msg)
	}
	if m.filtering {
		return m.filterKey(msg)
	}
	action, ok := keymap.Lookup(keymap.Tree, msg.String())
	if !ok {
		return m, nil
	}
	if m.poolsShown {
		return m.poolsAction(action)
	}
	return m.treeAction(action)
}

// treeAction runs the action keymap binds to a key on the tree.
func (m model) treeAction(action keymap.Action) (model, tea.Cmd) {
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
	case keymap.ActPools:
		return m.togglePools()
	case keymap.ActSwitch:
		if m.onQuiet() {
			return m.expandQuiet()
		}
		return m.paneAction(action)
	case keymap.ActMarkSeen, keymap.ActNewClaude, keymap.ActShell, keymap.ActPicker, keymap.ActClosePane,
		keymap.ActCloseProject:
		return m.paneAction(action)
	case keymap.ActTree:
		// The tree has no behavior for it. It is a tmux binding.
		return m, nil
	}
	return m, nil
}

// poolsAction runs the action of a key press in the pools view. p returns to the panes, j and k
// move the cursor over the member rows, ? opens help, and q and Esc close the tree. Every other
// action does nothing there, since the pane actions have no row to act on.
func (m model) poolsAction(action keymap.Action) (model, tea.Cmd) {
	switch action {
	case keymap.ActDown:
		return m.poolsMove(nextRow), nil
	case keymap.ActUp:
		return m.poolsMove(prevRow), nil
	case keymap.ActPools:
		return m.togglePools()
	case keymap.ActHelp:
		m.help = true
		return m, nil
	case keymap.ActQuit:
		return m, tea.Quit
	case keymap.ActNextUnseen, keymap.ActFilter, keymap.ActSwitch, keymap.ActMarkSeen, keymap.ActNewClaude,
		keymap.ActShell, keymap.ActPicker, keymap.ActClosePane, keymap.ActCloseProject, keymap.ActTree:
		return m, nil
	}
	return m, nil
}

// onQuiet reports whether the cursor is on the Quiet row.
func (m model) onQuiet() bool {
	cur, ok := m.cursorRow()
	return ok && cur.ID == quietRowID
}

// expandQuiet opens the Quiet section and moves the cursor to its first pane. The section stays
// open, so Enter on its header does nothing once it is open.
func (m model) expandQuiet() (model, tea.Cmd) {
	if m.quietOpen {
		return m, nil
	}
	header := m.locate(m.rows())
	m.quietOpen = true
	return m.selectRow(m.rows(), header+1)
}

// paneAction runs action on the pane under the cursor. With no pane under the cursor it does
// nothing.
func (m model) paneAction(action keymap.Action) (model, tea.Cmd) {
	cur, ok := m.current()
	if !ok {
		return m, nil
	}
	tty := m.clientTTY
	report := m.reportSeen
	switch action {
	case keymap.ActSwitch:
		return m, m.action(func(ctx context.Context, client *tmuxctl.Client) error {
			return switchAndMark(ctx, client, tty, cur.ID, report)
		}, true)
	case keymap.ActMarkSeen:
		return m, m.action(func(ctx context.Context, client *tmuxctl.Client) error {
			return markSeen(ctx, client, cur.ID, report)
		}, false)
	case keymap.ActNewClaude:
		return m, m.action(func(ctx context.Context, client *tmuxctl.Client) error {
			return newClaudeWindow(ctx, client, cur.Session, cur.Cwd)
		}, false)
	case keymap.ActShell:
		return m, m.action(func(ctx context.Context, client *tmuxctl.Client) error {
			return shell.Open(ctx, client, cur.Cwd, tty)
		}, true)
	case keymap.ActClosePane:
		return m, m.planClose(cur, false)
	case keymap.ActCloseProject:
		return m, m.planClose(cur, true)
	case keymap.ActPicker:
		m.result = Result{Action: ActionIntent, Dir: cur.Cwd}
		return m, tea.Quit
	case keymap.ActDown, keymap.ActUp, keymap.ActNextUnseen, keymap.ActFilter, keymap.ActHelp, keymap.ActQuit,
		keymap.ActTree, keymap.ActPools:
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

// planClose plans the close of cur in a command. A project close plans the project in cur's
// directory; otherwise the close plans cur's pane. The plan returns as a closePlanMsg.
func (m model) planClose(cur protocol.Pane, project bool) tea.Cmd {
	tty := m.clientTTY
	invoke := m.invoke
	return func() tea.Msg {
		var plan closer.Plan
		err := invoke(func(ctx context.Context, client *tmuxctl.Client) error {
			var planErr error
			if project {
				plan, planErr = closer.PlanProject(ctx, client, cur.Cwd, tty)
			} else {
				plan, planErr = closer.PlanPane(ctx, client, cur.ID, tty)
			}
			if planErr != nil {
				return fmt.Errorf("plan close: %w", planErr)
			}
			return nil
		})
		return closePlanMsg{plan: plan, project: project, err: err}
	}
}

// planned opens the close prompt for a planned close. A planning error goes to the status line
// and opens no prompt.
func (m model) planned(msg closePlanMsg) model {
	if msg.err != nil {
		m.actionErr = msg.err
		return m
	}
	m.closing = &closePrompt{plan: msg.plan, project: msg.project}
	return m
}

// closeKey answers the close prompt. y confirms the close, or asks the second time when the pane
// is working. Any other key cancels, and closes nothing.
func (m model) closeKey(msg tea.KeyPressMsg) (model, tea.Cmd) {
	prompt := m.closing
	m.closing = nil
	if msg.String() != "y" {
		return m, nil
	}
	if prompt.working() && !prompt.asked {
		prompt.asked = true
		m.closing = prompt
		return m, nil
	}
	return m, m.closePane(prompt.plan)
}

// closePane closes the planned panes in a command. The tree stays open, and reloads its panes
// once the close succeeds.
func (m model) closePane(plan closer.Plan) tea.Cmd {
	archivePath := m.archivePath
	now := m.now()
	return m.action(func(ctx context.Context, client *tmuxctl.Client) error {
		return closer.Close(ctx, client, plan, archivePath, now)
	}, false)
}

// switchAndMark switches the client at tty to paneID, then marks the pane seen. A pane is
// marked only once the client has been switched to it.
func switchAndMark(ctx context.Context, client *tmuxctl.Client, tty, paneID string, report cli.SeenReporter) error {
	if _, err := client.Run(ctx, "switch-client", "-c", tty, "-t", paneID); err != nil {
		return fmt.Errorf("switch client: %w", err)
	}
	return markSeen(ctx, client, paneID, report)
}

// markSeen sets the @orrery_seen option of paneID to the current time, then reports the pane and
// that time to orreryd through report. A nil report marks the pane and reports nothing.
func markSeen(ctx context.Context, client *tmuxctl.Client, paneID string, report cli.SeenReporter) error {
	if err := cli.Seen(ctx, client, report, []string{"--pane", paneID}, io.Discard); err != nil {
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

// selectRow puts the cursor on rows[i] and captures its preview. The Quiet row has no preview.
func (m model) selectRow(rows []protocol.Pane, i int) (model, tea.Cmd) {
	m.cursor, m.cursorID = i, rows[i].ID
	if rows[i].ID == quietRowID {
		return m, nil
	}
	return m, m.loadPreview(rows[i].ID)
}

// visible returns the given groups shown under the query, in their display order. A row is
// shown when its label, summary, or session, or its group's Dir, contains the query, ignoring
// case. A group with no shown row is dropped.
func (m model) visible(groups []view.Group) []view.Group {
	needle := strings.ToLower(m.query)
	var shown []view.Group
	for _, group := range groups {
		if strings.Contains(strings.ToLower(group.Dir), needle) {
			shown = append(shown, group)
			continue
		}
		var panes []protocol.Pane
		for _, pane := range group.Panes {
			if matches(pane, needle) {
				panes = append(panes, pane)
			}
		}
		if len(panes) > 0 {
			shown = append(shown, view.Group{Dir: group.Dir, Panes: panes})
		}
	}
	return shown
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

// workingPanes returns the planned panes in the working state.
func (c *closePrompt) workingPanes() []protocol.Pane {
	var working []protocol.Pane
	for _, pane := range c.plan.Panes {
		if pane.State == protocol.StateWorking {
			working = append(working, pane)
		}
	}
	return working
}

// working reports whether any planned pane is working.
func (c *closePrompt) working() bool {
	return len(c.workingPanes()) > 0
}

// lines is the close prompt, one entry per line. A pane close is the prompt line. A project close
// names the directory, each pane it closes, and each pane in the directory that stays open, then
// says where the invoking client goes, and asks. Its second prompt names the working panes.
func (c *closePrompt) lines() []string {
	if !c.project {
		return []string{c.text()}
	}
	if c.asked {
		var names []string
		for _, pane := range c.workingPanes() {
			names = append(names, paneName(pane))
		}
		return []string{"Working: " + strings.Join(names, ", ") + ".", "Close the project anyway? y/N"}
	}
	lines := []string{"Close the project at:", "  " + c.plan.Dir}
	for _, pane := range c.plan.Panes {
		lines = append(lines, "  close "+paneName(pane))
	}
	for _, pane := range c.plan.Kept {
		lines = append(lines, "  stays open: "+paneName(pane))
	}
	switch {
	case c.plan.MoveTo != "":
		lines = append(lines, "The client moves to "+c.plan.MoveTo+".")
	case c.plan.Detach:
		lines = append(lines, "The client detaches.")
	}
	return append(lines, "Close these panes? y/N")
}

// paneName names a pane by its session, and by its label when it has one.
func paneName(pane protocol.Pane) string {
	if pane.Label == "" {
		return pane.Session
	}
	return pane.Session + " (" + pane.Label + ")"
}

// text is the prompt line of a pane close. It names the planned pane's label, asks a second time
// when the pane is working, and says where the invoking client goes.
func (c *closePrompt) text() string {
	label := c.plan.Panes[0].Label
	question := "Close " + label + "?"
	if c.asked {
		question = label + " is working. Close it?"
	}
	switch {
	case c.plan.MoveTo != "":
		question += " The client moves to " + c.plan.MoveTo + "."
	case c.plan.Detach:
		question += " The client detaches."
	}
	return question + " y/N"
}
