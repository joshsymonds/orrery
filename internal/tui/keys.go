package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/view"
)

// Cursor steps: one row down and one row up.
const (
	nextRow = 1
	prevRow = -1
)

// keyPressed applies a key press. Filter input takes every key. Otherwise the keys move the
// cursor, jump to the next unseen row, start filter input, or close the tree.
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
	}
	return m, nil
}

// filterKey edits the query. Esc clears it, Enter keeps it, Backspace deletes its last
// character, and any other typed text is appended.
func (m model) filterKey(msg tea.KeyPressMsg) (model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.filtering, m.query = false, ""
	case "enter":
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
