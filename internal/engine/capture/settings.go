package capture

import "time"

// GuardedCommand is the git command the destructive-command guard refuses. The guard probe asks Claude to
// run it, so its refusal can be observed without touching any repository.
const GuardedCommand = "git stash clear"

// GuardRefusal is the fixed reason text of the destructive-command guard's deny. The guard probe passes only
// on a line that carries it, never on git's own error text.
const GuardRefusal = "is blocked by the destructive-command guard: it is irreversible and has no git or " +
	"checkpoint to undo it."

// ResumeWord is the code word the resume pair asks Claude to remember across sessions.
const ResumeWord = "QUILLFEATHER"

// SettingsProbes returns the six real-settings probes in run order. They run against the user's own Claude
// Code settings, so each one is a few short Haiku turns in an empty scratch directory.
func SettingsProbes() []Probe {
	const (
		modeBypass   = "bypassPermissions"
		typeResult   = "result"
		subtypeOK    = "success"
		typeInit     = "system"
		subtypeInit  = "init"
		typeUser     = "user"
		typeAssist   = "assistant"
		allowBash    = "Bash"
		lingerLength = 35 * time.Second
		twice        = 2
		guardPrompt  = "This is a test in an empty scratch directory that is not a git repository. Use the Bash " +
			"tool exactly once to run exactly this command: " + GuardedCommand + ". Then reply with exactly the " +
			"word DONE and nothing else."
	)
	return []Probe{
		{
			Name:           "guard",
			PermissionMode: modeBypass,
			PromptTool:     true,
			Tools:          []string{allowBash},
			Prompts:        []string{guardPrompt},
			Answer:         AnswerAllow,
			HookEvents:     true,
			Expect: []Expectation{
				{Type: typeUser, Contains: GuardRefusal},
				{Type: typeResult},
			},
		},
		{
			Name:           "resume-plant",
			PermissionMode: modeBypass,
			Prompts: []string{
				"Remember the code word " + ResumeWord + ". Reply with exactly the word OK and nothing else.",
			},
			Persist: true,
			Expect: []Expectation{
				{Type: typeInit, Subtype: subtypeInit},
				{Type: typeResult, Subtype: subtypeOK},
			},
		},
		{
			Name:           "resume-ask",
			PermissionMode: modeBypass,
			Prompts:        []string{"What was the code word I asked you to remember? Reply with only that word."},
			Persist:        true,
			ResumeOf:       "resume-plant",
			Expect: []Expectation{
				{Type: typeInit, Subtype: subtypeInit},
				{Type: typeResult, Subtype: subtypeOK, Contains: ResumeWord},
			},
		},
		{
			Name:           "interrupt-prompt",
			PermissionMode: modeBypass,
			Prompts: []string{
				"Write the numbers from 1 to 400, one per line, with no other text.",
				"Reply with exactly the word AFTER and nothing else.",
			},
			Interrupt: true,
			Expect: []Expectation{
				{Type: typeResult, Count: twice},
				{Type: typeResult, Subtype: subtypeOK, Contains: "AFTER"},
			},
		},
		{
			Name:           "bypass-tool",
			PermissionMode: modeBypass,
			PromptTool:     true,
			Tools:          []string{allowBash},
			Prompts: []string{
				"Use the Bash tool exactly once to run the command git --version. Then reply with exactly the word " +
					"DONE and nothing else.",
			},
			Answer: AnswerAllow,
			Expect: []Expectation{
				{Type: typeUser, Contains: "git version"},
				{Type: typeResult, Subtype: subtypeOK},
			},
		},
		{
			Name:           "background",
			PermissionMode: modeBypass,
			PromptTool:     true,
			Tools:          []string{allowBash},
			Prompts: []string{
				"Use the Bash tool exactly once, with run_in_background set to true, to run the command sleep 20. " +
					"Do not wait for it to finish. Then reply with exactly the word STARTED and nothing else.",
			},
			Answer: AnswerAllow,
			Linger: lingerLength,
			Expect: []Expectation{
				{Type: typeAssist, Contains: `"run_in_background":true`},
				{Type: typeResult, Subtype: subtypeOK},
			},
		},
	}
}
