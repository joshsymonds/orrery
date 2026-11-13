package conversation

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// QueueReplay holds the counts that ReplayQueues makes over the transcripts it reads. It holds no message text.
type QueueReplay struct {
	// Transcripts is the number of files kept.
	Transcripts int
	// Typed is the number of enqueues that opened a row.
	Typed int
	// Closed is the number of rows that a close took out of the queue, canceled rows included.
	Closed int
	// NeverTakenBefore is the number of rows open at the end of their file with no dequeue after their enqueue,
	// enqueued before the cutoff.
	NeverTakenBefore int
	// NeverTakenAfter is the number of rows open at the end of their file with no dequeue after their enqueue,
	// enqueued at or after the cutoff.
	NeverTakenAfter int
	// Other is the number of rows open at the end of their file with a dequeue after their enqueue.
	Other int
	// Falsified is the number of closes that a later line contradicts.
	Falsified int
	// IdleHidden is the number of rows that the idle rule could drop and that later lines show were still queued.
	IdleHidden int
	// NoticesTyped is the number of rejected enqueues whose text later arrives as a typed user line.
	NoticesTyped int
}

// Write prints one "name count" line for each count, in the order of the fields, and nothing else.
func (r QueueReplay) Write(writer io.Writer) error {
	counts := []struct {
		name  string
		count int
	}{
		{"transcripts", r.Transcripts},
		{"typed", r.Typed},
		{"closed", r.Closed},
		{"never_taken_before", r.NeverTakenBefore},
		{"never_taken_after", r.NeverTakenAfter},
		{"other", r.Other},
		{"falsified", r.Falsified},
		{"idle_hidden", r.IdleHidden},
		{"notices_typed", r.NoticesTyped},
	}
	for _, count := range counts {
		if _, err := fmt.Fprintf(writer, "%s %d\n", count.name, count.count); err != nil {
			return fmt.Errorf("write queue replay: %w", err)
		}
	}
	return nil
}

// ReplayQueues replays the transcripts under root that the selection keeps. Each line goes through the same rule that
// Queues uses, and each row is watched as it enters and leaves the queue. A row still open at the end of its file and
// enqueued before before counts as never taken before, and one enqueued at or after it counts as never taken after.
func ReplayQueues(root string, before time.Time) (QueueReplay, error) {
	paths, err := transcriptPaths(root)
	if err != nil {
		return QueueReplay{}, err
	}
	replay := QueueReplay{Transcripts: len(paths)}
	for _, path := range paths {
		err = replayTranscript(path, before, &replay)
		if err != nil {
			return QueueReplay{}, err
		}
	}
	return replay, nil
}

// transcriptPaths returns the kept transcripts under root, in name order. A transcript is a file one directory below
// root; files deeper down, such as subagent transcripts, are not read.
func transcriptPaths(root string) ([]string, error) {
	dirs, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read replay root: %w", err)
	}
	var paths []string
	for _, dir := range dirs {
		if !dir.IsDir() {
			continue
		}
		var kept []string
		kept, err = keptInDir(filepath.Join(root, dir.Name()))
		if err != nil {
			return nil, err
		}
		paths = append(paths, kept...)
	}
	return paths, nil
}

// keptInDir returns the kept transcripts that are direct children of dir, in name order.
func keptInDir(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read replay directory: %w", err)
	}
	var kept []string
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		var keep bool
		keep, err = keepTranscript(path)
		if err != nil {
			return nil, err
		}
		if keep {
			kept = append(kept, path)
		}
	}
	return kept, nil
}

// keepTranscript reports whether the transcript at path has a turn end and no line from a headless session, whose
// entrypoint starts with sdk.
func keepTranscript(path string) (bool, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return false, fmt.Errorf("open transcript: %w", err)
	}
	defer file.Close()
	var turnEnd, headless bool
	err = eachLine(file, func(line []byte) {
		var entry struct {
			Type       string `json:"type"`
			Subtype    string `json:"subtype"`
			Entrypoint string `json:"entrypoint"`
		}
		if json.Unmarshal(line, &entry) != nil {
			return
		}
		turnEnd = turnEnd || (entry.Type == systemType && entry.Subtype == turnDurationSubtype)
		headless = headless || strings.HasPrefix(entry.Entrypoint, "sdk")
	})
	if err != nil {
		return false, err
	}
	return turnEnd && !headless, nil
}

// eachLine calls fn with each complete line of r, in order. A trailing line without a newline is not read.
func eachLine(r io.Reader, fn func(line []byte)) error {
	reader := bufio.NewReader(r)
	for {
		line, err := reader.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read transcript: %w", err)
		}
		fn(line)
	}
}

// replayTranscript feeds each complete line of the transcript at path through the queue rule, and adds the counts of
// its rows to replay.
func replayTranscript(path string, before time.Time, replay *QueueReplay) error {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("open transcript: %w", err)
	}
	defer file.Close()
	state := newReplayFile(before, replay)
	err = eachLine(file, state.feed)
	if err != nil {
		return err
	}
	state.finish()
	return nil
}

// userType and assistantType are the types of a user line and an assistant line.
const (
	userType      = "user"
	assistantType = "assistant"
)

// closeKind is what took a row out of the queue. A remove or popAll, or a user line whose text equals the row's, is
// exact. A user line whose text differs from the row's contains it. A turn end is turn end.
type closeKind int

const (
	closeExact closeKind = iota
	closeContaining
	closeTurnEnd
)

// closeWatch is a close that the falsifier still checks. dequeues and assistants are the counts of dequeue records and
// assistant lines that had come when the close came.
type closeWatch struct {
	kind       closeKind
	dequeues   int
	assistants int
}

// replayRow is what the replay knows of one row. watched is set once the idle rule could drop the row, so that the row
// is watched once.
type replayRow struct {
	text     string
	enqueued time.Time
	closed   bool
	watched  bool
}

// replayFile replays one transcript. It holds the queue that the queue rule builds from the transcript's lines, and the
// checks that those lines make on its rows. The counts go to out.
type replayFile struct {
	queue      queueFile
	cutoff     time.Time
	out        *QueueReplay
	rows       map[int]*replayRow      // the rows in the queue, by seq
	idle       map[string][]int        // idle watches by text, each the count of assistant lines when it started
	watches    map[string][]closeWatch // closes that the falsifier still checks, by text
	notices    map[string]int          // rejected enqueues that no typed user line has matched yet, by text
	dequeues   int
	assistants int
}

// newReplayFile returns the replay state of one transcript, whose counts go to out.
func newReplayFile(cutoff time.Time, out *QueueReplay) *replayFile {
	return &replayFile{
		cutoff:  cutoff,
		out:     out,
		rows:    map[int]*replayRow{},
		idle:    map[string][]int{},
		watches: map[string][]closeWatch{},
		notices: map[string]int{},
	}
}

// feed applies one complete line: the queue rule first, then the checks on what the line did to the rows.
func (f *replayFile) feed(line []byte) {
	prior := slices.Clone(f.queue.rows)
	f.queue.feed(line)
	var entry queueLine
	if json.Unmarshal(line, &entry) != nil {
		return
	}
	f.observe(entry, prior)
}

// observe runs the checks that a parsed line makes and records the rows that it changed. The checks run first, so a
// close made by this line is not checked against this line, and the rows that this line leaves open are watched last.
func (f *replayFile) observe(entry queueLine, prior []queueRow) {
	var text string
	switch entry.Type {
	case queueOperation:
		f.operation(entry)
	case userType:
		text, _ = splitContent(entry.Message.Content)
		f.user(entry, text)
	case assistantType:
		f.assistants++
	}
	f.changes(entry, text, prior)
	f.watchIdle()
}

// operation runs the checks that a queue record makes. An enqueue ends the checks on its text, a remove or popAll
// falsifies the closes of its text and counts the idle watches of its text, unless the remove cancels, and a dequeue
// is counted.
func (f *replayFile) operation(entry queueLine) {
	switch entry.Operation {
	case "enqueue":
		f.endChecks(entry.Content)
		if fromOthers(entry.Content) {
			f.notices[entry.Content]++
		}
	case "remove":
		f.out.Falsified += len(f.watches[entry.Content])
		delete(f.watches, entry.Content)
		if entry.Reason != "cleared_on_cancel" {
			f.countIdle(entry.Content)
		}
	case "popAll":
		f.out.Falsified += len(f.watches[entry.Content])
		delete(f.watches, entry.Content)
		f.countIdle(entry.Content)
	case "dequeue":
		f.dequeues++
	}
}

// user runs the checks that a user line makes. It falsifies the closes of its text that a dequeue and an assistant line
// came between, counts the idle watches of its text that an assistant line came after, and, when the line is typed,
// counts the rejected enqueues of its text.
func (f *replayFile) user(entry queueLine, text string) {
	f.falsifyDelivered(text)
	f.countIdle(text)
	if typedLine(entry.transcriptLine) {
		f.out.NoticesTyped += f.notices[text]
		delete(f.notices, text)
	}
}

// endChecks ends the checks on text, because an enqueue of it has come. The idle watches of text end uncounted.
func (f *replayFile) endChecks(text string) {
	delete(f.watches, text)
	delete(f.idle, text)
}

// falsifyDelivered counts each containing or turn-end close of text that a dequeue came after and an assistant line
// came after, and drops it from the watch. A close of kind exact is never falsified this way.
func (f *replayFile) falsifyDelivered(text string) {
	watches := f.watches[text]
	kept := watches[:0]
	for _, watch := range watches {
		if watch.kind != closeExact && f.dequeues > watch.dequeues && f.assistants > watch.assistants {
			f.out.Falsified++
			continue
		}
		kept = append(kept, watch)
	}
	if len(kept) == 0 {
		delete(f.watches, text)
		return
	}
	f.watches[text] = kept
}

// countIdle counts each idle watch of text that an assistant line came after, and drops it so that it counts once.
func (f *replayFile) countIdle(text string) {
	watches := f.idle[text]
	kept := watches[:0]
	for _, assistants := range watches {
		if f.assistants > assistants {
			f.out.IdleHidden++
			continue
		}
		kept = append(kept, assistants)
	}
	if len(kept) == 0 {
		delete(f.idle, text)
		return
	}
	f.idle[text] = kept
}

// changes applies what the line did to the rows. A row that left the queue is closed, unless a cancel closed it
// already. An enqueue that opened a row adds it, and a cancel that marked a row closes it.
func (f *replayFile) changes(entry queueLine, text string, prior []queueRow) {
	after := f.queue.rows
	next := 0
	for _, row := range prior {
		if next < len(after) && after[next].seq == row.seq {
			next++
			continue
		}
		f.departed(row, entry, text)
	}
	for _, row := range after {
		if _, ok := f.rows[row.seq]; !ok {
			f.rows[row.seq] = &replayRow{text: row.text, enqueued: enqueuedAt(entry)}
			f.out.Typed++
		}
	}
	for _, row := range after {
		if rr := f.rows[row.seq]; row.canceled && !rr.closed {
			f.closeRow(rr, closeExact)
		}
	}
}

// departed handles a row that the line took out of the queue. A row that a cancel closed already is only forgotten.
func (f *replayFile) departed(row queueRow, entry queueLine, text string) {
	rr := f.rows[row.seq]
	delete(f.rows, row.seq)
	if !rr.closed {
		f.closeRow(rr, departureKind(entry, text, rr.text))
	}
}

// closeRow records that rr closed by a close of kind, and starts the falsifier's watch on its text.
func (f *replayFile) closeRow(rr *replayRow, kind closeKind) {
	rr.closed = true
	f.out.Closed++
	f.watches[rr.text] = append(f.watches[rr.text],
		closeWatch{kind: kind, dequeues: f.dequeues, assistants: f.assistants})
}

// departureKind returns the kind of close that took a row with rowText out of the queue on the line entry, whose user
// text is text. A turn end is turn end, a user line whose text differs from the row's contains it, typed or not, and
// anything else is exact.
func departureKind(entry queueLine, text, rowText string) closeKind {
	switch {
	case entry.Type == systemType:
		return closeTurnEnd
	case entry.Type == userType && text != rowText:
		return closeContaining
	}
	return closeExact
}

// watchIdle watches each open row that the idle rule could drop, once, from the count of assistant lines that have
// come by now.
func (f *replayFile) watchIdle() {
	for _, row := range f.queue.rows {
		rr := f.rows[row.seq]
		if rr.watched || !f.queue.idleDrops(row) {
			continue
		}
		rr.watched = true
		f.idle[rr.text] = append(f.idle[rr.text], f.assistants)
	}
}

// finish counts the rows still open at the end of the file.
func (f *replayFile) finish() {
	for _, row := range f.queue.rows {
		if row.canceled {
			continue
		}
		switch {
		case row.dequeued:
			f.out.Other++
		case f.rows[row.seq].enqueued.Before(f.cutoff):
			f.out.NeverTakenBefore++
		default:
			f.out.NeverTakenAfter++
		}
	}
}

// enqueuedAt returns the time of an enqueue line, or the zero time when its stamp does not parse.
func enqueuedAt(entry queueLine) time.Time {
	stamp, err := time.Parse(time.RFC3339Nano, entry.Timestamp)
	if err != nil {
		return time.Time{}
	}
	return stamp
}
