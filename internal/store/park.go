package store

// ParkClass is the class of an epic's park, which says who answers it first. A question class opens the epic's park
// question and adds its tell: the epic's steerer is told of the question and answers it, and the owner is asked only once
// the steerer escalates it, or when no steering session hears the epic (see SteererHolds). A planner class is answered
// by the planner: the park opens no question, adds no tell, and adds a pending trigger that wakes the planner with the
// park reason as its cause.
type ParkClass string

// The question classes. Each is a catastrophe the planner does not answer: its park question goes to the epic's
// steerer, which answers it or escalates it to the owner.
const (
	// ParkClassReleaseUndoNone is a release that stopped at a release step with undo none that failed.
	ParkClassReleaseUndoNone ParkClass = "release_undo_none"
	// ParkClassReleaseOwnerStop is a release the owner stopped: the stop is recorded at the attempt it stopped at.
	ParkClassReleaseOwnerStop ParkClass = "release_owner_stop"
	// ParkClassBranch is an epic branch that exists, is gone, has moved or is checked out elsewhere.
	ParkClassBranch ParkClass = "branch"
	// ParkClassRefMove is a ref move the ref guard parks as unexplained.
	ParkClassRefMove ParkClass = "ref_move"
	// ParkClassMetAtBase is a Requirement the evidence found met at the epic's base.
	ParkClassMetAtBase ParkClass = "met_at_base"
	// ParkClassOrchestrator is an orchestrator unable to run: its role or agent does not resolve, or its pinned workflow
	// does not load.
	ParkClassOrchestrator ParkClass = "orchestrator"
	// ParkClassRetryBound is a planner park that came back after two planner actions on its chain. The planner is not
	// woken again for it: the owner answers it.
	ParkClassRetryBound ParkClass = "retry_bound"
	// ParkClassStall is an epic the stall check found idle twice with nothing changed, or idle with no plan run to wake.
	ParkClassStall ParkClass = "stall"
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

// OpensQuestion reports whether a park of class c opens a park question rather than waking the planner: a question
// class, or the empty class. The empty class is the class of a park stored before 00050_park_class.sql, which its
// question answered, so it stays a question park.
func (c ParkClass) OpensQuestion() bool {
	switch c {
	case "", ParkClassReleaseUndoNone, ParkClassReleaseOwnerStop, ParkClassBranch, ParkClassRefMove,
		ParkClassMetAtBase, ParkClassOrchestrator, ParkClassRetryBound, ParkClassStall:
		return true
	case ParkClassNode, ParkClassTask, ParkClassLoop, ParkClassNotBuilt, ParkClassReleaseStep:
		return false
	}
	return false
}

// SteererHolds reports whether an open question, escalated when escalated is set, of an epic a steering session hears
// when heard is set, is its steerer's to answer rather than the owner's: a steering session hears the epic and the
// question is not escalated. The owner is asked an escalated question, and every open question of an epic no steering
// session hears, since no steerer is there to answer it or escalate it.
func SteererHolds(heard, escalated bool) bool {
	return heard && !escalated
}

// questionPark returns reason when a park of class opens a park question, and "" otherwise: the park plannerPark leaves
// out.
func questionPark(reason, class string) string {
	if !ParkClass(class).OpensQuestion() {
		return ""
	}
	return reason
}

// plannerPark returns reason when the planner answers a park of class, and "" otherwise: the park questionPark leaves
// out.
func plannerPark(reason, class string) string {
	if ParkClass(class).OpensQuestion() {
		return ""
	}
	return reason
}
