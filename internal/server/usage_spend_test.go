package server_test

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/ledger"
	"github.com/joshsymonds/orrery/internal/server"
)

// The synthetic span TestUsageSpend asks for, in unix seconds. The span is half-open: a request that starts at
// spendFrom counts, and one that starts at spendTo does not.
const (
	spendFrom  int64 = 1_800_000_100
	spendTo    int64 = 1_800_000_200
	spendSeatA       = "synthetic-seat-a"
	spendSeatB       = "synthetic-seat-b"
)

// spendRequest is one invented request the test ledger holds. A nil cost is unknown. A request that is not done is in
// flight: it is never finished, so it has no completion and no costs.
type spendRequest struct {
	startedAt    int64
	seat         string
	billed, list *int64
	done         bool
}

// spendCost returns a pointer to the cost v, for a spendRequest.
func spendCost(v int64) *int64 {
	return &v
}

// spendRequests returns the invented requests. Seat A has one request before the span, one at its start, one inside
// it, one at its last second, one at its end, one with an unknown billed cost and one in flight. Seat B has one before
// the span, one inside it with an unknown list cost, one inside it and one at its end.
func spendRequests() []spendRequest {
	return []spendRequest{
		{startedAt: spendFrom - 1, seat: spendSeatA, billed: spendCost(1000), list: spendCost(2000), done: true},
		{startedAt: spendFrom, seat: spendSeatA, billed: spendCost(10), list: spendCost(20), done: true},
		{startedAt: 1_800_000_150, seat: spendSeatA, billed: spendCost(30), list: spendCost(40), done: true},
		{startedAt: spendTo - 1, seat: spendSeatA, billed: spendCost(300), list: spendCost(400), done: true},
		{startedAt: spendTo, seat: spendSeatA, billed: spendCost(5000), list: spendCost(6000), done: true},
		{startedAt: 1_800_000_160, seat: spendSeatA, billed: nil, list: spendCost(50), done: true},
		{startedAt: 1_800_000_170, seat: spendSeatA},
		{startedAt: spendFrom - 1, seat: spendSeatB, billed: spendCost(7000), list: spendCost(8000), done: true},
		{startedAt: 1_800_000_120, seat: spendSeatB, billed: spendCost(100), list: nil, done: true},
		{startedAt: 1_800_000_180, seat: spendSeatB, billed: spendCost(200), list: spendCost(300), done: true},
		{startedAt: spendTo, seat: spendSeatB, billed: spendCost(9000), list: spendCost(9500), done: true},
	}
}

// seedSpendLedger writes spendRequests into a new ledger at path through its writer, and closes the writer.
func seedSpendLedger(t *testing.T, path string) {
	t.Helper()
	ctx := t.Context()
	l, err := ledger.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for _, r := range spendRequests() {
		id, err := l.Start(ctx, ledger.Start{
			Account:        "synthetic-account",
			Seat:           r.seat,
			Context:        "synthetic-context",
			RequestedModel: "synthetic-model",
			ClaudeSession:  "synthetic-session",
			AgentID:        "synthetic-agent",
			AgentType:      "synthetic-type",
			StartedAt:      r.startedAt,
		})
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		if !r.done {
			continue
		}
		finish := ledger.Finish{
			CompletedAt:       r.startedAt + 5,
			ResolvedModel:     "synthetic-model",
			BilledCostNanoUSD: r.billed,
			ListCostNanoUSD:   r.list,
		}
		if err := l.Finish(ctx, id, finish); err != nil {
			t.Fatalf("Finish: %v", err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// newSpendHandler returns the guarded handler that admits allowedLogin, reading the ledger through reader, or with no
// ledger when reader is nil.
func newSpendHandler(t *testing.T, reader *ledger.Reader) http.Handler {
	t.Helper()
	handler, err := server.New(server.Config{
		Logins: []string{allowedLogin},
		Tmux:   newTmuxClient(t),
		Push:   newPushStore(t),
		Ledger: reader,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return handler
}

// usageSpendClient serves handler on an httptest server and returns a generated Usage client whose requests carry
// allowedLogin and the client header.
func usageSpendClient(t *testing.T, handler http.Handler) orreryv1connect.UsageClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	httpClient := &http.Client{Transport: headerTransport{base: http.DefaultTransport, login: allowedLogin}}
	return orreryv1connect.NewUsageClient(httpClient, srv.URL)
}

// fullScanOfRequests reports whether a query plan row is a full scan of the requests table, with or without an index
// it scans through.
func fullScanOfRequests(detail string) bool {
	fields := strings.Fields(detail)
	if len(fields) < 2 || fields[0] != "SCAN" {
		return false
	}
	if fields[1] == "TABLE" && len(fields) > 2 {
		return fields[2] == "requests"
	}
	return fields[1] == "requests"
}

func TestUsageSpend(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "ledger.db")
	seedSpendLedger(t, path)
	usage := usageSpendClient(t, newSpendHandler(t, ledger.NewReader(path)))

	t.Run("seats over the half-open window", func(t *testing.T) {
		got, err := usage.Spend(ctx, connect.NewRequest(&orreryv1.UsageSpendRequest{From: spendFrom, To: spendTo}))
		if err != nil {
			t.Fatalf("Spend: %v", err)
		}
		want := map[string]*orreryv1.UsageSeatSpend{
			spendSeatA: {Seat: spendSeatA, BilledCostNanoUsd: 340, ListCostNanoUsd: 510, UnknownCostRequests: 1},
			spendSeatB: {Seat: spendSeatB, BilledCostNanoUsd: 300, ListCostNanoUsd: 300, UnknownCostRequests: 1},
		}
		seats := got.Msg.GetSeats()
		if len(seats) != len(want) {
			t.Fatalf("Spend returned %d seats, want %d: %v", len(seats), len(want), seats)
		}
		for _, s := range seats {
			w, ok := want[s.GetSeat()]
			if !ok {
				t.Errorf("Spend returned seat %q, want only %q and %q", s.GetSeat(), spendSeatA, spendSeatB)
				continue
			}
			if !proto.Equal(s, w) {
				t.Errorf("Spend seat %q = %v, want %v", s.GetSeat(), s, w)
			}
		}
	})

	t.Run("query plan", func(t *testing.T) {
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatalf("open ledger for EXPLAIN QUERY PLAN: %v", err)
		}
		t.Cleanup(func() {
			if err := db.Close(); err != nil {
				t.Errorf("close ledger: %v", err)
			}
		})
		rows, err := db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+ledger.SpendQuery, spendFrom, spendTo)
		if err != nil {
			t.Fatalf("EXPLAIN QUERY PLAN: %v", err)
		}
		defer func() {
			if err := rows.Close(); err != nil {
				t.Errorf("close EXPLAIN QUERY PLAN rows: %v", err)
			}
		}()
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatalf("scan EXPLAIN QUERY PLAN: %v", err)
			}
			t.Logf("plan: %s", detail)
			plan = append(plan, detail)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("read EXPLAIN QUERY PLAN: %v", err)
		}
		if len(plan) == 0 {
			t.Fatal("EXPLAIN QUERY PLAN returned no rows")
		}
		var searched bool
		for _, detail := range plan {
			if fullScanOfRequests(detail) {
				t.Errorf("query plan scans requests in full: %q", detail)
			}
			if strings.Contains(detail, "requests_started_at") {
				searched = true
			}
		}
		if !searched {
			t.Errorf("query plan does not read through requests_started_at: %v", plan)
		}
	})

	t.Run("window ending before it starts", func(t *testing.T) {
		_, err := usage.Spend(ctx, connect.NewRequest(&orreryv1.UsageSpendRequest{From: spendTo, To: spendFrom}))
		if code := connect.CodeOf(err); code != connect.CodeInvalidArgument {
			t.Errorf("Spend with to < from: code %v, want %v (err %v)", code, connect.CodeInvalidArgument, err)
		}
	})

	t.Run("no ledger", func(t *testing.T) {
		unavailable := usageSpendClient(t, newSpendHandler(t, nil))
		_, err := unavailable.Spend(ctx, connect.NewRequest(&orreryv1.UsageSpendRequest{From: spendFrom, To: spendTo}))
		if code := connect.CodeOf(err); code != connect.CodeUnavailable {
			t.Errorf("Spend with no ledger: code %v, want %v (err %v)", code, connect.CodeUnavailable, err)
		}
	})
}
