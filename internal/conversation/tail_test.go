package conversation

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// countingReaderAt counts the bytes ReadAt returns from src.
type countingReaderAt struct {
	src  io.ReaderAt
	read int64
}

func (c *countingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	n, err := c.src.ReadAt(p, off)
	c.read += int64(n)
	return n, err
}

// collectTail runs ReadTail over data and returns the visited lines and the bytes read.
func collectTail(t *testing.T, data []byte, size, maxBytes int64) ([]string, int64) {
	t.Helper()
	src := &countingReaderAt{src: bytes.NewReader(data)}
	var lines []string
	err := ReadTail(src, size, maxBytes, func(line []byte) bool {
		lines = append(lines, string(line))
		return true
	})
	if err != nil {
		t.Fatalf("ReadTail: %v", err)
	}
	return lines, src.read
}

func TestReadTailTable(t *testing.T) {
	// The long line counts upward, so its blocks differ and a misordered assembly changes it.
	var longBuf strings.Builder
	for i := 0; longBuf.Len() < 2*TailBlockSize+100; i++ {
		longBuf.WriteString(strconv.Itoa(i))
		longBuf.WriteByte(',')
	}
	long := longBuf.String()
	tests := []struct {
		name     string
		data     string
		size     int64 // -1 means len(data)
		maxBytes int64
		want     []string
	}{
		{"all lines newest first", "a\nbb\nccc\n", -1, 1 << 20, []string{"ccc", "bb", "a"}},
		{"trailing line without newline is dropped", "a\nbb\nccc", -1, 1 << 20, []string{"bb", "a"}},
		{"bytes at and after size are not visited", "a\nbb\nccc\n", 5, 1 << 20, []string{"bb", "a"}},
		{"empty lines are skipped", "a\n\nbb\n\n", -1, 1 << 20, []string{"bb", "a"}},
		{"window starting mid-line drops the partial line", "aaaa\nbbbbbbbb\ncc\n", -1, 6, []string{"cc"}},
		{"line longer than one block is assembled whole", "first\n" + long + "\nlast\n", -1, 1 << 20,
			[]string{"last", long, "first"}},
		{"maxBytes smaller than the newest line yields nothing", "a\nbb\nccc\n", -1, 3, nil},
		{"maxBytes of zero yields nothing", "a\nbb\n", -1, 0, nil},
		{"no newline anywhere yields nothing", "abcdef", -1, 1 << 20, nil},
		{"empty file yields nothing", "", -1, 1 << 20, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			size := tc.size
			if size < 0 {
				size = int64(len(tc.data))
			}
			got, read := collectTail(t, []byte(tc.data), size, tc.maxBytes)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("lines = %q, want %q", got, tc.want)
			}
			if read > tc.maxBytes {
				t.Fatalf("read %d bytes, limit %d", read, tc.maxBytes)
			}
		})
	}
}

func TestReadTailStopsWhenVisitReturnsFalse(t *testing.T) {
	data := []byte("a\nbb\nccc\n")
	var lines []string
	err := ReadTail(bytes.NewReader(data), int64(len(data)), 1<<20, func(line []byte) bool {
		lines = append(lines, string(line))
		return false
	})
	if err != nil {
		t.Fatalf("ReadTail: %v", err)
	}
	if want := []string{"ccc"}; !reflect.DeepEqual(lines, want) {
		t.Fatalf("lines = %q, want %q", lines, want)
	}
}

func TestReadTailSparseFileReadsOnlyTheTail(t *testing.T) {
	const fileSize = 200 << 20
	f, err := os.Create(filepath.Join(t.TempDir(), "transcript.jsonl"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	if err := f.Truncate(fileSize); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	tail := []string{`{"type":"user","n":1}`, `{"type":"assistant","n":2}`, `{"type":"user","n":3}`}
	// The leading newline ends the sparse zero bytes so the first line is complete.
	payload := "\n" + strings.Join(tail, "\n") + "\n"
	if _, err := f.WriteAt([]byte(payload), fileSize-int64(len(payload))); err != nil {
		t.Fatalf("write tail: %v", err)
	}

	src := &countingReaderAt{src: f}
	var lines []string
	err = ReadTail(src, fileSize, MaxTailBytes, func(line []byte) bool {
		lines = append(lines, string(line))
		return true
	})
	if err != nil {
		t.Fatalf("ReadTail: %v", err)
	}
	if want := []string{tail[2], tail[1], tail[0]}; !reflect.DeepEqual(lines, want) {
		t.Fatalf("lines = %q, want %q", lines, want)
	}
	if src.read > MaxTailBytes {
		t.Fatalf("read %d bytes from a %d-byte file, limit %d", src.read, fileSize, MaxTailBytes)
	}
}

func TestReadTailOversizeNewestLineYieldsNothing(t *testing.T) {
	data := []byte("old\n" + strings.Repeat("x", MaxTailBytes+100) + "\n")
	got, read := collectTail(t, data, int64(len(data)), MaxTailBytes)
	if len(got) != 0 {
		t.Fatalf("lines = %d, want none", len(got))
	}
	if read > MaxTailBytes {
		t.Fatalf("read %d bytes, limit %d", read, MaxTailBytes)
	}
}
