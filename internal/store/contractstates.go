package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// ErrContractState reports that a contract is in a state the change does not apply to.
var ErrContractState = errors.New("contract is in another state")

// ErrNotContractConversation reports that the caller is not the conversation that proposed the contract.
var ErrNotContractConversation = errors.New("caller is not the contract's conversation")

// ErrContractVersion reports that the version a revision names is not the one after the contract's newest version.
var ErrContractVersion = errors.New("contract version moved")

// ErrOpenDecisions reports that a contract has decisions with no answer. Its message ends with the open ids.
var ErrOpenDecisions = errors.New("contract has unanswered decisions")

// ErrContractEpic reports that an epic already holds the id of the contract.
var ErrContractEpic = errors.New("an epic holds the contract's id")

// ContractAccepted is the state of a contract whose decisions are all answered and whose version is frozen.
const ContractAccepted = "accepted"

// ContractSetAside is the state of a contract its conversation or its owner has set aside.
const ContractSetAside = "set_aside"

// ContractChange is a change of a contract's state. Session is the sessions row id of the session that makes it, and
// "" for the owner. Words are what the session said. Base is the commit an accept stores as the contract's base, and
// Maker is who the change is made for, stored with the state; the changes that store no base leave it "", and the
// changes that store no maker leave it the zero StageMaker.
type ContractChange struct {
	Session, Words, Base string
	Maker                StageMaker
	At                   int64
}

// ReviseContract appends revision as the next version of the contract id. It returns an error that matches ErrNoContract
// when no contract has id, ErrContractState unless the contract is proposed, ErrNotContractConversation unless
// revision.Writer is the writer of version 1, and ErrContractVersion unless revision.Version is one after the newest
// version. It appends no state.
func (s *Store) ReviseContract(ctx context.Context, id string, revision ContractVersion) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		listing, err := readContractListing(ctx, queries, id)
		if err != nil {
			return err
		}
		if listing.State != ContractProposed {
			return fmt.Errorf("contract %s is %s: %w", id, listing.State, ErrContractState)
		}
		if revision.Writer != listing.Conversation {
			return fmt.Errorf("contract %s: %w", id, ErrNotContractConversation)
		}
		if revision.Version != listing.Version+1 {
			return fmt.Errorf(
				"contract %s has version %d, not %d: %w",
				id,
				listing.Version,
				revision.Version,
				ErrContractVersion,
			)
		}
		return insertContractVersion(ctx, queries, id, revision)
	})
}

// AcceptContract accepts the contract id at its newest version. It returns an error that matches ErrNoContract when no
// contract has id, ErrContractState unless the contract is proposed, and an error that matches ErrOpenDecisions, ending
// with the open ids joined by ", ", while a decision of the newest version has no answer.
func (s *Store) AcceptContract(ctx context.Context, id string, change ContractChange) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		listing, err := readContractListing(ctx, queries, id)
		if err != nil {
			return err
		}
		if listing.State != ContractProposed {
			return fmt.Errorf("contract %s is %s: %w", id, listing.State, ErrContractState)
		}
		newest, err := queries.GetContractVersion(ctx, sqlcgen.GetContractVersionParams{
			Contract: id, Version: listing.Version,
		})
		if err != nil {
			return fmt.Errorf("read version %d of contract %s: %w", listing.Version, id, err)
		}
		decisions, err := decodeList[ContractDecision](newest.Decisions)
		if err != nil {
			return fmt.Errorf("decode decisions of version %d of contract %s: %w", listing.Version, id, err)
		}
		var open []string
		for _, decision := range decisions {
			if decision.Answer == "" {
				open = append(open, decision.ID)
			}
		}
		if len(open) > 0 {
			return fmt.Errorf("contract %s: %w: %s", id, ErrOpenDecisions, strings.Join(open, ", "))
		}
		return appendContractState(withEpicEvent(ctx), queries, id, ContractAccepted, listing.Version, change)
	})
}

// SetAsideContract sets the contract id aside at its newest version. It returns an error that matches ErrNoContract
// when no contract has id, ErrContractEpic when the contract is accepted and an epic holds its id, and
// ErrContractState for any state other than proposed or accepted.
func (s *Store) SetAsideContract(ctx context.Context, id string, change ContractChange) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		listing, err := readContractListing(ctx, queries, id)
		if err != nil {
			return err
		}
		if listing.State != ContractProposed && listing.State != ContractAccepted {
			return fmt.Errorf("contract %s is %s: %w", id, listing.State, ErrContractState)
		}
		if listing.State == ContractAccepted {
			epics, countErr := queries.CountEpic(ctx, id)
			if countErr != nil {
				return fmt.Errorf("count epic %s: %w", id, countErr)
			}
			if epics > 0 {
				return fmt.Errorf("contract %s: %w", id, ErrContractEpic)
			}
		}
		return appendContractState(withEpicEvent(ctx), queries, id, ContractSetAside, listing.Version, change)
	})
}

// ReopenContract reopens the contract id, set aside, as proposed at its newest version. It returns an error that matches
// ErrNoContract when no contract has id and ErrContractState unless the contract is set aside.
func (s *Store) ReopenContract(ctx context.Context, id string, change ContractChange) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		listing, err := readContractListing(ctx, queries, id)
		if err != nil {
			return err
		}
		if listing.State != ContractSetAside {
			return fmt.Errorf("contract %s is %s: %w", id, listing.State, ErrContractState)
		}
		return appendContractState(withEpicEvent(ctx), queries, id, ContractProposed, listing.Version, change)
	})
}

// readContractListing returns the listing of the contract id, read with queries. It returns an error that matches
// ErrNoContract when no contract has id.
func readContractListing(ctx context.Context, queries *sqlcgen.Queries, id string) (ContractListing, error) {
	row, err := queries.GetContractListing(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return ContractListing{}, fmt.Errorf("contract %s: %w", id, ErrNoContract)
	}
	if err != nil {
		return ContractListing{}, fmt.Errorf("read contract %s: %w", id, err)
	}
	return contractListingOf(row), nil
}

// insertContractVersion appends revision to the contract id as version revision.Version, with its open decision count.
func insertContractVersion(ctx context.Context, queries *sqlcgen.Queries, id string, revision ContractVersion) error {
	decisions, err := encodeList(revision.Decisions)
	if err != nil {
		return fmt.Errorf("encode decisions of contract %s: %w", id, err)
	}
	changedRows, err := encodeList(revision.ChangedRows)
	if err != nil {
		return fmt.Errorf("encode changed rows of contract %s: %w", id, err)
	}
	changedSections, err := encodeList(revision.ChangedSections)
	if err != nil {
		return fmt.Errorf("encode changed sections of contract %s: %w", id, err)
	}
	var open int64
	for _, decision := range revision.Decisions {
		if decision.Answer == "" {
			open++
		}
	}
	if err = queries.InsertContractVersion(ctx, sqlcgen.InsertContractVersionParams{
		Contract:        id,
		Version:         revision.Version,
		Repository:      revision.Repository,
		Title:           revision.Title,
		Care:            revision.Care,
		Body:            revision.Body,
		Decisions:       decisions,
		OpenDecisions:   open,
		Words:           revision.Words,
		Writer:          revision.Writer,
		At:              revision.At,
		ChangedRows:     changedRows,
		ChangedSections: changedSections,
	}); err != nil {
		return fmt.Errorf("insert version %d of contract %s: %w", revision.Version, id, err)
	}
	return nil
}

// appendContractState records state as the next state of the contract id, entered at version by the session of change
// with its words and base at its time. A change that names a maker stores the maker's columns with the state, in the
// same write.
func appendContractState(
	ctx context.Context, queries *sqlcgen.Queries, id, state string, version int64, change ContractChange,
) error {
	count, err := queries.CountContractStates(ctx, id)
	if err != nil {
		return fmt.Errorf("count states of contract %s: %w", id, err)
	}
	seq := count + 1
	if err = queries.InsertContractState(ctx, sqlcgen.InsertContractStateParams{
		Contract: id,
		Seq:      seq,
		State:    state,
		Version:  version,
		Session:  change.Session,
		Words:    change.Words,
		At:       change.At,
		Base:     change.Base,
	}); err != nil {
		return fmt.Errorf("insert state %s of contract %s: %w", state, id, err)
	}
	if change.Maker.Kind == "" {
		return nil
	}
	if err = queries.SetContractStateMaker(ctx, sqlcgen.SetContractStateMakerParams{
		Maker:       change.Maker.Kind,
		Quote:       change.Maker.Quote,
		MakerReason: change.Maker.Reason,
		Delegation:  change.Maker.Delegation,
		Contract:    id,
		Seq:         seq,
	}); err != nil {
		return fmt.Errorf("set maker of state %s of contract %s: %w", state, id, err)
	}
	return nil
}
