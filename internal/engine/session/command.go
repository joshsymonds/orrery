package session

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/joshsymonds/orrery/internal/engine/proc"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// ShellArgs returns the arguments that run program through a shell: the shell's login flag when login
// is true, then -c with the exec form, then argv0 and program as the exec'd argument vector, followed
// by args. The shell replaces itself with program, so program sees argv0 as its name.
func ShellArgs(login bool, argv0, program string, args ...string) []string {
	var out []string
	if login {
		out = append(out, "-l")
	}
	out = append(out, "-c", `exec "$@"`, argv0, program)
	return append(out, args...)
}

// Config describes one session start. Shell is the owner's login shell, Claude the claude binary, Dir
// the working directory, Resume a session id to resume or empty, Model the model alias to run or empty,
// Name orrery's name for the session, Agent the claude agent to run or empty, Schema the JSON schema
// claude must return its result in or empty, and Environ the daemon's environment.
type Config struct {
	Shell          string
	Claude         string
	Dir            string
	PermissionMode string
	Resume         string
	Model          string
	Agent          string
	Schema         string
	Name           string
	Environ        []string
}

// Command returns the process that starts a headless session: the owner's login shell execs claude in
// the stream-json form with the permission prompt tool on stdio and the configured permission mode. It
// resumes Resume when set, runs Model when set, and runs Agent with a strict MCP config and returns
// Schema as its result when set. The environment is Environ without the names dropsEnv drops, then the
// session marker. An error is returned, with no Command, when Shell or Claude is not absolute, or when
// Dir, PermissionMode or Name is empty.
func Command(cfg Config) (proc.Config, error) {
	for _, path := range []struct{ name, value string }{
		{"shell", cfg.Shell}, {"claude", cfg.Claude},
	} {
		if !filepath.IsAbs(path.value) {
			return proc.Config{}, fmt.Errorf("session %s %q: %w", path.name, path.value, proc.ErrPathNotAbsolute)
		}
	}
	if cfg.Dir == "" {
		return proc.Config{}, errors.New("session working directory is empty")
	}
	if cfg.PermissionMode == "" {
		return proc.Config{}, errors.New("session permission mode is empty")
	}
	if cfg.Name == "" {
		return proc.Config{}, errors.New("session name is empty")
	}
	args := ShellArgs(true, "orrery-session", cfg.Claude, "-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--include-partial-messages",
		"--verbose",
		"--permission-prompt-tool", "stdio",
		"--permission-mode", cfg.PermissionMode,
	)
	if cfg.Resume != "" {
		args = append(args, "--resume", cfg.Resume)
	}
	if cfg.Model != "" {
		args = append(args, "--model", cfg.Model)
	}
	if cfg.Agent != "" {
		args = append(args, "--agent", cfg.Agent, "--strict-mcp-config")
	}
	if cfg.Schema != "" {
		args = append(args, "--json-schema", cfg.Schema)
	}
	env := make([]string, 0, len(cfg.Environ)+1)
	for _, entry := range cfg.Environ {
		if !dropsEnv(envName(entry)) {
			env = append(env, entry)
		}
	}
	env = append(env, protocol.EnvSession+"="+cfg.Name)
	return proc.Config{
		Path: cfg.Shell,
		Args: args,
		Dir:  cfg.Dir,
		Env:  env,
	}, nil
}

// Start starts a headless session in cfg: it builds the process with Command, starts it and runs a
// Session on it. When the session cannot open, New has already stopped the process.
func Start(cfg Config) (*Session, error) {
	command, err := Command(cfg)
	if err != nil {
		return nil, err
	}
	process, err := proc.Start(command)
	if err != nil {
		return nil, fmt.Errorf("session: start process: %w", err)
	}
	return New(process)
}

// dropsEnv reports whether a session never inherits the environment variable name: the tmux markers,
// the summarizer marker and any stale session marker.
func dropsEnv(name string) bool {
	switch name {
	case "TMUX", "TMUX_PANE", "ORRERY_SUMMARIZER", protocol.EnvSession:
		return true
	}
	return false
}

// envName returns the name of an environment entry: the text before its first "=", or the whole entry
// when it has none.
func envName(entry string) string {
	name, _, _ := strings.Cut(entry, "=")
	return name
}
