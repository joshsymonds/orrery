package open

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// stampEnvJSON is the --settings document of the example stamp that stampedHome writes: its whole env, keys sorted.
const stampEnvJSON = `{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:9/ctx/example","EXAMPLE_KEY":"v"}}`

// stampPane is one Claude pane a stamp reaches: how it is opened in a directory, and the claude arguments it runs
// with no stamp.
type stampPane struct {
	name  string
	argv  []string
	start func(ctx context.Context, cfg Config, dir string) (Target, error)
}

// stampPanes returns the Claude panes a stamp reaches: Execute's new session, ExecuteAction's pane with a prompt,
// and the resume, continue and fresh kinds.
func stampPanes() []stampPane {
	kinds := claudeKinds()
	panes := make([]stampPane, 0, len(kinds)+2)
	panes = append(panes,
		stampPane{
			name: "new",
			argv: nil,
			start: func(ctx context.Context, cfg Config, dir string) (Target, error) {
				return Execute(ctx, cfg, Plan{Session: "example-new", Dir: dir})
			},
		},
		stampPane{
			name: "pane with prompt",
			argv: []string{"--", "example prompt"},
			start: func(ctx context.Context, cfg Config, dir string) (Target, error) {
				return ExecuteAction(ctx, cfg, Action{Kind: ActionPane, Dir: dir, Prompt: "example prompt"})
			},
		},
	)
	for _, kind := range kinds {
		panes = append(panes, stampPane{
			name: string(kind),
			argv: wantClaudeArgv(kind),
			start: func(ctx context.Context, cfg Config, dir string) (Target, error) {
				return ExecuteAction(ctx, cfg, claudeKindAction(kind, dir))
			},
		})
	}
	return panes
}

// stampedHome returns a synthetic home under t.TempDir() and two folders directly inside it: example-stamped, whose
// .claude/settings.json sets the example stamp's env, and example-plain, which has no settings. It returns the home,
// the stamped folder, and the plain folder.
func stampedHome(t *testing.T) (string, string, string) {
	t.Helper()
	home := t.TempDir()
	stamped := filepath.Join(home, "example-stamped")
	plain := filepath.Join(home, "example-plain")
	for _, dir := range []string{stamped, plain} {
		if err := os.Mkdir(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	writeSettings(t, stamped, `{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:9/ctx/example","EXAMPLE_KEY":"v"}}`)
	return home, stamped, plain
}

// writeSettings writes content as the .claude/settings.json of dir.
func writeSettings(t *testing.T, dir, content string) {
	t.Helper()
	claudeDir := filepath.Join(dir, ".claude")
	if err := os.Mkdir(claudeDir, 0o750); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(claudeDir, "settings.json"), content)
}

// openStampPane opens pane in dir with home as the Config's Home, on a fresh tmux server with a keeper session, and
// returns the path of the fake claude and the error from opening the pane.
func openStampPane(t *testing.T, pane stampPane, home, dir string) (string, error) {
	t.Helper()
	client := newClient(t)
	startSession(t, client, "keeper", t.TempDir())
	claude := installClaude(t, "", 0)
	cfg := executeConfig(client, claude)
	cfg.Home = home
	_, err := pane.start(t.Context(), cfg, dir)
	return claude, err
}

func TestStampedPanesRunClaudeWithSettings(t *testing.T) {
	home, stamped, _ := stampedHome(t)
	for _, pane := range stampPanes() {
		t.Run(pane.name, func(t *testing.T) {
			claude, err := openStampPane(t, pane, home, stamped)
			if err != nil {
				t.Fatalf("open %s pane: %v", pane.name, err)
			}
			run := waitClaudeRun(t, claude)
			if run.cwd != stamped {
				t.Errorf("claude cwd = %q, want %q", run.cwd, stamped)
			}
			want := slices.Concat([]string{"--settings", stampEnvJSON}, pane.argv)
			if !slices.Equal(run.args, want) {
				t.Errorf("claude argv = %q, want %q", run.args, want)
			}
		})
	}
}

func TestUnstampedPanesRunClaudeWithTodayArgv(t *testing.T) {
	home, _, plain := stampedHome(t)
	for _, pane := range stampPanes() {
		t.Run(pane.name, func(t *testing.T) {
			claude, err := openStampPane(t, pane, home, plain)
			if err != nil {
				t.Fatalf("open %s pane: %v", pane.name, err)
			}
			run := waitClaudeRun(t, claude)
			if run.cwd != plain {
				t.Errorf("claude cwd = %q, want %q", run.cwd, plain)
			}
			if !slices.Equal(run.args, pane.argv) {
				t.Errorf("claude argv = %q, want %q", run.args, pane.argv)
			}
		})
	}
}

// A new folder inherits its parent's stamp, so the stamp is found only once the folder exists.
func TestExecuteNewFolderUnderStampRunsClaudeWithSettings(t *testing.T) {
	home, stamped, _ := stampedHome(t)
	client := newClient(t)
	startSession(t, client, "keeper", t.TempDir())
	claude := installClaude(t, "", 0)
	cfg := executeConfig(client, claude)
	cfg.Home = home
	dir := filepath.Join(stamped, "example-new")

	if _, err := Execute(t.Context(), cfg, Plan{Session: "example-new", Dir: dir, MakeDir: true}); err != nil {
		t.Fatalf("Execute(new folder under stamp): %v", err)
	}
	run := waitClaudeRun(t, claude)
	if run.cwd != dir {
		t.Errorf("claude cwd = %q, want %q", run.cwd, dir)
	}
	if want := []string{"--settings", stampEnvJSON}; !slices.Equal(run.args, want) {
		t.Errorf("claude argv = %q, want %q", run.args, want)
	}
}

func TestUnparseableStampStartsNoPane(t *testing.T) {
	home := t.TempDir()
	broken := filepath.Join(home, "example-broken")
	if err := os.Mkdir(broken, 0o750); err != nil {
		t.Fatal(err)
	}
	writeSettings(t, broken, "{not json")
	for _, pane := range stampPanes() {
		t.Run(pane.name, func(t *testing.T) {
			client := newClient(t)
			startSession(t, client, "keeper", t.TempDir())
			claude := installClaude(t, "", 0)
			cfg := executeConfig(client, claude)
			cfg.Home = home

			if _, err := pane.start(t.Context(), cfg, broken); err == nil {
				t.Fatalf("open %s pane with an unparseable stamp returned nil error", pane.name)
			}
			if runs := claudeRuns(t, claude); len(runs) != 0 {
				t.Errorf("claude ran %d times, want 0", len(runs))
			}
			if got := sessionNames(t, client); !slices.Equal(got, []string{"keeper"}) {
				t.Errorf("sessions = %q, want only keeper", got)
			}
		})
	}
}
