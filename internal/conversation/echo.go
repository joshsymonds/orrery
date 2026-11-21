package conversation

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// EchoBound is how long after a send its echo may be written: one hour, in unix seconds.
const EchoBound = int64(time.Hour / time.Second)

// Send is one prompt that a pane send wrote into a session: its text, the client that sent it and the unix
// seconds it was sent at.
type Send struct {
	Text   string
	Client string
	At     int64
}

// MarkEchoes sets Client on each prompt item of items that a send echoes. earlier are the prompt items that
// come before items in the transcript (see EarlierPrompts), so an echo just before items takes its send too.
// The sends are taken in At order, ties in the order given. A send's echo is the first prompt, in transcript
// order, whose text equals the send's text and whose Time is in [At, At+EchoBound], and that no earlier send
// has taken. Only prompts are marked.
func MarkEchoes(items, earlier []Item, sends []Send) {
	transcript := slices.Concat(earlier, items)
	taken := make([]bool, len(transcript))
	ordered := slices.Clone(sends)
	slices.SortStableFunc(ordered, func(a, b Send) int { return cmp.Compare(a.At, b.At) })
	for _, send := range ordered {
		i := echoOf(transcript, taken, send)
		if i < 0 {
			continue
		}
		taken[i] = true
		transcript[i].Client = send.Client
	}
	for i := range items {
		items[i].Client = transcript[len(earlier)+i].Client
	}
}

// echoOf returns the index in transcript of the first prompt that send echoes and that no earlier send has
// taken, or -1 when there is none.
func echoOf(transcript []Item, taken []bool, send Send) int {
	text := echoText(send.Text)
	for i, item := range transcript {
		if taken[i] || item.Kind != kindPrompt || strings.TrimSpace(item.Text) != text {
			continue
		}
		if item.Time >= send.At && item.Time <= send.At+EchoBound {
			return i
		}
	}
	return -1
}

// echoText returns a send's text as a prompt item keeps it: pasted content stripped, cut to maxTextBytes, and
// trimmed of its surrounding whitespace.
func echoText(text string) string {
	return strings.TrimSpace(cutText(stripPastedContent(text), maxTextBytes))
}

// EarlierPrompts returns the prompt items of the transcript at path that come before the line start before,
// oldest first. It reads backward from before, as Older does, and stops at the first line stamped older than
// since. A line with no stamp does not stop it. It returns an error matching ErrOffset when before is not a
// line start.
func EarlierPrompts(path string, before, since int64) ([]Item, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("open transcript: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat transcript: %w", err)
	}
	return earlierPrompts(file, info.Size(), before, since)
}

// earlierPrompts returns the prompt items of the lines of src, of size bytes, that end before before, oldest first. It
// visits the lines newest first, as ReadTail does, and stops at the first line stamped older than since.
func earlierPrompts(src io.ReaderAt, size, before, since int64) ([]Item, error) {
	if err := checkLineStart(src, size, before); err != nil {
		return nil, err
	}
	var prompts []Item
	err := ReadTail(src, before, before, func(line []byte) bool {
		if stamp := lineStamp(line); stamp != 0 && stamp < since {
			return false
		}
		for _, item := range slices.Backward(lineItems(line)) {
			if item.Kind == kindPrompt {
				prompts = append(prompts, item)
			}
		}
		return true
	})
	if err != nil {
		return nil, err
	}
	slices.Reverse(prompts)
	return prompts, nil
}

// lineStamp returns the unix time of the timestamp of a transcript line, or 0 when the line has none or does
// not decode.
func lineStamp(line []byte) int64 {
	var entry struct {
		Timestamp string `json:"timestamp"`
	}
	if json.Unmarshal(line, &entry) != nil {
		return 0
	}
	return unixSeconds(entry.Timestamp)
}
