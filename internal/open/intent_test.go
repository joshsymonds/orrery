package open

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// claudeRun is what one fake claude run recorded.
type claudeRun struct {
	args  []string
	env   []string
	cwd   string
	stdin string
}

// installClaude copies the scripted fake into a fresh directory and returns the path of the copy, named claude.
// A non-empty reply is printed on each run; a non-zero status is the run's exit status.
func installClaude(t *testing.T, reply string, status int) string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("testdata", "fake-claude.sh"))
	if err != nil {
		t.Fatalf("read fake claude: %v", err)
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, src, 0o755); err != nil {
		t.Fatalf("install fake claude: %v", err)
	}
	if reply != "" {
		writeFile(t, filepath.Join(dir, "reply"), reply)
	}
	if status != 0 {
		writeFile(t, filepath.Join(dir, "exit"), strconv.Itoa(status))
	}
	return bin
}

// recordLines splits a record file into lines, keeping empty lines such as an empty argument.
func recordLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	text := strings.TrimSuffix(string(data), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// claudeRuns returns what each run of the fake beside bin recorded, in no particular order.
func claudeRuns(t *testing.T, bin string) []claudeRun {
	t.Helper()
	dirs, err := filepath.Glob(filepath.Join(filepath.Dir(bin), "calls", "run.*"))
	if err != nil {
		t.Fatalf("glob claude runs: %v", err)
	}
	runs := make([]claudeRun, 0, len(dirs))
	for _, dir := range dirs {
		cwd, err := os.ReadFile(filepath.Join(dir, "cwd"))
		if err != nil {
			t.Fatalf("read claude cwd: %v", err)
		}
		stdin, err := os.ReadFile(filepath.Join(dir, "stdin"))
		if err != nil {
			t.Fatalf("read claude stdin: %v", err)
		}
		runs = append(runs, claudeRun{
			args:  recordLines(t, filepath.Join(dir, "args")),
			env:   recordLines(t, filepath.Join(dir, "env")),
			cwd:   strings.TrimSuffix(string(cwd), "\n"),
			stdin: string(stdin),
		})
	}
	return runs
}

// startLabelledPane starts session name and sets the pane options a Pane reads from it; an empty value leaves
// that option unset. It returns the pane ID.
func startLabelledPane(t *testing.T, client *tmuxctl.Client, name, state, label, summary, context string) string {
	t.Helper()
	startSession(t, client, name, t.TempDir())
	id := strings.TrimSuffix(mustRun(t, client, "display-message", "-p", "-t", name, "#{pane_id}"), "\n")
	options := map[string]string{
		protocol.OptionState:   state,
		protocol.OptionLabel:   label,
		protocol.OptionSummary: summary,
		protocol.OptionContext: context,
	}
	for option, value := range options {
		if value != "" {
			mustRun(t, client, "set-option", "-p", "-t", id, option, value)
		}
	}
	return id
}

// startDefaultPane starts session name as orreryd records a default-context pane: the pane options a Pane reads
// are set, and @orrery_context is set to "". It returns the pane ID.
func startDefaultPane(t *testing.T, client *tmuxctl.Client, name, state, label, summary string) string {
	t.Helper()
	id := startLabelledPane(t, client, name, state, label, summary, "")
	mustRun(t, client, "set-option", "-p", "-t", id, protocol.OptionContext, "")
	return id
}

// zoxideListing is the `zoxide query -l -s` output for count directories named dir-01, dir-02, ... under root.
func zoxideListing(root string, count int) string {
	var b strings.Builder
	for i := 1; i <= count; i++ {
		b.WriteString(scored(filepath.Join(root, fmt.Sprintf("dir-%02d", i))))
	}
	return b.String()
}

func intentConfig(client *tmuxctl.Client, zoxide, claude, root string) Config {
	return Config{Tmux: client, Zoxide: zoxide, Claude: claude, Root: root}
}

func TestIntentStdinCarriesTextDirectoriesAndEligiblePanes(t *testing.T) {
	client := newClient(t)
	root := t.TempDir()
	eligibleA := startDefaultPane(t, client, "example-session-a",
		"waiting", "example-label-a", "example summary a")
	unsetContext := startLabelledPane(t, client, "example-session-b",
		"waiting", "example-label-b", "example summary b", "")
	withContext := startLabelledPane(t, client, "example-session-c",
		"working", "example-label-c", "example summary c", "example-context")
	eligibleD := startDefaultPane(t, client, "example-session-d",
		"done", "example-label-d", "example summary d")
	unsetState := startDefaultPane(t, client, "example-session-e",
		"", "example-label-e", "example summary e")
	zoxide := installZoxide(t, zoxideListing(root, 32), 0)
	claude := installClaude(t, "pane d1\n", 0)

	if _, err := Intent(t.Context(), intentConfig(client, zoxide, claude, root), "example text"); err != nil {
		t.Fatalf("Intent: %v", err)
	}
	runs := claudeRuns(t, claude)
	if len(runs) != 1 {
		t.Fatalf("claude ran %d times, want 1", len(runs))
	}
	stdin := runs[0].stdin
	lines := strings.Split(stdin, "\n")
	for _, want := range []string{
		"example text",
		"d1 dir-01",
		"d30 dir-30",
		eligibleA + " example-label-a: example summary a",
		eligibleD + " example-label-d: example summary d",
	} {
		if !slices.Contains(lines, want) {
			t.Errorf("stdin lacks line %q", want)
		}
	}
	unwantedText := []string{
		root, "dir-31", "example-context", "example-label-b", "example-label-c", "example summary c", "example-label-e",
		"example-session",
	}
	for _, unwanted := range unwantedText {
		if strings.Contains(stdin, unwanted) {
			t.Errorf("stdin contains %q, want it absent", unwanted)
		}
	}
	for _, excluded := range []string{unsetContext, withContext, unsetState} {
		for _, line := range lines {
			if strings.HasPrefix(line, excluded+" ") {
				t.Errorf("stdin carries pane %s: line %q, want it excluded", excluded, line)
			}
		}
	}
}

func TestIntentRunArgvEnvAndCwd(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", "https://example.invalid/ctx/example-context")
	t.Setenv(protocol.EnvSummarizer, "0")
	root := t.TempDir()
	zoxide := installZoxide(t, zoxideListing(root, 1), 0)
	claude := installClaude(t, "pane d1\n", 0)

	if _, err := Intent(t.Context(), intentConfig(newClient(t), zoxide, claude, root), "example text"); err != nil {
		t.Fatalf("Intent: %v", err)
	}
	runs := claudeRuns(t, claude)
	if len(runs) != 1 {
		t.Fatalf("claude ran %d times, want 1", len(runs))
	}
	wantArgs := []string{
		"-p", "--model", protocol.DefaultSummaryModel,
		"--settings", `{"disableAllHooks":true}`,
		"--no-session-persistence",
		"--tools", "",
		"--max-turns", "1",
	}
	if !slices.Equal(runs[0].args, wantArgs) {
		t.Errorf("claude argv = %q, want %q", runs[0].args, wantArgs)
	}
	if !slices.Equal(runs[0].env, []string{protocol.EnvSummarizer + "=" + protocol.SummarizerValue}) {
		t.Errorf("claude env = %q, want only %s=%s", runs[0].env, protocol.EnvSummarizer, protocol.SummarizerValue)
	}
	if runs[0].cwd != "/" {
		t.Errorf("claude cwd = %q, want /", runs[0].cwd)
	}
}

func TestIntentReplyKindsParseInRankOrder(t *testing.T) {
	client := newClient(t)
	root := t.TempDir()
	pane := startDefaultPane(t, client, "example-session-a", "waiting", "example-label-a", "example summary a")
	zoxide := installZoxide(t, zoxideListing(root, 3), 0)
	dirs := []string{filepath.Join(root, "dir-01"), filepath.Join(root, "dir-02"), filepath.Join(root, "dir-03")}
	claude := installClaude(t, "new example-project d2\njump "+pane+"\nPANE d1\ntask: yes\n", 0)

	got, err := Intent(t.Context(), intentConfig(client, zoxide, claude, root), "example text")
	if err != nil {
		t.Fatalf("Intent: %v", err)
	}
	want := []Action{
		{Kind: ActionProject, Name: "example-project", Dir: dirs[1], Prompt: "example text"},
		{Kind: ActionJump, Pane: pane},
		{Kind: ActionPane, Dir: dirs[0], Prompt: "example text"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("Intent() = %+v, want %+v", got, want)
	}
}

func TestIntentTaskNoLeavesPromptEmpty(t *testing.T) {
	client := newClient(t)
	root := t.TempDir()
	pane := startDefaultPane(t, client, "example-session-a", "waiting", "example-label-a", "example summary a")
	zoxide := installZoxide(t, zoxideListing(root, 1), 0)
	claude := installClaude(t, "pane d1\njump "+pane+"\nnew example-project default\ntask: no\n", 0)

	got, err := Intent(t.Context(), intentConfig(client, zoxide, claude, root), "example text")
	if err != nil {
		t.Fatalf("Intent: %v", err)
	}
	want := []Action{
		{Kind: ActionPane, Dir: filepath.Join(root, "dir-01")},
		{Kind: ActionJump, Pane: pane},
		{Kind: ActionProject, Name: "example-project", Dir: root},
	}
	if !slices.Equal(got, want) {
		t.Errorf("Intent() = %+v, want %+v", got, want)
	}
}

func TestIntentDropsBadReferences(t *testing.T) {
	client := newClient(t)
	root := t.TempDir()
	unsetContext := startLabelledPane(t, client, "example-session-b",
		"waiting", "example-label-b", "example summary b", "")
	withContext := startLabelledPane(t, client, "example-session-c",
		"working", "example-label-c", "example summary c", "example-context")
	unsetState := startDefaultPane(t, client, "example-session-e", "", "example-label-e", "example summary e")
	zoxide := installZoxide(t, zoxideListing(root, 3), 0)
	claude := installClaude(t, strings.Join([]string{
		"jump " + unsetContext,
		"jump " + withContext,
		"jump " + unsetState,
		"jump %999",
		"pane d4",
		"pane d0",
		"pane d1 extra",
		"new example/bad d1",
		"new .. d1",
		"new example-kept d1",
		"new example-default default",
		"task: yes",
	}, "\n")+"\n", 0)
	dirs := []string{filepath.Join(root, "dir-01"), filepath.Join(root, "dir-02"), filepath.Join(root, "dir-03")}

	got, err := Intent(t.Context(), intentConfig(client, zoxide, claude, root), "example text")
	if err != nil {
		t.Fatalf("Intent: %v", err)
	}
	want := []Action{
		{Kind: ActionProject, Name: "example-kept", Dir: dirs[0], Prompt: "example text"},
		{Kind: ActionProject, Name: "example-default", Dir: root, Prompt: "example text"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("Intent() = %+v, want %+v", got, want)
	}
}

func TestIntentNoValidActionIsError(t *testing.T) {
	cases := map[string]string{
		"empty reply":            "",
		"only task line":         "task: yes\n",
		"unknown keyword":        "open %1\n",
		"reference without ref":  "pane\n",
		"unlisted pane only":     "jump %999\ntask: no\n",
		"malformed task keyword": "task: maybe\n",
	}
	for name, reply := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			zoxide := installZoxide(t, zoxideListing(root, 1), 0)
			claude := installClaude(t, reply, 0)
			cfg := intentConfig(newClient(t), zoxide, claude, root)
			if _, err := Intent(t.Context(), cfg, "example text"); err == nil {
				t.Fatalf("Intent with reply %q returned nil error", reply)
			}
		})
	}
}

func TestIntentFailedRunsAreErrors(t *testing.T) {
	root := t.TempDir()
	zoxide := installZoxide(t, zoxideListing(root, 1), 0)
	failingClaude := installClaude(t, "pane d1\n", 2)
	cfg := intentConfig(newClient(t), zoxide, failingClaude, root)
	if _, err := Intent(t.Context(), cfg, "example text"); err == nil {
		t.Error("Intent with a failing claude returned nil error")
	}

	failingZoxide := installZoxide(t, "", 2)
	claude := installClaude(t, "pane d1\n", 0)
	cfg = intentConfig(newClient(t), failingZoxide, claude, root)
	if _, err := Intent(t.Context(), cfg, "example text"); err == nil {
		t.Error("Intent with a failing zoxide returned nil error")
	}
}

func TestRunClaudeTimeoutIsError(t *testing.T) {
	claude := installClaude(t, "pane d1\n", 0)
	writeFile(t, filepath.Join(filepath.Dir(claude), "hold"), "")

	if _, err := runClaude(t.Context(), claude, "example stdin", 200*time.Millisecond); err == nil {
		t.Fatal("runClaude past its timeout returned nil error")
	}
}

func TestIntentCreatesNothing(t *testing.T) {
	client := newClient(t)
	root := t.TempDir()
	startDefaultPane(t, client, "example-session-a", "waiting", "example-label-a", "example summary a")
	claude := installClaude(t, "new example-fresh d1\nnew example-made default\npane d1\ntask: yes\n", 0)
	cfg := intentConfig(client, installZoxide(t, zoxideListing(root, 1), 0), claude, root)
	before, err := listSessions(t.Context(), cfg)
	if err != nil {
		t.Fatalf("listSessions before: %v", err)
	}

	if _, err := Intent(t.Context(), cfg, "example text"); err != nil {
		t.Fatalf("Intent: %v", err)
	}
	for _, path := range []string{filepath.Join(root, "example-fresh"), filepath.Join(root, "example-made")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("stat %s after Intent: err %v, want not exist", path, err)
		}
	}
	after, err := listSessions(t.Context(), cfg)
	if err != nil {
		t.Fatalf("listSessions after: %v", err)
	}
	if len(after) != len(before) {
		t.Errorf("sessions after Intent = %d, want %d", len(after), len(before))
	}
}

func TestIntentWithoutTmuxServerHasNoPanes(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "dir-01")
	zoxide := installZoxide(t, scored(dir), 0)
	claude := installClaude(t, "pane d1\ntask: no\n", 0)

	got, err := Intent(t.Context(), intentConfig(newClient(t), zoxide, claude, root), "example text")
	if err != nil {
		t.Fatalf("Intent with no tmux server: %v", err)
	}
	if want := []Action{{Kind: ActionPane, Dir: dir}}; !slices.Equal(got, want) {
		t.Errorf("Intent() = %+v, want %+v", got, want)
	}
}

// waitClaudeRun waits for the fake beside bin to start a run and returns that run. The fake writes its stdin
// record last of the run's records, before it blocks reading stdin, so the record's presence means the run has
// recorded everything. The deadline bounds the wait for a pane that has not started.
func waitClaudeRun(t *testing.T, bin string) claudeRun {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		matches, err := filepath.Glob(filepath.Join(filepath.Dir(bin), "calls", "run.*", "stdin"))
		if err != nil {
			t.Fatalf("glob claude stdin records: %v", err)
		}
		if len(matches) > 0 {
			runs := claudeRuns(t, bin)
			if len(runs) != 1 {
				t.Fatalf("claude ran %d times, want 1", len(runs))
			}
			return runs[0]
		}
		if time.Now().After(deadline) {
			t.Fatal("claude did not start within 10s")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// activePane returns the pane ID of session name's first pane.
func activePane(t *testing.T, client *tmuxctl.Client, name string) string {
	t.Helper()
	return strings.TrimSpace(mustRun(t, client, "display-message", "-p", "-t", name, "#{pane_id}"))
}

func TestExecuteActionJumpSelectsExistingPane(t *testing.T) {
	client := newClient(t)
	startSession(t, client, "example-session-a", t.TempDir())
	pane := activePane(t, client, "example-session-a")
	cfg := executeConfig(client, installClaude(t, "", 0))

	got, err := ExecuteAction(t.Context(), cfg, Action{Kind: ActionJump, Pane: pane})
	if err != nil {
		t.Fatalf("ExecuteAction(jump): %v", err)
	}
	if want := (Target{Session: "example-session-a", Pane: pane}); got != want {
		t.Errorf("ExecuteAction(jump) = %+v, want %+v", got, want)
	}
}

func TestExecuteActionJumpMissingPaneIsError(t *testing.T) {
	client := newClient(t)
	startSession(t, client, "example-session-a", t.TempDir())
	cfg := executeConfig(client, installClaude(t, "", 0))

	if _, err := ExecuteAction(t.Context(), cfg, Action{Kind: ActionJump, Pane: "%999"}); err == nil {
		t.Fatal("ExecuteAction(jump to a missing pane) returned nil error")
	}
	if got := sessionNames(t, client); !slices.Equal(got, []string{"example-session-a"}) {
		t.Errorf("sessions = %q, want only example-session-a", got)
	}
}

func TestExecuteActionPaneOpensWindowInSessionWithSameDir(t *testing.T) {
	client := newClient(t)
	dir := filepath.Join(t.TempDir(), "example-dir")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	startSession(t, client, "example-session-a", t.TempDir())
	setDir(t, client, "example-session-a", t.TempDir())
	startSession(t, client, "example-session-b", t.TempDir())
	setDir(t, client, "example-session-b", dir)
	claude := installClaude(t, "", 0)

	got, err := ExecuteAction(t.Context(), executeConfig(client, claude),
		Action{Kind: ActionPane, Dir: dir, Prompt: "example prompt"})
	if err != nil {
		t.Fatalf("ExecuteAction(pane): %v", err)
	}
	if got.Session != "example-session-b" || got.Pane == "" {
		t.Fatalf("ExecuteAction(pane) = %+v, want session example-session-b with a new pane", got)
	}
	panes := strings.Fields(mustRun(t, client, "list-panes", "-s", "-t", "=example-session-b:", "-F", "#{pane_id}"))
	if !slices.Contains(panes, got.Pane) || len(panes) != 2 {
		t.Errorf("panes of example-session-b = %q, want the original pane and %s", panes, got.Pane)
	}
	run := waitClaudeRun(t, claude)
	if run.cwd != dir {
		t.Errorf("claude cwd = %q, want %q", run.cwd, dir)
	}
	if want := []string{"--", "example prompt"}; !slices.Equal(run.args, want) {
		t.Errorf("claude argv = %q, want %q", run.args, want)
	}
	if got := sessionNames(t, client); !slices.Equal(got, []string{"example-session-a", "example-session-b"}) {
		t.Errorf("sessions = %q, want example-session-a and example-session-b", got)
	}
}

func TestExecuteActionPaneCreatesSessionWithDirAndNoPrompt(t *testing.T) {
	client := newClient(t)
	startSession(t, client, "keeper", t.TempDir())
	dir := filepath.Join(t.TempDir(), "example-dir")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	claude := installClaude(t, "", 0)

	got, err := ExecuteAction(t.Context(), executeConfig(client, claude), Action{Kind: ActionPane, Dir: dir})
	if err != nil {
		t.Fatalf("ExecuteAction(pane): %v", err)
	}
	if want := (Target{Session: "example-dir"}); got != want {
		t.Errorf("ExecuteAction(pane) = %+v, want %+v", got, want)
	}
	if got := dirOption(t, client, "example-dir"); got != dir {
		t.Errorf("@orrery_dir = %q, want %q", got, dir)
	}
	run := waitClaudeRun(t, claude)
	if run.cwd != dir {
		t.Errorf("claude cwd = %q, want %q", run.cwd, dir)
	}
	if len(run.args) != 0 {
		t.Errorf("claude argv = %q, want none", run.args)
	}
	if got := sessionNames(t, client); !slices.Equal(got, []string{"example-dir", "keeper"}) {
		t.Errorf("sessions = %q, want example-dir and keeper", got)
	}
}

func TestExecuteActionProjectCreatesExactlyRootNameAndSessionWithPrompt(t *testing.T) {
	client := newClient(t)
	startSession(t, client, "keeper", t.TempDir())
	root := t.TempDir()
	claude := installClaude(t, "", 0)

	got, err := ExecuteAction(t.Context(), executeConfig(client, claude),
		Action{Kind: ActionProject, Name: "example-project", Dir: root, Prompt: "example prompt"})
	if err != nil {
		t.Fatalf("ExecuteAction(project): %v", err)
	}
	if want := (Target{Session: "example-project"}); got != want {
		t.Errorf("ExecuteAction(project) = %+v, want %+v", got, want)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != "example-project" {
		t.Errorf("root entries = %v, %v; want only example-project", entries, err)
	}
	if got := dirOption(t, client, "example-project"); got != filepath.Join(root, "example-project") {
		t.Errorf("@orrery_dir = %q, want %q", got, filepath.Join(root, "example-project"))
	}
	run := waitClaudeRun(t, claude)
	if run.cwd != filepath.Join(root, "example-project") {
		t.Errorf("claude cwd = %q, want %q", run.cwd, filepath.Join(root, "example-project"))
	}
	if want := []string{"--", "example prompt"}; !slices.Equal(run.args, want) {
		t.Errorf("claude argv = %q, want %q", run.args, want)
	}
}

func TestExecuteActionProjectExistingDirIsErrorWithNoSession(t *testing.T) {
	client := newClient(t)
	startSession(t, client, "keeper", t.TempDir())
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "example-project"), 0o750); err != nil {
		t.Fatal(err)
	}

	action := Action{Kind: ActionProject, Name: "example-project", Dir: root}
	if _, err := ExecuteAction(t.Context(), executeConfig(client, installClaude(t, "", 0)), action); err == nil {
		t.Fatal("ExecuteAction(project) over an existing directory returned nil error")
	}
	if got := sessionNames(t, client); !slices.Equal(got, []string{"keeper"}) {
		t.Errorf("sessions = %q, want only keeper", got)
	}
}

func TestExecuteActionProjectBadInputIsErrorWithNothingCreated(t *testing.T) {
	cases := map[string]func(root string) Action{
		"slash in name": func(root string) Action {
			return Action{Kind: ActionProject, Name: "example/bad", Dir: root}
		},
		"dotdot name": func(root string) Action {
			return Action{Kind: ActionProject, Name: "..", Dir: root}
		},
		"empty name": func(root string) Action {
			return Action{Kind: ActionProject, Name: "", Dir: root}
		},
		"relative root": func(string) Action {
			return Action{Kind: ActionProject, Name: "example-project", Dir: "relative-root"}
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			client := newClient(t)
			startSession(t, client, "keeper", t.TempDir())
			root := t.TempDir()
			action := build(root)
			cfg := executeConfig(client, installClaude(t, "", 0))
			if _, err := ExecuteAction(t.Context(), cfg, action); err == nil {
				t.Fatalf("ExecuteAction(%+v) returned nil error", action)
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Errorf("root entries = %v, %v; want none", entries, err)
			}
			if got := sessionNames(t, client); !slices.Equal(got, []string{"keeper"}) {
				t.Errorf("sessions = %q, want only keeper", got)
			}
		})
	}
}

func TestExecuteActionUnknownKindIsErrorWithNoSession(t *testing.T) {
	client := newClient(t)
	startSession(t, client, "keeper", t.TempDir())
	dir := t.TempDir()

	action := Action{Kind: ActionKind("bogus"), Dir: dir}
	if _, err := ExecuteAction(t.Context(), executeConfig(client, installClaude(t, "", 0)), action); err == nil {
		t.Fatal("ExecuteAction(unknown kind) returned nil error")
	}
	if got := sessionNames(t, client); !slices.Equal(got, []string{"keeper"}) {
		t.Errorf("sessions = %q, want only keeper", got)
	}
}

func TestExecutePassesNoPrompt(t *testing.T) {
	client := newClient(t)
	startSession(t, client, "keeper", t.TempDir())
	dir := t.TempDir()
	claude := installClaude(t, "", 0)

	if _, err := Execute(t.Context(), executeConfig(client, claude), Plan{Session: "proj", Dir: dir}); err != nil {
		t.Fatalf("Execute(Dir plan): %v", err)
	}
	run := waitClaudeRun(t, claude)
	if len(run.args) != 0 {
		t.Errorf("claude argv = %q, want none", run.args)
	}
}

// literalDirName is a directory name tmux would format-expand if it reached -c unescaped.
const literalDirName = "example-#{session_name}#dir"

// literalPrompt ends in ";", which tmux reads as a command separator unless it reaches tmux escaped, and
// names a format that tmux would expand if it were expanded.
const literalPrompt = "example prompt #{session_name};"

// assertLiteralRun checks the Claude pane that ran for session: its directory and argv exactly as given, and
// the @orrery_dir of session exactly as given.
func assertLiteralRun(t *testing.T, client *tmuxctl.Client, claude, session, dir string) {
	t.Helper()
	run := waitClaudeRun(t, claude)
	if run.cwd != dir {
		t.Errorf("claude cwd = %q, want %q", run.cwd, dir)
	}
	if want := []string{"--", literalPrompt}; !slices.Equal(run.args, want) {
		t.Errorf("claude argv = %q, want %q", run.args, want)
	}
	if got := dirOption(t, client, session); got != dir {
		t.Errorf("@orrery_dir of %s = %q, want %q", session, got, dir)
	}
}

func TestExecuteActionArgumentsReachTmuxLiterally(t *testing.T) {
	t.Run("pane creates session", func(t *testing.T) {
		client := newClient(t)
		startSession(t, client, "keeper", t.TempDir())
		dir := filepath.Join(t.TempDir(), literalDirName)
		if err := os.Mkdir(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		claude := installClaude(t, "", 0)

		got, err := ExecuteAction(t.Context(), executeConfig(client, claude),
			Action{Kind: ActionPane, Dir: dir, Prompt: literalPrompt})
		if err != nil {
			t.Fatalf("ExecuteAction(pane): %v", err)
		}
		if want := (Target{Session: "example-_{session_name}_dir"}); got != want {
			t.Errorf("ExecuteAction(pane) = %+v, want %+v", got, want)
		}
		assertLiteralRun(t, client, claude, got.Session, dir)
	})

	t.Run("pane opens window in matching session", func(t *testing.T) {
		client := newClient(t)
		dir := filepath.Join(t.TempDir(), literalDirName)
		if err := os.Mkdir(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		startSession(t, client, "example-session", t.TempDir())
		setDir(t, client, "example-session", dir)
		claude := installClaude(t, "", 0)

		got, err := ExecuteAction(t.Context(), executeConfig(client, claude),
			Action{Kind: ActionPane, Dir: dir, Prompt: literalPrompt})
		if err != nil {
			t.Fatalf("ExecuteAction(pane): %v", err)
		}
		if got.Session != "example-session" || got.Pane == "" {
			t.Fatalf("ExecuteAction(pane) = %+v, want session example-session with a new pane", got)
		}
		assertLiteralRun(t, client, claude, "example-session", dir)
	})

	t.Run("project", func(t *testing.T) {
		client := newClient(t)
		startSession(t, client, "keeper", t.TempDir())
		root := t.TempDir()
		claude := installClaude(t, "", 0)

		got, err := ExecuteAction(t.Context(), executeConfig(client, claude),
			Action{Kind: ActionProject, Name: literalDirName, Dir: root, Prompt: literalPrompt})
		if err != nil {
			t.Fatalf("ExecuteAction(project): %v", err)
		}
		if want := (Target{Session: "example-_{session_name}_dir"}); got != want {
			t.Errorf("ExecuteAction(project) = %+v, want %+v", got, want)
		}
		entries, err := os.ReadDir(root)
		if err != nil || len(entries) != 1 || entries[0].Name() != literalDirName {
			t.Errorf("root entries = %v, %v; want only %s", entries, err, literalDirName)
		}
		assertLiteralRun(t, client, claude, got.Session, filepath.Join(root, literalDirName))
	})
}
