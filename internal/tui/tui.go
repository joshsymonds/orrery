// Package tui shows the pane tree and the selected pane's preview in a terminal.
package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
	"github.com/joshsymonds/orrery/internal/view"
)

// Source returns the panes to show.
type Source func(ctx context.Context) ([]protocol.Pane, error)

// Options configures Run.
type Options struct {
	Client    *tmuxctl.Client
	ClientTTY string
	Panes     Source
	Input     io.Reader
	Output    io.Writer
}

// Action is what the user asked for when Run returned.
type Action string

// Actions Run can return.
const (
	ActionClose  Action = "close"
	ActionIntent Action = "intent"
)

// Result is the outcome of Run.
type Result struct {
	Action Action
	Dir    string
}

// reloadInterval is how often the panes and the selected preview reload.
const reloadInterval = time.Second

// fallbackWidth and fallbackHeight size the screen when output is not a terminal. A terminal
// reports its own size and replaces them.
const (
	fallbackWidth  = 80
	fallbackHeight = 24
)

// day is the unit for ages of a day or more.
const day = 24 * time.Hour

// unsetMark stands for an unset state or an age with no change time.
const unsetMark = "-"

// Run shows the pane tree until ctx ends. It returns ActionClose once the program stops.
func Run(ctx context.Context, opts Options) (Result, error) {
	tree := newModel(
		func() ([]protocol.Pane, error) { return opts.Panes(ctx) },
		func(paneID string) (string, error) {
			return opts.Client.Run(ctx, "capture-pane", "-p", "-t", paneID)
		},
		time.Now,
	)
	program := tea.NewProgram(
		tree,
		tea.WithContext(ctx),
		tea.WithInput(opts.Input),
		tea.WithOutput(opts.Output),
		tea.WithWindowSize(fallbackWidth, fallbackHeight),
	)
	final, err := program.Run()
	if err != nil && !errors.Is(err, tea.ErrProgramKilled) {
		return Result{}, fmt.Errorf("run tree: %w", err)
	}
	done, ok := final.(model)
	if !ok {
		return Result{}, fmt.Errorf("run tree: final model is %T", final)
	}
	return done.result, nil
}

// model is the tree's state. Panes reload through source and previews through capture,
// both called from commands so that no blocking tmux call runs in Update.
type model struct {
	source     func() ([]protocol.Pane, error)
	capture    func(paneID string) (string, error)
	now        func() time.Time
	groups     []view.Group
	cursorID   string
	cursor     int
	query      string
	filtering  bool
	result     Result
	preview    string
	previewID  string
	sourceErr  error
	captureErr error
	width      int
	height     int
}

type (
	tickMsg  struct{}
	panesMsg struct {
		panes []protocol.Pane
		err   error
	}
	previewMsg struct {
		paneID string
		text   string
		err    error
	}
)

func newModel(
	source func() ([]protocol.Pane, error),
	capture func(string) (string, error),
	now func() time.Time,
) model {
	return model{source: source, capture: capture, now: now, result: Result{Action: ActionClose}}
}

// Init loads the panes and starts the reload timer.
func (m model) Init() tea.Cmd {
	return tea.Batch(m.loadPanes(), tick())
}

// Update applies key presses, window size, reload, and load results.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return m.keyPressed(msg)
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tickMsg:
		return m, tea.Batch(m.loadPanes(), tick())
	case panesMsg:
		return m.applyPanes(msg)
	case previewMsg:
		m.preview, m.previewID, m.captureErr = msg.text, msg.paneID, msg.err
		return m, nil
	}
	return m, nil
}

// View renders the current frame.
func (m model) View() tea.View {
	return tea.NewView(m.frame())
}

func tick() tea.Cmd {
	return tea.Tick(reloadInterval, func(time.Time) tea.Msg { return tickMsg{} })
}

// loadPanes reads the panes from source.
func (m model) loadPanes() tea.Cmd {
	return func() tea.Msg {
		panes, err := m.source()
		return panesMsg{panes: panes, err: err}
	}
}

// loadPreview captures paneID.
func (m model) loadPreview(paneID string) tea.Cmd {
	return func() tea.Msg {
		text, err := m.capture(paneID)
		return previewMsg{paneID: paneID, text: text, err: err}
	}
}

// applyPanes regroups the panes, keeps the cursor on its pane, and captures the preview of
// the row under the cursor. A source error keeps the previous tree and shows the error.
func (m model) applyPanes(msg panesMsg) (tea.Model, tea.Cmd) {
	m.sourceErr = msg.err
	if msg.err != nil {
		return m, nil
	}
	m.groups = view.Groups(msg.panes)
	m = m.sync()
	cur, ok := m.current()
	if !ok {
		m.preview, m.previewID, m.captureErr = "", "", nil
		return m, nil
	}
	return m, m.loadPreview(cur.ID)
}

// rows returns the visible panes in display order.
func (m model) rows() []protocol.Pane {
	var rows []protocol.Pane
	for _, g := range m.visible() {
		rows = append(rows, g.Panes...)
	}
	return rows
}

// current returns the pane under the cursor. The cursor starts on the first row.
func (m model) current() (protocol.Pane, bool) {
	rows := m.rows()
	if len(rows) == 0 {
		return protocol.Pane{}, false
	}
	return rows[m.locate(rows)], true
}

// locate returns the index in rows of the cursor: the row with the cursor's pane ID when
// that pane is still shown, else the cursor's index clamped to the last row. rows must not
// be empty.
func (m model) locate(rows []protocol.Pane) int {
	for i, p := range rows {
		if p.ID == m.cursorID {
			return i
		}
	}
	return min(m.cursor, len(rows)-1)
}

// sync records where the cursor is after the rows changed, so that a later reload can find
// its pane again or clamp to the same place.
func (m model) sync() model {
	rows := m.rows()
	if len(rows) == 0 {
		m.cursor, m.cursorID = 0, ""
		return m
	}
	i := m.locate(rows)
	m.cursor, m.cursorID = i, rows[i].ID
	return m
}

// frame renders the filter line, the groups, the selected preview, and an error line,
// clipped to the terminal.
func (m model) frame() string {
	cur, hasCur := m.current()
	var lines []string
	if m.filtering || m.query != "" {
		lines = append(lines, "/"+m.query)
	}
	if !hasCur {
		lines = append(lines, "No panes.")
	}
	for _, g := range m.visible() {
		lines = append(lines, g.Dir)
		for _, p := range g.Panes {
			lines = append(lines, m.row(p, p.ID == cur.ID))
		}
	}
	if hasCur && m.previewID == cur.ID && m.preview != "" {
		lines = append(lines, "")
		lines = append(lines, strings.Split(strings.TrimSuffix(m.preview, "\n"), "\n")...)
	}

	var status []string
	if err := m.statusErr(); err != nil {
		status = []string{"error: " + err.Error()}
	}
	if m.height > 0 {
		lines = clip(lines, m.height-len(status))
	}
	lines = append(lines, status...)
	for i, line := range lines {
		lines[i] = fit(line, m.width)
	}
	return strings.Join(lines, "\n")
}

// statusErr returns the error to show: a source error before a capture error.
func (m model) statusErr() error {
	if m.sourceErr != nil {
		return m.sourceErr
	}
	return m.captureErr
}

// row formats one pane: cursor, unseen marker, state, label, summary, and age.
func (m model) row(pane protocol.Pane, cursor bool) string {
	mark := "  "
	if cursor {
		mark = "> "
	}
	unseen := " "
	if view.Unseen(pane) {
		unseen = "*"
	}
	state := string(pane.State)
	if state == "" {
		state = unsetMark
	}
	return fmt.Sprintf("%s%s %-8s %-16s %-24s %s", mark, unseen, state, pane.Label, pane.Summary, m.age(pane.Changed))
}

// age returns how long ago the pane changed, in the largest whole unit.
func (m model) age(changed int64) string {
	if changed == 0 {
		return unsetMark
	}
	elapsed := max(m.now().Sub(time.Unix(changed, 0)), 0)
	switch {
	case elapsed < time.Minute:
		return fmt.Sprintf("%ds", int(elapsed/time.Second))
	case elapsed < time.Hour:
		return fmt.Sprintf("%dm", int(elapsed/time.Minute))
	case elapsed < day:
		return fmt.Sprintf("%dh", int(elapsed/time.Hour))
	default:
		return fmt.Sprintf("%dd", int(elapsed/day))
	}
}

// clip keeps the first n lines, or none when n is negative.
func clip(lines []string, n int) []string {
	if n < 0 {
		n = 0
	}
	if len(lines) > n {
		return lines[:n]
	}
	return lines
}

// fit truncates line to width runes. A width of 0 means the terminal size is not known yet.
func fit(line string, width int) string {
	runes := []rune(line)
	if width <= 0 || len(runes) <= width {
		return line
	}
	return string(runes[:width])
}
