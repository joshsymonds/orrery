package store

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// recordsSeededTables returns the tables seedRecordsStore writes into. 00107_stage_records.sql adds no column to any of
// them but contract_states, so each reads back as it was.
func recordsSeededTables() []string {
	return []string{"sessions", "contracts", "contract_versions", "contract_states", "designs", "design_questions"}
}

// recordsNewTables returns the tables 00107_stage_records.sql adds.
func recordsNewTables() []string {
	return []string{
		"design_approaches", "design_approach_choices", "design_delegations", "design_conversations", "design_log",
		"design_step_reads", "contract_working", "contract_row_writes", "contract_refusals", "contract_shown",
	}
}

// seedRecordsStore writes the rows a store at version 106 holds for the migration to leave alone: a session, a contract
// with one version and one proposed state, and a design with one question about that contract.
func seedRecordsStore(t *testing.T, s *Store) {
	t.Helper()
	seeds := []string{
		`INSERT INTO sessions (id, server, pane) VALUES ('claude-writer', '', '%2')`,
		`INSERT INTO contracts (id, created) VALUES ('contract-one', 1700000300)`,
		`INSERT INTO contract_versions (contract, version, repository, title, care, body, decisions, open_decisions,
			words, writer, at, changed_rows, changed_sections)
			VALUES ('contract-one', 1, '/work/repo', 'Contract one', 'serious', '{}', '[]', 0, 'Store the contract',
			'claude-writer', 1700000300, '[]', '[]')`,
		`INSERT INTO contract_states (contract, seq, state, version, session, words, at)
			VALUES ('contract-one', 1, 'proposed', 1, 'claude-writer', 'Store the contract', 1700000300)`,
		`INSERT INTO designs (id, title, conversation, created)
			VALUES ('design-one', 'Design one', 'claude-writer', 1700000400)`,
		`INSERT INTO design_questions (design, seq, contract, text, options, recommendation, asker, asked)
			VALUES ('design-one', 1, 'contract-one', 'Which store?', '["sqlite","files"]', 'sqlite', 'claude-writer',
			1700000500)`,
	}
	for _, seed := range seeds {
		if _, err := s.fixtureExec(context.Background(), seed); err != nil {
			t.Fatalf("seed a row: %v", err)
		}
	}
}

// recordsStoreUp returns a store at version 106 holding the seeded rows, with 00107 applied alone, and the rows of the
// seeded tables as they read before the migration. Each subtest of TestRecordsSchema takes its own store, so the
// subtests do not depend on one another's writes.
func recordsStoreUp(t *testing.T) (*Store, map[string][][]any) {
	t.Helper()
	ctx := context.Background()
	s, provider := storeAtVersion(t, 106)
	seedRecordsStore(t, s)
	before := map[string][][]any{}
	for _, table := range recordsSeededTables() {
		before[table] = rowsOf(t, s.db.DB, table)
	}
	names := tableNames(t, s)
	for _, table := range recordsNewTables() {
		if names[table] {
			t.Fatalf("table %s present at version 106, want absent", table)
		}
	}

	result, err := provider.UpByOne(ctx)
	if err != nil {
		t.Fatalf("Up of 00107: %v", err)
	}
	if result.Source.Version != 107 {
		t.Fatalf("Up applied version %d, want 107", result.Source.Version)
	}
	return s, before
}

// TestRecordsSchema checks 00107_stage_records.sql, which adds the stage's record tables and the maker columns of
// contract_states. It takes a store at version 106 holding a contract with a state row and a design with a question, and
// applies 00107 alone. The rows stored before it read back unchanged, its new state columns read empty, each table it
// creates round-trips a row, and its CHECKs refuse an unknown row action and an unknown maker.
func TestRecordsSchema(t *testing.T) {
	t.Parallel()
	t.Run("rows stored before 00107 read back unchanged", func(t *testing.T) {
		t.Parallel()
		s, before := recordsStoreUp(t)
		assertSeededRowsUnchanged(t, s, before)
	})
	t.Run("the new contract_states columns read empty", func(t *testing.T) {
		t.Parallel()
		s, _ := recordsStoreUp(t)
		assertNewStateColumnsEmpty(t, s)
	})
	t.Run("each table 00107 creates round-trips a row", func(t *testing.T) {
		t.Parallel()
		s, _ := recordsStoreUp(t)
		roundTripApproaches(t, s)
		roundTripDesign(t, s)
		roundTripContract(t, s)
		roundTripContractWrites(t, s)
	})
	t.Run("the state maker query writes the maker columns of one state", func(t *testing.T) {
		t.Parallel()
		s, _ := recordsStoreUp(t)
		assertStateMakerWritten(t, s)
	})
	t.Run("the CHECK refuses an unknown row action", func(t *testing.T) {
		t.Parallel()
		s, _ := recordsStoreUp(t)
		assertActionRefused(t, s)
	})
	t.Run("the CHECK refuses an unknown maker", func(t *testing.T) {
		t.Parallel()
		s, _ := recordsStoreUp(t)
		assertMakerRefused(t, s)
	})
}

// assertSeededRowsUnchanged checks that the seeded tables read back as they were before 00107. contract_states is
// compared over the columns it had, since the migration adds its maker columns at its end.
func assertSeededRowsUnchanged(t *testing.T, s *Store, before map[string][][]any) {
	t.Helper()
	for _, table := range recordsSeededTables() {
		got := rowsOf(t, s.db.DB, table)
		if table == "contract_states" {
			got = leadingColumns(got, before[table])
		}
		if !reflect.DeepEqual(got, before[table]) {
			t.Errorf("%s after Up = %v, want %v", table, got, before[table])
		}
	}
}

// assertNewStateColumnsEmpty checks that the seeded state reads empty in each maker column the migration adds.
func assertNewStateColumnsEmpty(t *testing.T, s *Store) {
	t.Helper()
	var maker, quote, makerReason, delegation string
	err := s.db.QueryRowContext(context.Background(),
		`SELECT maker, quote, maker_reason, delegation FROM contract_states WHERE contract = ? AND seq = ?`,
		"contract-one", 1).Scan(&maker, &quote, &makerReason, &delegation)
	if err != nil {
		t.Fatalf("read the maker columns of the seeded state: %v", err)
	}
	if got := []string{maker, quote, makerReason, delegation}; !reflect.DeepEqual(got, []string{"", "", "", ""}) {
		t.Fatalf("maker columns of the seeded state = %q, want all empty", got)
	}
}

// checkRoundTrip runs a subtest named for table: insert writes want, and list reads the table back as exactly want.
func checkRoundTrip[T any](t *testing.T, table string, insert func() error, list func() ([]T, error), want T) {
	t.Helper()
	t.Run(table, func(t *testing.T) {
		if err := insert(); err != nil {
			t.Fatalf("insert into %s: %v", table, err)
		}
		got, err := list()
		if err != nil {
			t.Fatalf("list %s: %v", table, err)
		}
		if !reflect.DeepEqual(got, []T{want}) {
			t.Fatalf("%s read back %+v, want %+v", table, got, []T{want})
		}
	})
}

// roundTripApproaches checks design_approaches and design_approach_choices. Each insert takes its row's model, whose
// fields are the insert's parameters in column order.
func roundTripApproaches(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	q := s.fixtureQueries()
	approach := sqlcgen.DesignApproach{
		Design: "design-one", Seq: 1, Name: "Thin store", Shape: "one table", Cost: "low", Wins: "simple",
		Loses: "slow", Reconsider: "if reads grow", Research: `["step-one"]`, Recommended: 1, Reason: "fits the stage",
		Session: "claude-writer", At: 1700000600,
	}
	checkRoundTrip(t, "design_approaches",
		func() error { return q.InsertDesignApproach(ctx, sqlcgen.InsertDesignApproachParams(approach)) },
		func() ([]sqlcgen.DesignApproach, error) { return q.ListDesignApproaches(ctx, "design-one") },
		approach)

	choice := sqlcgen.DesignApproachChoice{
		Design: "design-one", Seq: 1, Approach: 1, Words: "Take the thin store", Maker: StageMakerOwnerServe,
		Quote: "thin", MakerReason: "simplest", Session: "claude-writer", At: 1700000700,
	}
	checkRoundTrip(t, "design_approach_choices",
		func() error {
			return q.InsertDesignApproachChoice(ctx, sqlcgen.InsertDesignApproachChoiceParams(choice))
		},
		func() ([]sqlcgen.DesignApproachChoice, error) { return q.ListDesignApproachChoices(ctx, "design-one") },
		choice)
}

// roundTripDesign checks design_delegations, design_conversations and design_log.
func roundTripDesign(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	q := s.fixtureQueries()
	delegation := sqlcgen.DesignDelegation{
		Design: "design-one", Seq: 1, Kinds: `["approach"]`, Message: "Decide the store", Session: "claude-writer",
		At: 1700000800,
	}
	checkRoundTrip(t, "design_delegations",
		func() error { return q.InsertDesignDelegation(ctx, sqlcgen.InsertDesignDelegationParams(delegation)) },
		func() ([]sqlcgen.DesignDelegation, error) { return q.ListDesignDelegations(ctx, "design-one") },
		delegation)

	conversation := sqlcgen.DesignConversation{
		Design: "design-one", Seq: 1, Session: "claude-writer", Previous: "", At: 1700000900,
	}
	checkRoundTrip(t, "design_conversations",
		func() error {
			return q.InsertDesignConversation(ctx, sqlcgen.InsertDesignConversationParams(conversation))
		},
		func() ([]sqlcgen.DesignConversation, error) { return q.ListDesignConversations(ctx, "design-one") },
		conversation)

	line := sqlcgen.DesignLog{
		Design: "design-one", Seq: 1, At: 1700001000, Maker: LogMakerOrreryd, Session: "claude-writer",
		What: "closed", Ids: `["design-one"]`, Words: "Logged",
	}
	checkRoundTrip(t, "design_log",
		func() error { return q.InsertDesignLogLine(ctx, sqlcgen.InsertDesignLogLineParams(line)) },
		func() ([]sqlcgen.DesignLog, error) { return q.ListDesignLog(ctx, "design-one") },
		line)
}

// roundTripContract checks design_step_reads and contract_working. A second write of contract_working over version 2
// updates the row and keeps the time it was created.
func roundTripContract(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	q := s.fixtureQueries()
	stepRead := sqlcgen.DesignStepRead{Step: "step-one", Contract: "contract-one", Version: 0}
	checkRoundTrip(t, "design_step_reads",
		func() error { return q.InsertContractStepRead(ctx, sqlcgen.InsertContractStepReadParams(stepRead)) },
		func() ([]sqlcgen.DesignStepRead, error) { return q.ListContractStepReads(ctx, "contract-one") },
		stepRead)

	working := sqlcgen.ContractWorking{
		Contract: "contract-one", Version: 1, Body: "{}", Decisions: "[]", Writer: "claude-writer",
		Created: 1700001100, Updated: 1700001100,
	}
	t.Run("contract_working", func(t *testing.T) {
		if err := q.UpsertContractWorking(ctx, sqlcgen.UpsertContractWorkingParams(working)); err != nil {
			t.Fatalf("upsert contract_working: %v", err)
		}
		again := sqlcgen.ContractWorking{
			Contract: "contract-one", Version: 2, Body: `{"title":"Contract one"}`, Decisions: "[]",
			Writer: "claude-writer", Created: 1700009999, Updated: 1700001200,
		}
		if err := q.UpsertContractWorking(ctx, sqlcgen.UpsertContractWorkingParams(again)); err != nil {
			t.Fatalf("upsert contract_working over version 2: %v", err)
		}
		got, err := q.GetContractWorking(ctx, "contract-one")
		if err != nil {
			t.Fatalf("get contract_working: %v", err)
		}
		want := again
		want.Created = working.Created
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("contract_working read back %+v, want %+v", got, want)
		}
	})
}

// roundTripContractWrites checks contract_row_writes, contract_refusals and contract_shown.
func roundTripContractWrites(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	q := s.fixtureQueries()
	write := sqlcgen.ContractRowWrite{
		Contract: "contract-one", Seq: 1, Version: 0, Row: "R1", Action: RowActionWrite, Depends: "R0",
		Words: "Store the rows", Session: "claude-writer", At: 1700001300,
	}
	checkRoundTrip(t, "contract_row_writes",
		func() error { return q.InsertContractRowWrite(ctx, sqlcgen.InsertContractRowWriteParams(write)) },
		func() ([]sqlcgen.ContractRowWrite, error) { return q.ListContractRowWrites(ctx, "contract-one") },
		write)

	refusal := sqlcgen.ContractRefusal{
		Contract: "contract-one", Seq: 1, Version: 2, Field: "scope", Text: "too broad", Session: "claude-writer",
		At: 1700001400,
	}
	checkRoundTrip(t, "contract_refusals",
		func() error { return q.InsertContractRefusal(ctx, sqlcgen.InsertContractRefusalParams(refusal)) },
		func() ([]sqlcgen.ContractRefusal, error) { return q.ListContractRefusals(ctx, "contract-one") },
		refusal)

	shown := sqlcgen.ContractShown{Contract: "contract-one", Version: 1, Row: "R1", At: 1700001500}
	checkRoundTrip(t, "contract_shown",
		func() error { return q.InsertContractShown(ctx, sqlcgen.InsertContractShownParams(shown)) },
		func() ([]sqlcgen.ContractShown, error) { return q.ListContractShown(ctx, "contract-one") },
		shown)
}

// assertStateMakerWritten checks that SetContractStateMaker writes the four maker columns of one state and leaves the
// rest of the state as it was.
func assertStateMakerWritten(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	q := s.fixtureQueries()
	err := q.SetContractStateMaker(ctx, sqlcgen.SetContractStateMakerParams{
		Maker: StageMakerOwnerServe, Quote: "quoted", MakerReason: "why", Delegation: "delegation-one",
		Contract: "contract-one", Seq: 1,
	})
	if err != nil {
		t.Fatalf("set the maker of the seeded state: %v", err)
	}
	states, err := q.ListContractStates(ctx, "contract-one")
	if err != nil {
		t.Fatalf("list states: %v", err)
	}
	want := []sqlcgen.ContractState{{
		Contract: "contract-one", Seq: 1, State: "proposed", Version: 1, Session: "claude-writer",
		Words: "Store the contract", At: 1700000300, Maker: StageMakerOwnerServe, Quote: "quoted", MakerReason: "why",
		Delegation: "delegation-one",
	}}
	if !reflect.DeepEqual(states, want) {
		t.Fatalf("states after the maker write = %+v, want %+v", states, want)
	}
}

// assertActionRefused checks that a row write naming an action no CHECK allows is refused.
func assertActionRefused(t *testing.T, s *Store) {
	t.Helper()
	write := sqlcgen.ContractRowWrite{
		Contract: "contract-one", Seq: 2, Row: "R2", Action: "bogus", Session: "claude-writer", At: 1700001600,
	}
	err := s.fixtureQueries().InsertContractRowWrite(context.Background(),
		sqlcgen.InsertContractRowWriteParams(write))
	assertCheckRefused(t, err)
}

// assertMakerRefused checks that a state's maker no CHECK allows is refused, on the seeded state the migration altered.
func assertMakerRefused(t *testing.T, s *Store) {
	t.Helper()
	err := s.fixtureQueries().SetContractStateMaker(context.Background(), sqlcgen.SetContractStateMakerParams{
		Maker: "bogus", Contract: "contract-one", Seq: 1,
	})
	assertCheckRefused(t, err)
}

// assertCheckRefused checks that err is the refusal of a CHECK constraint.
func assertCheckRefused(t *testing.T, err error) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "CHECK constraint failed") {
		t.Fatalf("write error = %v, want a CHECK constraint failure", err)
	}
}
