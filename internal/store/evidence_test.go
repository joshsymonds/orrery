package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
)

// runEpic is the epic the evidence run tests store their runs under.
const runEpic = "orrery-1a2b3c4/epic-runs"

// runCreatedAt is the second the run tests add a run at.
func runCreatedAt() time.Time {
	return time.Unix(1700000100, 0)
}

// runCapturedAt is the second the run tests finish a capture at.
func runCapturedAt() time.Time {
	return time.Unix(1700000200, 0)
}

// runEndedAt is the second the run tests give a verdict at.
func runEndedAt() time.Time {
	return time.Unix(1700000300, 0)
}

// runItem is the evidence item of the runs these tests store: a capture with a limit and two artifacts.
func runItem() evidence.Item {
	return evidence.Item{
		Capture:    []string{"go", "test", "./internal/store/"},
		Limit:      600,
		Artifacts:  []string{"out/store.json", "out/log.txt"},
		Conclusion: "The store keeps runs",
		Judge:      "Read the test output",
	}
}

// newRun returns a red run of Requirement R1 at revision abc1234 in epic runEpic, created at created and not yet
// captured.
func newRun(id string, created time.Time) EvidenceRun {
	return EvidenceRun{
		ID: id, Epic: runEpic, Item: "R1", Revision: "abc1234", Phase: "red", Source: "captured",
		Spec: runItem(), Created: created,
	}
}

// runCapture is the capture of a red run: a failing exit, a log, and three artifacts in an order that is not
// alphabetical.
func runCapture() EvidenceCapture {
	return EvidenceCapture{
		Exit:     1,
		LogHash:  "sha256-log",
		LogBytes: 2048,
		Artifacts: []EvidenceArtifact{
			{Path: "out/store.json", State: "kept", Hash: "sha256-json", Blob: "blob-json", Size: 512},
			{Path: "out/log.txt", State: "over_cap", Size: 9000000},
			{Path: "out/trace.txt", State: "missing"},
		},
		At: runCapturedAt(),
	}
}

// laterCapture is a second capture that differs from runCapture in every field a write could change.
func laterCapture() EvidenceCapture {
	return EvidenceCapture{
		TimedOut:    true,
		NotCaptured: "the runner timed out",
		LogHash:     "sha256-later",
		LogBytes:    7,
		Artifacts: []EvidenceArtifact{
			{Path: "out/other.txt", State: "kept", Hash: "sha256-other", Blob: "blob", Size: 3},
		},
		At: time.Unix(1700000250, 0),
	}
}

// runVerdict is the judge's holding verdict on a run that captured.
func runVerdict() EvidenceVerdict {
	return EvidenceVerdict{
		Verdict: "holds", Reason: "The output shows the store keeps runs", Passed: true, At: runEndedAt(),
	}
}

// laterVerdict is a second verdict that differs from runVerdict in every field a write could change.
func laterVerdict() EvidenceVerdict {
	return EvidenceVerdict{
		Verdict: "fails", Reason: "A second judge disagrees", Passed: false, At: time.Unix(1700000400, 0),
	}
}

// captureRun, grantRun, judgeRun and verdictRun are the four guarded writes on a run, each with a valid argument
// that differs from the values runAt stores.
func captureRun(ctx context.Context, s *Store, id string) error {
	return s.RecordEvidenceCapture(ctx, id, laterCapture())
}

func grantRun(ctx context.Context, s *Store, id string) error {
	return s.SetEvidenceGrant(ctx, id, "grant-9", "holder-9")
}

func judgeRun(ctx context.Context, s *Store, id string) error {
	return s.SetEvidenceJudge(ctx, id, "judge-step-9")
}

func verdictRun(ctx context.Context, s *Store, id string) error {
	return s.RecordEvidenceVerdict(ctx, id, laterVerdict())
}

// runAt adds run id and moves it to state: capturing, judging after its capture, or ended after its verdict.
func runAt(t *testing.T, s *Store, id, state string) {
	t.Helper()
	ctx := context.Background()
	if err := s.AddEvidenceRun(ctx, newRun(id, runCreatedAt())); err != nil {
		t.Fatalf("AddEvidenceRun %s: %v", id, err)
	}
	switch state {
	case runCapturing:
		return
	case runJudging, runEnded:
	default:
		t.Fatalf("runAt: unknown state %q", state)
	}
	if err := s.RecordEvidenceCapture(ctx, id, runCapture()); err != nil {
		t.Fatalf("RecordEvidenceCapture %s: %v", id, err)
	}
	if state == runJudging {
		return
	}
	if err := s.RecordEvidenceVerdict(ctx, id, runVerdict()); err != nil {
		t.Fatalf("RecordEvidenceVerdict %s: %v", id, err)
	}
}

// assertRun reads run id back and fails unless it equals want.
func assertRun(t *testing.T, s *Store, id string, want EvidenceRun, step string) {
	t.Helper()
	got, err := s.EvidenceRun(context.Background(), id)
	if err != nil {
		t.Fatalf("EvidenceRun %s: %v", step, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("EvidenceRun %s = %+v, want %+v", step, got, want)
	}
}

// evidenceRowsOf returns the rows of the two evidence tables, by table name.
func evidenceRowsOf(t *testing.T, s *Store) map[string][][]any {
	t.Helper()
	return map[string][][]any{
		"evidence_runs":      rowsOf(t, s.db, "evidence_runs"),
		"evidence_artifacts": rowsOf(t, s.db, "evidence_artifacts"),
	}
}

// runIDs returns the ids of runs in order.
func runIDs(runs []EvidenceRun) []string {
	ids := make([]string, 0, len(runs))
	for _, run := range runs {
		ids = append(ids, run.ID)
	}
	return ids
}

func TestEvidenceRunReadsBackAtEachStep(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = "run-r1-red"
	if err := s.AddEvidenceRun(ctx, newRun(id, runCreatedAt())); err != nil {
		t.Fatalf("AddEvidenceRun: %v", err)
	}
	want := EvidenceRun{
		ID: id, Epic: runEpic, Item: "R1", Revision: "abc1234", Phase: "red", Source: "captured",
		State: "capturing", Spec: runItem(), Created: runCreatedAt(),
	}
	assertRun(t, s, id, want, "after AddEvidenceRun")

	if err := s.SetEvidenceGrant(ctx, id, "grant-7", "holder-2"); err != nil {
		t.Fatalf("SetEvidenceGrant: %v", err)
	}
	want.Grant, want.Holder = "grant-7", "holder-2"
	assertRun(t, s, id, want, "after SetEvidenceGrant")

	capture := runCapture()
	if err := s.RecordEvidenceCapture(ctx, id, capture); err != nil {
		t.Fatalf("RecordEvidenceCapture: %v", err)
	}
	want.State = "judging"
	want.Exit, want.LogHash, want.LogBytes = 1, "sha256-log", 2048
	want.Artifacts = capture.Artifacts
	assertRun(t, s, id, want, "after RecordEvidenceCapture")

	if err := s.SetEvidenceJudge(ctx, id, "judge-step-1"); err != nil {
		t.Fatalf("SetEvidenceJudge: %v", err)
	}
	want.JudgeStep, want.Judges = "judge-step-1", 1
	assertRun(t, s, id, want, "after SetEvidenceJudge")

	if err := s.RecordEvidenceVerdict(ctx, id, runVerdict()); err != nil {
		t.Fatalf("RecordEvidenceVerdict: %v", err)
	}
	want.State = "ended"
	want.Verdict, want.JudgeReason, want.Passed = "holds", "The output shows the store keeps runs", true
	want.Ended = runEndedAt()
	assertRun(t, s, id, want, "after RecordEvidenceVerdict")
}

func TestNotCapturedCaptureEndsRunWithoutJudge(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = "run-no-capture"
	if err := s.AddEvidenceRun(ctx, newRun(id, runCreatedAt())); err != nil {
		t.Fatalf("AddEvidenceRun: %v", err)
	}
	if err := s.RecordEvidenceCapture(ctx, id, EvidenceCapture{
		NotCaptured: "the runner has no display", At: runCapturedAt(),
	}); err != nil {
		t.Fatalf("RecordEvidenceCapture: %v", err)
	}

	want := EvidenceRun{
		ID: id, Epic: runEpic, Item: "R1", Revision: "abc1234", Phase: "red", Source: "captured",
		State: "ended", Spec: runItem(), Created: runCreatedAt(), NotCaptured: "the runner has no display",
		Ended: runCapturedAt(),
	}
	assertRun(t, s, id, want, "after a not-captured capture")

	if err := s.SetEvidenceJudge(ctx, id, "judge-step-1"); !errors.Is(err, ErrEvidenceState) {
		t.Errorf("SetEvidenceJudge after a not-captured capture err = %v, want ErrEvidenceState", err)
	}
	if err := s.RecordEvidenceVerdict(ctx, id, runVerdict()); !errors.Is(err, ErrEvidenceState) {
		t.Errorf("RecordEvidenceVerdict after a not-captured capture err = %v, want ErrEvidenceState", err)
	}
	assertRun(t, s, id, want, "after the refused judge and verdict")
}

func TestEvidenceWritesRefusedInWrongStateChangeNothing(t *testing.T) {
	t.Parallel()
	refused := []struct {
		name  string
		state string
		write func(ctx context.Context, s *Store, id string) error
	}{
		{"second capture record", runJudging, captureRun},
		{"capture after verdict", runEnded, captureRun},
		{"grant after capture", runJudging, grantRun},
		{"grant after verdict", runEnded, grantRun},
		{"judge before capture", runCapturing, judgeRun},
		{"judge after verdict", runEnded, judgeRun},
		{"verdict before capture", runCapturing, verdictRun},
		{"second verdict", runEnded, verdictRun},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := openEpicStore(t)
			const id = "run-refused"
			runAt(t, s, id, tc.state)
			before := evidenceRowsOf(t, s)

			err := tc.write(context.Background(), s, id)
			if !errors.Is(err, ErrEvidenceState) {
				t.Fatalf("%s err = %v, want ErrEvidenceState", tc.name, err)
			}
			if after := evidenceRowsOf(t, s); !reflect.DeepEqual(after, before) {
				t.Errorf("%s changed the store: rows = %v, want %v", tc.name, after, before)
			}
		})
	}
}

func TestEvidenceWritesToUnknownRunAreErrNoEvidenceRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = "run-missing"
	writes := map[string]func(ctx context.Context, s *Store, id string) error{
		"capture": captureRun, "grant": grantRun, "judge": judgeRun, "verdict": verdictRun,
	}
	for name, write := range writes {
		if err := write(ctx, s, id); !errors.Is(err, ErrNoEvidenceRun) {
			t.Errorf("%s of unknown run err = %v, want ErrNoEvidenceRun", name, err)
		}
	}
	if _, err := s.EvidenceRun(ctx, id); !errors.Is(err, ErrNoEvidenceRun) {
		t.Errorf("EvidenceRun of unknown run err = %v, want ErrNoEvidenceRun", err)
	}
	if got := evidenceRowsOf(t, s); len(got["evidence_runs"]) != 0 || len(got["evidence_artifacts"]) != 0 {
		t.Errorf("evidence rows after refused writes = %v, want none", got)
	}
}

func TestAddEvidenceRunRefusesSecondAddOfSameID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = "run-once"
	if err := s.AddEvidenceRun(ctx, newRun(id, runCreatedAt())); err != nil {
		t.Fatalf("AddEvidenceRun: %v", err)
	}
	before := evidenceRowsOf(t, s)

	second := newRun(id, runEndedAt())
	second.Item = "R9"
	if err := s.AddEvidenceRun(ctx, second); err == nil {
		t.Fatalf("second AddEvidenceRun of %s: err = nil, want the duplicate id refused", id)
	}
	if after := evidenceRowsOf(t, s); !reflect.DeepEqual(after, before) {
		t.Errorf("second AddEvidenceRun changed the store: rows = %v, want %v", after, before)
	}
}

func TestSetEvidenceJudgeTwiceKeepsSecondStep(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = "run-rejudged"
	runAt(t, s, id, runJudging)

	if err := s.SetEvidenceJudge(ctx, id, "judge-step-1"); err != nil {
		t.Fatalf("first SetEvidenceJudge: %v", err)
	}
	if err := s.SetEvidenceJudge(ctx, id, "judge-step-2"); err != nil {
		t.Fatalf("second SetEvidenceJudge: %v", err)
	}

	got, err := s.EvidenceRun(ctx, id)
	if err != nil {
		t.Fatalf("EvidenceRun: %v", err)
	}
	if got.JudgeStep != "judge-step-2" || got.Judges != 2 {
		t.Errorf("judge after two SetEvidenceJudge = step %q, judges %d; want step %q, judges 2",
			got.JudgeStep, got.Judges, "judge-step-2")
	}
	if got.State != runJudging {
		t.Errorf("state after SetEvidenceJudge = %q, want %q", got.State, runJudging)
	}
}

func TestEvidenceRunsOrderedByCreatedThenID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	added := []struct {
		id      string
		epic    string
		created time.Time
	}{
		{"run-late", runEpic, time.Unix(1700000300, 0)},
		{"run-tied-b", runEpic, time.Unix(1700000200, 0)},
		{"run-early", runEpic, time.Unix(1700000100, 0)},
		{"run-tied-a", runEpic, time.Unix(1700000200, 0)},
		{"run-other-epic", "orrery-9/other", time.Unix(1700000050, 0)},
	}
	for _, a := range added {
		run := newRun(a.id, a.created)
		run.Epic = a.epic
		if err := s.AddEvidenceRun(ctx, run); err != nil {
			t.Fatalf("AddEvidenceRun %s: %v", a.id, err)
		}
	}

	runs, err := s.EvidenceRuns(ctx, runEpic)
	if err != nil {
		t.Fatalf("EvidenceRuns: %v", err)
	}
	want := []string{"run-early", "run-tied-a", "run-tied-b", "run-late"}
	if got := runIDs(runs); !reflect.DeepEqual(got, want) {
		t.Errorf("EvidenceRuns order = %v, want %v", got, want)
	}
}

func TestOpenEvidenceRunsListsCapturingAndJudgingOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	states := []struct {
		id    string
		state string
	}{
		{"run-a-capturing", runCapturing},
		{"run-b-judging", runJudging},
		{"run-c-ended", runEnded},
	}
	for i, st := range states {
		run := newRun(st.id, time.Unix(1700000100+int64(i)*100, 0))
		if err := s.AddEvidenceRun(ctx, run); err != nil {
			t.Fatalf("AddEvidenceRun %s: %v", st.id, err)
		}
		if st.state == runCapturing {
			continue
		}
		if err := s.RecordEvidenceCapture(ctx, st.id, runCapture()); err != nil {
			t.Fatalf("RecordEvidenceCapture %s: %v", st.id, err)
		}
		if st.state == runEnded {
			if err := s.RecordEvidenceVerdict(ctx, st.id, runVerdict()); err != nil {
				t.Fatalf("RecordEvidenceVerdict %s: %v", st.id, err)
			}
		}
	}

	open, err := s.OpenEvidenceRuns(ctx)
	if err != nil {
		t.Fatalf("OpenEvidenceRuns: %v", err)
	}
	want := []string{"run-a-capturing", "run-b-judging"}
	if got := runIDs(open); !reflect.DeepEqual(got, want) {
		t.Errorf("OpenEvidenceRuns = %v, want %v", got, want)
	}
}
