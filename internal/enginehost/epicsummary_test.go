package enginehost

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/exectest"
	"github.com/joshsymonds/orrery/internal/store"
)

// The ids of the epics the summary tests store.
const (
	summaryEpicA = "orrery-1a2b3c4/epic-a"
	summaryEpicB = "orrery-1a2b3c4/epic-b"
)

// summaryStamp is the unix time the summary tests create their epics at.
const summaryStamp = 1794700000

// summaryResult is the result line the fake claude prints for a valid summary. Its summary is the fake's own echo.
const summaryResult = `{"type":"result","subtype":"success","is_error":false,"structured_output":{"summary":"Fake summary."}}`

// summaryFakeScript is the fake claude the summary tests run. A run appends start to dir/calls, writes its argv to
// dir/argv and its stdin to dir/stdin, prints dir/stdout, appends end to dir/calls and exits with status. So the calls
// file holds each run's start and end in the order the runs happened.
const summaryFakeScript = `#!/bin/sh
dir='%s'
echo start >> "$dir/calls"
for a in "$@"; do printf '%%s\n' "$a"; done > "$dir/argv"
cat > "$dir/stdin"
cat "$dir/stdout"
echo end >> "$dir/calls"
exit %d
`

// summaryClaude writes the fake claude into dir, set to print stdout and exit with status, and returns its path.
func summaryClaude(t *testing.T, dir, stdout string, status int) string {
	t.Helper()
	summarySetStdout(t, dir, stdout)
	path := filepath.Join(dir, "claude")
	exectest.WriteFile(t, path, fmt.Appendf(nil, summaryFakeScript, dir, status), 0o700)
	return path
}

// summarySetStdout sets what the fake claude in dir prints from its next run on.
func summarySetStdout(t *testing.T, dir, stdout string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "stdout"), []byte(stdout), 0o600); err != nil {
		t.Fatalf("write fake stdout: %v", err)
	}
}

// summaryRead returns the contents of name in dir, or fails the test.
func summaryRead(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

// summaryCalls returns the start and end marks the fake claude in dir has written, in order.
func summaryCalls(t *testing.T, dir string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "calls"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read calls: %v", err)
	}
	return strings.Fields(string(data))
}

// summaryRuns counts the runs of the fake claude in dir.
func summaryRuns(t *testing.T, dir string) int {
	t.Helper()
	runs := 0
	for _, mark := range summaryCalls(t, dir) {
		if mark == "start" {
			runs++
		}
	}
	return runs
}

// createSummaryEpic stores an accepted epic with id, created at the unix second created, whose contract has no summary.
func createSummaryEpic(t *testing.T, st *store.Store, id string, created int64) {
	t.Helper()
	at := time.Unix(created, 0)
	if err := st.CreateEpic(t.Context(), store.EpicRow{
		ID: id, Repository: "/work/synthetic-repo", Title: "Synthetic epic", Intent: "Synthetic intent.",
		Care: "serious", Approach: "Synthetic approach.", Phase: "accepted", Created: at, Changed: at,
	}); err != nil {
		t.Fatalf("CreateEpic %s: %v", id, err)
	}
}

// storedSummaryOf returns the summary of the epic id as the store reads it.
func storedSummaryOf(t *testing.T, st *store.Store, id string) string {
	t.Helper()
	row, err := st.Epic(t.Context(), id)
	if err != nil {
		t.Fatalf("Epic %s: %v", id, err)
	}
	return row.Summary
}

// summaryLoop is an EpicSummaries started over a store, and the channels a test drives it with.
type summaryLoop struct {
	ticks  chan time.Time
	passes chan struct{}
	done   chan struct{}
	cancel context.CancelFunc
}

// startSummaryLoop starts an EpicSummaries over st whose Haiku runs the fake claude at claude, and returns once its
// start pass is done. The test's end stops it too.
func startSummaryLoop(t *testing.T, st *store.Store, claude string) *summaryLoop {
	t.Helper()
	ticks := make(chan time.Time)
	passes := make(chan struct{}, 16)
	ctx, cancel := context.WithCancel(t.Context())
	loop := &summaryLoop{ticks: ticks, passes: passes, done: make(chan struct{}), cancel: cancel}
	summaries := NewEpicSummaries(EpicSummariesConfig{
		Store:  st,
		Haiku:  NewHaiku(HaikuConfig{Claude: claude, Environ: []string{"PATH=" + os.Getenv("PATH")}}),
		Tick:   ticks,
		Passed: func() { passes <- struct{}{} },
	})
	go func() {
		defer close(loop.done)
		summaries.Run(ctx)
	}()
	t.Cleanup(loop.stop)
	<-passes
	return loop
}

// stop ends the loop and waits for Run to return. It is safe to call more than once.
func (l *summaryLoop) stop() {
	l.cancel()
	<-l.done
}

// tick has the loop pass once more and waits until that pass is done.
func (l *summaryLoop) tick() {
	l.ticks <- time.Time{}
	<-l.passes
}

// TestEpicSummaryCallsHaikuWithSchemaAndStoresTheSummaryOnce covers R3: the call carries the summary schema and reads
// the contract of an epic created before the start, a valid summary is stored, and later ticks make no further call.
func TestEpicSummaryCallsHaikuWithSchemaAndStoresTheSummaryOnce(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	claude := summaryClaude(t, dir, summaryResult, 0)
	st := epicTestStore(t)
	createSummaryEpic(t, st, summaryEpicA, summaryStamp)

	loop := startSummaryLoop(t, st, claude)
	if got := summaryRuns(t, dir); got != 1 {
		t.Fatalf("calls after start = %d, want 1", got)
	}
	if argv := summaryRead(t, dir, "argv"); !strings.Contains(argv, "--json-schema\n"+epicSummarySchema+"\n") {
		t.Fatalf("argv = %q, want --json-schema followed by the summary schema", argv)
	}
	if stdin := summaryRead(t, dir, "stdin"); !strings.Contains(stdin, "Intent: Synthetic intent.") {
		t.Fatalf("stdin = %q, want the epic's contract", stdin)
	}
	if got := storedSummaryOf(t, st, summaryEpicA); got != "Fake summary." {
		t.Fatalf("summary after start = %q, want the fake's echo", got)
	}
	loop.tick()
	loop.tick()
	if got := summaryRuns(t, dir); got != 1 {
		t.Fatalf("calls after two ticks = %d, want still 1", got)
	}
}

// TestEpicSummaryFillsAnEpicCreatedAfterStartAtTheNextTick covers R3: a tick summarizes an epic stored after the start
// and calls for no epic that already has its summary.
func TestEpicSummaryFillsAnEpicCreatedAfterStartAtTheNextTick(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	claude := summaryClaude(t, dir, summaryResult, 0)
	st := epicTestStore(t)
	createSummaryEpic(t, st, summaryEpicA, summaryStamp)
	loop := startSummaryLoop(t, st, claude)

	createSummaryEpic(t, st, summaryEpicB, summaryStamp+60)
	loop.tick()
	if got := summaryRuns(t, dir); got != 2 {
		t.Fatalf("calls after the tick = %d, want 2: one for each epic", got)
	}
	if got := storedSummaryOf(t, st, summaryEpicB); got != "Fake summary." {
		t.Fatalf("summary of the epic created after start = %q, want the fake's echo", got)
	}
}

// TestEpicSummaryFailedOrInvalidCallLeavesTheSummaryEmptyForTheStart covers R4: a failed or invalid call leaves the
// summary empty, and no later tick of the same start calls for it again.
func TestEpicSummaryFailedOrInvalidCallLeavesTheSummaryEmptyForTheStart(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		stdout string
		status int
	}{
		{name: "claude exits nonzero", stdout: "", status: 3},
		{
			name:   "summary is blank",
			stdout: `{"type":"result","subtype":"success","is_error":false,"structured_output":{"summary":" \n "}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			claude := summaryClaude(t, dir, tc.stdout, tc.status)
			st := epicTestStore(t)
			createSummaryEpic(t, st, summaryEpicA, summaryStamp)

			loop := startSummaryLoop(t, st, claude)
			if got := summaryRuns(t, dir); got != 1 {
				t.Fatalf("calls after start = %d, want 1", got)
			}
			if got := storedSummaryOf(t, st, summaryEpicA); got != "" {
				t.Fatalf("summary after a failed call = %q, want empty", got)
			}
			loop.tick()
			if got := summaryRuns(t, dir); got != 1 {
				t.Fatalf("calls after a tick = %d, want still 1", got)
			}
		})
	}
}

// TestEpicSummarySecondStartRetriesAnEmptyEpicOnce covers R4: a start retries an epic that a failed call left empty,
// and a tick of that start calls for it no more.
func TestEpicSummarySecondStartRetriesAnEmptyEpicOnce(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	claude := summaryClaude(t, dir, "not json", 0)
	st := epicTestStore(t)
	createSummaryEpic(t, st, summaryEpicA, summaryStamp)

	first := startSummaryLoop(t, st, claude)
	first.tick()
	first.stop()
	if got := summaryRuns(t, dir); got != 1 {
		t.Fatalf("calls of the first start = %d, want 1", got)
	}

	summarySetStdout(t, dir, summaryResult)
	second := startSummaryLoop(t, st, claude)
	if got := summaryRuns(t, dir); got != 2 {
		t.Fatalf("calls after the second start = %d, want 2", got)
	}
	if got := storedSummaryOf(t, st, summaryEpicA); got != "Fake summary." {
		t.Fatalf("summary after the second start = %q, want the fake's echo", got)
	}
	second.tick()
	if got := summaryRuns(t, dir); got != 2 {
		t.Fatalf("calls after a tick of the second start = %d, want still 2", got)
	}
}

// TestEpicSummaryCallsRunOneAtATime covers F5: the calls for two epics never overlap, so each run ends before the next
// one starts.
func TestEpicSummaryCallsRunOneAtATime(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	claude := summaryClaude(t, dir, summaryResult, 0)
	st := epicTestStore(t)
	createSummaryEpic(t, st, summaryEpicA, summaryStamp)
	createSummaryEpic(t, st, summaryEpicB, summaryStamp)

	startSummaryLoop(t, st, claude)
	if want := []string{"start", "end", "start", "end"}; !slices.Equal(summaryCalls(t, dir), want) {
		t.Fatalf("calls = %v, want %v", summaryCalls(t, dir), want)
	}
	for _, id := range []string{summaryEpicA, summaryEpicB} {
		if got := storedSummaryOf(t, st, id); got != "Fake summary." {
			t.Fatalf("summary of %s = %q, want the fake's echo", id, got)
		}
	}
}
