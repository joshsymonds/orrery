package stage

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// positionalKinds are the letters of the rows that are a position in their list: a removal moves the rows after it down
// one place. The other letters name their rows by id.
const positionalKinds = "ANLS"

// Keep stores the keep of an addition a contract holds, with the maker the request names, and returns the write as
// stored. The contract must be proposed, and the addition, an A row, must be held by its newest version or its working
// copy. The keep is logged in the design's log as kept, with the addition as its id.
func (s *Service) Keep(
	ctx context.Context,
	req *connect.Request[v1.KeepRequest],
) (*connect.Response[v1.KeepResponse], error) {
	contract, addition := req.Msg.GetContract(), req.Msg.GetAddition()
	if err := requireAddition(contract, addition); err != nil {
		return nil, err
	}
	maker, session, err := s.checkMaker(ctx, req.Header(), req.Msg.GetMaker())
	if err != nil {
		return nil, err
	}
	words := req.Msg.GetWords()
	stored, design, err := s.cfg.Store.KeepContractAddition(ctx, store.ContractRowChange{
		Contract: contract, Row: addition, Action: store.RowActionKeep, Words: words, Maker: maker, Session: session,
		At: s.cfg.Now().Unix(),
	}, func(current store.ContractBody) error {
		return requireHeldAddition(current, addition)
	})
	if err != nil {
		return nil, additionWriteError(err, contract)
	}
	err = s.logCall(ctx, design, loggedCall{
		Maker: maker, Session: session, What: "kept", IDs: []string{addition}, Words: words,
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.KeepResponse{Write: stageRowWriteOf(stored)}), nil
}

// Strike stores the striking of an addition a contract holds, with the maker the request names. The addition is removed
// from the contract's working copy, with each row whose newest row write names it in depends, and the rows removed are
// returned with the write. The contract must be proposed. The striking is logged in the design's log as struck, with
// the addition and the removed rows as its ids.
func (s *Service) Strike(
	ctx context.Context,
	req *connect.Request[v1.StrikeRequest],
) (*connect.Response[v1.StrikeResponse], error) {
	contract, addition := req.Msg.GetContract(), req.Msg.GetAddition()
	if err := requireAddition(contract, addition); err != nil {
		return nil, err
	}
	maker, session, err := s.checkMaker(ctx, req.Header(), req.Msg.GetMaker())
	if err != nil {
		return nil, err
	}
	words := req.Msg.GetWords()
	result, design, err := s.cfg.Store.StrikeContractAddition(ctx, store.ContractRowChange{
		Contract: contract, Row: addition, Action: store.RowActionStrike, Words: words, Maker: maker, Session: session,
		At: s.cfg.Now().Unix(),
	}, strikeBody)
	if err != nil {
		return nil, additionWriteError(err, contract)
	}
	removed := make([]string, 0, len(result.Removed))
	for _, write := range result.Removed {
		removed = append(removed, write.Row)
	}
	err = s.logCall(ctx, design, loggedCall{
		Maker: maker, Session: session, What: "struck", IDs: append([]string{addition}, removed...), Words: words,
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.StrikeResponse{
		Write:   stageRowWriteOf(result.Strike),
		Removed: removed,
	}), nil
}

// requireAddition returns nil when a Keep or Strike names a contract and an addition that is an A row's id. A missing
// contract or addition is an invalid argument that names the field, and any other addition an invalid argument that
// names it.
func requireAddition(contract, addition string) error {
	if err := requireField("contract", contract); err != nil {
		return err
	}
	if err := requireField("addition", addition); err != nil {
		return err
	}
	if kind, _, ok := parseRow(addition); !ok || kind != 'A' {
		return refuse(connect.CodeInvalidArgument,
			fmt.Sprintf("addition: %s is not an addition id, an A and its number", addition))
	}
	return nil
}

// requireHeldAddition returns nil when current holds addition, which is held when the additions of the body reach its
// number. A body that does not hold it refuses the call as a row that names the addition. A body that does not decode
// is an error of its own.
func requireHeldAddition(current store.ContractBody, addition string) error {
	contract, err := decodeBody(current.Body)
	if err != nil {
		return err
	}
	_, number, ok := parseRow(addition)
	if !ok || number > len(contract.GetAdditions()) {
		return &rowError{row: addition, reason: "is not an addition the contract holds"}
	}
	return nil
}

// strikeBody removes addition and each of dependents from current, and returns the body the removals leave. The
// addition must be held. The rows are removed in the order compareRemoval gives, so that removing a numbered row leaves
// the positions of the rows still to remove where they were.
func strikeBody(current store.ContractBody, addition string, dependents []string) (store.ContractBody, error) {
	if err := requireHeldAddition(current, addition); err != nil {
		return store.ContractBody{}, err
	}
	rows := append([]string{addition}, dependents...)
	slices.SortStableFunc(rows, compareRemoval)
	for _, row := range rows {
		next, err := applyRow(current, row, "", true)
		if err != nil {
			return store.ContractBody{}, err
		}
		current = next
	}
	return current, nil
}

// compareRemoval orders two rows for removal. Rows are grouped by the letter their id begins with, and within a group a
// numbered row of a list comes before the rows numbered below it, so that the highest number is removed first.
func compareRemoval(a, b string) int {
	kindA, keyA := removalKey(a)
	kindB, keyB := removalKey(b)
	if kindA != kindB {
		return cmp.Compare(kindA, kindB)
	}
	return cmp.Compare(keyA, keyB)
}

// removalKey returns the letter a row's removal is grouped by, and its key within the group: the negated number of a
// positional row, so that the highest number sorts first, and 0 for any other row.
func removalKey(row string) (byte, int) {
	kind, number, ok := parseRow(row)
	if !ok {
		return 0, 0
	}
	if strings.IndexByte(positionalKinds, kind) >= 0 {
		return kind, -number
	}
	return kind, 0
}

// additionWriteError returns the error Keep or Strike answers with for err, which the store returned for contract. A
// refused addition is an invalid argument that names it, a contract that is not proposed is a failed precondition named
// state: not proposed, a contract no one holds is not found, and any other error is internal.
func additionWriteError(err error, contract string) error {
	var refusal *rowError
	switch {
	case errors.As(err, &refusal):
		return refuse(connect.CodeInvalidArgument, refusal.Error())
	case errors.Is(err, store.ErrContractState):
		return refuse(connect.CodeFailedPrecondition, "state: not proposed")
	case errors.Is(err, store.ErrNoContract):
		return errors.Join(connect.NewError(connect.CodeNotFound, err))
	default:
		return internalError(fmt.Errorf("record addition of contract %s: %w", contract, err))
	}
}
