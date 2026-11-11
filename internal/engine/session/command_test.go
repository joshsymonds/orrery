package session

import (
	"errors"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/engine/proc"
	"github.com/joshsymonds/orrery/internal/protocol"
)

const (
	testShell  = "/run/current-system/sw/bin/zsh"
	testClaude = "/home/user/.local/bin/claude"
	testDir    = "/home/user/work"
	testMode   = "bypassPermissions"
	testName   = "work-1"
	testResume = "00000000-0000-4000-8000-000000000001"
	testModel  = "haiku"
	testAgent  = "haiku-medium"
	testSchema = `{"type":"object"}`
)

// testConfig is a valid session configuration; each case changes one field from it.
func testConfig() Config {
	return Config{
		Shell:          testShell,
		Claude:         testClaude,
		Dir:            testDir,
		PermissionMode: testMode,
		Name:           testName,
		Environ:        []string{"PATH=/usr/bin", "HOME=/home/user"},
	}
}

// wantSessionArgs spells out the argument vector a new session starts claude with, after the login
// shell's exec form.
func wantSessionArgs(claude, mode string) []string {
	return []string{
		"-l", "-c", `exec "$@"`, "orrery-session", claude,
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--include-partial-messages",
		"--verbose",
		"--permission-prompt-tool", "stdio",
		"--permission-mode", mode,
	}
}

func TestShellArgsForms(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		login bool
		args  []string
		want  []string
	}{
		{
			name:  "login with arguments",
			login: true,
			args:  []string{"-p", "x"},
			want:  []string{"-l", "-c", `exec "$@"`, "prog", testClaude, "-p", "x"},
		},
		{
			name:  "plain without arguments",
			login: false,
			want:  []string{"-c", `exec "$@"`, "prog", testClaude},
		},
		{
			name:  "plain with arguments",
			login: false,
			args:  []string{"--verbose"},
			want:  []string{"-c", `exec "$@"`, "prog", testClaude, "--verbose"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ShellArgs(tt.login, "prog", testClaude, tt.args...)
			if !slices.Equal(got, tt.want) {
				t.Errorf("ShellArgs(%v) =\n%q\nwant\n%q", tt.login, got, tt.want)
			}
		})
	}
}

func TestCommandNewSession(t *testing.T) {
	t.Parallel()
	cmd, err := Command(testConfig())
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	if cmd.Path != testShell {
		t.Errorf("Path = %q, want %q", cmd.Path, testShell)
	}
	if want := wantSessionArgs(testClaude, testMode); !slices.Equal(cmd.Args, want) {
		t.Errorf("Args =\n%q\nwant\n%q", cmd.Args, want)
	}
	if cmd.Dir != testDir {
		t.Errorf("Dir = %q, want %q", cmd.Dir, testDir)
	}
	wantEnv := []string{"PATH=/usr/bin", "HOME=/home/user", protocol.EnvSession + "=" + testName}
	if !slices.Equal(cmd.Env, wantEnv) {
		t.Errorf("Env =\n%q\nwant\n%q", cmd.Env, wantEnv)
	}
}

func TestCommandResumedSession(t *testing.T) {
	t.Parallel()
	cfg := testConfig()
	cfg.Resume = testResume
	cmd, err := Command(cfg)
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	want := append(wantSessionArgs(testClaude, testMode), "--resume", testResume)
	if !slices.Equal(cmd.Args, want) {
		t.Errorf("Args =\n%q\nwant\n%q", cmd.Args, want)
	}
}

func TestCommandModel(t *testing.T) {
	t.Parallel()
	cfg := testConfig()
	cfg.Model = testModel
	cmd, err := Command(cfg)
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	want := append(wantSessionArgs(testClaude, testMode), "--model", testModel)
	if !slices.Equal(cmd.Args, want) {
		t.Errorf("Args =\n%q\nwant\n%q", cmd.Args, want)
	}
}

func TestCommandResumedModelSession(t *testing.T) {
	t.Parallel()
	cfg := testConfig()
	cfg.Resume = testResume
	cfg.Model = testModel
	cmd, err := Command(cfg)
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	want := append(wantSessionArgs(testClaude, testMode), "--resume", testResume, "--model", testModel)
	if !slices.Equal(cmd.Args, want) {
		t.Errorf("Args =\n%q\nwant\n%q", cmd.Args, want)
	}
}

func TestCommandAgentSession(t *testing.T) {
	t.Parallel()
	cfg := testConfig()
	cfg.Agent = testAgent
	cfg.Schema = testSchema
	cmd, err := Command(cfg)
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	want := append(wantSessionArgs(testClaude, testMode),
		"--agent", testAgent, "--strict-mcp-config", "--json-schema", testSchema)
	if !slices.Equal(cmd.Args, want) {
		t.Errorf("Args =\n%q\nwant\n%q", cmd.Args, want)
	}
}

func TestCommandAgentResumedModelSession(t *testing.T) {
	t.Parallel()
	cfg := testConfig()
	cfg.Resume = testResume
	cfg.Model = testModel
	cfg.Agent = testAgent
	cfg.Schema = testSchema
	cmd, err := Command(cfg)
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	want := append(wantSessionArgs(testClaude, testMode), "--resume", testResume, "--model", testModel,
		"--agent", testAgent, "--strict-mcp-config", "--json-schema", testSchema)
	if !slices.Equal(cmd.Args, want) {
		t.Errorf("Args =\n%q\nwant\n%q", cmd.Args, want)
	}
}

func TestCommandSchemaWithoutAgent(t *testing.T) {
	t.Parallel()
	cfg := testConfig()
	cfg.Schema = testSchema
	cmd, err := Command(cfg)
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	want := append(wantSessionArgs(testClaude, testMode), "--json-schema", testSchema)
	if !slices.Equal(cmd.Args, want) {
		t.Errorf("Args =\n%q\nwant\n%q", cmd.Args, want)
	}
}

func TestCommandEnvironment(t *testing.T) {
	t.Parallel()
	cfg := testConfig()
	cfg.Environ = []string{
		"PATH=/usr/bin",
		"TMUX=/tmp/tmux-1000/default,1,0",
		"TMUX_PANE=%3",
		"TMUX_TMPDIR=/tmp/tmux-scratch",
		"ORRERY_SUMMARIZER=1",
		"ORRERY_SESSION=stale",
		"HOME=/home/user",
		"ORRERY_SESSIONX=kept",
		"TMUX_PANEX=kept",
	}
	cmd, err := Command(cfg)
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	wantEnv := []string{
		"PATH=/usr/bin",
		"TMUX_TMPDIR=/tmp/tmux-scratch",
		"HOME=/home/user",
		"ORRERY_SESSIONX=kept",
		"TMUX_PANEX=kept",
		protocol.EnvSession + "=" + testName,
	}
	if !slices.Equal(cmd.Env, wantEnv) {
		t.Errorf("Env =\n%q\nwant\n%q", cmd.Env, wantEnv)
	}
}

func TestCommandErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		mutate     func(*Config)
		wantNotAbs bool
	}{
		{name: "shell relative", mutate: func(c *Config) { c.Shell = "zsh" }, wantNotAbs: true},
		{name: "shell empty", mutate: func(c *Config) { c.Shell = "" }, wantNotAbs: true},
		{name: "claude relative", mutate: func(c *Config) { c.Claude = "claude" }, wantNotAbs: true},
		{name: "claude empty", mutate: func(c *Config) { c.Claude = "" }, wantNotAbs: true},
		{name: "dir empty", mutate: func(c *Config) { c.Dir = "" }},
		{name: "permission mode empty", mutate: func(c *Config) { c.PermissionMode = "" }},
		{name: "name empty", mutate: func(c *Config) { c.Name = "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := testConfig()
			tt.mutate(&cfg)
			_, err := Command(cfg)
			if err == nil {
				t.Fatalf("Command(%s) returned no error", tt.name)
			}
			if got := errors.Is(err, proc.ErrPathNotAbsolute); got != tt.wantNotAbs {
				t.Errorf("errors.Is(err, ErrPathNotAbsolute) = %v, want %v (err: %v)", got, tt.wantNotAbs, err)
			}
		})
	}
	t.Run("zero config", func(t *testing.T) {
		t.Parallel()
		_, err := Command(Config{})
		if !errors.Is(err, proc.ErrPathNotAbsolute) {
			t.Errorf("Command(Config{}) error = %v, want ErrPathNotAbsolute", err)
		}
	})
}
