package store

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// schemaLines returns the schema of db, one line for each table, index, view and trigger in name order: its kind, name,
// table and the SQL that made it, with its spacing normalized, and for a table each of its columns with their type, NOT
// NULL flag, default and key position. goose's version table and SQLite's own tables are left out. It reads the schema
// in two queries, whatever the number of tables, since each query costs the walk of the migrations its time.
func schemaLines(t *testing.T, db *sql.DB) []string {
	t.Helper()
	ctx := context.Background()
	objects, err := db.QueryContext(ctx, `SELECT type, name, tbl_name, COALESCE(sql, '') FROM sqlite_master
		WHERE name NOT LIKE 'sqlite_%' AND name != 'goose_db_version' ORDER BY type, name`)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	var lines []string
	for objects.Next() {
		var kind, name, table, text string
		if err = objects.Scan(&kind, &name, &table, &text); err != nil {
			t.Fatalf("scan schema: %v", err)
		}
		lines = append(lines, kind+" "+name+" on "+table+": "+normalizedSQL(text))
	}
	if err = rowsDone(objects); err != nil {
		t.Fatalf("read schema: %v", err)
	}
	return append(lines, columnLines(t, db)...)
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

// columnLines returns one line for each column of each table schemaLines lists, sorted, so a table's columns follow
// one another in name order.
func columnLines(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(),
		`SELECT m.name, p.name, p.type, p."notnull", COALESCE(p.dflt_value, ''), p.pk
		FROM sqlite_master AS m JOIN pragma_table_info(m.name) AS p
		WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite_%' AND m.name != 'goose_db_version'`)
	if err != nil {
		t.Fatalf("read columns: %v", err)
	}
	var lines []string
	for rows.Next() {
		var table, name, typ, dflt string
		var notNull, pk int
		if err = rows.Scan(&table, &name, &typ, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scan columns: %v", err)
		}
		lines = append(lines, fmt.Sprintf("column %s.%s %s notnull=%d default=%q pk=%d", table, name, typ, notNull,
			dflt, pk))
	}
	if err = rowsDone(rows); err != nil {
		t.Fatalf("read columns: %v", err)
	}
	slices.Sort(lines)
	return lines
}

// migrationCheck is what the walk of TestEachMigrationDownAndUp asserts of one migration beyond its schema. seed stores
// rows at the migration's version before its Down; down checks what the Down left, at the version before; up checks
// what the Up left, at the migration's version again. Each is optional.
type migrationCheck struct {
	seed func(t *testing.T, s *Store)
	down func(t *testing.T, s *Store)
	up   func(t *testing.T, s *Store)
	// columnsAtEnd names the table whose columns the Down adds back at its end, the columns its Up dropped from the middle
	// of it. SQLite places an added column after the table's last, so the table's SQL reads them after the columns that
	// followed them at the version before. The walk then compares that table by its name, and its columns by their lines,
	// which hold each column's type, NOT NULL flag, default and key position.
	columnsAtEnd string
}

// migrationChecks returns the walk's checks by migration version.
func migrationChecks() map[int64]migrationCheck {
	return map[int64]migrationCheck{
		13:  engineReadoptDownAndUp(),
		20:  evidenceDownAndUp(),
		21:  taskBuildDownAndUp(),
		22:  workflowDownAndUp(),
		23:  checkoutTailsDownAndUp(),
		24:  turnEndIndexDownAndUp(),
		25:  dueResumesDownAndUp(),
		26:  limitToldDownAndUp(),
		35:  reviewersDownAndUp(),
		37:  assertionsDownAndUp(),
		38:  releaseDownAndUp(),
		39:  questionsDownAndUp(),
		40:  guardsDownAndUp(),
		48:  epicCloseDownAndUp(),
		49:  plannerActionsDownAndUp(),
		50:  parkClassDownAndUp(),
		51:  parkBoundDownAndUp(),
		52:  evidenceAmendmentsDownAndUp(),
		101: {columnsAtEnd: "epics"},
		115: stepPurposeDownAndUp(),
	}
}

// migrationNames returns the file name of each embedded migration without its extension, by version.
func migrationNames(t *testing.T) map[int64]string {
	t.Helper()
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		t.Fatalf("list migrations: %v", err)
	}
	names := map[int64]string{}
	for _, entry := range entries {
		name := strings.TrimSuffix(entry.Name(), ".sql")
		prefix, _, _ := strings.Cut(name, "_")
		version, err := strconv.ParseInt(prefix, 10, 64)
		if err != nil {
			t.Fatalf("migration %s has no version: %v", entry.Name(), err)
		}
		names[version] = name
	}
	return names
}

// newestMigration returns the version of the newest embedded migration, the version Open leaves a store at, so a test
// that opens a store need not name it and does not move with each new migration.
func newestMigration(t *testing.T) int64 {
	t.Helper()
	newest := int64(0)
	for version := range migrationNames(t) {
		newest = max(newest, version)
	}
	return newest
}

// TestEachMigrationDownAndUp walks the migrations once. Each migration starts from a copy of the store at its version,
// as the walk of the migrations from the first leaves it, and steps down and up once: its Down must leave the schema of
// the version before, and its Up the schema of its version again, so every Down undoes its Up whatever the version a
// store is taken down from. The migration's check (see migrationChecks) seeds its rows before the Down and asserts what
// the Down removes and what the Up restores. A subtest is named for its migration's file, so a broken Down or Up names
// the migration.
func TestEachMigrationDownAndUp(t *testing.T) {
	t.Parallel()
	images, err := storeImages.versions()
	if err != nil {
		t.Fatalf("make store version images: %v", err)
	}
	names := migrationNames(t)
	if len(names) != len(images) {
		t.Fatalf("%d migrations, %d store version images", len(names), len(images))
	}
	checks := migrationChecks()
	for version := range checks {
		if names[version] == "" {
			t.Fatalf("a migration check is keyed to version %d, which no migration has", version)
		}
	}
	// The versions are the migrations that exist, in ascending order: a version the branch skips has no migration to walk.
	versions := make([]int64, 0, len(names))
	for version := range names {
		versions = append(versions, version)
	}
	slices.Sort(versions)
	for i, version := range versions {
		t.Run(names[version], func(t *testing.T) {
			t.Parallel()
			var before int64
			var previous []string
			if i > 0 {
				before = versions[i-1]
				prior := openStoreAtVersion(t, filepath.Join(t.TempDir(), "before.db"), before)
				previous = schemaLines(t, prior)
				closeDB(t, prior)
			}
			s, provider := storeAtVersion(t, version)
			at := schemaLines(t, s.db.DB)
			ctx := context.Background()
			check := checks[version]
			if check.seed != nil {
				check.seed(t, s)
			}

			if _, err := provider.Down(ctx); err != nil {
				t.Fatalf("Down of %s: %v", names[version], err)
			}
			got, want := schemaLines(t, s.db.DB), previous
			if check.columnsAtEnd != "" {
				got, want = withoutTableSQL(got, check.columnsAtEnd), withoutTableSQL(want, check.columnsAtEnd)
			}
			if !slices.Equal(got, want) {
				t.Fatalf("schema after the Down of %s differs from version %d:\n%s", names[version], before,
					lineDiff(got, want))
			}
			if check.down != nil {
				check.down(t, s)
			}
			if _, err := provider.UpByOne(ctx); err != nil {
				t.Fatalf("Up of %s: %v", names[version], err)
			}
			if got := schemaLines(t, s.db.DB); !slices.Equal(got, at) {
				t.Fatalf("schema after the Down and Up of %s differs from version %d:\n%s", names[version], version,
					lineDiff(got, at))
			}
			if check.up != nil {
				check.up(t, s)
			}
		})
	}
}

// withoutTableSQL returns schema with the SQL of table left out of its line, so the table is compared by its name alone.
// Its columns keep their lines in schema.
func withoutTableSQL(schema []string, table string) []string {
	lines := make([]string, len(schema))
	for i, line := range schema {
		lines[i] = line
		if key := schemaKey(line); key == "table "+table+" on "+table {
			lines[i] = key
		}
	}
	return lines
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
