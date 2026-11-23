package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"github.com/joshsymonds/orrery/internal/llmroutes"
	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// The scopes a route binding may hold: the model a context routes, the agent type its subagents route, and the model its
// subagents route.
const (
	scopeModel         = "model"
	scopeAgentType     = "subagent_agent_type"
	scopeSubagentModel = "subagent_model"
)

// ReplaceRoutes stores reg as the route registry, replacing every stored route. In one write transaction it reads the
// ids of the stored accounts and holds reg to llmroutes.Validate with them. When Validate refuses reg, ReplaceRoutes
// returns an error that matches ErrRoutesRefused and the validation error, and changes nothing.
func (s *Store) ReplaceRoutes(ctx context.Context, reg llmroutes.Registry) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		ids, err := queries.ListLLMAccountIDs(ctx)
		if err != nil {
			return fmt.Errorf("list account ids: %w", err)
		}
		if err = llmroutes.Validate(reg, ids); err != nil {
			return fmt.Errorf("%w: %w", ErrRoutesRefused, err)
		}
		return replaceRoutes(ctx, queries, reg)
	})
}

// SetContextAccount sets the account of the route context with id context to account, and clears it when account is
// "". It returns an error that matches ErrNoRouteContext when no context has id, and one that matches ErrNoAccount when
// account is not "" and no account has its id. Only the context's account changes.
func (s *Store) SetContextAccount(ctx context.Context, context, account string) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		held, err := queries.CountRouteContext(ctx, context)
		if err != nil {
			return fmt.Errorf("count route context %s: %w", context, err)
		}
		if held == 0 {
			return fmt.Errorf("route context %s: %w", context, ErrNoRouteContext)
		}
		if account != "" {
			known, countErr := queries.CountLLMAccount(ctx, account)
			if countErr != nil {
				return fmt.Errorf("count account %s: %w", account, countErr)
			}
			if known == 0 {
				return fmt.Errorf("account %s: %w", account, ErrNoAccount)
			}
		}
		err = queries.SetRouteContextAccount(ctx, sqlcgen.SetRouteContextAccountParams{Account: account, ID: context})
		if err != nil {
			return fmt.Errorf("set account of route context %s: %w", context, err)
		}
		return nil
	})
}

// Routes returns the route registry: its default context, its seats and its contexts, the seats and the contexts in id
// order. It reads them in one read transaction. An empty store gives the zero Registry.
func (s *Store) Routes(ctx context.Context) (llmroutes.Registry, error) {
	tx, err := s.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return llmroutes.Registry{}, fmt.Errorf("begin routes read: %w", err)
	}
	reg, err := readRoutes(ctx, sqlcgen.New(tx))
	if err != nil {
		return llmroutes.Registry{}, errors.Join(err, tx.Rollback())
	}
	if err = tx.Commit(); err != nil {
		return llmroutes.Registry{}, fmt.Errorf("end routes read: %w", err)
	}
	return reg, nil
}

// Routes returns the route registry as Store.Routes does, read without writing the store.
func (r *Reader) Routes(ctx context.Context) (llmroutes.Registry, error) {
	var reg llmroutes.Registry
	err := r.readEngine(ctx, func(queries *sqlcgen.Queries) error {
		var readErr error
		reg, readErr = readRoutes(ctx, queries)
		return readErr
	})
	return reg, err
}

// replaceRoutes deletes every stored route and inserts those of reg, with queries, in the caller's write transaction.
func replaceRoutes(ctx context.Context, queries *sqlcgen.Queries, reg llmroutes.Registry) error {
	if err := queries.DeleteRouteBindings(ctx); err != nil {
		return fmt.Errorf("delete route bindings: %w", err)
	}
	if err := queries.DeleteRouteContexts(ctx); err != nil {
		return fmt.Errorf("delete route contexts: %w", err)
	}
	if err := queries.DeleteRouteSeats(ctx); err != nil {
		return fmt.Errorf("delete route seats: %w", err)
	}
	if err := queries.DeleteRouteRegistry(ctx); err != nil {
		return fmt.Errorf("delete route registry: %w", err)
	}
	if err := queries.InsertRouteRegistry(ctx, reg.DefaultContext); err != nil {
		return fmt.Errorf("insert route registry: %w", err)
	}
	for _, seat := range reg.Seats {
		if err := insertRouteSeat(ctx, queries, seat); err != nil {
			return err
		}
	}
	for _, route := range reg.Contexts {
		if err := insertRouteContext(ctx, queries, route); err != nil {
			return err
		}
	}
	return nil
}

// insertRouteSeat stores seat, with its strip betas as a JSON array.
func insertRouteSeat(ctx context.Context, queries *sqlcgen.Queries, seat llmroutes.Seat) error {
	stripBetas, err := encodeList(seat.StripBetas)
	if err != nil {
		return fmt.Errorf("encode strip betas of seat %s: %w", seat.ID, err)
	}
	err = queries.InsertRouteSeat(ctx, sqlcgen.InsertRouteSeatParams{
		ID: seat.ID, AuthMode: string(seat.AuthMode), Upstream: seat.Upstream, Model: seat.Model,
		ApiKeyEnvFile: seat.APIKeyEnvFile, AwsProfile: seat.AWSProfile, AwsRegion: seat.AWSRegion,
		ModelMapEnvFile: seat.ModelMapEnvFile, Billing: seat.Billing, Speed: seat.Speed, CacheTtl: seat.CacheTTL,
		Insecure: boolInt(seat.Insecure), StripCacheControl: boolInt(seat.StripCacheControl),
		StrictStructuredOutput: boolInt(seat.StrictStructuredOutput), MaxInputTokens: seat.MaxInputTokens,
		StripBetas: stripBetas,
	})
	if err != nil {
		return fmt.Errorf("insert seat %s: %w", seat.ID, err)
	}
	return nil
}

// insertRouteContext stores route, and each of its bindings. The subagent columns hold the subagent routing's default
// seat and whether the routing is set at all.
func insertRouteContext(ctx context.Context, queries *sqlcgen.Queries, route llmroutes.Context) error {
	params := sqlcgen.InsertRouteContextParams{
		ID: route.ID, DefaultSeat: route.DefaultSeat, Account: route.Account,
		HasSubagents: boolInt(route.Subagents != nil),
	}
	if route.Subagents != nil {
		params.SubagentDefaultSeat = route.Subagents.DefaultSeat
	}
	if err := queries.InsertRouteContext(ctx, params); err != nil {
		return fmt.Errorf("insert route context %s: %w", route.ID, err)
	}
	if err := insertBindings(ctx, queries, route.ID, scopeModel, route.Models); err != nil {
		return err
	}
	if route.Subagents == nil {
		return nil
	}
	if err := insertBindings(ctx, queries, route.ID, scopeAgentType, route.Subagents.AgentTypes); err != nil {
		return err
	}
	return insertBindings(ctx, queries, route.ID, scopeSubagentModel, route.Subagents.Models)
}

// insertBindings stores one binding of contextID in scope for each selector of seats, and the seat it names.
func insertBindings(
	ctx context.Context, queries *sqlcgen.Queries, contextID, scope string, seats map[string]string,
) error {
	for selector, seat := range seats {
		err := queries.InsertRouteBinding(ctx, sqlcgen.InsertRouteBindingParams{
			Context: contextID, Scope: scope, Selector: selector, Seat: seat,
		})
		if err != nil {
			return fmt.Errorf("insert %s binding of route context %s: %w", scope, contextID, err)
		}
	}
	return nil
}

// readRoutes returns the route registry read with queries. A store with no registry row has an empty default context,
// and its seats and contexts stay nil when none are stored.
func readRoutes(ctx context.Context, queries *sqlcgen.Queries) (llmroutes.Registry, error) {
	registry, err := queries.GetRouteRegistry(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return llmroutes.Registry{}, fmt.Errorf("read route registry: %w", err)
	}
	seats, err := readSeats(ctx, queries)
	if err != nil {
		return llmroutes.Registry{}, err
	}
	contexts, err := readContexts(ctx, queries)
	if err != nil {
		return llmroutes.Registry{}, err
	}
	return llmroutes.Registry{DefaultContext: registry.DefaultContext, Seats: seats, Contexts: contexts}, nil
}

// readSeats returns the seats in id order, each with its strip betas decoded.
func readSeats(ctx context.Context, queries *sqlcgen.Queries) ([]llmroutes.Seat, error) {
	rows, err := queries.ListRouteSeats(ctx)
	if err != nil {
		return nil, fmt.Errorf("list route seats: %w", err)
	}
	// slices.Grow keeps a nil slice nil when there are no rows, which the zero Registry needs.
	seats := slices.Grow([]llmroutes.Seat(nil), len(rows))
	for _, row := range rows {
		stripBetas, decodeErr := decodeList[string](row.StripBetas)
		if decodeErr != nil {
			return nil, fmt.Errorf("read strip betas of seat %s: %w", row.ID, decodeErr)
		}
		seats = append(seats, llmroutes.Seat{
			ID: row.ID, AuthMode: llmroutes.AuthMode(row.AuthMode), Upstream: row.Upstream, Model: row.Model,
			APIKeyEnvFile: row.ApiKeyEnvFile, AWSProfile: row.AwsProfile, AWSRegion: row.AwsRegion,
			ModelMapEnvFile: row.ModelMapEnvFile, Billing: row.Billing, Speed: row.Speed, CacheTTL: row.CacheTtl,
			Insecure: row.Insecure == 1, StripCacheControl: row.StripCacheControl == 1,
			StrictStructuredOutput: row.StrictStructuredOutput == 1, MaxInputTokens: row.MaxInputTokens,
			StripBetas: stripBetas,
		})
	}
	return seats, nil
}

// readContexts returns the contexts in id order, each with its model bindings and its subagent routing.
func readContexts(ctx context.Context, queries *sqlcgen.Queries) ([]llmroutes.Context, error) {
	rows, err := queries.ListRouteContexts(ctx)
	if err != nil {
		return nil, fmt.Errorf("list route contexts: %w", err)
	}
	bindings, err := queries.ListRouteBindings(ctx)
	if err != nil {
		return nil, fmt.Errorf("list route bindings: %w", err)
	}
	byContext := groupBindings(bindings)
	// slices.Grow keeps a nil slice nil when there are no rows, which the zero Registry needs.
	contexts := slices.Grow([]llmroutes.Context(nil), len(rows))
	for _, row := range rows {
		scopes := byContext[row.ID]
		route := llmroutes.Context{
			ID: row.ID, DefaultSeat: row.DefaultSeat, Account: row.Account, Models: scopes[scopeModel],
		}
		if row.HasSubagents == 1 {
			route.Subagents = &llmroutes.Subagents{
				DefaultSeat: row.SubagentDefaultSeat,
				AgentTypes:  scopes[scopeAgentType],
				Models:      scopes[scopeSubagentModel],
			}
		}
		contexts = append(contexts, route)
	}
	return contexts, nil
}

// groupBindings groups bindings by context, then by scope, each selector mapped to its seat. A scope with no bindings
// has no entry, so the map it reads as is nil.
func groupBindings(rows []sqlcgen.RouteBinding) map[string]map[string]map[string]string {
	grouped := map[string]map[string]map[string]string{}
	for _, row := range rows {
		if grouped[row.Context] == nil {
			grouped[row.Context] = map[string]map[string]string{}
		}
		if grouped[row.Context][row.Scope] == nil {
			grouped[row.Context][row.Scope] = map[string]string{}
		}
		grouped[row.Context][row.Scope][row.Selector] = row.Seat
	}
	return grouped
}
