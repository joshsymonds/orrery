package store_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// stepReadStart is the second the step read tests start their first step at. A later step starts a minute later.
const stepReadStart int64 = 1700006000

// addReadStep stores the step id as started at the unix second at. A step read names its step's row, which is all these
// tests need of a step.
func addReadStep(t *testing.T, s *store.Store, id string, at int64) {
	t.Helper()
	add := store.StepAdd{ID: id, Agent: "steelman-agent", Prompt: "check the packet"}
	if err := s.ApplyEngine(t.Context(), []store.EngineOp{add}, time.Unix(at, 0)); err != nil {
		t.Fatalf("ApplyEngine add step %s: %v", id, err)
	}
}

// TestRecordsProvenanceStepReadsListedOldestFirst checks that the reads of a contract list oldest first by the start of
// the step that made each, with ties by step id, and that a read of another contract does not list. step-zeta starts
// first, though its id sorts last, and step-alpha and step-mu start together, so only the tie puts alpha first. The
// reads are stored out of that order, and the Reader lists the same.
func TestRecordsProvenanceStepReadsListedOldestFirst(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, path := openEpicCallStore(t)
	addReadStep(t, s, "step-alpha", stepReadStart+60)
	addReadStep(t, s, "step-zeta", stepReadStart)
	addReadStep(t, s, "step-mu", stepReadStart+60)
	if err := s.RecordStepReads(ctx, []store.StepRead{
		{Step: "step-alpha", Contract: "contract-x", Version: 1},
		{Step: "step-zeta", Contract: "contract-y", Version: 1},
		{Step: "step-zeta", Contract: "contract-x", Version: 1},
		{Step: "step-mu", Contract: "contract-x", Version: 2},
	}); err != nil {
		t.Fatalf("RecordStepReads: %v", err)
	}

	want := []store.StepRead{
		{Step: "step-zeta", Contract: "contract-x", Version: 1},
		{Step: "step-alpha", Contract: "contract-x", Version: 1},
		{Step: "step-mu", Contract: "contract-x", Version: 2},
	}
	got, err := s.ContractStepReads(ctx, "contract-x")
	if err != nil {
		t.Fatalf("ContractStepReads: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ContractStepReads = %+v, want %+v", got, want)
	}
	read, err := store.NewReader(path).ContractStepReads(ctx, "contract-x")
	if err != nil {
		t.Fatalf("Reader.ContractStepReads: %v", err)
	}
	if !reflect.DeepEqual(read, want) {
		t.Fatalf("Reader.ContractStepReads = %+v, want %+v", read, want)
	}
}

// TestRecordsProvenanceStepReadsRefusedWriteStoresNone checks that a batch of reads that repeats a read the same step
// already made is refused, and that the reads ahead of the repeat in the batch are not stored either.
func TestRecordsProvenanceStepReadsRefusedWriteStoresNone(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, _ := openEpicCallStore(t)
	addReadStep(t, s, "step-one", stepReadStart)
	if err := s.RecordStepReads(ctx, []store.StepRead{
		{Step: "step-one", Contract: "contract-x", Version: 1},
		{Step: "step-one", Contract: "contract-x", Version: 1},
	}); err == nil {
		t.Fatal("RecordStepReads of a repeated read: no error, want one")
	}
	got, err := s.ContractStepReads(ctx, "contract-x")
	if err != nil {
		t.Fatalf("ContractStepReads: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ContractStepReads after a refused write = %+v, want none", got)
	}
}
