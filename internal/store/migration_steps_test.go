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

// schemaLines returns the schema of db, one line for each table, index, view and trigger in name order: its kind, name,
// table and the SQL that made it, with its spacing normalized, and for a table each of its columns with their type, NOT
// NULL flag, default and key position. goose's version table and SQLite's own tables are left out.
func schemaLines(t *testing.T, db *sql.DB) []string {
	t.Helper()
	ctx := context.Background()
	objects, err := db.QueryContext(ctx, `SELECT type, name, tbl_name, COALESCE(sql, '') FROM sqlite_master
		WHERE name NOT LIKE 'sqlite_%' AND name != 'goose_db_version' ORDER BY type, name`)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	var lines []string
	var tables []string
	for objects.Next() {
		var kind, name, table, text string
		if err = objects.Scan(&kind, &name, &table, &text); err != nil {
			t.Fatalf("scan schema: %v", err)
		}
		lines = append(lines, kind+" "+name+" on "+table+": "+normalizedSQL(text))
		if kind == "table" {
			tables = append(tables, name)
		}
	}
	if err = rowsDone(objects); err != nil {
		t.Fatalf("read schema: %v", err)
	}
	for _, table := range tables {
		lines = append(lines, columnLines(t, db, table)...)
	}
	return lines
}

// normalizedSQL returns text with its runs of white space made one space, and a space on each side of each parenthesis
// and comma, so SQL that differs only in its spacing reads the same.
func normalizedSQL(text string) string {
	spaced := strings.NewReplacer("(", " ( ", ")", " ) ", ",", " , ").Replace(text)
	return strings.Join(strings.Fields(spaced), " ")
}

// rowsDone closes rows and returns the error that ended them, or the error of the close.
func rowsDone(rows *sql.Rows) error {
	if err := rows.Err(); err != nil {
		return err
	}
	return rows.Close()
}

// columnLines returns one line for each column of table in db, as schemaLines lists them.
func columnLines(t *testing.T, db *sql.DB, table string) []string {
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
		lines = append(lines, fmt.Sprintf("column %s.%s %s notnull=%d default=%q pk=%d", table, name, typ, notNull,
			dflt, pk))
	}
	if err = rowsDone(rows); err != nil {
		t.Fatalf("read columns of %s: %v", table, err)
	}
	slices.Sort(lines)
	return lines
}

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
			var previous []string
			if version > 1 {
				before := openStoreAtVersion(t, filepath.Join(t.TempDir(), "before.db"), version-1)
				previous = schemaLines(t, before)
				closeDB(t, before)
			}
			s, provider := storeAtVersion(t, version)
			at := schemaLines(t, s.db.DB)
			ctx := context.Background()

			if _, err := provider.Down(ctx); err != nil {
				t.Fatalf("Down of %d: %v", version, err)
			}
			if got := schemaLines(t, s.db.DB); !slices.Equal(got, previous) {
				t.Fatalf("schema after the Down of %d differs from version %d:\n%s", version, version-1,
					lineDiff(got, previous))
			}
			if _, err := provider.UpByOne(ctx); err != nil {
				t.Fatalf("Up of %d: %v", version, err)
			}
			if got := schemaLines(t, s.db.DB); !slices.Equal(got, at) {
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
