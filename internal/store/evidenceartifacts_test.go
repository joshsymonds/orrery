package store

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// seedArtifactRuns stores count runs of runEpic: every third is still waiting and has no artifacts, every third is
// judging with runCapture's three artifacts, and the rest ended after laterCapture's one artifact. It also stores a
// run of another epic with artifacts, which no read of runEpic may take.
func seedArtifactRuns(t *testing.T, s *Store, count int) []string {
	t.Helper()
	ctx := context.Background()
	ids := make([]string, 0, count)
	for i := range count {
		id := fmt.Sprintf("run-%02d", i)
		ids = append(ids, id)
		switch i % 3 {
		case 0:
			runAt(t, s, id, RunWaiting)
		case 1:
			runAt(t, s, id, runJudging)
		default:
			if err := s.AddEvidenceRun(ctx, newRun(id, runCreatedAt())); err != nil {
				t.Fatalf("AddEvidenceRun %s: %v", id, err)
			}
			if err := s.RecordEvidenceCapture(ctx, id, laterCapture()); err != nil {
				t.Fatalf("RecordEvidenceCapture %s: %v", id, err)
			}
		}
	}
	other := newRun("other-run", runCreatedAt())
	other.Epic = "orrery-1a2b3c4/other-epic"
	if err := s.AddEvidenceRun(ctx, other); err != nil {
		t.Fatalf("AddEvidenceRun other: %v", err)
	}
	if err := s.RecordEvidenceCapture(ctx, other.ID, runCapture()); err != nil {
		t.Fatalf("RecordEvidenceCapture other: %v", err)
	}
	return ids
}

// TestEvidenceRunsReadEveryRunsArtifactsInOneQuery reads the runs of an epic with nine runs and proves the read
// issues two queries, the runs and then the artifacts of all of them, whatever the number of runs. Each run read so
// equals the run read alone, which reads its own artifacts, so each run keeps its own artifacts in their order.
func TestEvidenceRunsReadEveryRunsArtifactsInOneQuery(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)
	ids := seedArtifactRuns(t, s, 9)

	recorder := &planRecorder{db: s.db.DB}
	runs, err := readEvidenceRuns(ctx, sqlcgen.New(recorder), runEpic)
	if err != nil {
		t.Fatalf("readEvidenceRuns: %v", err)
	}
	want := []string{"ListEvidenceRuns", "ListEpicEvidenceArtifacts"}
	if got := recordedQueryNames(recorder); !reflect.DeepEqual(got, want) {
		t.Fatalf("queries = %v, want %v", got, want)
	}
	if got := runIDs(runs); !reflect.DeepEqual(got, ids) {
		t.Fatalf("runs = %v, want %v", got, ids)
	}
	listed, err := s.EvidenceRuns(ctx, runEpic)
	if err != nil {
		t.Fatalf("EvidenceRuns: %v", err)
	}
	if !reflect.DeepEqual(listed, runs) {
		t.Errorf("EvidenceRuns = %+v, want the runs readEvidenceRuns read, %+v", listed, runs)
	}
	for _, run := range runs {
		alone, err := s.EvidenceRun(ctx, run.ID)
		if err != nil {
			t.Fatalf("EvidenceRun %s: %v", run.ID, err)
		}
		if !reflect.DeepEqual(run, alone) {
			t.Errorf("run %s listed = %+v, want it as read alone, %+v", run.ID, run, alone)
		}
	}
	if got := len(runs[1].Artifacts); got != 3 {
		t.Errorf("judging run %s has %d artifacts, want runCapture's 3", runs[1].ID, got)
	}
}

// TestOpenEvidenceRunsReadEveryRunsArtifactsInOneQuery checks that the open runs, waiting and judging, of every epic
// come back each with its own artifacts, as each reads alone.
func TestOpenEvidenceRunsReadEveryRunsArtifactsInOneQuery(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)
	seedArtifactRuns(t, s, 6)

	open, err := s.OpenEvidenceRuns(ctx)
	if err != nil {
		t.Fatalf("OpenEvidenceRuns: %v", err)
	}
	want := []string{"other-run", "run-00", "run-01", "run-03", "run-04"}
	if got := runIDs(open); !reflect.DeepEqual(got, want) {
		t.Fatalf("open runs = %v, want %v", got, want)
	}
	for _, run := range open {
		alone, err := s.EvidenceRun(ctx, run.ID)
		if err != nil {
			t.Fatalf("EvidenceRun %s: %v", run.ID, err)
		}
		if !reflect.DeepEqual(run, alone) {
			t.Errorf("open run %s = %+v, want it as read alone, %+v", run.ID, run, alone)
		}
	}
}
