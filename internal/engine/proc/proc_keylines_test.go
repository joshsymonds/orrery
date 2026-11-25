package proc

import (
	"slices"
	"strings"
	"testing"
)

// keyLinesScript writes a gate-remote line and then filler to stderr, and a failing test, filler and a failing package
// with no newline after it to stdout, with the files named by $1 and $2 as the filler of each.
const keyLinesScript = `printf 'gate-remote: running just check here (load 9.00 8.00 7.00, nproc 4)\n' >&2
cat "$1" >&2
printf -- '--- FAIL: TestX (0.10s)\n'
cat "$2"
printf 'FAIL\tgithub.com/x/y'
`

// keyLinesFiller returns lines of plain output, more bytes in all than the tails the key line tests keep.
func keyLinesFiller(t *testing.T, name string) string {
	t.Helper()
	return writeFile(t, name, strings.Repeat("ok  \tgithub.com/x/filler\t0.01s\n", 200))
}

// TestKeyLinesComeFromTheWholeOfEachStream: with KeyLines, the key lines of stdout and stderr are kept from the whole of
// each stream, the line a stream ends on without a newline included, although the tails have cut them off.
func TestKeyLinesComeFromTheWholeOfEachStream(t *testing.T) {
	const capacity = 64
	cfg := Config{StdoutCapacity: capacity, StderrCapacity: capacity, KeyLines: true}
	p := startWith(t, cfg, keyLinesScript, keyLinesFiller(t, "stderr.txt"), keyLinesFiller(t, "stdout.txt"))
	if err := p.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	got := p.KeyLines()
	for _, want := range []string{
		"gate-remote: running just check here (load 9.00 8.00 7.00, nproc 4)",
		"--- FAIL: TestX (0.10s)",
		"FAIL\tgithub.com/x/y",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("KeyLines() = %q, want it to hold %q", got, want)
		}
	}
	if len(got) != 3 {
		t.Errorf("KeyLines() = %q, want only the three key lines", got)
	}
	if strings.Contains(p.Stderr(), "gate-remote") || strings.Contains(p.StdoutTail(), "--- FAIL") {
		t.Errorf("tails %q and %q hold the early key lines, want them cut off", p.Stderr(), p.StdoutTail())
	}
	if i, j := slices.Index(got, "--- FAIL: TestX (0.10s)"), slices.Index(got, "FAIL\tgithub.com/x/y"); i > j {
		t.Errorf("KeyLines() = %q, want stdout's lines in the order they were written", got)
	}
}

// TestKeyLinesAreNilUnlessAskedFor: without KeyLines, no key line is kept, whatever the child writes.
func TestKeyLinesAreNilUnlessAskedFor(t *testing.T) {
	cfg := Config{StdoutCapacity: 1 << 10}
	p := startWith(t, cfg, keyLinesScript, keyLinesFiller(t, "stderr.txt"), keyLinesFiller(t, "stdout.txt"))
	if err := p.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got := p.KeyLines(); got != nil {
		t.Errorf("KeyLines() without Config.KeyLines = %q, want nil", got)
	}
}
