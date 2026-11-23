package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// ErrNoDraft reports that no contract draft has the id asked for.
var ErrNoDraft = errors.New("no contract draft")

// DraftContract stores draft under its ID, or replaces in place the draft already stored under that ID. A replaced
// draft keeps the created time of the draft first stored, and draft.Created is then ignored. The draft joins the design
// draft.Design names, or no design when it is "". It returns an error that matches ErrContractExists when a contract,
// an epic or a design already holds the ID, and an error that matches ErrNoDesign when draft.Design is not "" and no
// design has it. It stores nothing on an error.
func (s *Store) DraftContract(ctx context.Context, draft ContractDraft) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		held, err := contractIDHeld(ctx, queries, draft.ID)
		if err != nil {
			return err
		}
		if held {
			return fmt.Errorf("contract %s: %w", draft.ID, ErrContractExists)
		}
		if draft.Design != "" {
			if err := requireDesign(ctx, queries, draft.Design); err != nil {
				return err
			}
		}
		decisions, err := encodeList(draft.Decisions)
		if err != nil {
			return fmt.Errorf("encode decisions of draft %s: %w", draft.ID, err)
		}
		if err = queries.UpsertContractDraft(ctx, sqlcgen.UpsertContractDraftParams{
			ID: draft.ID, Design: draft.Design, Repository: draft.Repository, Title: draft.Title, Body: draft.Body,
			Decisions: decisions, Words: draft.Words, Writer: draft.Writer,
			Created: draft.Created.Unix(), Updated: draft.Updated.Unix(),
		}); err != nil {
			return fmt.Errorf("store draft %s: %w", draft.ID, err)
		}
		return nil
	})
}

// ContractDraft returns the draft stored under id. It returns an error that matches ErrNoDraft when no draft has id.
func (s *Store) ContractDraft(ctx context.Context, id string) (ContractDraft, error) {
	tx, err := s.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ContractDraft{}, fmt.Errorf("begin draft read: %w", err)
	}
	draft, err := readContractDraft(ctx, sqlcgen.New(tx), id)
	if err != nil {
		return ContractDraft{}, errors.Join(err, tx.Rollback())
	}
	if err = tx.Commit(); err != nil {
		return ContractDraft{}, fmt.Errorf("end draft read: %w", err)
	}
	return draft, nil
}

// DesignDrafts returns the drafts that join design, ordered by created and then by id. A design with no drafts has
// none.
func (s *Store) DesignDrafts(ctx context.Context, design string) ([]ContractDraft, error) {
	tx, err := s.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin design drafts read: %w", err)
	}
	rows, err := sqlcgen.New(tx).ListDesignDrafts(ctx, design)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("list drafts of design %s: %w", design, err), tx.Rollback())
	}
	drafts := make([]ContractDraft, 0, len(rows))
	for _, row := range rows {
		draft, convertErr := contractDraftOf(row)
		if convertErr != nil {
			return nil, errors.Join(fmt.Errorf("read draft %s: %w", row.ID, convertErr), tx.Rollback())
		}
		drafts = append(drafts, draft)
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("end design drafts read: %w", err)
	}
	return drafts, nil
}

// readContractDraft returns the draft stored under id, read with queries. It returns an error that matches ErrNoDraft
// when no draft has id.
func readContractDraft(ctx context.Context, queries *sqlcgen.Queries, id string) (ContractDraft, error) {
	row, err := queries.GetContractDraft(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return ContractDraft{}, fmt.Errorf("draft %s: %w", id, ErrNoDraft)
	}
	if err != nil {
		return ContractDraft{}, fmt.Errorf("get draft %s: %w", id, err)
	}
	return contractDraftOf(row)
}

// contractDraftOf converts a stored draft row to a ContractDraft, decoding its decisions.
func contractDraftOf(row sqlcgen.ContractDraft) (ContractDraft, error) {
	decisions, err := decodeList[ContractDecision](row.Decisions)
	if err != nil {
		return ContractDraft{}, fmt.Errorf("decode decisions: %w", err)
	}
	return ContractDraft{
		ID: row.ID, Design: row.Design, Repository: row.Repository, Title: row.Title, Body: row.Body,
		Decisions: decisions, Words: row.Words, Writer: row.Writer,
		Created: time.Unix(row.Created, 0), Updated: time.Unix(row.Updated, 0),
	}, nil
}
