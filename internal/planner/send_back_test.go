package planner

import (
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// sendBackRoleSentence is the sentence role.md gives the planner about send_back: what the action does, from where, and
// what the next implementer is told.
const sendBackRoleSentence = "send_back names a task by its number and sends it back to implement on rung 1 from any " +
	"rung, open, done or ended as a gap, with the reason as the failure its next implementer's brief carries."

// sendBackNoResolve is a gate and judge that resolve every name, for a return that names none.
func sendBackNoResolve(string) error {
	return nil
}

// sendBackCheck returns the refusals of a return of one send_back action with args.
func sendBackCheck(t *testing.T, args string) []string {
	t.Helper()
	ret, err := Parse(actionPlan(store.ActionSendBack, args))
	if err != nil {
		t.Fatalf("Parse(send_back %s): %v", args, err)
	}
	return Check(ret, nil, nil, sendBackNoResolve, sendBackNoResolve)
}

// TestSendBackActionPassesCheckWithATaskAndAReason checks that a send_back that names an existing task by number and
// states a reason passes the check, with the reason read as the action's own.
func TestSendBackActionPassesCheckWithATaskAndAReason(t *testing.T) {
	t.Parallel()
	if refusals := sendBackCheck(t, `{"task":2}`); refusals != nil {
		t.Errorf("Check(send_back task 2) = %q, want no refusal", refusals)
	}
}

// TestSendBackActionNeedsATaskAboveZero checks that a send_back with no task, or a task of 0, is refused on its task.
func TestSendBackActionNeedsATaskAboveZero(t *testing.T) {
	t.Parallel()
	for _, args := range []string{`{}`, `{"task":0}`} {
		want := `actions[0] "send_back" task: ` + reasonAboveZero
		if refusals := sendBackCheck(t, args); !slices.Contains(refusals, want) {
			t.Errorf("Check(send_back %s) = %q, want %q", args, refusals, want)
		}
	}
}

// TestSendBackActionRefusesFieldsItDoesNotUse checks that a send_back that names a field its kind does not use, such as
// a deliverable, is refused on that field.
func TestSendBackActionRefusesFieldsItDoesNotUse(t *testing.T) {
	t.Parallel()
	want := `actions[0] "send_back" deliverable: ` + reasonUnused
	if refusals := sendBackCheck(t, `{"task":2,"deliverable":"x"}`); !slices.Contains(refusals, want) {
		t.Errorf("Check(send_back with a deliverable) = %q, want %q", refusals, want)
	}
}

// TestSendBackRoleSaysWhenToUseIt checks that the role text describes send_back, its form and its effect.
func TestSendBackRoleSaysWhenToUseIt(t *testing.T) {
	t.Parallel()
	assertRoleHasSentence(t, sendBackRoleSentence)
	if !strings.Contains(role, `send_back {"task": <number>}`) {
		t.Errorf("role text does not give the args of send_back as send_back {\"task\": <number>}")
	}
}
