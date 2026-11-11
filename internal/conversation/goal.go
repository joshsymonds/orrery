package conversation

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// goalStatus is the attachment type of a goal record, and the substring that every goal record holds.
const goalStatus = "goal_status"

// readSize is the buffer size of the reader that scans a transcript.
const readSize = 64 << 10

// goalLine holds the fields of a transcript line that a goal record uses.
type goalLine struct {
	Type       string `json:"type"`
	Attachment struct {
		Type      string `json:"type"`
		Condition string `json:"condition"`
		Met       bool   `json:"met"`
	} `json:"attachment"`
}

// trackedFile is what a fileTracker remembers of one transcript: the file it read, how many of its bytes were
// read, and the state those bytes leave.
type trackedFile[S any] struct {
	info   fs.FileInfo
	offset int64
	state  S
}

// fileTracker reads transcripts incrementally. For each path it remembers how far it has read and the state that
// read left, so a later call reads only the complete lines appended since. fileTracker is safe for concurrent use.
type fileTracker[S any] struct {
	mu    sync.Mutex
	files map[string]*trackedFile[S]
}

// newFileTracker returns a fileTracker that has read no transcript.
func newFileTracker[S any]() *fileTracker[S] {
	return &fileTracker[S]{files: map[string]*trackedFile[S]{}}
}

// read applies each complete line of the transcript at path past the remembered offset to the state, and returns
// the state. A transcript that shrank or was replaced is read from its start.
func (t *fileTracker[S]) read(path string, apply func(state *S, line []byte)) (S, error) {
	var zero S
	clean := filepath.Clean(path)
	file, err := os.Open(clean)
	if err != nil {
		return zero, fmt.Errorf("open transcript: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return zero, fmt.Errorf("stat transcript: %w", err)
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	tracked := t.files[clean]
	if tracked == nil || tracked.offset > info.Size() || !os.SameFile(tracked.info, info) {
		tracked = &trackedFile[S]{}
		t.files[clean] = tracked
	}
	tracked.info = info
	if scanErr := tracked.scan(file, info.Size(), apply); scanErr != nil {
		return zero, scanErr
	}
	return tracked.state, nil
}

// scan reads the complete lines of file from the remembered offset to size and applies each one to the state. A
// trailing line without a newline is left unread until a later call sees its newline.
func (f *trackedFile[S]) scan(file *os.File, size int64, apply func(state *S, line []byte)) error {
	reader := bufio.NewReaderSize(io.NewSectionReader(file, f.offset, size-f.offset), readSize)
	for {
		line, err := reader.ReadBytes('\n')
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read transcript: %w", err)
		}
		f.offset += int64(len(line))
		apply(&f.state, line)
	}
}

// Goals reports the active goal of transcripts. For each path it remembers how far it has read and the goal that
// read left active, so a later call reads only the bytes appended since. Goals is safe for concurrent use.
type Goals struct {
	tracker *fileTracker[string]
}

// NewGoals returns a Goals that has read no transcript.
func NewGoals() *Goals {
	return &Goals{tracker: newFileTracker[string]()}
}

// Goal returns the active goal of the transcript at path, or "" when none is active. The newest goal record
// in the transcript decides it: a record that is not met gives its condition, cut to maxRunes runes, and a
// met record gives "".
func (g *Goals) Goal(path string) (string, error) {
	return g.tracker.read(path, applyGoalLine)
}

// applyGoalLine sets the goal from one transcript line. A line that is not a goal record leaves the goal unchanged.
func applyGoalLine(goal *string, line []byte) {
	if !bytes.Contains(line, []byte(goalStatus)) {
		return
	}
	var entry goalLine
	if json.Unmarshal(line, &entry) != nil || entry.Type != "attachment" || entry.Attachment.Type != goalStatus {
		return
	}
	if entry.Attachment.Met {
		*goal = ""
		return
	}
	*goal = cutRunes(entry.Attachment.Condition)
}
