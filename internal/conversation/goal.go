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

// goalReadSize is the buffer size of the reader that scans a transcript.
const goalReadSize = 64 << 10

// goalLine holds the fields of a transcript line that a goal record uses.
type goalLine struct {
	Type       string `json:"type"`
	Attachment struct {
		Type      string `json:"type"`
		Condition string `json:"condition"`
		Met       bool   `json:"met"`
	} `json:"attachment"`
}

// goalFile is what Goals remembers of one transcript: the file it read, how many of its bytes were read, and
// the goal those bytes leave active.
type goalFile struct {
	info   fs.FileInfo
	offset int64
	goal   string
}

// Goals reports the active goal of transcripts. For each path it remembers how far it has read and the goal that
// read left active, so a later call reads only the bytes appended since. Goals is safe for concurrent use.
type Goals struct {
	mu    sync.Mutex
	files map[string]*goalFile
}

// NewGoals returns a Goals that has read no transcript.
func NewGoals() *Goals {
	return &Goals{files: map[string]*goalFile{}}
}

// Goal returns the active goal of the transcript at path, or "" when none is active. The newest goal record
// in the transcript decides it: a record that is not met gives its condition, cut to maxRunes runes, and a
// met record gives "".
func (g *Goals) Goal(path string) (string, error) {
	clean := filepath.Clean(path)
	file, err := os.Open(clean)
	if err != nil {
		return "", fmt.Errorf("open transcript: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("stat transcript: %w", err)
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	state := g.files[clean]
	if state == nil || state.offset > info.Size() || !os.SameFile(state.info, info) {
		state = &goalFile{}
		g.files[clean] = state
	}
	state.info = info
	if scanErr := state.scan(file, info.Size()); scanErr != nil {
		return "", scanErr
	}
	return state.goal, nil
}

// scan reads the complete lines of file from the remembered offset to size and applies each goal record it
// finds. A trailing line without a newline is left unread until a later call sees its newline.
func (s *goalFile) scan(file *os.File, size int64) error {
	reader := bufio.NewReaderSize(io.NewSectionReader(file, s.offset, size-s.offset), goalReadSize)
	for {
		line, err := reader.ReadBytes('\n')
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read transcript: %w", err)
		}
		s.offset += int64(len(line))
		if bytes.Contains(line, []byte(goalStatus)) {
			s.apply(line)
		}
	}
}

// apply sets the goal from one goal record line. A line that is not a goal record leaves the goal unchanged.
func (s *goalFile) apply(line []byte) {
	var entry goalLine
	if json.Unmarshal(line, &entry) != nil || entry.Type != "attachment" || entry.Attachment.Type != goalStatus {
		return
	}
	if entry.Attachment.Met {
		s.goal = ""
		return
	}
	s.goal = cutRunes(entry.Attachment.Condition)
}
