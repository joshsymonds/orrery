package enginehost

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// The ids of the epics the report tests store.
const (
	reportEpicA = "orrery-2b3c4d5/epic-a"
	reportEpicB = "orrery-2b3c4d5/epic-b"
)

// reportStamp is the unix time the report tests create their epics at.
const reportStamp = 1794800000

// reportResult is the result line the fake claude prints for a valid report. Its why is the fake's own text, and no test
// reads it.
const reportResult = `{"type":"result","subtype":"success","is_error":false,"structured_output":{"why":"Fake why."}}`

// reportItem returns the captured item the report tests' Requirements hold and their evidence runs carry.
func reportItem() evidence.Item {
	return evidence.Item{
		Capture: []string{"/bin/sh", "capture.sh"}, Limit: 60, Conclusion: "the build passes", Judge: "reviewer",
	}
}

// createReportEpic stores an epic with id in phase, created at reportStamp. It has three Requirements, R1, R2 and R3,
// each holding reportItem.
func createReportEpic(t *testing.T, st *store.Store, id, phase string) {
	t.Helper()
	at := time.Unix(reportStamp, 0)
	if err := st.CreateEpic(t.Context(), store.EpicRow{
		ID: id, Repository: "/work/synthetic-repo", Title: "Synthetic epic", Intent: "Synthetic intent.",
		Care: "serious", Approach: "Synthetic approach.", Phase: phase, Created: at, Changed: at,
		Requirements: []store.EpicRequirement{
			{
				ID: "R1", Text: "Synthetic requirement one.", Evidence: "Synthetic evidence.", Kind: "test",
				Item: reportItem(),
			},
			{
				ID: "R2", Text: "Synthetic requirement two.", Evidence: "Synthetic evidence.", Kind: "test",
				Item: reportItem(),
			},
			{
				ID: "R3", Text: "Synthetic requirement three.", Evidence: "Synthetic evidence.", Kind: "test",
				Item: reportItem(),
			},
		},
	}); err != nil {
		t.Fatalf("CreateEpic %s: %v", id, err)
	}
}

// recordReportRun stores an evidence run with id of requirement in epic, in phase, captured with exit and judged with
// verdict. Its spec is reportItem, so the run is current, and it passes when evidence.Passed says so.
func recordReportRun(t *testing.T, st *store.Store, epic, id, requirement, phase string, exit int, verdict string) {
	t.Helper()
	ctx := t.Context()
	at := time.Unix(reportStamp, 0)
	if err := st.AddEvidenceRun(ctx, store.EvidenceRun{
		ID: id, Epic: epic, Item: requirement, Revision: "synthetic-revision", Phase: phase,
		Source: evidence.SourceCaptured, Spec: reportItem(), Created: at,
	}); err != nil {
		t.Fatalf("AddEvidenceRun %s: %v", id, err)
	}
	if err := st.RecordEvidenceCapture(ctx, id, store.EvidenceCapture{Exit: exit, LogBytes: 12, At: at}); err != nil {
		t.Fatalf("RecordEvidenceCapture %s: %v", id, err)
	}
	if err := st.SetEvidenceJudge(ctx, id, "synthetic-judge"); err != nil {
		t.Fatalf("SetEvidenceJudge %s: %v", id, err)
	}
	if err := st.RecordEvidenceVerdict(ctx, id, store.EvidenceVerdict{
		Verdict: verdict, Reason: "Synthetic reason.", Passed: evidence.Passed(phase, "", exit, verdict), At: at,
	}); err != nil {
		t.Fatalf("RecordEvidenceVerdict %s: %v", id, err)
	}
}

// storedReportOf returns the report of the epic id as the store reads it.
func storedReportOf(t *testing.T, st *store.Store, id string) store.EpicReport {
	t.Helper()
	row, err := st.Epic(t.Context(), id)
	if err != nil {
		t.Fatalf("Epic %s: %v", id, err)
	}
	return row.Report
}

// reportLoop is an EpicReports started over a store, and the channels a test drives it with.
type reportLoop struct {
	ticks  chan time.Time
	passes chan struct{}
	done   chan struct{}
	cancel context.CancelFunc
}

// startReportLoop starts an EpicReports over st whose Haiku runs the fake claude at claude, and returns once its start
// pass is done. The test's end stops it too.
func startReportLoop(t *testing.T, st *store.Store, claude string) *reportLoop {
	t.Helper()
	loop := runReportLoop(t, st, NewHaiku(HaikuConfig{Claude: claude, Environ: []string{"PATH=" + os.Getenv("PATH")}}))
	t.Cleanup(loop.stop)
	<-loop.passes
	return loop
}

// runReportLoop starts an EpicReports over st whose Haiku is haiku, and returns without waiting for its start pass. The
// caller stops it.
func runReportLoop(t *testing.T, st *store.Store, haiku haikuCaller) *reportLoop {
	t.Helper()
	ticks := make(chan time.Time)
	passes := make(chan struct{}, 16)
	ctx, cancel := context.WithCancel(t.Context())
	loop := &reportLoop{ticks: ticks, passes: passes, done: make(chan struct{}), cancel: cancel}
	reports := NewEpicReports(EpicReportsConfig{
		Store:  st,
		Haiku:  haiku,
		Tick:   ticks,
		Passed: func() { passes <- struct{}{} },
	})
	go func() {
		defer close(loop.done)
		reports.Run(ctx)
	}()
	return loop
}

// stop ends the loop and waits for Run to return. It is safe to call more than once.
func (l *reportLoop) stop() {
	l.cancel()
	<-l.done
}

// tick has the loop pass once more and waits until that pass is done.
func (l *reportLoop) tick() {
	l.ticks <- time.Time{}
	<-l.passes
}

// TestEpicReportWritesOneReportOfAnEndedEpic covers R3: an ended epic gets one report whose shipped and not-shipped ids
// are the Requirements its stored evidence verdicts say shipped and did not. The why comes from the call, and later
// ticks make no further call.
func TestEpicReportWritesOneReportOfAnEndedEpic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	claude := haikuWriteFake(t, dir, haikuFake{stdout: reportResult, status: 0})
	st := epicTestStore(t)
	createReportEpic(t, st, reportEpicA, "released")
	recordReportRun(t, st, reportEpicA, "run-r1", "R1", "green", 0, "holds")
	recordReportRun(t, st, reportEpicA, "run-r2", "R2", "green", 0, "fails")
	recordReportRun(t, st, reportEpicA, "run-r3", "R3", "red", 0, "fails")

	loop := startReportLoop(t, st, claude)
	if got := haikuRuns(t, dir); got != 1 {
		t.Fatalf("calls after start = %d, want 1", got)
	}
	if argv := haikuReadFile(t, dir, "argv"); !strings.Contains(argv, "--json-schema\n"+epicReportSchema+"\n") {
		t.Fatalf("argv = %q, want --json-schema followed by the report schema", argv)
	}
	if stdin := haikuReadFile(t, dir, "stdin"); !strings.Contains(stdin, "Synthetic requirement one.") {
		t.Fatalf("stdin = %q, want the text of the Requirement that shipped", stdin)
	}
	report := storedReportOf(t, st, reportEpicA)
	if report.Phase != "released" {
		t.Fatalf("report phase = %q, want released", report.Phase)
	}
	if !slices.Equal(report.Shipped, []string{"R1"}) {
		t.Fatalf("shipped = %v, want [R1]", report.Shipped)
	}
	if !slices.Equal(report.NotShipped, []string{"R2", "R3"}) {
		t.Fatalf("not shipped = %v, want [R2 R3]", report.NotShipped)
	}
	if report.Why == "" {
		t.Fatal("why after a valid call is empty")
	}
	loop.tick()
	loop.tick()
	if got := haikuRuns(t, dir); got != 1 {
		t.Fatalf("calls after two ticks = %d, want still 1", got)
	}
}

// TestEpicReportFailedOrBlankCallLeavesTheReportEmptyForTheStart covers R3: a failed call or a blank why stores no
// report, and no later tick of the same start calls for the epic again.
func TestEpicReportFailedOrBlankCallLeavesTheReportEmptyForTheStart(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		stdout string
		status int
	}{
		{name: "claude exits nonzero", stdout: "", status: 3},
		{
			name:   "why is blank",
			stdout: `{"type":"result","subtype":"success","is_error":false,"structured_output":{"why":" \n "}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			claude := haikuWriteFake(t, dir, haikuFake{stdout: tc.stdout, status: tc.status})
			st := epicTestStore(t)
			createReportEpic(t, st, reportEpicA, "released")

			loop := startReportLoop(t, st, claude)
			if got := haikuRuns(t, dir); got != 1 {
				t.Fatalf("calls after start = %d, want 1", got)
			}
			report := storedReportOf(t, st, reportEpicA)
			if report.Phase != "" || report.Why != "" || len(report.Shipped) > 0 || len(report.NotShipped) > 0 {
				t.Fatalf("report after a failed call = %+v, want empty", report)
			}
			loop.tick()
			if got := haikuRuns(t, dir); got != 1 {
				t.Fatalf("calls after a tick = %d, want still 1", got)
			}
		})
	}
}

// TestEpicReportSecondStartRetriesAnUnreportedEpicOnce covers R3: a start retries an epic whose failed call left it
// unreported, and a tick of that start calls for it no more.
func TestEpicReportSecondStartRetriesAnUnreportedEpicOnce(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	claude := haikuWriteFake(t, dir, haikuFake{stdout: "not json", status: 0})
	st := epicTestStore(t)
	createReportEpic(t, st, reportEpicA, "released")

	first := startReportLoop(t, st, claude)
	first.tick()
	first.stop()
	if got := haikuRuns(t, dir); got != 1 {
		t.Fatalf("calls of the first start = %d, want 1", got)
	}

	claude = haikuWriteFake(t, dir, haikuFake{stdout: reportResult, status: 0})
	second := startReportLoop(t, st, claude)
	if got := haikuRuns(t, dir); got != 2 {
		t.Fatalf("calls after the second start = %d, want 2", got)
	}
	if got := storedReportOf(t, st, reportEpicA).Phase; got != "released" {
		t.Fatalf("report phase after the second start = %q, want released", got)
	}
	second.tick()
	if got := haikuRuns(t, dir); got != 2 {
		t.Fatalf("calls after a tick of the second start = %d, want still 2", got)
	}
}

// TestEpicReportSecondEndingInOneStartGetsItsOwnReport covers R3's one report per ending: an epic reported when it ended
// with gaps and released later in the same start gets a second call, and its report is for the release.
func TestEpicReportSecondEndingInOneStartGetsItsOwnReport(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	claude := haikuWriteFake(t, dir, haikuFake{stdout: reportResult, status: 0})
	st := epicTestStore(t)
	createReportEpic(t, st, reportEpicA, "ended_with_gaps")

	loop := startReportLoop(t, st, claude)
	if got := storedReportOf(t, st, reportEpicA).Phase; got != "ended_with_gaps" {
		t.Fatalf("report phase after start = %q, want ended_with_gaps", got)
	}
	if err := st.EndEpic(t.Context(), reportEpicA, "released", "Released. No gaps.", nil,
		time.Unix(reportStamp+60, 0)); err != nil {
		t.Fatalf("EndEpic released: %v", err)
	}
	loop.tick()
	if got := haikuRuns(t, dir); got != 2 {
		t.Fatalf("calls after the second ending = %d, want 2", got)
	}
	if got := storedReportOf(t, st, reportEpicA).Phase; got != "released" {
		t.Fatalf("report phase after the second ending = %q, want released", got)
	}
}

// reportOverlapHaiku is an in-process haikuCaller for the one-at-a-time test. Its first call is held open until release
// closes, and it records whether a call began while another was still open.
type reportOverlapHaiku struct {
	release    chan struct{}
	mu         sync.Mutex
	calls      int
	open       int
	overlapped bool
}

// Call holds the first call open until release closes and returns the why of a valid report.
func (h *reportOverlapHaiku) Call(_ context.Context, _, _ string) (json.RawMessage, error) {
	h.mu.Lock()
	h.calls++
	first := h.calls == 1
	if h.open > 0 {
		h.overlapped = true
	}
	h.open++
	h.mu.Unlock()
	if first {
		<-h.release
	}
	h.mu.Lock()
	h.open--
	h.mu.Unlock()
	return json.RawMessage(`{"why":"Fake why."}`), nil
}

// sawOverlap reports whether a call began while another was still open.
func (h *reportOverlapHaiku) sawOverlap() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.overlapped
}

// TestEpicReportCallsRunOneAtATime covers R3: the calls for two ended epics never overlap, so each call ends before the
// next one begins, and each epic gets its report. The first call is held open. synctest.Wait returns once every goroutine
// of the loop is blocked, so a second call that began meanwhile is seen before the test releases the first.
func TestEpicReportCallsRunOneAtATime(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	createReportEpic(t, st, reportEpicA, "released")
	createReportEpic(t, st, reportEpicB, "ended_with_gaps")

	synctest.Test(t, func(t *testing.T) {
		haiku := &reportOverlapHaiku{release: make(chan struct{})}
		releaseOnce := sync.OnceFunc(func() { close(haiku.release) })
		loop := runReportLoop(t, st, haiku)
		defer loop.stop()
		defer releaseOnce()

		synctest.Wait()
		if haiku.sawOverlap() {
			t.Fatal("a report call began while the first call was still open")
		}
		releaseOnce()
		<-loop.passes
		for id, phase := range map[string]string{reportEpicA: "released", reportEpicB: "ended_with_gaps"} {
			if got := storedReportOf(t, st, id).Phase; got != phase {
				t.Fatalf("report phase of %s = %q, want %q", id, got, phase)
			}
		}
	})
}

// TestEpicReportLiveEpicGetsNoCall covers R3: an epic that has not ended gets no report and no call.
func TestEpicReportLiveEpicGetsNoCall(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	claude := haikuWriteFake(t, dir, haikuFake{stdout: reportResult, status: 0})
	st := epicTestStore(t)
	createReportEpic(t, st, reportEpicA, "accepted")

	loop := startReportLoop(t, st, claude)
	loop.tick()
	if got := haikuRuns(t, dir); got != 0 {
		t.Fatalf("calls for a live epic = %d, want 0", got)
	}
	if got := storedReportOf(t, st, reportEpicA).Phase; got != "" {
		t.Fatalf("report phase of a live epic = %q, want none", got)
	}
}
