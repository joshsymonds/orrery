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
	eligibleA := startLabelledPane(t, client, "example-session-a",
		"waiting", "example-label-a", "example summary a", "")
	unsetState := startLabelledPane(t, client, "example-session-b",
		"", "example-label-b", "example summary b", "")
	withContext := startLabelledPane(t, client, "example-session-c",
		"working", "example-label-c", "example summary c", "example-context")
	eligibleD := startLabelledPane(t, client, "example-session-d",
		"done", "example-label-d", "example summary d", "")
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
		root, "dir-31", "example-context", "example-label-b", "example-label-c", "example summary c", "example-session",
	}
	for _, unwanted := range unwantedText {
		if strings.Contains(stdin, unwanted) {
			t.Errorf("stdin contains %q, want it absent", unwanted)
		}
	}
	for _, excluded := range []string{unsetState, withContext} {
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
	pane := startLabelledPane(t, client, "example-session-a", "waiting", "example-label-a", "example summary a", "")
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
	pane := startLabelledPane(t, client, "example-session-a", "waiting", "example-label-a", "example summary a", "")
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
	unsetState := startLabelledPane(t, client, "example-session-b", "", "example-label-b", "example summary b", "")
	withContext := startLabelledPane(t, client, "example-session-c",
		"working", "example-label-c", "example summary c", "example-context")
	zoxide := installZoxide(t, zoxideListing(root, 3), 0)
	claude := installClaude(t, strings.Join([]string{
		"jump " + unsetState,
		"jump " + withContext,
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
	startLabelledPane(t, client, "example-session-a", "waiting", "example-label-a", "example summary a", "")
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
