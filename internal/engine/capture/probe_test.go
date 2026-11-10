package capture

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/proc"
)

const testBinary = "/opt/fake/bin/claude"

// declared is one fixed probe, spelled out field by field.
type declared struct {
	name, mode, tools string
	promptTool        bool
	answer            Answer
	interrupt         bool
	modeChange        string
	prompts           []string
	expect            []Expectation
}

func declaredProbes() []declared {
	return []declared{
		{
			name: "two-turns", mode: "default",
			prompts: []string{
				"Reply with exactly the word ONE and nothing else.",
				"Reply with exactly the word TWO and nothing else.",
			},
			expect: []Expectation{{Type: "system", Subtype: "init"}, {Type: "result", Subtype: "success", Count: 2}},
		},
		{
			name: "interrupt", mode: "default", interrupt: true,
			prompts: []string{"Write the numbers from 1 to 400, one per line, with no other text."},
			expect: []Expectation{
				{Type: "control_response", Subtype: "success", Count: 2},
				{Type: "result"},
			},
		},
		{
			name: "mode-change", mode: "default", modeChange: "acceptEdits",
			prompts: []string{"Reply with exactly the word OK and nothing else."},
			expect: []Expectation{
				{Type: "control_response", Subtype: "success", Count: 2},
				{Type: "system", Subtype: "init"},
				{Type: "result", Subtype: "success"},
			},
		},
		{
			name: "question", mode: "bypassPermissions", tools: "AskUserQuestion",
			promptTool: true, answer: AnswerQuestion,
			prompts: []string{
				"Use the AskUserQuestion tool exactly once to ask me which letter I prefer, with exactly two " +
					"options labeled A and B. After I answer, reply with exactly the letter I chose and nothing else.",
			},
			expect: []Expectation{
				{Type: "control_request", Subtype: "can_use_tool"},
				{Type: "result"},
			},
		},
		{
			name: "permission-allow", mode: "default", tools: "Write",
			promptTool: true, answer: AnswerAllow,
			prompts: []string{
				"Use the Write tool exactly once to create the file note.txt in the current directory containing " +
					"exactly the text ok. Then reply with exactly the word DONE and nothing else.",
			},
			expect: []Expectation{
				{Type: "control_request", Subtype: "can_use_tool"},
				{Type: "result", Subtype: "success"},
			},
		},
		{
			name: "permission-deny", mode: "default", tools: "Write",
			promptTool: true, answer: AnswerDeny,
			prompts: []string{
				"Use the Write tool exactly once to create the file note.txt in the current directory containing " +
					"exactly the text ok. Then reply with exactly the word DONE and nothing else.",
			},
			expect: []Expectation{
				{Type: "control_request", Subtype: "can_use_tool"},
				{Type: "result"},
			},
		},
	}
}

// wantArgs spells out the fixed argument vector, one element per token, with the probe's own tools,
// mode, and prompt-tool flag.
func wantArgs(tools, mode string, promptTool bool) []string {
	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--include-partial-messages",
		"--verbose",
		"--model", "haiku",
		"--max-turns", "2",
		"--settings", `{"disableAllHooks":true}`,
		"--setting-sources", "",
		"--strict-mcp-config",
		"--mcp-config", `{"mcpServers":{}}`,
		"--no-session-persistence",
		"--tools", tools,
		"--permission-mode", mode,
	}
	if promptTool {
		args = append(args, "--permission-prompt-tool", "stdio")
	}
	return args
}

// fixtureEnviron mixes allow-listed entries with names the probe must drop, including session and
// account variables from a parent Claude Code session.
func fixtureEnviron() []string {
	return []string{
		"HOME=/home/user",
		"HOMEX=1",
		"TMUX=/tmp/tmux-1000/default,1,0",
		"TMUX_PANE=%1",
		"PATH=/usr/bin:/bin",
		"CLAUDECODE=1",
		"CLAUDE_CODE_ENTRYPOINT=cli",
		"ANTHROPIC_BASE_URL=https://api.example.test",
		"USER=user",
		"LC_ALL=C",
		"LANG=C.UTF-8",
		"TERM=xterm",
		"AWS_PROFILE=dev",
		"XDG_RUNTIME_DIR=/run/user/1000",
		"XDG_CONFIG_HOME=/home/user/.config",
		"XDG_STATE_HOME=/home/user/.local/state",
		"XDG_DATA_HOME=/home/user/.local/share",
	}
}

func mustCommand(t *testing.T, probe Probe, environ []string) Command {
	t.Helper()
	cmd, err := NewCommand(probe, testBinary, environ)
	if err != nil {
		t.Fatalf("NewCommand(%s): %v", probe.Name, err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(cmd.Dir); err != nil {
			t.Errorf("remove %s: %v", cmd.Dir, err)
		}
	})
	return cmd
}

func TestProbesDeclared(t *testing.T) {
	t.Parallel()
	want := declaredProbes()
	got := Probes()
	if len(got) != len(want) {
		t.Fatalf("Probes() returned %d probes, want %d", len(got), len(want))
	}
	for i, w := range want {
		p := got[i]
		if p.Name != w.name {
			t.Errorf("probe %d name = %q, want %q", i, p.Name, w.name)
		}
		if p.PermissionMode != w.mode {
			t.Errorf("%s PermissionMode = %q, want %q", w.name, p.PermissionMode, w.mode)
		}
		if p.PromptTool != w.promptTool {
			t.Errorf("%s PromptTool = %v, want %v", w.name, p.PromptTool, w.promptTool)
		}
		if got := strings.Join(p.Tools, ","); got != w.tools {
			t.Errorf("%s tools = %q, want %q", w.name, got, w.tools)
		}
		if p.Answer != w.answer {
			t.Errorf("%s Answer = %d, want %d", w.name, p.Answer, w.answer)
		}
		if p.Interrupt != w.interrupt {
			t.Errorf("%s Interrupt = %v, want %v", w.name, p.Interrupt, w.interrupt)
		}
		if p.ModeChange != w.modeChange {
			t.Errorf("%s ModeChange = %q, want %q", w.name, p.ModeChange, w.modeChange)
		}
		if !slices.Equal(p.Prompts, w.prompts) {
			t.Errorf("%s Prompts = %q, want %q", w.name, p.Prompts, w.prompts)
		}
		if !slices.Equal(p.Expect, w.expect) {
			t.Errorf("%s Expect = %+v, want %+v", w.name, p.Expect, w.expect)
		}
	}
}

func TestCommandArgsPinIsolationFlags(t *testing.T) {
	t.Parallel()
	want := declaredProbes()
	for i, p := range Probes() {
		cmd := mustCommand(t, p, fixtureEnviron())
		w := want[i]
		exp := wantArgs(w.tools, w.mode, w.promptTool)
		if !slices.Equal(cmd.Args, exp) {
			t.Errorf("%s Args =\n%q\nwant\n%q", p.Name, cmd.Args, exp)
		}
	}
}

func TestCommandArgsNeverResumeOrSkipPermissions(t *testing.T) {
	t.Parallel()
	forbidden := []string{
		"--resume", "-r", "--continue", "-c", "--fork-session", "--session-id", "--dangerously-skip-permissions",
	}
	for _, p := range Probes() {
		cmd := mustCommand(t, p, fixtureEnviron())
		for _, arg := range cmd.Args {
			if slices.Contains(forbidden, arg) {
				t.Errorf("%s Args contains forbidden %q", p.Name, arg)
			}
		}
	}
}

func TestCommandEnvAllowListOnly(t *testing.T) {
	t.Parallel()
	want := []string{
		"HOME=/home/user",
		"PATH=/usr/bin:/bin",
		"USER=user",
		"LANG=C.UTF-8",
		"TERM=xterm",
		"XDG_RUNTIME_DIR=/run/user/1000",
		"XDG_CONFIG_HOME=/home/user/.config",
		"XDG_STATE_HOME=/home/user/.local/state",
	}
	for _, p := range Probes() {
		cmd := mustCommand(t, p, fixtureEnviron())
		if !slices.Equal(cmd.Env, want) {
			t.Errorf("%s Env =\n%q\nwant\n%q", p.Name, cmd.Env, want)
		}
	}
}

func TestCommandEnvNeverInheritsWhenEnvironEmpty(t *testing.T) {
	t.Parallel()
	cmd := mustCommand(t, Probes()[0], nil)
	if cmd.Env == nil {
		t.Fatal("Env is nil, which would make proc inherit the whole parent environment")
	}
	if len(cmd.Env) != 0 {
		t.Errorf("Env = %q, want empty", cmd.Env)
	}
}

func TestCommandDirFreshEmptyAndDistinct(t *testing.T) {
	t.Parallel()
	probe := Probes()[0]
	first := mustCommand(t, probe, nil)
	second := mustCommand(t, probe, nil)
	if first.Dir == second.Dir {
		t.Fatalf("two calls share working directory %q", first.Dir)
	}
	for _, cmd := range []Command{first, second} {
		if !strings.HasPrefix(filepath.Base(cmd.Dir), "orrery-probe-") {
			t.Errorf("Dir %q does not carry the orrery-probe- prefix", cmd.Dir)
		}
		info, err := os.Stat(cmd.Dir)
		if err != nil {
			t.Fatalf("stat Dir %q: %v", cmd.Dir, err)
		}
		if !info.IsDir() {
			t.Errorf("Dir %q is not a directory", cmd.Dir)
		}
		entries, err := os.ReadDir(cmd.Dir)
		if err != nil {
			t.Fatalf("read Dir %q: %v", cmd.Dir, err)
		}
		if len(entries) != 0 {
			t.Errorf("Dir %q holds %d entries, want none", cmd.Dir, len(entries))
		}
	}
}

func TestCommandPathTimeoutAndBinary(t *testing.T) {
	t.Parallel()
	for _, p := range Probes() {
		cmd := mustCommand(t, p, nil)
		if cmd.Path != testBinary {
			t.Errorf("%s Path = %q, want %q", p.Name, cmd.Path, testBinary)
		}
		if cmd.Timeout != 120*time.Second {
			t.Errorf("%s Timeout = %v, want 120s", p.Name, cmd.Timeout)
		}
	}
}

func TestCommandDirCreatedInSystemTempDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	for _, cmd := range []Command{mustCommand(t, Probes()[0], nil), mustCommand(t, Probes()[0], nil)} {
		if parent := filepath.Dir(cmd.Dir); parent != tmp {
			t.Errorf("Dir %q is inside %q, want the system temporary directory %q", cmd.Dir, parent, tmp)
		}
	}
}

func TestNewCommandRejectsRelativeBinaryWithoutCreatingDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	for _, binary := range []string{"claude", "./claude"} {
		_, err := NewCommand(Probes()[0], binary, nil)
		if !errors.Is(err, proc.ErrPathNotAbsolute) {
			t.Errorf("NewCommand(%q) error = %v, want proc.ErrPathNotAbsolute", binary, err)
		}
	}
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("read TMPDIR: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("relative binary left %d entries in TMPDIR, want none", len(entries))
	}
}
