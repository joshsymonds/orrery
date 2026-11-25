package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// ErrNoWorking reports that a contract has no working copy.
var ErrNoWorking = errors.New("no contract working copy")

// ContractBody is the body a contract's draft or working copy holds: Body is the EpicContract in protobuf JSON, and is ""
// for a draft no write has given a body yet, and Decisions are its decisions. Repository and Title are the ones the body
// names, which a draft keeps as its own columns. A working copy keeps neither, so they are unset for it.
type ContractBody struct {
	Body, Repository, Title string
	Decisions               []ContractDecision
}

// ContractEdit changes the body a contract's draft or working copy holds for one row write. It returns the body the write
// leaves, from the body it is given. It runs inside the write's transaction, so it must not call the store, and an error
// it returns stores nothing.
type ContractEdit func(current ContractBody) (ContractBody, error)

// ContractRowChange is one write to a row of a contract. Design is the design a new draft joins, and a draft that exists
// keeps its own. Action is RowActionWrite or RowActionRemove. Maker is the maker the write names, the zero StageMaker for
// none, and Session is the sessions row id of the session that makes the write, "" for the owner. At is unix seconds.
type ContractRowChange struct {
	Contract, Design, Row, Action string
	Depends, Words                string
	Maker                         StageMaker
	Session                       string
	At                            int64
}

// ContractWorkingCopy is a contract's working copy with ChangedRows, the distinct rows its row writes over its version
// named, in the order they were first written.
type ContractWorkingCopy struct {
	ContractWorking
	ChangedRows []string
}

// WriteContractRow stores one write to a row of change.Contract and returns the write as stored, and the design its log
// lines belong to. A contract no one holds is a draft: the write goes into the draft, which is created empty under
// change.Design when no draft holds the id. A proposed contract is written through its working copy: the write goes into
// the working copy, which is made at the first write as a copy of the newest version's body and decisions, over that
// version. A contract in any other state is refused with ErrContractState. The write and its edit are one transaction, so
// a failed edit stores nothing.
//
// The design is the one the draft or the contract is in, and it is the contract's own id for one in no design. It returns
// an error that matches ErrContractState, ErrNoDesign when a new draft names a design no record holds, and
// ErrContractExists when an epic or a design already holds the id of a new draft, or the error edit returns.
func (s *Store) WriteContractRow(
	ctx context.Context,
	change ContractRowChange,
	edit ContractEdit,
) (ContractRowWrite, string, error) {
	var (
		stored ContractRowWrite
		design string
	)
	err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		var writeErr error
		stored, design, writeErr = writeContractRow(ctx, queries, change, edit)
		return writeErr
	})
	if err != nil {
		return ContractRowWrite{}, "", err
	}
	return stored, design, nil
}

// ContractRowWrites returns the row writes of the contract id, oldest first. A contract with none has none.
func (s *Store) ContractRowWrites(ctx context.Context, id string) ([]ContractRowWrite, error) {
	return readContractRowWrites(ctx, sqlcgen.New(s.read), id)
}

// ContractWorkingCopy returns the working copy of the contract id, read in one read transaction. It returns an error that
// matches ErrNoWorking when the contract has none.
func (s *Store) ContractWorkingCopy(ctx context.Context, id string) (ContractWorkingCopy, error) {
	tx, err := s.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ContractWorkingCopy{}, fmt.Errorf("begin working copy read: %w", err)
	}
	working, err := readContractWorkingCopy(ctx, sqlcgen.New(tx), id)
	if err != nil {
		return ContractWorkingCopy{}, errors.Join(err, tx.Rollback())
	}
	if err = tx.Commit(); err != nil {
		return ContractWorkingCopy{}, fmt.Errorf("end working copy read: %w", err)
	}
	return working, nil
}

// ContractWorkingCopy returns the working copy of the contract id as Store.ContractWorkingCopy does, read through the
// Reader.
func (r *Reader) ContractWorkingCopy(ctx context.Context, id string) (ContractWorkingCopy, error) {
	var working ContractWorkingCopy
	err := r.readEngine(ctx, func(queries *sqlcgen.Queries) error {
		var readErr error
		working, readErr = readContractWorkingCopy(ctx, queries, id)
		return readErr
	})
	return working, err
}

// writeContractRow stores the row write change names, with queries, as WriteContractRow does. It returns the write as
// stored and the design its log lines belong to.
func writeContractRow(
	ctx context.Context,
	queries *sqlcgen.Queries,
	change ContractRowChange,
	edit ContractEdit,
) (ContractRowWrite, string, error) {
	listing, err := readContractListing(ctx, queries, change.Contract)
	if errors.Is(err, ErrNoContract) {
		return writeDraftRow(ctx, queries, change, edit)
	}
	if err != nil {
		return ContractRowWrite{}, "", err
	}
	if listing.State != ContractProposed {
		return ContractRowWrite{}, "", fmt.Errorf(
			"contract %s is %s: %w",
			change.Contract,
			listing.State,
			ErrContractState,
		)
	}
	return writeWorkingRow(ctx, queries, listing, change, edit)
}

// writeDraftRow stores the row write change names into the draft of its contract, as WriteContractRow does for a contract
// no one holds. The draft is created empty when no draft holds its id.
func writeDraftRow(
	ctx context.Context,
	queries *sqlcgen.Queries,
	change ContractRowChange,
	edit ContractEdit,
) (ContractRowWrite, string, error) {
	draft, err := readContractDraft(ctx, queries, change.Contract)
	if errors.Is(err, ErrNoDraft) {
		draft, err = newDraft(ctx, queries, change)
	}
	if err != nil {
		return ContractRowWrite{}, "", err
	}
	next, err := edit(ContractBody{Body: draft.Body, Decisions: draft.Decisions})
	if err != nil {
		return ContractRowWrite{}, "", err
	}
	decisions, err := encodeList(next.Decisions)
	if err != nil {
		return ContractRowWrite{}, "", fmt.Errorf("encode decisions of draft %s: %w", change.Contract, err)
	}
	if err = queries.UpsertContractDraft(ctx, sqlcgen.UpsertContractDraftParams{
		ID: change.Contract, Design: draft.Design, Repository: next.Repository, Title: next.Title, Body: next.Body,
		Decisions: decisions, Words: change.Words, Writer: change.Session,
		Created: draft.Created.Unix(), Updated: change.At,
	}); err != nil {
		return ContractRowWrite{}, "", fmt.Errorf("store draft %s: %w", change.Contract, err)
	}
	write, err := insertRowWrite(ctx, queries, change, 0)
	if err != nil {
		return ContractRowWrite{}, "", err
	}
	return write, designOrID(draft.Design, change.Contract), nil
}

// newDraft returns the empty draft change.Contract is created as, under change.Design, when no draft holds its id. It
// returns an error that matches ErrContractExists when an epic or a design holds the id, and ErrNoDesign when
// change.Design is not "" and no design has it.
func newDraft(ctx context.Context, queries *sqlcgen.Queries, change ContractRowChange) (ContractDraft, error) {
	held, err := contractIDHeld(ctx, queries, change.Contract)
	if err != nil {
		return ContractDraft{}, err
	}
	if held {
		return ContractDraft{}, fmt.Errorf("contract %s: %w", change.Contract, ErrContractExists)
	}
	if change.Design != "" {
		err = requireDesign(ctx, queries, change.Design)
		if err != nil {
			return ContractDraft{}, err
		}
	}
	return ContractDraft{ID: change.Contract, Design: change.Design, Created: time.Unix(change.At, 0)}, nil
}

// writeWorkingRow stores the row write change names into the working copy of the proposed contract listing, as
// WriteContractRow does. The working copy is made from the newest version when none is stored.
func writeWorkingRow(
	ctx context.Context,
	queries *sqlcgen.Queries,
	listing ContractListing,
	change ContractRowChange,
	edit ContractEdit,
) (ContractRowWrite, string, error) {
	working, err := readContractWorking(ctx, queries, listing.ID)
	if errors.Is(err, ErrNoWorking) {
		working, err = copyNewestVersion(ctx, queries, listing, change.At)
	}
	if err != nil {
		return ContractRowWrite{}, "", err
	}
	next, err := edit(ContractBody{Body: working.Body, Decisions: working.Decisions})
	if err != nil {
		return ContractRowWrite{}, "", err
	}
	decisions, err := encodeList(next.Decisions)
	if err != nil {
		return ContractRowWrite{}, "", fmt.Errorf("encode decisions of working copy of %s: %w", listing.ID, err)
	}
	if err = queries.UpsertContractWorking(ctx, sqlcgen.UpsertContractWorkingParams{
		Contract: listing.ID, Version: working.Version, Body: next.Body, Decisions: decisions, Writer: change.Session,
		Created: working.Created, Updated: change.At,
	}); err != nil {
		return ContractRowWrite{}, "", fmt.Errorf("store working copy of %s: %w", listing.ID, err)
	}
	write, err := insertRowWrite(ctx, queries, change, working.Version)
	if err != nil {
		return ContractRowWrite{}, "", err
	}
	design, err := contractDesignOf(ctx, queries, listing.ID)
	if err != nil {
		return ContractRowWrite{}, "", err
	}
	return write, designOrID(design, listing.ID), nil
}

// copyNewestVersion returns the working copy a proposed contract's first write makes: the body and decisions of its newest
// version, over that version and created at at. The copy's writer is set by the write that stores it.
func copyNewestVersion(
	ctx context.Context,
	queries *sqlcgen.Queries,
	listing ContractListing,
	at int64,
) (ContractWorking, error) {
	row, err := queries.GetContractVersion(ctx, sqlcgen.GetContractVersionParams{
		Contract: listing.ID, Version: listing.Version,
	})
	if err != nil {
		return ContractWorking{}, fmt.Errorf("read version %d of contract %s: %w", listing.Version, listing.ID, err)
	}
	newest, err := contractVersionOf(row)
	if err != nil {
		return ContractWorking{}, fmt.Errorf("read version %d of contract %s: %w", listing.Version, listing.ID, err)
	}
	return ContractWorking{
		Contract: listing.ID, Version: listing.Version, Body: newest.Body, Decisions: newest.Decisions, Created: at,
		Updated: at,
	}, nil
}

// readContractWorking returns the working copy of the contract id, read with queries. It returns an error that matches
// ErrNoWorking when the contract has none.
func readContractWorking(ctx context.Context, queries *sqlcgen.Queries, id string) (ContractWorking, error) {
	row, err := queries.GetContractWorking(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return ContractWorking{}, fmt.Errorf("working copy of contract %s: %w", id, ErrNoWorking)
	}
	if err != nil {
		return ContractWorking{}, fmt.Errorf("read working copy of contract %s: %w", id, err)
	}
	decisions, err := decodeList[ContractDecision](row.Decisions)
	if err != nil {
		return ContractWorking{}, fmt.Errorf("decode decisions of working copy of contract %s: %w", id, err)
	}
	return ContractWorking{
		Contract: row.Contract, Version: row.Version, Body: row.Body, Decisions: decisions, Writer: row.Writer,
		Created: row.Created, Updated: row.Updated,
	}, nil
}

// readContractWorkingCopy returns the working copy of the contract id with its changed rows, read with queries. The rows
// are those its row writes over its version named.
func readContractWorkingCopy(ctx context.Context, queries *sqlcgen.Queries, id string) (ContractWorkingCopy, error) {
	working, err := readContractWorking(ctx, queries, id)
	if err != nil {
		return ContractWorkingCopy{}, err
	}
	writes, err := readContractRowWrites(ctx, queries, id)
	if err != nil {
		return ContractWorkingCopy{}, err
	}
	result := ContractWorkingCopy{ContractWorking: working}
	named := make(map[string]bool)
	for _, write := range writes {
		if write.Version != working.Version || named[write.Row] {
			continue
		}
		named[write.Row] = true
		result.ChangedRows = append(result.ChangedRows, write.Row)
	}
	return result, nil
}

// insertRowWrite stores change's row write against version, numbered as the contract's next row write, and returns it as
// stored.
func insertRowWrite(
	ctx context.Context,
	queries *sqlcgen.Queries,
	change ContractRowChange,
	version int64,
) (ContractRowWrite, error) {
	writes, err := readContractRowWrites(ctx, queries, change.Contract)
	if err != nil {
		return ContractRowWrite{}, err
	}
	seq := int64(1)
	for _, write := range writes {
		if write.Seq >= seq {
			seq = write.Seq + 1
		}
	}
	write := ContractRowWrite{
		Contract: change.Contract, Seq: seq, Version: version, Row: change.Row, Action: change.Action,
		Depends: change.Depends, Words: change.Words, Maker: change.Maker.Kind, Quote: change.Maker.Quote,
		MakerReason: change.Maker.Reason, Delegation: change.Maker.Delegation, Session: change.Session, At: change.At,
	}
	if err = queries.InsertContractRowWrite(ctx, sqlcgen.InsertContractRowWriteParams{
		Contract: write.Contract, Seq: write.Seq, Version: write.Version, Row: write.Row, Action: write.Action,
		Depends: write.Depends, Words: write.Words, Maker: write.Maker, Quote: write.Quote,
		MakerReason: write.MakerReason, Delegation: write.Delegation, Session: write.Session, At: write.At,
	}); err != nil {
		return ContractRowWrite{}, fmt.Errorf("insert row write %d of contract %s: %w", seq, change.Contract, err)
	}
	return write, nil
}

// readContractRowWrites returns the row writes of the contract id, oldest first, read with queries.
func readContractRowWrites(ctx context.Context, queries *sqlcgen.Queries, id string) ([]ContractRowWrite, error) {
	rows, err := queries.ListContractRowWrites(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list row writes of contract %s: %w", id, err)
	}
	writes := make([]ContractRowWrite, 0, len(rows))
	for _, row := range rows {
		writes = append(writes, ContractRowWrite{
			Contract: row.Contract, Seq: row.Seq, Version: row.Version, Row: row.Row, Action: row.Action,
			Depends: row.Depends, Words: row.Words, Maker: row.Maker, Quote: row.Quote, MakerReason: row.MakerReason,
			Delegation: row.Delegation, Session: row.Session, At: row.At,
		})
	}
	return writes, nil
}

// designOrID returns design, or id when design is "": a contract in no design is a design of one, listed under its own id.
func designOrID(design, id string) string {
	if design == "" {
		return id
	}
	return design
}
