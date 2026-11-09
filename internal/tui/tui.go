// Package tui shows the pane tree and the selected pane's preview in a terminal.
package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/joshsymonds/orrery/internal/closer"
	"github.com/joshsymonds/orrery/internal/keymap"
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
	// ArchivePath is the archive file a closed Claude pane is appended to.
	ArchivePath string
	Panes       Source
	Input       io.Reader
	Output      io.Writer
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

// quietRowID is the ID of the Quiet row in rows. It stands for the section's header, which is no
// pane. tmux pane IDs start with %, so no pane has this ID.
const quietRowID = "quiet"

// Run shows the pane tree until ctx ends. It returns ActionClose once the program stops.
func Run(ctx context.Context, opts Options) (Result, error) {
	tree := newModel(
		func() ([]protocol.Pane, error) { return opts.Panes(ctx) },
		func(paneID string) (string, error) {
			return opts.Client.Run(ctx, "capture-pane", "-p", "-t", paneID)
		},
		time.Now,
	)
	tree.clientTTY = opts.ClientTTY
	tree.archivePath = opts.ArchivePath
	tree.invoke = func(fn func(context.Context, *tmuxctl.Client) error) error {
		return fn(ctx, opts.Client)
	}
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

// closePrompt is the close the tree asks the user to confirm. asked is set once the second
// prompt for a working pane has been answered with y.
type closePrompt struct {
	plan  closer.Plan
	asked bool
}

// model is the tree's state. Panes reload through source and previews through capture,
// both called from commands so that no blocking tmux call runs in Update. Actions run through
// invoke, also from commands, against the invoking client at clientTTY.
type model struct {
	source      func() ([]protocol.Pane, error)
	capture     func(paneID string) (string, error)
	now         func() time.Time
	invoke      func(fn func(context.Context, *tmuxctl.Client) error) error
	clientTTY   string
	archivePath string
	closing     *closePrompt
	groups      []view.Group
	quiet       []view.Group
	quietOpen   bool
	cursorID    string
	cursor      int
	query       string
	filtering   bool
	help        bool
	result      Result
	preview     string
	previewID   string
	sourceErr   error
	captureErr  error
	actionErr   error
	width       int
	height      int
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
	// actionMsg reports a finished action. quit ends the tree when the action succeeds.
	actionMsg struct {
		err  error
		quit bool
	}
	// closePlanMsg reports the plan of a close the user asked for.
	closePlanMsg struct {
		plan closer.Plan
		err  error
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
	case actionMsg:
		return m.actionDone(msg)
	case closePlanMsg:
		return m.planned(msg), nil
	}
	return m, nil
}

// actionDone shows an action's failure on the status line and keeps the tree open. A success
// quits the tree when the action ends it, and otherwise reloads the panes so that a new pane
// shows.
func (m model) actionDone(msg actionMsg) (tea.Model, tea.Cmd) {
	m.actionErr = msg.err
	switch {
	case msg.err != nil:
		return m, nil
	case msg.quit:
		return m, tea.Quit
	default:
		return m, m.loadPanes()
	}
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

// applyPanes regroups the panes into active and quiet projects, keeps the cursor on its pane,
// and captures the preview of the row under the cursor. A source error keeps the previous tree
// and shows the error.
func (m model) applyPanes(msg panesMsg) (tea.Model, tea.Cmd) {
	m.sourceErr = msg.err
	if msg.err != nil {
		return m, nil
	}
	m.groups, m.quiet = view.Fold(view.Groups(msg.panes), m.now())
	m = m.sync()
	cur, ok := m.current()
	if !ok {
		m.preview, m.previewID, m.captureErr = "", "", nil
		return m, nil
	}
	return m, m.loadPreview(cur.ID)
}

// rows returns the visible panes in display order. When quiet projects are visible, the Quiet
// row follows the active panes, and the quiet panes follow it once the section is open.
func (m model) rows() []protocol.Pane {
	var rows []protocol.Pane
	for _, g := range m.visible(m.groups) {
		rows = append(rows, g.Panes...)
	}
	quiet := m.visible(m.quiet)
	if len(quiet) == 0 {
		return rows
	}
	rows = append(rows, protocol.Pane{ID: quietRowID})
	if m.quietOpen {
		for _, g := range quiet {
			rows = append(rows, g.Panes...)
		}
	}
	return rows
}

// cursorRow returns the row under the cursor, the Quiet row included. The cursor starts on the
// first row.
func (m model) cursorRow() (protocol.Pane, bool) {
	rows := m.rows()
	if len(rows) == 0 {
		return protocol.Pane{}, false
	}
	return rows[m.locate(rows)], true
}

// current returns the pane under the cursor. It returns false when no row is shown or the
// cursor is on the Quiet row.
func (m model) current() (protocol.Pane, bool) {
	cur, ok := m.cursorRow()
	if !ok || cur.ID == quietRowID {
		return protocol.Pane{}, false
	}
	return cur, true
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

// frame renders the filter line, the groups, the Quiet row, the selected preview, and an error
// line, clipped to the terminal. An open help overlay replaces all of it.
func (m model) frame() string {
	if m.help {
		lines := helpLines(m.width)
		if m.height > 0 {
			lines = clip(lines, m.height)
		}
		return render(lines, m.width)
	}
	cur, hasCur := m.cursorRow()
	var lines []string
	if m.filtering || m.query != "" {
		lines = append(lines, "/"+m.query)
	}
	if !hasCur {
		lines = append(lines, "No panes.")
	}
	lines = append(lines, m.groupLines(m.visible(m.groups), cur)...)
	if quiet := m.visible(m.quiet); len(quiet) > 0 {
		lines = append(lines, m.quietLine(len(quiet), cur.ID == quietRowID))
		if m.quietOpen {
			lines = append(lines, m.groupLines(quiet, cur)...)
		}
	}
	if pane, ok := m.current(); ok && m.previewID == pane.ID && m.preview != "" {
		lines = append(lines, "")
		lines = append(lines, strings.Split(strings.TrimSuffix(m.preview, "\n"), "\n")...)
	}

	var status []string
	if m.closing != nil {
		status = []string{m.closing.text()}
	} else if err := m.statusErr(); err != nil {
		status = []string{"error: " + err.Error()}
	}
	if m.height > 0 {
		lines = clip(lines, m.height-len(status))
	}
	lines = append(lines, status...)
	return render(lines, m.width)
}

// groupLines renders each group as its directory line and its pane rows. The row under cur is
// marked.
func (m model) groupLines(groups []view.Group, cur protocol.Pane) []string {
	var lines []string
	for _, g := range groups {
		lines = append(lines, g.Dir)
		for _, p := range g.Panes {
			lines = append(lines, m.row(p, p.ID == cur.ID))
		}
	}
	return lines
}

// quietLine formats the Quiet row: its count of quiet projects while the section is collapsed,
// a plain header once it is open. cursor marks the row.
func (m model) quietLine(count int, cursor bool) string {
	mark := "  "
	if cursor {
		mark = "> "
	}
	if m.quietOpen {
		return mark + "Quiet"
	}
	return fmt.Sprintf("%sQuiet (%d)", mark, count)
}

// helpGap is the space between a binding's keys and its help text.
const helpGap = 2

// helpLines lists every tree key in display order under a Keys title. Each binding's keys
// fill a column as wide as the longest of them, and its help text wraps to the width left
// beside that column, with continuation lines under the help text.
func helpLines(width int) []string {
	bindings := keymap.For(keymap.Tree)
	keyCol := 0
	for _, b := range bindings {
		keyCol = max(keyCol, len(strings.Join(b.Keys, ", ")))
	}
	indent := strings.Repeat(" ", keyCol+helpGap)
	lines := []string{"Keys"}
	for _, b := range bindings {
		wrapped := wrap(b.Help, width-len(indent))
		lines = append(lines, fmt.Sprintf("%-*s%s", keyCol+helpGap, strings.Join(b.Keys, ", "), wrapped[0]))
		for _, part := range wrapped[1:] {
			lines = append(lines, indent+part)
		}
	}
	return lines
}

// wrap breaks text into lines of at most width runes at its spaces. A word longer than width
// keeps its own line.
func wrap(text string, width int) []string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(text) {
		switch {
		case line == "":
			line = word
		case utf8.RuneCountInString(line)+1+utf8.RuneCountInString(word) <= width:
			line += " " + word
		default:
			lines = append(lines, line)
			line = word
		}
	}
	return append(lines, line)
}

// render fits each line to width and joins them into a frame.
func render(lines []string, width int) string {
	for i, line := range lines {
		lines[i] = fit(line, width)
	}
	return strings.Join(lines, "\n")
}

// statusErr returns the error to show: a source error, then a capture error, then the error of
// the last action.
func (m model) statusErr() error {
	switch {
	case m.sourceErr != nil:
		return m.sourceErr
	case m.captureErr != nil:
		return m.captureErr
	default:
		return m.actionErr
	}
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
