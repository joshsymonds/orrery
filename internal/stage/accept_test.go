package stage

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// The contracts the accept tests propose on stageDesign: the contract an accept names, and another contract of the
// design.
const (
	acceptContract = steelmanIDA
	acceptOther    = steelmanIDB
)

// newStageAcceptFixture returns a fixture whose store holds the design stageDesign with the contracts acceptContract and
// acceptOther, and whose roles name a steelman agent, so a steelman pass can start. It also returns the path the store
// is kept at, for the test that writes a row the Store does not write.
func newStageAcceptFixture(t *testing.T) (stageResearchFixture, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	s, err := store.Open(t.Context(), path, storetest.NoSync())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	if err = s.CreateDesign(t.Context(), store.Design{
		ID: stageDesign, Title: "Design one", Conversation: "conversation-one", Created: stageResearchNow(),
	}); err != nil {
		t.Fatalf("CreateDesign: %v", err)
	}
	getenv := stageRoleHome(t, stageSteelmanRoles)
	starter := &fakeStarter{store: s}
	service := NewService(Config{Store: s, Steps: starter, Now: stageResearchNow, Getenv: getenv})
	fx := stageResearchFixture{service: service, store: s, starter: starter, getenv: getenv}
	proposeSteelmanContract(t, fx, steelmanFixtureContract(acceptContract, "Contract A"))
	proposeSteelmanContract(t, fx, steelmanFixtureContract(acceptOther, "Contract B"))
	return fx, path
}

// acceptDiscoveryReturn returns a counted discovery return with the findings given.
func acceptDiscoveryReturn(t *testing.T, findings ...map[string]string) string {
	t.Helper()
	return ledgerReturn(t, map[string]any{
		"status": "done", "pass": "discovery", "verdict": "REVISE",
		"strongest_case": "the draft holds", "strongest_alternative": "a queue",
		"findings": findings, "evidence_and_coverage": "read the repository",
	})
}

// requireAccepted fails the test unless AcceptCheck accepts contract.
func requireAccepted(t *testing.T, fx stageResearchFixture, contract string) {
	t.Helper()
	if err := fx.service.AcceptCheck(t.Context(), contract); err != nil {
		t.Fatalf("AcceptCheck of %s: %v, want accepted", contract, err)
	}
}

// requireAcceptRefusal fails the test unless AcceptCheck refuses contract as a failed precondition whose text contains
// message.
func requireAcceptRefusal(t *testing.T, fx stageResearchFixture, contract, message string) {
	t.Helper()
	requireLedgerRefusal(t, fx.service.AcceptCheck(t.Context(), contract), connect.CodeFailedPrecondition, message)
}

// askAcceptQuestion asks a question on stageDesign that names contract, or none when contract is "", and returns its
// number.
func askAcceptQuestion(t *testing.T, fx stageResearchFixture, contract string) int64 {
	t.Helper()
	reply, err := fx.service.Ask(t.Context(), connect.NewRequest(&v1.AskRequest{
		Design: stageDesign, Contract: contract, Text: questionText, Options: questionOptions(),
		Recommendation: questionRecommendation,
	}))
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	return reply.Msg.GetQuestion().GetId()
}

// answerAcceptQuestion accepts the recommendation of question seq on stageDesign as the owner through serve.
func answerAcceptQuestion(t *testing.T, fx stageResearchFixture, seq int64) {
	t.Helper()
	if _, err := answer(t, fx, "", &v1.StageAnswerRequest{
		Question: seq, Accept: true, Maker: ownerServe(),
	}); err != nil {
		t.Fatalf("Answer question %d: %v", seq, err)
	}
}

// disposeAcceptItem disposes item of the ledger of stageDesign as the owner through serve, as disposition says, with
// reason as its reason.
func disposeAcceptItem(t *testing.T, fx stageResearchFixture, item, disposition, reason string) {
	t.Helper()
	if _, err := disposeLedger(t, fx, "", &v1.DisposeRequest{
		Item: item, Disposition: disposition, Reason: reason, Maker: ownerServe(),
	}); err != nil {
		t.Fatalf("Dispose %s: %v", item, err)
	}
}

// insertPreMigrationContract writes a contract row to the store file at path the way the store held contracts before
// 00106_contract_stage.sql, which names no staged value, so the row reads the column's default 0.
func insertPreMigrationContract(t *testing.T, path, id string) {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open store file: %v", err)
	}
	_, execErr := db.ExecContext(t.Context(),
		`INSERT INTO contracts (id, created) VALUES (?, ?)`, id, stageResearchNow().Unix())
	if err = errors.Join(execErr, db.Close()); err != nil {
		t.Fatalf("insert contract %s before the migration: %v", id, err)
	}
}

func TestStageAcceptRefusesWhileDiscoveryHasNotCounted(t *testing.T) {
	t.Parallel()
	fx, _ := newStageAcceptFixture(t)
	const noDiscovery = "no counted discovery"
	requireAcceptRefusal(t, fx, acceptContract, noDiscovery)

	running, err := steelmanPass(t, fx, stageDesign, &v1.SteelmanRequest{})
	if err != nil {
		t.Fatalf("Steelman: %v", err)
	}
	requireAcceptRefusal(t, fx, acceptContract, noDiscovery)

	blocked := ledgerReturn(t, map[string]any{
		"status": "blocked", "pass": "discovery", "verdict": "BLOCKED",
		"evidence_and_coverage": "the draft has no checks",
	})
	returnSteelmanStep(t, fx, running.GetStep().GetStep(), blocked)
	requireAcceptRefusal(t, fx, acceptContract, noDiscovery)

	second, err := steelmanPass(t, fx, stageDesign, &v1.SteelmanRequest{})
	if err != nil {
		t.Fatalf("Steelman after a BLOCKED pass: %v", err)
	}
	returnSteelmanStep(t, fx, second.GetStep().GetStep(), acceptDiscoveryReturn(t))
	requireAccepted(t, fx, acceptContract)
	requireAccepted(t, fx, acceptOther)
}

func TestStageAcceptRefusesWhileFindingNamingContractIsOpen(t *testing.T) {
	t.Parallel()
	fx, _ := newStageAcceptFixture(t)
	seedLedgerStep(t, fx, ledgerDiscovery, store.StepDiscovery, acceptDiscoveryReturn(t,
		ledgerFinding("S1", acceptContract), ledgerFinding("S2", acceptOther)))

	requireAcceptRefusal(t, fx, acceptContract, "item S1 is open and names contract "+acceptContract)
	disposeAcceptItem(t, fx, "S1", store.DispositionAdopted, "")
	// S2 is still open, but it names the other contract, so it does not refuse this one.
	requireAccepted(t, fx, acceptContract)
	requireAcceptRefusal(t, fx, acceptOther, "item S2 is open and names contract "+acceptOther)
	disposeAcceptItem(t, fx, "S2", store.DispositionAdopted, "")
	requireAccepted(t, fx, acceptOther)
}

func TestStageAcceptRefusesWhileFindingNamingNoContractIsOpen(t *testing.T) {
	t.Parallel()
	fx, _ := newStageAcceptFixture(t)
	seedLedgerStep(t, fx, ledgerDiscovery, store.StepDiscovery, acceptDiscoveryReturn(t,
		ledgerFinding("S1", "")))

	requireAcceptRefusal(t, fx, acceptContract, "item S1 is open and names no contract")
	requireAcceptRefusal(t, fx, acceptOther, "item S1 is open and names no contract")
	disposeAcceptItem(t, fx, "S1", store.DispositionRejected, "the draft does not state it")
	requireAccepted(t, fx, acceptContract)
	requireAccepted(t, fx, acceptOther)
}

func TestStageAcceptRefusesWhileQuestionNamingContractIsUnanswered(t *testing.T) {
	t.Parallel()
	fx, _ := newStageAcceptFixture(t)
	seedLedgerStep(t, fx, ledgerDiscovery, store.StepDiscovery, acceptDiscoveryReturn(t))
	seq := askAcceptQuestion(t, fx, acceptContract)

	requireAcceptRefusal(t, fx, acceptContract,
		fmt.Sprintf("question %d is unanswered and names contract %s", seq, acceptContract))
	// The question names the contract, so the other contract's accept is not refused by it.
	requireAccepted(t, fx, acceptOther)
	answerAcceptQuestion(t, fx, seq)
	requireAccepted(t, fx, acceptContract)
}

func TestStageAcceptRefusesWhileQuestionNamingNoContractIsUnanswered(t *testing.T) {
	t.Parallel()
	fx, _ := newStageAcceptFixture(t)
	seedLedgerStep(t, fx, ledgerDiscovery, store.StepDiscovery, acceptDiscoveryReturn(t))
	seq := askAcceptQuestion(t, fx, "")

	requireAcceptRefusal(t, fx, acceptContract, fmt.Sprintf("question %d is unanswered and names no contract", seq))
	requireAcceptRefusal(t, fx, acceptOther, fmt.Sprintf("question %d is unanswered and names no contract", seq))
	answerAcceptQuestion(t, fx, seq)
	requireAccepted(t, fx, acceptContract)
	requireAccepted(t, fx, acceptOther)
}

func TestStageAcceptRefusesPreMigrationContractByTodaysRule(t *testing.T) {
	t.Parallel()
	fx, path := newStageAcceptFixture(t)
	const old = "contract-before"
	insertPreMigrationContract(t, path, old)
	staged, err := fx.store.ContractStaged(t.Context(), old)
	if err != nil {
		t.Fatalf("ContractStaged: %v", err)
	}
	if staged {
		t.Fatalf("ContractStaged of %s = true, want false for a contract stored before the migration", old)
	}

	// The proposed contract of the same design waits on its discovery, and the old contract needs none.
	requireAcceptRefusal(t, fx, acceptContract, "no counted discovery")
	requireAccepted(t, fx, old)
	if len(fx.starter.starts) != 0 {
		t.Fatalf("starts = %d, want no steelman pass for a contract stored before the migration",
			len(fx.starter.starts))
	}
}

func TestStageAcceptRefusesUnknownContractAsNotFound(t *testing.T) {
	t.Parallel()
	fx, _ := newStageAcceptFixture(t)
	requireLedgerRefusal(t, fx.service.AcceptCheck(t.Context(), "contract-missing"),
		connect.CodeNotFound, "contract-missing")
}
