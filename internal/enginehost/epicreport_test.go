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

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/exectest"
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

// reportOrderFakeScript is the fake claude the ordering test runs. A run appends start to dir/calls, prints dir/stdout,
// appends end to dir/calls and exits 0. So the calls file holds each run's start and end in the order the runs happened.
const reportOrderFakeScript = `#!/bin/sh
dir='%s'
echo start >> "$dir/calls"
cat "$dir/stdout"
echo end >> "$dir/calls"
`

// reportOrderClaude writes the start and end mark fake claude into dir, set to print reportResult, and returns its path.
func reportOrderClaude(t *testing.T, dir string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "stdout"), []byte(reportResult), 0o600); err != nil {
		t.Fatalf("write fake stdout: %v", err)
	}
	path := filepath.Join(dir, "claude")
	exectest.WriteFile(t, path, fmt.Appendf(nil, reportOrderFakeScript, dir), 0o700)
	return path
}

// reportCalls returns the start and end marks the fake claude in dir has written, in order.
func reportCalls(t *testing.T, dir string) []string {
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
	ticks := make(chan time.Time)
	passes := make(chan struct{}, 16)
	ctx, cancel := context.WithCancel(t.Context())
	loop := &reportLoop{ticks: ticks, passes: passes, done: make(chan struct{}), cancel: cancel}
	reports := NewEpicReports(EpicReportsConfig{
		Store:  st,
		Haiku:  NewHaiku(HaikuConfig{Claude: claude, Environ: []string{"PATH=" + os.Getenv("PATH")}}),
		Tick:   ticks,
		Passed: func() { passes <- struct{}{} },
	})
	go func() {
		defer close(loop.done)
		reports.Run(ctx)
	}()
	t.Cleanup(loop.stop)
	<-passes
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

// TestEpicReportCallsRunOneAtATime covers R3: the calls for two ended epics never overlap, so each run ends before the
// next one starts, and each epic gets its report.
func TestEpicReportCallsRunOneAtATime(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	claude := reportOrderClaude(t, dir)
	st := epicTestStore(t)
	createReportEpic(t, st, reportEpicA, "released")
	createReportEpic(t, st, reportEpicB, "ended_with_gaps")

	startReportLoop(t, st, claude)
	if want := []string{"start", "end", "start", "end"}; !slices.Equal(reportCalls(t, dir), want) {
		t.Fatalf("calls = %v, want %v", reportCalls(t, dir), want)
	}
	for id, phase := range map[string]string{reportEpicA: "released", reportEpicB: "ended_with_gaps"} {
		if got := storedReportOf(t, st, id).Phase; got != phase {
			t.Fatalf("report phase of %s = %q, want %q", id, got, phase)
		}
	}
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
