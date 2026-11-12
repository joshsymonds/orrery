package route_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/joshsymonds/orrery/internal/route"
)

const (
	testHost        = "synthetic-host"
	testSession     = "synthetic-session"
	testPane        = "%synthetic-pane"
	concurrentCount = 8
	concurrentEach  = 50
	longReasonBytes = 4096
)

func paneLine() route.Line {
	return route.Line{
		Time:   1_700_000_001,
		Host:   testHost,
		Pane:   testPane,
		State:  "waiting",
		Route:  route.Desk,
		Reason: "at the desk",
	}
}

func sessionLine() route.Line {
	return route.Line{
		Time:    1_700_000_002,
		Host:    testHost,
		Session: testSession,
		State:   "done",
		Route:   route.DeskPhone,
		Reason:  "away",
	}
}

func closeLog(t *testing.T, l *route.Log) {
	t.Helper()
	if err := l.Close(); err != nil {
		t.Fatalf("close log: %v", err)
	}
}

func readLines(t *testing.T, path string) [][]byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if len(data) == 0 {
		return nil
	}
	if data[len(data)-1] != '\n' {
		t.Fatalf("log does not end with a newline: %q", data)
	}
	return bytes.Split(bytes.TrimSuffix(data, []byte("\n")), []byte("\n"))
}

func decodeLine(t *testing.T, raw []byte) route.Line {
	t.Helper()
	var line route.Line
	if err := json.Unmarshal(raw, &line); err != nil {
		t.Fatalf("decode line %q: %v", raw, err)
	}
	return line
}

func keysOf(t *testing.T, raw []byte) map[string]bool {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("decode fields %q: %v", raw, err)
	}
	keys := make(map[string]bool, len(fields))
	for key := range fields {
		keys[key] = true
	}
	return keys
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s mode = %o, want %o", path, got, want)
	}
}

func TestOpenLogCreatesMissingDirectoryAndFilePrivately(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "orrery")
	path := filepath.Join(dir, "notices.jsonl")

	l, err := route.OpenLog(path)
	if err != nil {
		t.Fatalf("OpenLog: %v", err)
	}
	closeLog(t, l)

	assertMode(t, dir, 0o700)
	assertMode(t, path, 0o600)
}

func TestWriteAppendsOneJSONLinePerNotice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notices.jsonl")
	l, err := route.OpenLog(path)
	if err != nil {
		t.Fatalf("OpenLog: %v", err)
	}
	pane, session := paneLine(), sessionLine()
	if err := l.Write(pane); err != nil {
		t.Fatalf("Write pane line: %v", err)
	}
	if err := l.Write(session); err != nil {
		t.Fatalf("Write session line: %v", err)
	}
	closeLog(t, l)

	lines := readLines(t, path)
	if len(lines) != 2 {
		t.Fatalf("log has %d lines, want 2: %q", len(lines), lines)
	}
	if got := decodeLine(t, lines[0]); got != pane {
		t.Errorf("first line decodes to %+v, want %+v", got, pane)
	}
	if got := decodeLine(t, lines[1]); got != session {
		t.Errorf("second line decodes to %+v, want %+v", got, session)
	}

	paneKeys := keysOf(t, lines[0])
	for _, key := range []string{"time", "host", "pane", "state", "route", "reason"} {
		if !paneKeys[key] {
			t.Errorf("pane line lacks key %q: %q", key, lines[0])
		}
	}
	if paneKeys["session"] {
		t.Errorf("pane line carries a session key: %q", lines[0])
	}

	sessionKeys := keysOf(t, lines[1])
	for _, key := range []string{"time", "host", "session", "state", "route", "reason"} {
		if !sessionKeys[key] {
			t.Errorf("session line lacks key %q: %q", key, lines[1])
		}
	}
	if sessionKeys["pane"] {
		t.Errorf("session line carries a pane key: %q", lines[1])
	}
}

func TestOpenLogAppendsAfterExistingLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notices.jsonl")
	first, err := route.OpenLog(path)
	if err != nil {
		t.Fatalf("OpenLog first: %v", err)
	}
	if err := first.Write(paneLine()); err != nil {
		t.Fatalf("Write first: %v", err)
	}
	closeLog(t, first)

	second, err := route.OpenLog(path)
	if err != nil {
		t.Fatalf("OpenLog second: %v", err)
	}
	if err := second.Write(sessionLine()); err != nil {
		t.Fatalf("Write second: %v", err)
	}
	closeLog(t, second)

	lines := readLines(t, path)
	if len(lines) != 2 {
		t.Fatalf("log has %d lines, want 2: %q", len(lines), lines)
	}
	if got := decodeLine(t, lines[0]); got != paneLine() {
		t.Errorf("first line decodes to %+v, want %+v", got, paneLine())
	}
	if got := decodeLine(t, lines[1]); got != sessionLine() {
		t.Errorf("second line decodes to %+v, want %+v", got, sessionLine())
	}
}

func concurrentLine(n int) route.Line {
	return route.Line{
		Time:   int64(n),
		Host:   testHost,
		Pane:   fmt.Sprintf("%%synthetic-%d", n%concurrentCount),
		State:  "waiting",
		Route:  route.Phone,
		Reason: strings.Repeat("r", longReasonBytes),
	}
}

func TestConcurrentWritesLandAsWholeLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notices.jsonl")
	l, err := route.OpenLog(path)
	if err != nil {
		t.Fatalf("OpenLog: %v", err)
	}

	total := concurrentCount * concurrentEach
	errs := make([]error, concurrentCount)
	var wg sync.WaitGroup
	for g := range concurrentCount {
		wg.Go(func() {
			for i := range concurrentEach {
				if err := l.Write(concurrentLine(g*concurrentEach + i)); err != nil {
					errs[g] = err
					return
				}
			}
		})
	}
	wg.Wait()
	closeLog(t, l)
	for g, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d Write: %v", g, err)
		}
	}

	lines := readLines(t, path)
	if len(lines) != total {
		t.Fatalf("log has %d lines, want %d", len(lines), total)
	}
	seen := make(map[int64]bool, total)
	for _, raw := range lines {
		got := decodeLine(t, raw)
		if seen[got.Time] {
			t.Fatalf("notice %d landed twice", got.Time)
		}
		seen[got.Time] = true
		if want := concurrentLine(int(got.Time)); got != want {
			t.Fatalf("notice %d decodes to a different line than written", got.Time)
		}
	}
}

func TestWriteAfterCloseReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notices.jsonl")
	l, err := route.OpenLog(path)
	if err != nil {
		t.Fatalf("OpenLog: %v", err)
	}
	closeLog(t, l)

	if err := l.Write(paneLine()); err == nil {
		t.Fatal("Write after Close returned nil, want an error")
	}
}
