package ledger

import (
	"database/sql"
	"path/filepath"
	"slices"
	"testing"
)

// storedRequest is one requests row as it reads back from ledger.db.
type storedRequest struct {
	StartedAt, CompletedAt             sql.NullInt64
	Account, Seat, Context             string
	RequestedModel, ResolvedModel      string
	ClaudeSession, AgentID, AgentType  string
	InputTokens, OutputTokens          int64
	CacheReadTokens, CacheWriteTokens  int64
	BilledCostNanoUSD, ListCostNanoUSD sql.NullInt64
}

// storedClaim is one request_claims row as it reads back from ledger.db.
type storedClaim struct {
	Claim       string
	Utilization sql.NullFloat64
	ResetAt     sql.NullInt64
	Status      string
}

// nullInt returns an sql.NullInt64 that holds v.
func nullInt(v int64) sql.NullInt64 {
	return sql.NullInt64{Int64: v, Valid: true}
}

// nullFloat returns an sql.NullFloat64 that holds v.
func nullFloat(v float64) sql.NullFloat64 {
	return sql.NullFloat64{Float64: v, Valid: true}
}

// openWriteLedger opens a ledger at a new path in a temp dir. It returns the Ledger, which writes, and a second handle
// on the same ledger.db, which reads it, so each check reads what the file holds.
func openWriteLedger(t *testing.T) (*Ledger, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ledger.db")
	l, err := Open(t.Context(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := l.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open ledger.db for reading: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close ledger.db reader: %v", err)
		}
	})
	return l, db
}

// inventedStart returns a request as it starts, with invented values.
func inventedStart() Start {
	return Start{
		Account:        "acct-invented-1",
		CallerAccount:  "caller-invented-2",
		Seat:           "seat-invented",
		Context:        "context-invented",
		RequestedModel: "model-requested-invented",
		ClaudeSession:  "session-invented",
		AgentID:        "agent-invented",
		AgentType:      "agent-type-invented",
		StartedAt:      1_800_000_000,
	}
}

// inventedFinish returns a request as it completes, with invented tokens, costs and claims.
func inventedFinish() Finish {
	return Finish{
		CompletedAt:       1_800_000_042,
		ResolvedModel:     "model-resolved-invented",
		InputTokens:       1200,
		OutputTokens:      345,
		CacheReadTokens:   6789,
		CacheWriteTokens:  210,
		BilledCostNanoUSD: new(int64(1_234_500)),
		ListCostNanoUSD:   new(int64(2_345_600)),
		Claims: []Claim{
			{Name: "five_hour", Utilization: new(0.42), Reset: new(int64(1_800_003_600)), Status: "allowed"},
			{Name: "seven_day", Status: "rejected"},
		},
	}
}

// readRequest reads the requests row with id from db.
func readRequest(t *testing.T, db *sql.DB, id int64) storedRequest {
	t.Helper()
	var r storedRequest
	err := db.QueryRowContext(t.Context(), `SELECT started_at, completed_at, account, seat, context, requested_model,
		resolved_model, claude_session, agent_id, agent_type, input_tokens, output_tokens, cache_read_tokens,
		cache_write_tokens, billed_cost_nano_usd, list_cost_nano_usd FROM requests WHERE id = ?`, id).Scan(
		&r.StartedAt, &r.CompletedAt, &r.Account, &r.Seat, &r.Context, &r.RequestedModel, &r.ResolvedModel,
		&r.ClaudeSession, &r.AgentID, &r.AgentType, &r.InputTokens, &r.OutputTokens, &r.CacheReadTokens,
		&r.CacheWriteTokens, &r.BilledCostNanoUSD, &r.ListCostNanoUSD)
	if err != nil {
		t.Fatalf("read request %d: %v", id, err)
	}
	return r
}

// readClaims reads the request_claims rows of the request id from db, in claim name order.
func readClaims(t *testing.T, db *sql.DB, id int64) []storedClaim {
	t.Helper()
	rows, err := db.QueryContext(t.Context(),
		`SELECT claim, utilization, reset_at, status FROM request_claims WHERE request_id = ? ORDER BY claim`, id)
	if err != nil {
		t.Fatalf("read claims of request %d: %v", id, err)
	}
	defer rows.Close()
	var claims []storedClaim
	for rows.Next() {
		var c storedClaim
		if err := rows.Scan(&c.Claim, &c.Utilization, &c.ResetAt, &c.Status); err != nil {
			t.Fatalf("scan claim of request %d: %v", id, err)
		}
		claims = append(claims, c)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate claims of request %d: %v", id, err)
	}
	return claims
}

// countRows returns the result of the counting query, a SELECT COUNT(*) statement, run on db.
func countRows(t *testing.T, db *sql.DB, query string) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRowContext(t.Context(), query).Scan(&n); err != nil {
		t.Fatalf("count rows with %q: %v", query, err)
	}
	return n
}

func TestLedgerWrite(t *testing.T) {
	t.Run("stores a request at Start and finalizes it at Finish", func(t *testing.T) {
		l, db := openWriteLedger(t)
		id, err := l.Start(t.Context(), inventedStart())
		if err != nil {
			t.Fatalf("Start: %v", err)
		}

		wantStarted := storedRequest{
			StartedAt:      nullInt(1_800_000_000),
			Account:        "acct-invented-1",
			Seat:           "seat-invented",
			Context:        "context-invented",
			RequestedModel: "model-requested-invented",
			ClaudeSession:  "session-invented",
			AgentID:        "agent-invented",
			AgentType:      "agent-type-invented",
		}
		if got := readRequest(t, db, id); got != wantStarted {
			t.Fatalf("stored at Start = %+v, want %+v", got, wantStarted)
		}
		if got := readClaims(t, db, id); len(got) != 0 {
			t.Fatalf("claims stored at Start = %+v, want none", got)
		}

		if err := l.Finish(t.Context(), id, inventedFinish()); err != nil {
			t.Fatalf("Finish: %v", err)
		}

		wantFinished := wantStarted
		wantFinished.CompletedAt = nullInt(1_800_000_042)
		wantFinished.ResolvedModel = "model-resolved-invented"
		wantFinished.InputTokens = 1200
		wantFinished.OutputTokens = 345
		wantFinished.CacheReadTokens = 6789
		wantFinished.CacheWriteTokens = 210
		wantFinished.BilledCostNanoUSD = nullInt(1_234_500)
		wantFinished.ListCostNanoUSD = nullInt(2_345_600)
		if got := readRequest(t, db, id); got != wantFinished {
			t.Fatalf("stored at Finish = %+v, want %+v", got, wantFinished)
		}
		wantClaims := []storedClaim{
			{Claim: "five_hour", Utilization: nullFloat(0.42), ResetAt: nullInt(1_800_003_600), Status: "allowed"},
			{Claim: "seven_day", Status: "rejected"},
		}
		if got := readClaims(t, db, id); !slices.Equal(got, wantClaims) {
			t.Fatalf("claims stored at Finish = %+v, want %+v", got, wantClaims)
		}
	})

	t.Run("stores an unknown cost as NULL", func(t *testing.T) {
		l, db := openWriteLedger(t)
		id, err := l.Start(t.Context(), inventedStart())
		if err != nil {
			t.Fatalf("Start: %v", err)
		}

		f := inventedFinish()
		f.BilledCostNanoUSD = nil
		f.ListCostNanoUSD = nil
		if err := l.Finish(t.Context(), id, f); err != nil {
			t.Fatalf("Finish: %v", err)
		}

		got := readRequest(t, db, id)
		if got.BilledCostNanoUSD.Valid || got.ListCostNanoUSD.Valid {
			t.Fatalf("unknown costs stored as billed %+v, list %+v; want both NULL",
				got.BilledCostNanoUSD, got.ListCostNanoUSD)
		}
		if !got.CompletedAt.Valid || got.CompletedAt.Int64 != 1_800_000_042 {
			t.Fatalf("completed_at = %+v, want 1800000042", got.CompletedAt)
		}
	})

	t.Run("stores CallerAccount as the account when Account is empty", func(t *testing.T) {
		l, db := openWriteLedger(t)
		s := inventedStart()
		s.Account = ""
		s.CallerAccount = "caller-uuid-invented"
		id, err := l.Start(t.Context(), s)
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		if got := readRequest(t, db, id).Account; got != "caller-uuid-invented" {
			t.Fatalf("account = %q, want the caller's account caller-uuid-invented", got)
		}
	})

	t.Run("refuses an id with no row and writes nothing", func(t *testing.T) {
		l, db := openWriteLedger(t)
		if err := l.Finish(t.Context(), 404, inventedFinish()); err == nil {
			t.Fatal("Finish on an id with no row: want error, got nil")
		}
		if n := countRows(t, db, `SELECT COUNT(*) FROM requests`); n != 0 {
			t.Fatalf("requests rows = %d, want 0", n)
		}
		if n := countRows(t, db, `SELECT COUNT(*) FROM request_claims`); n != 0 {
			t.Fatalf("request_claims rows = %d, want 0", n)
		}
	})

	t.Run("refuses a request already finalized and keeps its first finish", func(t *testing.T) {
		l, db := openWriteLedger(t)
		id, err := l.Start(t.Context(), inventedStart())
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		if err := l.Finish(t.Context(), id, inventedFinish()); err != nil {
			t.Fatalf("first Finish: %v", err)
		}
		before := readRequest(t, db, id)
		beforeClaims := readClaims(t, db, id)

		second := inventedFinish()
		second.CompletedAt = 1_800_000_099
		second.InputTokens = 9999
		second.Claims = []Claim{{Name: "other", Status: "allowed"}}
		if err := l.Finish(t.Context(), id, second); err == nil {
			t.Fatal("second Finish: want error, got nil")
		}
		if got := readRequest(t, db, id); got != before {
			t.Fatalf("row after refused Finish = %+v, want %+v", got, before)
		}
		if got := readClaims(t, db, id); !slices.Equal(got, beforeClaims) {
			t.Fatalf("claims after refused Finish = %+v, want %+v", got, beforeClaims)
		}
	})

	t.Run("rolls back the whole Finish when a claim fails", func(t *testing.T) {
		l, db := openWriteLedger(t)
		id, err := l.Start(t.Context(), inventedStart())
		if err != nil {
			t.Fatalf("Start: %v", err)
		}

		f := inventedFinish()
		f.Claims = []Claim{
			{Name: "five_hour", Status: "allowed"},
			{Name: "five_hour", Status: "rejected"},
		}
		if err := l.Finish(t.Context(), id, f); err == nil {
			t.Fatal("Finish with a duplicate claim name: want error, got nil")
		}
		if got := readRequest(t, db, id); got.CompletedAt.Valid || got.InputTokens != 0 {
			t.Fatalf("request after failed Finish = %+v, want it still open", got)
		}
		if n := countRows(t, db, `SELECT COUNT(*) FROM request_claims`); n != 0 {
			t.Fatalf("request_claims rows after failed Finish = %d, want 0", n)
		}
		if err := l.Finish(t.Context(), id, inventedFinish()); err != nil {
			t.Fatalf("Finish after failed Finish: %v", err)
		}
	})
}
