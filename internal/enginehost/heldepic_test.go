package enginehost

import (
	"testing"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// heldCreateDesign is the design the held create test puts its two contracts in. heldCreatePredecessor is the contract
// the epic's contract starts after, which has no epic.
const (
	heldCreateDesign      = "synthetic-repo-0a1b2c3/design-held-create"
	heldCreatePredecessor = "synthetic-repo-0a1b2c3/held-predecessor"
)

// proposeAcceptHeldCreate proposes contract into heldCreateDesign as version 1 by contractEpicWriter, then accepts it as
// the owner. It fails the test on error.
func proposeAcceptHeldCreate(t *testing.T, st *store.Store, contract *v1.EpicContract) {
	t.Helper()
	id := contract.GetId()
	version, err := contractVersionOf(id, contract, nil, "Synthetic words.", contractEpicWriter, contractEpicAt)
	if err != nil {
		t.Fatalf("contractVersionOf %s: %v", id, err)
	}
	if err = st.ProposeContractInDesign(t.Context(), heldCreateDesign, id, version); err != nil {
		t.Fatalf("ProposeContractInDesign %s: %v", id, err)
	}
	if err = st.AcceptContract(t.Context(), id, store.ContractChange{At: contractEpicAt + 1}); err != nil {
		t.Fatalf("AcceptContract %s: %v", id, err)
	}
}

// TestDesignHeldEpicCreateRPC asserts that CreateEpic through the Epics service returns the epic of a contract that
// starts after a contract with no epic in phase held.
func TestDesignHeldEpicCreateRPC(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	repo := stepGitTestRepo(t)
	c := fullEpicRequest(repo, epicServiceID)
	if err := st.CreateDesign(t.Context(), store.Design{
		ID: heldCreateDesign, Title: "Synthetic design", Conversation: contractEpicWriter,
		Created: time.Unix(contractEpicAt, 0),
	}); err != nil {
		t.Fatalf("CreateDesign %s: %v", heldCreateDesign, err)
	}
	proposeAcceptHeldCreate(t, st, &v1.EpicContract{
		Id: heldCreatePredecessor, Repository: repo, Title: "Held predecessor", Care: "serious",
	})
	proposeAcceptHeldCreate(t, st, c.Contract)
	if err := st.AddDesignEdge(
		t.Context(), heldCreateDesign, epicServiceID, heldCreatePredecessor, time.Unix(contractEpicAt+2, 0),
	); err != nil {
		t.Fatalf("AddDesignEdge: %v", err)
	}

	created, err := client.CreateEpic(t.Context(), connect.NewRequest(c.request()))
	if err != nil {
		t.Fatalf("CreateEpic %s: %v", epicServiceID, err)
	}
	if got := created.Msg.GetEpic().GetPhase(); got != store.PhaseHeld {
		t.Fatalf("created epic phase = %q, want %q", got, store.PhaseHeld)
	}
}
