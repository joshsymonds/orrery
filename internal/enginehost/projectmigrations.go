package enginehost

import (
	"context"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/joshsymonds/orrery/internal/hostexec"
)

// projectMigrationsKey is the top-level key of a project file's migrations table, and projectGooseKey the key under it
// that names the directory of the project's goose SQL migrations.
const (
	projectMigrationsKey = "migrations"
	projectGooseKey      = "goose"
)

// ParseProjectMigrations returns the directory of goose SQL migrations that a project file in data declares: the goose
// key of its migrations table, a directory from the repository's root. A file with no migrations table, or a table with
// no goose key, declares none, and "" comes back with a nil error. The directory is a string that is not blank and is
// written clean, relative and inside the repository, so it is not absolute, holds no . or .. part and no trailing
// slash. A file that is not TOML comes back wrapped. Any other key under the table, and a value that is not of that
// form, is refused with an *epicFieldError naming the field.
func ParseProjectMigrations(data []byte) (string, error) {
	var doc map[string]any
	if _, err := toml.Decode(string(data), &doc); err != nil {
		return "", fmt.Errorf("parse project migrations: %w", err)
	}
	raw, found := doc[projectMigrationsKey]
	if !found {
		return "", nil
	}
	table, isTable := raw.(map[string]any)
	if !isTable {
		return "", &epicFieldError{field: projectMigrationsKey, reason: reasonNotTable}
	}
	// The keys are checked in name order, so the refusal for a table with several wrong keys is the same every time.
	for _, key := range slices.Sorted(maps.Keys(table)) {
		if key != projectGooseKey {
			return "", &epicFieldError{field: projectMigrationsKey + "." + key, reason: reasonUnknownKey}
		}
	}
	value, found := table[projectGooseKey]
	if !found {
		return "", nil
	}
	field := projectMigrationsKey + "." + projectGooseKey
	dir, isString := value.(string)
	switch {
	case !isString:
		return "", &epicFieldError{field: field, reason: reasonNotString}
	case isBlank(dir):
		return "", &epicFieldError{field: field, reason: epicReasonEmpty}
	case path.IsAbs(dir) || path.Clean(dir) != dir || dir == "." || dir == ".." || strings.HasPrefix(dir, "../"):
		return "", &epicFieldError{field: field, reason: "not a clean directory inside the repository"}
	}
	return dir, nil
}

// ReadProjectMigrations returns the directory of goose SQL migrations that the project of repo declares at commit
// (ParseProjectMigrations), read from the commit through host and never the working tree. A commit with no project
// file, or a project file that declares none, gives "". It returns an error when the commit does not resolve or the
// file's migrations table does not parse.
func ReadProjectMigrations(ctx context.Context, host hostexec.Host, repo, commit string) (string, error) {
	resolved, err := stepGitResolve(ctx, host, repo, commit)
	if err != nil {
		return "", fmt.Errorf("resolve commit: %w", err)
	}
	data, found, err := baseReader(ctx, host, repo, resolved)(projectFileRel)
	if err != nil {
		return "", err
	}
	if !found {
		return "", nil
	}
	return ParseProjectMigrations(data)
}
