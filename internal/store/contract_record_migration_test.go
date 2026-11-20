package store

import (
	"context"
	"reflect"
	"testing"
)

// TestContractRecordMigrationStepsDownAndUp checks the migration that adds the contract record's three tables. It seeds
// version 81 with a session, the seeded epic with its decision and its failure, requirement and premise sections, its
// epic_sessions row and a caller_events row. The Up
// adds the three tables empty, and rows written into them under the migration are gone after the Down removes the
// tables. The seeded rows read back unchanged after the Up, the Down, and the second Up, which starts the tables empty.
func TestContractRecordMigrationStepsDownAndUp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, provider := storeAtVersion(t, 81)
	seedEpic(t, s.db.DB)
	if _, err := s.fixtureExec(ctx, `INSERT INTO epic_failures (epic, position, id, text, rating, response)
		VALUES (?, 0, 'F1', 'The store loses a row', 'serious', 'prevent: R1')`, seededEpic); err != nil {
		t.Fatalf("insert failure: %v", err)
	}
	if _, err := s.fixtureExec(ctx, `INSERT INTO epic_requirements (epic, position, id, text, evidence, kind, item)
		VALUES (?, 0, 'R1', 'Rows read back', '', '', '{"conclusion":"rows read back"}')`, seededEpic); err != nil {
		t.Fatalf("insert requirement: %v", err)
	}
	if _, err := s.fixtureExec(ctx, `INSERT INTO epic_premises (epic, position, id, text, if_wrong)
		VALUES (?, 0, 'P1', 'The store is open', 'survives')`, seededEpic); err != nil {
		t.Fatalf("insert premise: %v", err)
	}
	if _, err := s.fixtureExec(ctx,
		`INSERT INTO sessions (id, server, pane) VALUES ('claude-writer', '', '%2')`); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	if _, err := s.fixtureExec(ctx, `INSERT INTO epic_sessions (epic, session, call, at, creator)
		VALUES (?, 'claude-writer', 'epic_create', 1700000100, 1)`, seededEpic); err != nil {
		t.Fatalf("insert epic session: %v", err)
	}
	if _, err := s.fixtureExec(ctx, `INSERT INTO caller_events (event, session, at)
		VALUES (?, 'claude-writer', 1700000200)`, "epic:"+seededEpic+":Q1"); err != nil {
		t.Fatalf("insert caller event: %v", err)
	}
	seededTables := []string{
		"sessions",
		"epics",
		"epic_decisions",
		"epic_failures",
		"epic_requirements",
		"epic_premises",
		"epic_sessions",
		"caller_events",
	}
	readSeeded := func() map[string][][]any {
		t.Helper()
		rows := map[string][][]any{}
		for _, table := range seededTables {
			rows[table] = rowsOf(t, s.db.DB, table)
		}
		return rows
	}
	seeded := readSeeded()
	contractTables := []string{"contracts", "contract_versions", "contract_states"}
	// assertContractTables checks that the three tables are present and empty, or absent when want is false.
	assertContractTables := func(when string, want bool) {
		t.Helper()
		names := tableNames(t, s)
		for _, table := range contractTables {
			if names[table] != want {
				t.Fatalf("%s: table %s present = %t, want %t", when, table, names[table], want)
			}
			if want {
				if got := rowsOf(t, s.db.DB, table); len(got) != 0 {
					t.Fatalf("%s: %s = %v, want none", when, table, got)
				}
			}
		}
	}
	assertSeeded := func(when string) {
		t.Helper()
		if got := readSeeded(); !reflect.DeepEqual(got, seeded) {
			t.Fatalf("seeded rows %s = %v, want %v", when, got, seeded)
		}
	}

	if _, err := provider.UpByOne(ctx); err != nil {
		t.Fatalf("Up of 00082: %v", err)
	}
	assertContractTables("after Up", true)
	assertSeeded("after Up")

	if _, err := s.fixtureExec(ctx,
		`INSERT INTO contracts (id, created) VALUES ('contract-one', 1700000300)`); err != nil {
		t.Fatalf("insert contract: %v", err)
	}
	if _, err := s.fixtureExec(ctx, `INSERT INTO contract_versions
		(contract, version, repository, title, care, body, decisions, open_decisions, words, writer, at,
		changed_rows, changed_sections)
		VALUES ('contract-one', 1, '/work/repo', 'Contract one', 'serious', '{}', '[]', 0, 'Store the contract',
		'claude-writer', 1700000300, '[]', '[]')`); err != nil {
		t.Fatalf("insert contract version: %v", err)
	}
	if _, err := s.fixtureExec(ctx, `INSERT INTO contract_states
		(contract, seq, state, version, session, words, at)
		VALUES ('contract-one', 1, 'proposed', 1, 'claude-writer', 'Store the contract', 1700000300)`); err != nil {
		t.Fatalf("insert contract state: %v", err)
	}

	if _, err := provider.Down(ctx); err != nil {
		t.Fatalf("Down of 00082: %v", err)
	}
	assertContractTables("after Down", false)
	assertSeeded("after Down")

	if _, err := provider.UpByOne(ctx); err != nil {
		t.Fatalf("Up of 00082 again: %v", err)
	}
	assertContractTables("after second Up", true)
	assertSeeded("after second Up")
}
