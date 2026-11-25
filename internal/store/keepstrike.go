package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// ContractCheck returns nil when the body it is given holds the addition a keep or a strike names, and the error that
// refuses the call otherwise.
type ContractCheck func(current ContractBody) error

// ContractStrike is the edit a strike makes to a contract's working copy. It removes addition and each row of
// dependents from current, and returns the body the removals leave. It runs inside the strike's transaction, so it must
// not call the store, and an error it returns stores nothing.
type ContractStrike func(current ContractBody, addition string, dependents []string) (ContractBody, error)

// ContractStrikeResult is what a strike stores. Strike is the write that struck the addition, and Removed are the
// remove writes of the rows removed with it, in the order they were written.
type ContractStrikeResult struct {
	Strike  ContractRowWrite
	Removed []ContractRowWrite
}

// KeepContractAddition stores a keep of the addition change.Row of change.Contract at the contract's newest version,
// and returns the write as stored and the design its log lines belong to. A keep writes no working copy. The contract
// must be proposed: a draft, or a contract in any other state, is refused with ErrContractState, and a contract no one
// holds with ErrNoContract. check is called with the newest version's body and, when the contract has a working copy,
// with the working copy's body. The keep is refused with the error check returns when neither body holds the addition.
func (s *Store) KeepContractAddition(
	ctx context.Context,
	change ContractRowChange,
	check ContractCheck,
) (ContractRowWrite, string, error) {
	var (
		stored ContractRowWrite
		design string
	)
	err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		var writeErr error
		stored, design, writeErr = keepContractAddition(ctx, queries, change, check)
		return writeErr
	})
	if err != nil {
		return ContractRowWrite{}, "", err
	}
	return stored, design, nil
}

// StrikeContractAddition strikes the addition change.Row of change.Contract, and returns the result as stored and the
// design its log lines belong to. The working copy is made from the newest version when absent, as WriteContractRow
// makes it. Each row whose newest row write is a write that names the addition in depends is removed with the addition:
// the strike stores the addition's strike write, then a remove write for each such row, all over the working copy's
// version. The contract must be proposed, with the same refusals as KeepContractAddition, and a strike that strike
// returns an error for stores nothing.
func (s *Store) StrikeContractAddition(
	ctx context.Context,
	change ContractRowChange,
	strike ContractStrike,
) (ContractStrikeResult, string, error) {
	var (
		result ContractStrikeResult
		design string
	)
	err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		var writeErr error
		result, design, writeErr = strikeContractAddition(ctx, queries, change, strike)
		return writeErr
	})
	if err != nil {
		return ContractStrikeResult{}, "", err
	}
	return result, design, nil
}

// keepContractAddition stores the keep change names, with queries, as KeepContractAddition does.
func keepContractAddition(
	ctx context.Context,
	queries *sqlcgen.Queries,
	change ContractRowChange,
	check ContractCheck,
) (ContractRowWrite, string, error) {
	listing, err := proposedContract(ctx, queries, change.Contract)
	if err != nil {
		return ContractRowWrite{}, "", err
	}
	if refusal := holdsAddition(ctx, queries, listing, change.At, check); refusal != nil {
		return ContractRowWrite{}, "", refusal
	}
	write, err := insertRowWrite(ctx, queries, ContractRowChange{
		Contract: listing.ID, Row: change.Row, Action: RowActionKeep, Words: change.Words, Maker: change.Maker,
		Session: change.Session, At: change.At,
	}, listing.Version)
	if err != nil {
		return ContractRowWrite{}, "", err
	}
	design, err := contractDesignOf(ctx, queries, listing.ID)
	if err != nil {
		return ContractRowWrite{}, "", err
	}
	return write, designOrID(design, listing.ID), nil
}

// holdsAddition returns nil when check accepts the newest version's body of the proposed contract listing, or else when
// check accepts its working copy's body. A contract with no working copy is checked on its newest version alone. It
// returns the newest version's refusal when neither body is accepted.
func holdsAddition(
	ctx context.Context,
	queries *sqlcgen.Queries,
	listing ContractListing,
	at int64,
	check ContractCheck,
) error {
	newest, err := copyNewestVersion(ctx, queries, listing, at)
	if err != nil {
		return err
	}
	refusal := check(ContractBody{Body: newest.Body, Decisions: newest.Decisions})
	if refusal == nil {
		return nil
	}
	working, err := readContractWorking(ctx, queries, listing.ID)
	if errors.Is(err, ErrNoWorking) {
		return refusal
	}
	if err != nil {
		return err
	}
	if check(ContractBody{Body: working.Body, Decisions: working.Decisions}) == nil {
		return nil
	}
	return refusal
}

// strikeContractAddition strikes the addition change names, with queries, as StrikeContractAddition does. It returns
// the result as stored and the design its log lines belong to.
func strikeContractAddition(
	ctx context.Context,
	queries *sqlcgen.Queries,
	change ContractRowChange,
	strike ContractStrike,
) (ContractStrikeResult, string, error) {
	listing, err := proposedContract(ctx, queries, change.Contract)
	if err != nil {
		return ContractStrikeResult{}, "", err
	}
	working, err := readContractWorking(ctx, queries, listing.ID)
	if errors.Is(err, ErrNoWorking) {
		working, err = copyNewestVersion(ctx, queries, listing, change.At)
	}
	if err != nil {
		return ContractStrikeResult{}, "", err
	}
	dependents, err := dependentRows(ctx, queries, listing.ID, change.Row)
	if err != nil {
		return ContractStrikeResult{}, "", err
	}
	next, err := strike(ContractBody{Body: working.Body, Decisions: working.Decisions}, change.Row, dependents)
	if err != nil {
		return ContractStrikeResult{}, "", err
	}
	decisions, err := encodeList(next.Decisions)
	if err != nil {
		return ContractStrikeResult{}, "", fmt.Errorf("encode decisions of working copy of %s: %w", listing.ID, err)
	}
	if err = queries.UpsertContractWorking(ctx, sqlcgen.UpsertContractWorkingParams{
		Contract: listing.ID, Version: working.Version, Body: next.Body, Decisions: decisions, Writer: change.Session,
		Created: working.Created, Updated: change.At,
	}); err != nil {
		return ContractStrikeResult{}, "", fmt.Errorf("store working copy of %s: %w", listing.ID, err)
	}
	strikeWrite, err := insertRowWrite(ctx, queries, ContractRowChange{
		Contract: listing.ID, Row: change.Row, Action: RowActionStrike, Words: change.Words, Maker: change.Maker,
		Session: change.Session, At: change.At,
	}, working.Version)
	if err != nil {
		return ContractStrikeResult{}, "", err
	}
	result := ContractStrikeResult{Strike: strikeWrite}
	for _, row := range dependents {
		removed, removeErr := insertRowWrite(ctx, queries, ContractRowChange{
			Contract: listing.ID, Row: row, Action: RowActionRemove, Depends: change.Row, Words: change.Words,
			Maker: change.Maker, Session: change.Session, At: change.At,
		}, working.Version)
		if removeErr != nil {
			return ContractStrikeResult{}, "", removeErr
		}
		result.Removed = append(result.Removed, removed)
	}
	design, err := contractDesignOf(ctx, queries, listing.ID)
	if err != nil {
		return ContractStrikeResult{}, "", err
	}
	return result, designOrID(design, listing.ID), nil
}

// dependentRows returns the rows whose newest row write over contract is a write that names addition in depends, in the
// order each row was first written. addition itself is left out.
func dependentRows(ctx context.Context, queries *sqlcgen.Queries, contract, addition string) ([]string, error) {
	writes, err := readContractRowWrites(ctx, queries, contract)
	if err != nil {
		return nil, err
	}
	newest := make(map[string]ContractRowWrite)
	var order []string
	for _, write := range writes {
		if _, named := newest[write.Row]; !named {
			order = append(order, write.Row)
		}
		newest[write.Row] = write
	}
	var rows []string
	for _, row := range order {
		write := newest[row]
		if row != addition && write.Action == RowActionWrite && write.Depends == addition {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

// proposedContract returns the listing of the contract id when it is proposed. It returns an error that matches
// ErrContractState when the contract is a draft or is in another state, and one that matches ErrNoContract when no
// contract or draft has id.
func proposedContract(ctx context.Context, queries *sqlcgen.Queries, id string) (ContractListing, error) {
	listing, err := readContractListing(ctx, queries, id)
	if errors.Is(err, ErrNoContract) {
		_, draftErr := readContractDraft(ctx, queries, id)
		if draftErr == nil {
			return ContractListing{}, fmt.Errorf("contract %s is a draft: %w", id, ErrContractState)
		}
		if !errors.Is(draftErr, ErrNoDraft) {
			return ContractListing{}, draftErr
		}
		return ContractListing{}, err
	}
	if err != nil {
		return ContractListing{}, err
	}
	if listing.State != ContractProposed {
		return ContractListing{}, fmt.Errorf("contract %s is %s: %w", id, listing.State, ErrContractState)
	}
	return listing, nil
}
