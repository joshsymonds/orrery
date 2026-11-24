package server_test

import (
	"database/sql"
	"path/filepath"
	"testing"

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
	// usageEpicSteered is steered by usageSteerer. Its steps are usageStepA, usageStepB and usageStepNoClaude, and
	// usageReader and usageStray are linked to it by epic_sessions rows that name no step.
	usageEpicSteered = "orrery-5e6f7a8/epic-steered"
	// usageEpicOther is steered by usageSteerer too, and its only step is usageOtherStep.
	usageEpicOther = "orrery-5e6f7a8/epic-other"
	// usageEpicRecord has no epics row, so it is a record epic. Its only step is usageRecordStep, called by
	// usageRecordCaller.
	usageEpicRecord = "orrery-5e6f7a8/epic-record"
	// usageEpicEmpty has an epics row with no steerer and no steps.
	usageEpicEmpty = "orrery-5e6f7a8/epic-empty"
	// usageEpicUnknown has no row at all.
	usageEpicUnknown = "orrery-5e6f7a8/epic-unknown"
)

// The orrery sessions the epic tests seed. usageStepNoClaude is a step with no Claude session, and usageReader is
// linked to usageEpicSteered only by an epic_get read.
const (
	usageSteerer      = "epic-steerer"
	usageStepA        = "epic-step-a"
	usageStepB        = "epic-step-b"
	usageStepNoClaude = "epic-step-c"
	usageReader       = "epic-reader"
	usageStray        = "epic-stray"
	usageHeadless     = "headless-1"
	usageOtherStep    = "other-step"
	usageRecordStep   = "record-step"
	usageRecordCaller = "record-caller"
)

// The Claude sessions of the orrery sessions above. usageStepNoClaude has none, so its ledger row has none either.
const (
	usageClaudeSteerer   = "synthetic-claude-steerer"
	usageClaudeStepA     = "synthetic-claude-step-a"
	usageClaudeStepB     = "synthetic-claude-step-b"
	usageClaudeReader    = "synthetic-claude-reader"
	usageClaudeStray     = "synthetic-claude-stray"
	usageClaudeHeadless  = "synthetic-claude-headless"
	usageClaudeOther     = "synthetic-claude-other-step"
	usageClaudeRecord    = "synthetic-claude-record-step"
	usageClaudeRecordCal = "synthetic-claude-record-caller"
)

// execEpicRow runs one insert of an invented row on raw, and fails the test on error.
func execEpicRow(t *testing.T, raw *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := raw.Exec(query, args...); err != nil {
		t.Fatalf("insert row: %v", err)
	}
}

// seedEpicStore writes the sessions, epics, steps and epic_sessions rows the epic tests read into the store at storePath.
func seedEpicStore(t *testing.T, storePath string) {
	t.Helper()
	storetest.Seed(t, storePath)
	raw, err := sql.Open("sqlite3", storePath)
	if err != nil {
		t.Fatalf("open raw handle: %v", err)
	}
	t.Cleanup(func() {
		if err := raw.Close(); err != nil {
			t.Errorf("close raw handle: %v", err)
		}
	})
	sessions := []struct{ id, kind, claude string }{
		{usageSteerer, "", usageClaudeSteerer},
		{usageStepA, store.KindStep, usageClaudeStepA},
		{usageStepB, store.KindStep, usageClaudeStepB},
		{usageStepNoClaude, store.KindStep, ""},
		{usageReader, "", usageClaudeReader},
		{usageStray, "", usageClaudeStray},
		{usageHeadless, store.KindEngine, usageClaudeHeadless},
		{usageOtherStep, store.KindStep, usageClaudeOther},
		{usageRecordStep, store.KindStep, usageClaudeRecord},
		{usageRecordCaller, "", usageClaudeRecordCal},
	}
	for _, s := range sessions {
		execEpicRow(t, raw, `INSERT INTO sessions (id, server, pane, kind, claude_session) VALUES (?, '', '', ?, ?)`,
			s.id, s.kind, s.claude)
	}
	epics := []struct{ id, steerer string }{
		{usageEpicSteered, usageSteerer},
		{usageEpicOther, usageSteerer},
		{usageEpicEmpty, ""},
	}
	for _, e := range epics {
		execEpicRow(t, raw, `INSERT INTO epics (id, repository, title, approved, intent, care, approach, release_then,
			checks, phase, created, changed, steerer)
			VALUES (?, '/work/repo', 'Epic', 'approved', 'intent', 'serious', 'approach', 'then', 'checks', 'design',
			1700000000, 1700000000, ?)`, e.id, e.steerer)
	}
	steps := []struct{ id, epic, caller string }{
		{usageStepA, usageEpicSteered, usageSteerer},
		{usageStepB, usageEpicSteered, usageSteerer},
		{usageStepNoClaude, usageEpicSteered, ""},
		{usageOtherStep, usageEpicOther, ""},
		{usageRecordStep, usageEpicRecord, usageRecordCaller},
	}
	for _, s := range steps {
		execEpicRow(t, raw, `INSERT INTO steps (id, agent, prompt, output_schema, workspace, revision, worktree,
			done_value, read_only, owned, gate, gate_limit, started, checks, result, status, end_value, reason, ended,
			epic, caller)
			VALUES (?, 'haiku', 'Do the task', '{}', '/work/ws', 'abc1234', '/work/wt', 'done', 0, '[]', '[]', 0,
			1700000000, '[]', '{}', 'passed', '', '', 1700000600, ?, ?)`, s.id, s.epic, s.caller)
	}
	calls := []struct{ epic, session, call string }{
		{usageEpicSteered, usageStepA, "epic_create"},
		{usageEpicSteered, usageReader, "epic_get"},
		{usageEpicSteered, usageStray, "epic_kick"},
	}
	for _, c := range calls {
		execEpicRow(t, raw, `INSERT INTO epic_sessions (epic, session, call, at) VALUES (?, ?, ?, 1700000100)`,
			c.epic, c.session, c.call)
	}
}

// usageEpicSingle is the finish of a request that completed with n in every token count and in both costs, so the
// one total it makes counts n in each of those fields.
func usageEpicSingle(n int64) ledger.Finish {
	return ledger.Finish{
		CompletedAt: usageDone, ResolvedModel: usageModelOne,
		InputTokens: n, OutputTokens: n, CacheReadTokens: n, CacheWriteTokens: n,
		BilledCostNanoUSD: new(n), ListCostNanoUSD: new(n),
	}
}

// seedEpicLedger writes the ledger the epic tests read beside the store at storePath, and returns its path. The
// sessions of usageEpicSteered each have requests, and one of usageStepA's is still in flight. A request with no
// Claude session is in the ledger too, so a read that passed "" would find it.
func seedEpicLedger(t *testing.T, storePath string) string {
	t.Helper()
	path := ledger.Path(storePath)
	l, err := ledger.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("ledger.Open: %v", err)
	}
	finishRequest(t, l, usageStart(usageClaudeSteerer, usageAgentOne), usageEpicSingle(10))
	finishRequest(t, l, usageStart(usageClaudeStepA, usageAgentOne), ledger.Finish{
		CompletedAt: usageDone, ResolvedModel: usageModelOne,
		InputTokens: 11, OutputTokens: 1, CacheReadTokens: 100, CacheWriteTokens: 10,
		BilledCostNanoUSD: new(int64(5)), ListCostNanoUSD: new(int64(9)),
	})
	finishRequest(t, l, usageStart(usageClaudeStepA, usageAgentOne), ledger.Finish{
		CompletedAt: usageDone, ResolvedModel: usageModelOne,
		InputTokens: 22, OutputTokens: 2, CacheReadTokens: 200, CacheWriteTokens: 20,
		BilledCostNanoUSD: new(int64(7)), ListCostNanoUSD: new(int64(11)),
	})
	finishRequest(t, l, usageStart(usageClaudeStepA, usageAgentTwo), ledger.Finish{
		CompletedAt: usageDone, ResolvedModel: usageModelTwo,
		InputTokens: 4, OutputTokens: 4,
		BilledCostNanoUSD: nil, ListCostNanoUSD: new(int64(40)),
	})
	startRequest(t, l, usageStart(usageClaudeStepA, usageAgentOne))
	finishRequest(t, l, usageStart(usageClaudeStepB, usageAgentOne), usageEpicSingle(30))
	finishRequest(t, l, usageStart(usageClaudeHeadless, usageAgentTwo), usageEpicSingle(20))
	finishRequest(t, l, usageStart(usageClaudeReader, usageAgentOne), usageEpicSingle(40))
	finishRequest(t, l, usageStart(usageClaudeStray, usageAgentOne), usageEpicSingle(50))
	finishRequest(t, l, usageStart(usageClaudeOther, usageAgentOne), usageEpicSingle(60))
	finishRequest(t, l, usageStart(usageClaudeRecord, usageAgentOne), usageEpicSingle(70))
	finishRequest(t, l, usageStart(usageClaudeRecordCal, usageAgentOne), usageEpicSingle(80))
	finishRequest(t, l, usageStart("", usageAgentOne), usageEpicSingle(90))
	if err = l.Close(); err != nil {
		t.Fatalf("Close ledger: %v", err)
	}
	return path
}

// wantUsageEpicSingle returns the one total of a request of usageModelOne made as agentType, finished by usageEpicSingle(n).
func wantUsageEpicSingle(agentType string, n int64) []*orreryv1.UsageSessionTotal {
	return []*orreryv1.UsageSessionTotal{{
		Model: usageModelOne, AgentType: agentType, Requests: 1,
		InputTokens: n, OutputTokens: n, CacheReadTokens: n, CacheWriteTokens: n,
		BilledCostNanoUsd: n, ListCostNanoUsd: n,
	}}
}

// wantUsageEpicStepA are the totals of usageStepA. Its in-flight request is not among them, and its model-two request
// has an unknown billed cost, so that sum is 0.
func wantUsageEpicStepA() []*orreryv1.UsageSessionTotal {
	return []*orreryv1.UsageSessionTotal{
		{
			Model: usageModelOne, AgentType: usageAgentOne, Requests: 2,
			InputTokens: 33, OutputTokens: 3, CacheReadTokens: 300, CacheWriteTokens: 30,
			BilledCostNanoUsd: 12, ListCostNanoUsd: 20,
		},
		{
			Model: usageModelTwo, AgentType: usageAgentTwo, Requests: 1,
			InputTokens: 4, OutputTokens: 4, ListCostNanoUsd: 40,
		},
	}
}

// wantUsageEpicSessions returns the sessions the Usage API answers for the epic. Each session is listed by its id, and
// the steerer is marked shared.
func wantUsageEpicSessions(epic string) []*orreryv1.UsageEpicSession {
	switch epic {
	case usageEpicSteered:
		return []*orreryv1.UsageEpicSession{
			{Session: usageSteerer, Shared: true, Totals: wantUsageEpicSingle(usageAgentOne, 10)},
			{Session: usageStepA, Totals: wantUsageEpicStepA()},
			{Session: usageStepB, Totals: wantUsageEpicSingle(usageAgentOne, 30)},
		}
	case usageEpicOther:
		return []*orreryv1.UsageEpicSession{
			{Session: usageSteerer, Shared: true, Totals: wantUsageEpicSingle(usageAgentOne, 10)},
			{Session: usageOtherStep, Totals: wantUsageEpicSingle(usageAgentOne, 60)},
		}
	case usageEpicRecord:
		return []*orreryv1.UsageEpicSession{
			{Session: usageRecordCaller, Shared: true, Totals: wantUsageEpicSingle(usageAgentOne, 80)},
			{Session: usageRecordStep, Totals: wantUsageEpicSingle(usageAgentOne, 70)},
		}
	}
	return nil
}

// epicTotals returns the sessions the Usage API answers for the epic id. It fails the test on any error.
func epicTotals(t *testing.T, client orreryv1connect.UsageClient, epic string) []*orreryv1.UsageEpicSession {
	t.Helper()
	req := connect.NewRequest(&orreryv1.UsageEpicTotalsRequest{Epic: epic})
	resp, err := client.EpicTotals(t.Context(), req)
	if err != nil {
		t.Fatalf("EpicTotals(%s): %v", epic, err)
	}
	return resp.Msg.GetSessions()
}

// requireEpicSessions fails the test unless got holds the sessions of want, in order.
func requireEpicSessions(t *testing.T, got, want []*orreryv1.UsageEpicSession) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("sessions: got %d, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if !proto.Equal(got[i], want[i]) {
			t.Errorf("sessions[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

// requireEpicSessionAbsent fails the test if got lists the orrery session id.
func requireEpicSessionAbsent(t *testing.T, got []*orreryv1.UsageEpicSession, id string) {
	t.Helper()
	for _, session := range got {
		if session.GetSession() == id {
			t.Errorf("sessions list %s, want it absent: %v", id, got)
		}
	}
}

// expectEpicTotalsCode fails the test unless the Usage API fails the totals of the epic with want.
func expectEpicTotalsCode(t *testing.T, client orreryv1connect.UsageClient, epic string, want connect.Code) {
	t.Helper()
	req := connect.NewRequest(&orreryv1.UsageEpicTotalsRequest{Epic: epic})
	_, err := client.EpicTotals(t.Context(), req)
	if err == nil {
		t.Fatalf("EpicTotals(%s) succeeded, want code %v", epic, want)
	}
	if got := connect.CodeOf(err); got != want {
		t.Fatalf("EpicTotals(%s) code: got %v (%v), want %v", epic, got, err, want)
	}
}

// epicQueryPlanOf returns the detail of each line of the query plan of ledger.EpicTotalsQuery for claudeSessions, read
// from the ledger file at ledgerPath.
func epicQueryPlanOf(t *testing.T, ledgerPath string, claudeSessions []string) []string {
	t.Helper()
	db, err := sql.Open("sqlite3", ledgerPath)
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close ledger: %v", err)
		}
	}()
	args := make([]any, len(claudeSessions))
	for i, id := range claudeSessions {
		args[i] = id
	}
	rows, err := db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+ledger.EpicTotalsQuery(len(claudeSessions)), args...)
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

func TestUsageEpicTotals(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "orrery.db")
	seedEpicStore(t, storePath)
	ledgerPath := seedEpicLedger(t, storePath)
	client := usageClient(t, server.Config{
		Sessions: store.NewReader(storePath),
		Ledger:   ledger.NewReader(ledgerPath),
	})

	t.Run("SessionTotals answers a headless and a step session by id", func(t *testing.T) {
		requireTotals(t, sessionTotals(t, client, usageHeadless), wantUsageEpicSingle(usageAgentTwo, 20))
		requireTotals(t, sessionTotals(t, client, usageStepA), wantUsageEpicStepA())
	})
	t.Run("EpicTotals answers each step session once with its own rows, leaving out a step with no Claude session", func(t *testing.T) {
		requireEpicSessions(t, epicTotals(t, client, usageEpicSteered), wantUsageEpicSessions(usageEpicSteered))
	})
	t.Run("the steerer is its own row marked shared in both epics it steers", func(t *testing.T) {
		requireEpicSessions(t, epicTotals(t, client, usageEpicOther), wantUsageEpicSessions(usageEpicOther))
	})
	t.Run("the read-only session and every other epic_sessions row are absent", func(t *testing.T) {
		got := epicTotals(t, client, usageEpicSteered)
		requireEpicSessionAbsent(t, got, usageReader)
		requireEpicSessionAbsent(t, got, usageStray)
	})
	t.Run("the other epic's sessions are absent", func(t *testing.T) {
		requireEpicSessionAbsent(t, epicTotals(t, client, usageEpicSteered), usageOtherStep)
	})
	t.Run("a record epic answers its steps and their shared caller on the same rule", func(t *testing.T) {
		requireEpicSessions(t, epicTotals(t, client, usageEpicRecord), wantUsageEpicSessions(usageEpicRecord))
	})
	t.Run("an epic with no sessions and an absent ledger answer empty", func(t *testing.T) {
		requireEpicSessions(t, epicTotals(t, client, usageEpicEmpty), nil)
		requireEpicSessions(t, epicTotals(t, client, usageEpicUnknown), nil)
		absent := usageClient(t, server.Config{
			Sessions: store.NewReader(storePath),
			Ledger:   ledger.NewReader(filepath.Join(t.TempDir(), "no-ledger.db")),
		})
		requireEpicSessions(t, epicTotals(t, absent, usageEpicSteered), nil)
	})
	t.Run("a nil ledger or sessions reader answers unavailable", func(t *testing.T) {
		noLedger := usageClient(t, server.Config{Sessions: store.NewReader(storePath)})
		expectEpicTotalsCode(t, noLedger, usageEpicSteered, connect.CodeUnavailable)
		noSessions := usageClient(t, server.Config{Ledger: ledger.NewReader(ledgerPath)})
		expectEpicTotalsCode(t, noSessions, usageEpicSteered, connect.CodeUnavailable)
	})
	t.Run("query plan searches requests through the session index", func(t *testing.T) {
		requireIndexedPlan(t, epicQueryPlanOf(t, ledgerPath, []string{usageClaudeSteerer, usageClaudeStepA, usageClaudeStepB}))
	})
}
