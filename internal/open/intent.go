package open

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/summary"
)

// ActionKind is the kind of an Intent action.
type ActionKind string

// The action kinds: jump switches to an existing pane, pane opens a new Claude pane in a directory, and project
// opens a new project directory with a Claude session.
const (
	ActionJump    ActionKind = "jump"
	ActionPane    ActionKind = "pane"
	ActionProject ActionKind = "project"
)

// Action is one choice Intent returns.
type Action struct {
	Kind   ActionKind
	Pane   string // jump: the pane ID
	Dir    string // pane: the directory; project: the root directory
	Name   string // project: the new project's name
	Prompt string // pane and project: TEXT as the first prompt when the model calls it a task, else ""
}

// intentDirLimit is the number of zoxide directories Intent sends the model.
const intentDirLimit = 30

// pairWords is the word count of a keyword and one argument: "jump <pane id>", "pane d<N>", "task: yes".
// newWords is the word count of "new <name> <root>", and keywordNew is the project keyword.
const (
	pairWords  = 2
	newWords   = 3
	keywordNew = "new"
)

// intentKillGrace bounds how long a run waits for its output pipes once the child has been killed.
const intentKillGrace = 2 * time.Second

// anthropicBaseURLPrefix is the environment entry Intent never passes: it runs the default context.
const anthropicBaseURLPrefix = "ANTHROPIC_BASE_URL="

// intentInstruction states the reply grammar. TEXT, the directory lines, and the pane lines follow it on stdin.
const intentInstruction = `Choose the actions that fit TEXT below and rank them best first.
Reply with one line per action, using only these forms:
jump <pane id>
pane d<N>
new <name> d<N>
new <name> default
Use only the pane ids and directory numbers listed below. "new" starts a project
named <name> under directory d<N>, or under the default directory. Then reply with
exactly one line, "task: yes" if TEXT asks for a task to start in a new pane, or
"task: no" if it does not. Reply with nothing else.
`

// Intent asks the summary model which actions fit text and returns them ranked, best first. It executes nothing.
func Intent(ctx context.Context, cfg Config, text string) ([]Action, error) {
	dirs, err := queryZoxide(ctx, cfg)
	if err != nil {
		return nil, err
	}
	dirs = dirs[:min(len(dirs), intentDirLimit)]
	panes, err := intentPanes(ctx, cfg)
	if err != nil {
		return nil, err
	}
	reply, err := runClaude(ctx, cfg.Claude, intentStdin(text, dirs, panes), summary.DefaultTimeout)
	if err != nil {
		return nil, err
	}
	actions := parseIntent(reply, cfg.Root, dirs, panes, text)
	if len(actions) == 0 {
		return nil, errors.New("intent: the model chose no valid action")
	}
	return actions, nil
}

// intentPanes returns the panes Intent may name: those with a state whose @orrery_context is set to "", the
// default context. An unset @orrery_context is a pane summarized before orreryd recorded contexts, so it is not
// eligible. With no tmux server there are none.
func intentPanes(ctx context.Context, cfg Config) ([]protocol.Pane, error) {
	all, err := cfg.Tmux.ListPanes(ctx)
	if err != nil {
		if strings.Contains(err.Error(), "no server running") || strings.Contains(err.Error(), "error connecting to") {
			return nil, nil
		}
		return nil, fmt.Errorf("intent: list panes: %w", err)
	}
	var panes []protocol.Pane
	for _, pane := range all {
		if pane.State == "" {
			continue
		}
		name, ok, optionErr := cfg.Tmux.PaneOption(ctx, pane.ID, protocol.OptionContext)
		if optionErr != nil {
			// A pane that closed after ListPanes is no longer a candidate; any other error fails the lookup.
			if strings.Contains(optionErr.Error(), "can't find pane") {
				continue
			}
			return nil, fmt.Errorf("intent: pane %s context: %w", pane.ID, optionErr)
		}
		if ok && name == "" {
			panes = append(panes, pane)
		}
	}
	return panes, nil
}

// intentStdin is the instruction, then TEXT, then the numbered directory basenames and the pane lines.
func intentStdin(text string, dirs []string, panes []protocol.Pane) string {
	var out strings.Builder
	out.WriteString(intentInstruction)
	out.WriteString("\nTEXT:\n" + text + "\n")
	out.WriteString("\nDIRECTORIES:\n")
	for i, dir := range dirs {
		fmt.Fprintf(&out, "d%d %s\n", i+1, filepath.Base(dir))
	}
	out.WriteString("\nPANES:\n")
	for _, p := range panes {
		fmt.Fprintf(&out, "%s %s: %s\n", p.ID, p.Label, p.Summary)
	}
	return out.String()
}

// parseIntent turns the reply into actions in reply order. A line with the wrong shape, an unlisted pane or
// directory, or a project name checkName rejects is dropped. "task: yes" anywhere in the reply sets a prompt.
func parseIntent(reply, root string, dirs []string, panes []protocol.Pane, text string) []Action {
	task := false
	var choices [][]string
	for _, line := range strings.Split(reply, "\n") {
		fields := strings.Fields(line)
		if len(fields) == pairWords && strings.EqualFold(fields[0], "task:") {
			task = task || strings.EqualFold(fields[1], "yes")
			continue
		}
		choices = append(choices, fields)
	}
	var actions []Action
	for _, fields := range choices {
		action, ok := intentAction(fields, root, dirs, panes)
		if !ok {
			continue
		}
		if task && action.Kind != ActionJump {
			action.Prompt = text
		}
		actions = append(actions, action)
	}
	return actions
}

// intentAction reads one reply line's fields as an action; ok is false when the line names nothing valid.
func intentAction(fields []string, root string, dirs []string, panes []protocol.Pane) (Action, bool) {
	if len(fields) == 0 {
		return Action{}, false
	}
	switch strings.ToLower(fields[0]) {
	case "jump":
		if len(fields) != pairWords || !hasPane(panes, fields[1]) {
			return Action{}, false
		}
		return Action{Kind: ActionJump, Pane: fields[1]}, true
	case "pane":
		if len(fields) != pairWords {
			return Action{}, false
		}
		dir, ok := dirRef(fields[1], dirs)
		return Action{Kind: ActionPane, Dir: dir}, ok
	case keywordNew:
		if len(fields) != newWords || checkName(fields[1]) != nil {
			return Action{}, false
		}
		dir, ok := projectRoot(fields[2], root, dirs)
		return Action{Kind: ActionProject, Name: fields[1], Dir: dir}, ok
	}
	return Action{}, false
}

// hasPane reports whether panes includes the pane with ID id.
func hasPane(panes []protocol.Pane, id string) bool {
	return slices.ContainsFunc(panes, func(p protocol.Pane) bool { return p.ID == id })
}

// dirRef returns the zoxide directory ref names, written d<N> with N counting from 1.
func dirRef(ref string, dirs []string) (string, bool) {
	ref = strings.ToLower(ref)
	if !strings.HasPrefix(ref, "d") {
		return "", false
	}
	n, err := strconv.Atoi(ref[1:])
	if err != nil || n < 1 || n > len(dirs) {
		return "", false
	}
	return dirs[n-1], true
}

// projectRoot returns the parent directory a new project goes under: cfg.Root for default, else the directory
// ref names. An empty or relative cfg.Root is not a directory, as in open.go's resolveNew.
func projectRoot(ref, root string, dirs []string) (string, bool) {
	if strings.EqualFold(ref, "default") {
		return root, filepath.IsAbs(root)
	}
	return dirRef(ref, dirs)
}

// runClaude runs the claude binary once with stdin, in cwd "/" with the summary argv and env, and returns its
// stdout. The run is killed with its process group when timeout elapses.
func runClaude(ctx context.Context, claude, stdin string, timeout time.Duration) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, claude, intentArgv()...)
	cmd.Dir = "/"
	cmd.Env = intentEnv(os.Environ())
	cmd.Stdin = strings.NewReader(stdin)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = intentKillGrace

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("intent: claude run: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// intentArgv is the argv after the claude binary for the default summary model: hooks disabled, no session
// saved, no tools, one turn.
func intentArgv() []string {
	return []string{
		"-p", "--model", protocol.DefaultSummaryModel,
		"--settings", `{"disableAllHooks":true}`,
		"--no-session-persistence",
		"--tools", "",
		"--max-turns", "1",
	}
}

// intentEnv returns environ without ANTHROPIC_BASE_URL and ORRERY_SUMMARIZER, plus the summarizer sentinel.
func intentEnv(environ []string) []string {
	env := make([]string, 0, len(environ)+1)
	for _, entry := range environ {
		if strings.HasPrefix(entry, anthropicBaseURLPrefix) || strings.HasPrefix(entry, protocol.EnvSummarizer+"=") {
			continue
		}
		env = append(env, entry)
	}
	return append(env, protocol.EnvSummarizer+"="+protocol.SummarizerValue)
}

// ExecuteAction carries out action and returns the Target to attach. A non-empty Prompt becomes the first
// prompt of the Claude pane the action opens. The caller chooses the action; nothing else runs.
func ExecuteAction(ctx context.Context, cfg Config, action Action) (Target, error) {
	switch action.Kind {
	case ActionJump:
		return jumpTo(ctx, cfg, action.Pane)
	case ActionPane:
		return openPane(ctx, cfg, action)
	case ActionProject:
		return newProject(ctx, cfg, action)
	default:
		return Target{}, fmt.Errorf("intent: unknown action kind %q", action.Kind)
	}
}

// jumpTo returns the Target for pane, which must exist. tmux prints an empty session name for a pane that
// does not exist, and session names are never empty.
func jumpTo(ctx context.Context, cfg Config, pane string) (Target, error) {
	out, err := cfg.Tmux.Run(ctx, "display-message", "-p", "-t", pane, "#{session_name}")
	if err != nil {
		return Target{}, fmt.Errorf("intent: pane %q: %w", pane, err)
	}
	session := strings.TrimSuffix(out, "\n")
	if session == "" {
		return Target{}, fmt.Errorf("intent: pane %q does not exist", pane)
	}
	return Target{Session: session, Pane: pane}, nil
}

// openPane opens a Claude pane in dir: a new window in the session for dir when one exists, else a new session.
func openPane(ctx context.Context, cfg Config, action Action) (Target, error) {
	if action.Dir == "" {
		return Target{}, errors.New("intent: pane needs a directory")
	}
	sessions, err := listSessions(ctx, cfg)
	if err != nil {
		return Target{}, err
	}
	plan := directoryPlan(sessions, action.Dir)
	if plan.Dir != "" {
		err = createSession(ctx, cfg, plan.Session, action.Dir, action.Prompt)
		if err != nil {
			return Target{}, err
		}
		return Target{Session: plan.Session}, nil
	}
	pane, err := newWindow(ctx, cfg, plan.Session, action.Dir, action.Prompt)
	if err != nil {
		return Target{}, err
	}
	return Target{Session: plan.Session, Pane: pane}, nil
}

// newWindow opens a Claude pane in a new window of session, in dir, and returns the new pane's ID.
func newWindow(ctx context.Context, cfg Config, session, dir, prompt string) (string, error) {
	args := slices.Concat(
		[]string{"new-window", "-P", "-F", "#{pane_id}", "-t", "=" + session + ":", "-c", tmuxDirArg(dir)},
		claudeCommand(cfg.Claude, prompt),
	)
	out, err := cfg.Tmux.Run(ctx, args...)
	if err != nil {
		return "", fmt.Errorf("intent: new window in %q: %w", session, err)
	}
	return strings.TrimSuffix(out, "\n"), nil
}

// newProject creates the directory root/name, which must not exist, and a session for it.
func newProject(ctx context.Context, cfg Config, action Action) (Target, error) {
	if err := checkName(action.Name); err != nil {
		return Target{}, err
	}
	if !filepath.IsAbs(action.Dir) {
		return Target{}, fmt.Errorf("intent: project root %q must be an absolute directory", action.Dir)
	}
	sessions, err := listSessions(ctx, cfg)
	if err != nil {
		return Target{}, err
	}
	dir := filepath.Join(action.Dir, action.Name)
	err = os.Mkdir(dir, 0o750)
	if err != nil {
		return Target{}, fmt.Errorf("intent: create directory: %w", err)
	}
	name := freeName(sessions, action.Name)
	err = createSession(ctx, cfg, name, dir, action.Prompt)
	if err != nil {
		return Target{}, err
	}
	return Target{Session: name}, nil
}
