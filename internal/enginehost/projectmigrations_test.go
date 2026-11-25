package enginehost

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/pools"
	"github.com/joshsymonds/orrery/internal/reviewers"
)

// migrationsProjectText is a project file that declares its goose migrations beside a release and a gate, as orrery's
// own does.
const migrationsProjectText = `[migrations]
goose = "db/migrations"

[gates.check]
argv = ["/opt/example/bin/check"]
limit = 60

[release]
then = "announce the build"

[[release.steps]]
step = "publish the bundle"
target = "mirror-one"
undo = "remove the bundle"
`

func TestProjectMigrations_ParsesTheGooseDirectory(t *testing.T) {
	t.Parallel()
	dir, err := ParseProjectMigrations([]byte(migrationsProjectText))
	if err != nil || dir != "db/migrations" {
		t.Fatalf("ParseProjectMigrations = %q, %v, want db/migrations and no error", dir, err)
	}
}

func TestProjectMigrations_NoDeclarationNamesNone(t *testing.T) {
	t.Parallel()
	for _, text := range []string{"", "[release]\nthen = \"announce\"\n", "[migrations]\n"} {
		dir, err := ParseProjectMigrations([]byte(text))
		if err != nil || dir != "" {
			t.Errorf("ParseProjectMigrations(%q) = %q, %v, want none and no error", text, dir, err)
		}
	}
}

func TestProjectMigrations_SyntaxErrorWraps(t *testing.T) {
	t.Parallel()
	_, err := ParseProjectMigrations([]byte("[migrations\n"))
	var fieldErr *epicFieldError
	if err == nil || errors.As(err, &fieldErr) {
		t.Fatalf("ParseProjectMigrations error = %v, want a TOML error that is no field refusal", err)
	}
}

func TestProjectMigrations_RefusesMalformed(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ text, want string }{
		{"migrations = \"db\"\n", "migrations: not a table"},
		{"[migrations]\nsql = \"db\"\n", "migrations.sql: unknown key"},
		{"[migrations]\ngoose = 1\n", "migrations.goose: not a string"},
		{"[migrations]\ngoose = \"  \"\n", "migrations.goose: empty"},
		{"[migrations]\ngoose = \"/db/migrations\"\n", "migrations.goose: not a clean directory inside the repository"},
		{"[migrations]\ngoose = \"db/migrations/\"\n", "migrations.goose: not a clean directory inside the repository"},
		{"[migrations]\ngoose = \"db/./migrations\"\n", "migrations.goose: not a clean directory inside the repository"},
		{"[migrations]\ngoose = \"../db\"\n", "migrations.goose: not a clean directory inside the repository"},
		{"[migrations]\ngoose = \".\"\n", "migrations.goose: not a clean directory inside the repository"},
	} {
		_, err := ParseProjectMigrations([]byte(tc.text))
		var fieldErr *epicFieldError
		if !errors.As(err, &fieldErr) || err.Error() != tc.want {
			t.Errorf("ParseProjectMigrations(%q) error = %v, want the field refusal %q", tc.text, err, tc.want)
		}
	}
}

// TestProjectMigrations_OtherReadersPassTheTable: the readers of the project file's other tables pass the migrations
// table, so a project file that declares its migrations still gives its pools, gates, reviewers and release.
func TestProjectMigrations_OtherReadersPassTheTable(t *testing.T) {
	t.Parallel()
	data := []byte(migrationsProjectText)
	if _, err := pools.Parse("project.toml", data); err != nil {
		t.Errorf("pools.Parse error = %v, want nil", err)
	}
	defined, err := gates.ParseProject("project.toml", data)
	if err != nil || len(defined) != 1 {
		t.Errorf("gates.ParseProject = %+v, %v, want the one gate and no error", defined, err)
	}
	if _, err = reviewers.ParseProject("project.toml", data); err != nil {
		t.Errorf("reviewers.ParseProject error = %v, want nil", err)
	}
	if _, _, found, releaseErr := parseProjectRelease(data); releaseErr != nil || !found {
		t.Errorf("parseProjectRelease found %v, error %v, want the release and no error", found, releaseErr)
	}
}

func TestProjectMigrations_ReadsTheDeclarationAtCommit(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	base := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	withFile := stepGitTestCommit(t, repo, map[string]string{".orrery/project.toml": migrationsProjectText})
	stepGitTestWrite(t, repo, ".orrery/project.toml", "[migrations]\ngoose = \"other\"\n")

	dir, err := ReadProjectMigrations(t.Context(), hostexec.NewLocal(), repo, withFile)
	if err != nil || dir != "db/migrations" {
		t.Fatalf("ReadProjectMigrations at the commit with the file = %q, %v, want db/migrations", dir, err)
	}
	none, err := ReadProjectMigrations(t.Context(), hostexec.NewLocal(), repo, base)
	if err != nil || none != "" {
		t.Fatalf("ReadProjectMigrations at the commit without the file = %q, %v, want none", none, err)
	}
}

// TestProjectMigrations_RepoDeclaresTheStoreMigrations: orrery's own project file declares the store's goose
// migrations, a directory that holds them.
func TestProjectMigrations_RepoDeclaresTheStoreMigrations(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("..", "..", ".orrery", "project.toml"))
	if err != nil {
		t.Fatal(err)
	}
	dir, err := ParseProjectMigrations(data)
	if err != nil || dir != "internal/store/migrations" {
		t.Fatalf("ParseProjectMigrations = %q, %v, want internal/store/migrations", dir, err)
	}
	entries, err := os.ReadDir(filepath.Join("..", "..", filepath.FromSlash(dir)))
	if err != nil || len(entries) == 0 {
		t.Fatalf("read %s: %d entries, %v, want its migrations", dir, len(entries), err)
	}
}
