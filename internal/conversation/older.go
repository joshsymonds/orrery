package conversation

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
)

// Page is one backward page of a transcript: its items, oldest first, the line start of the oldest transcript line
// that yields one of them, and whether items older than the page remain. Oldest is 0 when Items is empty.
type Page struct {
	Items  []Item
	Oldest int64
	More   bool
}

// pageScan holds the state of a backward read of a transcript's complete lines, newest first: the items of the page
// taken so far, the compactions still waiting for their boundary, and the context figure once one is found.
//
// A whole-line page takes each line that yields items entire, or none of it. A line whose items would take the page
// past limit closes the page, and no line older than a closed line is taken. A page with no item yet takes its newest
// item-yielding line whatever its size, so a read always makes progress. A page that is not whole-line takes the
// newest limit items, and so may take only the newest part of the oldest line it reaches. Either way, oldest is the
// start of the oldest line the page took items from. A whole-line page holds that line entire, so a read that starts
// there reaches every item the page left out; a page that is not whole-line does not.
type pageScan struct {
	limit int
	// wholeLines is set when the page takes whole lines, and clear when it takes the newest limit items.
	wholeLines bool
	// usage is set when the read also looks for the context figure: it reads past a full page until it finds one.
	usage bool
	// items are the page's items, newest first.
	items  []Item
	oldest int64
	// awaiting are the indexes of the compactions in items whose boundary line has not been read.
	awaiting []int
	// closed is set when a whole-line page refused a line, so older items remain.
	closed bool
	tokens int64
	found  bool
	cursor lineCursor
	err    error
}

// newPageScan returns a scan of the lines that end before end, the offset just past the last complete line of the
// transcript.
func newPageScan(src io.ReaderAt, end int64, limit int, wholeLines, usage bool) *pageScan {
	return &pageScan{
		limit: limit, wholeLines: wholeLines, usage: usage, items: []Item{},
		cursor: lineCursor{src: src, pos: end},
	}
}

// run visits the complete lines of the transcript in src that end within size, newest first, and at most maxBytes of
// them, as ReadTail does, and returns the first read error.
func (s *pageScan) run(src io.ReaderAt, size, maxBytes int64) error {
	if err := ReadTail(src, size, maxBytes, s.visit); err != nil {
		return err
	}
	return s.err
}

// visit takes one transcript line, newest first, and reports whether the read should go on. Every line ReadTail visits
// reaches visit, in order, so the cursor sees each line's bytes before the next line's.
func (s *pageScan) visit(line []byte) bool {
	start, err := s.cursor.start(len(line))
	if err != nil {
		s.err = err
		return false
	}
	if trigger, pre, post, ok := boundaryOf(line); ok {
		s.boundary(trigger, pre, post)
	} else {
		if !s.found {
			s.tokens, s.found = assistantTokens(line)
		}
		s.addLine(start, lineItems(line))
	}
	return s.keepGoing()
}

// full reports whether the page can take no more items: a whole-line page once it has refused a line, and any other
// page once it holds limit items.
func (s *pageScan) full() bool {
	if s.wholeLines {
		return s.closed
	}
	return len(s.items) >= s.limit
}

// keepGoing reports whether the scan goes on past the line it has just taken: while the page is not full, or a
// compaction waits for its boundary, or, for a usage read, the context figure is not found.
func (s *pageScan) keepGoing() bool {
	return !s.full() || len(s.awaiting) > 0 || (s.usage && !s.found)
}

// boundary gives a compaction the trigger and pre-compaction tokens of its boundary, and takes the boundary's
// post-compaction tokens as the context figure when none is found yet.
func (s *pageScan) boundary(trigger string, pre, post int64) {
	for _, i := range s.awaiting {
		s.items[i].Trigger = trigger
		s.items[i].Tokens = pre
	}
	s.awaiting = nil
	if !s.found {
		s.tokens, s.found = post, true
	}
}

// addLine takes the items of the line that starts at start into the page, when the page is not full and the line
// yields items. A whole-line page refuses the line, and closes, when its items would take it past limit. A page with
// no item takes its line whatever its size, unless limit is below 1.
func (s *pageScan) addLine(start int64, items []Item) {
	if len(items) == 0 || s.full() {
		return
	}
	if !s.wholeLines {
		newest := items[max(len(items)-(s.limit-len(s.items)), 0):]
		s.take(newest)
		if len(newest) > 0 {
			s.oldest = start
		}
		return
	}
	if len(s.items)+len(items) > s.limit && (len(s.items) > 0 || s.limit < 1) {
		s.closed = true
		return
	}
	s.take(items)
	s.oldest = start
}

// take adds items, which are in transcript order, to the page newest first, and records the compactions among them as
// waiting for their boundary.
func (s *pageScan) take(items []Item) {
	for i := len(items) - 1; i >= 0; i-- {
		s.items = append(s.items, items[i])
		if items[i].Kind == kindCompaction {
			s.awaiting = append(s.awaiting, len(s.items)-1)
		}
	}
}

// page returns the scan's items oldest first, with the cursor and the more flag of the page. more is set only for a
// whole-line page, which knows an older item remains once it refuses a line.
func (s *pageScan) page() Page {
	slices.Reverse(s.items)
	page := Page{Items: s.items, More: s.closed}
	page.Oldest = s.oldest
	return page
}

// lineCursor finds the start offsets of the lines ReadTail visits, which it gives without their offsets. It walks back
// from pos, the end of the bytes not yet assigned to a line. A line's content ends in a byte that is not a newline, and
// the bytes between that end and the next line's start are the line's newline and any empty lines, all newlines. So the
// start of a line of n bytes is n bytes before the last byte, walking back from pos, that is not a newline.
type lineCursor struct {
	src io.ReaderAt
	pos int64
}

// lineCursorChunk is how many bytes a lineCursor reads at a time while it walks back over newlines.
const lineCursorChunk = 64

// start returns the start offset of the next line, n bytes long, that ReadTail visits, and moves the cursor to it.
func (c *lineCursor) start(n int) (int64, error) {
	end, err := c.contentEnd()
	if err != nil {
		return 0, err
	}
	start := end - int64(n)
	if start < 0 {
		return 0, fmt.Errorf("transcript line of %d bytes has no start before offset %d", n, end)
	}
	c.pos = start
	return start, nil
}

// contentEnd returns the offset just past the last byte before pos that is not a newline, and moves pos to it. It
// returns 0 when no such byte is before pos.
func (c *lineCursor) contentEnd() (int64, error) {
	var chunk [lineCursorChunk]byte
	for c.pos > 0 {
		from := max(c.pos-lineCursorChunk, 0)
		span := chunk[:c.pos-from]
		if err := readFull(c.src, span, from); err != nil {
			return 0, err
		}
		for i := len(span) - 1; i >= 0; i-- {
			if span[i] != '\n' {
				c.pos = from + int64(i) + 1
				return c.pos, nil
			}
		}
		c.pos = from
	}
	return 0, nil
}

// Older returns the page of the transcript at path whose lines end before the line start before: the newest items
// below that offset, at most limit of them, oldest first, from whole lines. Pages chain: the next page is Older at the
// page's Oldest, and a page with More false holds the oldest items. It returns an error matching ErrOffset when before
// is not 0 or just after a newline.
func Older(path string, before int64, limit int) (Page, error) {
	if limit < 1 {
		return Page{}, fmt.Errorf("older limit %d: must be at least 1", limit)
	}
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return Page{}, fmt.Errorf("open transcript: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return Page{}, fmt.Errorf("stat transcript: %w", err)
	}
	return olderPage(file, info.Size(), before, limit)
}

// olderPage returns the page of the transcript in src, of size bytes, whose lines end before the line start before.
func olderPage(src io.ReaderAt, size, before int64, limit int) (Page, error) {
	if err := checkLineStart(src, size, before); err != nil {
		return Page{}, err
	}
	scan := newPageScan(src, before, limit, true, false)
	if err := scan.run(src, before, before); err != nil {
		return Page{}, err
	}
	return scan.page(), nil
}
