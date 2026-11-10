package capture

import (
	"errors"
	"fmt"
	"path/filepath"
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
