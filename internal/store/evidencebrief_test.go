package store

import (
	"context"
	"reflect"
	"testing"
)

// TestEvidenceBriefStoredWaitingReadsBackWithItsFields stores a brief waiting, as the runner files a captured item, and reads it
// back with each field it was stored with: its epic, item, commit, phase, source, state and caller's id, and its spec. It holds
// no grant, capture or judge, and it is open among the briefs of its epic.
func TestEvidenceBriefStoredWaitingReadsBackWithItsFields(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = "brief-r1-green"
	brief := newRun(id, runCreatedAt())
	brief.Phase = "green"
	brief.State = RunWaiting
	if err := s.AddEvidenceRun(ctx, brief); err != nil {
		t.Fatalf("AddEvidenceRun: %v", err)
	}

	want := EvidenceRun{
		ID: id, Epic: runEpic, Item: "R1", Revision: "abc1234", Phase: "green", Source: "captured",
		State: RunWaiting, Spec: runItem(), Created: runCreatedAt(),
	}
	assertRun(t, s, id, want, "after AddEvidenceRun")

	open, err := s.OpenBriefs(ctx, runEpic)
	if err != nil {
		t.Fatalf("OpenBriefs: %v", err)
	}
	if len(open) != 1 || !reflect.DeepEqual(open[0], want) {
		t.Fatalf("OpenBriefs = %+v, want the one waiting brief %+v", open, want)
	}
}
