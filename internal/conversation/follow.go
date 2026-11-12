package conversation

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ErrOffset is returned by Since for an offset that is negative, past the end of the transcript, or neither 0
// nor just after a newline.
var ErrOffset = errors.New("offset is not a line start")

// Follow is what Since reads from a transcript: the items of its complete lines, the offset just past the last
// of them, and the context figure of the newest qualifying line among them.
type Follow struct {
	Items     []Item
	End       int64
	Tokens    int64
	HasTokens bool
}

// followState is what a Since call builds from the complete lines it reads.
type followState struct {
	items []Item
	// trigger and compactTokens are the trigger and pre-compaction tokens of the newest compact_boundary read.
	trigger       string
	compactTokens int64
	// tokens and hasTokens are the context figure of the newest qualifying line read.
	tokens    int64
	hasTokens bool
}

// Since returns the items of the complete lines of the transcript at path from offset from up to its current size,
// in file order. A trailing line without its newline is left unread. from must be 0 or just after a newline, or
// Since returns ErrOffset.
func Since(path string, from int64) (Follow, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return Follow{}, fmt.Errorf("open transcript: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return Follow{}, fmt.Errorf("stat transcript: %w", err)
	}
	size := info.Size()
	err = checkLineStart(file, size, from)
	if err != nil {
		return Follow{}, err
	}

	tracked := &trackedFile[followState]{offset: from, state: followState{items: []Item{}}}
	err = tracked.scan(file, size, applyFollow)
	if err != nil {
		return Follow{}, err
	}
	return Follow{
		Items:     tracked.state.items,
		End:       tracked.offset,
		Tokens:    tracked.state.tokens,
		HasTokens: tracked.state.hasTokens,
	}, nil
}

// checkLineStart returns ErrOffset unless from is 0 or just after a newline at or before size.
func checkLineStart(src io.ReaderAt, size, from int64) error {
	if from < 0 || from > size {
		return fmt.Errorf("offset %d outside 0..%d: %w", from, size, ErrOffset)
	}
	if from == 0 {
		return nil
	}
	var prev [1]byte
	if _, err := src.ReadAt(prev[:], from-1); err != nil {
		return fmt.Errorf("read transcript: %w", err)
	}
	if prev[0] != '\n' {
		return fmt.Errorf("offset %d: %w", from, ErrOffset)
	}
	return nil
}

// applyFollow adds the items of one complete line to the state and takes its context figure. A compaction takes
// the trigger and tokens of the newest boundary read before it in the same call.
func applyFollow(state *followState, line []byte) {
	if trigger, pre, post, ok := boundaryOf(line); ok {
		state.trigger, state.compactTokens = trigger, pre
		state.tokens, state.hasTokens = post, true
		return
	}
	if tokens, ok := assistantTokens(line); ok {
		state.tokens, state.hasTokens = tokens, true
	}
	for _, item := range lineItems(line) {
		if item.Kind == kindCompaction {
			item.Trigger, item.Tokens = state.trigger, state.compactTokens
		}
		state.items = append(state.items, item)
	}
}

// ReadEnd returns what Read returns for the transcript at path, from one size of the file, and the offset just past
// the last complete line of that size. The offset is 0 when the file has no complete line.
func ReadEnd(path string, limit int) (Conversation, int64, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return Conversation{}, 0, fmt.Errorf("open transcript: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return Conversation{}, 0, fmt.Errorf("stat transcript: %w", err)
	}
	size := info.Size()

	items, tokens, err := newestItems(file, size, limit)
	if err != nil {
		return Conversation{}, 0, err
	}
	origin, err := originPrompt(file, size)
	if err != nil {
		return Conversation{}, 0, err
	}
	end, err := lastLineEnd(file, size)
	if err != nil {
		return Conversation{}, 0, err
	}
	return Conversation{Origin: origin, Items: items, Context: Context{Tokens: tokens, Window: contextWindow}}, end, nil
}

// lastLineEnd returns the offset just past the last newline of src before size, or 0 when there is none. It reads
// backward from size in blocks of TailBlockSize.
func lastLineEnd(src io.ReaderAt, size int64) (int64, error) {
	block := make([]byte, min(size, TailBlockSize))
	for end := size; end > 0; {
		start := max(end-TailBlockSize, 0)
		span := block[:end-start]
		if err := readFull(src, span, start); err != nil {
			return 0, err
		}
		if i := bytes.LastIndexByte(span, '\n'); i >= 0 {
			return start + int64(i) + 1, nil
		}
		end = start
	}
	return 0, nil
}
