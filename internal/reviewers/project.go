package reviewers

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/BurntSushi/toml"

	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/pools"
)

// sectionReviewers is the top-level array of tables of a project file that this package reads.
const sectionReviewers = "reviewers"

// keyPrompt is the entry key that holds a reviewer's prompt; Merge and the compile name the same key.
const keyPrompt = "prompt"

// Project reads the reviewers of the project file that pools.Discover finds for dir on host, which reads the file. It
// returns nil when there is no project file. getenv reads the environment for the machine's pool file, as
// pools.Discover does.
func Project(ctx context.Context, host hostexec.Host, dir string, getenv func(string) string) ([]Entry, error) {
	files, err := pools.Discover(ctx, host, dir, getenv)
	if err != nil {
		return nil, fmt.Errorf("find the project for %s: %w", dir, err)
	}
	if files.ProjectFile == "" {
		return nil, nil
	}
	data, err := host.ReadFile(ctx, files.ProjectFile)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", files.ProjectFile, err)
	}
	return ParseProject(files.ProjectFile, data)
}

// ParseProject reads the [[reviewers]] entries of a project file. path names the file in errors; data is its contents.
// Each entry comes back holding exactly the keys it sets, in file order. The gates and pools tables are left to their
// readers. A key the entry does not define, or a value of the wrong type, is refused with an Error naming the file,
// the entry and the key.
func ParseProject(path string, data []byte) ([]Entry, error) {
	var doc map[string]any
	if _, err := toml.Decode(string(data), &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	raw, ok := doc[sectionReviewers]
	if !ok {
		return nil, nil
	}
	// TOML metadata cannot tell one array element's keys from another's, so presence is read from each entry's table.
	tables, ok := raw.([]map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: %s: must be an array of tables", path, sectionReviewers)
	}
	var out []Entry
	for i, table := range tables {
		entry, err := toEntry(path, i, table)
		if err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	return out, nil
}

// toEntry reads one entry's table. It refuses the first key, in name order, that the entry does not define or that
// holds the wrong type.
func toEntry(path string, index int, table map[string]any) (Entry, error) {
	entry := Entry{Source: path, Index: index}
	// The name is read first so every refusal names the entry, whichever key it concerns.
	if name, isString := table["name"].(string); isString {
		entry.Name = name
	}
	for _, key := range slices.Sorted(maps.Keys(table)) {
		known, typed := entry.set(key, table[key])
		if !known {
			return Entry{}, refuse(entry, key, "is not a key of a reviewer")
		}
		if !typed {
			return Entry{}, refuse(entry, key, "has the wrong type")
		}
	}
	return entry, nil
}

// set stores value as key on the entry. known reports whether key is one an entry may hold; typed reports whether
// value holds the type that key takes.
func (entry *Entry) set(key string, value any) (known, typed bool) {
	switch key {
	case "name":
		return true, assign(value, &entry.Name)
	case "enabled":
		return true, assignPtr(value, &entry.Enabled)
	case "stage":
		return true, assignList(value, &entry.Stage)
	case "must":
		return true, assignList(value, &entry.Must)
	case "must_not":
		return true, assignList(value, &entry.MustNot)
	case keyPrompt:
		return true, assignPtr(value, &entry.Prompt)
	case "agent":
		return true, assignPtr(value, &entry.Agent)
	case "model":
		return true, assignPtr(value, &entry.Model)
	case "tools":
		return true, assignList(value, &entry.Tools)
	case "effort":
		return true, assignPtr(value, &entry.Effort)
	case "limit":
		return true, assignPtr(value, &entry.Limit)
	case "verify":
		return true, assignPtr(value, &entry.Verify)
	}
	return false, false
}

// assign stores value in dst when value holds a T, and reports whether it does.
func assign[T any](value any, dst *T) bool {
	v, ok := value.(T)
	if ok {
		*dst = v
	}
	return ok
}

// assignPtr stores a pointer to value in dst when value holds a T, and reports whether it does.
func assignPtr[T any](value any, dst **T) bool {
	v, ok := value.(T)
	if ok {
		*dst = &v
	}
	return ok
}

// assignList stores value in dst when value is an array of strings, and reports whether it is. An empty array stores
// an empty slice that is not nil, so the key reads as set.
func assignList(value any, dst *[]string) bool {
	items, ok := value.([]any)
	if !ok {
		return false
	}
	list := make([]string, 0, len(items))
	for _, item := range items {
		s, isString := item.(string)
		if !isString {
			return false
		}
		list = append(list, s)
	}
	*dst = list
	return true
}
