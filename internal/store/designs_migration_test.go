package store

import (
	"context"
	"reflect"
	"testing"
)

// designTables returns the tables 00105_designs.sql adds.
func designTables() []string {
	return []string{"designs", "design_contracts", "design_edges"}
}

// assertDesignTables checks that each design table is present and empty when want is true, or absent when want is false.
func assertDesignTables(t *testing.T, s *Store, when string, want bool) {
	t.Helper()
	names := tableNames(t, s)
	for _, table := range designTables() {
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

// columnLists returns the column names of each table of s in column order, read through pragma table_info.
func columnLists(t *testing.T, s *Store) map[string][]string {
	t.Helper()
	lists := map[string][]string{}
	for table := range tableNames(t, s) {
		lists[table] = columnsOf(t, s, table)
	}
	return lists
}

// TestDesignMigrationOnlyAdds checks 00105_designs.sql, which adds the designs, design_contracts and design_edges
// tables. It opens a store at version 104, before the migration, and steps the migration Up, Down and Up again. After
// each Up the design tables are present and empty, and after the Down they are gone. The column lists of every table
// present at 104 read the same before and after each step, so the migration only adds.
func TestDesignMigrationOnlyAdds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, provider := storeAtVersion(t, 104)
	assertDesignTables(t, s, "at 104", false)
	before := columnLists(t, s)
	// assertColumns checks that the tables other than the design tables read the same as before.
	assertColumns := func(when string) {
		t.Helper()
		got := columnLists(t, s)
		for _, table := range designTables() {
			delete(got, table)
		}
		if !reflect.DeepEqual(got, before) {
			t.Fatalf("column lists %s = %v, want %v", when, got, before)
		}
	}

	if _, err := provider.UpByOne(ctx); err != nil {
		t.Fatalf("Up of 00105: %v", err)
	}
	assertDesignTables(t, s, "after Up", true)
	assertColumns("after Up")

	if _, err := provider.Down(ctx); err != nil {
		t.Fatalf("Down of 00105: %v", err)
	}
	assertDesignTables(t, s, "after Down", false)
	assertColumns("after Down")

	if _, err := provider.UpByOne(ctx); err != nil {
		t.Fatalf("Up of 00105 again: %v", err)
	}
	assertDesignTables(t, s, "after second Up", true)
	assertColumns("after second Up")
}

// TestDesignMigrationKeepsRows checks that 00105_designs.sql leaves the rows of the tables at 104 as they were. It seeds
// version 104 with a session, a contract with a version and a state, and the seeded epic with its decision. The Up adds
// the design tables empty, and a design row written into them under the migration is gone after the Down removes the
// tables. The seeded rows read back unchanged after the Up, the Down and the second Up.
func TestDesignMigrationKeepsRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, provider := storeAtVersion(t, 104)
	seedEpic(t, s.db.DB)
	if _, err := s.fixtureExec(ctx,
		`INSERT INTO sessions (id, server, pane) VALUES ('claude-writer', '', '%2')`); err != nil {
		t.Fatalf("insert session: %v", err)
	}
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
	seededTables := []string{
		"sessions",
		"epics",
		"epic_decisions",
		"contracts",
		"contract_versions",
		"contract_states",
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
	assertSeeded := func(when string) {
		t.Helper()
		if got := readSeeded(); !reflect.DeepEqual(got, seeded) {
			t.Fatalf("seeded rows %s = %v, want %v", when, got, seeded)
		}
	}

	if _, err := provider.UpByOne(ctx); err != nil {
		t.Fatalf("Up of 00105: %v", err)
	}
	assertDesignTables(t, s, "after Up", true)
	assertSeeded("after Up")

	// The design rows are written under the migration. The schema names no reference from design_edges to a contract,
	// so the edge needs no second contract to stand on.
	if _, err := s.fixtureExec(ctx, `INSERT INTO designs (id, title, conversation, created)
		VALUES ('design-one', 'Design one', 'claude-writer', 1700000400)`); err != nil {
		t.Fatalf("insert design: %v", err)
	}
	if _, err := s.fixtureExec(ctx, `INSERT INTO design_contracts (contract, design, position, added)
		VALUES ('contract-one', 'design-one', 0, 1700000400)`); err != nil {
		t.Fatalf("insert design contract: %v", err)
	}
	if _, err := s.fixtureExec(ctx, `INSERT INTO design_edges (design, contract, after, created)
		VALUES ('design-one', 'contract-one', 'contract-two', 1700000400)`); err != nil {
		t.Fatalf("insert design edge: %v", err)
	}

	if _, err := provider.Down(ctx); err != nil {
		t.Fatalf("Down of 00105: %v", err)
	}
	assertDesignTables(t, s, "after Down", false)
	assertSeeded("after Down")

	if _, err := provider.UpByOne(ctx); err != nil {
		t.Fatalf("Up of 00105 again: %v", err)
	}
	assertDesignTables(t, s, "after second Up", true)
	assertSeeded("after second Up")
}
