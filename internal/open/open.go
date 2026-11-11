// Package open resolves `orrery open` arguments to tmux sessions and executes the chosen action.
package open

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/joshsymonds/orrery/internal/archive"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// Config holds the tmux client and the binary paths and defaults that Resolve needs.
type Config struct {
	Tmux    *tmuxctl.Client
	Zoxide  string // path of the zoxide binary
	Claude  string // path of the claude binary run in created panes and by the intent lookup
	Root    string // default parent directory of new projects
	Archive string // path of the archive file of closed Claude panes
	Home    string // the user's home directory; intent names directories strictly inside it relative to it
	Roots   []Root // directory trees the intent lookup lists below zoxide's directories, in order
}

// Request is one `orrery open` invocation.
type Request struct {
	Args   []string // words after `open`
	Cwd    string   // working directory of the invoking process
	Remote bool     // open runs through a remote host, so the current directory is not the user's
}

// Plan is what `orrery open` will do. Resolve creates nothing; the caller acts on the Plan.
type Plan struct {
	Session string // session to attach; with Dir set, the name of the session to create
	Dir     string // directory of the session to create; "" when Session exists
	MakeDir bool   // Dir itself is created first (new)
	Tree    bool   // the caller shows the tree after attaching (no argument)
	Text    string // no fast match: TEXT for the intent resolver; every other field is zero
}

// Target is what the caller attaches after Execute.
type Target struct {
	Session string // session to attach
	Pane    string // pane to select, "" for the session's current pane
	Tree    bool   // the caller shows the tree after attaching
}

// Execute carries out plan and returns the Target the caller attaches. A plan without Dir names an
// existing session. A plan with Dir creates the session, and creates Dir first when MakeDir. A Text
// plan is an error that creates nothing.
func Execute(ctx context.Context, cfg Config, plan Plan) (Target, error) {
	switch {
	case plan.Text != "":
		return Target{}, errors.New("open: text is not a session or directory to execute")
	case plan.Dir == "":
		return Target{Session: plan.Session, Tree: plan.Tree}, nil
	}
	if plan.MakeDir {
		if err := os.Mkdir(plan.Dir, 0o750); err != nil {
			return Target{}, fmt.Errorf("open: create directory: %w", err)
		}
	}
	if err := createSession(ctx, cfg, plan.Session, plan.Dir, claudeCommand(cfg.Claude, "")); err != nil {
		return Target{}, err
	}
	return Target{Session: plan.Session}, nil
}

// ResumeActions returns the picker rows for dir when it has no live session: one resume action per archived Claude
// session for dir whose transcript still exists, newest first; one continue action when there is none; then one
// fresh action. It reads cfg.Archive and executes nothing.
func ResumeActions(cfg Config, dir string) ([]Action, error) {
	if cfg.Archive == "" {
		return nil, errors.New("open: resume actions need an archive path")
	}
	records, err := archive.ForDir(cfg.Archive, dir)
	if err != nil {
		return nil, fmt.Errorf("open: resume actions: %w", err)
	}
	var actions []Action
	resumed := make(map[string]bool)
	for _, record := range records {
		if record.ClaudeSession == "" || resumed[record.ClaudeSession] {
			continue
		}
		exists, checkErr := transcriptExists(record.Transcript)
		if checkErr != nil {
			return nil, checkErr
		}
		if !exists {
			continue
		}
		resumed[record.ClaudeSession] = true
		actions = append(actions, Action{
			Kind: ActionResume, Dir: dir, Session: record.ClaudeSession, Note: record.Summary,
		})
	}
	if len(actions) == 0 {
		actions = append(actions, Action{Kind: ActionContinue, Dir: dir})
	}
	return append(actions, Action{Kind: ActionFresh, Dir: dir}), nil
}

// transcriptExists reports whether the transcript file at path exists. An empty path names no file.
func transcriptExists(path string) (bool, error) {
	if path == "" {
		return false, nil
	}
	_, err := os.Stat(path)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	default:
		return false, fmt.Errorf("open: stat transcript: %w", err)
	}
}

// createSession starts detached session name in dir running argv, and sets @orrery_dir to dir, in one tmux call
// so the option is set as the session starts.
func createSession(ctx context.Context, cfg Config, name, dir string, argv []string) error {
	args := slices.Concat(
		[]string{"new-session", "-d", "-s", tmuxLiteral(name), "-c", tmuxDirArg(dir)},
		paneArgv(ctx, cfg, argv),
		[]string{";", "set-option", "-t", "=" + name + ":", protocol.OptionDir, tmuxLiteral(dir)},
	)
	_, err := cfg.Tmux.Run(ctx, args...)
	if err != nil {
		return fmt.Errorf("open: create session %q: %w", name, err)
	}
	return nil
}

// oneWordCommand is the word count of a command with no arguments.
const oneWordCommand = 1

// paneArgv returns the words tmux runs as a new pane's command. A command with arguments runs through tmux's
// default-shell when that shell's base name is sh, bash, zsh, dash or ksh: the shell loads its environment and
// execs the command, and each argument stays one word. A one-word command, a default-shell with another name,
// and a default-shell that cannot be read all start the command directly, as argv. The lookup starts the server
// when none runs, so the default-shell applies on a cold server too.
func paneArgv(ctx context.Context, cfg Config, argv []string) []string {
	if len(argv) <= oneWordCommand {
		return argv
	}
	out, err := cfg.Tmux.Run(ctx, "start-server", ";", "show-options", "-gv", "default-shell")
	if err != nil {
		return argv
	}
	shell := strings.TrimSpace(out)
	if !isPOSIXShell(shell) {
		return argv
	}
	return slices.Concat([]string{shell, "-c", `exec "$@"`, "orrery"}, argv)
}

// isPOSIXShell reports whether the shell at path is one whose -c runs `exec "$@"` with its arguments.
func isPOSIXShell(path string) bool {
	switch filepath.Base(path) {
	case "sh", "bash", "zsh", "dash", "ksh":
		return true
	default:
		return false
	}
}

// claudeCommand is the argv of a Claude pane: claude alone, or claude -- prompt when prompt is set. The prompt
// stays one argument: paneArgv passes each word to the shell unchanged.
func claudeCommand(claude, prompt string) []string {
	if prompt == "" {
		return []string{claude}
	}
	return []string{claude, "--", tmuxLiteral(prompt)}
}

// tmuxLiteral returns value as an argument that tmux reads back as value. tmux ends a command at an argument
// ending in ";", so that final ";" is written as "\;".
func tmuxLiteral(value string) string {
	if before, ok := strings.CutSuffix(value, ";"); ok {
		return before + `\;`
	}
	return value
}

// tmuxDirArg returns dir as a -c argument. tmux expands formats in -c, so every "#" is doubled, then the
// argument is escaped as tmuxLiteral escapes it.
func tmuxDirArg(dir string) string {
	return tmuxLiteral(strings.ReplaceAll(dir, "#", "##"))
}

// session is one tmux session as listSessions reads it.
type session struct {
	name     string
	dir      string // @orrery_dir, "" when unset
	activity int64
}

// sessionFieldSeparator splits listSessions fields. tmux prints it raw, and it cannot appear in a single-line option value.
const sessionFieldSeparator = "\x1f"

// sessionFormat is the list-sessions format: name, activity, and the directory option.
const sessionFormat = "#{session_name}" + sessionFieldSeparator +
	"#{session_activity}" + sessionFieldSeparator +
	"#{" + protocol.OptionDir + "}"

// sessionFieldCount is the number of fields in sessionFormat.
const sessionFieldCount = 3

// newWithRootWords is the word count of `new NAME --root DIR`.
const newWithRootWords = 4

// Resolve turns the open arguments in req into a Plan without creating a directory or a session.
func Resolve(ctx context.Context, cfg Config, req Request) (Plan, error) {
	args := req.Args
	switch {
	case len(args) == 0:
		return resolveRecent(ctx, cfg)
	case len(args) == 1 && args[0] == "new":
		return Plan{}, errors.New("open new: NAME is required")
	case len(args) == 1 && args[0] == ".":
		return resolveDot(ctx, cfg, req)
	case len(args) == 1:
		return resolveWord(ctx, cfg, args[0])
	case args[0] == "new" && (len(args) == 2 || (len(args) == newWithRootWords && args[2] == "--root")):
		return resolveNew(ctx, cfg, args)
	default:
		return Plan{Text: strings.Join(args, " ")}, nil
	}
}

// resolveRecent is the no-argument form: the most recently active non-shell session, to be shown as a tree.
func resolveRecent(ctx context.Context, cfg Config) (Plan, error) {
	sessions, err := listSessions(ctx, cfg)
	if err != nil {
		return Plan{}, err
	}
	s, ok := mostRecent(sessions)
	if !ok {
		return Plan{}, errors.New("open: no session to attach")
	}
	return Plan{Session: s.name, Tree: true}, nil
}

// resolveDot is the form for the current directory: the session for it, or a plan to create one.
func resolveDot(ctx context.Context, cfg Config, req Request) (Plan, error) {
	if req.Remote {
		return Plan{}, errors.New("open .: the current directory is on the remote host")
	}
	sessions, err := listSessions(ctx, cfg)
	if err != nil {
		return Plan{}, err
	}
	return directoryPlan(sessions, filepath.Clean(req.Cwd)), nil
}

// resolveWord handles one word: an exact session name, then a zoxide fast match, else intent text.
func resolveWord(ctx context.Context, cfg Config, word string) (Plan, error) {
	sessions, err := listSessions(ctx, cfg)
	if err != nil {
		return Plan{}, err
	}
	for _, s := range sessions {
		if !isShell(s.name) && s.name == word {
			return Plan{Session: s.name}, nil
		}
	}
	results, err := queryZoxide(ctx, cfg, word)
	if err != nil {
		return Plan{}, err
	}
	fast := len(results) == 1 || (len(results) > 1 && filepath.Base(results[0]) == word)
	if fast {
		return directoryPlan(sessions, results[0]), nil
	}
	return Plan{Text: word}, nil
}

// resolveNew is `new NAME` or `new NAME --root DIR`: a session and directory to create.
func resolveNew(ctx context.Context, cfg Config, args []string) (Plan, error) {
	name := args[1]
	if err := checkName(name); err != nil {
		return Plan{}, err
	}
	root := cfg.Root
	if len(args) == newWithRootWords {
		root = args[3]
	}
	if !filepath.IsAbs(root) {
		return Plan{}, fmt.Errorf("open new: root %q must be an absolute directory", root)
	}
	sessions, err := listSessions(ctx, cfg)
	if err != nil {
		return Plan{}, err
	}
	return Plan{Session: freeName(sessions, name), Dir: filepath.Join(root, name), MakeDir: true}, nil
}

// directoryPlan picks the session for dir: the one whose @orrery_dir is dir, else an unset-directory
// session named after dir's basename, else a plan to create a session for dir.
func directoryPlan(sessions []session, dir string) Plan {
	if s, ok := sessionForDir(sessions, dir); ok {
		return Plan{Session: s.name}
	}
	base := filepath.Base(dir)
	for _, s := range sessions {
		if !isShell(s.name) && s.dir == "" && s.name == sanitize(base) {
			return Plan{Session: s.name}
		}
	}
	return Plan{Session: freeName(sessions, base), Dir: dir}
}

// listSessions reads every session on the configured tmux server. A server that is not running has no sessions.
func listSessions(ctx context.Context, cfg Config) ([]session, error) {
	out, err := cfg.Tmux.Run(ctx, "list-sessions", "-F", sessionFormat)
	if err != nil {
		if strings.Contains(err.Error(), "no server running") || strings.Contains(err.Error(), "error connecting to") {
			return nil, nil
		}
		return nil, fmt.Errorf("open: list sessions: %w", err)
	}
	var sessions []session
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, sessionFieldSeparator)
		if len(fields) != sessionFieldCount {
			return nil, fmt.Errorf("open: unexpected list-sessions line %q", line)
		}
		activity, parseErr := strconv.ParseInt(fields[1], 10, 64)
		if parseErr != nil {
			return nil, fmt.Errorf("open: session activity in %q: %w", line, parseErr)
		}
		sessions = append(sessions, session{name: fields[0], dir: fields[2], activity: activity})
	}
	return sessions, nil
}

// mostRecent returns the non-shell session with the highest activity, ties broken by lowest name.
func mostRecent(sessions []session) (session, bool) {
	var best session
	found := false
	for _, s := range sessions {
		if isShell(s.name) {
			continue
		}
		if !found || s.activity > best.activity || (s.activity == best.activity && s.name < best.name) {
			best, found = s, true
		}
	}
	return best, found
}

// sessionForDir returns the non-shell session whose @orrery_dir is dir.
func sessionForDir(sessions []session, dir string) (session, bool) {
	dir = filepath.Clean(dir)
	for _, s := range sessions {
		if !isShell(s.name) && s.dir != "" && s.dir == dir {
			return s, true
		}
	}
	return session{}, false
}

// freeName returns base with "." and ":" replaced by "_", then base-2, base-3, ... for the first name no session has.
func freeName(sessions []session, base string) string {
	base = sanitize(base)
	taken := make(map[string]bool, len(sessions))
	for _, s := range sessions {
		taken[s.name] = true
	}
	name := base
	for n := 2; taken[name]; n++ {
		name = base + "-" + strconv.Itoa(n)
	}
	return name
}

// checkName rejects names that cannot be a directory name under the new root.
func checkName(name string) error {
	if name == "" || name == "." || name == ".." || strings.Contains(name, "/") {
		return fmt.Errorf("open new: invalid name %q", name)
	}
	return nil
}

// queryZoxide returns the directories zoxide ranks for keywords, best first. Exit status 1 is no results.
func queryZoxide(ctx context.Context, cfg Config, keywords ...string) ([]string, error) {
	argv := append([]string{"query", "-l", "-s", "--"}, keywords...)
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, cfg.Zoxide, argv...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return nil, nil
		}
		return nil, fmt.Errorf("zoxide query: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var paths []string
	for _, line := range strings.Split(stdout.String(), "\n") {
		if line == "" {
			continue
		}
		_, path, ok := strings.Cut(strings.TrimLeft(line, " "), " ")
		if !ok {
			return nil, fmt.Errorf("zoxide query: unexpected output line %q", line)
		}
		paths = append(paths, filepath.Clean(strings.TrimLeft(path, " ")))
	}
	return paths, nil
}

// isShell reports whether name is a shell session, which open never matches.
func isShell(name string) bool {
	return strings.HasPrefix(name, protocol.ShellSessionPrefix)
}

// sanitize replaces the characters tmux session names cannot carry, and "#", which tmux expands as a format.
func sanitize(name string) string {
	return strings.NewReplacer(".", "_", ":", "_", "#", "_").Replace(name)
}
