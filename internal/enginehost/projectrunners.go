package enginehost

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"

	"github.com/BurntSushi/toml"
)

// ProjectRunner is one runner a project names: the name a capture asks for it by, the argv it runs, and a note on what
// it is for.
type ProjectRunner struct {
	Name string
	Argv []string
	Note string
}

// projectRunnersKey is the key under the release table that holds the project's runners, an array of tables. The
// release table is where orreryd accepts a key of the project file, so the runners sit there.
const projectRunnersKey = "runners"

// localRunnerName is the runner name no runner may take, since it names the machine a capture runs on.
const localRunnerName = "local"

// runnerNamePattern is the form a runner's name takes.
var runnerNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// runnerKeys lists the keys a runner table holds. Any other key is refused.
func runnerKeys() []string {
	return []string{"name", "argv", "note"}
}

// ParseProjectRunners returns the runners that a project's runners file in data names, in the order the file lists
// them. A file with no release table, or a release table with no runners, names none. Each runner is a table under
// release.runners with three keys: name, which matches [a-z][a-z0-9_]* and is neither "local" nor a name another
// runner has; argv, a non-empty array of non-blank strings; and note, non-blank text. A file that is not TOML comes
// back wrapped. Any other key, and any value that is not of the form above, is refused with an *epicFieldError
// naming the field.
func ParseProjectRunners(data []byte) ([]ProjectRunner, error) {
	var doc map[string]any
	if _, err := toml.Decode(string(data), &doc); err != nil {
		return nil, fmt.Errorf("parse project runners: %w", err)
	}
	raw, found := doc[projectReleaseKey]
	if !found {
		return nil, nil
	}
	release, isTable := raw.(map[string]any)
	if !isTable {
		return nil, &epicFieldError{field: projectReleaseKey, reason: "not a table"}
	}
	value, found := release[projectRunnersKey]
	if !found {
		return nil, nil
	}
	tables, err := runnerTables(value)
	if err != nil {
		return nil, err
	}
	var runners []ProjectRunner
	seen := make(map[string]bool, len(tables))
	for i, table := range tables {
		runner, runnerErr := parseProjectRunner(i, table)
		if runnerErr != nil {
			return nil, runnerErr
		}
		if seen[runner.Name] {
			return nil, &epicFieldError{field: runnerPath(i) + ".name", reason: epicReasonDuplicate}
		}
		seen[runner.Name] = true
		runners = append(runners, runner)
	}
	return runners, nil
}

// ReadProjectRunners returns the runners that the project of repo names at commit, read from the commit and never the
// working tree. A commit with no project file, or a project file with no runners, names none. It returns an error when
// the commit does not resolve or the file's runners do not parse.
func ReadProjectRunners(ctx context.Context, repo, commit string) ([]ProjectRunner, error) {
	base, err := stepGitResolve(ctx, repo, commit)
	if err != nil {
		return nil, fmt.Errorf("resolve commit: %w", err)
	}
	data, found, err := baseReader(ctx, repo, base)(projectFileRel)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	return ParseProjectRunners(data)
}

// runnerPath returns the path of the runner table at index i, for the field a refusal names.
func runnerPath(i int) string {
	return fmt.Sprintf("%s.%s[%d]", projectReleaseKey, projectRunnersKey, i)
}

// runnerTables returns the runner tables of the runners key. The key must be an array of tables; an empty array counts
// as none.
func runnerTables(value any) ([]map[string]any, error) {
	if list, isList := value.([]any); isList && len(list) == 0 {
		return []map[string]any{}, nil
	}
	tables, isTables := value.([]map[string]any)
	if !isTables {
		return nil, &epicFieldError{
			field:  projectReleaseKey + "." + projectRunnersKey,
			reason: "not an array of tables",
		}
	}
	return tables, nil
}

// parseProjectRunner reads the table of the runner at index i. The keys are checked in name order, so the refusal for
// a table with several wrong keys is the same every time.
func parseProjectRunner(i int, table map[string]any) (ProjectRunner, error) {
	path := runnerPath(i)
	for _, key := range slices.Sorted(maps.Keys(table)) {
		if !slices.Contains(runnerKeys(), key) {
			return ProjectRunner{}, &epicFieldError{field: path + "." + key, reason: reasonUnknownKey}
		}
	}
	name, err := runnerText(path, table, "name")
	if err != nil {
		return ProjectRunner{}, err
	}
	if name == localRunnerName {
		return ProjectRunner{}, &epicFieldError{field: path + ".name", reason: `"local" is reserved`}
	}
	if !runnerNamePattern.MatchString(name) {
		return ProjectRunner{}, &epicFieldError{field: path + ".name", reason: "must match [a-z][a-z0-9_]*"}
	}
	argv, err := runnerArgv(path, table)
	if err != nil {
		return ProjectRunner{}, err
	}
	note, err := runnerText(path, table, "note")
	if err != nil {
		return ProjectRunner{}, err
	}
	return ProjectRunner{Name: name, Argv: argv, Note: note}, nil
}

// runnerText returns the text at key of the runner table at path. The key must be present, must be a string, and must
// not be blank after trimming; a missing key counts as blank.
func runnerText(path string, table map[string]any, key string) (string, error) {
	field := path + "." + key
	raw, has := table[key]
	if !has {
		return "", &epicFieldError{field: field, reason: epicReasonMissing}
	}
	text, isString := raw.(string)
	if !isString {
		return "", &epicFieldError{field: field, reason: reasonNotString}
	}
	if isBlank(text) {
		return "", &epicFieldError{field: field, reason: epicReasonEmpty}
	}
	return text, nil
}

// runnerArgv returns the argv of the runner table at path. The key must be present and must be an array of strings
// that holds at least one, and each string must not be blank after trimming.
func runnerArgv(path string, table map[string]any) ([]string, error) {
	field := path + ".argv"
	raw, has := table["argv"]
	if !has {
		return nil, &epicFieldError{field: field, reason: epicReasonMissing}
	}
	list, isList := raw.([]any)
	if !isList {
		return nil, &epicFieldError{field: field, reason: "not an array of strings"}
	}
	if len(list) == 0 {
		return nil, &epicFieldError{field: field, reason: epicReasonEmpty}
	}
	argv := make([]string, 0, len(list))
	for j, item := range list {
		text, isString := item.(string)
		if !isString {
			return nil, &epicFieldError{field: fmt.Sprintf("%s[%d]", field, j), reason: reasonNotString}
		}
		if isBlank(text) {
			return nil, &epicFieldError{field: fmt.Sprintf("%s[%d]", field, j), reason: epicReasonEmpty}
		}
		argv = append(argv, text)
	}
	return argv, nil
}
