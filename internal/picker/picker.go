// Package picker shows the actions open.Intent ranks for a text, and runs the one the user confirms.
package picker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"

	"github.com/joshsymonds/orrery/internal/open"
)

// Options configures Run. Text is what open.Intent ranks actions for; "" asks for it first. Dir, when set, is the
// directory the first action opens a Claude pane in. Input is the terminal the keys are read from and must not be
// nil; Output is where the prompt and the list are drawn.
type Options struct {
	Config open.Config
	Text   string
	Dir    string
	Input  *os.File
	Output io.Writer
}

// drainBufferSize is the size of each read that discards queued input.
const drainBufferSize = 256

// The key names the prompt and the list act on.
const (
	keyEnter = "enter"
	keyEsc   = "esc"
)

// Run asks open.Intent for the actions for the text and shows them, best first, with the first one selected. An
// empty opts.Text is asked for first, on a one-line prompt; Esc there returns false before the model runs. Enter
// runs the selected action and returns its Target with true. Esc and q return false and run nothing. Keys queued on
// a terminal before the prompt or the list is drawn are discarded, so none of them reaches either.
func Run(ctx context.Context, opts Options) (open.Target, bool, error) {
	text := opts.Text
	if text == "" {
		if err := discardQueued(opts.Input); err != nil {
			return open.Target{}, false, err
		}
		var submitted bool
		var err error
		text, submitted, err = askText(ctx, opts)
		if err != nil || !submitted {
			return open.Target{}, false, err
		}
	}
	actions, err := open.Intent(ctx, opts.Config, text)
	if err != nil {
		return open.Target{}, false, fmt.Errorf("picker: %w", err)
	}
	if drainErr := discardQueued(opts.Input); drainErr != nil {
		return open.Target{}, false, drainErr
	}
	execute := func(action open.Action) (open.Target, error) {
		return open.ExecuteAction(ctx, opts.Config, action)
	}
	program := tea.NewProgram(
		newModel(withDir(actions, opts.Dir, text), execute),
		tea.WithContext(ctx),
		tea.WithInput(opts.Input),
		tea.WithOutput(opts.Output),
	)
	final, err := program.Run()
	if err != nil && !errors.Is(err, tea.ErrProgramKilled) {
		return open.Target{}, false, fmt.Errorf("picker: run: %w", err)
	}
	done, ok := final.(model)
	if !ok {
		return open.Target{}, false, fmt.Errorf("picker: final model is %T", final)
	}
	return done.target, done.chosen, done.err
}

// askText shows the text prompt and returns what was typed. submitted is false when Esc ended the prompt.
func askText(ctx context.Context, opts Options) (string, bool, error) {
	program := tea.NewProgram(
		textModel{},
		tea.WithContext(ctx),
		tea.WithInput(opts.Input),
		tea.WithOutput(opts.Output),
	)
	final, err := program.Run()
	if err != nil && !errors.Is(err, tea.ErrProgramKilled) {
		return "", false, fmt.Errorf("picker: ask text: %w", err)
	}
	done, ok := final.(textModel)
	if !ok {
		return "", false, fmt.Errorf("picker: final text model is %T", final)
	}
	return done.text, done.submitted, nil
}

// withDir returns actions with the Dir pane first. Its prompt is text when any action has a prompt, and "" otherwise.
// An action that opens a pane in dir is dropped from the rest. With no dir, actions is returned as it is.
func withDir(actions []open.Action, dir, text string) []open.Action {
	if dir == "" {
		return actions
	}
	var prompt string
	if slices.ContainsFunc(actions, func(action open.Action) bool { return action.Prompt != "" }) {
		prompt = text
	}
	out := []open.Action{{Kind: open.ActionPane, Dir: dir, Prompt: prompt}}
	for _, action := range actions {
		if action.Kind == open.ActionPane && action.Dir == dir {
			continue
		}
		out = append(out, action)
	}
	return out
}

// discardQueued drops the keys queued on in when it is a terminal.
func discardQueued(in *os.File) error {
	if !term.IsTerminal(in.Fd()) {
		return nil
	}
	if err := discardInput(in); err != nil {
		return fmt.Errorf("picker: discard queued input: %w", err)
	}
	return nil
}

// discardInput drops every byte queued on the terminal in. The terminal leaves line mode for the read, so a
// partly typed line is dropped too, and its previous state is restored after the read.
func discardInput(in *os.File) error {
	fd := in.Fd()
	state, err := term.MakeRaw(fd)
	if err != nil {
		return fmt.Errorf("raw mode: %w", err)
	}
	drainErr := drain(int(fd))
	restoreErr := term.Restore(fd, state)
	return errors.Join(drainErr, restoreErr)
}

// drain reads fd until no byte is queued. It sets fd non-blocking for the read and restores blocking mode after.
func drain(fd int) error {
	if err := syscall.SetNonblock(fd, true); err != nil {
		return fmt.Errorf("non-blocking: %w", err)
	}
	readErr := readQueued(fd)
	return errors.Join(readErr, syscall.SetNonblock(fd, false))
}

// readQueued reads and drops bytes from non-blocking fd until a read finds none.
func readQueued(fd int) error {
	buf := make([]byte, drainBufferSize)
	for {
		n, err := syscall.Read(fd, buf)
		switch {
		case errors.Is(err, syscall.EINTR):
		case errors.Is(err, syscall.EAGAIN):
			return nil
		case err != nil:
			return fmt.Errorf("read: %w", err)
		case n == 0:
			return nil
		}
	}
}

// textModel is the text prompt: the text typed so far, and whether Enter submitted it.
type textModel struct {
	text      string
	submitted bool
}

// Init has no startup command.
func (m textModel) Init() tea.Cmd {
	return nil
}

// Update types into the text, submits a non-empty text on Enter, and ends the prompt on Esc.
func (m textModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case keyEnter:
		if m.text == "" {
			return m, nil
		}
		m.submitted = true
		return m, tea.Quit
	case keyEsc:
		return m, tea.Quit
	}
	m.text = typed(m.text, key)
	return m, nil
}

// View draws the prompt and the text typed so far.
func (m textModel) View() tea.View {
	return tea.NewView("text: " + m.text)
}

// typed returns line after key: Backspace drops its last character, and a key with text appends that text.
func typed(line string, key tea.KeyPressMsg) string {
	if key.String() == "backspace" {
		runes := []rune(line)
		if len(runes) == 0 {
			return line
		}
		return string(runes[:len(runes)-1])
	}
	return line + key.Text
}

// model is the list: the actions in rank order, the cursor, the project edit, and what the run returned.
type model struct {
	actions []open.Action
	execute func(open.Action) (open.Target, error)
	cursor  int
	edit    projectEdit
	running bool
	chosen  bool
	target  open.Target
	err     error
}

// projectEdit is the open edit of a project row: the name and root typed so far, and whether the root is the field
// being typed.
type projectEdit struct {
	active bool
	onRoot bool
	name   string
	root   string
}

// field draws the field being typed.
func (e projectEdit) field() string {
	if e.onRoot {
		return "root: " + e.root
	}
	return "name: " + e.name
}

// executed is the result of running the selected action.
type executed struct {
	target open.Target
	err    error
}

func newModel(actions []open.Action, execute func(open.Action) (open.Target, error)) model {
	return model{actions: actions, execute: execute}
}

// Init has no startup command.
func (m model) Init() tea.Cmd {
	return nil
}

// Update applies key presses and the result of the run.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return m.keyPressed(msg)
	case executed:
		m.chosen, m.target, m.err = true, msg.target, msg.err
		return m, tea.Quit
	}
	return m, nil
}

// keyPressed moves the cursor, edits the project row under it, runs the selected action, or quits without running
// anything. Once an action is running, every key is ignored, so the run ends only with its result.
func (m model) keyPressed(msg tea.KeyPressMsg) (model, tea.Cmd) {
	if m.running {
		return m, nil
	}
	if m.edit.active {
		return m.editKeyPressed(msg), nil
	}
	switch msg.String() {
	case "j", "down":
		m.cursor = min(m.cursor+1, len(m.actions)-1)
	case "k", "up":
		m.cursor = max(m.cursor-1, 0)
	case "e":
		return m.beginEdit(), nil
	case keyEnter:
		return m.run()
	case keyEsc, "q":
		return m, tea.Quit
	}
	return m, nil
}

// beginEdit opens the name and root of the project row under the cursor. Any other row has nothing to edit.
func (m model) beginEdit() model {
	action := m.actions[m.cursor]
	if action.Kind != open.ActionProject {
		return m
	}
	m.edit = projectEdit{active: true, name: action.Name, root: action.Dir}
	return m
}

// editKeyPressed types into the field being edited. Enter confirms the name and then the root, and the second
// confirmation sets the row's name and root. Esc abandons the edit and keeps the row's action.
func (m model) editKeyPressed(msg tea.KeyPressMsg) model {
	switch msg.String() {
	case keyEsc:
		m.edit = projectEdit{}
	case keyEnter:
		if !m.edit.onRoot {
			m.edit.onRoot = true
			return m
		}
		action := &m.actions[m.cursor]
		action.Name, action.Dir = m.edit.name, m.edit.root
		m.edit = projectEdit{}
	default:
		if m.edit.onRoot {
			m.edit.root = typed(m.edit.root, msg)
		} else {
			m.edit.name = typed(m.edit.name, msg)
		}
	}
	return m
}

// run executes the selected action in a command, so the program keeps drawing while tmux works.
func (m model) run() (model, tea.Cmd) {
	m.running = true
	action := m.actions[m.cursor]
	execute := m.execute
	return m, func() tea.Msg {
		target, err := execute(action)
		return executed{target: target, err: err}
	}
}

// View draws one row per action, marking the selected row, and the field being edited under them.
func (m model) View() tea.View {
	lines := make([]string, 0, len(m.actions)+1)
	for i, action := range m.actions {
		mark := "  "
		if i == m.cursor {
			mark = "> "
		}
		lines = append(lines, mark+describe(action))
	}
	if m.edit.active {
		lines = append(lines, m.edit.field())
	}
	return tea.NewView(strings.Join(lines, "\n"))
}

// describe names an action by its kind with its pane, directory, or name and root, and marks an action with a
// prompt.
func describe(action open.Action) string {
	var text string
	switch action.Kind {
	case open.ActionJump:
		text = "jump " + action.Pane
	case open.ActionPane:
		text = "pane " + action.Dir
	case open.ActionProject:
		text = "project " + action.Name + " in " + action.Dir
	default:
		text = string(action.Kind)
	}
	if action.Prompt != "" {
		text += " (prompt)"
	}
	return text
}
