package planner

// State is the planner-relevant state of one epic.
type State struct {
	Paused  bool
	Parked  bool
	Running bool
	Pending int
}

// ActionKind names what the planner does for an epic next.
type ActionKind string

// The actions Decide returns for an epic.
const (
	ActionNone  ActionKind = "none"
	ActionClaim ActionKind = "claim"
	ActionPark  ActionKind = "park"
)

// Action is the planner's next step for one epic. Reason is set when the epic is parked.
type Action struct {
	Kind   ActionKind
	Reason string
}

// Decide returns the next action for an epic in state. roleErr is the error from reading the epic's planner role.
func Decide(state State, roleErr error) Action {
	switch {
	case state.Parked, state.Paused, state.Running, state.Pending < 1:
		return Action{Kind: ActionNone}
	case roleErr != nil:
		return Action{Kind: ActionPark, Reason: roleErr.Error()}
	default:
		return Action{Kind: ActionClaim}
	}
}
