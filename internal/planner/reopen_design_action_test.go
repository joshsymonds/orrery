package planner

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestReopenDesignActionArgsAreParsedAndChecked checks that a reopen_design action reads its task and deliverable from its
// args and carries both to the store, and that Check refuses a task of 0, an empty deliverable, and each field the kind
// does not use.
func TestReopenDesignActionArgsAreParsedAndChecked(t *testing.T) {
	t.Parallel()
	const deliverable = "the interface, widened to take a list"
	args := approachArgs(t, map[string]any{"task": 2, "deliverable": deliverable})

	ret, err := Parse(actionPlan(store.ActionReopenDesign, args))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := ret.Actions[0]
	if got.Task != 2 || got.Deliverable != deliverable || got.ArgsError != "" || len(got.Unknown) != 0 {
		t.Errorf("Parse reopen_design = %+v, want task 2 and the deliverable read with no error", got)
	}
	checkLines(t, checkPlan(ret, nil))
	if action := planOf(ret).Actions[0]; action.Kind != store.ActionReopenDesign || action.Task != 2 ||
		action.Deliverable != deliverable {
		t.Errorf("planOf action = %+v, want the task and deliverable carried to the store", action)
	}

	cases := []struct {
		name, args, want string
	}{
		{
			name: "a task of 0",
			args: approachArgs(t, map[string]any{"deliverable": deliverable}),
			want: `actions[0] "reopen_design" task: not above 0`,
		},
		{
			name: "an empty deliverable",
			args: approachArgs(t, map[string]any{"task": 2, "deliverable": "  "}),
			want: `actions[0] "reopen_design" deliverable: empty`,
		},
		{
			name: "a file set on reopen_design",
			args: approachArgs(t, map[string]any{"task": 2, "deliverable": deliverable, "files": []string{"b.go"}}),
			want: `actions[0] "reopen_design" files: not used by this kind; leave it empty or 0`,
		},
		{
			name: "an evidence item set on reopen_design",
			args: approachArgs(t, map[string]any{
				"task": 2, "deliverable": deliverable, "item": amendedItem("the revision holds"),
			}),
			want: `actions[0] "reopen_design" item: not used by this kind; leave it empty or 0`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ret, err := Parse(actionPlan(store.ActionReopenDesign, tc.args))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			checkLines(t, checkPlan(ret, nil), tc.want)
		})
	}
}
