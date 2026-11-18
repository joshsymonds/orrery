package enginehost

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// stepMemoryRun starts a step in dir with environ, waits for its first turn end, and returns the argv the fake claude
// recorded for that run.
func stepMemoryRun(t *testing.T, environ []string, dir string) string {
	t.Helper()
	m, path := stampTestManager(t, environ)
	obs := &stepObserver{}
	err := m.StartStep(t.Context(), StepSession{
		ID: stepA, Dir: dir, Prompt: "hi", Agent: "reviewer", Schema: stepSchema, Observer: obs,
	})
	if err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	waitHistory(t, engineOf(m), path, stepA, "the step's reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	waitReturns(t, engineOf(m), obs, 1)
	args := fakeArgs(t, dir)
	if len(args) != 1 {
		t.Fatalf("fake claude starts = %q, want one", args)
	}
	return args[0]
}

// stepMemoryHome returns a synthetic home and a folder for a step under it.
func stepMemoryHome(t *testing.T) (string, string) {
	t.Helper()
	home := stampTestHome(t)
	dir := filepath.Join(home, "work", "step")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return home, dir
}

// stepMemoryConfigAgents writes the reviewer agent a step starts with under configDir, where a claude that runs with
// CLAUDE_CONFIG_DIR set to configDir resolves its agents.
func stepMemoryConfigAgents(t *testing.T, configDir string) {
	t.Helper()
	dir := filepath.Join(configDir, "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: reviewer\ndescription: Checks a step\n---\n"
	if err := os.WriteFile(filepath.Join(dir, "reviewer.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestStepMemoryExcluded checks that every agent Orrery runs on its own behalf names the user CLAUDE.md under
// claudeMdExcludes, which the claude run under HOME and CLAUDE_CONFIG_DIR loads, and that sessions the owner talks to
// do not. Its subtests run one at a time: in parallel, each one that starts a session takes seconds.
func TestStepMemoryExcluded(t *testing.T) {
	t.Run("a step's settings name the user CLAUDE.md under claudeMdExcludes", func(t *testing.T) {
		home, dir := stepMemoryHome(t)
		writeStepAgents(t, home)
		line := stepMemoryRun(t, []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home}, dir)
		want := `{"claudeMdExcludes":["` + filepath.Join(home, ".claude", "CLAUDE.md") + `"]}`
		if !hasFlagValue(line, "--settings", want) {
			t.Fatalf("step args = %q, want --settings %s", line, want)
		}
	})

	t.Run("a step under CLAUDE_CONFIG_DIR names that directory's CLAUDE.md", func(t *testing.T) {
		home, dir := stepMemoryHome(t)
		configDir := filepath.Join(home, "config")
		stepMemoryConfigAgents(t, configDir)
		environ := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "CLAUDE_CONFIG_DIR=" + configDir}
		line := stepMemoryRun(t, environ, dir)
		want := `{"claudeMdExcludes":["` + filepath.Join(configDir, "CLAUDE.md") + `"]}`
		if !hasFlagValue(line, "--settings", want) {
			t.Fatalf("step args = %q, want --settings %s", line, want)
		}
	})

	t.Run("a step's settings set no setting sources and no disableAllHooks", func(t *testing.T) {
		home, dir := stepMemoryHome(t)
		writeStepAgents(t, home)
		// The stamp sits above the step's folder, so the step runs under a stamp and its settings carry its env too.
		stampTestWrite(t, filepath.Join(home, "work", ".claude", "settings.json"), stampSettings)
		line := stepMemoryRun(t, []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home}, dir)
		if strings.Contains(line, "--setting-sources") {
			t.Fatalf("step args = %q, want no --setting-sources", line)
		}
		if strings.Contains(line, "disableAllHooks") {
			t.Fatalf("step args = %q, want no disableAllHooks", line)
		}
	})

	t.Run("a headless session's settings carry no claudeMdExcludes", func(t *testing.T) {
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
		waitHistory(t, engineOf(m), path, id, "the first reply block", func(h store.EngineHistory) bool {
			return hasBlock(t, h, "fake reply 1")
		})
		args := fakeArgs(t, dir)
		if len(args) != 1 {
			t.Fatalf("fake claude starts = %q, want one", args)
		}
		env := `{"ANTHROPIC_BASE_URL":"http://127.0.0.1:9","ORRERY_STAMP_TEST":"whole"}`
		want := cronSettingsWant(env, testOrrery(t))
		if got := cronSettingsArg(t, args[0]); got != want {
			t.Fatalf("start --settings = %s, want %s", got, want)
		}
	})

	t.Run("the engine's Haiku caller's settings name it", func(t *testing.T) {
		dir := haikuDir(t)
		claude := haikuWriteFake(t, dir, haikuFake{stdout: haikuSuccessLine, status: 0})
		h := NewHaiku(HaikuConfig{Claude: claude, Environ: haikuEnviron()})

		if _, err := h.Call(context.Background(), haikuSchema, "input"); err != nil {
			t.Fatalf("Call: %v", err)
		}
		if got := stepStampTestSettings(t, dir); got != haikuPlainSettings {
			t.Fatalf("haiku --settings = %s, want %s", got, haikuPlainSettings)
		}
	})

	t.Run("a Haiku call whose user CLAUDE.md path cannot resolve runs no claude", func(t *testing.T) {
		dir := haikuDir(t)
		claude := haikuWriteFake(t, dir, haikuFake{stdout: haikuSuccessLine, status: 0})
		h := NewHaiku(HaikuConfig{Claude: claude, Environ: []string{"PATH=" + os.Getenv("PATH")}})

		if _, err := h.Call(context.Background(), haikuSchema, "input"); err == nil {
			t.Fatal("Call with no HOME returned nil error, want a refusal naming the user CLAUDE.md")
		}
		if runs := haikuRuns(t, dir); runs != 0 {
			t.Fatalf("claude ran %d times, want none", runs)
		}
	})
}
