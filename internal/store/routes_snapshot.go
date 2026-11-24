package store

import (
	"context"
	"fmt"

	"github.com/joshsymonds/orrery/internal/llmroutes"
	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// RoutesSnapshot is the route registry with the token of each account a route context names. Tokens maps the id of each
// such account to its token, and has no entry for an account no context names.
type RoutesSnapshot struct {
	Routes llmroutes.Registry
	Tokens map[string]string
}

// RoutesSnapshot reads the route registry and the token of each account a route context names, in one read transaction.
// It is the gateway's one token-bearing read, and it is Go-only: nothing else returns a token. It returns an error that
// matches ErrUnavailable when the file is missing or the database is busy. It never creates the file or its directory,
// and it never writes the database.
func (r *Reader) RoutesSnapshot(ctx context.Context) (RoutesSnapshot, error) {
	var snap RoutesSnapshot
	err := r.readEngine(ctx, func(queries *sqlcgen.Queries) error {
		var readErr error
		snap, readErr = readRoutesSnapshot(ctx, queries)
		return readErr
	})
	return snap, err
}

// readRoutesSnapshot returns the route registry and the tokens of the accounts it names, read with queries.
func readRoutesSnapshot(ctx context.Context, queries *sqlcgen.Queries) (RoutesSnapshot, error) {
	routes, err := readRoutes(ctx, queries)
	if err != nil {
		return RoutesSnapshot{}, err
	}
	rows, err := queries.ListRouteAccountTokens(ctx)
	if err != nil {
		return RoutesSnapshot{}, fmt.Errorf("list route account tokens: %w", err)
	}
	tokens := make(map[string]string, len(rows))
	for _, row := range rows {
		tokens[row.ID] = row.Token
	}
	return RoutesSnapshot{Routes: routes, Tokens: tokens}, nil
}
