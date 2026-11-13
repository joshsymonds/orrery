package enginehost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// stampSettings is the --settings JSON that the synthetic stamp in stampTestHome carries: its whole env, with a loopback
// base URL and a second entry, so the test sees the env carried whole.
const stampSettings = `{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:9","ORRERY_STAMP_TEST":"whole"}}`

// stampTestWrite writes content to path, creating its parent directories.
func stampTestWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// stampTestHome returns a synthetic home under t.TempDir(), resolved through its symlinks, so the paths that Find
// reports are the paths the test builds.
func stampTestHome(t *testing.T) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return home
}

// stampTestManager returns a manager with a session cap and a step cap of one that runs the fake claude and starts
// every process with environ. It reads rows from the store at the returned path.
func stampTestManager(t *testing.T, environ []string) (*Manager, string) {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	s, path := openStore(t)
	writer := NewWriter(WriterConfig{Applier: s, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	m := NewManager(Config{
		Shell:   sh,
		Claude:  fakeClaudePath(t),
		Environ: environ,
		Cap:     1,
		StepCap: 1,
		Writer:  writer,
		Now:     fixedClock,
		Store:   s,
	})
	t.Cleanup(m.Close)
	return m, path
}

// stampTestRecordedReason returns the reason the history records with its failed state.
func stampTestRecordedReason(t *testing.T, history store.EngineHistory) string {
	t.Helper()
	reason := ""
	for _, fields := range changedFields(t, history) {
		if fields["reason"] != "" {
			reason = fields["reason"]
		}
	}
	return reason
}

func TestStartWithStampPassesStampSettingsAndResumeKeepsThem(t *testing.T) {
	home := stampTestHome(t)
	stampTestWrite(t, filepath.Join(home, "work", ".claude", "settings.json"), stampSettings)
	dir := filepath.Join(home, "work", "child")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	m, path := stampTestManager(t, []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home})

	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, path, id, "the first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	if err := m.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := m.Say(t.Context(), SayRequest{ID: id, Text: "again"}); err != nil {
		t.Fatalf("Say of a stopped session: %v", err)
	}
	waitHistory(t, path, id, "the resumed reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2")
	})

	args := fakeArgs(t, dir)
	if len(args) != 2 {
		t.Fatalf("fake claude starts = %q, want two", args)
	}
	for i, line := range args {
		if !hasFlagValue(line, "--settings", stampSettings) {
			t.Fatalf("start %d args = %q, want --settings %s", i, line, stampSettings)
		}
	}
}

func TestStepInLinkedWorktreeOfStampedCheckoutPassesStampSettings(t *testing.T) {
	home := stampTestHome(t)
	stampTestWrite(t, filepath.Join(home, "work", ".claude", "settings.json"), stampSettings)
	writeStepAgents(t, home)
	// The owner's git config never applies to the repository the test makes.
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	checkout := filepath.Join(home, "work", "repo")
	if err := os.MkdirAll(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	stepGitTestRun(t, checkout, "init", "-q", "-b", "main")
	stepGitTestWrite(t, checkout, "README", "base\n")
	stepGitTestRun(t, checkout, "add", "README")
	stepGitTestRun(t, checkout, "commit", "-q", "-m", "base")
	// The linked worktree lies outside the stamped folder, so only its primary checkout carries the stamp.
	linked := filepath.Join(home, "elsewhere", "linked")
	if err := os.MkdirAll(filepath.Dir(linked), 0o755); err != nil {
		t.Fatal(err)
	}
	stepGitTestRun(t, checkout, "worktree", "add", "--detach", linked, "HEAD")
	m, path := stampTestManager(t, []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home})
	obs := &stepObserver{}

	err := m.StartStep(t.Context(), StepSession{
		ID: stepA, Dir: linked, Prompt: "hi", Agent: "reviewer", Schema: stepSchema, Observer: obs,
	})
	if err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	waitHistory(t, path, stepA, "the step's reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	waitReturns(t, obs, 1)

	args := fakeArgs(t, linked)
	if len(args) != 1 {
		t.Fatalf("fake claude starts = %q, want one", args)
	}
	if !hasFlagValue(args[0], "--settings", stampSettings) {
		t.Fatalf("step args = %q, want --settings %s", args[0], stampSettings)
	}
}

func TestStartWithoutStampPassesNoSettings(t *testing.T) {
	home := stampTestHome(t)
	dir := filepath.Join(home, "plain")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	m, path := stampTestManager(t, []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home})

	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, path, id, "the first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	if err := m.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	args := fakeArgs(t, dir)
	if len(args) != 1 {
		t.Fatalf("fake claude starts = %q, want one", args)
	}
	if strings.Contains(args[0], "--settings") {
		t.Fatalf("start args = %q, want no --settings", args[0])
	}
}

// stampTestRelativeHome returns home relative to the test's working directory, so a HOME entry holding it is relative
// and still names the stamped tree.
func stampTestRelativeHome(t *testing.T, home string) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(wd, home)
	if err != nil {
		t.Fatal(err)
	}
	return rel
}

func TestStartWithoutAbsoluteHomeRefusesBeforeClaude(t *testing.T) {
	cases := []struct {
		name    string
		environ func(home string) []string
	}{
		{name: "no HOME entry", environ: func(string) []string { return nil }},
		{name: "empty HOME", environ: func(string) []string { return []string{"HOME="} }},
		{name: "relative HOME", environ: func(home string) []string {
			return []string{"HOME=" + stampTestRelativeHome(t, home)}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := stampTestHome(t)
			stampTestWrite(t, filepath.Join(home, "work", ".claude", "settings.json"), stampSettings)
			dir := filepath.Join(home, "work", "child")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			environ := append([]string{"PATH=" + os.Getenv("PATH")}, tc.environ(home)...)
			m, path := stampTestManager(t, environ)

			id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "hi"})
			if err != nil {
				t.Fatalf("Start: %v, want the refusal recorded on the session", err)
			}
			history := waitHistory(t, path, id, "the failed state", func(h store.EngineHistory) bool {
				return h.State == "failed"
			})
			if reason := stampTestRecordedReason(t, history); !strings.Contains(reason, "HOME") {
				t.Fatalf("failed reason = %q, want it to name HOME", reason)
			}
			if _, err := os.Stat(filepath.Join(dir, "fakeclaude.args")); !os.IsNotExist(err) {
				t.Fatalf("start without an absolute HOME ran claude: stat = %v", err)
			}
		})
	}
}

func TestStartBelowUnparseableStampRecordsFailedAndRunsNoClaude(t *testing.T) {
	home := stampTestHome(t)
	stamp := filepath.Join(home, "work", ".claude", "settings.json")
	stampTestWrite(t, stamp, `{"env": `)
	dir := filepath.Join(home, "work", "child")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	m, path := stampTestManager(t, []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home})

	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v, want the spawn failure recorded on the session", err)
	}
	history := waitHistory(t, path, id, "the failed state", func(h store.EngineHistory) bool {
		return h.State == "failed"
	})
	if reason := stampTestRecordedReason(t, history); !strings.Contains(reason, stamp) {
		t.Fatalf("failed reason = %q, want it to name %s", reason, stamp)
	}
	if _, err := os.Stat(filepath.Join(dir, "fakeclaude.args")); !os.IsNotExist(err) {
		t.Fatalf("start below an unparseable stamp ran claude: stat = %v", err)
	}
}
