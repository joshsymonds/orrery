package reviewers

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/gates"
)

// agentPrefix starts the name of the inline agent a model reviewer runs under.
const agentPrefix = "orrery-reviewer-"

// AgentName is the agent claude runs for the reviewer: the agent it names, or else the inline agent named from it.
func (r Reviewer) AgentName() string {
	if r.Agent != "" {
		return r.Agent
	}
	return agentPrefix + r.Name
}

// Agents is the inline agent definition a model reviewer runs under, as the JSON the claude --agents flag takes: one
// object keyed by AgentName. tools and effort appear only when set; a set tools list stays even when empty. It is ""
// for a reviewer with no model, since an agent reviewer runs an agent claude already has.
func (r Reviewer) Agents() (string, error) {
	if r.Model == "" {
		return "", nil
	}
	def := map[string]any{
		"description": fmt.Sprintf("orrery reviewer %s", r.Name),
		"prompt":      r.Prompt,
		"model":       r.Model,
	}
	if r.Tools != nil {
		def["tools"] = r.Tools
	}
	if r.Effort != "" {
		def["effort"] = r.Effort
	}
	out, err := json.Marshal(map[string]map[string]any{r.AgentName(): def})
	if err != nil {
		return "", fmt.Errorf("encode agent %s: %w", r.AgentName(), err)
	}
	return string(out), nil
}

// Gate is the agent gate that checks the reviewer: its name, the agent it runs, its limit, and its assertions, the
// musts first and then the must_nots, each in its listed order.
func (r Reviewer) Gate() gates.Gate {
	assertions := make([]gates.Assertion, 0, len(r.Must)+len(r.MustNot))
	for _, text := range r.Must {
		assertions = append(assertions, gates.Assertion{Text: text, Kind: gates.KindMust})
	}
	for _, text := range r.MustNot {
		assertions = append(assertions, gates.Assertion{Text: text, Kind: gates.KindMustNot})
	}
	return gates.Gate{
		Name:       r.Name,
		Agent:      r.AgentName(),
		Limit:      time.Duration(r.Limit) * time.Second,
		Assertions: assertions,
	}
}
