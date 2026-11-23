package stage

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/joshsymonds/orrery/internal/contracttext"
	"github.com/joshsymonds/orrery/internal/enginehost"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// The contracts and drafts the steelman tests propose on stageDesign.
const (
	steelmanIDA = "contract-a"
	steelmanIDB = "contract-b"
)

// stageSteelmanRoles is the role file the Stage steelman tests run their steelman under.
const stageSteelmanRoles = `steelman = "steelman-agent"` + "\n"

// newStageSteelmanFixture returns a fixture whose store holds the design stageDesign and whose roles name a steelman
// agent.
func newStageSteelmanFixture(t *testing.T) stageResearchFixture {
	t.Helper()
	s := openStageStore(t)
	if err := s.CreateDesign(t.Context(), store.Design{
		ID: stageDesign, Title: "Design one", Conversation: "conversation-one", Created: stageResearchNow(),
	}); err != nil {
		t.Fatalf("CreateDesign: %v", err)
	}
	getenv := stageRoleHome(t, stageSteelmanRoles)
	starter := &fakeStarter{store: s}
	service := NewService(Config{Store: s, Steps: starter, Now: stageResearchNow, Getenv: getenv})
	return stageResearchFixture{service: service, store: s, starter: starter, getenv: getenv}
}

// steelmanFixtureContract returns an EpicContract with the id and title given, in the repository /repo.
func steelmanFixtureContract(id, title string) *v1.EpicContract {
	return &v1.EpicContract{
		Id: id, Repository: "/repo", Title: title, Care: "serious", Intent: "intent of " + title,
		Checks: "go test ./internal/...",
	}
}

// steelmanBody returns contract as the protobuf JSON a stored contract version or draft holds.
func steelmanBody(t *testing.T, contract *v1.EpicContract) string {
	t.Helper()
	raw, err := protojson.Marshal(contract)
	if err != nil {
		t.Fatalf("encode contract %s: %v", contract.GetId(), err)
	}
	return string(raw)
}

// proposeSteelmanContract proposes contract on stageDesign at version 1, by the conversation that made the design.
func proposeSteelmanContract(t *testing.T, fx stageResearchFixture, contract *v1.EpicContract) {
	t.Helper()
	if err := fx.store.ProposeContractInDesign(t.Context(), stageDesign, contract.GetId(), store.ContractVersion{
		Version: 1, Repository: contract.GetRepository(), Title: contract.GetTitle(), Care: contract.GetCare(),
		Body: steelmanBody(t, contract), Words: "words", Writer: "conversation-one", At: stageResearchNow().Unix(),
	}); err != nil {
		t.Fatalf("ProposeContractInDesign %s: %v", contract.GetId(), err)
	}
}

// steelmanPass asks the Stage service for a steelman pass on design, with no session header, and returns the reply's
// message, or the error the service returns.
func steelmanPass(
	t *testing.T, fx stageResearchFixture, design string, msg *v1.SteelmanRequest,
) (*v1.SteelmanResponse, error) {
	t.Helper()
	msg.Design = design
	reply, err := fx.service.Steelman(t.Context(), connect.NewRequest(msg))
	if err != nil {
		return nil, err
	}
	return reply.Msg, nil
}

// returnSteelmanStep stores result as the kept return of the steelman step, and ends the step done, as a pass that
// returns does.
func returnSteelmanStep(t *testing.T, fx stageResearchFixture, step, result string) {
	t.Helper()
	applyStageSteps(t, fx.store,
		store.StepReturn{ID: step, Result: result},
		store.StepEnd{ID: step, End: "done", Status: "done", Reason: "returned"},
	)
}

func TestStageSteelmanDiscoveryPromptHoldsContractsAndDrafts(t *testing.T) {
	t.Parallel()
	fx := newStageSteelmanFixture(t)
	ctx := t.Context()
	proposeSteelmanContract(t, fx, steelmanFixtureContract(steelmanIDA, "Contract A"))
	proposeSteelmanContract(t, fx, steelmanFixtureContract(steelmanIDB, "Contract B"))
	revised := steelmanFixtureContract(steelmanIDA, "Contract A, revised")
	if err := fx.store.ReviseContract(ctx, steelmanIDA, store.ContractVersion{
		Version: 2, Repository: "/repo", Title: revised.GetTitle(), Care: "serious", Body: steelmanBody(t, revised),
		Words: "revised words", Writer: "conversation-one", At: stageResearchNow().Unix(),
	}); err != nil {
		t.Fatalf("ReviseContract: %v", err)
	}

	// The draft created first is listed first, though its id sorts after the other draft's id.
	zeta := steelmanFixtureContract("draft-zeta", "Zeta draft")
	alpha := steelmanFixtureContract("draft-alpha", "Alpha draft")
	decisions := []store.ContractDecision{
		{ID: "D1", Question: "Which queue?", Options: []string{"fifo", "lifo"}, Recommendation: "fifo",
			Reason: "ordering", Row: "R1"},
		{ID: "D2", Question: "Which cap?", Options: []string{"10", "20"}, Recommendation: "10", Reason: "load",
			Row: "R2", Answer: "20"},
	}
	for _, draft := range []store.ContractDraft{
		{ID: alpha.GetId(), Design: stageDesign, Repository: "/repo", Title: "Alpha draft",
			Body: steelmanBody(t, alpha), Words: "words", Writer: "conversation-one",
			Created: stageResearchNow().Add(2 * time.Minute)},
		{ID: zeta.GetId(), Design: stageDesign, Repository: "/repo", Title: "Zeta draft",
			Body: steelmanBody(t, zeta), Decisions: decisions, Words: "words", Writer: "conversation-one",
			Created: stageResearchNow().Add(time.Minute)},
	} {
		if err := fx.store.DraftContract(ctx, draft); err != nil {
			t.Fatalf("DraftContract %s: %v", draft.ID, err)
		}
	}

	if _, err := steelmanPass(t, fx, stageDesign, &v1.SteelmanRequest{}); err != nil {
		t.Fatalf("Steelman: %v", err)
	}
	// The open decision is asked; the answered one is not.
	open := []*v1.ContractDecision{{
		Id: "D1", Question: "Which queue?", Options: []string{"fifo", "lifo"}, Recommendation: "fifo",
		Reason: "ordering", Row: "R1",
	}}
	packet := strings.Join([]string{
		contracttext.Render(revised, nil),
		contracttext.Render(steelmanFixtureContract(steelmanIDB, "Contract B"), nil),
		contracttext.Render(zeta, open),
		contracttext.Render(alpha, nil),
	}, "\n")
	want := steelmanContract + "Pass: discovery\n\n" + packet
	wantStart := enginehost.StepStart{
		Agent: "steelman-agent", Prompt: want, Schema: steelmanSchema, Workspace: "/repo", ReadOnly: true,
		DoneValue: "done",
	}
	if len(fx.starter.starts) != 1 || !reflect.DeepEqual(fx.starter.starts[0], wantStart) {
		t.Fatalf("starts = %+v, want one discovery start with the packet", fx.starter.starts)
	}
}

func TestStageSteelmanContractOfOneIsItsOwnPacket(t *testing.T) {
	t.Parallel()
	fx := newStageSteelmanFixture(t)
	lone := steelmanFixtureContract("contract-one", "Contract one")
	if err := fx.store.ProposeContract(t.Context(), lone.GetId(), store.ContractVersion{
		Version: 1, Repository: lone.GetRepository(), Title: lone.GetTitle(), Care: lone.GetCare(),
		Body: steelmanBody(t, lone), Words: "words", Writer: "conversation-one", At: stageResearchNow().Unix(),
	}); err != nil {
		t.Fatalf("ProposeContract: %v", err)
	}

	if _, err := steelmanPass(t, fx, lone.GetId(), &v1.SteelmanRequest{}); err != nil {
		t.Fatalf("Steelman on a contract of one: %v", err)
	}
	want := steelmanContract + "Pass: discovery\n\n" + contracttext.Render(lone, nil)
	if len(fx.starter.starts) != 1 || fx.starter.starts[0].Prompt != want {
		t.Fatalf("starts = %+v, want one discovery start whose prompt is the contract of one's packet", fx.starter.starts)
	}
}

func TestStageSteelmanReturnedDiscoveryCounts(t *testing.T) {
	t.Parallel()
	fx := newStageSteelmanFixture(t)
	proposeSteelmanContract(t, fx, steelmanFixtureContract(steelmanIDA, "Contract A"))
	reply, err := steelmanPass(t, fx, stageDesign, &v1.SteelmanRequest{})
	if err != nil {
		t.Fatalf("Steelman: %v", err)
	}
	if reply.GetStep().GetKind() != store.StepDiscovery || reply.GetPass() {
		t.Fatalf("reply = %v, want a discovery step that has not counted before it returns", reply)
	}

	returnSteelmanStep(t, fx, reply.GetStep().GetStep(), ledgerDiscoveryReturn(t))
	stage := ledgerStage(t, fx)
	steps := stage.GetSteps()
	if len(steps) != 1 || steps[0].GetVerdict() != "REVISE" || !steps[0].GetCounted() {
		t.Fatalf("steps = %v, want one discovery step with verdict REVISE that counts", steps)
	}
	if !stage.GetDiscoveryCounted() || stage.GetClosureCounted() || stage.GetClosureSkipped() {
		t.Fatalf("stage = %v, want only the discovery counted", stage)
	}
}

func TestStageSteelmanBlockedAndReturnlessPassesDoNotCount(t *testing.T) {
	t.Parallel()
	fx := newStageSteelmanFixture(t)
	proposeSteelmanContract(t, fx, steelmanFixtureContract(steelmanIDA, "Contract A"))
	blocked := ledgerReturn(t, map[string]any{
		"status": "blocked", "pass": "discovery", "verdict": "BLOCKED",
		"evidence_and_coverage": "the draft has no checks",
	})
	first, err := steelmanPass(t, fx, stageDesign, &v1.SteelmanRequest{})
	if err != nil {
		t.Fatalf("Steelman: %v", err)
	}
	returnSteelmanStep(t, fx, first.GetStep().GetStep(), blocked)

	// The second pass ends with no return at all.
	second, err := steelmanPass(t, fx, stageDesign, &v1.SteelmanRequest{})
	if err != nil {
		t.Fatalf("Steelman after a BLOCKED pass: %v", err)
	}
	applyStageSteps(t, fx.store,
		store.StepEnd{ID: second.GetStep().GetStep(), End: "failed", Status: "failed", Reason: "exited"})

	stage := ledgerStage(t, fx)
	steps := stage.GetSteps()
	if len(steps) != 2 {
		t.Fatalf("steps = %v, want both passes listed", steps)
	}
	if steps[0].GetVerdict() != "BLOCKED" || steps[0].GetCounted() || steps[0].GetReport() != blocked {
		t.Fatalf("BLOCKED step = %v, want verdict BLOCKED, not counted, with its return kept", steps[0])
	}
	if steps[1].GetStatus() != "failed" || steps[1].GetVerdict() != "" || steps[1].GetCounted() ||
		steps[1].GetReport() != "" {
		t.Fatalf("returnless step = %v, want status failed, no verdict, not counted", steps[1])
	}
	if stage.GetDiscoveryCounted() {
		t.Fatal("discovery counted after only a BLOCKED and a returnless pass, want none")
	}
	if len(fx.starter.starts) != 2 {
		t.Fatalf("starts = %d, want a second discovery after the BLOCKED pass", len(fx.starter.starts))
	}
}

func TestStageSteelmanRefusesPassWhileAStepRuns(t *testing.T) {
	t.Parallel()
	fx := newStageSteelmanFixture(t)
	proposeSteelmanContract(t, fx, steelmanFixtureContract(steelmanIDA, "Contract A"))
	if _, err := steelmanPass(t, fx, stageDesign, &v1.SteelmanRequest{}); err != nil {
		t.Fatalf("Steelman: %v", err)
	}
	_, err := steelmanPass(t, fx, stageDesign, &v1.SteelmanRequest{})
	requireLedgerRefusal(t, err, connect.CodeFailedPrecondition, "still running")
	if len(fx.starter.starts) != 1 {
		t.Fatalf("starts = %d, want none while the discovery runs", len(fx.starter.starts))
	}
}

func TestStageSteelmanClosurePromptCarriesLedgerAndDelta(t *testing.T) {
	t.Parallel()
	fx := newStageSteelmanFixture(t)
	contract := steelmanFixtureContract(steelmanIDA, "Contract A")
	proposeSteelmanContract(t, fx, contract)
	seedLedgerStep(t, fx, ledgerDiscovery, store.StepDiscovery, ledgerDiscoveryReturn(t))
	for _, msg := range []*v1.DisposeRequest{
		{Item: "S1", Disposition: store.DispositionAdopted, Maker: &v1.StageMaker{Kind: store.StageMakerOwnerServe}},
		{Item: "S2", Disposition: store.DispositionRejected, Reason: "out of scope",
			Maker: &v1.StageMaker{Kind: store.StageMakerOwnerServe}},
		{Item: "S3", Disposition: store.DispositionDeferred, Boundary: "phase two",
			Maker: &v1.StageMaker{Kind: store.StageMakerOwnerServe}},
	} {
		if _, err := disposeLedger(t, fx, "", msg); err != nil {
			t.Fatalf("Dispose %s: %v", msg.GetItem(), err)
		}
	}

	const delta = "R1 now requires the queue capture; S2 is rejected in the contract."
	reply, err := steelmanPass(t, fx, stageDesign, &v1.SteelmanRequest{Delta: delta})
	if err != nil {
		t.Fatalf("Steelman closure: %v", err)
	}
	if reply.GetStep().GetKind() != store.StepClosure || reply.GetStep().GetDelta() != delta {
		t.Fatalf("closure step = %v, want a closure step carrying the delta", reply.GetStep())
	}
	if len(fx.starter.starts) != 1 {
		t.Fatalf("starts = %d, want the one closure start", len(fx.starter.starts))
	}
	prompt := fx.starter.starts[0].Prompt
	if !strings.HasPrefix(prompt, steelmanContract+"Pass: closure\n\n"+contracttext.Render(contract, nil)) {
		t.Fatalf("closure prompt does not open with the closure pass and the packet:\n%s", prompt)
	}
	for _, row := range []string{
		"| S1 | R1 | impact of S1 | adopted |  |  |",
		"| S2 | R2 | impact of S2 | rejected | out of scope |  |",
		"| S3 | P1 | impact of S3 | deferred |  | phase two |",
		"| S4 | D1 | impact of S4 | open |  |  |",
	} {
		if !strings.Contains(prompt, row+"\n") {
			t.Fatalf("closure prompt lacks the ledger row %q:\n%s", row, prompt)
		}
	}
	if !strings.Contains(prompt, "\n## The frozen ledger\n") {
		t.Fatalf("closure prompt lacks the frozen ledger heading:\n%s", prompt)
	}
	if !strings.HasSuffix(prompt, "\n## The delta\n"+delta+"\n") {
		t.Fatalf("closure prompt does not end with the delta:\n%s", prompt)
	}
	if !ledgerStage(t, fx).GetFrozen() {
		t.Fatal("ledger not frozen once the closure pass started, want frozen")
	}
}

func TestStageSteelmanCountedThirdPassRefused(t *testing.T) {
	t.Parallel()
	fx := newStageSteelmanFixture(t)
	proposeSteelmanContract(t, fx, steelmanFixtureContract(steelmanIDA, "Contract A"))
	seedLedgerStep(t, fx, ledgerDiscovery, store.StepDiscovery, ledgerDiscoveryReturn(t))
	reply, err := steelmanPass(t, fx, stageDesign, &v1.SteelmanRequest{Delta: "a delta"})
	if err != nil {
		t.Fatalf("Steelman closure: %v", err)
	}
	returnSteelmanStep(t, fx, reply.GetStep().GetStep(), ledgerClosureReturn(t, nil, nil))
	if stage := ledgerStage(t, fx); !stage.GetClosureCounted() {
		t.Fatalf("stage = %v, want the closure counted", stage)
	}

	_, err = steelmanPass(t, fx, stageDesign, &v1.SteelmanRequest{Delta: "another delta"})
	requireLedgerRefusal(t, err, connect.CodeFailedPrecondition, "steelman: budget spent")
	_, err = steelmanPass(t, fx, stageDesign, &v1.SteelmanRequest{
		SkipClosure: true, Reason: "a rename", Maker: &v1.StageMaker{Kind: store.StageMakerOwnerServe},
	})
	requireLedgerRefusal(t, err, connect.CodeFailedPrecondition, "steelman: budget spent")
	if len(fx.starter.starts) != 1 {
		t.Fatalf("starts = %d, want the closure only; a refused pass starts nothing", len(fx.starter.starts))
	}
}

func TestStageSteelmanSkipClosureRecordsSkipAfterCountedDiscovery(t *testing.T) {
	t.Parallel()
	fx := newStageSteelmanFixture(t)
	proposeSteelmanContract(t, fx, steelmanFixtureContract(steelmanIDA, "Contract A"))
	skip := &v1.SteelmanRequest{
		SkipClosure: true, Reason: "a rename", Maker: &v1.StageMaker{Kind: store.StageMakerOwnerServe},
	}
	_, err := steelmanPass(t, fx, stageDesign, skip)
	requireLedgerRefusal(t, err, connect.CodeFailedPrecondition, "counted discovery")

	seedLedgerStep(t, fx, ledgerDiscovery, store.StepDiscovery, ledgerDiscoveryReturn(t))
	reply, err := steelmanPass(t, fx, stageDesign, skip)
	if err != nil {
		t.Fatalf("Steelman skip: %v", err)
	}
	if reply.GetStep() != nil || len(fx.starter.starts) != 0 {
		t.Fatalf("skip reply = %v with %d starts, want no step and no start", reply, len(fx.starter.starts))
	}
	if stage := ledgerStage(t, fx); !stage.GetClosureSkipped() || !stage.GetFrozen() {
		t.Fatalf("stage = %v, want the closure recorded skipped and the ledger frozen", stage)
	}
	_, err = steelmanPass(t, fx, stageDesign, &v1.SteelmanRequest{Delta: "a delta"})
	requireLedgerRefusal(t, err, connect.CodeFailedPrecondition, "steelman: budget spent")
}

func TestStageSteelmanRefusesBlankDeltaAndUnknownOrEmptyDesigns(t *testing.T) {
	t.Parallel()
	fx := newStageSteelmanFixture(t)
	proposeSteelmanContract(t, fx, steelmanFixtureContract(steelmanIDA, "Contract A"))
	seedLedgerStep(t, fx, ledgerDiscovery, store.StepDiscovery, ledgerDiscoveryReturn(t))
	_, err := steelmanPass(t, fx, stageDesign, &v1.SteelmanRequest{})
	requireLedgerRefusal(t, err, connect.CodeInvalidArgument, "delta is required")

	_, err = steelmanPass(t, fx, "design-missing", &v1.SteelmanRequest{})
	requireLedgerRefusal(t, err, connect.CodeNotFound, "design-missing")

	if err = fx.store.CreateDesign(t.Context(), store.Design{
		ID: "design-empty", Title: "Empty", Conversation: "conversation-one", Created: stageResearchNow(),
	}); err != nil {
		t.Fatalf("CreateDesign: %v", err)
	}
	_, err = steelmanPass(t, fx, "design-empty", &v1.SteelmanRequest{})
	requireLedgerRefusal(t, err, connect.CodeFailedPrecondition, "holds no contract or draft")
	if len(fx.starter.starts) != 0 {
		t.Fatalf("starts = %d, want none for the refused passes", len(fx.starter.starts))
	}
}
