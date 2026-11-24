package gateway

import (
	"errors"

	"github.com/joshsymonds/orrery/internal/llmroutes"
)

// ErrNoContext is returned when the route registry holds no context a request names.
var ErrNoContext = errors.New("gateway: no such context")

// Observation is the identity a marked subagent's request reports: the agent id and agent type that Claude Code sends
// in its x-claude-code-agent-id and x-claude-code-agent-type headers.
type Observation struct {
	// AgentID is the value of the x-claude-code-agent-id header.
	AgentID string

	// AgentType is the value of the x-claude-code-agent-type header.
	AgentType string
}

// Resolution is the seat one request goes through: the context the request resolved to, the seat that context chose,
// and the selector that chose it.
type Resolution struct {
	// Context is the route context the request resolved to.
	Context llmroutes.Context

	// SeatID is the id of the seat the request goes through.
	SeatID string

	// Seat is the seat the request goes through.
	Seat llmroutes.Seat

	// Selector is the model or agent type binding that chose SeatID, or empty when the context's default seat did.
	Selector string

	// Subagent is whether the request is a marked subagent's.
	Subagent bool
}

// SplitContext separates the context a request path names from the path the upstream receives. A path that begins
// with /ctx/<name> names the context <name>, and rest is the path after that prefix. Any other path names no context,
// so name is empty and rest is the path itself.
func SplitContext(_ string) (name, rest string) {
	return "", ""
}

// Resolve selects the seat a request goes through. contextName names the route context, and empty means
// reg.DefaultContext. model is the model the request names, and obs is the identity of a marked subagent's request, or
// nil for any other request. It returns ErrNoContext when reg holds no context the request names.
func Resolve(_ llmroutes.Registry, _, _ string, _ *Observation) (Resolution, error) {
	return Resolution{}, errNotImplemented
}
