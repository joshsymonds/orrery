package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
)

// amendEpic is the epic the amendment tests amend, and amendBase its base, where each amendment's red run runs.
const (
	amendEpic = "orrery-1a2b3c4/epic-amend"
	amendBase = "base-amend"
)

// amendAt returns the second the amendment tests ask for and settle an amendment at.
func amendAt() time.Time {
	return time.Unix(1700002000, 0)
}

// amendItem returns a test item with conclusion, whose capture runs one Go test in pkg.
func amendItem(pkg, conclusion string) evidence.Item {
	return evidence.Item{
		Capture:    []string{"/usr/bin/go", "test", "-run", "QuietStepRestart", pkg},
		Limit:      600,
		Conclusion: conclusion,
		Judge:      "haiku-high-ro",
		Kind:       evidence.KindTest,
		Tests:      []string{pkg + "/quiet_test.go"},
	}
}

// amendConclusion is the conclusion of R1 of the amended epic.
const amendConclusion = "a quiet step survives a restart"

// oldAmendItem is the item R1 of the amended epic is created with, and newAmendItem the item the tests amend it to: the
// same conclusion, its test moved to another package.
func oldAmendItem() evidence.Item { return amendItem("internal/enginehost", amendConclusion) }
func newAmendItem() evidence.Item { return amendItem("cmd/orreryd", amendConclusion) }

// createAmendEpic stores the amended epic at its base, with R1 naming oldAmendItem and R3 naming a capture item.
func createAmendEpic(t *testing.T, s *Store) {
	t.Helper()
	row := fullEpic(amendEpic)
	row.Base = amendBase
	row.Requirements = []EpicRequirement{
		{ID: "R1", Text: "quiet steps restart", Item: oldAmendItem()},
		{ID: "R3", Text: "the store keeps runs", Item: runItem()},
	}
	row.Requirements[1].Item.Kind = evidence.KindCapture
	createEpic(t, s, row)
}

// amendOwner asks, as the owner, for R1 of the amended epic to hold item, and fails the test on an error.
func amendOwner(t *testing.T, s *Store, item evidence.Item) EvidenceAmendment {
	t.Helper()
	amended, err := s.AmendEvidence(context.Background(), amendEpic, Amendment{
		Requirement: "R1", Item: item, Reason: "the test moved to cmd/orreryd",
	}, amendAt())
	if err != nil {
		t.Fatalf("AmendEvidence: %v", err)
	}
	return amended
}

// r1Item returns the item R1 of the amended epic holds.
func r1Item(t *testing.T, s *Store) evidence.Item {
	t.Helper()
	for _, requirement := range closeRow(t, s, amendEpic).Requirements {
		if requirement.ID == "R1" {
			return requirement.Item
		}
	}
	t.Fatalf("epic %s has no Requirement R1", amendEpic)
	return evidence.Item{}
}

// storedAmendment returns amendment seq of the amended epic as the epic reads it back.
func storedAmendment(t *testing.T, s *Store, seq int64) EvidenceAmendment {
	t.Helper()
	for _, amended := range closeRow(t, s, amendEpic).Amendments {
		if amended.Seq == seq {
			return amended
		}
	}
	t.Fatalf("epic %s has no amendment %d", amendEpic, seq)
	return EvidenceAmendment{}
}

// judgedRun stores run id of the new item of R1 in phase at revision, captured with exit and judged verdict, as the
// evidence runner leaves a run it has judged. passed is what evidence.Passed gives for the phase and verdict.
func judgedRun(t *testing.T, s *Store, id, phase, revision string, exit int, verdict string) {
	t.Helper()
	judgedSpecRun(t, s, newAmendItem(), id, phase, revision, exit, verdict)
}

// judgedSpecRun stores run id of spec, an item asked for R1, as judgedRun stores a run of the new item.
func judgedSpecRun(t *testing.T, s *Store, spec evidence.Item, id, phase, revision string, exit int, verdict string) {
	t.Helper()
	ctx := context.Background()
	if err := s.AddEvidenceRun(ctx, EvidenceRun{
		ID: id, Epic: amendEpic, Item: "R1", Revision: revision, Phase: phase, Source: evidence.SourceCaptured,
		Spec: spec, Created: amendAt(),
	}); err != nil {
		t.Fatalf("AddEvidenceRun %s: %v", id, err)
	}
	if err := s.RecordEvidenceCapture(ctx, id, EvidenceCapture{Exit: exit, LogHash: "log", At: amendAt()}); err != nil {
		t.Fatalf("RecordEvidenceCapture %s: %v", id, err)
	}
	if err := s.SetEvidenceJudge(ctx, id, "judge-"+id); err != nil {
		t.Fatalf("SetEvidenceJudge %s: %v", id, err)
	}
	if err := s.RecordEvidenceVerdict(ctx, id, EvidenceVerdict{
		Verdict: verdict, Reason: "the judge read the log", Passed: evidence.Passed(phase, "", exit, verdict),
		At: amendAt(),
	}); err != nil {
		t.Fatalf("RecordEvidenceVerdict %s: %v", id, err)
	}
}

// uncapturedRun stores run id of the new item of R1 at the base as a red run that captured nothing, for notCaptured.
func uncapturedRun(t *testing.T, s *Store, id, notCaptured string) {
	t.Helper()
	ctx := context.Background()
	if err := s.AddEvidenceRun(ctx, EvidenceRun{
		ID: id, Epic: amendEpic, Item: "R1", Revision: amendBase, Phase: evidence.PhaseRed,
		Source: evidence.SourceCaptured, Spec: newAmendItem(), Created: amendAt(),
	}); err != nil {
		t.Fatalf("AddEvidenceRun %s: %v", id, err)
	}
	if err := s.RecordEvidenceCapture(ctx, id, EvidenceCapture{NotCaptured: notCaptured, At: amendAt()}); err != nil {
		t.Fatalf("RecordEvidenceCapture %s: %v", id, err)
	}
}

// amendTells returns the texts of the tells of the amended epic, in seq order, as EpicHistory reads them.
func amendTells(t *testing.T, s *Store) []string {
	t.Helper()
	history, err := s.EpicHistory(context.Background(), amendEpic)
	if err != nil {
		t.Fatalf("EpicHistory: %v", err)
	}
	texts := make([]string, 0, len(history.Tells))
	for _, tell := range history.Tells {
		texts = append(texts, tell.Text)
	}
	return texts
}

// settle settles amendment A1 of the amended epic at amendAt and fails the test on an error.
func settle(t *testing.T, s *Store) {
	t.Helper()
	if err := s.SettleAmendment(context.Background(), amendEpic, 1, amendAt()); err != nil {
		t.Fatalf("SettleAmendment: %v", err)
	}
}

// moveStoredBase stores stored-2 as the stored base of the amended epic, as a passed batch moves it.
func moveStoredBase(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.fixtureExec(context.Background(), `UPDATE epics SET stored_base = 'stored-2' WHERE id = ?`,
		amendEpic); err != nil {
		t.Fatalf("move stored base: %v", err)
	}
}

func TestAmendEvidenceStartsCheckingAndKeepsTheItem(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	createAmendEpic(t, s)

	got := amendOwner(t, s, newAmendItem())
	want := EvidenceAmendment{
		Epic: amendEpic, Seq: 1, Requirement: "R1", Amender: AmenderOwner, Prior: oldAmendItem(), Item: newAmendItem(),
		Reason: "the test moved to cmd/orreryd", Base: amendBase, State: AmendmentChecking,
		RedRun: AmendmentWorkID(amendEpic, 1, AmendmentRed, 0), Created: amendAt(),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("AmendEvidence = %+v, want %+v", got, want)
	}
	if stored := storedAmendment(t, s, 1); !reflect.DeepEqual(stored, want) {
		t.Errorf("Epic amendment 1 = %+v, want %+v", stored, want)
	}
	if item := r1Item(t, s); !reflect.DeepEqual(item, oldAmendItem()) {
		t.Errorf("R1 item while checking = %+v, want the old item %+v", item, oldAmendItem())
	}
	open, err := s.OpenAmendments(context.Background())
	if err != nil {
		t.Fatalf("OpenAmendments: %v", err)
	}
	if len(open) != 1 || open[0].Seq != 1 {
		t.Errorf("OpenAmendments = %+v, want amendment 1 alone", open)
	}
}

func TestAmendmentWorkIDIsStablePerPartAndAttempt(t *testing.T) {
	t.Parallel()
	first := AmendmentWorkID(amendEpic, 1, AmendmentRed, 0)
	if again := AmendmentWorkID(amendEpic, 1, AmendmentRed, 0); again != first {
		t.Errorf("AmendmentWorkID twice = %s and %s, want the same id", first, again)
	}
	others := []string{
		AmendmentWorkID(amendEpic, 1, AmendmentRed, 1), AmendmentWorkID(amendEpic, 2, AmendmentRed, 0),
		AmendmentWorkID(amendEpic, 1, AmendmentGreen, 0), AmendmentWorkID("other", 1, AmendmentRed, 0),
	}
	for _, other := range others {
		if other == first {
			t.Errorf("AmendmentWorkID %s repeats for other inputs", other)
		}
	}
	if len(first) != nodeIDLength {
		t.Errorf("AmendmentWorkID length = %d, want %d", len(first), nodeIDLength)
	}
}

func TestAmendEvidenceRefusals(t *testing.T) {
	t.Parallel()
	changed := newAmendItem()
	changed.Conclusion = "a quiet step is quiet"
	noKind := newAmendItem()
	noKind.Kind = ""
	cases := []struct {
		name    string
		amend   Amendment
		prepare func(t *testing.T, s *Store)
		want    string
	}{
		{
			name:  "a Requirement the epic does not hold",
			amend: Amendment{Requirement: "R9", Item: newAmendItem()},
			want:  `epic orrery-1a2b3c4/epic-amend has no Requirement "R9"`,
		},
		{
			name:  "an item evidence.Check refuses",
			amend: Amendment{Requirement: "R1", Item: noKind},
			want:  "item: kind: none, name test, capture or supplied",
		},
		{
			name:  "the item the Requirement holds",
			amend: Amendment{Requirement: "R1", Item: oldAmendItem()},
			want:  "R1 already holds this item",
		},
		{
			name: "the item the Requirement holds, its empty lists given as []",
			amend: Amendment{Requirement: "R1", Item: func() evidence.Item {
				item := oldAmendItem()
				item.Artifacts, item.Supplied = []string{}, []string{}
				return item
			}()},
			want: "R1 already holds this item",
		},
		{
			name:    "a second amendment while one is checking",
			amend:   Amendment{Requirement: "R1", Item: changed},
			prepare: func(t *testing.T, s *Store) { t.Helper(); amendOwner(t, s, newAmendItem()) },
			want:    "R1 has an amendment that is not settled yet",
		},
		{
			name:  "a Requirement dropped as met at the base",
			amend: Amendment{Requirement: "R1", Item: newAmendItem()},
			prepare: func(t *testing.T, s *Store) {
				t.Helper()
				if _, err := s.fixtureExec(context.Background(),
					`UPDATE epic_requirements SET dropped_by = 2 WHERE epic = ? AND id = 'R1'`, amendEpic); err != nil {
					t.Fatalf("drop R1: %v", err)
				}
			},
			want: "R1 was dropped as already met at the epic's base by decision 2",
		},
		{
			name:  "an epic in an ended phase",
			amend: Amendment{Requirement: "R1", Item: newAmendItem()},
			prepare: func(t *testing.T, s *Store) {
				t.Helper()
				if _, err := s.fixtureExec(context.Background(),
					`UPDATE epics SET phase = 'released' WHERE id = ?`, amendEpic); err != nil {
					t.Fatalf("release epic: %v", err)
				}
			},
			want: "epic orrery-1a2b3c4/epic-amend is released, an ended phase, and takes no evidence amendment",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := openEpicStore(t)
			createAmendEpic(t, s)
			if tc.prepare != nil {
				tc.prepare(t, s)
			}
			before := len(closeRow(t, s, amendEpic).Amendments)
			_, err := s.AmendEvidence(context.Background(), amendEpic, tc.amend, amendAt())
			var refusal RefusalError
			if !errors.As(err, &refusal) || refusal.Error() != tc.want {
				t.Fatalf("AmendEvidence = %v, want the refusal %q", err, tc.want)
			}
			if after := len(closeRow(t, s, amendEpic).Amendments); after != before {
				t.Errorf("amendments after the refusal = %d, want %d: a refusal stores nothing", after, before)
			}
			if item := r1Item(t, s); !reflect.DeepEqual(item, oldAmendItem()) {
				t.Errorf("R1 item after the refusal = %+v, want the old item", item)
			}
		})
	}
}

func TestAmendEvidenceOfAnUnknownEpicIsErrNoEpic(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	createAmendEpic(t, s)
	amend := Amendment{Requirement: "R1", Item: newAmendItem()}
	if _, err := s.AmendEvidence(context.Background(), "no-such-epic", amend, amendAt()); !errors.Is(err, ErrNoEpic) {
		t.Errorf("AmendEvidence of an unknown epic = %v, want ErrNoEpic", err)
	}
}

func TestOwnerMayChangeTheConclusion(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	createAmendEpic(t, s)
	changed := newAmendItem()
	changed.Conclusion = "a quiet step restarts quietly"
	if got := amendOwner(t, s, changed); got.State != AmendmentChecking || got.Item.Conclusion != changed.Conclusion {
		t.Errorf("owner AmendEvidence with a new conclusion = %+v, want checking with that conclusion", got)
	}
}

func TestSettleAmendmentAppliesARedThatFailsAtTheBase(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	createAmendEpic(t, s)
	amended := amendOwner(t, s, newAmendItem())

	settle(t, s)
	if stored := storedAmendment(t, s, 1); stored.State != AmendmentChecking {
		t.Fatalf("amendment with no red run = %s, want checking: a settle with no work writes nothing", stored.State)
	}

	judgedRun(t, s, amended.RedRun, evidence.PhaseRed, amendBase, 1, evidence.VerdictFails)
	settle(t, s)

	stored := storedAmendment(t, s, 1)
	if stored.State != AmendmentApplied || stored.Settled != amendAt() {
		t.Errorf("amendment after a red that fails = %s settled %v, want applied at %v", stored.State, stored.Settled,
			amendAt())
	}
	if item := r1Item(t, s); !reflect.DeepEqual(item, newAmendItem()) {
		t.Errorf("R1 item after the amendment = %+v, want the new item %+v", item, newAmendItem())
	}
	text := decisionNamed(t, s, amendEpic, "amend-1").Text
	for _, want := range []string{
		"owner amended the evidence of R1 (A1): the test moved to cmd/orreryd.",
		"The red run " + amended.RedRun + " of the new item at the epic's base " + amendBase + " was judged fails.",
		"Prior item: " + itemJSON(oldAmendItem()) + ".", "New item: " + itemJSON(newAmendItem()) + ".",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("decision amend-1 = %q, want it to hold %q", text, want)
		}
	}
	state, cause := triggerRow(t, s, amendEpic, "amend-1")
	wantCause := "evidence of R1 amended by the owner (A1): the next integrate captures the new item at its candidate"
	if state != triggerPending || cause != wantCause {
		t.Errorf("trigger amend-1 = %s %q, want pending %q", state, cause, wantCause)
	}
	if tells := amendTells(t, s); len(tells) != 1 || tells[0] != capitalized(wantCause) {
		t.Errorf("tells = %q, want the owner told %q", tells, capitalized(wantCause))
	}

	// The settle is done: a repeat changes nothing.
	settle(t, s)
	if got := storedAmendment(t, s, 1); !reflect.DeepEqual(got, stored) {
		t.Errorf("amendment after a second settle = %+v, want it unchanged %+v", got, stored)
	}
}

func TestSettleAmendmentRefusesARedThatHoldsAtTheBase(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	createAmendEpic(t, s)
	amended := amendOwner(t, s, newAmendItem())
	judgedRun(t, s, amended.RedRun, evidence.PhaseRed, amendBase, 0, evidence.VerdictHolds)

	settle(t, s)

	refusal := "the red run " + amended.RedRun + " of the new item at the epic's base " + amendBase +
		" was judged holds, so the new item does not fail at the base: the judge read the log"
	stored := storedAmendment(t, s, 1)
	if stored.State != AmendmentRefused || stored.Refusal != refusal {
		t.Errorf("amendment after a red that holds = %s %q, want refused %q", stored.State, stored.Refusal, refusal)
	}
	if item := r1Item(t, s); !reflect.DeepEqual(item, oldAmendItem()) {
		t.Errorf("R1 item after the refusal = %+v, want the old item kept", item)
	}
	text := decisionNamed(t, s, amendEpic, "amend-1-refused").Text
	want := "owner amendment A1 of the evidence of R1 refused: " + refusal + ". Its reason: the test moved to " +
		"cmd/orreryd." + itemsText(oldAmendItem(), newAmendItem())
	if text != want {
		t.Errorf("decision amend-1-refused = %q, want %q", text, want)
	}
	tell := "Evidence amendment A1 of R1 refused: " + refusal
	if tells := amendTells(t, s); len(tells) != 1 || tells[0] != tell {
		t.Errorf("tells = %q, want %q", tells, tell)
	}
	if open, err := s.OpenAmendments(context.Background()); err != nil || len(open) != 0 {
		t.Errorf("OpenAmendments after the refusal = %v, %v, want none", open, err)
	}
}

func TestSettleAmendmentRefusesARedWithNoVerdict(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	createAmendEpic(t, s)
	amended := amendOwner(t, s, newAmendItem())
	uncapturedRun(t, s, amended.RedRun, "could not start: no grant")

	settle(t, s)

	want := "the red run " + amended.RedRun + " of the new item at the epic's base " + amendBase +
		" captured nothing: could not start: no grant"
	if stored := storedAmendment(t, s, 1); stored.State != AmendmentRefused || stored.Refusal != want {
		t.Errorf("amendment after a red that captured nothing = %s %q, want refused %q", stored.State, stored.Refusal,
			want)
	}
}

func TestSettleAmendmentRerunsARedLostAtARestart(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	createAmendEpic(t, s)
	amendOwner(t, s, newAmendItem())

	for attempt := range int64(amendmentReruns) {
		uncapturedRun(t, s, AmendmentWorkID(amendEpic, 1, AmendmentRed, attempt), LostAtRestart)
		settle(t, s)
		stored := storedAmendment(t, s, 1)
		next := AmendmentWorkID(amendEpic, 1, AmendmentRed, attempt+1)
		if stored.State != AmendmentChecking || stored.RedAttempt != attempt+1 || stored.RedRun != next {
			t.Fatalf("amendment after red attempt %d was lost = %s attempt %d run %s, want checking at %d under %s",
				attempt, stored.State, stored.RedAttempt, stored.RedRun, attempt+1, next)
		}
	}
	last := AmendmentWorkID(amendEpic, 1, AmendmentRed, amendmentReruns)
	uncapturedRun(t, s, last, LostAtRestart)
	settle(t, s)
	want := fmt.Sprintf("the red run %s of the new item at the epic's base %s captured nothing: %s", last, amendBase,
		LostAtRestart)
	if stored := storedAmendment(t, s, 1); stored.State != AmendmentRefused || stored.Refusal != want {
		t.Errorf("amendment after its last rerun was lost = %s %q, want refused %q", stored.State, stored.Refusal, want)
	}
}

// TestSettleAmendmentWaitsForEvidenceWorkUnderWay checks that an amendment whose red passed is not applied while a node
// run that captures the Requirement under its own ids is under way: an integrate run with its revision, or a sync run of
// origin's main with its merge, whose checks capture as an integrate run's do. It is applied once that run has ended.
func TestSettleAmendmentWaitsForEvidenceWorkUnderWay(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		run      NodeRun
		revision string
	}{
		{
			name:     "an integrate run with its revision",
			run:      NodeRun{Path: "plan/S1/integrate", ID: "integrate-run", Node: "integrate", Kind: "integrate"},
			revision: "candidate-1",
		},
		{
			name:     "a sync run of origin's main with its merge",
			run:      NodeRun{Path: "plan/M1", ID: "sync-run", Node: "sync", Kind: "sync"},
			revision: "merge-1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			s := openEpicStore(t)
			createAmendEpic(t, s)
			amended := amendOwner(t, s, newAmendItem())
			judgedRun(t, s, amended.RedRun, evidence.PhaseRed, amendBase, 1, evidence.VerdictFails)
			if err := s.StartNodes(ctx, amendEpic, []NodeRun{tc.run}, nil, amendAt()); err != nil {
				t.Fatalf("StartNodes: %v", err)
			}
			if tc.revision != "" {
				if err := s.PrepareNode(ctx, amendEpic, tc.run.Path, 0, tc.revision, "refs"); err != nil {
					t.Fatalf("PrepareNode: %v", err)
				}
			}

			err := s.SettleAmendment(ctx, amendEpic, 1, amendAt())
			if !errors.Is(err, ErrAmendmentDeferred) {
				t.Fatalf("SettleAmendment with %s under way = %v, want ErrAmendmentDeferred", tc.name, err)
			}
			if stored := storedAmendment(t, s, 1); stored.State != AmendmentChecking {
				t.Errorf("deferred amendment = %s, want checking", stored.State)
			}
			if item := r1Item(t, s); !reflect.DeepEqual(item, oldAmendItem()) {
				t.Errorf("R1 item while deferred = %+v, want the old item", item)
			}

			if err = s.EndNodes(
				ctx,
				amendEpic,
				[]NodeEnd{{Path: tc.run.Path, Outcome: "passed"}},
				amendAt(),
			); err != nil {
				t.Fatalf("EndNodes: %v", err)
			}
			settle(t, s)
			if stored := storedAmendment(t, s, 1); stored.State != AmendmentApplied {
				t.Errorf("amendment once the run ended = %s, want applied", stored.State)
			}
		})
	}
}

// amendRelease is the running release node run of the amended epic in the release tests, at its first attempt.
func amendRelease() NodeRun {
	return NodeRun{Path: "release", ID: "release-run", Node: "release", Kind: "release"}
}

// releaseAmendEpic stores the amended epic in phase with its release node run running, the release runs runs recorded
// at its first attempt, and the runs under the release running, then asks for A1 and stores its red as judged fails at
// the base, so that only the release can hold the amendment back.
func releaseAmendEpic(t *testing.T, s *Store, phase string, runs []ReleaseRun, under ...NodeRun) {
	t.Helper()
	ctx := context.Background()
	createAmendEpic(t, s)
	if _, err := s.fixtureExec(ctx, `UPDATE epics SET phase = ? WHERE id = ?`, phase, amendEpic); err != nil {
		t.Fatalf("set phase %s: %v", phase, err)
	}
	if err := s.StartNodes(ctx, amendEpic, append([]NodeRun{amendRelease()}, under...), nil, amendAt()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}
	for _, run := range runs {
		run.At = amendAt()
		var err error
		if run.Part == ReleasePartCandidate {
			err = s.RecordCandidate(ctx, amendEpic, run)
		} else {
			err = s.RecordRelease(ctx, amendEpic, run)
		}
		if err != nil {
			t.Fatalf("record release run %s %d: %v", run.Part, run.Position, err)
		}
	}
	amended := amendOwner(t, s, newAmendItem())
	judgedRun(t, s, amended.RedRun, evidence.PhaseRed, amendBase, 1, evidence.VerdictFails)
}

// mergedRound returns round position of the release integrator at the first attempt, which passed with the merge head,
// and failedRound one that returned blocked.
func mergedRound(position int64, head string) ReleaseRun {
	return ReleaseRun{
		Part: ReleasePartIntegrator, Position: position, Step: "integrator", Start: "tip-1", Head: head, Passed: true,
	}
}

func failedRound(position int64) ReleaseRun {
	return ReleaseRun{
		Part: ReleasePartIntegrator, Position: position, Step: "integrator", Start: "tip-1", End: "returned",
		Reason: "blocked on R3's wording",
	}
}

// candidateRun returns the candidate of the first attempt, passed at head.
func candidateRun(head string) ReleaseRun {
	return ReleaseRun{Part: ReleasePartCandidate, Start: "tip-1", Head: head, Passed: true}
}

// TestSettleAmendmentWaitsForAReleaseCapturingItsMerge checks that an amendment whose red passed is not applied while
// the epic's release captures the Requirement under its own ids: the epic is releasing, and the latest round of its
// release integrator that passed at the run's attempt holds a merge whose Done gates and captures are walked until that
// merge is recorded as the candidate. It is applied once the merge is the candidate, since the release then reads the
// captures no more.
func TestSettleAmendmentWaitsForAReleaseCapturingItsMerge(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		runs  []ReleaseRun
		merge string
	}{
		{
			name:  "the merge of the first round",
			runs:  []ReleaseRun{mergedRound(1, "merge-1")},
			merge: "merge-1",
		},
		{
			name:  "the merge of a later round, after the first one's captures failed",
			runs:  []ReleaseRun{mergedRound(1, "merge-1"), mergedRound(2, "merge-2")},
			merge: "merge-2",
		},
		{
			name: "the merge of a candidate prepared again",
			runs: []ReleaseRun{
				mergedRound(1, "merge-1"), candidateRun("merge-1"),
				{Part: ReleasePartVerify, Head: "merge-1", Reason: "origin's main moved"}, mergedRound(4, "merge-2"),
			},
			merge: "merge-2",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			s := openEpicStore(t)
			releaseAmendEpic(t, s, PhaseReleasing, tc.runs)

			err := s.SettleAmendment(ctx, amendEpic, 1, amendAt())
			if !errors.Is(err, ErrAmendmentDeferred) {
				t.Fatalf("SettleAmendment while the release captures %s = %v, want ErrAmendmentDeferred", tc.merge, err)
			}
			if stored := storedAmendment(t, s, 1); stored.State != AmendmentChecking {
				t.Errorf("deferred amendment = %s, want checking", stored.State)
			}
			if item := r1Item(t, s); !reflect.DeepEqual(item, oldAmendItem()) {
				t.Errorf("R1 item while deferred = %+v, want the old item", item)
			}

			candidate := candidateRun(tc.merge)
			candidate.At = amendAt()
			if err = s.RecordCandidate(ctx, amendEpic, candidate); err != nil {
				t.Fatalf("RecordCandidate: %v", err)
			}
			settle(t, s)
			if stored := storedAmendment(t, s, 1); stored.State != AmendmentApplied {
				t.Errorf("amendment once %s is the candidate = %s, want applied", tc.merge, stored.State)
			}
		})
	}
}

// TestSettleAmendmentDoesNotWaitForAReleaseThatCapturesNothing checks that a running release run captures nothing, and
// so holds no amendment back, unless its epic is releasing with a merge of its release integrator not yet its
// candidate. The first case is the release a replan sent back to planning: its fix plan runs under it and waits for
// the amendment to settle, so a release that held the amendment back would never move again.
func TestSettleAmendmentDoesNotWaitForAReleaseThatCapturesNothing(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		phase string
		runs  []ReleaseRun
		under []NodeRun
	}{
		{
			name:  "a release replanned, with its fix plan running under it",
			phase: "correcting",
			runs: []ReleaseRun{
				mergedRound(1, "merge-1"), failedRound(2),
				{Part: ReleasePartReplan, Step: "replan", Start: "tip-1", Reason: "blocked on R3's wording"},
			},
			under: []NodeRun{{Path: "release/A0/fix", ID: "fix-run", Node: "fix", Kind: "plan"}},
		},
		{
			name:  "a releasing release with no merge of its integrator",
			phase: PhaseReleasing,
		},
		{
			name:  "a releasing release whose merge is its candidate",
			phase: PhaseReleasing,
			runs:  []ReleaseRun{mergedRound(1, "merge-1"), candidateRun("merge-1")},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := openEpicStore(t)
			releaseAmendEpic(t, s, tc.phase, tc.runs, tc.under...)

			settle(t, s)

			if stored := storedAmendment(t, s, 1); stored.State != AmendmentApplied {
				t.Errorf("amendment beside %s = %s, want applied", tc.name, stored.State)
			}
			if item := r1Item(t, s); !reflect.DeepEqual(item, newAmendItem()) {
				t.Errorf("R1 item beside %s = %+v, want the new item", tc.name, item)
			}
		})
	}
}

// TestSettleAmendmentDoesNotWaitForAnIntegrateWithNoRevision checks that an integrate run that has not prepared its
// batch, which captures nothing yet, does not hold an amendment back: the batch it prepares later captures the new item.
func TestSettleAmendmentDoesNotWaitForAnIntegrateWithNoRevision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createAmendEpic(t, s)
	amended := amendOwner(t, s, newAmendItem())
	judgedRun(t, s, amended.RedRun, evidence.PhaseRed, amendBase, 1, evidence.VerdictFails)
	run := NodeRun{Path: "plan/S1/integrate", ID: "integrate-run", Node: "integrate", Kind: "integrate"}
	if err := s.StartNodes(ctx, amendEpic, []NodeRun{run}, nil, amendAt()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}

	settle(t, s)

	if stored := storedAmendment(t, s, 1); stored.State != AmendmentApplied {
		t.Errorf("amendment beside an unprepared integrate = %s, want applied", stored.State)
	}
}

func TestSettleAmendmentCapturesGreenAtAMovedStoredBase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createAmendEpic(t, s)
	moveStoredBase(t, s)
	amended := amendOwner(t, s, newAmendItem())
	judgedRun(t, s, amended.RedRun, evidence.PhaseRed, amendBase, 1, evidence.VerdictFails)

	settle(t, s)

	green := AmendmentWorkID(amendEpic, 1, AmendmentGreen, 0)
	repro := AmendmentWorkID(amendEpic, 1, AmendmentRepro, 0)
	stored := storedAmendment(t, s, 1)
	if stored.State != AmendmentCapturing || stored.StoredBase != "stored-2" || stored.GreenRun != green ||
		stored.ReproCheck != repro {
		t.Fatalf("amendment at a moved stored base = %+v, want capturing at stored-2 under %s and %s", stored, green,
			repro)
	}
	if item := r1Item(t, s); !reflect.DeepEqual(item, newAmendItem()) {
		t.Errorf("R1 item while capturing = %+v, want the new item applied", item)
	}
	history, err := s.EpicHistory(ctx, amendEpic)
	if err != nil {
		t.Fatalf("EpicHistory: %v", err)
	}
	for _, trigger := range history.Triggers {
		if trigger.ID == "amend-1" {
			t.Fatalf("trigger amend-1 while capturing, want none until green has run")
		}
	}

	judgedRun(t, s, green, evidence.PhaseGreen, "stored-2", 0, evidence.VerdictHolds)
	settle(t, s)
	if got := storedAmendment(t, s, 1); got.State != AmendmentCapturing {
		t.Fatalf("amendment with its reproduction not stored = %s, want capturing", got.State)
	}
	check := NodeCheck{ID: repro, Epic: amendEpic, Path: "amendment A1", Revision: "overlay", Argv: []string{"x/y"}}
	if err = s.AddNodeCheck(ctx, check, amendAt()); err != nil {
		t.Fatalf("AddNodeCheck: %v", err)
	}
	if err = s.EndNodeCheck(ctx, repro, NodeCheckEnd{Exit: 1}, amendAt()); err != nil {
		t.Fatalf("EndNodeCheck: %v", err)
	}
	settle(t, s)

	if got := storedAmendment(t, s, 1); got.State != AmendmentApplied {
		t.Errorf("amendment once green and its reproduction ended = %s, want applied", got.State)
	}
	_, cause := triggerRow(t, s, amendEpic, "amend-1")
	want := "evidence of R1 amended by the owner (A1): the green run " + green + " at the stored base stored-2 " +
		"passed; its reproduction " + repro + " at the epic's base exited 1"
	if cause != want {
		t.Errorf("trigger amend-1 cause = %q, want %q", cause, want)
	}
}

func TestSettleAmendmentRerunsAGreenLostAtARestart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createAmendEpic(t, s)
	moveStoredBase(t, s)
	capture := newAmendItem()
	capture.Kind, capture.Tests = evidence.KindCapture, nil
	amended, err := s.AmendEvidence(ctx, amendEpic, Amendment{
		Requirement: "R1", Item: capture, Reason: "a capture shows it",
	}, amendAt())
	if err != nil {
		t.Fatalf("AmendEvidence: %v", err)
	}
	judgedSpecRun(t, s, capture, amended.RedRun, evidence.PhaseRed, amendBase, 1, evidence.VerdictFails)
	settle(t, s)
	green := AmendmentWorkID(amendEpic, 1, AmendmentGreen, 0)
	if stored := storedAmendment(t, s, 1); stored.GreenRun != green || stored.ReproCheck != "" {
		t.Fatalf("capturing amendment of a capture item = green %s repro %q, want %s and no reproduction",
			stored.GreenRun, stored.ReproCheck, green)
	}
	if err = s.AddEvidenceRun(ctx, EvidenceRun{
		ID: green, Epic: amendEpic, Item: "R1", Revision: "stored-2", Phase: evidence.PhaseGreen,
		Source: evidence.SourceCaptured, Spec: capture, Created: amendAt(),
	}); err != nil {
		t.Fatalf("AddEvidenceRun: %v", err)
	}
	if err = s.RecordEvidenceCapture(
		ctx,
		green,
		EvidenceCapture{NotCaptured: LostAtRestart, At: amendAt()},
	); err != nil {
		t.Fatalf("RecordEvidenceCapture: %v", err)
	}

	settle(t, s)

	stored := storedAmendment(t, s, 1)
	next := AmendmentWorkID(amendEpic, 1, AmendmentGreen, 1)
	if stored.State != AmendmentCapturing || stored.GreenAttempt != 1 || stored.GreenRun != next {
		t.Errorf("amendment after its green was lost = %s attempt %d run %s, want capturing at 1 under %s",
			stored.State, stored.GreenAttempt, stored.GreenRun, next)
	}
}

func TestStopAmendment(t *testing.T) {
	t.Parallel()
	t.Run("a checking amendment is refused", func(t *testing.T) {
		t.Parallel()
		s := openEpicStore(t)
		createAmendEpic(t, s)
		amended := amendOwner(t, s, newAmendItem())
		if err := s.StopAmendment(
			context.Background(),
			amendEpic,
			1,
			"revision does not resolve",
			amendAt(),
		); err != nil {
			t.Fatalf("StopAmendment: %v", err)
		}
		want := "the red run " + amended.RedRun + " of the new item at the epic's base " + amendBase +
			" could not start: revision does not resolve"
		if stored := storedAmendment(t, s, 1); stored.State != AmendmentRefused || stored.Refusal != want {
			t.Errorf("stopped checking amendment = %s %q, want refused %q", stored.State, stored.Refusal, want)
		}
	})
	t.Run("a capturing amendment is applied", func(t *testing.T) {
		t.Parallel()
		s := openEpicStore(t)
		createAmendEpic(t, s)
		moveStoredBase(t, s)
		amended := amendOwner(t, s, newAmendItem())
		judgedRun(t, s, amended.RedRun, evidence.PhaseRed, amendBase, 1, evidence.VerdictFails)
		settle(t, s)
		if err := s.StopAmendment(context.Background(), amendEpic, 1, "no such revision", amendAt()); err != nil {
			t.Fatalf("StopAmendment: %v", err)
		}
		if stored := storedAmendment(t, s, 1); stored.State != AmendmentApplied {
			t.Errorf("stopped capturing amendment = %s, want applied", stored.State)
		}
		_, cause := triggerRow(t, s, amendEpic, "amend-1")
		want := "evidence of R1 amended by the owner (A1): the green run " +
			AmendmentWorkID(amendEpic, 1, AmendmentGreen, 0) + " at the stored base stored-2 could not start: " +
			"no such revision"
		if cause != want {
			t.Errorf("trigger amend-1 cause = %q, want %q", cause, want)
		}
	})
}

// amendAction is an amend_evidence action that asks for R1 to hold item.
func amendAction(item evidence.Item) EpicActionInput {
	return EpicActionInput{
		Kind: ActionAmendEvidence, Requirement: "R1", Item: item, Reason: "R1's test now lives in cmd/orreryd",
	}
}

// actionState returns the state and refusal of action as planner_actions stores them.
func actionState(t *testing.T, s *Store, action PlannerAction) (state, refusal string) {
	t.Helper()
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT state, refusal FROM planner_actions WHERE epic = ? AND turn = ? AND position = ?`,
		action.Epic, action.Turn, action.Position).Scan(&state, &refusal); err != nil {
		t.Fatalf("read action %d of epic %s: %v", action.Position, action.Epic, err)
	}
	return state, refusal
}

func TestAmendEvidenceActionStaysPendingWhileItsRedRuns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createAmendEpic(t, s)
	action := storeActionsOf(t, s, amendAction(newAmendItem()))[0]
	if action.Requirement != "R1" || !reflect.DeepEqual(action.Item, newAmendItem()) {
		t.Fatalf("stored action = %+v, want R1 and the new item read back", action)
	}

	for range 2 {
		if stop, err := s.CarryOutAction(ctx, action, amendAt()); err != nil || stop != nil {
			t.Fatalf("CarryOutAction = %v, %v, want no steps and no error", stop, err)
		}
	}
	amendments := closeRow(t, s, amendEpic).Amendments
	if len(amendments) != 1 {
		t.Fatalf("amendments after carrying the action out twice = %d, want 1", len(amendments))
	}
	amended := amendments[0]
	if amended.Amender != AmenderPlanner || amended.Turn != 1 || amended.Position != 1 ||
		amended.State != AmendmentChecking {
		t.Errorf("planned amendment = %+v, want the planner's, of action 1 of turn 1, checking", amended)
	}
	if state, _ := actionState(t, s, action); state != "pending" {
		t.Errorf("action while its red runs = %s, want pending", state)
	}

	judgedRun(t, s, amended.RedRun, evidence.PhaseRed, amendBase, 1, evidence.VerdictFails)
	settle(t, s)

	if state, _ := actionState(t, s, action); state != "taken" {
		t.Errorf("action once its amendment applied = %s, want taken", state)
	}
	text := decisionNamed(t, s, amendEpic, "action-1-1").Text
	if !strings.HasPrefix(text, "planner amended the evidence of R1 (A1): R1's test now lives in cmd/orreryd.") ||
		!strings.Contains(text, itemsText(oldAmendItem(), newAmendItem())) {
		t.Errorf("decision action-1-1 = %q, want the planner's amendment with both items", text)
	}
	if state, _ := triggerRow(t, s, amendEpic, "amend-1"); state != triggerPending {
		t.Errorf("trigger amend-1 = %s, want pending", state)
	}
	if tells := amendTells(t, s); len(tells) != 0 {
		t.Errorf("tells of a planner amendment = %q, want none", tells)
	}
	if pending := pendingActionsOf(t, s, amendEpic); len(pending) != 0 {
		t.Errorf("pending actions once the amendment applied = %+v, want none", pending)
	}
}

func TestAmendEvidenceActionRefusedWhenItsRedHoldsAtTheBase(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	createAmendEpic(t, s)
	action := storeActionsOf(t, s, amendAction(newAmendItem()))[0]
	if _, err := s.CarryOutAction(context.Background(), action, amendAt()); err != nil {
		t.Fatalf("CarryOutAction: %v", err)
	}
	amended := storedAmendment(t, s, 1)
	judgedRun(t, s, amended.RedRun, evidence.PhaseRed, amendBase, 0, evidence.VerdictHolds)

	settle(t, s)

	stored := storedAmendment(t, s, 1)
	if stored.State != AmendmentRefused {
		t.Fatalf("amendment after a red that holds = %s, want refused", stored.State)
	}
	requireRefusedAmendAction(t, s, action, stored.Refusal)
	if item := r1Item(t, s); !reflect.DeepEqual(item, oldAmendItem()) {
		t.Errorf("R1 item after the refusal = %+v, want the old item kept", item)
	}
}

// requireRefusedAmendAction fails unless action, an amend_evidence action of R1 asking for newAmendItem, is recorded as
// refused with refusal: its row holds the refusal, its decision holds the refusal, the planner's reason and both items,
// and a pending trigger names the action and the refusal.
func requireRefusedAmendAction(t *testing.T, s *Store, action PlannerAction, refusal string) {
	t.Helper()
	if state, stored := actionState(t, s, action); state != "refused" || stored != refusal {
		t.Errorf("action = %s %q, want refused %q", state, stored, refusal)
	}
	id := fmt.Sprintf("action-%d-%d-refused", action.Turn, action.Position)
	want := "planner amend evidence of R1 refused: " + refusal + ". Its reason: " + action.Reason + "." +
		itemsText(oldAmendItem(), action.Item)
	if got := decisionNamed(t, s, amendEpic, id).Text; got != want {
		t.Errorf("decision %s = %q, want %q", id, got, want)
	}
	if state, cause := triggerRow(t, s, amendEpic, id); state != triggerPending ||
		cause != "planner amend evidence of R1 refused: "+refusal {
		t.Errorf("trigger %s = %s %q, want pending with the refusal", id, state, cause)
	}
}

// storeActionsOf stores actions as the pending actions of turn 1 of the amended epic, as storeActions does.
func storeActionsOf(t *testing.T, s *Store, actions ...EpicActionInput) []PlannerAction {
	t.Helper()
	return storeActions(t, s, amendEpic, actions...)
}

func TestAmendEvidenceActionThatChangesTheConclusionOrKindIsRefusedAtOnce(t *testing.T) {
	t.Parallel()
	conclusion := newAmendItem()
	conclusion.Conclusion = "a quiet step is quiet"
	kind := newAmendItem()
	kind.Kind, kind.Tests = evidence.KindCapture, nil
	cases := []struct {
		name    string
		item    evidence.Item
		refusal string
	}{
		{"a new conclusion", conclusion,
			"the planner may not change the conclusion of R1; only the owner amends a conclusion"},
		{"a new kind", kind, "the planner may not change the kind of R1; only the owner amends a kind"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := openEpicStore(t)
			createAmendEpic(t, s)
			action := storeActionsOf(t, s, amendAction(tc.item))[0]

			if _, err := s.CarryOutAction(context.Background(), action, amendAt()); err != nil {
				t.Fatalf("CarryOutAction: %v", err)
			}

			requireRefusedAmendAction(t, s, action, tc.refusal)
			if amendments := closeRow(t, s, amendEpic).Amendments; len(amendments) != 0 {
				t.Errorf("amendments after a refused action = %+v, want none stored", amendments)
			}
		})
	}
}

// actionParkOf returns the park planner_actions links action to.
func actionParkOf(t *testing.T, s *Store, action PlannerAction) int64 {
	t.Helper()
	var park int64
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT park FROM planner_actions WHERE epic = ? AND turn = ? AND position = ?`,
		action.Epic, action.Turn, action.Position).Scan(&park); err != nil {
		t.Fatalf("read park of action %d of epic %s: %v", action.Position, action.Epic, err)
	}
	return park
}

// parkNotBuilt parks the amended epic as not built with reason, a planner park, and returns the seq of its park row.
func parkNotBuilt(t *testing.T, s *Store, reason string) int64 {
	t.Helper()
	ctx := context.Background()
	if err := s.ParkEpicWaking(ctx, amendEpic, reason, NodeTrigger{ID: "workflow:" + reason, Cause: reason},
		amendAt()); err != nil {
		t.Fatalf("ParkEpicWaking: %v", err)
	}
	var seq int64
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(seq) FROM epic_parks WHERE epic = ?`, amendEpic).Scan(
		&seq); err != nil {
		t.Fatalf("read park seq: %v", err)
	}
	return seq
}

// TestAmendEvidenceActionKeepsThePark checks that an amend_evidence action is linked to the planner park its epic held
// when it was carried out, and keeps that link when its amendment is applied while another park is held.
func TestAmendEvidenceActionKeepsThePark(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createAmendEpic(t, s)
	first := parkNotBuilt(t, s, "epic not built: R1 is not green at the stored base")
	action := storeActionsOf(t, s, amendAction(newAmendItem()))[0]
	if _, err := s.CarryOutAction(ctx, action, amendAt()); err != nil {
		t.Fatalf("CarryOutAction: %v", err)
	}
	if got := actionParkOf(t, s, action); got != first {
		t.Fatalf("park of the pending action = %d, want the park %d it answered", got, first)
	}
	if err := s.RetryEpic(ctx, amendEpic); err != nil {
		t.Fatalf("RetryEpic: %v", err)
	}
	second := parkNotBuilt(t, s, "epic not built: R1 has no red at the epic's base")
	if second == first {
		t.Fatalf("second park = %d, want a new park row", second)
	}

	judgedRun(t, s, storedAmendment(t, s, 1).RedRun, evidence.PhaseRed, amendBase, 1, evidence.VerdictFails)
	settle(t, s)

	if state, _ := actionState(t, s, action); state != "taken" {
		t.Fatalf("action after its amendment applied = %s, want taken", state)
	}
	if got := actionParkOf(t, s, action); got != first {
		t.Errorf("park of the taken action = %d, want the park %d it answered, not the park %d held now", got, first,
			second)
	}
}

func TestSettleAmendmentRefusesARunThatIsNotTheNewItemsRed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createAmendEpic(t, s)
	amended := amendOwner(t, s, newAmendItem())
	// A run of another item stored under the red's id, judged fails, must not stand in for the new item's red.
	if err := s.AddEvidenceRun(ctx, EvidenceRun{
		ID: amended.RedRun, Epic: amendEpic, Item: "R1", Revision: amendBase, Phase: evidence.PhaseRed,
		Source: evidence.SourceCaptured, Spec: oldAmendItem(), Created: amendAt(),
	}); err != nil {
		t.Fatalf("AddEvidenceRun: %v", err)
	}
	if err := s.RecordEvidenceCapture(ctx, amended.RedRun, EvidenceCapture{Exit: 1, At: amendAt()}); err != nil {
		t.Fatalf("RecordEvidenceCapture: %v", err)
	}
	if err := s.RecordEvidenceVerdict(ctx, amended.RedRun, EvidenceVerdict{
		Verdict: evidence.VerdictFails, Passed: true, At: amendAt(),
	}); err != nil {
		t.Fatalf("RecordEvidenceVerdict: %v", err)
	}

	settle(t, s)

	want := "the run " + amended.RedRun + " is not a red run of the new item at the epic's base " + amendBase
	if stored := storedAmendment(t, s, 1); stored.State != AmendmentRefused || stored.Refusal != want {
		t.Errorf("amendment = %s %q, want refused %q", stored.State, stored.Refusal, want)
	}
	if item := r1Item(t, s); !reflect.DeepEqual(item, oldAmendItem()) {
		t.Errorf("R1 item = %+v, want the old item kept", item)
	}
}

func TestCloseSettlesOpenAmendments(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createAmendEpic(t, s)
	moveStoredBase(t, s)
	owner := amendOwner(t, s, newAmendItem())
	judgedRun(t, s, owner.RedRun, evidence.PhaseRed, amendBase, 1, evidence.VerdictFails)
	settle(t, s)
	capture := runItem()
	capture.Capture[0], capture.Kind, capture.Limit = "/usr/bin/go", evidence.KindCapture, 300
	action := storeActionsOf(t, s, EpicActionInput{
		Kind: ActionAmendEvidence, Requirement: "R3", Item: capture, Reason: "a shorter limit",
	})[0]
	if _, err := s.CarryOutAction(ctx, action, amendAt()); err != nil {
		t.Fatalf("CarryOutAction: %v", err)
	}

	if _, err := s.CloseEpic(ctx, amendEpic, "abandoned", "", CloserOwner, amendAt()); err != nil {
		t.Fatalf("CloseEpic: %v", err)
	}

	if got := storedAmendment(t, s, 1); got.State != AmendmentApplied {
		t.Errorf("capturing amendment after the close = %s, want applied", got.State)
	}
	refusal := "epic " + amendEpic + " closed as abandoned"
	if got := storedAmendment(t, s, 2); got.State != AmendmentRefused || got.Refusal != refusal {
		t.Errorf("checking amendment after the close = %s %q, want refused %q", got.State, got.Refusal, refusal)
	}
	if state, stored := actionState(t, s, action); state != "refused" || stored != refusal {
		t.Errorf("action of the checking amendment = %s %q, want refused %q", state, stored, refusal)
	}
	if open, err := s.OpenAmendments(ctx); err != nil || len(open) != 0 {
		t.Errorf("OpenAmendments after the close = %v, %v, want none", open, err)
	}
}
