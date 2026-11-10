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

// testPasswd holds synthetic passwd entries; uid 1000 and uid 1001 are both present, so a lookup that
// takes the first line or a line by position fails.
const testPasswd = "" +
	"root:x:0:0:root:/root:/run/current-system/sw/bin/bash\n" +
	"user:x:1000:100:User:/srv/user:/run/current-system/sw/bin/zsh\n" +
	"other:x:1001:100:Other:/srv/other:/run/current-system/sw/bin/fish\n"

var (
	errFakeLookPath     = errors.New("fake lookPath: not found")
	errFakeEvalSymlinks = errors.New("fake evalSymlinks: no such path")
)

// fakeLookPath finds only the names in paths and fails the rest with errFakeLookPath.
func fakeLookPath(paths map[string]string) func(string) (string, error) {
	return func(name string) (string, error) {
		if path, ok := paths[name]; ok {
			return path, nil
		}
		return "", errFakeLookPath
	}
}

// fakeEvalSymlinks resolves only the paths in links and fails the rest with errFakeEvalSymlinks.
func fakeEvalSymlinks(links map[string]string) func(string) (string, error) {
	return func(path string) (string, error) {
		if resolved, ok := links[path]; ok {
			return resolved, nil
		}
		return "", errFakeEvalSymlinks
	}
}

// daemonArgs are the inputs NewDaemon reads.
type daemonArgs struct {
	environ      []string
	lookPath     func(string) (string, error)
	evalSymlinks func(string) (string, error)
	passwd       []byte
	uid          int
}

// testDaemonArgs are inputs for the unit of a user with uid 1000, every value synthetic. claude and
// tmux are found in a profile directory, and their symlinks resolve into package store directories.
func testDaemonArgs() daemonArgs {
	return daemonArgs{
		environ: []string{"LANG=C.UTF-8", "XDG_RUNTIME_DIR=/run/user/1000"},
		lookPath: fakeLookPath(map[string]string{
			"claude": "/home/user/.nix-profile/bin/claude",
			"tmux":   "/home/user/.nix-profile/bin/tmux",
		}),
		evalSymlinks: fakeEvalSymlinks(map[string]string{
			"/home/user/.nix-profile/bin/claude": "/nix/store/aaaa-claude-code/bin/claude",
			"/home/user/.nix-profile/bin/tmux":   "/nix/store/bbbb-tmux/bin/tmux",
		}),
		passwd: []byte(testPasswd),
		uid:    1000,
	}
}

func (a daemonArgs) call() (Daemon, error) {
	return NewDaemon(a.environ, a.lookPath, a.evalSymlinks, a.passwd, a.uid)
}

func TestNewDaemonFillsEveryField(t *testing.T) {
	t.Parallel()
	got, err := testDaemonArgs().call()
	if err != nil {
		t.Fatalf("NewDaemon: %v", err)
	}
	want := Daemon{
		Shell:      "/run/current-system/sw/bin/zsh",
		Claude:     "/nix/store/aaaa-claude-code/bin/claude",
		Tmux:       "/nix/store/bbbb-tmux/bin/tmux",
		Home:       "/srv/user",
		User:       "user",
		Lang:       "C.UTF-8",
		RuntimeDir: "/run/user/1000",
	}
	if got != want {
		t.Errorf("NewDaemon = %+v, want %+v", got, want)
	}
}

func TestNewDaemonResolvesSymlinksOfLookedUpBinaries(t *testing.T) {
	t.Parallel()
	args := testDaemonArgs()
	var resolved []string
	args.evalSymlinks = func(path string) (string, error) {
		resolved = append(resolved, path)
		return fakeEvalSymlinks(map[string]string{
			"/home/user/.nix-profile/bin/claude": "/nix/store/aaaa-claude-code/bin/claude",
			"/home/user/.nix-profile/bin/tmux":   "/nix/store/bbbb-tmux/bin/tmux",
		})(path)
	}
	got, err := args.call()
	if err != nil {
		t.Fatalf("NewDaemon: %v", err)
	}
	wantResolved := []string{"/home/user/.nix-profile/bin/claude", "/home/user/.nix-profile/bin/tmux"}
	if !slices.Equal(resolved, wantResolved) {
		t.Errorf("evalSymlinks called with %q, want %q", resolved, wantResolved)
	}
	if got.Claude != "/nix/store/aaaa-claude-code/bin/claude" || got.Tmux != "/nix/store/bbbb-tmux/bin/tmux" {
		t.Errorf("Claude, Tmux = %q, %q; want the resolved store paths", got.Claude, got.Tmux)
	}
}

func TestNewDaemonPasswdEntryChosenByUID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		uid   int
		shell string
		home  string
		user  string
	}{
		{uid: 0, shell: "/run/current-system/sw/bin/bash", home: "/root", user: "root"},
		{uid: 1000, shell: "/run/current-system/sw/bin/zsh", home: "/srv/user", user: "user"},
		{uid: 1001, shell: "/run/current-system/sw/bin/fish", home: "/srv/other", user: "other"},
	}
	for _, tt := range tests {
		t.Run(tt.user, func(t *testing.T) {
			t.Parallel()
			args := testDaemonArgs()
			args.uid = tt.uid
			got, err := args.call()
			if err != nil {
				t.Fatalf("NewDaemon(uid %d): %v", tt.uid, err)
			}
			if got.Shell != tt.shell || got.Home != tt.home || got.User != tt.user {
				t.Errorf("NewDaemon(uid %d) Shell, Home, User = %q, %q, %q; want %q, %q, %q",
					tt.uid, got.Shell, got.Home, got.User, tt.shell, tt.home, tt.user)
			}
		})
	}
}

func TestNewDaemonRuntimeDirFallsBackToSystemdPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		environ []string
		uid     int
		want    string
	}{
		{name: "absent", environ: []string{"LANG=C.UTF-8"}, uid: 1000, want: "/run/user/1000"},
		{name: "empty", environ: []string{"XDG_RUNTIME_DIR=", "LANG=C.UTF-8"}, uid: 1001, want: "/run/user/1001"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			args := testDaemonArgs()
			args.environ = tt.environ
			args.uid = tt.uid
			got, err := args.call()
			if err != nil {
				t.Fatalf("NewDaemon: %v", err)
			}
			if got.RuntimeDir != tt.want {
				t.Errorf("RuntimeDir = %q, want %q", got.RuntimeDir, tt.want)
			}
		})
	}
}

func TestNewDaemonErrorsNameWhatIsMissing(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		mutate   func(*daemonArgs)
		wantErr  error
		wantText string
	}{
		{
			name: "claude not on PATH",
			mutate: func(a *daemonArgs) {
				a.lookPath = fakeLookPath(map[string]string{"tmux": "/home/user/.nix-profile/bin/tmux"})
			},
			wantErr:  errFakeLookPath,
			wantText: "claude",
		},
		{
			name: "tmux not on PATH",
			mutate: func(a *daemonArgs) {
				a.lookPath = fakeLookPath(map[string]string{"claude": "/home/user/.nix-profile/bin/claude"})
			},
			wantErr:  errFakeLookPath,
			wantText: "tmux",
		},
		{
			name: "claude does not resolve",
			mutate: func(a *daemonArgs) {
				a.evalSymlinks = fakeEvalSymlinks(map[string]string{
					"/home/user/.nix-profile/bin/tmux": "/nix/store/bbbb-tmux/bin/tmux",
				})
			},
			wantErr:  errFakeEvalSymlinks,
			wantText: "claude",
		},
		{
			name: "tmux does not resolve",
			mutate: func(a *daemonArgs) {
				a.evalSymlinks = fakeEvalSymlinks(map[string]string{
					"/home/user/.nix-profile/bin/claude": "/nix/store/aaaa-claude-code/bin/claude",
				})
			},
			wantErr:  errFakeEvalSymlinks,
			wantText: "tmux",
		},
		{
			name:     "no passwd entry for uid",
			mutate:   func(a *daemonArgs) { a.uid = 4242 },
			wantText: "passwd",
		},
		{
			name: "empty shell field",
			mutate: func(a *daemonArgs) {
				a.passwd = []byte("user:x:1000:100:User:/srv/user:\n")
			},
			wantText: "shell",
		},
		{
			name: "empty home field",
			mutate: func(a *daemonArgs) {
				a.passwd = []byte("user:x:1000:100:User::/run/current-system/sw/bin/zsh\n")
			},
			wantText: "home",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			args := testDaemonArgs()
			tt.mutate(&args)
			got, err := args.call()
			if err == nil {
				t.Fatalf("NewDaemon returned no error and %+v", got)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("error = %v, want wrapping %v", err, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantText) {
				t.Errorf("error = %q, want it to name %q", err, tt.wantText)
			}
			if got != (Daemon{}) {
				t.Errorf("error case returned a non-zero Daemon: %+v", got)
			}
		})
	}
}
