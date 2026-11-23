package stage

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// The steps the ledger tests seed: a discovery step and a closure step, each with a steelman return as its kept return.
const (
	ledgerDiscovery = "step-discovery"
	ledgerClosure   = "step-closure"
)

// ledgerReturn returns the JSON text of a steelman return that holds fields.
func ledgerReturn(t *testing.T, fields map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("encode steelman return: %v", err)
	}
	return string(raw)
}

// ledgerFinding returns the discovery finding id, which concerns contract.
func ledgerFinding(id, contract string) map[string]string {
	return map[string]string{
		"id": id, "contract": contract, "impact": "impact of " + id, "evidence": "evidence of " + id,
		"change": "change " + id,
	}
}

// ledgerDiscoveryReturn returns a counted discovery return with findings S1, S2, S3 and S4, which concern R1, R2, P1
// and D1.
func ledgerDiscoveryReturn(t *testing.T) string {
	t.Helper()
	return ledgerReturn(t, map[string]any{
		"status": "done", "pass": "discovery", "verdict": "REVISE",
		"strongest_case": "the draft holds", "strongest_alternative": "a queue",
		"findings": []map[string]string{
			ledgerFinding("S1", "R1"), ledgerFinding("S2", "R2"), ledgerFinding("S3", "P1"), ledgerFinding("S4", "D1"),
		},
		"evidence_and_coverage": "read the repository",
	})
}

// ledgerClosureReturn returns a counted closure return with the ledger entries and concerns given.
func ledgerClosureReturn(t *testing.T, ledger, concerns []map[string]string) string {
	t.Helper()
	return ledgerReturn(t, map[string]any{
		"status": "done", "pass": "closure", "verdict": "STILL_OPEN", "ledger": ledger, "concerns": concerns,
		"evidence_and_coverage": "read the revision",
	})
}

// ledgerEntry returns closure's ledger entry for the item id, with its word and note.
func ledgerEntry(id, word, note string) map[string]string {
	return map[string]string{"id": id, "word": word, "note": note}
}

// seedLedgerStep stores step as a steelman step of kind that ended done, with result as its kept return, and stores its
// design_steps row on stageDesign.
func seedLedgerStep(t *testing.T, fx stageResearchFixture, step, kind, result string) {
	t.Helper()
	applyStageSteps(t, fx.store,
		store.StepAdd{
			ID: step, Agent: "steelman-agent", Prompt: "steelman packet", Workspace: "/repo", ReadOnly: true,
			DoneValue: "done",
		},
		store.StepReturn{ID: step, Result: result},
		store.StepEnd{ID: step, End: "done", Status: "done", Reason: "returned"},
	)
	if err := fx.store.AddDesignStep(t.Context(), store.DesignStep{
		Step: step, Design: stageDesign, Kind: kind, Subject: "steelman packet", Repository: "/repo",
		Revision: "abc1234", Created: stageResearchNow(),
	}); err != nil {
		t.Fatalf("AddDesignStep: %v", err)
	}
}

// disposeLedger disposes an item of stageDesign through the service of fx, as msg says, from the session header session
// or from no session when session is "". It returns the item Dispose replies with, or the error it returns.
func disposeLedger(
	t *testing.T, fx stageResearchFixture, session string, msg *v1.DisposeRequest,
) (*v1.StageLedgerItem, error) {
	t.Helper()
	msg.Design = stageDesign
	req := connect.NewRequest(msg)
	if session != "" {
		req.Header().Set(protocol.HeaderSession, session)
	}
	reply, err := fx.service.Dispose(t.Context(), req)
	if err != nil {
		return nil, err
	}
	return reply.Msg.GetItem(), nil
}

// ledgerStage returns the stage GetStage reports for stageDesign.
func ledgerStage(t *testing.T, fx stageResearchFixture) *v1.DesignStage {
	t.Helper()
	got, err := fx.service.GetStage(t.Context(), connect.NewRequest(&v1.GetStageRequest{Design: stageDesign}))
	if err != nil {
		t.Fatalf("GetStage: %v", err)
	}
	return got.Msg.GetStage()
}

// discoveredItem returns the ledger item of discovery finding id, which concerns contract, before any disposition.
func discoveredItem(id, contract string) *v1.StageLedgerItem {
	return &v1.StageLedgerItem{
		Id: id, Contract: contract, Origin: "discovery", Impact: "impact of " + id, Evidence: "evidence of " + id,
		Change: "change " + id, Disposition: store.DispositionOpen,
	}
}

// requireLedgerRefusal fails the test unless err is a refusal with code whose text contains message.
func requireLedgerRefusal(t *testing.T, err error, code connect.Code, message string) {
	t.Helper()
	if err == nil {
		t.Fatalf("no error, want code %v naming %q", code, message)
	}
	if got := connect.CodeOf(err); got != code {
		t.Fatalf("code = %v, want %v (err %v)", got, code, err)
	}
	if !strings.Contains(err.Error(), message) {
		t.Fatalf("error %v does not name %q", err, message)
	}
}

// requireNoStoredDispositions fails the test unless the store holds no disposition for stageDesign.
func requireNoStoredDispositions(t *testing.T, fx stageResearchFixture) {
	t.Helper()
	stored, err := fx.store.DesignDispositions(t.Context(), stageDesign)
	if err != nil {
		t.Fatalf("DesignDispositions: %v", err)
	}
	if len(stored) != 0 {
		t.Fatalf("stored dispositions = %+v, want none", stored)
	}
}

func TestStageLedgerStoresEachDispositionWithItsMaker(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedQuestionSession(t, fx)
	seedLedgerStep(t, fx, ledgerDiscovery, store.StepDiscovery, ledgerDiscoveryReturn(t))

	adopted := discoveredItem("S1", "R1")
	adopted.Disposition, adopted.Maker = store.DispositionAdopted, &v1.StageMaker{Kind: store.StageMakerOwnerServe}
	rejected := discoveredItem("S2", "R2")
	rejected.Disposition, rejected.Reason = store.DispositionRejected, "outside the owner's words"
	rejected.Maker = &v1.StageMaker{Kind: store.StageMakerOwnerConversation, Quote: "keep R2 out"}
	deferred := discoveredItem("S3", "P1")
	deferred.Disposition, deferred.Boundary = store.DispositionDeferred, "after the v2 release"
	deferred.Maker = &v1.StageMaker{
		Kind: store.StageMakerConversation, Reason: "the owner left the call to me", Delegation: "decide the boundary",
	}
	open := discoveredItem("S4", "D1")
	open.Maker = &v1.StageMaker{Kind: store.StageMakerOwnerServe}

	steps := []struct {
		session string
		msg     *v1.DisposeRequest
		want    *v1.StageLedgerItem
	}{
		{"", &v1.DisposeRequest{
			Item: "S1", Disposition: store.DispositionAdopted, Maker: ownerServe(),
		}, adopted},
		{questionSession, &v1.DisposeRequest{
			Item: "S2", Disposition: store.DispositionRejected, Reason: "outside the owner's words",
			Maker: &v1.StageMaker{Kind: store.StageMakerOwnerConversation, Quote: "keep R2 out"},
		}, rejected},
		{questionSession, &v1.DisposeRequest{
			Item: "S3", Disposition: store.DispositionDeferred, Boundary: "after the v2 release",
			Maker: &v1.StageMaker{
				Kind: store.StageMakerConversation, Reason: "the owner left the call to me",
				Delegation: "decide the boundary",
			},
		}, deferred},
		{"", &v1.DisposeRequest{
			Item: "S4", Disposition: store.DispositionOpen, Maker: ownerServe(),
		}, open},
	}
	for _, step := range steps {
		got, err := disposeLedger(t, fx, step.session, step.msg)
		if err != nil {
			t.Fatalf("Dispose of %s: %v", step.msg.GetItem(), err)
		}
		if !proto.Equal(got, step.want) {
			t.Fatalf("Dispose of %s = %v, want %v", step.msg.GetItem(), got, step.want)
		}
	}

	stage := ledgerStage(t, fx)
	want := []*v1.StageLedgerItem{adopted, rejected, deferred, open}
	if len(stage.GetLedger()) != len(want) {
		t.Fatalf("ledger = %v, want %d items", stage.GetLedger(), len(want))
	}
	for i := range want {
		if !proto.Equal(stage.GetLedger()[i], want[i]) {
			t.Fatalf("ledger item %d = %v, want %v", i, stage.GetLedger()[i], want[i])
		}
	}
	if stage.GetFrozen() {
		t.Fatal("ledger is frozen before any closure step or skip")
	}

	stored, err := fx.store.DesignDispositions(t.Context(), stageDesign)
	if err != nil {
		t.Fatalf("DesignDispositions: %v", err)
	}
	wantStored := []store.DesignDisposition{
		{Design: stageDesign, Seq: 1, Item: "S1", Disposition: store.DispositionAdopted,
			Maker: store.StageMaker{Kind: store.StageMakerOwnerServe}, At: stageResearchNow()},
		{Design: stageDesign, Seq: 2, Item: "S2", Disposition: store.DispositionRejected,
			Reason: "outside the owner's words", Maker: store.StageMaker{
				Kind: store.StageMakerOwnerConversation, Quote: "keep R2 out"},
			Session: questionSession, At: stageResearchNow()},
		{Design: stageDesign, Seq: 3, Item: "S3", Disposition: store.DispositionDeferred,
			Boundary: "after the v2 release", Maker: store.StageMaker{
				Kind: store.StageMakerConversation, Reason: "the owner left the call to me",
				Delegation: "decide the boundary"},
			Session: questionSession, At: stageResearchNow()},
		{Design: stageDesign, Seq: 4, Item: "S4", Disposition: store.DispositionOpen,
			Maker: store.StageMaker{Kind: store.StageMakerOwnerServe}, At: stageResearchNow()},
	}
	if !reflect.DeepEqual(stored, wantStored) {
		t.Fatalf("stored dispositions = %+v, want %+v", stored, wantStored)
	}
}

func TestStageLedgerChangesDispositionUntilClosure(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedLedgerStep(t, fx, ledgerDiscovery, store.StepDiscovery, ledgerDiscoveryReturn(t))

	for _, disposition := range []struct {
		value, reason string
	}{
		{store.DispositionAdopted, ""},
		{store.DispositionOpen, ""},
		{store.DispositionRejected, "not needed"},
	} {
		if _, err := disposeLedger(t, fx, "", &v1.DisposeRequest{
			Item: "S1", Disposition: disposition.value, Reason: disposition.reason, Maker: ownerServe(),
		}); err != nil {
			t.Fatalf("Dispose S1 %s: %v", disposition.value, err)
		}
	}

	stage := ledgerStage(t, fx)
	if got := stage.GetLedger()[0]; got.GetDisposition() != store.DispositionRejected || got.GetReason() != "not needed" {
		t.Fatalf("ledger item S1 = %v, want rejected for not needed", got)
	}
	if stage.GetFrozen() {
		t.Fatal("ledger is frozen before any closure step or skip")
	}
	stored, err := fx.store.DesignDispositions(t.Context(), stageDesign)
	if err != nil {
		t.Fatalf("DesignDispositions: %v", err)
	}
	var sequence []string
	for _, row := range stored {
		sequence = append(sequence, row.Item+":"+row.Disposition)
	}
	if want := []string{"S1:adopted", "S1:open", "S1:rejected"}; !reflect.DeepEqual(sequence, want) {
		t.Fatalf("stored dispositions = %v, want %v, oldest first and none replaced", sequence, want)
	}
}

func TestStageLedgerRefusesDispositionsOnceClosureStepExists(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedLedgerStep(t, fx, ledgerDiscovery, store.StepDiscovery, ledgerDiscoveryReturn(t))
	seedLedgerStep(t, fx, ledgerClosure, store.StepClosure, ledgerClosureReturn(t,
		[]map[string]string{ledgerEntry("S1", "satisfied", "R1 states the limit")}, nil))

	for _, msg := range []*v1.DisposeRequest{
		{Item: "S2", Disposition: store.DispositionRejected, Reason: "outside the owner's words", Maker: ownerServe()},
		{Item: "S3", Disposition: store.DispositionDeferred, Boundary: "after the v2 release", Maker: ownerServe()},
	} {
		_, err := disposeLedger(t, fx, "", msg)
		requireLedgerRefusal(t, err, connect.CodeFailedPrecondition, "refused after closure")
	}
	if !ledgerStage(t, fx).GetFrozen() {
		t.Fatal("ledger is not frozen once a closure step exists")
	}
	requireNoStoredDispositions(t, fx)
}

func TestStageLedgerRefusesDispositionsOnceClosureIsSkipped(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedLedgerStep(t, fx, ledgerDiscovery, store.StepDiscovery, ledgerDiscoveryReturn(t))
	const reason = "the owner accepts the draft as it stands"
	if err := fx.store.RecordClosureSkip(t.Context(), store.ClosureSkip{
		Design: stageDesign, Reason: reason, Maker: store.StageMaker{Kind: store.StageMakerOwnerServe},
		At: stageResearchNow(),
	}); err != nil {
		t.Fatalf("RecordClosureSkip: %v", err)
	}
	skip, err := fx.store.ClosureSkip(t.Context(), stageDesign)
	if err != nil {
		t.Fatalf("ClosureSkip: %v", err)
	}
	if skip.Reason != reason || skip.Maker.Kind != store.StageMakerOwnerServe {
		t.Fatalf("ClosureSkip = %+v, want the recorded reason and maker", skip)
	}

	_, err = disposeLedger(t, fx, "", &v1.DisposeRequest{
		Item: "S1", Disposition: store.DispositionAdopted, Maker: ownerServe(),
	})
	requireLedgerRefusal(t, err, connect.CodeFailedPrecondition, "refused after closure")
	if !ledgerStage(t, fx).GetFrozen() {
		t.Fatal("ledger is not frozen once a closure skip is recorded")
	}
	requireNoStoredDispositions(t, fx)
}

func TestStageLedgerReadsClosureWordsAndConcernsFromClosure(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedLedgerStep(t, fx, ledgerDiscovery, store.StepDiscovery, ledgerDiscoveryReturn(t))
	if _, err := disposeLedger(t, fx, "", &v1.DisposeRequest{
		Item: "S3", Disposition: store.DispositionRejected, Reason: "out of scope", Maker: ownerServe(),
	}); err != nil {
		t.Fatalf("Dispose S3: %v", err)
	}
	seedLedgerStep(t, fx, ledgerClosure, store.StepClosure, ledgerClosureReturn(t,
		[]map[string]string{
			ledgerEntry("S1", "satisfied", "R1 now states the limit"),
			ledgerEntry("S2", "not_satisfied", "R2 is unchanged"),
			ledgerEntry("S4", "satisfied", "D1 is recorded"),
		},
		[]map[string]string{{"id": "C1", "contract": "P1", "text": "the limit now bites the retry path"}},
	))

	s1 := discoveredItem("S1", "R1")
	s1.ClosureWord, s1.ClosureNote = "satisfied", "R1 now states the limit"
	s2 := discoveredItem("S2", "R2")
	s2.ClosureWord, s2.ClosureNote = "not_satisfied", "R2 is unchanged"
	s3 := discoveredItem("S3", "P1")
	s3.Disposition, s3.Reason = store.DispositionRejected, "out of scope"
	s3.Maker = &v1.StageMaker{Kind: store.StageMakerOwnerServe}
	s4 := discoveredItem("S4", "D1")
	s4.ClosureWord, s4.ClosureNote = "satisfied", "D1 is recorded"
	c1 := &v1.StageLedgerItem{
		Id: "C1", Contract: "P1", Origin: "closure", Impact: "the limit now bites the retry path",
		Disposition: store.DispositionOpen,
	}
	stage := ledgerStage(t, fx)
	want := []*v1.StageLedgerItem{s1, s2, s3, s4, c1}
	if len(stage.GetLedger()) != len(want) {
		t.Fatalf("ledger = %v, want %d items", stage.GetLedger(), len(want))
	}
	for i := range want {
		if !proto.Equal(stage.GetLedger()[i], want[i]) {
			t.Fatalf("ledger item %d = %v, want %v", i, stage.GetLedger()[i], want[i])
		}
	}
	if !stage.GetFrozen() {
		t.Fatal("ledger is not frozen once a closure step exists")
	}
}

func TestStageLedgerClosesAnOpenItemAsRevisedAfterClosure(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedQuestionSession(t, fx)
	seedLedgerStep(t, fx, ledgerDiscovery, store.StepDiscovery, ledgerDiscoveryReturn(t))
	seedLedgerStep(t, fx, ledgerClosure, store.StepClosure, ledgerClosureReturn(t,
		[]map[string]string{ledgerEntry("S2", "not_satisfied", "R2 is unchanged")}, nil))

	const reason = "the retry path now bounds R2"
	got, err := disposeLedger(t, fx, questionSession, &v1.DisposeRequest{
		Item: "S2", Disposition: store.DispositionRevisedAfterClosure, Reason: reason,
		Maker: &v1.StageMaker{
			Kind: store.StageMakerConversation, Reason: "the owner left the call to me",
			Delegation: "decide the retry bound",
		},
	})
	if err != nil {
		t.Fatalf("Dispose S2 revised_after_closure: %v", err)
	}
	want := discoveredItem("S2", "R2")
	want.ClosureWord, want.ClosureNote = "not_satisfied", "R2 is unchanged"
	want.Disposition, want.Reason = store.DispositionRevisedAfterClosure, reason
	want.Maker = &v1.StageMaker{
		Kind: store.StageMakerConversation, Reason: "the owner left the call to me",
		Delegation: "decide the retry bound",
	}
	if !proto.Equal(got, want) {
		t.Fatalf("Dispose S2 = %v, want %v", got, want)
	}
	if stage := ledgerStage(t, fx); !proto.Equal(stage.GetLedger()[1], want) {
		t.Fatalf("ledger item S2 = %v, want %v", stage.GetLedger()[1], want)
	}

	_, err = disposeLedger(t, fx, "", &v1.DisposeRequest{
		Item: "S2", Disposition: store.DispositionAcceptedAsRisk, Reason: "again", Maker: ownerServe(),
	})
	requireLedgerRefusal(t, err, connect.CodeFailedPrecondition, "only an open item")
}

func TestStageLedgerClosesAnOpenConcernAsAcceptedAsRisk(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedQuestionSession(t, fx)
	seedLedgerStep(t, fx, ledgerDiscovery, store.StepDiscovery, ledgerDiscoveryReturn(t))
	seedLedgerStep(t, fx, ledgerClosure, store.StepClosure, ledgerClosureReturn(t, nil,
		[]map[string]string{{"id": "C1", "contract": "P1", "text": "the limit now bites the retry path"}},
	))

	got, err := disposeLedger(t, fx, questionSession, &v1.DisposeRequest{
		Item: "C1", Disposition: store.DispositionAcceptedAsRisk, Reason: "the retry path tolerates it",
		Maker: &v1.StageMaker{Kind: store.StageMakerOwnerConversation, Quote: "accept the retry risk"},
	})
	if err != nil {
		t.Fatalf("Dispose C1 accepted_as_risk: %v", err)
	}
	want := &v1.StageLedgerItem{
		Id: "C1", Contract: "P1", Origin: "closure", Impact: "the limit now bites the retry path",
		Disposition: store.DispositionAcceptedAsRisk, Reason: "the retry path tolerates it",
		Maker: &v1.StageMaker{Kind: store.StageMakerOwnerConversation, Quote: "accept the retry risk"},
	}
	if !proto.Equal(got, want) {
		t.Fatalf("Dispose C1 = %v, want %v", got, want)
	}
	if stage := ledgerStage(t, fx); !proto.Equal(stage.GetLedger()[len(stage.GetLedger())-1], want) {
		t.Fatalf("ledger concern C1 = %v, want %v", stage.GetLedger(), want)
	}
}

func TestStageLedgerRefusesAdoptedAndOpenAfterClosure(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedLedgerStep(t, fx, ledgerDiscovery, store.StepDiscovery, ledgerDiscoveryReturn(t))
	seedLedgerStep(t, fx, ledgerClosure, store.StepClosure, ledgerClosureReturn(t,
		[]map[string]string{ledgerEntry("S1", "satisfied", "R1 states the limit")}, nil))

	_, err := disposeLedger(t, fx, "", &v1.DisposeRequest{
		Item: "S1", Disposition: store.DispositionAdopted, Maker: ownerServe(),
	})
	requireLedgerRefusal(t, err, connect.CodeFailedPrecondition, "disposition: adopted is refused after closure")
	_, err = disposeLedger(t, fx, "", &v1.DisposeRequest{
		Item: "S1", Disposition: store.DispositionOpen, Maker: ownerServe(),
	})
	requireLedgerRefusal(t, err, connect.CodeFailedPrecondition, "disposition: open is refused after closure")
	requireNoStoredDispositions(t, fx)
}

func TestStageLedgerRefusesRevisedAndAcceptedBeforeClosure(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedLedgerStep(t, fx, ledgerDiscovery, store.StepDiscovery, ledgerDiscoveryReturn(t))

	for _, disposition := range []string{store.DispositionRevisedAfterClosure, store.DispositionAcceptedAsRisk} {
		_, err := disposeLedger(t, fx, "", &v1.DisposeRequest{
			Item: "S1", Disposition: disposition, Reason: "too early", Maker: ownerServe(),
		})
		requireLedgerRefusal(t, err, connect.CodeFailedPrecondition, "refused before closure")
	}
	requireNoStoredDispositions(t, fx)
}

func TestStageLedgerRequiresReasonsAndBoundaries(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedLedgerStep(t, fx, ledgerDiscovery, store.StepDiscovery, ledgerDiscoveryReturn(t))

	_, err := disposeLedger(t, fx, "", &v1.DisposeRequest{
		Item: "S1", Disposition: store.DispositionRejected, Maker: ownerServe(),
	})
	requireLedgerRefusal(t, err, connect.CodeInvalidArgument, "reason is required")
	_, err = disposeLedger(t, fx, "", &v1.DisposeRequest{
		Item: "S1", Disposition: store.DispositionDeferred, Reason: "later", Maker: ownerServe(),
	})
	requireLedgerRefusal(t, err, connect.CodeInvalidArgument, "boundary is required")

	seedLedgerStep(t, fx, ledgerClosure, store.StepClosure, ledgerClosureReturn(t,
		[]map[string]string{ledgerEntry("S1", "not_satisfied", "R1 is unchanged")}, nil))
	_, err = disposeLedger(t, fx, "", &v1.DisposeRequest{
		Item: "S1", Disposition: store.DispositionRevisedAfterClosure, Maker: ownerServe(),
	})
	requireLedgerRefusal(t, err, connect.CodeInvalidArgument, "reason is required")
	requireNoStoredDispositions(t, fx)
}

func TestStageLedgerRefusesUnknownDesignItemAndMaker(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedQuestionSession(t, fx)
	seedLedgerStep(t, fx, ledgerDiscovery, store.StepDiscovery, ledgerDiscoveryReturn(t))

	_, err := fx.service.Dispose(t.Context(), connect.NewRequest(&v1.DisposeRequest{
		Design: "design-missing", Item: "S1", Disposition: store.DispositionAdopted, Maker: ownerServe(),
	}))
	requireLedgerRefusal(t, err, connect.CodeNotFound, "design-missing")

	_, err = disposeLedger(t, fx, "", &v1.DisposeRequest{
		Item: "S9", Disposition: store.DispositionAdopted, Maker: ownerServe(),
	})
	requireLedgerRefusal(t, err, connect.CodeNotFound, "not an item of design")

	_, err = disposeLedger(t, fx, questionSession, &v1.DisposeRequest{
		Item: "S1", Disposition: store.DispositionAdopted, Maker: ownerServe(),
	})
	requireLedgerRefusal(t, err, connect.CodePermissionDenied, "owner_serve")
	requireNoStoredDispositions(t, fx)
}

func TestStageLedgerCountsNoBlockedDiscoveryReturn(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedLedgerStep(t, fx, ledgerDiscovery, store.StepDiscovery, ledgerReturn(t, map[string]any{
		"status": "blocked", "pass": "discovery", "verdict": "BLOCKED",
		"findings":              []map[string]string{ledgerFinding("S1", "R1")},
		"evidence_and_coverage": "the repository is missing",
	}))

	stage := ledgerStage(t, fx)
	if len(stage.GetLedger()) != 0 || stage.GetFrozen() {
		t.Fatalf("stage ledger = %v frozen %v, want no items and not frozen", stage.GetLedger(), stage.GetFrozen())
	}
}
