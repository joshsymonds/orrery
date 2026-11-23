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

// summaryOrderFakeScript is the fake claude the ordering test runs. A run appends start to dir/calls, prints dir/stdout,
// appends end to dir/calls and exits 0. So the calls file holds each run's start and end in the order the runs happened.
const summaryOrderFakeScript = `#!/bin/sh
dir='%s'
echo start >> "$dir/calls"
cat "$dir/stdout"
echo end >> "$dir/calls"
`

// summaryOrderClaude writes the start and end mark fake claude into dir, set to print summaryResult, and returns its
// path.
func summaryOrderClaude(t *testing.T, dir string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "stdout"), []byte(summaryResult), 0o600); err != nil {
		t.Fatalf("write fake stdout: %v", err)
	}
	path := filepath.Join(dir, "claude")
	exectest.WriteFile(t, path, fmt.Appendf(nil, summaryOrderFakeScript, dir), 0o700)
	return path
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
	summaries *EpicSummaries
	passes    chan struct{}
	done      chan struct{}
	cancel    context.CancelFunc
}

// startSummaryLoop starts an EpicSummaries over st whose Haiku runs the fake claude at claude, and returns once its
// start pass is done. The test's end stops it too.
func startSummaryLoop(t *testing.T, st *store.Store, claude string) *summaryLoop {
	t.Helper()
	passes := make(chan struct{}, 16)
	ctx, cancel := context.WithCancel(t.Context())
	summaries := NewEpicSummaries(EpicSummariesConfig{
		Store: st,
		Haiku: NewHaiku(
			HaikuConfig{Claude: claude, Environ: []string{"PATH=" + os.Getenv("PATH"), "HOME=" + haikuHome}},
		),
		NewTimer: unfiredTimer,
		Passed:   func() { passes <- struct{}{} },
	})
	loop := &summaryLoop{summaries: summaries, passes: passes, done: make(chan struct{}), cancel: cancel}
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

// wake has the loop summarize the epic with id, as a store hook would after it stored the epic, and waits until that
// pass is done.
func (l *summaryLoop) wake(id string) {
	l.summaries.Wake(id)
	<-l.passes
}

// TestEpicSummaryCallsHaikuWithSchemaAndStoresTheSummaryOnce covers R3: the call carries the summary schema and reads
// the contract of an epic created before the start, a valid summary is stored, and later wakes make no further call.
func TestEpicSummaryCallsHaikuWithSchemaAndStoresTheSummaryOnce(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	claude := haikuWriteFake(t, dir, haikuFake{stdout: summaryResult, status: 0})
	st := epicTestStore(t)
	createSummaryEpic(t, st, summaryEpicA, summaryStamp)

	loop := startSummaryLoop(t, st, claude)
	if got := haikuRuns(t, dir); got != 1 {
		t.Fatalf("calls after start = %d, want 1", got)
	}
	if argv := haikuReadFile(t, dir, "argv"); !strings.Contains(argv, "--json-schema\n"+epicSummarySchema+"\n") {
		t.Fatalf("argv = %q, want --json-schema followed by the summary schema", argv)
	}
	if stdin := haikuReadFile(t, dir, "stdin"); !strings.Contains(stdin, "Intent: Synthetic intent.") {
		t.Fatalf("stdin = %q, want the epic's contract", stdin)
	}
	if got := storedSummaryOf(t, st, summaryEpicA); got != "Fake summary." {
		t.Fatalf("summary after start = %q, want the fake's echo", got)
	}
	loop.wake(summaryEpicA)
	loop.wake(summaryEpicA)
	if got := haikuRuns(t, dir); got != 1 {
		t.Fatalf("calls after two wakes = %d, want still 1", got)
	}
}

// TestEpicSummaryFillsAnEpicCreatedAfterStartAtTheNextTick covers R3: a wake summarizes an epic stored after the start
// and calls for no epic that already has its summary.
func TestEpicSummaryFillsAnEpicCreatedAfterStartAtTheNextTick(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	claude := haikuWriteFake(t, dir, haikuFake{stdout: summaryResult, status: 0})
	st := epicTestStore(t)
	createSummaryEpic(t, st, summaryEpicA, summaryStamp)
	loop := startSummaryLoop(t, st, claude)

	createSummaryEpic(t, st, summaryEpicB, summaryStamp+60)
	loop.wake(summaryEpicB)
	if got := haikuRuns(t, dir); got != 2 {
		t.Fatalf("calls after the wake = %d, want 2: one for each epic", got)
	}
	if got := storedSummaryOf(t, st, summaryEpicB); got != "Fake summary." {
		t.Fatalf("summary of the epic created after start = %q, want the fake's echo", got)
	}
}

// TestEpicSummaryFailedOrInvalidCallLeavesTheSummaryEmptyUntilItsDeadline covers R4: a failed or invalid call leaves the
// summary empty, and a wake before the epic's deadline fires calls for it no more (see callRetries).
func TestEpicSummaryFailedOrInvalidCallLeavesTheSummaryEmptyUntilItsDeadline(t *testing.T) {
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
			claude := haikuWriteFake(t, dir, haikuFake{stdout: tc.stdout, status: tc.status})
			st := epicTestStore(t)
			createSummaryEpic(t, st, summaryEpicA, summaryStamp)

			loop := startSummaryLoop(t, st, claude)
			if got := haikuRuns(t, dir); got != 1 {
				t.Fatalf("calls after start = %d, want 1", got)
			}
			if got := storedSummaryOf(t, st, summaryEpicA); got != "" {
				t.Fatalf("summary after a failed call = %q, want empty", got)
			}
			loop.wake(summaryEpicA)
			if got := haikuRuns(t, dir); got != 1 {
				t.Fatalf("calls after a wake = %d, want still 1", got)
			}
		})
	}
}

// TestEpicSummarySecondStartRetriesAnEmptyEpicOnce covers R4: a start calls at once for an epic that a failed call left
// empty, and once that call succeeds a wake calls for it no more.
func TestEpicSummarySecondStartRetriesAnEmptyEpicOnce(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	claude := haikuWriteFake(t, dir, haikuFake{stdout: "not json", status: 0})
	st := epicTestStore(t)
	createSummaryEpic(t, st, summaryEpicA, summaryStamp)

	first := startSummaryLoop(t, st, claude)
	first.wake(summaryEpicA)
	first.stop()
	if got := haikuRuns(t, dir); got != 1 {
		t.Fatalf("calls of the first start = %d, want 1", got)
	}

	claude = haikuWriteFake(t, dir, haikuFake{stdout: summaryResult, status: 0})
	second := startSummaryLoop(t, st, claude)
	if got := haikuRuns(t, dir); got != 2 {
		t.Fatalf("calls after the second start = %d, want 2", got)
	}
	if got := storedSummaryOf(t, st, summaryEpicA); got != "Fake summary." {
		t.Fatalf("summary after the second start = %q, want the fake's echo", got)
	}
	second.wake(summaryEpicA)
	if got := haikuRuns(t, dir); got != 2 {
		t.Fatalf("calls after a wake of the second start = %d, want still 2", got)
	}
}

// TestEpicSummaryCallsRunOneAtATime covers F5: the calls for two epics never overlap, so each run ends before the next
// one starts.
func TestEpicSummaryCallsRunOneAtATime(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	claude := summaryOrderClaude(t, dir)
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
