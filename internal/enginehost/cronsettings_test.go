package enginehost

import (
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// cronHookSettings is the part of a headless session's --settings that the cron takeover tests read back.
type cronHookSettings struct {
	Env   map[string]string `json:"env"`
	Hooks struct {
		PreToolUse []struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type      string `json:"type"`
				Command   string `json:"command"`
				Timeout   int    `json:"timeout"`
				OnFailure string `json:"onFailure"`
			} `json:"hooks"`
		} `json:"PreToolUse"`
	} `json:"hooks"`
}

// testOrrery returns the absolute path of the orrery binary on PATH, the binary a headless session's cron hook runs.
func testOrrery(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("orrery")
	if err != nil {
		t.Fatalf("find orrery: %v", err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("absolute orrery path: %v", err)
	}
	return abs
}

// cronSettingsArg returns the --settings JSON of args, an argument line the fake claude recorded as its arguments
// joined by spaces. The JSON is decoded from the value after --settings, so the spaces inside it, such as the space
// in the hook's command, do not split it.
func cronSettingsArg(t *testing.T, args string) string {
	t.Helper()
	_, value, found := strings.Cut(args, "--settings ")
	if !found {
		t.Fatalf("args = %q, want --settings", args)
	}
	var settings json.RawMessage
	if err := json.NewDecoder(strings.NewReader(value)).Decode(&settings); err != nil {
		t.Fatalf("--settings in args %q is not JSON: %v", args, err)
	}
	return string(settings)
}

// cronSettingsWant returns the --settings JSON of a headless session: the env object text env, which is omitted when
// it is "", and the cron hook that runs orrery.
func cronSettingsWant(env, orrery string) string {
	hooks := `"hooks":{"PreToolUse":[{"matcher":"CronCreate|CronDelete|CronList","hooks":[{"type":"command",` +
		`"command":"'` + orrery + `' hook cron","timeout":10,"onFailure":"block"}]}]}`
	if env == "" {
		return "{" + hooks + "}"
	}
	return `{"env":` + env + "," + hooks + "}"
}

// cronHookOf returns the --settings of args, an argument line the fake claude recorded, decoded as cronHookSettings.
func cronHookOf(t *testing.T, args string) cronHookSettings {
	t.Helper()
	var settings cronHookSettings
	if err := json.Unmarshal([]byte(cronSettingsArg(t, args)), &settings); err != nil {
		t.Fatalf("decode --settings of args %q: %v", args, err)
	}
	return settings
}

// checkCronHook fails t unless settings holds exactly one PreToolUse hook on the cron tools, which runs orrery hook
// cron as a command and blocks the tool call when the hook fails.
func checkCronHook(t *testing.T, settings cronHookSettings, orrery string) {
	t.Helper()
	if len(settings.Hooks.PreToolUse) != 1 {
		t.Fatalf("PreToolUse has %d entries, want one", len(settings.Hooks.PreToolUse))
	}
	group := settings.Hooks.PreToolUse[0]
	if group.Matcher != "CronCreate|CronDelete|CronList" {
		t.Fatalf("PreToolUse matcher = %q, want the cron tools", group.Matcher)
	}
	if len(group.Hooks) != 1 {
		t.Fatalf("PreToolUse hooks = %d, want one", len(group.Hooks))
	}
	hook := group.Hooks[0]
	if hook.Type != "command" || hook.Timeout != 10 {
		t.Fatalf("hook = %+v, want a command with timeout 10", hook)
	}
	if want := "'" + orrery + "' hook cron"; hook.Command != want {
		t.Fatalf("hook command = %q, want %q", hook.Command, want)
	}
	if hook.OnFailure != "block" {
		t.Fatalf("hook onFailure = %q, want block", hook.OnFailure)
	}
}

func TestCronTakeoverHeadlessSettingsCarryHook(t *testing.T) {
	t.Parallel()
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
	waitHistory(t, engineOf(m), path, id, "the first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	if err := m.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := m.Say(t.Context(), SayRequest{ID: id, Text: "again"}); err != nil {
		t.Fatalf("Say of a stopped session: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the resumed reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2")
	})

	args := fakeArgs(t, dir)
	if len(args) != 2 {
		t.Fatalf("fake claude starts = %q, want two", args)
	}
	orrery := testOrrery(t)
	for _, line := range args {
		checkCronHook(t, cronHookOf(t, line), orrery)
	}
}

func TestCronTakeoverStampEnvKeptWithHook(t *testing.T) {
	t.Parallel()
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
	settings := cronHookOf(t, args[0])
	wantEnv := map[string]string{"ANTHROPIC_BASE_URL": "http://127.0.0.1:9", "ORRERY_STAMP_TEST": "whole"}
	if !maps.Equal(settings.Env, wantEnv) {
		t.Fatalf("--settings env = %v, want the stamp's env %v", settings.Env, wantEnv)
	}
	checkCronHook(t, settings, testOrrery(t))
}

func TestCronTakeoverStepGetsNoHook(t *testing.T) {
	t.Parallel()
	home, dir := stepMemoryHome(t)
	writeStepAgents(t, home)
	line := stepMemoryRun(t, []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home}, dir)

	var settings map[string]json.RawMessage
	if err := json.Unmarshal([]byte(cronSettingsArg(t, line)), &settings); err != nil {
		t.Fatalf("decode step --settings: %v", err)
	}
	if hooks, ok := settings["hooks"]; ok {
		t.Fatalf("step --settings carries hooks %s, want none", hooks)
	}
}
