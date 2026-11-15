package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// taskRecord is one task from an effort's state.json, with its text as the record wrote it.
type taskRecord struct {
	Effort                int
	Slug, Subject, Status string
	Requirements          []string
}

// readTasks lists the tasks of the epic whose record directory is dir: every efforts/<n>/state.json, effort number
// ascending and file order within each. Only directories named in decimal digits are efforts. A state.json that
// does not parse, or whose "tasks" is not an array, is reported in problems and skipped; only an unexpected I/O
// failure is an error. An epic without an efforts directory has no tasks. Records are only read. Each state.json is
// stat'd on every call, and decoded through heads only when its size or mtime changed.
func readTasks(dir string, heads *headCache) ([]taskRecord, []string, error) {
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
		info, statErr := fs.Stat(fsys, statePath)
		if errors.Is(statErr, fs.ErrNotExist) {
			continue
		}
		if statErr != nil {
			return nil, nil, fmt.Errorf("stat effort state: %w", statErr)
		}
		key := filepath.Join(dir, statePath)
		entry, ok := heads.lookup(key, info)
		if !ok {
			raw, readErr := fs.ReadFile(fsys, statePath)
			if errors.Is(readErr, fs.ErrNotExist) {
				continue
			}
			if readErr != nil {
				return nil, nil, fmt.Errorf("read effort state: %w", readErr)
			}
			entry = decodeEffort(raw, candidate.number)
			heads.store(key, info, entry)
		}
		if !entry.parsed {
			problems = append(problems, statePath+": does not parse")
			continue
		}
		tasks = append(tasks, entry.tasks...)
	}
	return tasks, problems, nil
}

// decodeEffort decodes the content of one effort's state.json, as effort, into the entry readTasks keeps.
func decodeEffort(raw []byte, effort int) headEntry {
	found, ok := parseEffortTasks(raw, effort)
	return headEntry{parsed: ok, tasks: found}
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
		Slug        json.RawMessage `json:"slug"`
		Subject     json.RawMessage `json:"subject"`
		Status      json.RawMessage `json:"status"`
		Requirement json.RawMessage `json:"requirement"`
	}
	if json.Unmarshal(tasksJSON, &list) != nil {
		return nil, false
	}
	found := make([]taskRecord, 0, len(list))
	for _, task := range list {
		found = append(found, taskRecord{
			Effort:       effort,
			Slug:         recordText(task.Slug),
			Subject:      recordText(task.Subject),
			Status:       recordText(task.Status),
			Requirements: recordRequirements(task.Requirement),
		})
	}
	return found, true
}

// recordRequirements splits a task's requirement text on commas, each part trimmed, empty parts dropped, in order. A
// requirement that is missing, empty or not a JSON string gives none.
func recordRequirements(raw json.RawMessage) []string {
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return nil
	}
	var ids []string
	for part := range strings.SplitSeq(text, ",") {
		if id := strings.TrimSpace(part); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}
