package migrations

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/hostexec"
)

// testDir is the migrations directory the tests' trees hold their migrations in.
const testDir = "db/migrations"

// files returns the migrations Parse reads from the names under testDir.
func files(names ...string) []File {
	paths := make([]string, 0, len(names))
	for _, name := range names {
		paths = append(paths, testDir+"/"+name)
	}
	return Parse(paths)
}

// TestParseReadsGooseVersions: Parse keeps the .sql files whose names open with a version of 1 or more and an
// underscore, in version order, and leaves out every other path: no underscore, no digits, letters among the digits, a
// version of 0, and a file that is not SQL.
func TestParseReadsGooseVersions(t *testing.T) {
	t.Parallel()
	got := Parse([]string{
		"m/00003_c.sql", "m/00001_a.sql", "m/README.md", "m/00002_b.go", "m/abc_d.sql", "m/12a_e.sql", "m/0_zero.sql",
		"m/_none.sql", "m/00004.sql", "m/10_ten.sql",
	})
	want := []File{
		{Path: "m/00001_a.sql", Version: 1, digits: "00001"},
		{Path: "m/00003_c.sql", Version: 3, digits: "00003"},
		{Path: "m/10_ten.sql", Version: 10, digits: "10"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Parse = %+v, want %+v", got, want)
	}
}

// TestCheckNoCollisionRenamesNothing: new migrations whose versions origin does not use, and migrations origin already
// holds, give an empty plan, whose summary, lines and failure are empty too.
func TestCheckNoCollisionRenamesNothing(t *testing.T) {
	t.Parallel()
	own := files("00001_a.sql", "00002_b.sql", "00004_new.sql")
	origin := files("00001_a.sql", "00002_b.sql", "00003_c.sql")
	plan := Check(own, origin, nil)
	if !reflect.DeepEqual(plan, Plan{}) {
		t.Errorf("Check = %+v, want an empty plan", plan)
	}
	if plan.Summary("the candidate") != "" || plan.Lines("the candidate") != nil || plan.Failure() != "" {
		t.Errorf("empty plan renders %q, %q and %q, want nothing", plan.Summary("the candidate"),
			plan.Lines("the candidate"), plan.Failure())
	}
}

// TestCheckRenumbersEveryNewMigrationAfterOrigin: the candidate's 00114_foo.sql takes the version of origin's
// 00114_bar.sql, so both of the candidate's new migrations move, in order, to the two versions after origin's newest,
// 00115_qux.sql. The migrations both sides hold stay.
func TestCheckRenumbersEveryNewMigrationAfterOrigin(t *testing.T) {
	t.Parallel()
	own := files("00113_base.sql", "00114_foo.sql", "00115_baz.sql")
	origin := files("00113_base.sql", "00114_bar.sql", "00115_qux.sql")
	plan := Check(own, origin, nil)
	want := Plan{
		Collisions: []Collision{
			{Path: testDir + "/00114_foo.sql", Origin: testDir + "/00114_bar.sql", Version: 114},
			{Path: testDir + "/00115_baz.sql", Origin: testDir + "/00115_qux.sql", Version: 115},
		},
		Renames: []Rename{
			{From: testDir + "/00114_foo.sql", To: testDir + "/00116_foo.sql"},
			{From: testDir + "/00115_baz.sql", To: testDir + "/00117_baz.sql"},
		},
	}
	if !reflect.DeepEqual(plan, want) {
		t.Fatalf("Check = %+v, want %+v", plan, want)
	}
	if got, wantSummary := plan.Summary("the candidate"),
		"00114_foo.sql collides with origin's 00114_bar.sql, 00115_baz.sql collides with origin's 00115_qux.sql; "+
			"renumber the candidate's new migrations to 00116, 00117 in order"; got != wantSummary {
		t.Errorf("Summary = %q, want %q", got, wantSummary)
	}
	lines := plan.Lines("the candidate")
	for _, line := range []string{
		"- db/migrations/00114_foo.sql collides with origin's db/migrations/00114_bar.sql: both are version 114",
		"Renumber the candidate's new migrations, in order:",
		"- db/migrations/00114_foo.sql to db/migrations/00116_foo.sql",
		"- db/migrations/00115_baz.sql to db/migrations/00117_baz.sql",
	} {
		if !slices.Contains(lines, line) {
			t.Errorf("Lines = %q, want the line %q", lines, line)
		}
	}
	if failure := plan.Failure(); !strings.HasPrefix(failure, "migrations: db/migrations/00114_foo.sql collides with "+
		"origin's db/migrations/00114_bar.sql, db/migrations/00115_baz.sql collides with") {
		t.Errorf("Failure = %q, want the collisions named", failure)
	}
}

// TestCheckMovesNewMigrationsPastAlsoAndKeepsTheirs: a task adds 00003_task.sql, whose version origin uses, and
// 00006_more.sql, whose version nobody uses. The candidate it merges into, also, holds the epic's own 00005_epic.sql,
// which is not the task's to move and does not count as new. Both of the task's new migrations move after the newest of
// origin and the candidate, 00005, and keep their digit count.
func TestCheckMovesNewMigrationsPastAlsoAndKeepsTheirs(t *testing.T) {
	t.Parallel()
	own := Parse([]string{"m/001_a.sql", "m/005_epic.sql", "m/003_task.sql", "m/006_more.sql"})
	origin := Parse([]string{"m/001_a.sql", "m/003_origin.sql", "m/004_origin.sql"})
	also := Parse([]string{"m/001_a.sql", "m/005_epic.sql"})
	plan := Check(own, origin, also)
	want := Plan{
		Collisions: []Collision{{Path: "m/003_task.sql", Origin: "m/003_origin.sql", Version: 3}},
		Renames: []Rename{
			{From: "m/003_task.sql", To: "m/006_task.sql"},
			{From: "m/006_more.sql", To: "m/007_more.sql"},
		},
	}
	if !reflect.DeepEqual(plan, want) {
		t.Errorf("Check = %+v, want %+v", plan, want)
	}
}

// TestCheckIgnoresCollisionsAlsoHolds: a migration the candidate already holds is not new, so its version taking one
// origin uses is no collision of the task's.
func TestCheckIgnoresCollisionsAlsoHolds(t *testing.T) {
	t.Parallel()
	own := files("00001_a.sql", "00002_epic.sql")
	origin := files("00001_a.sql", "00002_origin.sql")
	also := files("00001_a.sql", "00002_epic.sql")
	if plan := Check(own, origin, also); !reflect.DeepEqual(plan, Plan{}) {
		t.Errorf("Check = %+v, want no collision of a migration also holds", plan)
	}
}

// testGit runs git in dir and returns its trimmed output, failing the test on error.
func testGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.invalid",
		"-c", "commit.gpgsign=false"}, args...)
	cmd := exec.CommandContext(t.Context(), "git", full...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestListReadsTheCommitsTree: List reads the migrations of a commit's tree, not the working tree: a migration written
// after the commit is not listed, nor a file of a subdirectory or one that is not a migration. A commit with no such
// directory lists none.
func TestListReadsTheCommitsTree(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	testGit(t, repo, "init", "-q", "-b", "main")
	for name, text := range map[string]string{
		"README":                       "base\n",
		testDir + "/00002_b.sql":       "-- b\n",
		testDir + "/00001_a.sql":       "-- a\n",
		testDir + "/notes.md":          "notes\n",
		testDir + "/old/00003_old.sql": "-- old\n",
	} {
		path := filepath.Join(repo, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("make directory of %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	testGit(t, repo, "add", ".")
	testGit(t, repo, "commit", "-q", "-m", "base")
	commit := testGit(t, repo, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, testDir, "00004_later.sql"), []byte("-- later\n"), 0o600); err != nil {
		t.Fatalf("write a later migration: %v", err)
	}

	got, err := List(t.Context(), hostexec.NewLocal(), repo, commit, testDir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := files("00001_a.sql", "00002_b.sql")
	if !reflect.DeepEqual(got, want) {
		t.Errorf("List = %+v, want %+v", got, want)
	}
	none, err := List(t.Context(), hostexec.NewLocal(), repo, commit, "no/such/dir")
	if err != nil || len(none) != 0 {
		t.Errorf("List of a missing directory = %+v, %v, want none and no error", none, err)
	}
}
