package capture

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// declaredSettings is one real-settings probe, spelled out field by field.
type declaredSettings struct {
	name, mode, tools, resumeOf, modeChange    string
	promptTool, interrupt, persist, hookEvents bool
	answer                                     Answer
	linger                                     time.Duration
	prompts                                    []string
	expect                                     []Expectation
}

func declaredSettingsProbes() []declaredSettings {
	return []declaredSettings{
		{
			name: "guard", mode: "bypassPermissions", tools: "Bash",
			promptTool: true, answer: AnswerAllow, hookEvents: true,
			prompts: []string{
				"This is a test in an empty scratch directory that is not a git repository. Use the Bash tool " +
					"exactly once to run exactly this command: " + GuardedCommand + ". Then reply with exactly the " +
					"word DONE and nothing else.",
			},
			expect: []Expectation{{Type: "user", Contains: GuardRefusal}, {Type: "result"}},
		},
		{
			name: "resume-plant", mode: "bypassPermissions", persist: true,
			prompts: []string{
				"Remember the code word " + ResumeWord + ". Reply with exactly the word OK and nothing else.",
			},
			expect: []Expectation{{Type: "system", Subtype: "init"}, {Type: "result", Subtype: "success"}},
		},
		{
			name: "resume-ask", mode: "bypassPermissions", persist: true, resumeOf: "resume-plant",
			prompts: []string{"What was the code word I asked you to remember? Reply with only that word."},
			expect: []Expectation{
				{Type: "system", Subtype: "init"},
				{Type: "result", Subtype: "success", Contains: ResumeWord},
			},
		},
		{
			name: "interrupt-prompt", mode: "bypassPermissions", interrupt: true,
			prompts: []string{
				"Write the numbers from 1 to 400, one per line, with no other text.",
				"Reply with exactly the word AFTER and nothing else.",
			},
			expect: []Expectation{
				{Type: "result", Count: 2},
				{Type: "result", Subtype: "success", Contains: "AFTER"},
			},
		},
		{
			name: "bypass-tool", mode: "bypassPermissions", tools: "Bash",
			promptTool: true, answer: AnswerAllow,
			prompts: []string{
				"Use the Bash tool exactly once to run the command git --version. Then reply with exactly the word " +
					"DONE and nothing else.",
			},
			expect: []Expectation{{Type: "user", Contains: "git version"}, {Type: "result", Subtype: "success"}},
		},
		{
			name: "background", mode: "bypassPermissions", tools: "Bash",
			promptTool: true, answer: AnswerAllow, linger: 35 * time.Second,
			prompts: []string{
				"Use the Bash tool exactly once, with run_in_background set to true, to run the command sleep 20. " +
					"Do not wait for it to finish. Then reply with exactly the word STARTED and nothing else.",
			},
			expect: []Expectation{
				{Type: "assistant", Contains: `"run_in_background":true`},
				{Type: "result", Subtype: "success"},
			},
		},
	}
}

// guardStream is a stream of the guard probe whose Bash call returned toolResult.
func guardStream(toolResult string) string {
	return `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1",` +
		`"is_error":true,"content":"` + toolResult + `"}]}}` + "\n" +
		`{"type":"result","subtype":"success","is_error":false,"result":"DONE"}` + "\n"
}

func TestSettingsProbesDeclared(t *testing.T) {
	t.Parallel()
	want := declaredSettingsProbes()
	got := SettingsProbes()
	if len(got) != len(want) {
		t.Fatalf("SettingsProbes() returned %d probes, want %d", len(got), len(want))
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
		if p.Persist != w.persist {
			t.Errorf("%s Persist = %v, want %v", w.name, p.Persist, w.persist)
		}
		if p.HookEvents != w.hookEvents {
			t.Errorf("%s HookEvents = %v, want %v", w.name, p.HookEvents, w.hookEvents)
		}
		if p.ResumeOf != w.resumeOf {
			t.Errorf("%s ResumeOf = %q, want %q", w.name, p.ResumeOf, w.resumeOf)
		}
		if p.Linger != w.linger {
			t.Errorf("%s Linger = %v, want %v", w.name, p.Linger, w.linger)
		}
		if !slices.Equal(p.Prompts, w.prompts) {
			t.Errorf("%s Prompts = %q, want %q", w.name, p.Prompts, w.prompts)
		}
		if !slices.Equal(p.Expect, w.expect) {
			t.Errorf("%s Expect = %+v, want %+v", w.name, p.Expect, w.expect)
		}
	}
}

func TestSettingsGuardedCommandIsThreeWords(t *testing.T) {
	t.Parallel()
	if got := strings.Fields(GuardedCommand); !slices.Equal(got, []string{"git", "stash", "clear"}) {
		t.Errorf("GuardedCommand words = %q, want git, stash, clear", got)
	}
}

func TestSettingsGuardPromptNamesGuardedCommand(t *testing.T) {
	t.Parallel()
	guard := SettingsProbes()[0]
	if guard.Name != "guard" {
		t.Fatalf("first settings probe = %q, want guard", guard.Name)
	}
	if !strings.Contains(guard.Prompts[0], GuardedCommand) {
		t.Errorf("guard prompt = %q, want it to contain GuardedCommand %q", guard.Prompts[0], GuardedCommand)
	}
}

func TestSettingsGuardPassesOnlyOnGuardRefusal(t *testing.T) {
	t.Parallel()
	guard := SettingsProbes()[0]
	wantMissing := []Expectation{{Type: "user", Contains: GuardRefusal}}

	gitError := guardStream("fatal: not a git repository")
	report, err := Check([]byte(gitError), guard.Expect)
	if err != nil {
		t.Fatalf("Check(git error) error = %v, want nil", err)
	}
	if !slices.Equal(report.Missing, wantMissing) {
		t.Errorf("Check(git error) Missing = %+v, want %+v", report.Missing, wantMissing)
	}

	refused := guardStream(GuardRefusal)
	report, err = Check([]byte(refused), guard.Expect)
	if err != nil {
		t.Fatalf("Check(refusal) error = %v, want nil", err)
	}
	if len(report.Missing) != 0 {
		t.Errorf("Check(refusal) Missing = %+v, want none", report.Missing)
	}
}

func TestSettingsOnlyGuardSetsHookEvents(t *testing.T) {
	t.Parallel()
	for _, p := range SettingsProbes() {
		if p.HookEvents != (p.Name == "guard") {
			t.Errorf("%s HookEvents = %v, want true only for guard", p.Name, p.HookEvents)
		}
	}
}

func TestSettingsOnlyResumePairPersists(t *testing.T) {
	t.Parallel()
	for _, p := range SettingsProbes() {
		resumePair := p.Name == "resume-plant" || p.Name == "resume-ask"
		if p.Persist != resumePair {
			t.Errorf("%s Persist = %v, want true only for the resume pair", p.Name, p.Persist)
		}
	}
}

func TestSettingsResumeAskResumesResumePlant(t *testing.T) {
	t.Parallel()
	probes := SettingsProbes()
	plant := slices.IndexFunc(probes, func(p Probe) bool { return p.Name == "resume-plant" })
	ask := slices.IndexFunc(probes, func(p Probe) bool { return p.Name == "resume-ask" })
	if plant < 0 || ask < 0 {
		t.Fatalf("resume-plant at %d, resume-ask at %d; want both present", plant, ask)
	}
	if probes[ask].ResumeOf != "resume-plant" {
		t.Errorf("resume-ask ResumeOf = %q, want resume-plant", probes[ask].ResumeOf)
	}
	if plant >= ask {
		t.Errorf("resume-plant at %d runs after resume-ask at %d, want before", plant, ask)
	}
	if !strings.Contains(probes[plant].Prompts[0], ResumeWord) {
		t.Errorf("resume-plant prompt does not carry ResumeWord %q", ResumeWord)
	}
}

func TestSettingsOnlyBackgroundLingers(t *testing.T) {
	t.Parallel()
	for _, p := range SettingsProbes() {
		if (p.Linger > 0) != (p.Name == "background") {
			t.Errorf("%s Linger = %v, want positive only for background", p.Name, p.Linger)
		}
	}
}
