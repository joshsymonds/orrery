// Package conversation reads the newest items of a Claude Code session transcript.
package conversation

import (
	"bytes"
	"fmt"
	"io"
)

// TailBlockSize is the size of each backward read in ReadTail.
const TailBlockSize = 64 << 10

// MaxTailBytes caps how many bytes ReadTail reads from the end of a transcript.
const MaxTailBytes = 4 << 20

// ReadTail visits the complete lines of src that end within [0, size), newest first, without their
// newlines. It reads backward from size in blocks of TailBlockSize and reads at most maxBytes bytes.
// A line is complete only when a newline precedes it (or it starts at offset 0) and a newline ends it
// at or before size, so a trailing line without a newline and a line whose start falls outside the
// window are never visited. Empty lines are skipped. ReadTail stops when visit returns false. The
// line slice is valid only during the call to visit.
func ReadTail(src io.ReaderAt, size, maxBytes int64, visit func(line []byte) bool) error {
	floor := max(size-maxBytes, 0)
	block := make([]byte, TailBlockSize)
	scan := tailScan{visit: visit, end: -1}
	for off := size; off > floor; {
		bs := max(off-TailBlockSize, floor)
		span := block[:off-bs]
		if err := readFull(src, span, bs); err != nil {
			return err
		}
		if !scan.scanBlock(span, bs) {
			return nil
		}
		off = bs
	}
	if floor == 0 && scan.end > 0 {
		scan.emit(nil)
	}
	return nil
}

// tailScan holds the state ReadTail carries from one block to the next.
type tailScan struct {
	visit func(line []byte) bool
	// end is the absolute offset of the newline that terminates the line being assembled, or -1
	// before the newest newline is found.
	end int64
	// pieces holds the bytes of the line being assembled that were read from later blocks,
	// rightmost first.
	pieces [][]byte
}

// scanBlock walks block, which holds the bytes at [bs, bs+len(block)), from its end back to its start
// and visits each line it completes. It reports false once visit returns false.
func (s *tailScan) scanBlock(block []byte, bs int64) bool {
	hi := len(block)
	for {
		i := bytes.LastIndexByte(block[:hi], '\n')
		if i < 0 {
			if s.end >= 0 {
				s.pieces = append(s.pieces, bytes.Clone(block[:hi]))
			}
			return true
		}
		if s.end >= 0 && !s.emit(block[i+1:hi]) {
			return false
		}
		s.end = bs + int64(i)
		s.pieces = nil
		hi = i
	}
}

// emit visits the line made of head followed by the pending pieces, skipping empty lines. It
// reports false when visit asks to stop.
func (s *tailScan) emit(head []byte) bool {
	line := joinLine(head, s.pieces)
	if len(line) == 0 {
		return true
	}
	return s.visit(line)
}

// readFull fills p from src at offset off and fails on a short read.
func readFull(src io.ReaderAt, p []byte, off int64) error {
	n, err := src.ReadAt(p, off)
	if n < len(p) {
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return fmt.Errorf("read tail at offset %d: %w", off, err)
	}
	return nil
}

// joinLine returns head followed by pieces, where pieces[len-1] is the leftmost piece.
func joinLine(head []byte, pieces [][]byte) []byte {
	if len(pieces) == 0 {
		return head
	}
	size := len(head)
	for _, p := range pieces {
		size += len(p)
	}
	line := make([]byte, 0, size)
	line = append(line, head...)
	for i := len(pieces) - 1; i >= 0; i-- {
		line = append(line, pieces[i]...)
	}
	return line
}
