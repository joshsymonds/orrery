package enginehost

import (
	"fmt"
	"maps"
	"slices"

	"github.com/BurntSushi/toml"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// projectReleaseKey is the top-level key of a project file's release table.
const projectReleaseKey = "release"

// parseProjectRelease reads the [release] table of a project file: an optional then, and the steps of the release in
// file order. The other top-level keys belong to their own readers and are ignored. found reports whether the file
// has a release key; it is false, with a nil error, when it does not. A file that is not TOML comes back wrapped. A
// release table that is malformed is refused with an *epicFieldError naming the field.
func parseProjectRelease(data []byte) (steps []*v1.EpicReleaseStep, then string, found bool, err error) {
	var doc map[string]any
	if _, decodeErr := toml.Decode(string(data), &doc); decodeErr != nil {
		return nil, "", false, fmt.Errorf("parse project release: %w", decodeErr)
	}
	raw, found := doc[projectReleaseKey]
	if !found {
		return nil, "", false, nil
	}
	table, isTable := raw.(map[string]any)
	if !isTable {
		return nil, "", true, &epicFieldError{field: projectReleaseKey, reason: "not a table"}
	}
	// The keys are checked in name order, so the refusal for a file with several wrong keys is the same every time.
	var tables []map[string]any
	for _, key := range slices.Sorted(maps.Keys(table)) {
		switch key {
		case "steps":
			tables, err = releaseStepTables(table[key])
			if err != nil {
				return nil, "", true, err
			}
		case "then":
			text, isString := table[key].(string)
			if !isString {
				return nil, "", true, &epicFieldError{field: projectReleaseKey + ".then", reason: "not a string"}
			}
			then = text
		default:
			return nil, "", true, &epicFieldError{field: projectReleaseKey + "." + key, reason: "unknown key"}
		}
	}
	if _, has := table["steps"]; !has {
		return nil, "", true, &epicFieldError{field: projectReleaseKey + ".steps", reason: epicReasonMissing}
	}
	for i, stepTable := range tables {
		step, stepErr := parseReleaseStep(i, stepTable)
		if stepErr != nil {
			return nil, "", true, stepErr
		}
		steps = append(steps, step)
	}
	return steps, then, true, nil
}

// releaseStepTables returns the step tables of the release's steps key. The key must be an array of tables, and it
// must hold at least one table; an empty array counts as none.
func releaseStepTables(value any) ([]map[string]any, error) {
	if list, isList := value.([]any); isList && len(list) == 0 {
		return nil, &epicFieldError{field: projectReleaseKey + ".steps", reason: epicReasonNone}
	}
	tables, isTables := value.([]map[string]any)
	if !isTables {
		return nil, &epicFieldError{field: projectReleaseKey + ".steps", reason: "not an array of tables"}
	}
	return tables, nil
}

// parseReleaseStep reads the table of the step at index i. It holds step, target and undo, each a string. The step
// and the undo must not be blank after trimming, and a missing one counts as blank. The keys are checked in name
// order.
func parseReleaseStep(i int, table map[string]any) (*v1.EpicReleaseStep, error) {
	step := &v1.EpicReleaseStep{}
	path := fmt.Sprintf("%s.steps[%d]", projectReleaseKey, i)
	fields := map[string]*string{"step": &step.Step, "target": &step.Target, "undo": &step.Undo}
	for _, key := range slices.Sorted(maps.Keys(table)) {
		field := path + "." + key
		dst, known := fields[key]
		if !known {
			return nil, &epicFieldError{field: field, reason: "unknown key"}
		}
		text, isString := table[key].(string)
		if !isString {
			return nil, &epicFieldError{field: field, reason: "not a string"}
		}
		*dst = text
	}
	if isBlank(step.Step) {
		return nil, &epicFieldError{field: path + ".step", reason: epicReasonEmpty}
	}
	if isBlank(step.Undo) {
		return nil, &epicFieldError{field: path + ".undo", reason: epicReasonEmpty}
	}
	return step, nil
}
