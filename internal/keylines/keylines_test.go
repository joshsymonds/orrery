package keylines

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// loadLine is the line scripts/gate-remote.sh prints as it starts a command here, with the load and CPU count.
const loadLine = "gate-remote: running just check here (no box; load 139.20 121.48 131.33, nproc 32)"

// collect returns the key lines c keeps once each of writes has been written to one Writer of c in turn and the
// Writer flushed.
func collect(writes ...string) []string {
	var c Collector
	w := c.Writer()
	for _, write := range writes {
		if _, err := w.Write([]byte(write)); err != nil {
			panic(err)
		}
	}
	w.Flush()
	return c.Lines()
}

// TestKeyNamesTheGateRemoteAndFailureLines: each shape of gate-remote and failure line is a key line, read without
// its terminal escapes and with each part a carriage return ends read as a line, and the other lines of a gate's output
// are not.
func TestKeyNamesTheGateRemoteAndFailureLines(t *testing.T) {
	t.Parallel()
	for _, line := range []string{
		loadLine,
		"gate-remote: running just check on orrery@orrery-gate-khw (load 3.10 2.00 1.50, nproc 64)",
		"gate-remote: just check exited 124 after 647s here",
		"gate-remote: orrery@orrery-gate-lqz did not answer; waiting for an orrery-gate box (log: /run/x.log)",
		"--- FAIL: TestX (0.10s)",
		"    --- FAIL: TestX/sub (0.00s)",
		"    b_test.go:12: got 1, want 2",
		"FAIL",
		"FAIL\tgithub.com/joshsymonds/orrery/internal/store\t0.058s",
		"FAIL\tgithub.com/joshsymonds/orrery/internal/store [build failed]",
		"FAIL: a run that could not be collected never ended",
		"panic: test timed out after 10m0s",
		"fatal error: concurrent map writes",
		"internal/store/x.go:12:3: ineffectual assignment to err (ineffassign)",
		"internal/build/taskversion_test.go:61:51: undefined: versionWith",
		`level=error msg="Timeout exceeded: try increasing it by passing --timeout option"`,
		"tests exceeded 360s budget",
		"error: recipe `cover` failed with exit code 124",
		"> Task :app:testDebugUnitTest FAILED",
		"BUILD FAILED in 1m 15s",
		"FAILURE: Build failed with an exception.",
		"Execution failed for task ':app:testDebugUnitTest'.",
		"e: file:///w/android/app/src/test/kotlin/X.kt:38:9 No parameter with name 'x' found.",
		"web/src/loadepics.test.ts(194,54): error TS2339: Property 'held' does not exist.",
		"✖ failing tests:",
		"write /tmp/x: no space left on device",
		"runtime: out of memory",
		"bash: line 1: 12345 Killed                  go test ./...",
		"\x1b[31m--- FAIL: TestColored (0.01s)\x1b[0m",
		"building 3/9\r--- FAIL: TestAfterProgress (0.02s)",
	} {
		if !Key(line) {
			t.Errorf("Key(%q) = false, want a key line", line)
		}
	}
	for _, line := range []string{
		"",
		"ok  \tgithub.com/joshsymonds/orrery/internal/store\t47.378s\tcoverage: 14.7% of statements",
		"    gate-remote: running bash -c x on orrery@fake-box (load 66.79 126.85 158.07, nproc 32)",
		"golangci-lint run",
		"scripts/gate-remote.sh just check",
		"=== RUN   TestX",
		"FAILURES are counted below",
		"✓ Building devenv-shell in 129ms",
		"the gate was killed by its limit",
	} {
		if Key(line) {
			t.Errorf("Key(%q) = true, want no key line", line)
		}
	}
}

// TestWriterSplitsLinesAcrossWrites: a line may come in several writes, a carriage return before its newline is taken
// off, and the line a stream ends on without a newline is added once the Writer is flushed, and not before.
func TestWriterSplitsLinesAcrossWrites(t *testing.T) {
	t.Parallel()
	var c Collector
	w := c.Writer()
	for _, write := range []string{"ok  \tpkg\t1s\n--- FA", "IL: TestX (0.1", "0s)\r\nok\n", "FAIL\tpkg"} {
		if n, err := w.Write([]byte(write)); n != len(write) || err != nil {
			t.Fatalf("Write(%q) = %d, %v; want %d, nil", write, n, err, len(write))
		}
	}
	if got, want := c.Lines(), []string{"--- FAIL: TestX (0.10s)"}; !slices.Equal(got, want) {
		t.Errorf("Lines() before Flush = %q, want %q: the unterminated line waits for its end", got, want)
	}
	w.Flush()
	if got, want := c.Lines(), []string{"--- FAIL: TestX (0.10s)", "FAIL\tpkg"}; !slices.Equal(got, want) {
		t.Errorf("Lines() after Flush = %q, want %q", got, want)
	}
	w.Flush()
	if got := c.Lines(); len(got) != 2 {
		t.Errorf("Lines() after a second Flush = %q, want the same two lines", got)
	}
}

// TestLongLineKeepsItsStartAndEnd: a line longer than LineMax keeps its start and its end around the cut mark, in at
// most LineMax bytes, however it was written, so a gate-remote line keeps both its command and its load. The word that
// runs into the cut on either side is left out whole.
func TestLongLineKeepsItsStartAndEnd(t *testing.T) {
	t.Parallel()
	start := "gate-remote: running just check here (no orrery-gate box could come up:"
	// The token starts 6 bytes before the end of the start the line keeps, so the cut runs into it.
	line := start + " " + strings.Repeat("w", cutHalf-8-len(start)) + " ghp_EXAMPLE0123456789abcdefABCDEF0123" +
		strings.Repeat(" more", 400) + "; load 16.56 28.07 22.10, nproc 32)"
	if !strings.Contains(line[cutHalf-6:cutHalf+6], "ghp_EXAMPLE") {
		t.Fatalf("the token does not cross the cut at %d: %q", cutHalf, line[cutHalf-6:cutHalf+6])
	}
	whole := collect(line)
	bytewise := make([]string, 0, len(line)+1)
	for i := range len(line) {
		bytewise = append(bytewise, line[i:i+1])
	}
	byByte := collect(append(bytewise, "\n")...)
	if len(whole) != 1 || !slices.Equal(whole, byByte) {
		t.Fatalf("lines = %q written whole and %q written a byte at a time, want the same one line", whole, byByte)
	}
	got := whole[0]
	if len(got) > LineMax || !strings.HasPrefix(got, start) || !strings.Contains(got, cutMark) ||
		!strings.HasSuffix(got, "; load 16.56 28.07 22.10, nproc 32)") {
		t.Errorf("kept line (%d bytes) = %q, want at most %d bytes with its start, the cut mark and its end", len(got),
			got, LineMax)
	}
	if strings.Contains(got, "EXAMPLE") || strings.Contains(got, "ghp_") {
		t.Errorf("kept line %q holds part of the token the cut ran into", got)
	}
}

// TestLongLineCutKeepsWholeRunes: a long line of one word, with no space to cut at, keeps what it has of the word on
// each side of the cut, and no rune the cut split.
func TestLongLineCutKeepsWholeRunes(t *testing.T) {
	t.Parallel()
	got := collect("FAIL: " + strings.Repeat("é", 2000) + "\n")
	if len(got) != 1 || !utf8.ValidString(got[0]) || len(got[0]) > LineMax || !strings.Contains(got[0], cutMark) {
		t.Fatalf("lines = %q, want one cut line of whole runes", got)
	}
}

// TestCollectorKeepsTheFirstAndLastGateRemoteLines: of many gate-remote lines, the first remoteFirst and the last
// remoteLast are kept, in order among the failure lines.
func TestCollectorKeepsTheFirstAndLastGateRemoteLines(t *testing.T) {
	t.Parallel()
	var writes []string
	for i := 1; i <= 10; i++ {
		writes = append(writes, fmt.Sprintf("gate-remote: line %d\n", i))
		if i == 3 {
			writes = append(writes, "--- FAIL: TestMiddle (0.01s)\n")
		}
	}
	got := collect(writes...)
	want := []string{
		"gate-remote: line 1", "gate-remote: line 2", "gate-remote: line 3", "--- FAIL: TestMiddle (0.01s)",
		"gate-remote: line 4", "gate-remote: line 5", "gate-remote: line 6", "gate-remote: line 9",
		"gate-remote: line 10",
	}
	if !slices.Equal(got, want) {
		t.Errorf("Lines() = %q, want %q", got, want)
	}
}

// TestCollectorKeepsTheNewestFailureLinesThatFit: failure lines fill the room the gate-remote lines leave, newest first,
// up to MaxLines lines and MaxBytes bytes.
func TestCollectorKeepsTheNewestFailureLinesThatFit(t *testing.T) {
	t.Parallel()
	writes := []string{loadLine + "\n"}
	for i := range 100 {
		writes = append(writes, fmt.Sprintf("FAIL: check %03d\n", i))
	}
	got := collect(writes...)
	if len(got) != MaxLines || got[0] != loadLine || got[1] != "FAIL: check 037" ||
		got[MaxLines-1] != "FAIL: check 099" {
		t.Errorf("Lines() = %d lines from %q to %q, want %d: the load line, then checks 037 to 099", len(got),
			got[0], got[len(got)-1], MaxLines)
	}

	long := strings.Repeat("x", 490)
	writes = writes[:1]
	for i := range 40 {
		writes = append(writes, fmt.Sprintf("FAIL: %02d %s\n", i, long))
	}
	got = collect(writes...)
	used := 0
	for _, line := range got {
		used += len(line) + 1
	}
	if used > MaxBytes || got[0] != loadLine || !strings.HasPrefix(got[1], "FAIL: 24 ") ||
		!strings.HasPrefix(got[len(got)-1], "FAIL: 39 ") {
		t.Errorf("Lines() = %d lines holding %d bytes, from %.12q, %.12q to %.12q; want at most %d bytes: the load "+
			"line, then the newest that fit, 24 to 39", len(got), used, got[0], got[1], got[len(got)-1], MaxBytes)
	}
}

// TestCollectorCountsARepeatedFailureLineOnce: a failure line equal to one kept already is kept once, at its newest
// place.
func TestCollectorCountsARepeatedFailureLineOnce(t *testing.T) {
	t.Parallel()
	got := collect("FAIL\n", "--- FAIL: TestA (0.00s)\n", "FAIL\n")
	if want := []string{"--- FAIL: TestA (0.00s)", "FAIL"}; !slices.Equal(got, want) {
		t.Errorf("Lines() = %q, want %q", got, want)
	}
	if got := collect("ok\n"); got != nil {
		t.Errorf("Lines() of an output with no key line = %q, want nil", got)
	}
}

// TestCollectorTakesStreamsAtOnce: two streams written at once through Writers of their own keep each of their lines
// whole.
func TestCollectorTakesStreamsAtOnce(t *testing.T) {
	t.Parallel()
	var c Collector
	var writers sync.WaitGroup
	for _, stream := range []string{"stdout", "stderr"} {
		w := c.Writer()
		writers.Go(func() {
			for i := range 20 {
				line := fmt.Sprintf("FAIL: %s %02d\n", stream, i)
				for _, half := range []string{line[:5], line[5:]} {
					if _, err := w.Write([]byte(half)); err != nil {
						t.Errorf("Write: %v", err)
					}
				}
			}
			w.Flush()
		})
	}
	writers.Wait()
	got := c.Lines()
	for _, stream := range []string{"stdout", "stderr"} {
		for i := range 20 {
			if want := fmt.Sprintf("FAIL: %s %02d", stream, i); !slices.Contains(got, want) {
				t.Errorf("Lines() lacks %q: %q", want, got)
			}
		}
	}
}

// TestCapHoldsLinesToTheCaps: Cap returns a Collector's lines as they are, keeps every other line within the same caps,
// a line that is not a gate-remote line counting as a failure line, cuts a long line, reads a line with newlines as the
// lines they end, and leaves a blank line out.
func TestCapHoldsLinesToTheCaps(t *testing.T) {
	t.Parallel()
	writes := make([]string, 0, 82)
	writes = append(writes, loadLine+"\n", "--- FAIL: TestX (0.10s)\n")
	for i := range 80 {
		writes = append(writes, fmt.Sprintf("FAIL: check %03d\n", i))
	}
	lines := collect(writes...)
	if got := Cap(lines); !slices.Equal(got, lines) {
		t.Errorf("Cap(Lines()) = %q, want the lines as they are: %q", got, lines)
	}
	if got := Cap(nil); got != nil {
		t.Errorf("Cap(nil) = %q, want nil", got)
	}
	got := Cap([]string{"plain one\nplain two\n", " ", strings.Repeat("y", 2*LineMax)})
	if len(got) != 3 || got[0] != "plain one" || got[1] != "plain two" || len(got[2]) > LineMax {
		t.Errorf("Cap() = %q, want the two plain lines and the long one cut, and no blank line", got)
	}
	many := make([]string, 0, 100)
	for i := range 100 {
		many = append(many, fmt.Sprintf("line %03d", i))
	}
	if got := Cap(many); len(got) != MaxLines || got[0] != "line 036" {
		t.Errorf("Cap() of 100 lines = %d lines from %q, want the newest %d", len(got), got[0], MaxLines)
	}
}

// TestPlainTakesOutTerminalEscapes: Plain takes a color, an operating system command and a two-byte escape out of a
// line, and leaves the rest as it was.
func TestPlainTakesOutTerminalEscapes(t *testing.T) {
	t.Parallel()
	in := "\x1b[35;1mwarning:\x1b[0m title\x1b]0;tab\x07 and\x1bM done"
	if got, want := Plain(in), "warning: title and done"; got != want {
		t.Errorf("Plain(%q) = %q, want %q", in, got, want)
	}
}
