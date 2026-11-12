package holder

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

const (
	// DefaultSegmentBytes is the size at which the spool opens its next segment file.
	DefaultSegmentBytes = 4 << 20
	// DefaultCapBytes is how far the child may run ahead of the controller's consumed offset.
	DefaultCapBytes = 64 << 20

	segmentPrefix = "stdout-"
	segmentMode   = 0o600
)

var (
	// ErrSpoolExists means the directory already holds a stdout segment from an earlier spool.
	ErrSpoolExists = errors.New("spool: directory already holds stdout segments")
	// ErrOverCap means an append would grow the spool past its cap above the consumed offset.
	ErrOverCap = errors.New("spool: append exceeds the cap above the consumed offset")
	// ErrTrimmed means the offset was deleted with the segments below replay-from.
	ErrTrimmed = errors.New("spool: offset is below the retained start")
	// ErrBadAck means the ack's offsets are out of order, out of range, or move backwards.
	ErrBadAck = errors.New("spool: ack offsets out of order or out of range")
)

// SpoolConfig sizes a Spool. A zero field takes its default.
type SpoolConfig struct {
	SegmentBytes int64
	CapBytes     int64
}

// segment is one numbered file of the spool. Every segment but the last is full.
type segment struct {
	start int64 // offset of the segment's first byte
	size  int64 // bytes written to the segment so far
}

func (g segment) end() int64 { return g.start + g.size }

// Spool is the holder's stdout spool: child output appended at growing byte offsets into numbered segment files
// in one directory. It is safe for concurrent use.
type Spool struct {
	mu         sync.Mutex
	root       *os.Root
	segBytes   int64
	capBytes   int64
	segs       []segment // contiguous and ascending; the last one is the segment Append writes
	end        int64
	consumed   int64
	replayFrom int64
	sealed     bool
	changed    chan struct{}
}

// NewSpool opens a spool in dir, which must exist and must hold no stdout segment, and creates its first segment.
func NewSpool(dir string, cfg SpoolConfig) (*Spool, error) {
	if cfg.SegmentBytes == 0 {
		cfg.SegmentBytes = DefaultSegmentBytes
	}
	if cfg.CapBytes == 0 {
		cfg.CapBytes = DefaultCapBytes
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("spool: read %s: %w", dir, err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), segmentPrefix) {
			return nil, ErrSpoolExists
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("spool: open %s: %w", dir, err)
	}
	s := &Spool{root: root, segBytes: cfg.SegmentBytes, capBytes: cfg.CapBytes, changed: make(chan struct{})}
	if err = s.createSegment(0); err != nil {
		return nil, errors.Join(err, root.Close())
	}
	return s, nil
}

// Append adds p at End, opening the next segment whenever the current one is full. It refuses with ErrOverCap,
// appending nothing, when p is larger than Room, unless the spool is sealed.
func (s *Spool) Append(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.sealed && int64(len(data)) > s.roomLocked() {
		return ErrOverCap
	}
	for len(data) > 0 {
		last := &s.segs[len(s.segs)-1]
		if last.size == s.segBytes {
			if err := s.createSegment(s.end); err != nil {
				return err
			}
			continue
		}
		chunk := data[:min(int64(len(data)), s.segBytes-last.size)]
		n, err := s.writeLast(chunk)
		last.size += int64(n)
		s.end += int64(n)
		if n > 0 {
			s.signal()
		}
		if err != nil {
			return err
		}
		data = data[n:]
	}
	return nil
}

// ReadAt reads from the one segment that holds off, so it returns fewer than len(buf) bytes at a segment boundary.
// It returns io.EOF at End.
func (s *Spool) ReadAt(buf []byte, off int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if off < s.segs[0].start {
		return 0, ErrTrimmed
	}
	if off > s.end {
		return 0, fmt.Errorf("spool: read at %d past end %d", off, s.end)
	}
	if off == s.end {
		return 0, io.EOF
	}
	i := len(s.segs) - 1
	for s.segs[i].start > off {
		i--
	}
	seg := s.segs[i]
	want := min(int64(len(buf)), seg.end()-off)
	name := segmentName(seg.start)
	f, err := s.root.Open(name)
	if err != nil {
		return 0, fmt.Errorf("spool: open %s: %w", name, err)
	}
	defer f.Close()
	n, err := f.ReadAt(buf[:want], off-seg.start)
	if err != nil {
		return n, fmt.Errorf("spool: read %s: %w", name, err)
	}
	return n, nil
}

// Ack records the controller's consumed and replay-from offsets, then deletes every segment that ends at or below
// replayFrom. The segment Append writes is never deleted.
func (s *Spool) Ack(consumed, replayFrom int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if replayFrom < 0 || replayFrom > consumed || consumed > s.end {
		return ErrBadAck
	}
	if consumed < s.consumed || replayFrom < s.replayFrom {
		return ErrBadAck
	}
	if consumed != s.consumed {
		s.consumed = consumed
		s.signal()
	}
	s.replayFrom = replayFrom
	for len(s.segs) > 1 && s.segs[0].end() <= replayFrom {
		name := segmentName(s.segs[0].start)
		if err := s.root.Remove(name); err != nil {
			return fmt.Errorf("spool: remove %s: %w", name, err)
		}
		s.segs = s.segs[1:]
	}
	return nil
}

// Seal records that the child is gone. From then on Append ignores the cap: the bytes a reaped child left in its
// pipe are bounded by the pipe and must not be lost.
func (s *Spool) Seal() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sealed = true
}

// Start is the first retained offset.
func (s *Spool) Start() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.segs[0].start
}

// End is the offset one past the last byte appended.
func (s *Spool) End() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.end
}

// Consumed is the controller's consumed offset, as last acked.
func (s *Spool) Consumed() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.consumed
}

// Room is how many more bytes Append accepts before it refuses with ErrOverCap.
func (s *Spool) Room() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.roomLocked()
}

// Changed returns a channel that is closed at the next change of End or Consumed.
func (s *Spool) Changed() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.changed
}

// Close releases the spool directory. Segment files stay on disk.
func (s *Spool) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.root.Close(); err != nil {
		return fmt.Errorf("spool: close: %w", err)
	}
	return nil
}

func (s *Spool) roomLocked() int64 {
	return max(0, s.capBytes-(s.end-s.consumed))
}

// signal closes the current Changed channel and starts the next one.
func (s *Spool) signal() {
	close(s.changed)
	s.changed = make(chan struct{})
}

// createSegment creates the empty segment file that starts at start and makes it the segment Append writes.
func (s *Spool) createSegment(start int64) error {
	f, err := s.root.OpenFile(segmentName(start), os.O_WRONLY|os.O_CREATE|os.O_EXCL, segmentMode)
	if err != nil {
		return fmt.Errorf("spool: create segment at %d: %w", start, err)
	}
	defer f.Close()
	s.segs = append(s.segs, segment{start: start})
	return nil
}

// writeLast appends data to the segment Append writes and reports how many bytes reached the file.
func (s *Spool) writeLast(data []byte) (int, error) {
	name := segmentName(s.segs[len(s.segs)-1].start)
	f, err := s.root.OpenFile(name, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return 0, fmt.Errorf("spool: open %s: %w", name, err)
	}
	defer f.Close()
	n, err := f.Write(data)
	if err != nil {
		return n, fmt.Errorf("spool: write %s: %w", name, err)
	}
	return n, nil
}

// segmentName is the file name of the segment that starts at offset start.
func segmentName(start int64) string {
	return fmt.Sprintf("%s%020d", segmentPrefix, start)
}
