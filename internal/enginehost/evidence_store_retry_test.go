package enginehost

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// evidenceRefusingStore refuses the first write of a run's capture, of its judge and of its verdict with errEndRefused,
// and passes every other write through. It reports the name of each write it refuses on refused.
type evidenceRefusingStore struct {
	EvidenceStore
	refused chan string

	mu   sync.Mutex
	seen map[string]bool
}

// refuse reports whether the write named name is refused: its first try is.
func (s *evidenceRefusingStore) refuse(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen[name] {
		return false
	}
	s.seen[name] = true
	s.refused <- name
	return true
}

// RecordEvidenceCapture refuses its first try and passes the rest through.
func (s *evidenceRefusingStore) RecordEvidenceCapture(
	ctx context.Context, id string, capture store.EvidenceCapture,
) error {
	if s.refuse("capture") {
		return errEndRefused
	}
	return s.EvidenceStore.RecordEvidenceCapture(ctx, id, capture)
}

// SetEvidenceJudge refuses its first try and passes the rest through.
func (s *evidenceRefusingStore) SetEvidenceJudge(ctx context.Context, id, step string) error {
	if s.refuse("judge") {
		return errEndRefused
	}
	return s.EvidenceStore.SetEvidenceJudge(ctx, id, step)
}

// RecordEvidenceVerdict refuses its first try and passes the rest through.
func (s *evidenceRefusingStore) RecordEvidenceVerdict(
	ctx context.Context, id string, verdict store.EvidenceVerdict,
) error {
	if s.refuse("verdict") {
		return errEndRefused
	}
	return s.EvidenceStore.RecordEvidenceVerdict(ctx, id, verdict)
}

// TestEvidenceRetriesEachRefusedWriteOfARun drives a supplied run whose capture, judge and verdict writes the store each
// refuses once. Each refused write waits storeRetry and is tried again, the run stays where the store has it until the
// write lands, and the drive ends with the run judged once and its verdict stored.
func TestEvidenceRetriesEachRefusedWriteOfARun(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	head := stepGitTestCommit(t, repo, map[string]string{"out.txt": "kept\n"})
	st := epicTestStore(t)
	item := evidence.Item{Supplied: []string{"out.txt"}, Conclusion: "the build passes", Judge: "reviewer"}
	evidenceRunSeed(t, st, repo, item, item)
	evidenceReadoptAdd(t, st, evidenceRecordRun, head, evidence.PhaseGreen, item)
	refusing := &evidenceRefusingStore{EvidenceStore: st, refused: make(chan string, 4), seen: map[string]bool{}}
	judges := &evidenceRunSteps{judges: []evidenceRunJudge{evidenceRunHolds()}}
	ev := evidenceRecordNew(t, refusing, judges, t.TempDir())
	clock := endRetryClock{waits: make(chan time.Duration, 4), tick: make(chan time.Time)}
	ev.after = clock.after

	done := make(chan struct{})
	go func() {
		defer close(done)
		ev.drive(t.Context(), repo, evidenceRecordStored(t, st))
	}()
	for _, write := range []string{"capture", "judge", "verdict"} {
		if got := <-refusing.refused; got != write {
			t.Fatalf("refused write = %q, want %q", got, write)
		}
		if d := <-clock.waits; d != storeRetry {
			t.Fatalf("wait before the %s write's next try = %v, want %v", write, d, storeRetry)
		}
		if write == "capture" {
			if got := evidenceRecordStored(t, st); got.State == "judging" || got.LogHash != "" {
				t.Fatalf(
					"run while its capture is refused = state %q log %q, want it uncaptured",
					got.State,
					got.LogHash,
				)
			}
		}
		clock.tick <- time.Time{}
	}
	<-done

	got := evidenceRecordStored(t, st)
	if got.Verdict != evidence.VerdictHolds || !got.Passed || got.Judges != 1 || judges.startCount() != 1 ||
		!evidenceRecordKeptOut(got) {
		t.Fatalf(
			"run after drive = verdict %q passed %t judges %d starts %d artifacts %+v, "+
				"want holds, passed, one judge, one start and out.txt kept",
			got.Verdict, got.Passed, got.Judges, judges.startCount(), got.Artifacts,
		)
	}
}
