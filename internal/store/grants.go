package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// ErrMemberGranted reports that a member is already held by a live grant, under any pool. AddGrant returns it,
// wrapped with the member, and writes nothing.
var ErrMemberGranted = errors.New("member already granted")

// GrantMember is one member a grant holds, with the pool it was granted under.
type GrantMember struct {
	Pool   string
	Member string
}

// GrantRow is a live grant. ClientStart is the client's start time from /proc, which tells the client's process from
// another that later takes its pid. Pools is opaque JSON the caller writes. Command is the argv the client runs under
// the grant; a nil Command is stored as an empty array and reads back as an empty slice. Granted is stored at
// whole-second precision.
type GrantRow struct {
	ID          string
	Project     string
	Holder      string
	ClientPID   int
	ClientStart int64
	Pools       string
	Command     []string
	Members     []GrantMember
	Granted     time.Time
}

// AddGrant stores row and its members in one write transaction. It returns an error matching ErrMemberGranted,
// and writes nothing, when a live grant already holds one of the members. The write lock that the transaction takes
// up front keeps the check and the insert atomic against other writers.
func (s *Store) AddGrant(ctx context.Context, row GrantRow) error {
	command := row.Command
	if command == nil {
		command = []string{}
	}
	encoded, encodeErr := json.Marshal(command)
	if encodeErr != nil {
		return fmt.Errorf("encode command of grant %s: %w", row.ID, encodeErr)
	}
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.InsertGrant(ctx, sqlcgen.InsertGrantParams{
			ID: row.ID, Project: row.Project, ClientPid: int64(row.ClientPID), ClientStart: row.ClientStart,
			Pools: row.Pools, Command: string(encoded), Granted: row.Granted.Unix(), Holder: row.Holder,
		}); err != nil {
			return fmt.Errorf("insert grant %s: %w", row.ID, err)
		}
		for i, member := range row.Members {
			held, err := queries.CountMemberGrants(ctx, member.Member)
			if err != nil {
				return fmt.Errorf("count grants of member %s: %w", member.Member, err)
			}
			if held > 0 {
				return fmt.Errorf("member %s: %w", member.Member, ErrMemberGranted)
			}
			if err = queries.InsertGrantMember(ctx, sqlcgen.InsertGrantMemberParams{
				Member: member.Member, GrantID: row.ID, Pool: member.Pool, Position: int64(i),
			}); err != nil {
				return fmt.Errorf("insert member %s of grant %s: %w", member.Member, row.ID, err)
			}
		}
		return nil
	})
}

// EndGrant removes the grant with id and its members, so its members are free to grant again. An unknown id is not
// an error.
func (s *Store) EndGrant(ctx context.Context, id string) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.DeleteGrantMembers(ctx, id); err != nil {
			return fmt.Errorf("delete members of grant %s: %w", id, err)
		}
		if err := queries.DeleteGrant(ctx, id); err != nil {
			return fmt.Errorf("delete grant %s: %w", id, err)
		}
		return nil
	})
}

// Grants returns every live grant ordered by grant time then id, each with its members in the order AddGrant
// stored them. The grants and their members are read in one read transaction, so they come from one snapshot.
func (s *Store) Grants(ctx context.Context) ([]GrantRow, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin grants read: %w", err)
	}
	queries := sqlcgen.New(tx)
	grants, err := queries.ListGrants(ctx)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("list grants: %w", err), tx.Rollback())
	}
	members, err := queries.ListGrantMembers(ctx)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("list grant members: %w", err), tx.Rollback())
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("end grants read: %w", err)
	}

	byGrant := make(map[string][]GrantMember)
	for _, member := range members {
		byGrant[member.GrantID] = append(byGrant[member.GrantID], GrantMember{Pool: member.Pool, Member: member.Member})
	}
	out := make([]GrantRow, 0, len(grants))
	for _, grant := range grants {
		var command []string
		if decodeErr := json.Unmarshal([]byte(grant.Command), &command); decodeErr != nil {
			return nil, fmt.Errorf("decode command of grant %s: %w", grant.ID, decodeErr)
		}
		out = append(out, GrantRow{
			ID: grant.ID, Project: grant.Project, Holder: grant.Holder, ClientPID: int(grant.ClientPid),
			ClientStart: grant.ClientStart, Pools: grant.Pools, Command: command, Members: byGrant[grant.ID],
			Granted: time.Unix(grant.Granted, 0),
		})
	}
	return out, nil
}
