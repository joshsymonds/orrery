// Package open resolves `orrery open` arguments to tmux sessions and executes the chosen action.
package open

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// Config holds the tmux client and the binary paths and defaults that Resolve needs.
type Config struct {
	Tmux   *tmuxctl.Client
	Zoxide string // path of the zoxide binary
	Claude string // path of the claude binary run in created panes and by the intent lookup
	Root   string // default parent directory of new projects
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

// sanitize replaces the characters tmux session names cannot carry.
func sanitize(name string) string {
	return strings.NewReplacer(".", "_", ":", "_").Replace(name)
}
