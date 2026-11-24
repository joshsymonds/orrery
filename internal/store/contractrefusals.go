package store

import (
	"context"
	"fmt"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// RefuseContract stores refusal as the next refusal of the contract id, numbered as the contract's next refusal. The
// refusal's Contract and Seq are ignored: the store sets them. A refusal may name an id no contract holds yet, as a
// propose the checks refuse does, so no contract is needed. It stores nothing on an error.
func (s *Store) RefuseContract(ctx context.Context, id string, refusal ContractRefusal) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		refusals, err := queries.ListContractRefusals(ctx, id)
		if err != nil {
			return fmt.Errorf("list refusals of contract %s: %w", id, err)
		}
		seq := int64(len(refusals)) + 1
		if err = queries.InsertContractRefusal(ctx, sqlcgen.InsertContractRefusalParams{
			Contract: id, Seq: seq, Version: refusal.Version, Field: refusal.Field, Text: refusal.Text,
			Session: refusal.Session, At: refusal.At,
		}); err != nil {
			return fmt.Errorf("insert refusal %d of contract %s: %w", seq, id, err)
		}
		return nil
	})
}

// ContractRefusals returns the refusals of the contract id, oldest first. A contract with none has none.
func (s *Store) ContractRefusals(ctx context.Context, id string) ([]ContractRefusal, error) {
	return readContractRefusals(ctx, sqlcgen.New(s.read), id)
}

// ContractRefusals returns the refusals of the contract id as Store.ContractRefusals does, read through the Reader.
func (r *Reader) ContractRefusals(ctx context.Context, id string) ([]ContractRefusal, error) {
	var refusals []ContractRefusal
	err := r.readEngine(ctx, func(queries *sqlcgen.Queries) error {
		var readErr error
		refusals, readErr = readContractRefusals(ctx, queries, id)
		return readErr
	})
	return refusals, err
}

// readContractRefusals returns the refusals of the contract id, oldest first, read with queries.
func readContractRefusals(ctx context.Context, queries *sqlcgen.Queries, id string) ([]ContractRefusal, error) {
	rows, err := queries.ListContractRefusals(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list refusals of contract %s: %w", id, err)
	}
	refusals := make([]ContractRefusal, 0, len(rows))
	for _, row := range rows {
		refusals = append(refusals, ContractRefusal{
			Contract: row.Contract, Seq: row.Seq, Version: row.Version, Field: row.Field, Text: row.Text,
			Session: row.Session, At: row.At,
		})
	}
	return refusals, nil
}
