package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
)

// TestRecaptureEvidenceCapturesTheHeldItemAtTheStoredBase: orreryd's recapture of R1 stores an amendment to the item R1
// already holds, which applies at once and captures its green and its reproduction at the stored base, with a decision
// that says it recaptures. Once both have run it is applied, with no trigger for the planner and no tell, since the
// plan reads its outcome.
func TestRecaptureEvidenceCapturesTheHeldItemAtTheStoredBase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createAmendEpic(t, s)
	moveStoredBase(t, s)

	if err := s.RecaptureEvidence(ctx, amendEpic, []string{"R1"}, amendAt()); err != nil {
		t.Fatalf("RecaptureEvidence: %v", err)
	}

	green := AmendmentWorkID(amendEpic, 1, AmendmentGreen, 0)
	repro := AmendmentWorkID(amendEpic, 1, AmendmentRepro, 0)
	stored := storedAmendment(t, s, 1)
	if stored.State != AmendmentCapturing || stored.StoredBase != "stored-2" || stored.GreenRun != green ||
		stored.ReproCheck != repro || stored.Amender != AmenderOrreryd {
		t.Fatalf("recapture = %+v, want orreryd's, capturing at stored-2 under %s and %s", stored, green, repro)
	}
	if !Recaptured(stored) {
		t.Errorf("Recaptured(%+v) = false, want true", stored)
	}
	if !reflect.DeepEqual(stored.Item, oldAmendItem()) || !reflect.DeepEqual(r1Item(t, s), oldAmendItem()) {
		t.Errorf("recapture item = %+v, R1 item = %+v, want both the item R1 held", stored.Item, r1Item(t, s))
	}
	decision := decisionNamed(t, s, amendEpic, "amend-1")
	if want := "orreryd recaptures the evidence of R1 at the stored base stored-2 (A1): the plan ended with R1 " +
		"not green at the stored base stored-2"; !strings.HasPrefix(decision.Text, want) {
		t.Errorf("decision = %q, want it to start %q", decision.Text, want)
	}

	judgedSpecRun(t, s, oldAmendItem(), green, evidence.PhaseGreen, "stored-2", 0, evidence.VerdictHolds)
	check := NodeCheck{ID: repro, Epic: amendEpic, Path: "amendment A1", Revision: "overlay", Argv: []string{"x/y"}}
	if err := s.AddNodeCheck(ctx, check, amendAt()); err != nil {
		t.Fatalf("AddNodeCheck: %v", err)
	}
	if err := s.EndNodeCheck(ctx, repro, NodeCheckEnd{Exit: 1}, amendAt()); err != nil {
		t.Fatalf("EndNodeCheck: %v", err)
	}
	settle(t, s)

	if got := storedAmendment(t, s, 1); got.State != AmendmentApplied {
		t.Fatalf("recapture once its green ran = %s, want applied", got.State)
	}
	history, err := s.EpicHistory(ctx, amendEpic)
	if err != nil {
		t.Fatalf("EpicHistory: %v", err)
	}
	for _, trigger := range history.Triggers {
		if trigger.ID == "amend-1" {
			t.Errorf("trigger amend-1 = %q, want none for a recapture", trigger.Cause)
		}
	}
	if tells := amendTells(t, s); len(tells) != 0 {
		t.Errorf("tells = %q, want none for a recapture", tells)
	}
}

// TestRecaptureEvidenceKeepsTheOtherRefusals: a recapture is refused, and writes nothing, for a Requirement that has an
// amendment not settled yet, as any amendment is; only the refusal of an item the Requirement already holds is lifted,
// and only for orreryd's recapture, so the owner's amendment to that item is still refused.
func TestRecaptureEvidenceKeepsTheOtherRefusals(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createAmendEpic(t, s)
	moveStoredBase(t, s)
	_, err := s.AmendEvidence(ctx, amendEpic, Amendment{Requirement: "R1", Item: oldAmendItem(), Reason: "again"},
		Maker{By: MakerOwner}, amendAt())
	var refusal RefusalError
	if !errors.As(err, &refusal) || string(refusal) != "R1 already holds this item" {
		t.Fatalf("owner AmendEvidence to the held item = %v, want it refused as already held", err)
	}
	if err = s.RecaptureEvidence(ctx, amendEpic, []string{"R1"}, amendAt()); err != nil {
		t.Fatalf("RecaptureEvidence: %v", err)
	}

	err = s.RecaptureEvidence(ctx, amendEpic, []string{"R1"}, amendAt())

	if !errors.As(err, &refusal) || string(refusal) != "R1 has an amendment that is not settled yet" {
		t.Fatalf("second RecaptureEvidence = %v, want the refusal of an amendment not settled yet", err)
	}
	if amendments := closeRow(t, s, amendEpic).Amendments; len(amendments) != 1 {
		t.Errorf("amendments = %+v, want the first recapture only", amendments)
	}
}

// TestRecapturedIsOnlyOrrerydsAmendmentToTheHeldItem: an owner amendment that changes how a Requirement is shown is no
// recapture, and neither is an amendment by orreryd to another item.
func TestRecapturedIsOnlyOrrerydsAmendmentToTheHeldItem(t *testing.T) {
	t.Parallel()
	held := oldAmendItem()
	moved := oldAmendItem()
	moved.Limit = 900
	cases := []struct {
		name      string
		amendment EvidenceAmendment
		want      bool
	}{
		{"orreryd to the held item", EvidenceAmendment{Amender: AmenderOrreryd, Prior: held, Item: held}, true},
		{"orreryd to another item", EvidenceAmendment{Amender: AmenderOrreryd, Prior: held, Item: moved}, false},
		{"the owner to the held item", EvidenceAmendment{Amender: AmenderOwner, Prior: held, Item: held}, false},
		{"the planner to another item", EvidenceAmendment{Amender: AmenderPlanner, Prior: held, Item: moved}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Recaptured(tc.amendment); got != tc.want {
				t.Errorf("Recaptured() = %v, want %v", got, tc.want)
			}
		})
	}
}
