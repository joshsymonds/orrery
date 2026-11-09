package open

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// paneMarker is the variable the probe shell exports. A pane that runs through the probe has it in its environment.
const paneMarker = "ORRERY_PANE_SHELL_MARKER"

// probeShell writes an executable named name that exports paneMarker and then runs /bin/sh with its arguments.
// It returns the path of the probe.
func probeShell(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	script := "#!/bin/sh\nexport " + paneMarker + "=1\nexec /bin/sh \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write probe shell: %v", err)
	}
	return path
}

// paneStub writes a claude stand-in named claude and returns its path. Each run records its environment, then its
// argv NUL-separated so that an argument with a newline stays exact, then writes done last.
func paneStub(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude")
	script := `#!/bin/sh
D=$(dirname "$0")
env > "$D/env"
printf '%s\000' "$@" > "$D/argv"
: > "$D/done"
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write claude stand-in: %v", err)
	}
	return path
}

// paneRun is what one run of the paneStub recorded.
type paneRun struct {
	argv   []string
	marker bool
}

// waitPaneRun waits for the paneStub beside claude to finish its run and returns what it recorded.
func waitPaneRun(t *testing.T, claude string) paneRun {
	t.Helper()
	dir := filepath.Dir(claude)
	deadline := time.Now().Add(10 * time.Second)
	for {
		_, err := os.Stat(filepath.Join(dir, "done"))
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("stat claude done: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("claude did not finish within 10s")
		}
		time.Sleep(50 * time.Millisecond)
	}
	argvData, err := os.ReadFile(filepath.Join(dir, "argv"))
	if err != nil {
		t.Fatalf("read claude argv: %v", err)
	}
	envData, err := os.ReadFile(filepath.Join(dir, "env"))
	if err != nil {
		t.Fatalf("read claude env: %v", err)
	}
	// Each argument ends in NUL, so the split leaves one empty element after the last argument.
	parts := strings.Split(string(argvData), "\x00")
	argv := parts[:len(parts)-1]
	marker := slices.Contains(strings.Split(string(envData), "\n"), paneMarker+"=1")
	return paneRun{argv: argv, marker: marker}
}

// startPaneServer starts the private server with probe as its default-shell, and a keeper session in keeperDir
// that keeps the server alive.
func startPaneServer(t *testing.T, client *tmuxctl.Client, probe, keeperDir string) {
	t.Helper()
	startSession(t, client, "keeper", keeperDir)
	mustRun(t, client, "set-option", "-g", "default-shell", probe)
}

// paneDir returns a new directory named example-dir under a fresh temporary directory.
func paneDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "example-dir")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	return dir
}

// paneForm is one form of a pane command with arguments.
type paneForm struct {
	name   string
	action func(dir string) Action
	want   []string
}

func paneForms() []paneForm {
	return []paneForm{
		{
			name:   "resume",
			action: func(dir string) Action { return claudeKindAction(ActionResume, dir) },
			want:   wantClaudeArgv(ActionResume),
		},
		{
			name:   "continue",
			action: func(dir string) Action { return claudeKindAction(ActionContinue, dir) },
			want:   wantClaudeArgv(ActionContinue),
		},
		{
			name:   "prompt",
			action: func(dir string) Action { return Action{Kind: ActionPane, Dir: dir, Prompt: "example prompt"} },
			want:   []string{"--", "example prompt"},
		},
	}
}

func TestPaneCommandRunsThroughDefaultShell(t *testing.T) {
	for _, form := range paneForms() {
		for _, placement := range []string{"new session", "new window"} {
			t.Run(form.name+"/"+placement, func(t *testing.T) {
				probe := probeShell(t, "sh")
				client := newClient(t)
				dir := paneDir(t)
				wantSession := "example-dir"
				keeperDir := t.TempDir()
				if placement == "new window" {
					wantSession = "keeper"
					keeperDir = dir
				}
				startPaneServer(t, client, probe, keeperDir)
				if placement == "new window" {
					setDir(t, client, "keeper", dir)
				}
				claude := paneStub(t)

				got, err := ExecuteAction(t.Context(), executeConfig(client, claude), form.action(dir))
				if err != nil {
					t.Fatalf("ExecuteAction(%s): %v", form.name, err)
				}
				if got.Session != wantSession {
					t.Errorf("ExecuteAction(%s) session = %q, want %q", form.name, got.Session, wantSession)
				}
				run := waitPaneRun(t, claude)
				if !run.marker {
					t.Errorf("pane environment lacks %s: the command did not run through default-shell", paneMarker)
				}
				if !slices.Equal(run.argv, form.want) {
					t.Errorf("claude argv = %q, want %q", run.argv, form.want)
				}
			})
		}
	}
}

func TestPaneCommandColdServerRunsThroughDefaultShell(t *testing.T) {
	probe := probeShell(t, "sh")
	home := t.TempDir()
	// No server is running. The server starts with SHELL as its default-shell, and the empty HOME and
	// XDG_CONFIG_HOME keep the user's tmux config from setting it.
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("SHELL", probe)
	client := newClient(t)
	dir := paneDir(t)
	claude := paneStub(t)

	got, err := ExecuteAction(t.Context(), executeConfig(client, claude),
		Action{Kind: ActionPane, Dir: dir, Prompt: "example prompt"})
	if err != nil {
		t.Fatalf("ExecuteAction(pane) on a cold server: %v", err)
	}
	if want := (Target{Session: "example-dir"}); got != want {
		t.Errorf("ExecuteAction(pane) = %+v, want %+v", got, want)
	}
	run := waitPaneRun(t, claude)
	if !run.marker {
		t.Errorf("pane environment lacks %s on a cold server: the command did not run through default-shell",
			paneMarker)
	}
	if want := []string{"--", "example prompt"}; !slices.Equal(run.argv, want) {
		t.Errorf("claude argv = %q, want %q", run.argv, want)
	}
}

func TestPaneCommandOtherShellStartsDirectly(t *testing.T) {
	probe := probeShell(t, "probe-shell")
	client := newClient(t)
	dir := paneDir(t)
	startPaneServer(t, client, probe, t.TempDir())
	claude := paneStub(t)

	action := claudeKindAction(ActionContinue, dir)
	if _, err := ExecuteAction(t.Context(), executeConfig(client, claude), action); err != nil {
		t.Fatalf("ExecuteAction(continue): %v", err)
	}
	run := waitPaneRun(t, claude)
	if run.marker {
		t.Errorf("pane environment has %s under probe-shell: the command ran through a non-POSIX default-shell",
			paneMarker)
	}
	if want := []string{"--continue"}; !slices.Equal(run.argv, want) {
		t.Errorf("claude argv = %q, want %q", run.argv, want)
	}
}

func TestPaneCommandArgumentsReachClaudeByteForByte(t *testing.T) {
	cases := []struct {
		name   string
		prompt string
	}{
		{name: "spaces", prompt: "example prompt with spaces"},
		{name: "single quotes", prompt: "it's 'quoted'"},
		{name: "double quotes", prompt: `say "hi"`},
		{name: "dollar", prompt: "cost $HOME ${PATH}"},
		{name: "backticks", prompt: "run `id`"},
		{name: "semicolon", prompt: "one; two"},
		{name: "trailing semicolon", prompt: "finish;"},
		{name: "format", prompt: "#{session_name}"},
		{name: "newline", prompt: "first\nsecond"},
		{name: "leading dash", prompt: "-x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			probe := probeShell(t, "sh")
			client := newClient(t)
			dir := paneDir(t)
			startPaneServer(t, client, probe, t.TempDir())
			claude := paneStub(t)

			if _, err := ExecuteAction(t.Context(), executeConfig(client, claude),
				Action{Kind: ActionPane, Dir: dir, Prompt: tc.prompt}); err != nil {
				t.Fatalf("ExecuteAction(pane): %v", err)
			}
			run := waitPaneRun(t, claude)
			if !run.marker {
				t.Errorf("pane environment lacks %s: the command did not run through default-shell", paneMarker)
			}
			if want := []string{"--", tc.prompt}; !slices.Equal(run.argv, want) {
				t.Errorf("claude argv = %q, want %q", run.argv, want)
			}
		})
	}
}
