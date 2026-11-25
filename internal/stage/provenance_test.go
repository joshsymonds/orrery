package stage

import (
	"fmt"
	"reflect"
	"testing"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// provenanceReads returns the step reads of the contract id, as the store lists them, failing t when it cannot.
func provenanceReads(t *testing.T, fx stageResearchFixture, id string) []store.StepRead {
	t.Helper()
	reads, err := fx.store.ContractStepReads(t.Context(), id)
	if err != nil {
		t.Fatalf("ContractStepReads %s: %v", id, err)
	}
	return reads
}

// reviseSteelmanContract revises steelmanIDA to version 2, a body titled "Contract A, revised", by the conversation
// that made it.
func reviseSteelmanContract(t *testing.T, fx stageResearchFixture) {
	t.Helper()
	revised := steelmanFixtureContract(steelmanIDA, "Contract A, revised")
	if err := fx.store.ReviseContract(t.Context(), steelmanIDA, store.ContractVersion{
		Version: 2, Repository: "/repo", Title: revised.GetTitle(), Care: "serious", Body: steelmanBody(t, revised),
		Words: "revised words", Writer: "conversation-one", At: stageResearchNow().Unix(),
	}); err != nil {
		t.Fatalf("ReviseContract %s: %v", steelmanIDA, err)
	}
}

// failureValue returns the F1 row's value, a failure rated rating.
func failureValue(rating string) string {
	return fmt.Sprintf(`{"text":"The page drew a stand-in","rating":%q,"response":"prevent"}`, rating)
}

func TestRecordsProvenanceDiscoveryReadsVersionOne(t *testing.T) {
	t.Parallel()
	fx := newStageSteelmanFixture(t)
	proposeSteelmanContract(t, fx, steelmanFixtureContract(steelmanIDA, "Contract A"))
	reply, err := steelmanPass(t, fx, stageDesign, &v1.SteelmanRequest{})
	if err != nil {
		t.Fatalf("Steelman: %v", err)
	}
	reviseSteelmanContract(t, fx)

	// The discovery read version 1, which the revision to version 2 does not change: version 2 has no read.
	want := []store.StepRead{{Step: reply.GetStep().GetStep(), Contract: steelmanIDA, Version: 1}}
	if got := provenanceReads(t, fx, steelmanIDA); !reflect.DeepEqual(got, want) {
		t.Fatalf("reads of %s = %+v, want the discovery at version 1 alone, %+v", steelmanIDA, got, want)
	}
}

func TestRecordsProvenanceDraftReadKeepsVersionZero(t *testing.T) {
	t.Parallel()
	fx := newStageSteelmanFixture(t)
	draft := steelmanFixtureContract("draft-zeta", "Zeta draft")
	if err := fx.store.DraftContract(t.Context(), store.ContractDraft{
		ID: draft.GetId(), Design: stageDesign, Repository: "/repo", Title: draft.GetTitle(),
		Body: steelmanBody(t, draft), Words: "words", Writer: "conversation-one", Created: stageResearchNow(),
	}); err != nil {
		t.Fatalf("DraftContract: %v", err)
	}
	reply, err := steelmanPass(t, fx, stageDesign, &v1.SteelmanRequest{})
	if err != nil {
		t.Fatalf("Steelman: %v", err)
	}

	want := []store.StepRead{{Step: reply.GetStep().GetStep(), Contract: draft.GetId(), Version: 0}}
	if got := provenanceReads(t, fx, draft.GetId()); !reflect.DeepEqual(got, want) {
		t.Fatalf("reads of draft %s = %+v, want the discovery at version 0, %+v", draft.GetId(), got, want)
	}
}

func TestRecordsProvenanceClosureReadsNewestVersion(t *testing.T) {
	t.Parallel()
	fx := newStageSteelmanFixture(t)
	proposeSteelmanContract(t, fx, steelmanFixtureContract(steelmanIDA, "Contract A"))
	discovery, err := steelmanPass(t, fx, stageDesign, &v1.SteelmanRequest{})
	if err != nil {
		t.Fatalf("Steelman discovery: %v", err)
	}
	returnSteelmanStep(t, fx, discovery.GetStep().GetStep(), ledgerDiscoveryReturn(t))
	reviseSteelmanContract(t, fx)

	closure, err := steelmanPass(t, fx, stageDesign, &v1.SteelmanRequest{Delta: "R1 now names the queue."})
	if err != nil {
		t.Fatalf("Steelman closure: %v", err)
	}
	// The closure reads the newest version, 2; the discovery keeps the version 1 it read.
	want := []store.StepRead{
		{Step: discovery.GetStep().GetStep(), Contract: steelmanIDA, Version: 1},
		{Step: closure.GetStep().GetStep(), Contract: steelmanIDA, Version: 2},
	}
	if got := provenanceReads(t, fx, steelmanIDA); !reflect.DeepEqual(got, want) {
		t.Fatalf("reads of %s = %+v, want %+v", steelmanIDA, got, want)
	}
}

func TestRecordsProvenanceSkippedClosureStoresNoRead(t *testing.T) {
	t.Parallel()
	fx := newStageSteelmanFixture(t)
	proposeSteelmanContract(t, fx, steelmanFixtureContract(steelmanIDA, "Contract A"))
	discovery, err := steelmanPass(t, fx, stageDesign, &v1.SteelmanRequest{})
	if err != nil {
		t.Fatalf("Steelman discovery: %v", err)
	}
	returnSteelmanStep(t, fx, discovery.GetStep().GetStep(), ledgerDiscoveryReturn(t))

	reply, err := steelmanPass(t, fx, stageDesign, &v1.SteelmanRequest{
		SkipClosure: true, Reason: "a rename", Maker: &v1.StageMaker{Kind: store.StageMakerOwnerServe},
	})
	if err != nil {
		t.Fatalf("Steelman skip: %v", err)
	}
	if reply.GetStep() != nil || len(fx.starter.starts) != 1 {
		t.Fatalf("skip reply = %v with %d starts, want no step and only the discovery's start", reply,
			len(fx.starter.starts))
	}
	want := []store.StepRead{{Step: discovery.GetStep().GetStep(), Contract: steelmanIDA, Version: 1}}
	if got := provenanceReads(t, fx, steelmanIDA); !reflect.DeepEqual(got, want) {
		t.Fatalf("reads of %s after the skip = %+v, want only the discovery's, %+v", steelmanIDA, got, want)
	}
}

func TestRecordsProvenanceFailureRatingKeepsItsMaker(t *testing.T) {
	t.Parallel()
	fx := newStageSteelmanFixture(t)
	seedRowsSession(t, fx)
	proposeSteelmanContract(t, fx, steelmanFixtureContract(steelmanIDA, "Contract A"))
	if _, err := writeRowFrom(t, fx, rowsSession, &v1.WriteRowRequest{
		Contract: steelmanIDA, Row: "F1", Value: failureValue("serious"), Words: "Rated serious",
		Maker: conversationMaker(),
	}); err != nil {
		t.Fatalf("WriteRow F1 serious: %v", err)
	}
	if _, err := writeRowFrom(t, fx, rowsSession, &v1.WriteRowRequest{
		Contract: steelmanIDA, Row: "F1", Value: failureValue("minor"), Words: "Rated minor",
		Maker: &v1.StageMaker{Kind: store.StageMakerOwnerConversation, Quote: "Minor is fine."},
	}); err != nil {
		t.Fatalf("WriteRow F1 minor: %v", err)
	}

	at := stageResearchNow().Unix()
	want := []store.ContractRowWrite{
		{
			Contract: steelmanIDA, Seq: 1, Version: 1, Row: "F1", Action: store.RowActionWrite, Words: "Rated serious",
			Maker: store.StageMakerConversation, MakerReason: "The owner decided", Delegation: "Decide the rows",
			Session: rowsSession, At: at,
		},
		{
			Contract: steelmanIDA, Seq: 2, Version: 1, Row: "F1", Action: store.RowActionWrite, Words: "Rated minor",
			Maker: store.StageMakerOwnerConversation, Quote: "Minor is fine.", Session: rowsSession, At: at,
		},
	}
	got, err := fx.store.ContractRowWrites(t.Context(), steelmanIDA)
	if err != nil {
		t.Fatalf("ContractRowWrites: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("row writes = %+v, want %+v", got, want)
	}
}
