// Package stage holds the contract stage's roles, which any conversation runs through Orrery's own tools, records and
// rules: the scout, which answers one question read-only about a repository, and the steelman, which tests a draft
// contract in a discovery pass and a closure pass. Each role runs under the agent the role file names for it, with no
// fallback.
package stage

import (
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/roles"
)

// scoutRole and steelmanRole are the role file keys the scout's and the steelman's agents are read from.
const (
	scoutRole    = "scout"
	steelmanRole = "steelman"
)

// scoutContract is the role text of a scout, from scout.md.
//
//go:embed scout.md
var scoutContract string

// scoutSchema is the schema a scout's return must match, from scout.json.
//
//go:embed scout.json
var scoutSchema string

// steelmanContract is the role text of a steelman, from steelman.md.
//
//go:embed steelman.md
var steelmanContract string

// steelmanSchema is the schema a steelman's return must match, from steelman.json.
//
//go:embed steelman.json
var steelmanSchema string

// roleAgent returns the agent role runs under, as the role file names it. There is no fallback: a role file without
// the role's key is an error. The error is wrapped with the role's name.
func roleAgent(getenv func(string) string, role string) (string, error) {
	agent, err := roles.Agent(getenv, role)
	if err != nil {
		return "", fmt.Errorf("%s role: %w", role, err)
	}
	return agent, nil
}

// ScoutStart returns the start of a scout step that answers question about repository at revision. The step is
// read-only and not scratch, with the scout schema and the done value "done". Its prompt is the scout contract, then
// the question. It runs under the agent the role file names for scout.
func ScoutStart(getenv func(string) string, repository, revision, question string) (enginehost.StepStart, error) {
	agent, err := roleAgent(getenv, scoutRole)
	if err != nil {
		return enginehost.StepStart{}, err
	}
	return enginehost.StepStart{
		Agent:     agent,
		Prompt:    scoutContract + question,
		Schema:    scoutSchema,
		Workspace: repository,
		Revision:  revision,
		ReadOnly:  true,
		DoneValue: "done",
	}, nil
}

// SteelmanStart returns the start of a steelman step that tests the draft contract in prompt against repository. The
// step is read-only, with the steelman schema and the done value "done". Its prompt is the steelman contract, then
// prompt. It runs under the agent the role file names for steelman.
func SteelmanStart(getenv func(string) string, repository, prompt string) (enginehost.StepStart, error) {
	agent, err := roleAgent(getenv, steelmanRole)
	if err != nil {
		return enginehost.StepStart{}, err
	}
	return enginehost.StepStart{
		Agent:     agent,
		Prompt:    steelmanContract + prompt,
		Schema:    steelmanSchema,
		Workspace: repository,
		ReadOnly:  true,
		DoneValue: "done",
	}, nil
}

// SteelmanReturn is the decoded return of one steelman pass. A list the return does not hold is empty, and a text it
// does not hold is "".
type SteelmanReturn struct {
	Status               string
	Pass                 string
	Verdict              string
	StrongestCase        string
	StrongestAlternative string
	Findings             []SteelmanFinding
	Ledger               []SteelmanLedgerItem
	Concerns             []SteelmanConcern
	EvidenceAndCoverage  string
}

// SteelmanFinding is one discovery finding: the contract element it concerns, its impact, the evidence for it, and the
// smallest contract change that resolves or records it.
type SteelmanFinding struct {
	ID       string `json:"id"`
	Contract string `json:"contract"`
	Impact   string `json:"impact"`
	Evidence string `json:"evidence"`
	Change   string `json:"change"`
}

// SteelmanLedgerItem is the closure pass's word on one ledger item: satisfied or not_satisfied, with its note.
type SteelmanLedgerItem struct {
	ID   string `json:"id"`
	Word string `json:"word"`
	Note string `json:"note"`
}

// SteelmanConcern is one concern a closure pass raises: the contract element it concerns and the text of the concern.
type SteelmanConcern struct {
	ID       string `json:"id"`
	Contract string `json:"contract"`
	Text     string `json:"text"`
}

// DecodeSteelman decodes a steelman return's fields from its JSON object. A field the object lacks keeps its zero
// value. Text that is not a JSON object, or a field of the wrong type, is an error. Top-level fields are read by their
// schema names, not struct tags, because the repository's tag rule is camelCase and the schema's names are snake_case.
func DecodeSteelman(result string) (SteelmanReturn, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(result), &fields); err != nil {
		return SteelmanReturn{}, fmt.Errorf("decode steelman return: %w", err)
	}
	var ret SteelmanReturn
	targets := []struct {
		name string
		dst  any
	}{
		{"status", &ret.Status},
		{"pass", &ret.Pass},
		{"verdict", &ret.Verdict},
		{"strongest_case", &ret.StrongestCase},
		{"strongest_alternative", &ret.StrongestAlternative},
		{"findings", &ret.Findings},
		{"ledger", &ret.Ledger},
		{"concerns", &ret.Concerns},
		{"evidence_and_coverage", &ret.EvidenceAndCoverage},
	}
	for _, target := range targets {
		raw, ok := fields[target.name]
		if !ok {
			continue
		}
		if err := json.Unmarshal(raw, target.dst); err != nil {
			return SteelmanReturn{}, fmt.Errorf("decode steelman return field %s: %w", target.name, err)
		}
	}
	return ret, nil
}
