package store

// ParkClass is the class of an epic's park, which says who answers it. An owner class is answered by the owner: the park
// opens the owner's question and adds its tell. A planner class is answered by the planner: the park opens no question,
// adds no tell, and adds a pending trigger that wakes the planner with the park reason as its cause.
type ParkClass string

// The owner classes. Each is a catastrophe the owner decides.
const (
	// ParkClassReleaseUndoNone is a release that stopped at a release step with undo none that failed.
	ParkClassReleaseUndoNone ParkClass = "release_undo_none"
	// ParkClassReleaseOwnerStop is a release the owner stopped: the epic no longer holds its release slot.
	ParkClassReleaseOwnerStop ParkClass = "release_owner_stop"
	// ParkClassBranch is an epic branch that exists, is gone, has moved or is checked out elsewhere.
	ParkClassBranch ParkClass = "branch"
	// ParkClassRefMove is a ref move the ref guard parks as unexplained.
	ParkClassRefMove ParkClass = "ref_move"
	// ParkClassMetAtBase is a Requirement the evidence found met at the epic's base.
	ParkClassMetAtBase ParkClass = "met_at_base"
	// ParkClassOrchestrator is an orchestrator unable to run: its role or agent does not resolve, three planner turns
	// failed in a row, or its pinned workflow does not load.
	ParkClassOrchestrator ParkClass = "orchestrator"
	// ParkClassRetryBound is a planner park that came back after two planner actions on its chain. The planner is not
	// woken again for it: the owner answers it.
	ParkClassRetryBound ParkClass = "retry_bound"
)

// The planner classes. Each is answered by the planner's next turn.
const (
	// ParkClassNode is a node run that failed.
	ParkClassNode ParkClass = "node"
	// ParkClassTask is a task whose build parks.
	ParkClassTask ParkClass = "task"
	// ParkClassLoop is a loop that ran out of rounds.
	ParkClassLoop ParkClass = "loop"
	// ParkClassNotBuilt is a plan run that ended with Requirements unbuilt.
	ParkClassNotBuilt ParkClass = "not_built"
	// ParkClassReleaseStep is a release that stopped at a release step the planner may resume.
	ParkClassReleaseStep ParkClass = "release_step"
)

// OwnerHeld reports whether the owner answers a park of class c: an owner class, or the empty class. The empty class is
// the class of a park stored before 00050_park_class.sql, which the owner's question answered, so it stays the owner's.
func (c ParkClass) OwnerHeld() bool {
	switch c {
	case "", ParkClassReleaseUndoNone, ParkClassReleaseOwnerStop, ParkClassBranch, ParkClassRefMove,
		ParkClassMetAtBase, ParkClassOrchestrator, ParkClassRetryBound:
		return true
	case ParkClassNode, ParkClassTask, ParkClassLoop, ParkClassNotBuilt, ParkClassReleaseStep:
		return false
	}
	return false
}

// ownedPark returns reason when the owner answers a park of class, and "" otherwise. Owner-facing readers report a park
// through it, so a planner park is not one the owner is asked about.
func ownedPark(reason, class string) string {
	if !ParkClass(class).OwnerHeld() {
		return ""
	}
	return reason
}
