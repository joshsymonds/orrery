package capture

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/joshsymonds/orrery/internal/engine/proc"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// Daemon is what the orreryd user unit gives a session it starts: the owner's login shell, the claude
// and tmux binaries (absolute, symlinks resolved), and the user unit's HOME, USER, LANG and
// XDG_RUNTIME_DIR.
type Daemon struct {
	Shell      string
	Claude     string
	Tmux       string
	Home       string
	User       string
	Lang       string
	RuntimeDir string
}

// NewDaemon returns what the orreryd user unit gives a session. claude and tmux are found with lookPath
// and resolved with evalSymlinks, so their directories are the package directories the unit's PATH holds. Shell, Home and User come from the passwd entry
// whose uid field equals uid. Lang is LANG from environ, and RuntimeDir is XDG_RUNTIME_DIR from environ,
// else /run/user/<uid> as systemd sets it. An error names the piece that is missing, with no Daemon.
func NewDaemon(
	environ []string, lookPath, evalSymlinks func(string) (string, error), passwd []byte, uid int,
) (Daemon, error) {
	claude, err := resolveBinary("claude", lookPath, evalSymlinks)
	if err != nil {
		return Daemon{}, err
	}
	tmux, err := resolveBinary("tmux", lookPath, evalSymlinks)
	if err != nil {
		return Daemon{}, err
	}
	fields, err := passwdEntry(passwd, uid)
	if err != nil {
		return Daemon{}, err
	}
	if fields[6] == "" {
		return Daemon{}, fmt.Errorf("capture: daemon passwd entry for uid %d has no shell", uid)
	}
	if fields[5] == "" {
		return Daemon{}, fmt.Errorf("capture: daemon passwd entry for uid %d has no home", uid)
	}
	runtimeDir := environValue(environ, "XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		runtimeDir = fmt.Sprintf("/run/user/%d", uid)
	}
	return Daemon{
		Shell:      fields[6],
		Claude:     claude,
		Tmux:       tmux,
		Home:       fields[5],
		User:       fields[0],
		Lang:       environValue(environ, "LANG"),
		RuntimeDir: runtimeDir,
	}, nil
}

// resolveBinary finds name with lookPath and returns the path with its symlinks resolved.
func resolveBinary(name string, lookPath, evalSymlinks func(string) (string, error)) (string, error) {
	path, err := lookPath(name)
	if err != nil {
		return "", fmt.Errorf("capture: daemon find %s: %w", name, err)
	}
	resolved, err := evalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("capture: daemon resolve %s %q: %w", name, path, err)
	}
	return resolved, nil
}

// passwdFieldCount is the number of colon-separated fields in a passwd line: name, password, uid, gid,
// gecos, home and shell.
const passwdFieldCount = 7

// passwdEntry returns the fields of the passwd line whose uid field equals uid. Lines with fewer than
// passwdFieldCount fields or a non-numeric uid are skipped.
func passwdEntry(passwd []byte, uid int) ([]string, error) {
	for _, line := range strings.Split(string(passwd), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) < passwdFieldCount {
			continue
		}
		if entryUID, err := strconv.Atoi(fields[2]); err == nil && entryUID == uid {
			return fields, nil
		}
	}
	return nil, fmt.Errorf("capture: daemon no passwd entry for uid %d", uid)
}

// environValue returns the value of name in environ, or "" when it is absent.
func environValue(environ []string, name string) string {
	for _, entry := range environ {
		if value, ok := strings.CutPrefix(entry, name+"="); ok {
			return value
		}
	}
	return ""
}

// DaemonCommand returns the command that starts probe the way orreryd starts a session: through the
// owner's shell, in the login form when login is true, from a bare environment that holds only the
// daemon's PATH, HOME, USER, LANG and XDG_RUNTIME_DIR, with orrery's hook silenced by its marker. The
// owner's settings and hooks stay on. dir is the probe's working directory, which the caller creates
// and removes; resume, when non-empty, is the session id to resume. An error is returned, with no
// Command, when a path is not absolute, dir is empty, or probe.ResumeOf is set without a resume id.
func DaemonCommand(probe Probe, daemon Daemon, login bool, dir, resume string) (Command, error) {
	for _, path := range []struct{ name, value string }{
		{"shell", daemon.Shell}, {"claude", daemon.Claude}, {"tmux", daemon.Tmux},
	} {
		if !filepath.IsAbs(path.value) {
			return Command{}, fmt.Errorf("capture: daemon %s %q: %w", path.name, path.value, proc.ErrPathNotAbsolute)
		}
	}
	if dir == "" {
		return Command{}, errors.New("capture: daemon working directory is empty")
	}
	if probe.ResumeOf != "" && resume == "" {
		return Command{}, fmt.Errorf("capture: probe %s resumes %s without a resume id", probe.Name, probe.ResumeOf)
	}
	var args []string
	if login {
		args = append(args, "-l")
	}
	args = append(args, "-c", `exec "$@"`, "orrery-probe", daemon.Claude, "-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--include-partial-messages",
		"--verbose",
		"--model", "haiku",
		"--max-turns", "2",
		"--strict-mcp-config",
		"--mcp-config", `{"mcpServers":{}}`,
		"--tools", strings.Join(probe.Tools, ","),
		"--permission-mode", probe.PermissionMode,
	)
	if probe.PromptTool {
		args = append(args, "--permission-prompt-tool", "stdio")
	}
	if !probe.Persist {
		args = append(args, "--no-session-persistence")
	}
	if probe.HookEvents {
		args = append(args, "--include-hook-events")
	}
	if resume != "" {
		args = append(args, "--resume", resume)
	}
	return Command{
		Path: daemon.Shell,
		Args: args,
		Env: []string{
			"PATH=" + filepath.Dir(daemon.Tmux) + ":" + filepath.Dir(daemon.Claude),
			"HOME=" + daemon.Home,
			"USER=" + daemon.User,
			"LANG=" + daemon.Lang,
			"XDG_RUNTIME_DIR=" + daemon.RuntimeDir,
			protocol.EnvSummarizer + "=" + protocol.SummarizerValue,
		},
		Dir:     dir,
		Timeout: probeTimeout,
	}, nil
}
