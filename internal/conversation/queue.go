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

// systemType is the type of a line that Claude Code writes for the system. A turn end is one of its subtypes.
const systemType = "system"

// turnDurationSubtype is the subtype of the system line that Claude Code writes when a turn ends.
const turnDurationSubtype = "turn_duration"

// Waiting is one message in Claude Code's queue: its text, cut to maxTextBytes, the unix time it was queued, and
// whether the person canceled it.
type Waiting struct {
	Text     string `json:"text"`
	Time     int64  `json:"time"`
	Canceled bool   `json:"canceled"`
}

// queueRow is a message that the queue holds. Its text is the full text, which matching uses. mark is where the row
// stands after a dequeue record, and dequeued is set while the row holds a mark. seq is the row's place among the rows
// its transcript enqueued, so a caller can tell rows apart as they leave the queue.
type queueRow struct {
	seq      int
	text     string
	time     int64
	canceled bool
	dequeued bool
	mark     dequeueMark
}

// dequeueMark is where a row stands after a dequeue record. A dequeue marks each unmarked row that waits undecided, and
// a row it finds undecided or settled keeps its mark. The first user line with text after it settles the undecided
// rows, or when it is a notice, closes the ones it leads and unmarks the rest.
type dequeueMark int

const (
	unmarked dequeueMark = iota
	undecided
	settled
)

// queueLine holds the fields of a transcript line that the queue rules use: a queue record's operation, content and
// reason, a system line's subtype, and the fields of a user line that transcriptLine reads.
type queueLine struct {
	transcriptLine
	Operation string `json:"operation"`
	Content   string `json:"content"`
	Reason    string `json:"reason"`
	Subtype   string `json:"subtype"`
}

// queueFile is what Queues remembers of one transcript: the file it read, how many of its bytes were read, and the
// rows that those bytes leave waiting, oldest first.
type queueFile struct {
	info    fs.FileInfo
	offset  int64
	nextSeq int
	rows    []queueRow
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
// message waits from its enqueue record until a record closes it. A remove or popAll with the same text closes it, and
// so does a typed, queued or accepted user line whose text contains it. A dequeue record that comes while the message
// waits marks it undecided until the first later user line with text decides it. A notice that the message leads with
// closes it, another notice unmarks it, and any other line settles it, so the next turn end closes it. A user line with
// the same text closes a marked message whatever that line's promptSource. A remove with reason cleared_on_cancel
// marks the message canceled instead. Each enqueue record that opens a message drops the canceled messages, and each
// typed, queued or accepted user line does too. When idle is true, every message that is not canceled is dropped for
// good, so only the canceled ones are returned.
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

// scan reads the complete lines of file from the remembered offset to size and applies each queue record, user line
// and turn end it finds. A trailing line without a newline is left unread until a later call sees its newline.
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
		s.feed(line)
	}
}

// feed applies one complete line to the rows. Only a queue record, a user line or a turn end can change them, so any
// other line is skipped before it is parsed. The replay feeds its lines through here too, so both apply one rule.
func (s *queueFile) feed(line []byte) {
	if bytes.Contains(line, []byte(queueOperation)) || bytes.Contains(line, []byte(`"user"`)) ||
		bytes.Contains(line, []byte(turnDurationSubtype)) {
		s.apply(line)
	}
}

// apply updates the rows from one line. A line that is neither a queue record, a user line nor a turn end leaves them
// unchanged.
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
	case systemType:
		s.applySystem(entry)
	}
}

// applyOperation updates the rows from one queue record.
func (s *queueFile) applyOperation(entry queueLine) {
	switch entry.Operation {
	case "enqueue":
		if entry.Content != "" && !fromOthers(entry.Content) {
			s.dropCanceled()
			s.rows = append(s.rows, queueRow{seq: s.nextSeq, text: entry.Content, time: unixSeconds(entry.Timestamp)})
			s.nextSeq++
		}
	case "remove":
		if entry.Reason == "cleared_on_cancel" {
			s.cancel(entry.Content)
		} else {
			s.close(entry.Content)
		}
	case "popAll":
		s.close(entry.Content)
	case "dequeue":
		s.markDequeued()
	}
}

// applyUser updates the rows from one user line. The line first decides the rows that a dequeue left undecided. Then a
// line the person typed, queued or accepted from a suggestion drops the canceled rows and closes each row whose text it
// contains, and any other user line closes the row whose text it equals when that row is marked.
func (s *queueFile) applyUser(entry queueLine) {
	text, _ := splitContent(entry.Message.Content)
	s.decide(entry, text)
	if typedLine(entry.transcriptLine) {
		s.dropCanceled()
		s.closeContained(text)
		return
	}
	s.closeDequeued(text)
}

// decide applies the first user line with text after a dequeue to the undecided rows. A notice closes each undecided,
// uncanceled row that it leads and unmarks the other undecided rows. Any other line settles the undecided rows, so the
// next turn end closes them. A line without text decides nothing.
func (s *queueFile) decide(entry queueLine, text string) {
	if text == "" {
		return
	}
	if !isNotice(entry, text) {
		s.remark(undecided, settled)
		return
	}
	s.rows = slices.DeleteFunc(s.rows, func(row queueRow) bool {
		return row.mark == undecided && !row.canceled && leads(text, row.text)
	})
	s.remark(undecided, unmarked)
}

// remark sets the mark of each row that holds from to to, and sets dequeued when to is a mark.
func (s *queueFile) remark(from, to dequeueMark) {
	for at := range s.rows {
		if s.rows[at].mark == from {
			s.rows[at].mark = to
			s.rows[at].dequeued = to != unmarked
		}
	}
}

// applySystem updates the rows from one system line. A turn end closes the settled rows that are not canceled.
func (s *queueFile) applySystem(entry queueLine) {
	if entry.Subtype == turnDurationSubtype {
		s.rows = slices.DeleteFunc(s.rows, func(row queueRow) bool { return row.mark == settled && !row.canceled })
	}
}

// markDequeued marks every unmarked row that waits as undecided. A dequeue record names no message, so it applies to the
// rows that are waiting when it comes. Rows enqueued after it are not marked, and a row that an earlier dequeue marked
// keeps its mark, so a settled row stays settled.
func (s *queueFile) markDequeued() {
	s.remark(unmarked, undecided)
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

// isNotice reports whether a user line is a notice rather than the person's text: its text is one fromOthers
// recognizes, or Claude Code marks its origin as a task notification.
func isNotice(entry queueLine, text string) bool {
	return entry.Origin.Kind == "task-notification" || fromOthers(text)
}

// leads reports whether notice starts with rowText, followed by a newline or by the end of the text.
func leads(notice, rowText string) bool {
	rest, found := strings.CutPrefix(notice, rowText)
	return found && (rest == "" || rest[0] == '\n')
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

// closeDequeued removes the oldest row whose text is text when that row is marked. The rows a dequeue marked are all
// older than the rows enqueued after it, so the oldest row with text is the marked one whenever a marked row has text.
func (s *queueFile) closeDequeued(text string) {
	if at := s.find(text); at >= 0 && s.rows[at].mark != unmarked {
		s.rows = slices.Delete(s.rows, at, at+1)
	}
}

// closeContained removes the oldest row of each distinct text that text contains.
func (s *queueFile) closeContained(text string) {
	var contained []string
	for _, row := range s.rows {
		if strings.Contains(text, row.text) && !slices.Contains(contained, row.text) {
			contained = append(contained, row.text)
		}
	}
	for _, rowText := range contained {
		s.close(rowText)
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
