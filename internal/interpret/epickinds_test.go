package interpret

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// reviewKind is the kind the format registers for an epic's review, which the interpreter leaves to O3e2.
const reviewKind = "review"

// reviewWaitText is a pinned workflow that plans and then names review, a kind the format registers and the
// interpreter does not.
const reviewWaitText = `name: review-wait
nodes:
  - id: plan
    kind: plan
    body:
      - id: build
        kind: build
  - id: review
    kind: review
`

// TestReviewNodeWaitsUnregistered loads a pinned workflow naming review and decides it with the kinds a Runner
// registers. The workflow loads, so the epic is not parked; the review node starts once the plan ends; and its running
// run gets no action, so it parks nothing and waits.
func TestReviewNodeWaitsUnregistered(t *testing.T) {
	t.Parallel()
	sum := sha256.Sum256([]byte(reviewWaitText))
	epic := store.EpicRow{
		ID:       decideEpic,
		Workflow: store.EpicWorkflow{Name: "review-wait", Text: reviewWaitText, SHA256: hex.EncodeToString(sum[:])},
	}
	flow, err := loadFlow(epic)
	if err != nil {
		t.Fatalf("loadFlow: %v", err)
	}
	reg := New(Config{}).kinds
	if _, ok := reg[reviewKind]; ok {
		t.Fatal("the Runner registers review; it must stay unregistered until O3e2")
	}
	plan := ended(startOf("plan", "plan", "plan", 0), "complete")

	started := Decide(flow, rowsOf(nil, nil, plan), reg)
	if len(started) != 1 || started[0].Kind != ActStart || len(started[0].Runs) != 1 ||
		started[0].Runs[0].Path != reviewKind || started[0].Runs[0].Kind != reviewKind {
		t.Fatalf("Decide after the plan = %+v, want one start of the review node", started)
	}

	review := running(startOf(reviewKind, reviewKind, reviewKind, 0))
	if waiting := Decide(flow, rowsOf(nil, nil, plan, review), reg); len(waiting) != 0 {
		t.Fatalf("Decide at the running review = %+v, want no action", waiting)
	}
}
