package enginehost

import (
	"slices"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// disagreementFlow returns the flow of one Requirement R1 that names a test item, at base-1 stored at base-1, with an
// integrate run that ended passed at the stored base and whose reproduction of R1 ended with exit.
func disagreementFlow(exit int) store.EpicFlow {
	return store.EpicFlow{
		Epic: store.EpicRow{
			Base:       "base-1",
			StoredBase: "base-1",
			Requirements: []store.EpicRequirement{{
				ID: "R1", Text: "the suite passes", Item: evidence.Item{Kind: evidence.KindTest},
			}},
		},
		Runs: []store.NodeRun{{
			ID: "int-run", Kind: "integrate", Outcome: "passed", Revision: "base-1", Attempt: 1,
		}},
		Checks: []store.NodeCheck{{
			ID: store.NodeWorkID("int-run", 1, "repro R1"), State: store.NodeEnded, Exit: exit,
		}},
	}
}

func TestRequirementClaimsShowsDisagreement(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		exit int
		want string
	}{
		{
			name: "a reproduction that exited 0 disagrees with red",
			exit: 0,
			want: "R1 judge and exit disagree at the epic's base base-1: red was judged fails, but its reproduction " +
				"with the candidate's tests exited 0",
		},
		{name: "a reproduction that exited 1 agrees with red", exit: 1, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			epic := &v1.StoredEpic{Contract: &v1.EpicContract{Requirements: []*v1.EpicRequirement{{Id: "R1"}}}}
			requirementClaims(epic, disagreementFlow(tc.exit))
			if got := requirementWithID(t, epic, "R1").GetDisagreement(); got != tc.want {
				t.Errorf("R1 disagreement = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEpicGetShowsTestChangesAndGuardRefusal(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, _ := openPlannerStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	turn, err := st.ClaimTurn(ctx, epicServiceID, time.Unix(turnStarted, 0))
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	plan := plannedReturn()
	plan.Tasks[0].Files = []string{"a.go", "a_test.go", "b_test.go"}
	plan.Tasks[0].TestChanges = []string{"b_test.go", "a_test.go"}
	if err = st.CompleteTurn(ctx, epicServiceID, turn.Seq, plan, time.Unix(turnCompleted, 0)); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	refusal := "b_test.go: refused by the test rule of the guard"
	if err = st.SetTaskBuild(ctx, epicServiceID, 1, store.TaskBuild{}, store.TaskBuild{Guard: refusal}); err != nil {
		t.Fatalf("SetTaskBuild: %v", err)
	}

	epic := getEpicOK(t, client)
	t1 := taskWithID(t, epic, "T1")
	if got := t1.GetTestChanges(); !slices.Equal(got, []string{"b_test.go", "a_test.go"}) {
		t.Errorf("T1 test changes = %v, want [b_test.go a_test.go]", got)
	}
	if got := t1.GetGuard(); got != refusal {
		t.Errorf("T1 guard = %q, want %q", got, refusal)
	}
	t2 := taskWithID(t, epic, "T2")
	if got := t2.GetTestChanges(); len(got) != 0 {
		t.Errorf("T2 test changes = %v, want none", got)
	}
	if got := t2.GetGuard(); got != "" {
		t.Errorf("T2 guard = %q, want none", got)
	}
}
