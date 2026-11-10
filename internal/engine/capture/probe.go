package capture

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/proc"
)

const probeTimeout = 120 * time.Second

// Answer is how a probe responds to a permission request that reaches it.
type Answer int

const (
	// AnswerNone sends no answer to a permission request.
	AnswerNone Answer = iota
	// AnswerAllow grants the requested tool use.
	AnswerAllow
	// AnswerDeny refuses the requested tool use.
	AnswerDeny
	// AnswerQuestion answers an AskUserQuestion prompt.
	AnswerQuestion
)

// Expectation names a stdout line that a probe's run must contain. A line matches when its type
// equals Type and its subtype equals Subtype. For control_request the subtype is request.subtype, and
// for control_response it is response.subtype. An empty Subtype matches any subtype. A non-empty
// Contains also requires the line's raw bytes to contain it; an empty Contains matches any text. The
// line must appear at least max(Count, 1) times.
type Expectation struct {
	Type     string
	Subtype  string
	Contains string
	Count    int
}

// Probe is one fixed run of Claude Code: its permission mode, tools, prompts, and how it answers. Linger,
// when positive, keeps stdin open that long after the last prompt's result before it closes.
type Probe struct {
	Name           string
	PermissionMode string
	PromptTool     bool
	Tools          []string
	Prompts        []string
	ModeChange     string
	Interrupt      bool
	Answer         Answer
	Expect         []Expectation
	Linger         time.Duration
	// Persist keeps session persistence on, so the session is written to disk instead of discarded.
	Persist bool
	// HookEvents includes the hook lifecycle events in the stream.
	HookEvents bool
	// ResumeOf names the earlier probe in the same set whose session this probe resumes, in that
	// probe's directory. DaemonCommand only requires a resume id when it is set.
	ResumeOf string
}

// Probes returns the six fixed probes in run order.
func Probes() []Probe {
	const (
		modeDefault    = "default"
		typeResult     = "result"
		typeControlReq = "control_request"
		subtypeSuccess = "success"
		subtypeCanUse  = "can_use_tool"
		twice          = 2
		writeFile      = "Use the Write tool exactly once to create the file note.txt in the current directory " +
			"containing exactly the text ok. Then reply with exactly the word DONE and nothing else."
		askLetter = "Use the AskUserQuestion tool exactly once to ask me which letter I prefer, with exactly two " +
			"options labeled A and B. After I answer, reply with exactly the letter I chose and nothing else."
	)
	return []Probe{
		{
			Name:           "two-turns",
			PermissionMode: modeDefault,
			Prompts: []string{
				"Reply with exactly the word ONE and nothing else.",
				"Reply with exactly the word TWO and nothing else.",
			},
			Expect: []Expectation{
				{Type: "system", Subtype: "init"},
				{Type: typeResult, Subtype: subtypeSuccess, Count: twice},
			},
		},
		{
			Name:           "interrupt",
			PermissionMode: modeDefault,
			Prompts:        []string{"Write the numbers from 1 to 400, one per line, with no other text."},
			Interrupt:      true,
			Expect: []Expectation{
				{Type: "control_response", Subtype: subtypeSuccess, Count: twice},
				{Type: typeResult},
			},
		},
		{
			Name:           "mode-change",
			PermissionMode: modeDefault,
			Prompts:        []string{"Reply with exactly the word OK and nothing else."},
			ModeChange:     "acceptEdits",
			Expect: []Expectation{
				{Type: "control_response", Subtype: subtypeSuccess, Count: twice},
				{Type: "system", Subtype: "init"},
				{Type: "result", Subtype: subtypeSuccess},
			},
		},
		{
			Name:           "question",
			PermissionMode: "bypassPermissions",
			PromptTool:     true,
			Tools:          []string{"AskUserQuestion"},
			Prompts:        []string{askLetter},
			Answer:         AnswerQuestion,
			Expect: []Expectation{
				{Type: typeControlReq, Subtype: subtypeCanUse},
				{Type: typeResult},
			},
		},
		{
			Name:           "permission-allow",
			PermissionMode: modeDefault,
			PromptTool:     true,
			Tools:          []string{"Write"},
			Prompts:        []string{writeFile},
			Answer:         AnswerAllow,
			Expect: []Expectation{
				{Type: typeControlReq, Subtype: subtypeCanUse},
				{Type: "result", Subtype: subtypeSuccess},
			},
		},
		{
			Name:           "permission-deny",
			PermissionMode: modeDefault,
			PromptTool:     true,
			Tools:          []string{"Write"},
			Prompts:        []string{writeFile},
			Answer:         AnswerDeny,
			Expect: []Expectation{
				{Type: typeControlReq, Subtype: subtypeCanUse},
				{Type: typeResult},
			},
		},
	}
}

// Command is the isolated invocation of one probe, in the shape proc.Config takes.
type Command struct {
	Path    string
	Args    []string
	Env     []string
	Dir     string
	Timeout time.Duration
}

// NewCommand builds the command that runs probe with the binary at binary. The environment keeps only
// the allow-listed entries of environ, in their order. Dir is a new empty directory that the caller
// removes. An error is returned, without creating Dir, when binary is not absolute.
func NewCommand(probe Probe, binary string, environ []string) (Command, error) {
	if !filepath.IsAbs(binary) {
		return Command{}, proc.ErrPathNotAbsolute
	}
	dir, err := os.MkdirTemp("", "orrery-probe-")
	if err != nil {
		return Command{}, fmt.Errorf("capture: create working directory: %w", err)
	}
	return Command{
		Path:    binary,
		Args:    argv(probe),
		Env:     allowedEnv(environ),
		Dir:     dir,
		Timeout: probeTimeout,
	}, nil
}

// argv returns the fixed argument vector for probe, one element per token.
func argv(probe Probe) []string {
	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--include-partial-messages",
		"--verbose",
		"--model", "haiku",
		"--max-turns", "2",
		"--settings", `{"disableAllHooks":true}`,
		"--setting-sources", "",
		"--strict-mcp-config",
		"--mcp-config", `{"mcpServers":{}}`,
		"--no-session-persistence",
		"--tools", strings.Join(probe.Tools, ","),
		"--permission-mode", probe.PermissionMode,
	}
	if probe.PromptTool {
		args = append(args, "--permission-prompt-tool", "stdio")
	}
	return args
}

// allowedEnv returns the entries of environ whose names are allow-listed. It is never nil, so proc
// does not inherit the parent environment when no entry qualifies.
func allowedEnv(environ []string) []string {
	env := []string{}
	for _, entry := range environ {
		name, _, _ := strings.Cut(entry, "=")
		if envAllowed(name) {
			env = append(env, entry)
		}
	}
	return env
}

func envAllowed(name string) bool {
	switch name {
	case "HOME", "PATH", "USER", "LANG", "TERM", "XDG_RUNTIME_DIR", "XDG_CONFIG_HOME", "XDG_STATE_HOME":
		return true
	default:
		return false
	}
}
