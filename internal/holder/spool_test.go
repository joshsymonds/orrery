package holder

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

const spoolTestFileMode os.FileMode = 0o600

// spoolBytes returns n bytes whose value at stream offset o is byte(o), so a read can be checked against its offset.
func spoolBytes(from, n int64) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(from + int64(i))
	}
	return b
}

func newTestSpool(t *testing.T, cfg SpoolConfig) (*Spool, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := NewSpool(dir, cfg)
	if err != nil {
		t.Fatalf("NewSpool: %v", err)
	}
	t.Cleanup(func() {
		if cerr := s.Close(); cerr != nil {
			t.Errorf("Close: %v", cerr)
		}
	})
	return s, dir
}

func mustAppend(t *testing.T, s *Spool, p []byte) {
	t.Helper()
	if err := s.Append(p); err != nil {
		t.Fatalf("Append(%d bytes at End %d): %v", len(p), s.End(), err)
	}
}

func mustAck(t *testing.T, s *Spool, consumed, replayFrom int64) {
	t.Helper()
	if err := s.Ack(consumed, replayFrom); err != nil {
		t.Fatalf("Ack(%d, %d): %v", consumed, replayFrom, err)
	}
}

func assertClosed(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	default:
		t.Fatalf("%s: channel still open", what)
	}
}

func assertOpen(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
		t.Fatalf("%s: channel closed", what)
	default:
	}
}

func TestSpoolSegmentsContinueOffsets(t *testing.T) {
	s, dir := newTestSpool(t, SpoolConfig{SegmentBytes: 10, CapBytes: 1000})
	mustAppend(t, s, spoolBytes(0, 25))

	if got := s.End(); got != 25 {
		t.Fatalf("End() = %d, want 25", got)
	}
	segs := []struct {
		name  string
		start int64
		size  int64
	}{
		{"stdout-00000000000000000000", 0, 10},
		{"stdout-00000000000000000010", 10, 10},
		{"stdout-00000000000000000020", 20, 5},
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != len(segs) {
		t.Fatalf("spool dir holds %d entries, want %d", len(entries), len(segs))
	}
	for _, seg := range segs {
		path := filepath.Join(dir, seg.name)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("segment %s: %v", seg.name, err)
		}
		if info.Size() != seg.size {
			t.Fatalf("segment %s size = %d, want %d", seg.name, info.Size(), seg.size)
		}
		if mode := info.Mode().Perm(); mode != spoolTestFileMode {
			t.Fatalf("segment %s mode = %v, want %v", seg.name, mode, spoolTestFileMode)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read segment %s: %v", seg.name, err)
		}
		if want := spoolBytes(seg.start, seg.size); !bytes.Equal(content, want) {
			t.Fatalf("segment %s holds %v, want %v", seg.name, content, want)
		}
	}
}

func TestSpoolReadAtReturnsAppendedBytes(t *testing.T) {
	s, _ := newTestSpool(t, SpoolConfig{SegmentBytes: 10, CapBytes: 1000})
	data := spoolBytes(0, 37)
	mustAppend(t, s, data[:4])
	mustAppend(t, s, data[4:])

	buf := make([]byte, 64)
	for off := int64(0); off < 37; {
		n, err := s.ReadAt(buf, off)
		if err != nil {
			t.Fatalf("ReadAt(%d): %v", off, err)
		}
		// A read stops at its segment's end: the next segment starts at the next multiple of 10.
		want := min(37, (off/10+1)*10) - off
		if int64(n) != want {
			t.Fatalf("ReadAt(%d) returned %d bytes, want %d", off, n, want)
		}
		if !bytes.Equal(buf[:n], data[off:off+int64(n)]) {
			t.Fatalf("ReadAt(%d) = %v, want %v", off, buf[:n], data[off:off+int64(n)])
		}
		off += int64(n)
	}

	if n, err := s.ReadAt(buf, 37); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("ReadAt(End) = %d, %v; want 0, io.EOF", n, err)
	}
	if _, err := s.ReadAt(buf, 38); err == nil || errors.Is(err, io.EOF) {
		t.Fatalf("ReadAt past End: err = %v, want a non-EOF error", err)
	}
}

func TestSpoolAckTrimsWholeSegments(t *testing.T) {
	s, dir := newTestSpool(t, SpoolConfig{SegmentBytes: 10, CapBytes: 1000})
	mustAppend(t, s, spoolBytes(0, 25))

	// Segment 0 still holds byte 9, the replay-from offset, so it stays.
	mustAck(t, s, 25, 9)
	if got := s.Start(); got != 0 {
		t.Fatalf("Start() after Ack(25, 9) = %d, want 0", got)
	}
	one := make([]byte, 1)
	if n, err := s.ReadAt(one, 9); err != nil || n != 1 || one[0] != spoolBytes(9, 1)[0] {
		t.Fatalf("ReadAt(9) after Ack(25, 9) = %d, %v; want byte 9", n, err)
	}

	mustAck(t, s, 25, 12)
	if got := s.Start(); got != 10 {
		t.Fatalf("Start() after Ack(25, 12) = %d, want 10", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "stdout-00000000000000000000")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("segment below replay-from still present: err = %v", err)
	}
	buf := make([]byte, 64)
	if _, err := s.ReadAt(buf, 5); !errors.Is(err, ErrTrimmed) {
		t.Fatalf("ReadAt(5) after trim: err = %v, want ErrTrimmed", err)
	}
	n, err := s.ReadAt(buf, 12)
	if err != nil || !bytes.Equal(buf[:n], spoolBytes(12, int64(n))) || n != 8 {
		t.Fatalf("ReadAt(12) after trim = %d, %v; want the 8 retained bytes", n, err)
	}

	// Segment 10 still holds byte 19, so replay-from 19 keeps it.
	mustAck(t, s, 25, 19)
	if got := s.Start(); got != 10 {
		t.Fatalf("Start() after Ack(25, 19) = %d, want 10", got)
	}

	// Segment 10 ends at 20, so it goes too. Segment 20 is the one Append writes and is kept even though
	// replay-from reaches its End.
	mustAck(t, s, 25, 25)
	if got := s.Start(); got != 20 {
		t.Fatalf("Start() after Ack(25, 25) = %d, want 20", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "stdout-00000000000000000010")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("trimmed segment 10 still present: err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "stdout-00000000000000000020")); err != nil {
		t.Fatalf("active segment 20 was removed: %v", err)
	}
	if got := s.Consumed(); got != 25 {
		t.Fatalf("Consumed() = %d, want 25", got)
	}
}

func TestSpoolCapRefusesOverRoomUntilAckOrSeal(t *testing.T) {
	s, _ := newTestSpool(t, SpoolConfig{SegmentBytes: 10, CapBytes: 30})
	if got := s.Room(); got != 30 {
		t.Fatalf("Room() on empty spool = %d, want 30", got)
	}
	mustAppend(t, s, spoolBytes(0, 30))
	if got := s.Room(); got != 0 {
		t.Fatalf("Room() at cap = %d, want 0", got)
	}
	if err := s.Append([]byte{1}); !errors.Is(err, ErrOverCap) {
		t.Fatalf("Append past Room: err = %v, want ErrOverCap", err)
	}
	if got := s.End(); got != 30 {
		t.Fatalf("End() after refused Append = %d, want 30", got)
	}

	mustAck(t, s, 10, 0)
	if got := s.Room(); got != 10 {
		t.Fatalf("Room() after Ack(10, 0) = %d, want 10", got)
	}
	if err := s.Append(spoolBytes(30, 11)); !errors.Is(err, ErrOverCap) {
		t.Fatalf("Append of 11 with Room 10: err = %v, want ErrOverCap", err)
	}
	if got := s.End(); got != 30 {
		t.Fatalf("End() after refused 11-byte Append = %d, want 30", got)
	}
	mustAppend(t, s, spoolBytes(30, 10))
	if got := s.End(); got != 40 {
		t.Fatalf("End() = %d, want 40", got)
	}

	s.Seal()
	mustAppend(t, s, spoolBytes(40, 25))
	if got := s.End(); got != 65 {
		t.Fatalf("End() after sealed Append = %d, want 65", got)
	}
}

func TestSpoolBadAckRefused(t *testing.T) {
	cases := []struct {
		name               string
		consumed, replayAt int64
	}{
		{"consumed past End", 31, 0},
		{"replay-from negative", 10, -1},
		{"replay-from above consumed", 10, 11},
		{"consumed below previous", 9, 9},
		{"replay-from below previous", 10, 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newTestSpool(t, SpoolConfig{SegmentBytes: 10, CapBytes: 1000})
			mustAppend(t, s, spoolBytes(0, 30))
			mustAck(t, s, 10, 8)

			if err := s.Ack(tc.consumed, tc.replayAt); !errors.Is(err, ErrBadAck) {
				t.Fatalf("Ack(%d, %d): err = %v, want ErrBadAck", tc.consumed, tc.replayAt, err)
			}
			if got := s.Consumed(); got != 10 {
				t.Fatalf("Consumed() after refused Ack = %d, want 10", got)
			}
		})
	}
}

func TestNewSpoolRefusesExistingSegment(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "stdout-00000000000000000000"), nil, spoolTestFileMode); err != nil {
		t.Fatalf("seed segment: %v", err)
	}
	if _, err := NewSpool(dir, SpoolConfig{}); !errors.Is(err, ErrSpoolExists) {
		t.Fatalf("NewSpool over existing segment: err = %v, want ErrSpoolExists", err)
	}
}

func TestNewSpoolIgnoresUnrelatedFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), nil, spoolTestFileMode); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	s, err := NewSpool(dir, SpoolConfig{})
	if err != nil {
		t.Fatalf("NewSpool beside an unrelated file: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestSpoolChangedClosesOnAppendAndAck(t *testing.T) {
	s, _ := newTestSpool(t, SpoolConfig{SegmentBytes: 10, CapBytes: 1000})
	ch := s.Changed()
	assertOpen(t, ch, "before any change")

	mustAppend(t, s, spoolBytes(0, 3))
	assertClosed(t, ch, "after Append")

	ch = s.Changed()
	assertOpen(t, ch, "after the previous change was consumed")
	mustAck(t, s, 3, 0)
	assertClosed(t, ch, "after Ack")
}

func TestSpoolDefaultsFillZeroConfig(t *testing.T) {
	const wantCap = 64 << 20
	s, dir := newTestSpool(t, SpoolConfig{})
	if got := s.Room(); got != wantCap {
		t.Fatalf("Room() with zero config = %d, want %d", got, wantCap)
	}
	mustAppend(t, s, spoolBytes(0, DefaultSegmentBytes+1))
	if _, err := os.Stat(filepath.Join(dir, "stdout-00000000000004194304")); err != nil {
		t.Fatalf("second default-sized segment: %v", err)
	}
}

func TestSpoolCloseEndsAppends(t *testing.T) {
	dir := t.TempDir()
	s, err := NewSpool(dir, SpoolConfig{SegmentBytes: 10, CapBytes: 1000})
	if err != nil {
		t.Fatalf("NewSpool: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := s.Append([]byte{1}); err == nil {
		t.Fatal("Append after Close succeeded")
	}
}

func TestSpoolConcurrentAppendAndAck(t *testing.T) {
	s, _ := newTestSpool(t, SpoolConfig{SegmentBytes: 16, CapBytes: 1 << 20})
	done := make(chan error, 1)
	go func() {
		for off := int64(0); off < 1000; off += 10 {
			if err := s.Append(spoolBytes(off, 10)); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()

	for finished := false; !finished; {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("concurrent Append: %v", err)
			}
			finished = true
		default:
		}
		end := s.End()
		if err := s.Ack(end, end); err != nil {
			t.Fatalf("Ack(%d, %d) during appends: %v", end, end, err)
		}
	}

	if got := s.End(); got != 1000 {
		t.Fatalf("End() = %d, want 1000", got)
	}
	buf := make([]byte, 10)
	n, err := s.ReadAt(buf, s.Start())
	if err != nil || n == 0 || !bytes.Equal(buf[:n], spoolBytes(s.Start(), int64(n))) {
		t.Fatalf("ReadAt(Start %d) = %d, %v", s.Start(), n, err)
	}
}
