package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// schemaLines returns the schema of db, one line for each table, index, view and trigger in name order: its kind, name
// and table, and for a table each of its columns with their type, NOT NULL flag, default and key position. goose's
// version table and SQLite's own tables are left out. A column whose table and name appear in noDefault is listed with
// no default.
func schemaLines(t *testing.T, db *sql.DB, noDefault map[string]bool) []string {
	t.Helper()
	ctx := context.Background()
	objects, err := db.QueryContext(ctx, `SELECT type, name, tbl_name FROM sqlite_master
		WHERE name NOT LIKE 'sqlite_%' AND name != 'goose_db_version' ORDER BY type, name`)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	var lines []string
	var tables []string
	for objects.Next() {
		var kind, name, table string
		if err = objects.Scan(&kind, &name, &table); err != nil {
			t.Fatalf("scan schema: %v", err)
		}
		lines = append(lines, kind+" "+name+" on "+table)
		if kind == "table" {
			tables = append(tables, name)
		}
	}
	if err = rowsDone(objects); err != nil {
		t.Fatalf("read schema: %v", err)
	}
	for _, table := range tables {
		lines = append(lines, columnLines(t, db, table, noDefault)...)
	}
	return lines
}

// rowsDone closes rows and returns the error that ended them, or the error of the close.
func rowsDone(rows *sql.Rows) error {
	if err := rows.Err(); err != nil {
		return err
	}
	return rows.Close()
}

// columnLines returns one line for each column of table in db, as schemaLines lists them.
func columnLines(t *testing.T, db *sql.DB, table string, noDefault map[string]bool) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(),
		`SELECT name, type, "notnull", COALESCE(dflt_value, ''), pk FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatalf("read columns of %s: %v", table, err)
	}
	var lines []string
	for rows.Next() {
		var name, typ, dflt string
		var notNull, pk int
		if err = rows.Scan(&name, &typ, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scan columns of %s: %v", table, err)
		}
		if noDefault[table+"."+name] {
			dflt = ""
		}
		lines = append(lines, fmt.Sprintf("column %s.%s %s notnull=%d default=%q pk=%d", table, name, typ, notNull,
			dflt, pk))
	}
	if err = rowsDone(rows); err != nil {
		t.Fatalf("read columns of %s: %v", table, err)
	}
	slices.Sort(lines)
	return lines
}

// downDefaults are the columns whose default a migration's Down cannot take back, by the version of that migration.
// SQLite adds a NOT NULL column only with a default, so the Down of 00020_evidence.sql gives task_check back with
// the empty string as its default, which the column did not have at version 19.
//
//nolint:gochecknoglobals // a fixed table of the migrations' known differences, read by one test
var downDefaults = map[int64]map[string]bool{20: {"epic_tasks.task_check": true}}

// TestEachMigrationDownRestoresThePreviousSchema runs each migration's Down on the store at its version, as the walk of
// the migrations from the first leaves it, and requires the schema of the version before; then its Up, and requires the
// schema of its version again. So every Down undoes its Up, whatever the version a store is taken down from, and the
// tests of one migration's Down start at that migration's version.
func TestEachMigrationDownRestoresThePreviousSchema(t *testing.T) {
	t.Parallel()
	images, err := storeImages.versions()
	if err != nil {
		t.Fatalf("make store version images: %v", err)
	}
	for version := int64(1); version <= int64(len(images)); version++ {
		t.Run(fmt.Sprintf("version %d", version), func(t *testing.T) {
			t.Parallel()
			noDefault := downDefaults[version]
			var previous []string
			if version > 1 {
				before := openStoreAtVersion(t, filepath.Join(t.TempDir(), "before.db"), version-1)
				previous = schemaLines(t, before, noDefault)
				closeDB(t, before)
			}
			s, provider := storeAtVersion(t, version)
			at := schemaLines(t, s.db.DB, noDefault)
			ctx := context.Background()

			if _, err := provider.Down(ctx); err != nil {
				t.Fatalf("Down of %d: %v", version, err)
			}
			if got := schemaLines(t, s.db.DB, noDefault); !slices.Equal(got, previous) {
				t.Fatalf("schema after the Down of %d differs from version %d:\n%s", version, version-1,
					lineDiff(got, previous))
			}
			if _, err := provider.UpByOne(ctx); err != nil {
				t.Fatalf("Up of %d: %v", version, err)
			}
			if got := schemaLines(t, s.db.DB, noDefault); !slices.Equal(got, at) {
				t.Fatalf("schema after the Down and Up of %d differs from version %d:\n%s", version, version,
					lineDiff(got, at))
			}
		})
	}
}

// closeDB closes db, failing the test when it cannot.
func closeDB(t *testing.T, db *sql.DB) {
	t.Helper()
	if err := db.Close(); err != nil {
		t.Errorf("close store: %v", err)
	}
}

// lineDiff returns the lines only got holds, marked +, and the lines only want holds, marked -.
func lineDiff(got, want []string) string {
	var out []string
	for _, line := range got {
		if !slices.Contains(want, line) {
			out = append(out, "+ "+line)
		}
	}
	for _, line := range want {
		if !slices.Contains(got, line) {
			out = append(out, "- "+line)
		}
	}
	return strings.Join(out, "\n")
}
