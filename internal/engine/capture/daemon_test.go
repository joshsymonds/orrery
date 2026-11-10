package capture

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/engine/proc"
	"github.com/joshsymonds/orrery/internal/protocol"
)

const (
	testDaemonClaude = "/home/user/.local/bin/claude"
	testDaemonTmux   = "/run/current-system/sw/bin/tmux"
	testDaemonShell  = "/run/current-system/sw/bin/zsh"
	// testResumeID uses the fixed id form the engine leak test allows.
	testResumeID = "00000000-0000-4000-8000-000000000001"
)

// testDaemon is the daemon a unit of orreryd hands a session, with every path absolute.
func testDaemon() Daemon {
	return Daemon{
		Shell:      testDaemonShell,
		Claude:     testDaemonClaude,
		Tmux:       testDaemonTmux,
		Home:       "/home/user",
		User:       "user",
		Lang:       "C.UTF-8",
		RuntimeDir: "/run/user/1000",
	}
}

// wantDaemonArgs spells out the argument vector DaemonCommand must build for these options.
func wantDaemonArgs(login bool, tools, mode string, promptTool, persist, hookEvents bool, resume string) []string {
	var args []string
	if login {
		args = append(args, "-l")
	}
	args = append(args, "-c", `exec "$@"`, "orrery-probe", testDaemonClaude,
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--include-partial-messages",
		"--verbose",
		"--model", "haiku",
		"--max-turns", "2",
		"--strict-mcp-config",
		"--mcp-config", `{"mcpServers":{}}`,
		"--tools", tools,
		"--permission-mode", mode,
	)
	if promptTool {
		args = append(args, "--permission-prompt-tool", "stdio")
	}
	if !persist {
		args = append(args, "--no-session-persistence")
	}
	if hookEvents {
		args = append(args, "--include-hook-events")
	}
	if resume != "" {
		args = append(args, "--resume", resume)
	}
	return args
}

func TestDaemonCommandArgsLoginAndPlainForms(t *testing.T) {
	t.Parallel()
	const tools = "Write"
	const mode = "default"
	for _, login := range []bool{true, false} {
		for _, promptTool := range []bool{false, true} {
			for _, persist := range []bool{false, true} {
				for _, hookEvents := range []bool{false, true} {
					for _, resume := range []string{"", testResumeID} {
						probe := Probe{
							Name:           "case",
							PermissionMode: mode,
							PromptTool:     promptTool,
							Tools:          []string{tools},
							Persist:        persist,
							HookEvents:     hookEvents,
						}
						cmd, err := DaemonCommand(probe, testDaemon(), login, t.TempDir(), resume)
						if err != nil {
							t.Fatalf("DaemonCommand(login=%v promptTool=%v persist=%v hookEvents=%v resume=%q): %v",
								login, promptTool, persist, hookEvents, resume, err)
						}
						want := wantDaemonArgs(login, tools, mode, promptTool, persist, hookEvents, resume)
						if !slices.Equal(cmd.Args, want) {
							t.Errorf("login=%v promptTool=%v persist=%v hookEvents=%v resume=%q Args =\n%q\nwant\n%q",
								login, promptTool, persist, hookEvents, resume, cmd.Args, want)
						}
					}
				}
			}
		}
	}
}

func TestDaemonCommandArgsEveryFixedProbe(t *testing.T) {
	t.Parallel()
	for _, probe := range Probes() {
		cmd, err := DaemonCommand(probe, testDaemon(), true, t.TempDir(), "")
		if err != nil {
			t.Fatalf("DaemonCommand(%s): %v", probe.Name, err)
		}
		want := wantDaemonArgs(true, strings.Join(probe.Tools, ","), probe.PermissionMode,
			probe.PromptTool, false, false, "")
		if !slices.Equal(cmd.Args, want) {
			t.Errorf("%s Args =\n%q\nwant\n%q", probe.Name, cmd.Args, want)
		}
	}
}

func TestDaemonCommandNeverPresentsForbiddenFlags(t *testing.T) {
	t.Parallel()
	forbidden := []string{
		"--settings", "--setting-sources", "--bare", "--dangerously-skip-permissions", "--safe-mode",
	}
	probe := Probe{Name: "all-on", PermissionMode: "default", PromptTool: true, Persist: true, HookEvents: true,
		Tools: []string{"Write"}}
	cmd, err := DaemonCommand(probe, testDaemon(), true, t.TempDir(), testResumeID)
	if err != nil {
		t.Fatalf("DaemonCommand: %v", err)
	}
	if !slices.Contains(cmd.Args, testDaemonClaude) {
		t.Fatalf("Args %q lacks the claude binary, so the forbidden-flag check would pass vacuously", cmd.Args)
	}
	for _, arg := range cmd.Args {
		if slices.Contains(forbidden, arg) {
			t.Errorf("Args contains forbidden %q", arg)
		}
		if strings.Contains(arg, "disableAllHooks") {
			t.Errorf("Args contains forbidden disableAllHooks in %q", arg)
		}
	}
}

func TestDaemonCommandEnvIsExactAndSilencesHook(t *testing.T) {
	t.Parallel()
	want := []string{
		"PATH=/run/current-system/sw/bin:/home/user/.local/bin",
		"HOME=/home/user",
		"USER=user",
		"LANG=C.UTF-8",
		"XDG_RUNTIME_DIR=/run/user/1000",
		protocol.EnvSummarizer + "=" + protocol.SummarizerValue,
	}
	cmd, err := DaemonCommand(Probes()[0], testDaemon(), false, t.TempDir(), "")
	if err != nil {
		t.Fatalf("DaemonCommand: %v", err)
	}
	if !slices.Equal(cmd.Env, want) {
		t.Errorf("Env =\n%q\nwant\n%q", cmd.Env, want)
	}
	if !slices.Contains(cmd.Env, "ORRERY_SUMMARIZER=1") {
		t.Errorf("Env %q lacks ORRERY_SUMMARIZER=1, so orrery's hook would send events", cmd.Env)
	}
	for _, entry := range cmd.Env {
		name, _, _ := strings.Cut(entry, "=")
		if name == "TMUX" || name == "TMUX_PANE" || name == "CLAUDECODE" || strings.HasPrefix(name, "CLAUDE_CODE_") {
			t.Errorf("Env carries forbidden entry %q", entry)
		}
	}
}

func TestDaemonCommandPathDirTimeout(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "absent")
	cmd, err := DaemonCommand(Probes()[0], testDaemon(), true, dir, "")
	if err != nil {
		t.Fatalf("DaemonCommand: %v", err)
	}
	if cmd.Path != testDaemonShell {
		t.Errorf("Path = %q, want %q", cmd.Path, testDaemonShell)
	}
	if cmd.Dir != dir {
		t.Errorf("Dir = %q, want %q", cmd.Dir, dir)
	}
	if cmd.Timeout != probeTimeout {
		t.Errorf("Timeout = %v, want %v", cmd.Timeout, probeTimeout)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("DaemonCommand created %s: stat error = %v, want not exist", dir, err)
	}
}

func TestDaemonCommandRejectsBadInputs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	relativeShell := testDaemon()
	relativeShell.Shell = "zsh"
	relativeClaude := testDaemon()
	relativeClaude.Claude = "claude"
	relativeTmux := testDaemon()
	relativeTmux.Tmux = "tmux"
	tests := []struct {
		name    string
		probe   Probe
		daemon  Daemon
		dir     string
		wantErr error
	}{
		{
			name:    "relative shell",
			probe:   Probes()[0],
			daemon:  relativeShell,
			dir:     dir,
			wantErr: proc.ErrPathNotAbsolute,
		},
		{
			name:    "relative claude",
			probe:   Probes()[0],
			daemon:  relativeClaude,
			dir:     dir,
			wantErr: proc.ErrPathNotAbsolute,
		},
		{
			name:    "relative tmux",
			probe:   Probes()[0],
			daemon:  relativeTmux,
			dir:     dir,
			wantErr: proc.ErrPathNotAbsolute,
		},
		{
			name:   "empty dir",
			probe:  Probes()[0],
			daemon: testDaemon(),
			dir:    "",
		},
		{
			name:   "resume of without resume id",
			probe:  Probe{Name: "chained", ResumeOf: "two-turns"},
			daemon: testDaemon(),
			dir:    dir,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cmd, err := DaemonCommand(tt.probe, tt.daemon, true, tt.dir, "")
			if err == nil {
				t.Fatalf("DaemonCommand returned no error and %+v", cmd)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("error = %v, want wrapping %v", err, tt.wantErr)
			}
			if cmd.Args != nil || cmd.Path != "" || cmd.Dir != "" || cmd.Env != nil {
				t.Errorf("error case returned a non-zero Command: %+v", cmd)
			}
		})
	}
}

func TestDaemonCommandAcceptsResumeOfWithResumeID(t *testing.T) {
	t.Parallel()
	probe := Probe{Name: "chained", ResumeOf: "two-turns"}
	cmd, err := DaemonCommand(probe, testDaemon(), true, t.TempDir(), testResumeID)
	if err != nil {
		t.Fatalf("DaemonCommand: %v", err)
	}
	if n := len(cmd.Args); n < 2 || cmd.Args[n-2] != "--resume" {
		t.Errorf("Args = %q, want the resume id last", cmd.Args)
	}
}
