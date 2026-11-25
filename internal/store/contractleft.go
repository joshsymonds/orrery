package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"google.golang.org/protobuf/encoding/protojson"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// The kinds of a ContractLeft: a design question, a decision, an addition, a refused write, a working copy and a
// release step that the contract's stage left open.
const (
	LeftQuestion = "question"
	LeftDecision = "decision"
	LeftAddition = "addition"
	LeftRefused  = "refused"
	LeftRevising = "revising"
	LeftNotShown = "not_shown"
)

// The fixed phrase each kind of ContractLeft carries. Every item of a kind carries the same phrase.
const (
	leftQuestionText = "design question not yet answered"
	leftDecisionText = "decision not yet answered"
	leftAdditionText = "addition neither kept nor struck"
	leftRefusedText  = "write refused, and no version stored after it"
	leftRevisingText = "working copy not yet proposed"
	leftNotShownText = "release step not yet shown to the owner"
)

// ContractLeft is one item a contract's stage left open, which the owner must settle before the contract is accepted.
// Kind is one of the Left constants. Ref names the item: Q<n> for a design question, D<n> for a decision, A<n> for an
// addition, the field of the refused write, the contract's id for its working copy, and S<n> for a release step.
// Text is the phrase the server gives the kind.
type ContractLeft struct {
	Kind, Ref, Text string
}

// ContractLeft returns what is left before the contract id can be accepted, in the order question, decision, addition,
// refused, revising and not_shown. An accepted or set-aside contract has nothing left. It returns an error that matches
// ErrNoContract when no contract has id.
func (s *Store) ContractLeft(ctx context.Context, id string) ([]ContractLeft, error) {
	tx, err := s.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin contract left read: %w", err)
	}
	left, err := readContractLeft(ctx, sqlcgen.New(tx), id)
	if err != nil {
		return nil, errors.Join(err, tx.Rollback())
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("end contract left read: %w", err)
	}
	return left, nil
}

// ContractLeft returns what is left before the contract id can be accepted as Store.ContractLeft does, read through the
// Reader.
func (r *Reader) ContractLeft(ctx context.Context, id string) ([]ContractLeft, error) {
	var left []ContractLeft
	err := r.readEngine(ctx, func(queries *sqlcgen.Queries) error {
		var readErr error
		left, readErr = readContractLeft(ctx, queries, id)
		return readErr
	})
	return left, err
}

// ContractShown returns the rows of the contract id the owner was shown, oldest version first and then by row. A
// contract with none has none.
func (s *Store) ContractShown(ctx context.Context, id string) ([]ContractShown, error) {
	return readContractShown(ctx, sqlcgen.New(s.read), id)
}

// ContractShown returns the rows of the contract id the owner was shown as Store.ContractShown does, read through the
// Reader.
func (r *Reader) ContractShown(ctx context.Context, id string) ([]ContractShown, error) {
	var marks []ContractShown
	err := r.readEngine(ctx, func(queries *sqlcgen.Queries) error {
		var readErr error
		marks, readErr = readContractShown(ctx, queries, id)
		return readErr
	})
	return marks, err
}

// MarkContractShown stores a contract_shown mark for each of rows, the rows of version the owner was shown, at at. A
// row that version already has a mark for keeps its first mark, and a row named twice is marked once. The contract must
// be proposed, and version must be its newest version. It returns an error that matches ErrNoContract when no contract
// has id, ErrContractState unless the contract is proposed, and ErrContractVersion unless version is the newest. It
// stores nothing on an error.
func (s *Store) MarkContractShown(ctx context.Context, id string, version int64, rows []string, at int64) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		return markContractShown(ctx, queries, id, version, rows, at)
	})
}

// markContractShown stores the marks MarkContractShown stores, with queries.
func markContractShown(
	ctx context.Context,
	queries *sqlcgen.Queries,
	id string,
	version int64,
	rows []string,
	at int64,
) error {
	listing, err := readContractListing(ctx, queries, id)
	if err != nil {
		return err
	}
	if listing.State != ContractProposed {
		return fmt.Errorf("contract %s is %s: %w", id, listing.State, ErrContractState)
	}
	if version != listing.Version {
		return fmt.Errorf("contract %s has version %d, not %d: %w", id, listing.Version, version, ErrContractVersion)
	}
	marks, err := readContractShown(ctx, queries, id)
	if err != nil {
		return err
	}
	marked := make(map[string]bool)
	for _, mark := range marks {
		if mark.Version == version {
			marked[mark.Row] = true
		}
	}
	for _, row := range rows {
		if marked[row] {
			continue
		}
		marked[row] = true
		if err = queries.InsertContractShown(ctx, sqlcgen.InsertContractShownParams{
			Contract: id, Version: version, Row: row, At: at,
		}); err != nil {
			return fmt.Errorf("insert shown row %s of contract %s: %w", row, id, err)
		}
	}
	return nil
}

// readContractLeft returns what is left before the contract id is accepted, read with queries, as Store.ContractLeft
// returns it. An accepted or set-aside contract has nothing left.
func readContractLeft(ctx context.Context, queries *sqlcgen.Queries, id string) ([]ContractLeft, error) {
	listing, err := readContractListing(ctx, queries, id)
	if err != nil {
		return nil, err
	}
	if listing.State == ContractAccepted || listing.State == ContractSetAside {
		return nil, nil
	}
	row, err := queries.GetContractVersion(ctx, sqlcgen.GetContractVersionParams{
		Contract: id, Version: listing.Version,
	})
	if err != nil {
		return nil, fmt.Errorf("read version %d of contract %s: %w", listing.Version, id, err)
	}
	newest, err := contractVersionOf(row)
	if err != nil {
		return nil, fmt.Errorf("read version %d of contract %s: %w", listing.Version, id, err)
	}
	body, err := decodeEpicContract(newest.Body)
	if err != nil {
		return nil, fmt.Errorf("decode body of version %d of contract %s: %w", listing.Version, id, err)
	}
	questions, err := openQuestionsLeft(ctx, queries, id)
	if err != nil {
		return nil, err
	}
	writes, err := readContractRowWrites(ctx, queries, id)
	if err != nil {
		return nil, err
	}
	refusals, err := readContractRefusals(ctx, queries, id)
	if err != nil {
		return nil, err
	}
	shown, err := readContractShown(ctx, queries, id)
	if err != nil {
		return nil, err
	}
	left := questions
	left = append(left, openDecisionsLeft(newest.Decisions)...)
	left = append(left, undecidedAdditionsLeft(body, newest.Version, writes)...)
	left = append(left, refusedLeft(refusals, newest.Version)...)
	revising, err := revisingLeft(ctx, queries, id)
	if err != nil {
		return nil, err
	}
	left = append(left, revising...)
	if newest.Care == "severe" {
		left = append(left, unshownReleaseStepsLeft(body, newest.Version, shown)...)
	}
	return left, nil
}

// openQuestionsLeft returns the ContractLeft of each question of the design the contract id is in that has no answer
// and names id or no contract, oldest first. A contract in no design is the design of its own id.
func openQuestionsLeft(ctx context.Context, queries *sqlcgen.Queries, id string) ([]ContractLeft, error) {
	design, err := contractDesignOf(ctx, queries, id)
	if err != nil {
		return nil, err
	}
	design = designOrID(design, id)
	rows, err := queries.ListDesignQuestions(ctx, design)
	if err != nil {
		return nil, fmt.Errorf("list questions of design %s: %w", design, err)
	}
	var left []ContractLeft
	for _, row := range rows {
		if row.Answered != 0 || (row.Contract != "" && row.Contract != id) {
			continue
		}
		left = append(left, ContractLeft{Kind: LeftQuestion, Ref: fmt.Sprintf("Q%d", row.Seq), Text: leftQuestionText})
	}
	return left, nil
}

// openDecisionsLeft returns the ContractLeft of each of decisions that has no answer, in order.
func openDecisionsLeft(decisions []ContractDecision) []ContractLeft {
	var left []ContractLeft
	for _, decision := range decisions {
		if decision.Answer == "" {
			left = append(left, ContractLeft{Kind: LeftDecision, Ref: decision.ID, Text: leftDecisionText})
		}
	}
	return left
}

// undecidedAdditionsLeft returns the ContractLeft of each addition of body that has no keep or strike write at version,
// in order. The additions are numbered A1 and up in the order the body holds them.
func undecidedAdditionsLeft(body *enginev1.EpicContract, version int64, writes []ContractRowWrite) []ContractLeft {
	var left []ContractLeft
	for index := range body.GetAdditions() {
		ref := fmt.Sprintf("A%d", index+1)
		if !decidedAt(writes, ref, version) {
			left = append(left, ContractLeft{Kind: LeftAddition, Ref: ref, Text: leftAdditionText})
		}
	}
	return left
}

// decidedAt reports whether writes holds a keep or a strike of row over version.
func decidedAt(writes []ContractRowWrite, row string, version int64) bool {
	for _, write := range writes {
		if write.Row == row && write.Version == version &&
			(write.Action == RowActionKeep || write.Action == RowActionStrike) {
			return true
		}
	}
	return false
}

// refusedLeft returns the ContractLeft of the newest refusal when that refusal would have made a version newer than
// version, the newest version. A refusal of an older version is answered by the version stored since, and leaves
// nothing.
func refusedLeft(refusals []ContractRefusal, version int64) []ContractLeft {
	if len(refusals) == 0 {
		return nil
	}
	newest := refusals[len(refusals)-1]
	if newest.Version <= version {
		return nil
	}
	return []ContractLeft{{Kind: LeftRefused, Ref: newest.Field, Text: leftRefusedText}}
}

// revisingLeft returns the ContractLeft of the working copy of the contract id when it has one, and none otherwise. It
// returns an error that is not ErrNoWorking when the working copy cannot be read.
func revisingLeft(ctx context.Context, queries *sqlcgen.Queries, id string) ([]ContractLeft, error) {
	_, err := readContractWorking(ctx, queries, id)
	if errors.Is(err, ErrNoWorking) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return []ContractLeft{{Kind: LeftRevising, Ref: id, Text: leftRevisingText}}, nil
}

// unshownReleaseStepsLeft returns the ContractLeft of each release step of body that has no shown mark over version, in
// order. The release steps are numbered S1 and up in the order the body holds them.
func unshownReleaseStepsLeft(body *enginev1.EpicContract, version int64, shown []ContractShown) []ContractLeft {
	var left []ContractLeft
	for index := range body.GetReleaseSteps() {
		ref := fmt.Sprintf("S%d", index+1)
		if !markedShownAt(shown, ref, version) {
			left = append(left, ContractLeft{Kind: LeftNotShown, Ref: ref, Text: leftNotShownText})
		}
	}
	return left
}

// markedShownAt reports whether marks holds a mark of row over version.
func markedShownAt(marks []ContractShown, row string, version int64) bool {
	for _, mark := range marks {
		if mark.Row == row && mark.Version == version {
			return true
		}
	}
	return false
}

// readContractShown returns the shown marks of the contract id, read with queries, in the order the store lists them.
func readContractShown(ctx context.Context, queries *sqlcgen.Queries, id string) ([]ContractShown, error) {
	rows, err := queries.ListContractShown(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list shown rows of contract %s: %w", id, err)
	}
	marks := make([]ContractShown, 0, len(rows))
	for _, row := range rows {
		marks = append(marks, ContractShown{Contract: row.Contract, Version: row.Version, Row: row.Row, At: row.At})
	}
	return marks, nil
}

// decodeEpicContract returns the EpicContract that body holds, decoded from protobuf JSON. A body of "" is an empty
// contract.
func decodeEpicContract(body string) (*enginev1.EpicContract, error) {
	var contract enginev1.EpicContract
	if body == "" {
		return &contract, nil
	}
	if err := protojson.Unmarshal([]byte(body), &contract); err != nil {
		return nil, fmt.Errorf("decode body: %w", err)
	}
	return &contract, nil
}
