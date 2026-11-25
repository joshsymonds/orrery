// Package keylines picks the key lines out of a gate's output as it streams: the lines scripts/gate-remote.sh prints
// about where the gate ran, and the lines that say how the gate failed. A gate's output is kept only as its newest
// bytes, and these lines often come before that cut, so they are kept apart from it, capped, for whoever judges why
// the gate failed.
package keylines

import (
	"io"
	"regexp"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"
)

// The caps on the key lines kept of one output. At most MaxLines lines are kept, holding at most MaxBytes bytes in all,
// with a newline counted after each line. Of the gate-remote lines, the first remoteFirst and the last remoteLast are
// kept. The rest of the room goes to the newest failure lines that fit. A line longer than LineMax keeps its start and
// its end around cutMark (see cut).
const (
	MaxLines    = 64
	MaxBytes    = 8 << 10
	LineMax     = 512
	remoteFirst = 6
	remoteLast  = 2
	cutMark     = " ... "
	// cutHalf is the most bytes a cut line keeps of each of its start and its end, so that a cut line, cutMark
	// included, is at most LineMax bytes.
	cutHalf = (LineMax - len(cutMark)) / 2
)

var (
	// remote matches a line scripts/gate-remote.sh prints: where the gate runs and the load and CPU count there, a
	// box that did not answer or a wait for one, a lost or regained connection, and how the command exited and after
	// how long. Only a line at the start of its line is the script's own: a test of the script prints such lines
	// indented.
	remote = regexp.MustCompile(`^gate-remote: `)
	// failure matches a line that says a gate failed, or why: a failing Go test, its failure message, a failing Go
	// package or a script's failing check, a Go panic or runtime crash, a compiler's or linter's finding at a file, line
	// and column, golangci-lint's own error, such as its timeout, a test run past its time budget, a just recipe that
	// failed, Gradle's failed task, build or test, a Kotlin or TypeScript compiler error, a failing node:test test, a
	// full disk, a lack of memory, and a killed process.
	failure = regexp.MustCompile(strings.Join([]string{
		`^\s*--- FAIL: `,
		`^\s+\S+_test\.go:\d+: `,
		`^FAIL(?:\s|:|$)`,
		`^(?:panic|fatal error): `,
		`^\S+\.\w+:\d+:\d+: `,
		`level=error`,
		`\bexceeded\b.*\bbudget\b`,
		`^error: recipe `,
		`\bFAILED\b`,
		`^FAILURE: `,
		`Execution failed for task`,
		`^e: `,
		`\berror TS\d+: `,
		`^\s*✖ `,
		`(?i:no space left on device|out of memory|cannot allocate memory)`,
		`\bKilled\b|signal: killed`,
	}, "|"))
	// terminalEscape matches an escape sequence a terminal reads: a control sequence such as a color, an operating
	// system command, or a two-byte escape.
	terminalEscape = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\)|[@-Z\\-_])`)
)

// Plain returns text with each escape sequence a terminal reads taken out, such as the colors a tool prints.
func Plain(text string) string {
	return terminalEscape.ReplaceAllString(text, "")
}

// Key reports whether line is a key line: a gate-remote line or a failure line. The line is read as Plain reads it,
// and each part of it a carriage return ends is read as a line of its own, as a terminal shows it.
func Key(line string) bool {
	return remoteLine(line) || failureLine(line)
}

// remoteLine reports whether line, read as Key reads it, is a gate-remote line.
func remoteLine(line string) bool {
	return matches(remote, line)
}

// failureLine reports whether line, read as Key reads it, is a failure line.
func failureLine(line string) bool {
	return matches(failure, line)
}

// matches reports whether pattern matches line, read as Plain reads it, or one of the parts of it a carriage return
// ends.
func matches(pattern *regexp.Regexp, line string) bool {
	for part := range strings.SplitSeq(Plain(line), "\r") {
		if pattern.MatchString(part) {
			return true
		}
	}
	return false
}

// kept is a key line as a Collector keeps it: its text, cut as cut cuts it, and its place among the lines the
// Collector was given.
type kept struct {
	text string
	seq  int
}

// size returns the bytes line counts toward MaxBytes: its text and the newline after it.
func (line kept) size() int {
	return len(line.text) + 1
}

// Collector keeps the key lines of one gate's output, capped (see MaxLines): the first remoteFirst and the last
// remoteLast gate-remote lines, and the newest failure lines that fit in what room is left. A failure line equal to one
// already kept counts once, at its newest place. Its zero value keeps nothing yet and is ready for use, and it is safe
// for concurrent use, so each stream of the output writes to it through a Writer of its own.
type Collector struct {
	mu       sync.Mutex
	seq      int
	first    []kept
	last     []kept
	failures []kept
}

// Writer returns a writer that splits what one stream of the output writes into lines and adds each key line among
// them to c.
func (c *Collector) Writer() *Writer {
	return &Writer{collector: c}
}

// Add adds line, one line of the output with no newline, to c when it is a key line (Key).
func (c *Collector) Add(line string) {
	if Key(line) {
		c.keep(line)
	}
}

// keep keeps line, cut as cut cuts it, as a gate-remote line when it is one and as a failure line otherwise, within the
// caps.
func (c *Collector) keep(line string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	line = cut(line)
	add := kept{text: line, seq: c.seq}
	switch {
	case remoteLine(line) && len(c.first) < remoteFirst:
		c.first = append(c.first, add)
	case remoteLine(line):
		c.last = newestOf(append(c.last, add), remoteLast)
	default:
		c.failures = slices.DeleteFunc(c.failures, func(had kept) bool { return had.text == line })
		c.failures = newestOf(append(c.failures, add), MaxLines)
	}
}

// newestOf returns the last n of lines, in a slice of its own once lines holds more, so a long run of adds keeps no more
// than n lines.
func newestOf(lines []kept, n int) []kept {
	if len(lines) <= n {
		return lines
	}
	return slices.Clone(lines[len(lines)-n:])
}

// Lines returns the key lines c keeps, in the order they were added: the gate-remote lines it keeps, and then as many of
// the newest failure lines as fit within MaxLines lines and MaxBytes bytes in all. A newer failure line that does not
// fit leaves out every older one too, so the failure lines kept run on to the last one added. It returns nil when c
// keeps none.
func (c *Collector) Lines() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	lines := slices.Concat(c.first, c.last)
	used := 0
	for _, line := range lines {
		used += line.size()
	}
	for i := len(c.failures) - 1; i >= 0 && len(lines) < MaxLines; i-- {
		if used+c.failures[i].size() > MaxBytes {
			break
		}
		used += c.failures[i].size()
		lines = append(lines, c.failures[i])
	}
	if len(lines) == 0 {
		return nil
	}
	slices.SortFunc(lines, func(a, b kept) int { return a.seq - b.seq })
	texts := make([]string, len(lines))
	for i, line := range lines {
		texts[i] = line.text
	}
	return texts
}

// Cap returns lines held to the caps a Collector keeps its lines within: each line cut as cut cuts it, the first and
// last gate-remote lines and the newest of the other lines that fit, in their order. Every line that is not a gate-remote
// line counts as a failure line, a line that holds newlines counts as the lines they end, and a blank line is left out.
// Cap of the lines a Collector returns returns them as they are. It returns nil for no lines.
func Cap(lines []string) []string {
	var collector Collector
	for _, line := range lines {
		for part := range strings.SplitSeq(line, "\n") {
			if strings.TrimSpace(part) != "" {
				collector.keep(part)
			}
		}
	}
	return collector.Lines()
}

// cut returns line when it is at most LineMax bytes. A longer line keeps at most cutHalf bytes of its start and of its
// end, around cutMark, and loses the word that runs into the cut on either side, so no word of it, such as a
// credential, is kept in part. A start or end that is one word with no space keeps what it has of that word, and a
// rune the cut splits is left out.
func cut(line string) string {
	if len(line) <= LineMax {
		return line
	}
	return cutParts(line[:cutHalf], line[len(line)-cutHalf:])
}

// cutParts returns head and tail, the start and the end of a line longer than LineMax, joined around cutMark as cut
// joins them.
func cutParts(head, tail string) string {
	for end := len(head) - 1; end >= 0 && end >= len(head)-utf8.UTFMax; end-- {
		if utf8.RuneStart(head[end]) {
			if !utf8.FullRuneInString(head[end:]) {
				head = head[:end]
			}
			break
		}
	}
	for tail != "" && !utf8.RuneStart(tail[0]) {
		tail = tail[1:]
	}
	if i := strings.LastIndexAny(head, " \t"); i >= 0 {
		head = head[:i]
	}
	if i := strings.IndexAny(tail, " \t"); i >= 0 {
		tail = tail[i+1:]
	}
	return head + cutMark + tail
}

// Writer is one stream of a gate's output as its Collector reads it: what is written to it is split into lines at each
// newline, with a carriage return before the newline taken off, and each line is added to the Collector (Collector.Add).
// A line may come in any number of writes. A line longer than LineMax is held only as the start and end that cut
// keeps of it, so a stream with no newline holds no more than LineMax bytes. Flush adds the line the stream ended on
// without a newline. A Writer is used by one goroutine at a time.
type Writer struct {
	collector *Collector
	// head holds the first LineMax bytes of the line being read, and tail its last cutHalf bytes once it is longer.
	head, tail []byte
	size       int
}

// A Writer is an io.Writer, so a stream is copied into it as into any writer.
var _ io.Writer = (*Writer)(nil)

// Write splits data into lines as Writer says, and adds each line it ends to the Collector. It never fails, and reports
// the whole of data written.
func (w *Writer) Write(data []byte) (int, error) {
	written := len(data)
	for len(data) > 0 {
		i := slices.Index(data, '\n')
		if i < 0 {
			w.part(data)
			break
		}
		w.part(data[:i])
		w.end()
		data = data[i+1:]
	}
	return written, nil
}

// Flush adds the line the stream ended on to the Collector when the stream did not end with a newline.
func (w *Writer) Flush() {
	if w.size > 0 {
		w.end()
	}
}

// part reads chunk, a part of the line being read that holds no newline.
func (w *Writer) part(chunk []byte) {
	before := w.size
	w.size += len(chunk)
	if room := LineMax - len(w.head); room > 0 {
		w.head = append(w.head, chunk[:min(room, len(chunk))]...)
	}
	if w.size <= LineMax {
		return
	}
	if len(chunk) >= cutHalf {
		w.tail = append(w.tail[:0], chunk[len(chunk)-cutHalf:]...)
		return
	}
	if before <= LineMax {
		// The line passes LineMax in chunk, so every byte of it before chunk is in head.
		w.tail = append(w.tail[:0], w.head[:before]...)
	}
	w.tail = append(w.tail, chunk...)
	if over := len(w.tail) - cutHalf; over > 0 {
		w.tail = append(w.tail[:0], w.tail[over:]...)
	}
}

// end adds the line being read to the Collector, as cut cuts it, and starts the next line.
func (w *Writer) end() {
	line := string(w.head)
	if w.size > LineMax {
		line = cutParts(string(w.head[:cutHalf]), string(w.tail))
	}
	w.collector.Add(strings.TrimSuffix(line, "\r"))
	w.head, w.tail, w.size = w.head[:0], w.tail[:0], 0
}
