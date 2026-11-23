// Package testrunner owns the test-runner role: a read-only step that runs one command in a scratch worktree at a
// revision and reports its exit code and output. orrery-stage-tools reuses it.
package testrunner

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"unicode/utf8"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/roles"
)

// Role is the role file key the test runner's agent is read from.
const Role = "test_runner"

// fallbackRole is the role file key the test runner's agent falls back to when the role file has no Role key.
const fallbackRole = "implementer"

// maxOutput caps the text Output renders, in bytes. The tail is kept.
const maxOutput = 64 << 10

// unreadablePrefix opens the text Output renders for a return that does not decode.
const unreadablePrefix = "unreadable test runner return: "

// Contract is the role text of a test runner, from contract.md.
//
//go:embed contract.md
var Contract string

// Schema is the schema a test runner's return must match, from schema.json.
//
//go:embed schema.json
var Schema string

// Request is one test run: the command to run, and the revision the worktree is checked out at.
type Request struct {
	Command  string
	Revision string
}

// returned is a test runner return as it decodes.
type returned struct {
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output"`
	Summary  string `json:"summary"`
}

// Prompt returns the prompt of a test runner step: Contract, then the command and the revision.
func Prompt(req Request) string {
	return Contract + "\nCommand: " + req.Command + "\nRevision: " + req.Revision + "\n"
}

// Agent returns the agent the test runner role runs under: the agent the role file names for Role, or the agent it
// names for the implementer when the role file has no Role key.
func Agent(getenv func(string) string) (string, error) {
	return roles.AgentOr(getenv, Role, fallbackRole)
}

// Start returns the start of the test runner step id that runs req in repository. The step is read-only and scratch,
// at req.Revision, with Schema and the done value "done", and runs under the agent Agent resolves. It carries epic and
// task as its labels. A role error is returned wrapped.
func Start(
	getenv func(string) string, repository, id string, req Request, epic, task string,
) (enginehost.StepStart, error) {
	agent, err := Agent(getenv)
	if err != nil {
		return enginehost.StepStart{}, fmt.Errorf("test runner role: %w", err)
	}
	return enginehost.StepStart{
		ID:        id,
		Agent:     agent,
		Prompt:    Prompt(req),
		Schema:    Schema,
		Workspace: repository,
		Revision:  req.Revision,
		ReadOnly:  true,
		Scratch:   true,
		DoneValue: "done",
		Epic:      epic,
		Task:      task,
	}, nil
}

// Output renders a kept test runner return as "exit <code>: <summary>", then its output on the lines after it, capped
// at 64 KiB keeping the tail. A return that does not decode renders as the unreadable prefix followed by its raw text,
// also capped.
func Output(result string) string {
	var ret returned
	if err := json.Unmarshal([]byte(result), &ret); err != nil {
		return tail(unreadablePrefix + result)
	}
	return tail(fmt.Sprintf("exit %d: %s\n%s", ret.ExitCode, ret.Summary, ret.Output))
}

// tail returns the last maxOutput bytes of text, or text when it is no longer than that. The cut moves forward past
// any partial character, so the kept text stays valid UTF-8.
func tail(text string) string {
	if len(text) <= maxOutput {
		return text
	}
	text = text[len(text)-maxOutput:]
	for len(text) > 0 && !utf8.RuneStart(text[0]) {
		text = text[1:]
	}
	return text
}
