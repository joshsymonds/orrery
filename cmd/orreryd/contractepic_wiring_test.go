package main

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// contractWiringID is the id of the synthetic contract the contract wiring test proposes and accepts.
const contractWiringID = "orrery-1a2b3c4/contract-wiring"

// TestContractEpicOwnerAcceptWiringNoTicker checks that a --sessions orreryd delivers an owner's accept of a contract
// only by the round that the store write of the accept wakes. The synthetic contract is proposed under the step-kind
// session callerEventCreator, which receives nothing and is marked. No pass runs between the start round and the
// accept, and options has no tick seam.
func TestContractEpicOwnerAcceptWiringNoTicker(t *testing.T) {
	st, getenv := openCallerWiringStore(t)
	addStepSession(t, st, callerEventCreator)
	proposeWiringContract(t, st)

	passed := make(chan struct{}, 16)
	host := startCallerWiringHost(t, st, getenv, passed)
	awaitCallerRound(passed)
	requireNoCallerRound(t, passed)

	accept := store.ContractChange{Session: "", Words: "Accept the synthetic contract", At: attentionWiringStamp}
	if err := st.AcceptContract(t.Context(), contractWiringID, accept); err != nil {
		t.Fatalf("accept contract %s: %v", contractWiringID, err)
	}
	awaitCallerRound(passed)
	if !markedIn(t, st, store.ContractStateEvent(contractWiringID, 2), callerEventCreator) {
		t.Fatalf("owner accept not delivered by the round its store write woke")
	}
	requireNoCallerEventTicks(t)

	if err := host.stop(); err != nil {
		t.Fatalf("stop sessions: %v", err)
	}
}

// proposeWiringContract proposes contractWiringID under callerEventCreator, with its one decision answered, so the
// owner can accept it.
func proposeWiringContract(t *testing.T, st *store.Store) {
	t.Helper()
	proposal := store.ContractVersion{
		Version: 1, Repository: "/work/synthetic-repo", Title: "Synthetic contract", Care: "serious",
		Body: `{"id":"synthetic-contract"}`,
		Decisions: []store.ContractDecision{
			{
				ID: "D1", Question: "Which store?", Options: []string{"sqlite", "postgres"},
				Recommendation: "sqlite", Reason: "Already here", Row: "R1", Answer: "sqlite",
			},
		},
		Words: "Propose the synthetic contract", Writer: callerEventCreator, At: attentionWiringStamp,
	}
	if err := st.ProposeContract(t.Context(), contractWiringID, proposal); err != nil {
		t.Fatalf("propose contract %s: %v", contractWiringID, err)
	}
}
