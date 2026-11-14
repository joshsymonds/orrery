package enginehost

import (
	"regexp"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// epicEvidenceGates returns the gates of the evidence test contract: two argv gates whose needs are out of pool order,
// and two agent gates, one with a must and a must_not.
func epicEvidenceGates() []*v1.EpicGate {
	return []*v1.EpicGate{
		{
			Name: "unit", Argv: []string{"scripts/unit"}, Limit: 120,
			Needs: []*v1.EpicGateNeed{{Pool: "beta", Count: 2}, {Pool: "alpha", Count: 1}},
		},
		{Name: "check", Argv: []string{"scripts/check"}, Limit: 60},
		{
			Name: "review", Agent: "reviewer", Limit: 300,
			Must: []string{"Synthetic statement holds."}, MustNot: []string{"Synthetic statement fails."},
		},
		{Name: "audit", Agent: "auditor", Limit: 200, Must: []string{"Synthetic audit holds."}},
	}
}

// TestEpicCreateStoresBaseAsFullCommit asserts a base named by a rev is stored, and returned, as the full commit it names.
func TestEpicCreateStoresBaseAsFullCommit(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	repo := stepGitTestRepo(t)
	req := fullEpicRequest(repo, epicServiceID)
	req.Contract.Base = "HEAD"
	head := epicTestBase(repo)

	stored := createEpicOK(t, client, req)
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(head) {
		t.Fatalf("HEAD = %q, want a full 40-hex commit", head)
	}
	if got := stored.GetContract().GetBase(); got != head {
		t.Fatalf("created base = %q, want the full commit %q", got, head)
	}
	if got := getEpicOK(t, client).GetContract().GetBase(); got != head {
		t.Fatalf("GetEpic base = %q, want the full commit %q", got, head)
	}
	row, err := st.Epic(t.Context(), epicServiceID)
	if err != nil {
		t.Fatalf("store Epic: %v", err)
	}
	if row.Base != head {
		t.Fatalf("stored base = %q, want the full commit %q", row.Base, head)
	}
}

// TestEpicCreateStoresGatesListsAndItems asserts the gates, the Done and review lists and each Requirement's capture
// or supplied item come back from CreateEpic and GetEpic equal to the request, in order.
func TestEpicCreateStoresGatesListsAndItems(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	repo := stepGitTestRepo(t)
	req := fullEpicRequest(repo, epicServiceID)
	req.Contract.Base = "HEAD"
	req.Contract.Gates = epicEvidenceGates()
	req.Contract.DoneGates = []string{"check", "unit"}
	req.Contract.ReviewGates = []string{"audit", "review"}
	req.Contract.Requirements[0].Item = &v1.EvidenceItem{
		Capture:    []string{"scripts/unit"},
		Limit:      120,
		Artifacts:  []string{"out/unit.json"},
		Conclusion: "Synthetic conclusion.",
		Judge:      "reviewer",
	}
	req.Contract.Requirements[1].Item = &v1.EvidenceItem{
		Supplied:   []string{"docs/report.md"},
		Conclusion: "Synthetic conclusion.",
		Judge:      "reviewer",
	}
	want, ok := proto.Clone(req.GetContract()).(*v1.EpicContract)
	if !ok {
		t.Fatal("clone of the contract is not an EpicContract")
	}
	want.Base = epicTestBase(repo)

	stored := createEpicOK(t, client, req)
	if !proto.Equal(stored.GetContract(), want) {
		t.Fatalf("created contract = %v, want %v", stored.GetContract(), want)
	}
	got := getEpicOK(t, client)
	if !proto.Equal(got, stored) {
		t.Fatalf("GetEpic = %v, want the created epic %v", got, stored)
	}
}

// TestEpicGetLegacyRequirementReadsNilItem asserts an epic stored before O3d2, whose requirement keeps its evidence
// and kind and no item, reads back with a nil item and its evidence and kind.
func TestEpicGetLegacyRequirementReadsNilItem(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	legacy := time.Unix(1700000000, 0)
	if err := st.CreateEpic(t.Context(), store.EpicRow{
		ID:      epicServiceID,
		Title:   "Legacy epic",
		Intent:  "Synthetic intent.",
		Care:    "serious",
		Phase:   "accepted",
		Created: legacy,
		Changed: legacy,
		Requirements: []store.EpicRequirement{{
			ID: "R1", Text: "Synthetic legacy requirement.", Evidence: "Synthetic legacy evidence.", Kind: "test",
		}},
	}); err != nil {
		t.Fatalf("store CreateEpic: %v", err)
	}

	requirements := getEpicOK(t, client).GetContract().GetRequirements()
	if len(requirements) != 1 {
		t.Fatalf("requirements = %d, want 1", len(requirements))
	}
	requirement := requirements[0]
	if requirement.GetItem() != nil {
		t.Fatalf("item = %v, want nil for a legacy requirement", requirement.GetItem())
	}
	if requirement.GetEvidence() != "Synthetic legacy evidence." || requirement.GetKind() != "test" {
		t.Fatalf(
			"evidence %q kind %q, want the stored legacy evidence and kind",
			requirement.GetEvidence(), requirement.GetKind(),
		)
	}
}
