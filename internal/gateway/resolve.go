package gateway

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/joshsymonds/orrery/internal/llmroutes"
)

// ErrNoContext is returned when the route registry holds no context a request names.
var ErrNoContext = errors.New("gateway: no such context")

// contextPrefix is how a request names the context it wants: /ctx/work/v1/messages asks the work context for
// /v1/messages.
const contextPrefix = "/ctx"

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

	// Selector is the model or agent type binding that chose SeatID, or empty when the context's default seat or the
	// subagents' default seat did.
	Selector string

	// Subagent is whether the subagent routing chose SeatID.
	Subagent bool
}

// SplitContext separates the context a request path names from the path the upstream receives. A path that begins
// with /ctx/<name> names the context <name>, and rest is the path after that prefix; a path that is only /ctx/<name>
// continues as /. Any other path names no context, so name is empty and rest is the path itself.
func SplitContext(path string) (name, rest string) {
	after, found := strings.CutPrefix(path, contextPrefix)
	if !found || (after != "" && !strings.HasPrefix(after, "/")) {
		return "", path
	}
	segment, tail, _ := strings.Cut(strings.TrimPrefix(after, "/"), "/")
	if segment == "" {
		return "", path
	}
	return segment, "/" + tail
}

// Resolve selects the seat a request goes through. contextName names the route context, and empty means
// reg.DefaultContext. model is the model the request names, and obs is the identity of a marked subagent's request, or
// nil for any other request. It returns ErrNoContext when reg holds no context the request names.
func Resolve(reg llmroutes.Registry, contextName, model string, obs *Observation) (Resolution, error) {
	if contextName == "" {
		contextName = reg.DefaultContext
	}
	ctx, ok := findContext(reg.Contexts, contextName)
	if !ok {
		return Resolution{}, fmt.Errorf("%w: %q", ErrNoContext, contextName)
	}
	seatID, selector, subagent := choose(ctx, model, obs)
	seat, ok := findSeat(reg.Seats, seatID)
	if !ok {
		return Resolution{}, fmt.Errorf("gateway: context %q names seat %q, which is not defined", ctx.ID, seatID)
	}
	return Resolution{
		Context:  cloneContext(ctx),
		SeatID:   seatID,
		Seat:     cloneSeat(seat),
		Selector: selector,
		Subagent: subagent,
	}, nil
}

// choose returns the id of the seat a request goes through, the selector that chose it, and whether subagent routing
// chose it. A marked subagent is routed by its context's Subagents when the context has them, and any other request
// takes the context's exact model binding, then its default seat.
func choose(ctx llmroutes.Context, model string, obs *Observation) (seatID, selector string, subagent bool) {
	if obs != nil && ctx.Subagents != nil {
		return chooseSubagent(*ctx.Subagents, model, obs.AgentType)
	}
	if id, ok := ctx.Models[model]; ok {
		return id, model, false
	}
	return ctx.DefaultSeat, "", false
}

// chooseSubagent takes the agent type binding first, then the subagent model binding, then the subagents' default seat.
func chooseSubagent(subagents llmroutes.Subagents, model, agentType string) (seatID, selector string, subagent bool) {
	if agentType != "" {
		if id, ok := subagents.AgentTypes[agentType]; ok {
			return id, agentType, true
		}
	}
	if id, ok := subagents.Models[model]; ok {
		return id, model, true
	}
	return subagents.DefaultSeat, "", true
}

// findContext returns the context with the given id.
func findContext(contexts []llmroutes.Context, id string) (llmroutes.Context, bool) {
	for _, ctx := range contexts {
		if ctx.ID == id {
			return ctx, true
		}
	}
	return llmroutes.Context{}, false
}

// findSeat returns the seat with the given id.
func findSeat(seats []llmroutes.Seat, id string) (llmroutes.Seat, bool) {
	for _, seat := range seats {
		if seat.ID == id {
			return seat, true
		}
	}
	return llmroutes.Seat{}, false
}

// cloneContext copies the bindings and subagent policy of a context, so a Resolution does not alias the registry it
// was resolved from.
func cloneContext(ctx llmroutes.Context) llmroutes.Context {
	ctx.Models = maps.Clone(ctx.Models)
	if ctx.Subagents != nil {
		subagents := *ctx.Subagents
		subagents.AgentTypes = maps.Clone(subagents.AgentTypes)
		subagents.Models = maps.Clone(subagents.Models)
		ctx.Subagents = &subagents
	}
	return ctx
}

// cloneSeat copies the beta list of a seat, so that appending to a Resolution's seat cannot write into the registry.
func cloneSeat(seat llmroutes.Seat) llmroutes.Seat {
	seat.StripBetas = slices.Clone(seat.StripBetas)
	return seat
}
