package planner

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// closureConclusion is the conclusion of a closure item in these tests.
const closureConclusion = "the findings this task corrects are closed"

// closureRolePara is the paragraph role.md gives the planner on when a task may carry evidence of kind closure.
const closureRolePara = "A task planned for a ledger trigger whose correction only changes tests, such as deleting, " +
	"merging, renaming or re-timing tests for a test_parsimony finding, and whose files are all test files (Go " +
	"_test.go, web .test.ts or .test.tsx, Kotlin under android/app/src/test/), may carry evidence of kind closure: " +
	"its conclusion names the findings it corrects, and every other field is empty. A closure task runs no red and " +
	"no green; it moves from its gate to review, and the round's closure check settles it. The check refuses kind " +
	"closure outside a correction round, on a task that names any other file, and on a Requirement's or an " +
	"amendment's item."

func TestCheckClosureEvidenceKind(t *testing.T) {
	t.Parallel()
	t.Run("the planner check refuses the kind outside a correction round with a message naming the rule",
		func(t *testing.T) {
			t.Parallel()
			task := Task{
				Title:       "T",
				Deliverable: "a deliverable",
				Files:       []string{"internal/x/x_test.go"},
				Evidence:    evidence.Item{Kind: evidence.KindClosure, Conclusion: closureConclusion},
			}
			const line = `tasks[0] "T" evidence.kind: closure is only for a task planned in a correction round; ` +
				`this epic is in phase accepted`
			checkLines(t, checkClosureTasks(Return{Tasks: []Task{task}}, "accepted"), line)
			// The return check of a planner step runs the same rule on the return, so the return is refused with the line.
			const plan = `{"status":"done","decisions":[],"tasks":[{"title":"T","deliverable":"d",` +
				`"files":["internal/x/x_test.go"],"requirements":["R1"],"gate":"check",` +
				`"evidence":{"kind":"closure","conclusion":"c"}}],"outline":"o","questions":[],"outcome":"work"}`
			epic := store.EpicRow{Phase: "accepted", Requirements: []store.EpicRequirement{{ID: "R1"}}}
			check := returnCheck(context.Background(), epic, store.Review{}, testResolver{}, nil, false)
			got := check(json.RawMessage(plan))
			if want := []string{line}; !reflect.DeepEqual(got, want) {
				t.Errorf("returnCheck() = %q, want %q", got, want)
			}
		})
	t.Run("the planner check refuses it for a task naming a non-test file with a message naming the rule",
		func(t *testing.T) {
			t.Parallel()
			task := Task{
				Title:       "T",
				Deliverable: "a deliverable",
				Files:       []string{"internal/x/x.go", "internal/x/x_test.go", "android/app/src/main/x/A.kt"},
				Evidence:    evidence.Item{Kind: evidence.KindClosure, Conclusion: closureConclusion},
			}
			const first = `tasks[0] "T" files[0]: not a test file; a closure-kind task names only test files ` +
				`(Go _test.go, web .test.ts or .test.tsx, Kotlin under android/app/src/test/)`
			const third = `tasks[0] "T" files[2]: not a test file; a closure-kind task names only test files ` +
				`(Go _test.go, web .test.ts or .test.tsx, Kotlin under android/app/src/test/)`
			checkLines(t, checkClosureTasks(Return{Tasks: []Task{task}}, phaseCorrecting), first, third)
			// The return check of a planner step runs the same rule on the return, so the return is refused with both.
			const plan = `{"status":"done","decisions":[],"tasks":[{"title":"T","deliverable":"d",` +
				`"files":["internal/x/x.go","internal/x/x_test.go","android/app/src/main/x/A.kt"],` +
				`"requirements":["R1"],"gate":"check","evidence":{"kind":"closure","conclusion":"c"}}],` +
				`"outline":"o","questions":[],"outcome":"work"}`
			epic := store.EpicRow{Phase: phaseCorrecting, Requirements: []store.EpicRequirement{{ID: "R1"}}}
			check := returnCheck(context.Background(), epic, store.Review{}, testResolver{}, nil, false)
			got := check(json.RawMessage(plan))
			if want := []string{first, third}; !reflect.DeepEqual(got, want) {
				t.Errorf("returnCheck() = %q, want %q", got, want)
			}
		})
	t.Run("a task whose files are android/app/src/test Kotlin files may carry the kind", func(t *testing.T) {
		t.Parallel()
		task := validTask("T", "android/app/src/test/x/ATest.kt")
		task.Evidence = evidence.Item{Kind: evidence.KindClosure, Conclusion: closureConclusion}
		ret := Return{Tasks: []Task{task}}
		checkLines(t, checkClosureTasks(ret, phaseCorrecting))
		checkLines(t, checkPlan(ret, nil))
	})
	t.Run("an amendment's item of kind closure is refused", func(t *testing.T) {
		t.Parallel()
		item := amendedItem(closureConclusion)
		item.Kind = evidence.KindClosure
		ret := Return{Actions: []EngineAction{{
			Kind: store.ActionAmendEvidence, Requirement: "R1", Item: item, Reason: "r",
		}}}
		checkLines(t, checkPlan(ret, nil),
			`actions[0] "amend_evidence" item.kind: closure is for a task in a correction round only; `+
				`a Requirement's or an amendment's item is test, capture or supplied`)
	})
	t.Run("the planner role says when to use the kind", func(t *testing.T) {
		t.Parallel()
		if !strings.Contains(role, closureRolePara) {
			t.Errorf("role text lacks the paragraph on when to use the closure kind")
		}
	})
}
