package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
)

// slowCallThreshold is how long a store call may take before the store logs it and counts it as slow.
const slowCallThreshold = 100 * time.Millisecond

// maxQueryNameLen caps the name a statement without a sqlc name is logged by: its first line, cut to this length.
const maxQueryNameLen = 60

// slowCalls reports each store call that takes longer than slowCallThreshold: it logs the call's query names and how
// long the call waited for the store's one connection and then ran on it, and runs counted.
type slowCalls struct {
	now     func() time.Time
	logger  *slog.Logger
	counted func(context.Context)
}

// callEnd says what ends a timed call.
type callEnd int

const (
	// endAtReturn ends an Exec on the pool when the pool method returns.
	endAtReturn callEnd = iota
	// endAtRowsClose ends a query on the pool when its rows close, or when the pool method fails.
	endAtRowsClose
	// endAtTxEnd ends a transaction on the pool at its Commit or Rollback, or when BeginTx fails.
	endAtTxEnd
	// endByCaller ends a write on a held connection when write returns.
	endByCaller
)

// callTimer times one store call, from the call to its end. The call's first statement on the connection marks the end
// of its wait for the connection: database/sql hands the connection over just before that statement reaches it. The
// time after is the call's run on the connection, the busy timeout's wait for SQLite's write lock among it.
type callTimer struct {
	slow  *slowCalls
	end   callEnd
	start time.Time

	mu       sync.Mutex
	acquired time.Time
	names    []string
	done     bool
}

// timerKey is the context key of the callTimer a pool call carries down to the connection.
type timerKey struct{}

// begin starts timing a call that ends as end. A non-empty name is its first query name. The returned context
// carries the timer to the connection.
func (s *slowCalls) begin(ctx context.Context, end callEnd, name string) (context.Context, *callTimer) {
	t := &callTimer{slow: s, end: end, start: s.now()}
	if name != "" {
		t.names = []string{statementName(name)}
	}
	return context.WithValue(ctx, timerKey{}, t), t
}

// timerOf returns the callTimer ctx carries, or nil.
func timerOf(ctx context.Context) *callTimer {
	if timer, ok := ctx.Value(timerKey{}).(*callTimer); ok {
		return timer
	}
	return nil
}

// ran records that the call ran query on the connection. The first statement ends the call's wait.
func (t *callTimer) ran(query string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.done {
		return
	}
	if t.acquired.IsZero() {
		t.acquired = t.slow.now()
	}
	if name := statementName(query); len(t.names) == 0 || t.names[len(t.names)-1] != name {
		t.names = append(t.names, name)
	}
}

// finish ends the call and reports it when it took longer than slowCallThreshold. A call that never reached the
// connection waited all its time. A second finish does nothing.
func (t *callTimer) finish(ctx context.Context) {
	t.mu.Lock()
	if t.done {
		t.mu.Unlock()
		return
	}
	t.done = true
	end := t.slow.now()
	acquired := t.acquired
	if acquired.IsZero() {
		acquired = end
	}
	names := slices.Clone(t.names)
	t.mu.Unlock()
	if end.Sub(t.start) <= slowCallThreshold {
		return
	}
	t.slow.logger.WarnContext(ctx, "slow store call",
		"queries", strings.Join(names, ","), "wait", acquired.Sub(t.start), "run", end.Sub(acquired))
	if t.slow.counted != nil {
		t.slow.counted(ctx)
	}
}

// statementName names query for the log: its sqlc name, or its first line cut to maxQueryNameLen bytes.
func statementName(query string) string {
	if rest, ok := strings.CutPrefix(query, "-- name: "); ok {
		name, _, _ := strings.Cut(rest, " ")
		return name
	}
	line, _, _ := strings.Cut(strings.TrimSpace(query), "\n")
	if len(line) > maxQueryNameLen {
		line = line[:maxQueryNameLen]
	}
	return line
}

// pool is the store's *sql.DB with each call the store makes on it timed: an Exec, a query and a transaction each
// from the call until the connection is free again. Conn is not timed here: write, its one caller, times its write.
// Its errors wrap database/sql's with their text unchanged, so errors.Is and every message stay as they were.
type pool struct {
	*sql.DB
	slow *slowCalls
}

// ExecContext runs query on the pool, timed until it returns.
func (p *pool) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	ctx, t := p.slow.begin(ctx, endAtReturn, query)
	result, err := p.DB.ExecContext(ctx, query, args...)
	t.finish(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}
	return result, nil
}

// QueryContext runs query on the pool, timed until its rows close.
func (p *pool) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	ctx, t := p.slow.begin(ctx, endAtRowsClose, query)
	rows, err := p.DB.QueryContext(ctx, query, args...)
	if err != nil {
		t.finish(ctx)
		return nil, fmt.Errorf("%w", err)
	}
	return rows, nil
}

// QueryRowContext runs query on the pool, timed until its row is scanned.
func (p *pool) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	ctx, t := p.slow.begin(ctx, endAtRowsClose, query)
	row := p.DB.QueryRowContext(ctx, query, args...)
	if row.Err() != nil {
		t.finish(ctx)
	}
	return row
}

// BeginTx starts a transaction on the pool, timed until it commits or rolls back.
func (p *pool) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	ctx, t := p.slow.begin(ctx, endAtTxEnd, "BEGIN")
	tx, err := p.DB.BeginTx(ctx, opts)
	if err != nil {
		t.finish(ctx)
		return nil, fmt.Errorf("%w", err)
	}
	return tx, nil
}

// timedRows ends its call's timer when it closes.
type timedRows struct {
	driver.Rows
	timer *callTimer
}

// Close closes the rows and ends the call. Close has no context, so the call's report has none.
func (r *timedRows) Close() error {
	err := r.Rows.Close()
	r.timer.finish(context.Background())
	if err != nil {
		return fmt.Errorf("%w", err)
	}
	return nil
}

// timedTx ends its call's timer when it commits or rolls back. Neither has a context, so the call's report has none.
type timedTx struct {
	driver.Tx
	timer *callTimer
}

// Commit commits the transaction and ends the call.
func (t *timedTx) Commit() error {
	err := t.Tx.Commit()
	t.timer.finish(context.Background())
	if err != nil {
		return fmt.Errorf("%w", err)
	}
	return nil
}

// Rollback rolls the transaction back and ends the call.
func (t *timedTx) Rollback() error {
	err := t.Tx.Rollback()
	t.timer.finish(context.Background())
	if err != nil {
		return fmt.Errorf("%w", err)
	}
	return nil
}
