package planner

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// storedClaim is a stored task of seq and status that claims requirements.
func storedClaim(seq int64, status string, requirements ...string) store.EpicTask {
	return store.EpicTask{Seq: seq, Title: "stored", Requirements: requirements, Status: status}
}

func TestUnclaimedLines(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		tasks []store.EpicTask
		want  []string
	}{
		{
			name: "no stored task names every Requirement in contract order",
			want: []string{"- R1 is claimed by no live task", "- R2 is claimed by no live task"},
		},
		{
			name:  "an open claim is live",
			tasks: []store.EpicTask{storedClaim(1, "open", "R1")},
			want:  []string{"- R2 is claimed by no live task"},
		},
		{
			name:  "a done claim is live",
			tasks: []store.EpicTask{storedClaim(1, "done", "R1", "R2")},
		},
		{
			name:  "a gapped claim is live: a gap holds its claims",
			tasks: []store.EpicTask{storedClaim(2, "gap", "R1")},
			want:  []string{"- R2 is claimed by no live task"},
		},
		{
			name:  "a failed claim is not live",
			tasks: []store.EpicTask{storedClaim(2, "failed", "R1")},
			want:  []string{"- R1 is claimed by no live task", "- R2 is claimed by no live task"},
		},
		{
			name:  "a claim of a Requirement outside the epic covers none of the epic's",
			tasks: []store.EpicTask{storedClaim(1, "open", "R9")},
			want:  []string{"- R1 is claimed by no live task", "- R2 is claimed by no live task"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := unclaimedLines(epicRequirements(), tc.tasks)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("unclaimedLines() = %q, want %q", got, tc.want)
			}
		})
	}
}

// contributingPlan is a planner return of two tasks: the first claims R1 and contributes to R2, which the second
// claims.
const contributingPlan = `{"status":"done","decisions":[],"tasks":[` +
	`{"title":"Build it","deliverable":"the runner","files":["internal/planner/runner.go"],` +
	`"requirements":["R1"],"contributes":["R2"],"gate":"check",` +
	`"evidence":{"capture":["scripts/check"],"limit":60,"artifacts":[],"supplied":[],` +
	`"conclusion":"the runner runs","judge":"a judge","kind":"capture"}},` +
	`{"title":"Test it","deliverable":"the runner tests","files":["internal/planner/runner_test.go"],` +
	`"requirements":["R2"],"contributes":[],"gate":"check",` +
	`"evidence":{"capture":["scripts/check"],"limit":60,"artifacts":[],"supplied":[],` +
	`"conclusion":"the runner is tested","judge":"a judge","kind":"capture"}}],"outline":"two tasks",` +
	`"questions":[],"outcome":"work"}`

func TestPlanOfCopiesContributes(t *testing.T) {
	t.Parallel()
	ret, err := Parse(json.RawMessage(contributingPlan))
	if err != nil {
		t.Fatalf("Parse(contributingPlan) = %v", err)
	}
	tasks := planOf(ret).Tasks
	if len(tasks) != 2 {
		t.Fatalf("planOf tasks = %+v, want two", tasks)
	}
	if want := []string{"R2"}; !reflect.DeepEqual(tasks[0].Contributes, want) {
		t.Errorf("task 1 contributes = %q, want %q", tasks[0].Contributes, want)
	}
	if want := []string{}; !reflect.DeepEqual(tasks[1].Contributes, want) {
		t.Errorf("task 2 contributes = %q, want %q", tasks[1].Contributes, want)
	}
}
