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
	"regexp"
	"slices"
	"strings"
	"sync"
)

// queueOperation is the type of a record that Claude Code writes when the queue changes.
const queueOperation = "queue-operation"

// promptSourceKey is the key that a user line carries when the person's prompt came from the typing, the queue or a
// suggestion. Every user line the queue rules read holds it.
const promptSourceKey = `"promptSource"`

// Waiting is one message in Claude Code's queue: its text, cut to maxTextBytes, the unix time it was queued, and
// whether the person canceled it.
type Waiting struct {
	Text     string `json:"text"`
	Time     int64  `json:"time"`
	Canceled bool   `json:"canceled"`
}

// queueRow is a message that the queue holds. Its text is the full text, which matching uses.
type queueRow struct {
	text     string
	time     int64
	canceled bool
}

// queueLine holds the fields of a transcript line that the queue rules use: a queue record's operation, content and
// reason, and the fields of a user line that transcriptLine reads.
type queueLine struct {
	transcriptLine
	Operation string `json:"operation"`
	Content   string `json:"content"`
	Reason    string `json:"reason"`
}

// queueFile is what Queues remembers of one transcript: the file it read, how many of its bytes were read, and the
// rows that those bytes leave waiting, oldest first.
type queueFile struct {
	info   fs.FileInfo
	offset int64
	rows   []queueRow
}

// Queues reports the waiting messages of transcripts. For each path it remembers how far it has read and the rows
// that read left waiting, so a later call reads only the bytes appended since. Queues is safe for concurrent use.
type Queues struct {
	mu    sync.Mutex
	files map[string]*queueFile
}

// NewQueues returns a Queues that has read no transcript.
func NewQueues() *Queues {
	return &Queues{files: map[string]*queueFile{}}
}

// Waiting returns the messages that wait in the queue of the transcript at path, oldest first, and never nil. A
// message waits from its enqueue record until a record closes it: a remove or popAll with the same text, or a
// queued user line with the same text. A remove with reason cleared_on_cancel marks the message canceled instead.
// Each enqueue record that opens a message drops the canceled messages, and each typed, queued or accepted user line
// does too. When idle is true, every message that is not canceled is dropped for good, so only the canceled ones are
// returned.
func (q *Queues) Waiting(path string, idle bool) ([]Waiting, error) {
	clean := filepath.Clean(path)
	file, err := os.Open(clean)
	if err != nil {
		return nil, fmt.Errorf("open transcript: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat transcript: %w", err)
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	state := q.files[clean]
	if state == nil || state.offset > info.Size() || !os.SameFile(state.info, info) {
		state = &queueFile{}
		q.files[clean] = state
	}
	state.info = info
	if scanErr := state.scan(file, info.Size()); scanErr != nil {
		return nil, scanErr
	}
	if idle {
		state.rows = slices.DeleteFunc(state.rows, func(row queueRow) bool { return !row.canceled })
	}
	return state.waiting(), nil
}

// scan reads the complete lines of file from the remembered offset to size and applies each queue record and user
// line it finds. A trailing line without a newline is left unread until a later call sees its newline.
func (s *queueFile) scan(file *os.File, size int64) error {
	reader := bufio.NewReader(io.NewSectionReader(file, s.offset, size-s.offset))
	for {
		line, err := reader.ReadBytes('\n')
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read transcript: %w", err)
		}
		s.offset += int64(len(line))
		if bytes.Contains(line, []byte(queueOperation)) || bytes.Contains(line, []byte(promptSourceKey)) {
			s.apply(line)
		}
	}
}

// apply updates the rows from one line. A line that is neither a queue record nor a user line leaves them unchanged.
func (s *queueFile) apply(line []byte) {
	var entry queueLine
	if json.Unmarshal(line, &entry) != nil {
		return
	}
	switch entry.Type {
	case queueOperation:
		s.applyOperation(entry)
	case "user":
		s.applyUser(entry)
	}
}

// applyOperation updates the rows from one queue record.
func (s *queueFile) applyOperation(entry queueLine) {
	switch entry.Operation {
	case "enqueue":
		if entry.Content != "" && !fromOthers(entry.Content) {
			s.dropCanceled()
			s.rows = append(s.rows, queueRow{text: entry.Content, time: unixSeconds(entry.Timestamp)})
		}
	case "remove":
		if entry.Reason == "cleared_on_cancel" {
			s.cancel(entry.Content)
		} else {
			s.close(entry.Content)
		}
	case "popAll":
		s.close(entry.Content)
	}
}

// applyUser updates the rows from one user line. A line the person typed, queued or accepted from a suggestion drops
// the canceled rows, and a queued line closes the row with its text.
func (s *queueFile) applyUser(entry queueLine) {
	if !typedLine(entry.transcriptLine) {
		return
	}
	s.dropCanceled()
	if entry.PromptSource == "queued" {
		text, _ := splitContent(entry.Message.Content)
		s.close(text)
	}
}

// stoppedAgentsPattern matches the plain notice that Claude Code enqueues when it stops background agents: one or more
// digits and the fixed phrase, at the start of the text.
var stoppedAgentsPattern = regexp.MustCompile(`\A[0-9]+ background agents were stopped`)

// fromOthers reports whether an enqueue's text belongs to a task notification, an agent message, a cross-session
// message, a teammate message or the stopped-agents notice rather than to the person.
func fromOthers(text string) bool {
	return strings.HasPrefix(text, "<task-notification>") ||
		strings.HasPrefix(text, "<agent-message") ||
		strings.HasPrefix(text, "<cross-session-message") ||
		strings.HasPrefix(text, "<teammate-message") ||
		stoppedAgentsPattern.MatchString(text)
}

// find returns the index of the oldest row that is not canceled and whose text is text, or -1 when there is none.
func (s *queueFile) find(text string) int {
	for at, row := range s.rows {
		if !row.canceled && row.text == text {
			return at
		}
	}
	return -1
}

// close removes the oldest row whose text is text.
func (s *queueFile) close(text string) {
	if at := s.find(text); at >= 0 {
		s.rows = slices.Delete(s.rows, at, at+1)
	}
}

// cancel marks the oldest row whose text is text as canceled.
func (s *queueFile) cancel(text string) {
	if at := s.find(text); at >= 0 {
		s.rows[at].canceled = true
	}
}

// dropCanceled removes every canceled row.
func (s *queueFile) dropCanceled() {
	s.rows = slices.DeleteFunc(s.rows, func(row queueRow) bool { return row.canceled })
}

// waiting returns the rows as Waiting values, with each text cut to maxTextBytes. It is never nil.
func (s *queueFile) waiting() []Waiting {
	out := make([]Waiting, 0, len(s.rows))
	for _, row := range s.rows {
		out = append(out, Waiting{Text: cutText(row.text, maxTextBytes), Time: row.time, Canceled: row.canceled})
	}
	return out
}
