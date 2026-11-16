package enginehost

import (
	"slices"
	"testing"
	"time"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// requirementClaimTask returns a synthetic task with seq, status, the Requirements it claims and the Requirements it
// contributes to.
func requirementClaimTask(seq int64, status string, claims, contributes []string) store.EpicTask {
	return store.EpicTask{Seq: seq, Status: status, Requirements: claims, Contributes: contributes}
}

// requirementClaimRun returns a synthetic evidence run of the item, in phase, that passed or failed.
func requirementClaimRun(item, phase string, passed bool) store.EvidenceRun {
	return store.EvidenceRun{Item: item, Phase: phase, Passed: passed}
}

func TestRequirementClaims(t *testing.T) {
	t.Parallel()
	const live, failed = "pending", "failed"
	cases := []struct {
		name         string
		tasks        []store.EpicTask
		evidence     []store.EvidenceRun
		owner        string
		contributors []string
		state        string
	}{
		{
			name:  "no task claims R1",
			tasks: []store.EpicTask{requirementClaimTask(1, live, []string{"R2"}, nil)},
			state: "pending",
		},
		{
			name: "a failed claim is skipped for a later live claim",
			tasks: []store.EpicTask{
				requirementClaimTask(1, failed, []string{"R1"}, nil),
				requirementClaimTask(2, live, []string{"R1"}, nil),
			},
			owner: "T2",
			state: "pending",
		},
		{
			name: "the lowest-seq live claim owns",
			tasks: []store.EpicTask{
				requirementClaimTask(3, live, []string{"R1"}, nil),
				requirementClaimTask(4, live, []string{"R1"}, nil),
			},
			owner: "T3",
			state: "pending",
		},
		{
			name: "contributors in seq order without failed ones",
			tasks: []store.EpicTask{
				requirementClaimTask(1, live, nil, []string{"R1"}),
				requirementClaimTask(2, failed, nil, []string{"R1"}),
				requirementClaimTask(3, live, nil, []string{"R1"}),
				requirementClaimTask(4, live, nil, []string{"R2"}),
			},
			contributors: []string{"T1", "T3"},
			state:        "pending",
		},
		{
			name: "a live claim owns and a live contribution is a contributor",
			tasks: []store.EpicTask{
				requirementClaimTask(1, live, nil, []string{"R1"}),
				requirementClaimTask(2, live, []string{"R1"}, nil),
			},
			owner:        "T2",
			contributors: []string{"T1"},
			state:        "pending",
		},
		{
			name:     "a run of another Requirement is not counted",
			evidence: []store.EvidenceRun{requirementClaimRun("R2", "green", true)},
			state:    "pending",
		},
		{
			name:     "a red run that failed is pending",
			evidence: []store.EvidenceRun{requirementClaimRun("R1", "red", false)},
			state:    "pending",
		},
		{
			name:     "a red run that passed is red",
			evidence: []store.EvidenceRun{requirementClaimRun("R1", "red", true)},
			state:    "red",
		},
		{
			name:     "a green run that passed is green",
			evidence: []store.EvidenceRun{requirementClaimRun("R1", "green", true)},
			state:    "green",
		},
		{
			name: "a passed green run wins over a passed red run",
			evidence: []store.EvidenceRun{
				requirementClaimRun("R1", "red", true),
				requirementClaimRun("R1", "green", true),
			},
			state: "green",
		},
		{
			name: "a passed green run wins over a passed red run in either order",
			evidence: []store.EvidenceRun{
				requirementClaimRun("R1", "green", true),
				requirementClaimRun("R1", "red", true),
			},
			state: "green",
		},
		{
			name: "a failed green run leaves a passed red run",
			evidence: []store.EvidenceRun{
				requirementClaimRun("R1", "red", true),
				requirementClaimRun("R1", "green", false),
			},
			state: "red",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			epic := &v1.StoredEpic{Contract: &v1.EpicContract{Requirements: []*v1.EpicRequirement{{Id: "R1"}}}}
			flow := store.EpicFlow{Epic: store.EpicRow{Tasks: tc.tasks}, Evidence: tc.evidence}
			requirementClaims(epic, flow)
			got := epic.GetContract().GetRequirements()[0]
			if got.GetOwner() != tc.owner {
				t.Errorf("owner = %q, want %q", got.GetOwner(), tc.owner)
			}
			if !slices.Equal(got.GetContributors(), tc.contributors) {
				t.Errorf("contributors = %v, want %v", got.GetContributors(), tc.contributors)
			}
			if got.GetState() != tc.state {
				t.Errorf("state = %q, want %q", got.GetState(), tc.state)
			}
		})
	}
}

// plannedContributesEpic stores epicServiceID with the two planned tasks of plannedReturn, where T2 contributes to R1,
// and returns an Epics client that serves it.
func plannedContributesEpic(t *testing.T) enginev1connect.EpicsClient {
	t.Helper()
	ctx := t.Context()
	st, _ := openPlannerStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	turn, err := st.ClaimTurn(ctx, epicServiceID, time.Unix(turnStarted, 0))
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	plan := plannedReturn()
	plan.Tasks[1].Contributes = []string{"R1"}
	if err = st.CompleteTurn(ctx, epicServiceID, turn.Seq, plan, time.Unix(turnCompleted, 0)); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	return client
}

// requirementWithID returns the Requirement of epic with id, failing the test when the contract holds no such one.
func requirementWithID(t *testing.T, epic *v1.StoredEpic, id string) *v1.EpicRequirement {
	t.Helper()
	for _, requirement := range epic.GetContract().GetRequirements() {
		if requirement.GetId() == id {
			return requirement
		}
	}
	t.Fatalf("epic has no Requirement %s", id)
	return nil
}

func TestEpicGetShowsRequirementClaimsAndTaskContributes(t *testing.T) {
	t.Parallel()
	epic := getEpicOK(t, plannedContributesEpic(t))

	r1 := requirementWithID(t, epic, "R1")
	if got := r1.GetOwner(); got != "T1" {
		t.Errorf("R1 owner = %q, want T1", got)
	}
	if got := r1.GetContributors(); !slices.Equal(got, []string{"T2"}) {
		t.Errorf("R1 contributors = %v, want [T2]", got)
	}
	if got := requirementWithID(t, epic, "R2").GetOwner(); got != "T1" {
		t.Errorf("R2 owner = %q, want T1", got)
	}

	if got := taskWithID(t, epic, "T2").GetContributes(); !slices.Equal(got, []string{"R1"}) {
		t.Errorf("T2 contributes = %v, want [R1]", got)
	}
	if got := taskWithID(t, epic, "T1").GetContributes(); len(got) != 0 {
		t.Errorf("T1 contributes = %v, want none", got)
	}
}
