package proc

import (
	"strings"
	"sync"
	"testing"
)

func TestTailKeepsWritesUnderCapacityWhole(t *testing.T) {
	tail := newTail(10)
	for _, chunk := range []string{"abc", "de", "fgh"} {
		if _, err := tail.Write([]byte(chunk)); err != nil {
			t.Fatalf("Write(%q) error = %v", chunk, err)
		}
	}
	if got, want := tail.String(), "abcdefgh"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestTailKeepsOnlyLastCapacityBytesAfterOverflow(t *testing.T) {
	tail := newTail(5)
	for _, chunk := range []string{"abc", "defgh", "ij"} {
		if _, err := tail.Write([]byte(chunk)); err != nil {
			t.Fatalf("Write(%q) error = %v", chunk, err)
		}
	}
	if got, want := tail.String(), "fghij"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestTailKeepsLastCapacityBytesOfOversizedWrite(t *testing.T) {
	tail := newTail(4)
	if _, err := tail.Write([]byte("0123456789")); err != nil {
		t.Fatalf("Write error = %v", err)
	}
	if got, want := tail.String(), "6789"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestTailKeepsLastCapacityBytesWhenOversizedWriteFollowsData(t *testing.T) {
	tail := newTail(4)
	for _, chunk := range []string{"ab", "0123456789", "cd"} {
		if _, err := tail.Write([]byte(chunk)); err != nil {
			t.Fatalf("Write(%q) error = %v", chunk, err)
		}
	}
	if got, want := tail.String(), "89cd"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestTailWriteReportsFullLengthWithNilError(t *testing.T) {
	tail := newTail(4)
	for _, chunk := range []string{"", "ab", "abcd", "0123456789"} {
		n, err := tail.Write([]byte(chunk))
		if err != nil {
			t.Fatalf("Write(%q) error = %v", chunk, err)
		}
		if n != len(chunk) {
			t.Fatalf("Write(%q) n = %d, want %d", chunk, n, len(chunk))
		}
	}
}

func TestTailConcurrentWritersLeaveCapacityBytesOfWrittenData(t *testing.T) {
	const (
		capacity = 64
		writers  = 8
		rounds   = 200
	)
	tail := newTail(capacity)
	var wg sync.WaitGroup
	for writer := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			marker := []byte(strings.Repeat(string(rune('a'+writer)), 1+writer*9))
			for range rounds {
				if _, err := tail.Write(marker); err != nil {
					t.Errorf("writer %d Write error = %v", writer, err)
					return
				}
			}
		}()
	}
	wg.Wait()

	got := tail.String()
	if len(got) != capacity {
		t.Fatalf("len(String()) = %d, want %d", len(got), capacity)
	}
	const allowed = "abcdefgh"
	for i := range len(got) {
		if strings.IndexByte(allowed, got[i]) < 0 {
			t.Fatalf("String()[%d] = %q, not written data", i, got[i])
		}
	}
}
