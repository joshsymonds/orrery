package planner

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// approachArgs returns the args of a change_approach action holding fields, encoded as a JSON object.
func approachArgs(t *testing.T, fields map[string]any) string {
	t.Helper()
	encoded, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("encode args: %v", err)
	}
	return string(encoded)
}

func TestTaskLadderChangeApproachArgs(t *testing.T) {
	t.Parallel()
	const deliverable = "the change made another way"
	item := amendedItem("the approach holds")
	withItem := approachArgs(t, map[string]any{"task": 2, "deliverable": deliverable, "item": item})
	withoutItem := approachArgs(t, map[string]any{"task": 2, "deliverable": deliverable})

	ret, err := Parse(actionPlan(store.ActionChangeApproach, withItem))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := ret.Actions[0]
	if got.Task != 2 || got.Deliverable != deliverable || !reflect.DeepEqual(got.Item, item) ||
		got.ArgsError != "" || len(got.Unknown) != 0 {
		t.Errorf("Parse change_approach = %+v, want task 2, the deliverable and the item read with no error", got)
	}
	if lines := checkPlan(ret, nil); len(lines) != 0 {
		t.Errorf("Check of a well-formed change_approach with an item = %q, want none", lines)
	}
	if action := planOf(ret).Actions[0]; action.Task != 2 || action.Deliverable != deliverable ||
		!reflect.DeepEqual(action.Item, item) {
		t.Errorf("planOf action = %+v, want the task, deliverable and item carried to the store", action)
	}

	ret, err = Parse(actionPlan(store.ActionChangeApproach, withoutItem))
	if err != nil {
		t.Fatalf("Parse without an item: %v", err)
	}
	if lines := checkPlan(ret, nil); len(lines) != 0 {
		t.Errorf("Check of a well-formed change_approach without an item = %q, want none", lines)
	}

	cases := []struct {
		name, kind, args, want string
	}{
		{
			name: "a task of 0", kind: store.ActionChangeApproach,
			args: approachArgs(t, map[string]any{"task": 0, "deliverable": deliverable}),
			want: `actions[0] "change_approach" task: not above 0`,
		},
		{
			name: "an empty deliverable", kind: store.ActionChangeApproach,
			args: approachArgs(t, map[string]any{"task": 2, "deliverable": ""}),
			want: `actions[0] "change_approach" deliverable: empty`,
		},
		{
			name: "a node set on change_approach", kind: store.ActionChangeApproach,
			args: approachArgs(t, map[string]any{"task": 2, "deliverable": deliverable, "node": "plan/integrate"}),
			want: `actions[0] "change_approach" node: not used by this kind; leave it empty or 0`,
		},
		{
			name: "a malformed item", kind: store.ActionChangeApproach,
			args: approachArgs(t, map[string]any{
				"task": 2, "deliverable": deliverable, "item": amendedItem(""),
			}),
			want: `actions[0] "change_approach" item.conclusion: empty or blank`,
		},
		{
			name: "a deliverable set on a fail_task", kind: store.ActionFailTask,
			args: approachArgs(t, map[string]any{"task": 2, "deliverable": deliverable}),
			want: `actions[0] "fail_task" deliverable: not used by this kind; leave it empty or 0`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ret, err := Parse(actionPlan(tc.kind, tc.args))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got := checkPlan(ret, nil); !reflect.DeepEqual(got, []string{tc.want}) {
				t.Errorf("Check = %q, want only %q", got, tc.want)
			}
		})
	}
}
