package server_test

import (
	"database/sql"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/ledger"
	"github.com/joshsymonds/orrery/internal/server"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

const (
	// usageClaudeA is the Claude session orrery-1 runs. Its finalized requests are the ones orrery-1's totals count.
	usageClaudeA = "synthetic-claude-session-a"
	// usageClaudeB is the Claude session orrery-2 runs. Its requests must never reach orrery-1's totals.
	usageClaudeB = "synthetic-claude-session-b"

	usageModelOne = "synthetic-model-one"
	usageModelTwo = "synthetic-model-two"
	usageAgentOne = "synthetic-agent-one"
	usageAgentTwo = "synthetic-agent-two"
	// usageRequested is the model every request asked for. The totals group by the model that answered instead.
	usageRequested = "synthetic-requested-model"
	usageAccount   = "synthetic-account"
	usageSeat      = "synthetic-seat"
	usageContext   = "synthetic-context"
	// usageStarted is the unix second every request starts at, and usageDone the second each finished request
	// completes at.
	usageStarted = 1_700_000_000
	usageDone    = 1_700_000_005
)

// seedUsageSessions writes the orrery sessions the usage tests read into the store at storePath. orrery-1 runs
// usageClaudeA, orrery-2 runs usageClaudeB, and orrery-3 has no Claude session.
func seedUsageSessions(t *testing.T, storePath string) {
	t.Helper()
	storetest.Seed(t, storePath)
	s, err := store.Open(t.Context(), storePath, storetest.NoSync())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close store: %v", err)
		}
	})
	panes := []store.Pane{
		{ID: "orrery-1", Pane: "%1", Fields: store.Fields{ClaudeSession: usageClaudeA}},
		{ID: "orrery-2", Pane: "%2", Fields: store.Fields{ClaudeSession: usageClaudeB}},
		{ID: "orrery-3", Pane: "%3"},
	}
	for _, pane := range panes {
		if err := s.SyncPane(t.Context(), "-L usage", pane, time.Unix(usageStarted, 0)); err != nil {
			t.Fatalf("SyncPane %s: %v", pane.ID, err)
		}
	}
}

// usageStart returns the start of a request the Claude session claude made as an agent of agentType.
func usageStart(claude, agentType string) ledger.Start {
	return ledger.Start{
		Account:        usageAccount,
		Seat:           usageSeat,
		Context:        usageContext,
		RequestedModel: usageRequested,
		ClaudeSession:  claude,
		AgentID:        "synthetic-agent-id",
		AgentType:      agentType,
		StartedAt:      usageStarted,
	}
}

// startRequest writes the start of a request in l and returns its id. It fails the test on error.
func startRequest(t *testing.T, l *ledger.Ledger, start ledger.Start) int64 {
	t.Helper()
	id, err := l.Start(t.Context(), start)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return id
}

// finishRequest writes the start of a request in l, then finishes it with finish. It fails the test on error.
func finishRequest(t *testing.T, l *ledger.Ledger, start ledger.Start, finish ledger.Finish) {
	t.Helper()
	id := startRequest(t, l, start)
	if err := l.Finish(t.Context(), id, finish); err != nil {
		t.Fatalf("Finish: %v", err)
	}
}

// seedUsageLedger writes the ledger the usage tests read beside the store at storePath, and returns its path. Each
// request of usageClaudeA is listed with its model, tokens and costs. Only the first two report claims, so a count
// that joined the claims would double them. A request with an unknown cost is included, as is one still in flight.
func seedUsageLedger(t *testing.T, storePath string) string {
	t.Helper()
	path := ledger.Path(storePath)
	l, err := ledger.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("ledger.Open: %v", err)
	}
	claims := []ledger.Claim{
		{Name: "five_hour", Utilization: new(0.4), Reset: new(int64(1_700_003_600)), Status: "allowed"},
		{Name: "seven_day", Status: "allowed"},
	}
	finishRequest(t, l, usageStart(usageClaudeA, usageAgentOne), ledger.Finish{
		CompletedAt: usageDone, ResolvedModel: usageModelOne,
		InputTokens: 100, OutputTokens: 10, CacheReadTokens: 1000, CacheWriteTokens: 200,
		BilledCostNanoUSD: new(int64(500)), ListCostNanoUSD: new(int64(900)), Claims: claims,
	})
	finishRequest(t, l, usageStart(usageClaudeA, usageAgentOne), ledger.Finish{
		CompletedAt: usageDone, ResolvedModel: usageModelOne,
		InputTokens: 200, OutputTokens: 20, CacheReadTokens: 2000, CacheWriteTokens: 0,
		BilledCostNanoUSD: new(int64(700)), ListCostNanoUSD: new(int64(1100)), Claims: claims,
	})
	finishRequest(t, l, usageStart(usageClaudeA, usageAgentTwo), ledger.Finish{
		CompletedAt: usageDone, ResolvedModel: usageModelOne,
		InputTokens: 300, OutputTokens: 30, CacheReadTokens: 0, CacheWriteTokens: 50,
		BilledCostNanoUSD: nil, ListCostNanoUSD: new(int64(400)),
	})
	finishRequest(t, l, usageStart(usageClaudeA, usageAgentTwo), ledger.Finish{
		CompletedAt: usageDone, ResolvedModel: usageModelTwo,
		InputTokens: 50, OutputTokens: 5, CacheReadTokens: 500, CacheWriteTokens: 100,
		BilledCostNanoUSD: new(int64(80)), ListCostNanoUSD: nil,
	})
	startRequest(t, l, usageStart(usageClaudeA, usageAgentOne))
	finishRequest(t, l, usageStart(usageClaudeB, usageAgentOne), ledger.Finish{
		CompletedAt: usageDone, ResolvedModel: usageModelOne,
		InputTokens: 9000, OutputTokens: 900, CacheReadTokens: 9000, CacheWriteTokens: 9000,
		BilledCostNanoUSD: new(int64(9000)), ListCostNanoUSD: new(int64(9000)),
	})
	finishRequest(t, l, usageStart("", usageAgentOne), ledger.Finish{
		CompletedAt: usageDone, ResolvedModel: usageModelOne,
		InputTokens: 7, OutputTokens: 7, CacheReadTokens: 7, CacheWriteTokens: 7,
		BilledCostNanoUSD: new(int64(7)), ListCostNanoUSD: new(int64(7)),
	})
	if err = l.Close(); err != nil {
		t.Fatalf("Close ledger: %v", err)
	}
	return path
}

// usageClient serves the Usage API that server.New builds from cfg, admitting allowedLogin, and returns a client for it.
func usageClient(t *testing.T, cfg server.Config) orreryv1connect.UsageClient {
	t.Helper()
	cfg.Logins = []string{allowedLogin}
	cfg.Tmux = newTmuxClient(t)
	cfg.Push = newPushStore(t)
	handler, err := server.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return orreryv1connect.NewUsageClient(guardedHTTPClient(), srv.URL)
}

// sessionTotals returns the totals the Usage API answers for the orrery session id. It fails the test on any error.
func sessionTotals(t *testing.T, client orreryv1connect.UsageClient, id string) []*orreryv1.UsageSessionTotal {
	t.Helper()
	req := connect.NewRequest(&orreryv1.UsageSessionTotalsRequest{Session: id})
	resp, err := client.SessionTotals(t.Context(), req)
	if err != nil {
		t.Fatalf("SessionTotals(%s): %v", id, err)
	}
	return resp.Msg.GetTotals()
}

// expectSessionTotalsCode fails the test unless the Usage API fails the totals of the orrery session id with want.
func expectSessionTotalsCode(t *testing.T, client orreryv1connect.UsageClient, id string, want connect.Code) {
	t.Helper()
	req := connect.NewRequest(&orreryv1.UsageSessionTotalsRequest{Session: id})
	_, err := client.SessionTotals(t.Context(), req)
	if err == nil {
		t.Fatalf("SessionTotals(%s) succeeded, want code %v", id, want)
	}
	if got := connect.CodeOf(err); got != want {
		t.Fatalf("SessionTotals(%s) code: got %v (%v), want %v", id, got, err, want)
	}
}

// requireTotals fails the test unless got holds the totals of want, in order.
func requireTotals(t *testing.T, got, want []*orreryv1.UsageSessionTotal) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("totals: got %d, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if !proto.Equal(got[i], want[i]) {
			t.Errorf("totals[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

// wantTotalsOfA are the totals of orrery-1, in model then agent type order. They count the three finalized groups of
// usageClaudeA. The in-flight request and the requests of the other sessions are not among them.
func wantTotalsOfA() []*orreryv1.UsageSessionTotal {
	return []*orreryv1.UsageSessionTotal{
		{
			Model: usageModelOne, AgentType: usageAgentOne, Requests: 2,
			InputTokens: 300, OutputTokens: 30, CacheReadTokens: 3000, CacheWriteTokens: 200,
			BilledCostNanoUsd: 1200, ListCostNanoUsd: 2000,
		},
		{
			// Its billed cost is unknown, so its billed sum is 0. Its list cost is known.
			Model: usageModelOne, AgentType: usageAgentTwo, Requests: 1,
			InputTokens: 300, OutputTokens: 30, CacheWriteTokens: 50,
			ListCostNanoUsd: 400,
		},
		{
			// Its list cost is unknown, so its list sum is 0. Its billed cost is known.
			Model: usageModelTwo, AgentType: usageAgentTwo, Requests: 1,
			InputTokens: 50, OutputTokens: 5, CacheReadTokens: 500, CacheWriteTokens: 100,
			BilledCostNanoUsd: 80,
		},
	}
}

// wantTotalsOfB are the totals of orrery-2, the one request of usageClaudeB.
func wantTotalsOfB() []*orreryv1.UsageSessionTotal {
	return []*orreryv1.UsageSessionTotal{
		{
			Model: usageModelOne, AgentType: usageAgentOne, Requests: 1,
			InputTokens: 9000, OutputTokens: 900, CacheReadTokens: 9000, CacheWriteTokens: 9000,
			BilledCostNanoUsd: 9000, ListCostNanoUsd: 9000,
		},
	}
}

// queryPlanOf returns the detail of each line of the query plan of ledger.SessionTotalsQuery for claudeSession, read
// from the ledger file at ledgerPath.
func queryPlanOf(t *testing.T, ledgerPath, claudeSession string) []string {
	t.Helper()
	db, err := sql.Open("sqlite", ledgerPath)
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close ledger: %v", err)
		}
	}()
	rows, err := db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+ledger.SessionTotalsQuery, claudeSession)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN: %v", err)
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err = rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatalf("scan query plan: %v", err)
		}
		details = append(details, detail)
	}
	if err = rows.Err(); err != nil {
		t.Fatalf("read query plan: %v", err)
	}
	return details
}

// requireIndexedPlan logs each line of plan, and fails the test when a line scans requests in full, or when no line
// searches requests through the requests_claude_session index.
func requireIndexedPlan(t *testing.T, plan []string) {
	t.Helper()
	searched := false
	for _, detail := range plan {
		t.Logf("query plan: %s", detail)
		if strings.HasPrefix(detail, "SCAN requests") || strings.HasPrefix(detail, "SCAN TABLE requests") {
			t.Errorf("query plan scans requests in full: %s", detail)
		}
		if strings.HasPrefix(detail, "SEARCH requests") && strings.Contains(detail, "requests_claude_session") {
			searched = true
		}
	}
	if !searched {
		t.Errorf("query plan does not search requests through requests_claude_session: %v", plan)
	}
}

func TestUsageSessionTotals(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "orrery.db")
	seedUsageSessions(t, storePath)
	ledgerPath := seedUsageLedger(t, storePath)
	client := usageClient(t, server.Config{
		Sessions: store.NewReader(storePath),
		Ledger:   ledger.NewReader(ledgerPath),
	})

	t.Run("totals count each finalized request once by model and agent type", func(t *testing.T) {
		requireTotals(t, sessionTotals(t, client, "orrery-1"), wantTotalsOfA())
	})
	t.Run("another session's totals hold only its own requests", func(t *testing.T) {
		requireTotals(t, sessionTotals(t, client, "orrery-2"), wantTotalsOfB())
	})
	t.Run("a session with no Claude session answers no totals", func(t *testing.T) {
		requireTotals(t, sessionTotals(t, client, "orrery-3"), nil)
	})
	t.Run("a session id no row holds answers not found", func(t *testing.T) {
		expectSessionTotalsCode(t, client, "orrery-9", connect.CodeNotFound)
	})
	t.Run("a nil ledger or sessions reader answers unavailable", func(t *testing.T) {
		noLedger := usageClient(t, server.Config{Sessions: store.NewReader(storePath)})
		expectSessionTotalsCode(t, noLedger, "orrery-1", connect.CodeUnavailable)
		noSessions := usageClient(t, server.Config{Ledger: ledger.NewReader(ledgerPath)})
		expectSessionTotalsCode(t, noSessions, "orrery-1", connect.CodeUnavailable)
	})
	t.Run("query plan searches requests through the session index", func(t *testing.T) {
		requireIndexedPlan(t, queryPlanOf(t, ledgerPath, usageClaudeA))
	})
}
