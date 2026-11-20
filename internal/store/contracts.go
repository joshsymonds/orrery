package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// ErrNoContract reports that no contract has the id asked for.
var ErrNoContract = errors.New("no contract")

// ErrContractExists reports that a contract or an epic already holds the id a contract was proposed under.
var ErrContractExists = errors.New("contract id already held by a contract or an epic")

// ContractProposed is the state a contract is in when it is first stored.
const ContractProposed = "proposed"

// ContractDecision is one decision of a contract version. Answer is "" while the decision is open.
type ContractDecision struct {
	ID             string   `json:"id"`
	Question       string   `json:"question"`
	Options        []string `json:"options"`
	Recommendation string   `json:"recommendation"`
	Reason         string   `json:"reason"`
	Row            string   `json:"row"`
	Answer         string   `json:"answer"`
}

// ContractVersion is one saved version of a contract. Body is the EpicContract in protobuf JSON. ChangedRows and
// ChangedSections name the rows and sections a revision changed.
type ContractVersion struct {
	Version                      int64
	Repository, Title, Care      string
	Body                         string
	Decisions                    []ContractDecision
	Words, Writer                string
	At                           int64
	ChangedRows, ChangedSections []string
}

// ContractListing is one contract as a listing shows it. Its Repository, Title, Care, Version and OpenDecisions are
// the newest version's, and State is the newest state's. Conversation is the writer of version 1, and Changed is the
// later of the newest version's time and the newest state's time. Created and Changed are unix seconds.
type ContractListing struct {
	ID, Repository, Title, Care, State string
	Version, OpenDecisions             int64
	Conversation                       string
	Created, Changed                   int64
}

// ContractStateChange is one state a contract entered. Session is the sessions row id of the session that made the
// change, and "" for the owner.
type ContractStateChange struct {
	State   string
	Version int64
	Session string
	Words   string
	At      int64
}

// ContractRecord is a contract with its listing, its versions in version order and its states in the order they
// were entered.
type ContractRecord struct {
	ContractListing
	Versions []ContractVersion
	States   []ContractStateChange
}

// ProposeContract stores id as a new contract. proposal is stored as version 1, whatever its Version says, with no
// changed rows or sections, and the contract enters state proposed at version 1 by proposal.Writer with
// proposal.Words at proposal.At. It returns an error that matches ErrContractExists, and stores nothing, when a
// contract or an epic already has id.
func (s *Store) ProposeContract(ctx context.Context, id string, proposal ContractVersion) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		held, err := queries.CountContract(ctx, id)
		if err != nil {
			return fmt.Errorf("count contract %s: %w", id, err)
		}
		epics, err := queries.CountEpic(ctx, id)
		if err != nil {
			return fmt.Errorf("count epic %s: %w", id, err)
		}
		if held > 0 || epics > 0 {
			return fmt.Errorf("contract %s: %w", id, ErrContractExists)
		}
		if err = queries.InsertContract(ctx, sqlcgen.InsertContractParams{ID: id, Created: proposal.At}); err != nil {
			return fmt.Errorf("insert contract %s: %w", id, err)
		}
		proposal.Version, proposal.ChangedRows, proposal.ChangedSections = 1, nil, nil
		if versionErr := insertContractVersion(ctx, queries, id, proposal); versionErr != nil {
			return versionErr
		}
		change := ContractChange{Session: proposal.Writer, Words: proposal.Words, At: proposal.At}
		return appendContractState(ctx, queries, id, ContractProposed, 1, change)
	})
}

// Contract returns the contract with id, its listing with its versions and states. Both are read in one read
// transaction. It returns an error that matches ErrNoContract when no contract has id.
func (s *Store) Contract(ctx context.Context, id string) (ContractRecord, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ContractRecord{}, fmt.Errorf("begin contract read: %w", err)
	}
	record, err := readContract(ctx, sqlcgen.New(tx), id)
	if err != nil {
		return ContractRecord{}, errors.Join(err, tx.Rollback())
	}
	if err = tx.Commit(); err != nil {
		return ContractRecord{}, fmt.Errorf("end contract read: %w", err)
	}
	return record, nil
}

// Contracts returns the listing of every contract, oldest first and ties by id.
func (s *Store) Contracts(ctx context.Context) ([]ContractListing, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin contracts read: %w", err)
	}
	listings, err := readContractListings(ctx, sqlcgen.New(tx))
	if err != nil {
		return nil, errors.Join(err, tx.Rollback())
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("end contracts read: %w", err)
	}
	return listings, nil
}

// Contract returns the contract with id as Store.Contract does, read without writing the store.
func (r *Reader) Contract(ctx context.Context, id string) (ContractRecord, error) {
	var record ContractRecord
	err := r.readEngine(ctx, func(queries *sqlcgen.Queries) error {
		var readErr error
		record, readErr = readContract(ctx, queries, id)
		return readErr
	})
	return record, err
}

// Contracts returns the listing of every contract as Store.Contracts does, read without writing the store.
func (r *Reader) Contracts(ctx context.Context) ([]ContractListing, error) {
	var listings []ContractListing
	err := r.readEngine(ctx, func(queries *sqlcgen.Queries) error {
		var readErr error
		listings, readErr = readContractListings(ctx, queries)
		return readErr
	})
	return listings, err
}

// readContract returns the contract with id, its listing, versions and states, read with queries.
func readContract(ctx context.Context, queries *sqlcgen.Queries, id string) (ContractRecord, error) {
	listing, err := readContractListing(ctx, queries, id)
	if err != nil {
		return ContractRecord{}, err
	}
	versions, err := queries.ListContractVersions(ctx, id)
	if err != nil {
		return ContractRecord{}, fmt.Errorf("list versions of contract %s: %w", id, err)
	}
	states, err := queries.ListContractStates(ctx, id)
	if err != nil {
		return ContractRecord{}, fmt.Errorf("list states of contract %s: %w", id, err)
	}
	record := ContractRecord{ContractListing: listing}
	for _, row := range versions {
		version, convertErr := contractVersionOf(row)
		if convertErr != nil {
			return ContractRecord{}, fmt.Errorf("read version %d of contract %s: %w", row.Version, id, convertErr)
		}
		record.Versions = append(record.Versions, version)
	}
	for _, row := range states {
		record.States = append(record.States, ContractStateChange{
			State: row.State, Version: row.Version, Session: row.Session, Words: row.Words, At: row.At,
		})
	}
	return record, nil
}

// readContractListings returns the listing of every contract as Store.Contracts does, read with queries.
func readContractListings(ctx context.Context, queries *sqlcgen.Queries) ([]ContractListing, error) {
	rows, err := queries.ListContractListings(ctx)
	if err != nil {
		return nil, fmt.Errorf("list contracts: %w", err)
	}
	listings := make([]ContractListing, 0, len(rows))
	for _, row := range rows {
		listings = append(listings, ContractListing{
			ID: row.ID, Repository: row.Repository, Title: row.Title, Care: row.Care, State: row.State,
			Version: row.Version, OpenDecisions: row.OpenDecisions, Conversation: row.Conversation,
			Created: row.Created, Changed: row.Changed,
		})
	}
	return listings, nil
}

// contractListingOf converts the row of GetContractListing to a ContractListing.
func contractListingOf(row sqlcgen.GetContractListingRow) ContractListing {
	return ContractListing{
		ID: row.ID, Repository: row.Repository, Title: row.Title, Care: row.Care, State: row.State,
		Version: row.Version, OpenDecisions: row.OpenDecisions, Conversation: row.Conversation,
		Created: row.Created, Changed: row.Changed,
	}
}

// contractVersionOf converts a stored version row to a ContractVersion, decoding its JSON lists.
func contractVersionOf(row sqlcgen.ContractVersion) (ContractVersion, error) {
	decisions, err := decodeList[ContractDecision](row.Decisions)
	if err != nil {
		return ContractVersion{}, fmt.Errorf("decode decisions: %w", err)
	}
	changedRows, err := decodeList[string](row.ChangedRows)
	if err != nil {
		return ContractVersion{}, fmt.Errorf("decode changed rows: %w", err)
	}
	changedSections, err := decodeList[string](row.ChangedSections)
	if err != nil {
		return ContractVersion{}, fmt.Errorf("decode changed sections: %w", err)
	}
	return ContractVersion{
		Version: row.Version, Repository: row.Repository, Title: row.Title, Care: row.Care, Body: row.Body,
		Decisions: decisions, Words: row.Words, Writer: row.Writer, At: row.At,
		ChangedRows: changedRows, ChangedSections: changedSections,
	}, nil
}
