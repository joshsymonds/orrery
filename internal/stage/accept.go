package stage

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/store"
)

// AcceptCheck returns the refusal of accepting the contract, or nil when the accept may go on. A contract the contract
// stage did not propose reads staged 0, as every contract the migration found does, and is checked by no rule here. Any
// other contract's design must have a counted discovery, and nothing that names the contract may be open: no ledger
// item whose contract is the contract or empty may have disposition open, and no question whose contract is the
// contract or empty may be unanswered. Findings and questions naming another contract of the design do not refuse it.
// A refusal is a failed precondition that names its reason, a contract no one holds is not found, and a failed read is
// internal.
func (s *Service) AcceptCheck(ctx context.Context, contract string) error {
	staged, err := s.cfg.Store.ContractStaged(ctx, contract)
	switch {
	case errors.Is(err, store.ErrNoContract):
		return errors.Join(connect.NewError(connect.CodeNotFound, err))
	case err != nil:
		return internalError(err)
	case !staged:
		return nil
	}
	design, err := s.designOf(ctx, contract)
	if err != nil {
		return err
	}
	state, err := s.passesOf(ctx, design)
	if err != nil {
		return internalError(err)
	}
	if !state.discoveryCounted {
		return refuse(connect.CodeFailedPrecondition, fmt.Sprintf(
			"accept: design %s has no counted discovery; its steelman discovery has not returned, or returned BLOCKED",
			design))
	}
	ledger, _, err := s.ledgerOf(ctx, design)
	if err != nil {
		return internalError(err)
	}
	for _, item := range ledger {
		if item.Disposition == store.DispositionOpen && namesContract(item.Contract, contract) {
			return refuse(connect.CodeFailedPrecondition, fmt.Sprintf(
				"accept: item %s is open and names %s", item.ID, contractNameOf(item.Contract)))
		}
	}
	questions, err := s.cfg.Store.DesignQuestions(ctx, design)
	if err != nil {
		return internalError(fmt.Errorf("list questions of design %s: %w", design, err))
	}
	for _, question := range questions {
		if question.Answered.IsZero() && namesContract(question.Contract, contract) {
			return refuse(connect.CodeFailedPrecondition, fmt.Sprintf(
				"accept: question %d is unanswered and names %s", question.Seq, contractNameOf(question.Contract)))
		}
	}
	return nil
}

// designOf returns the id of the design that holds contract: the design the contract is in, or the contract itself when
// it is in none, since a contract in no design reads as a design of one. The store's listing holds both kinds of design,
// and a contract is in at most one of them.
func (s *Service) designOf(ctx context.Context, contract string) (string, error) {
	designs, err := s.cfg.Store.Designs(ctx)
	if err != nil {
		return "", internalError(fmt.Errorf("list designs: %w", err))
	}
	for _, record := range designs {
		if designHolds(record, contract) {
			return record.ID, nil
		}
	}
	return "", internalError(fmt.Errorf("contract %s: %w", contract, store.ErrNoDesign))
}

// namesContract reports whether an item or question whose contract is named names the contract: it names it when named
// is the contract or empty, since an empty contract names none, which every contract is checked against.
func namesContract(named, contract string) bool {
	return named == "" || named == contract
}

// contractNameOf returns how a refusal names the contract an item or question names: the contract, or none when it
// names none.
func contractNameOf(named string) string {
	if named == "" {
		return "no contract"
	}
	return "contract " + named
}
