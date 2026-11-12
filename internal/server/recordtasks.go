package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"sort"
	"strconv"
)

// taskRecord is one task from an effort's state.json, with its text as the record wrote it.
type taskRecord struct {
	Effort                int
	Slug, Subject, Status string
}

// readTasks lists the tasks of the epic whose record directory is dir: every efforts/<n>/state.json, effort number
// ascending and file order within each. Only directories named in decimal digits are efforts. A state.json that
// does not parse, or whose "tasks" is not an array, is reported in problems and skipped; only an unexpected I/O
// failure is an error. An epic without an efforts directory has no tasks. Records are only read.
func readTasks(dir string) ([]taskRecord, []string, error) {
	fsys := os.DirFS(dir)
	entries, err := fs.ReadDir(fsys, "efforts")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read efforts: %w", err)
	}
	type effort struct {
		number int
		name   string
	}
	var efforts []effort
	for _, entry := range entries {
		if number, ok := effortNumber(entry); ok {
			efforts = append(efforts, effort{number: number, name: entry.Name()})
		}
	}
	sort.SliceStable(efforts, func(i, j int) bool { return efforts[i].number < efforts[j].number })

	var tasks []taskRecord
	var problems []string
	for _, candidate := range efforts {
		statePath := path.Join("efforts", candidate.name, "state.json")
		raw, readErr := fs.ReadFile(fsys, statePath)
		if errors.Is(readErr, fs.ErrNotExist) {
			continue
		}
		if readErr != nil {
			return nil, nil, fmt.Errorf("read effort state: %w", readErr)
		}
		found, ok := parseEffortTasks(raw, candidate.number)
		if !ok {
			problems = append(problems, statePath+": does not parse")
			continue
		}
		tasks = append(tasks, found...)
	}
	return tasks, problems, nil
}

// effortNumber reports the effort number an entry under efforts/ names: a directory whose name is all decimal
// digits. A name too large for an int names no effort.
func effortNumber(entry fs.DirEntry) (int, bool) {
	if !entry.IsDir() {
		return 0, false
	}
	name := entry.Name()
	for _, c := range name {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	number, err := strconv.Atoi(name)
	if err != nil {
		return 0, false
	}
	return number, true
}

// parseEffortTasks reads the tasks of one effort's state.json. It reports false when raw does not parse or its
// "tasks" is not an array.
func parseEffortTasks(raw []byte, effort int) ([]taskRecord, bool) {
	var state struct {
		Tasks json.RawMessage `json:"tasks"`
	}
	if json.Unmarshal(raw, &state) != nil {
		return nil, false
	}
	tasksJSON := bytes.TrimSpace(state.Tasks)
	if len(tasksJSON) == 0 || tasksJSON[0] != '[' {
		return nil, false
	}
	var list []struct {
		Slug    json.RawMessage `json:"slug"`
		Subject json.RawMessage `json:"subject"`
		Status  json.RawMessage `json:"status"`
	}
	if json.Unmarshal(tasksJSON, &list) != nil {
		return nil, false
	}
	found := make([]taskRecord, 0, len(list))
	for _, task := range list {
		found = append(found, taskRecord{
			Effort:  effort,
			Slug:    recordText(task.Slug),
			Subject: recordText(task.Subject),
			Status:  recordText(task.Status),
		})
	}
	return found, true
}
