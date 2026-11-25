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
// integrate run that ended passed at the stored base, whose reproduction of R1 exited 0, and rulings on it.
func disagreementFlow(rulings ...store.ReproRuling) store.EpicFlow {
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
			ID: store.NodeWorkID("int-run", 1, "repro R1"), State: store.NodeEnded,
		}},
		Rulings: rulings,
	}
}

// TestRequirementClaimsShowsDisagreement checks that a Requirement's disagreement is read from the rulings the flow
// holds: a judge's ruling that the reproduction contradicts red shows it, with the judge's reason, and a reproduction
// that exited 0 with no ruling, or one ruled not to contradict red, shows none.
func TestRequirementClaimsShowsDisagreement(t *testing.T) {
	t.Parallel()
	check := store.NodeWorkID("int-run", 1, "repro R1")
	cases := []struct {
		name    string
		rulings []store.ReproRuling
		want    string
	}{
		{
			name: "a ruling that the reproduction contradicts red shows it",
			rulings: []store.ReproRuling{{
				Check: check, Step: "judge-0", Verdict: evidence.VerdictFails, Reason: "TestSuite asserts nothing new",
			}},
			want: "R1: the judge ruled that its reproduction at the epic's base base-1 contradicts red: TestSuite " +
				"asserts nothing new",
		},
		{
			name:    "a ruling that it does not contradict red shows none",
			rulings: []store.ReproRuling{{Check: check, Step: "judge-0", Verdict: evidence.VerdictHolds}},
			want:    "",
		},
		{name: "a reproduction that exited 0 with no ruling shows none", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			epic := &v1.StoredEpic{Contract: &v1.EpicContract{Requirements: []*v1.EpicRequirement{{Id: "R1"}}}}
			requirementClaims(epic, disagreementFlow(tc.rulings...))
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
	createEpicOK(t, st, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
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
