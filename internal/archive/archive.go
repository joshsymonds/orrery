// Package archive records closed Claude panes as JSON lines so a closed conversation can be resumed.
package archive

import (
	"bufio"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

// claudeSessionKey is the wire key of Record.ClaudeSession. The field has no struct tag: MarshalJSON
// appends the key last and UnmarshalJSON reads it by this constant.
const claudeSessionKey = "claude_session"

// Record describes one closed Claude pane.
type Record struct {
	Dir           string `json:"dir"`
	Session       string `json:"session"`
	ClaudeSession string `json:"-"`
	Transcript    string `json:"transcript"`
	Label         string `json:"label"`
	Summary       string `json:"summary"`
	Closed        int64  `json:"closed"`
}

// recordFields is Record without its methods, so encoding it does not recurse into MarshalJSON.
type recordFields Record

// MarshalJSON encodes the tagged fields in order, then claude_session as the last key.
func (r Record) MarshalJSON() ([]byte, error) {
	body, err := json.Marshal(recordFields(r))
	if err != nil {
		return nil, fmt.Errorf("encode archive record: %w", err)
	}
	session, err := json.Marshal(r.ClaudeSession)
	if err != nil {
		return nil, fmt.Errorf("encode claude session: %w", err)
	}
	out := append([]byte(nil), body[:len(body)-1]...)
	return fmt.Appendf(out, `,"%s":%s}`, claudeSessionKey, session), nil
}

// UnmarshalJSON decodes the tagged fields, then reads claude_session by its key. An absent key leaves
// ClaudeSession empty; a non-string value is an error.
func (r *Record) UnmarshalJSON(data []byte) error {
	var fields recordFields
	if err := json.Unmarshal(data, &fields); err != nil {
		return fmt.Errorf("decode archive record: %w", err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		return fmt.Errorf("decode archive record: %w", err)
	}
	*r = Record(fields)
	raw, ok := keys[claudeSessionKey]
	if !ok {
		return nil
	}
	if err := json.Unmarshal(raw, &r.ClaudeSession); err != nil {
		return fmt.Errorf("decode %s: %w", claudeSessionKey, err)
	}
	return nil
}

// Path resolves the archive file: $XDG_STATE_HOME/orrery/archive.jsonl when XDG_STATE_HOME is set,
// else $HOME/.local/state/orrery/archive.jsonl. It returns an error when both are unset.
func Path(getenv func(string) string) (string, error) {
	if stateHome := getenv("XDG_STATE_HOME"); stateHome != "" {
		return filepath.Join(stateHome, "orrery", "archive.jsonl"), nil
	}
	if home := getenv("HOME"); home != "" {
		return filepath.Join(home, ".local", "state", "orrery", "archive.jsonl"), nil
	}
	return "", fmt.Errorf("no archive path: set XDG_STATE_HOME or HOME")
}

// Append writes record as one JSON line at the end of path, creating the parent directory and the file
// when absent. Existing lines are never rewritten.
func Append(path string, record Record) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create archive directory: %w", err)
	}
	line, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode archive record: %w", err)
	}
	file, err := os.OpenFile(filepath.Clean(path), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	_, writeErr := file.Write(append(line, '\n'))
	if err = errors.Join(writeErr, file.Close()); err != nil {
		return fmt.Errorf("write archive: %w", err)
	}
	return nil
}

// ForDir returns the records in path whose Dir equals dir, newest first: by Closed descending, and
// records with equal Closed in reverse append order. A missing file yields no records and no error.
// A malformed line is an error naming its line number.
func ForDir(path, dir string) ([]Record, error) {
	matches, err := readArchive(path, func(record Record) bool { return record.Dir == dir })
	if err != nil {
		return nil, err
	}
	return newestFirst(matches), nil
}

// Dirs returns the distinct Dir values of the records in path, newest first in ForDir's order: each dir
// appears once, at the position of its newest record. Records with an empty Dir are left out. A missing
// file yields no dirs and no error. A malformed line is an error naming its line number.
func Dirs(path string) ([]string, error) {
	records, err := readArchive(path, func(record Record) bool { return record.Dir != "" })
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var dirs []string
	for _, record := range newestFirst(records) {
		if !seen[record.Dir] {
			seen[record.Dir] = true
			dirs = append(dirs, record.Dir)
		}
	}
	return dirs, nil
}

// readArchive returns the records in path that keep accepts, in append order. Every line is decoded,
// kept or not, so a malformed line anywhere is an error. A missing file yields no records and no error.
func readArchive(path string, keep func(Record) bool) ([]Record, error) {
	file, err := os.Open(filepath.Clean(path))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	defer func() { _ = file.Close() }()

	reader := bufio.NewReader(file)
	var kept []Record
	for lineNo := 1; ; lineNo++ {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			var record Record
			if err = json.Unmarshal(line, &record); err != nil {
				return nil, fmt.Errorf("archive line %d: %w", lineNo, err)
			}
			if keep(record) {
				kept = append(kept, record)
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("read archive: %w", readErr)
		}
	}
	return kept, nil
}

// newestFirst orders records by Closed descending, with records of equal Closed in reverse append order.
// It reorders records in place and returns it.
func newestFirst(records []Record) []Record {
	slices.Reverse(records)
	slices.SortStableFunc(records, func(a, b Record) int { return cmp.Compare(b.Closed, a.Closed) })
	return records
}
