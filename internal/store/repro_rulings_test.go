package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
)

// TestReproRulingsRoundTripThroughEpicFlow checks that recorded rulings read back from ReproRulings and from EpicFlow
// in the order of their check and judge, each with every field it was recorded with and the second it was recorded at,
// and that a repeat of a ruling under its check and judge leaves the first as it was.
func TestReproRulingsRoundTripThroughEpicFlow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createAmendEpic(t, s)
	at := amendAt()
	recorded := []ReproRuling{
		{
			Check:   "check-b",
			Judge:   0,
			Step:    "step-b0",
			Verdict: evidence.VerdictFails,
			Reason:  "TestX passes on the base",
		},
		{Check: "check-a", Judge: 1, Step: "step-a1", Verdict: evidence.VerdictHolds, Reason: "the task only renames"},
		{Check: "check-a", Judge: 0, Step: "step-a0", Reason: "overtime"},
		{Check: "check-c", Judge: 0, Reason: "the judge did not start: agent x is not defined"},
	}
	for _, ruling := range recorded {
		if err := s.RecordReproRuling(ctx, amendEpic, ruling, at); err != nil {
			t.Fatalf("RecordReproRuling %+v: %v", ruling, err)
		}
	}
	repeat := ReproRuling{Check: "check-b", Judge: 0, Step: "step-other", Verdict: evidence.VerdictHolds}
	if err := s.RecordReproRuling(ctx, amendEpic, repeat, at.Add(time.Hour)); err != nil {
		t.Fatalf("RecordReproRuling repeat: %v", err)
	}

	want := []ReproRuling{recorded[2], recorded[1], recorded[0], recorded[3]}
	for i := range want {
		want[i].Created = at
	}
	got, err := s.ReproRulings(ctx, amendEpic)
	if err != nil {
		t.Fatalf("ReproRulings: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ReproRulings() = %+v, want %+v", got, want)
	}
	flow, err := s.EpicFlow(ctx, amendEpic)
	if err != nil {
		t.Fatalf("EpicFlow: %v", err)
	}
	if !reflect.DeepEqual(flow.Rulings, want) {
		t.Errorf("EpicFlow().Rulings = %+v, want %+v", flow.Rulings, want)
	}
	if other, err := s.ReproRulings(ctx, "orrery-1a2b3c4/epic-other"); err != nil || other != nil {
		t.Errorf("ReproRulings(another epic) = %+v, %v; want none", other, err)
	}
}

func TestCheckRulingsKeepsOneCheckInJudgeOrder(t *testing.T) {
	t.Parallel()
	rulings := []ReproRuling{{Check: "a", Judge: 1}, {Check: "b", Judge: 0}, {Check: "a", Judge: 0}}
	want := []ReproRuling{{Check: "a", Judge: 0}, {Check: "a", Judge: 1}}
	if got := CheckRulings(rulings, "a"); !reflect.DeepEqual(got, want) {
		t.Errorf("CheckRulings(a) = %+v, want %+v", got, want)
	}
	if got := CheckRulings(rulings, "c"); got != nil {
		t.Errorf("CheckRulings(c) = %+v, want none", got)
	}
}

func TestSettledRuling(t *testing.T) {
	t.Parallel()
	silent := ReproRuling{Judge: 0, Step: "s0", Reason: "overtime"}
	secondSilent := ReproRuling{Judge: 1, Step: "s1", Reason: "engine_restarted"}
	fails := ReproRuling{Judge: 1, Step: "s1", Verdict: evidence.VerdictFails, Reason: "proves nothing"}
	holds := ReproRuling{Judge: 0, Step: "s0", Verdict: evidence.VerdictHolds}
	unstarted := ReproRuling{Judge: 0, Reason: "the judge did not start: refused"}
	cases := []struct {
		name    string
		rulings []ReproRuling
		want    ReproRuling
		settled bool
	}{
		{name: "no ruling settles nothing", rulings: nil, settled: false},
		{name: "a first judge with no verdict settles nothing", rulings: []ReproRuling{silent}, settled: false},
		{name: "a verdict settles", rulings: []ReproRuling{holds}, want: holds, settled: true},
		{name: "a second judge's verdict settles", rulings: []ReproRuling{silent, fails}, want: fails, settled: true},
		{
			name: "a second judge with no verdict settles as no verdict", rulings: []ReproRuling{silent, secondSilent},
			want: secondSilent, settled: true,
		},
		{name: "a judge that did not start settles", rulings: []ReproRuling{unstarted}, want: unstarted, settled: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, settled := SettledRuling(tc.rulings)
			if settled != tc.settled || !reflect.DeepEqual(got, tc.want) {
				t.Errorf("SettledRuling() = %+v, %t; want %+v, %t", got, settled, tc.want, tc.settled)
			}
		})
	}
}

func TestRulingOutcome(t *testing.T) {
	t.Parallel()
	cases := []struct {
		ruling ReproRuling
		want   string
	}{
		{
			ReproRuling{Step: "s", Verdict: evidence.VerdictHolds, Reason: "the task only renames"},
			"the judge ruled that it does not contradict red: the task only renames",
		},
		{ReproRuling{Step: "s", Verdict: evidence.VerdictFails}, "the judge ruled that it contradicts red"},
		{ReproRuling{Step: "s", Reason: "overtime"}, "the judge gave no verdict twice: overtime"},
		{ReproRuling{Reason: "refused"}, "the judge did not start: refused"},
	}
	for _, tc := range cases {
		if got := RulingOutcome(tc.ruling); got != tc.want {
			t.Errorf("RulingOutcome(%+v) = %q, want %q", tc.ruling, got, tc.want)
		}
	}
}

func TestPassedOnBase(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		check NodeCheck
		want  bool
	}{
		{"exit 0 passed", NodeCheck{State: NodeEnded}, true},
		{"exit 1 did not", NodeCheck{State: NodeEnded, Exit: 1}, false},
		{"a timeout did not", NodeCheck{State: NodeEnded, TimedOut: true}, false},
		{"a check that did not run did not", NodeCheck{State: NodeEnded, NotRun: "no grant"}, false},
		{"a running check has not", NodeCheck{State: NodeRunning}, false},
	}
	for _, tc := range cases {
		if got := PassedOnBase(tc.check); got != tc.want {
			t.Errorf("PassedOnBase(%s) = %t, want %t", tc.name, got, tc.want)
		}
	}
}

// reproRulingsDownAndUp is the walk's check of 00117_repro_rulings.sql: a ruling stored at the migration goes with the
// table its Down drops, and its Up makes the table again empty.
func reproRulingsDownAndUp() migrationCheck {
	return migrationCheck{
		seed: func(t *testing.T, s *Store) {
			t.Helper()
			ruling := ReproRuling{Check: "check-1", Step: "step-1", Verdict: evidence.VerdictFails, Reason: "nothing"}
			if err := s.RecordReproRuling(context.Background(), "repo/epic", ruling, amendAt()); err != nil {
				t.Fatalf("RecordReproRuling: %v", err)
			}
		},
		up: func(t *testing.T, s *Store) {
			t.Helper()
			var rulings int
			if err := s.db.DB.QueryRow(`SELECT COUNT(*) FROM repro_rulings`).
				Scan(&rulings); err != nil ||
				rulings != 0 {
				t.Errorf("repro_rulings after Down and Up counts %d (err %v), want none", rulings, err)
			}
		},
	}
}

// amendedReproAt moves the amended epic's stored base, applies the new test item of R1 with its red judged fails at the
// base, and ends its green at the stored base passed and its reproduction with exit. It returns the capturing
// amendment.
func amendedReproAt(t *testing.T, s *Store, exit int) EvidenceAmendment {
	t.Helper()
	ctx := context.Background()
	createAmendEpic(t, s)
	moveStoredBase(t, s)
	amended := amendOwner(t, s, newAmendItem())
	judgedRun(t, s, amended.RedRun, evidence.PhaseRed, amendBase, 1, evidence.VerdictFails)
	settle(t, s)
	capturing := storedAmendment(t, s, 1)
	judgedRun(t, s, capturing.GreenRun, evidence.PhaseGreen, "stored-2", 0, evidence.VerdictHolds)
	check := NodeCheck{
		ID: capturing.ReproCheck, Epic: amendEpic, Path: "amendment A1", Revision: "overlay", Argv: []string{"x/y"},
	}
	if err := s.AddNodeCheck(ctx, check, amendAt()); err != nil {
		t.Fatalf("AddNodeCheck: %v", err)
	}
	if err := s.EndNodeCheck(ctx, capturing.ReproCheck, NodeCheckEnd{Exit: exit}, amendAt()); err != nil {
		t.Fatalf("EndNodeCheck: %v", err)
	}
	return capturing
}

// requireJudging fails the test unless a settle of amendment A1 of the amended epic, when it is, writes nothing and
// reports ErrAmendmentJudging, leaving the amendment capturing.
func requireJudging(t *testing.T, s *Store, when string) {
	t.Helper()
	err := s.SettleAmendment(context.Background(), amendEpic, 1, amendAt())
	if !errors.Is(err, ErrAmendmentJudging) {
		t.Fatalf("SettleAmendment %s = %v, want ErrAmendmentJudging", when, err)
	}
	if got := storedAmendment(t, s, 1); got.State != AmendmentCapturing {
		t.Fatalf("amendment %s = %s, want capturing", when, got.State)
	}
}

// TestSettleAmendmentWaitsForItsReproductionsRuling checks that a capturing amendment whose reproduction exited 0
// writes nothing and reports ErrAmendmentJudging until a ruling settles that reproduction: a first judge with no
// verdict leaves it waiting, and a second judge's ruling applies it, with the ruling after the reproduction's exit in
// its trigger's cause.
func TestSettleAmendmentWaitsForItsReproductionsRuling(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	capturing := amendedReproAt(t, s, 0)
	requireJudging(t, s, "with no ruling")
	first := ReproRuling{Check: capturing.ReproCheck, Judge: 0, Step: "judge-0", Reason: "overtime"}
	if err := s.RecordReproRuling(ctx, amendEpic, first, amendAt()); err != nil {
		t.Fatalf("RecordReproRuling first: %v", err)
	}
	requireJudging(t, s, "after a first judge with no verdict")
	second := ReproRuling{
		Check: capturing.ReproCheck, Judge: 1, Step: "judge-1", Verdict: evidence.VerdictFails,
		Reason: "QuietStepRestart passes on the base: it asserts nothing the base lacks",
	}
	if err := s.RecordReproRuling(ctx, amendEpic, second, amendAt()); err != nil {
		t.Fatalf("RecordReproRuling second: %v", err)
	}

	settle(t, s)

	if got := storedAmendment(t, s, 1); got.State != AmendmentApplied {
		t.Errorf("amendment once its reproduction is ruled on = %s, want applied", got.State)
	}
	_, cause := triggerRow(t, s, amendEpic, "amend-1")
	want := "evidence of R1 amended by the owner (A1): the green run " + capturing.GreenRun + " at the stored base " +
		"stored-2 passed; its reproduction " + capturing.ReproCheck + " at the epic's base exited 0; the judge ruled " +
		"that it contradicts red: QuietStepRestart passes on the base: it asserts nothing the base lacks"
	if cause != want {
		t.Errorf("trigger amend-1 cause = %q, want %q", cause, want)
	}
}

// TestSettleAmendmentNeedsNoRulingForAReproductionThatFails checks that a reproduction that exited non-zero is what red
// predicts, so the amendment applies without a ruling, and its cause carries the exit alone.
func TestSettleAmendmentNeedsNoRulingForAReproductionThatFails(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	capturing := amendedReproAt(t, s, 1)

	settle(t, s)

	if got := storedAmendment(t, s, 1); got.State != AmendmentApplied {
		t.Errorf("amendment whose reproduction exited 1 = %s, want applied", got.State)
	}
	_, cause := triggerRow(t, s, amendEpic, "amend-1")
	want := "evidence of R1 amended by the owner (A1): the green run " + capturing.GreenRun + " at the stored base " +
		"stored-2 passed; its reproduction " + capturing.ReproCheck + " at the epic's base exited 1"
	if cause != want {
		t.Errorf("trigger amend-1 cause = %q, want %q", cause, want)
	}
}

func TestAmendmentJudgeIDIsDerivedPerJudgeAndAttempt(t *testing.T) {
	t.Parallel()
	amended := EvidenceAmendment{Epic: amendEpic, Seq: 1, GreenAttempt: 0}
	first, second := AmendmentJudgeID(amended, 0), AmendmentJudgeID(amended, 1)
	if first != AmendmentWorkID(amendEpic, 1, "repro judge 0", 0) || first == second {
		t.Errorf("judge ids %s and %s, want the derived id of each judge", first, second)
	}
	amended.GreenAttempt = 1
	if AmendmentJudgeID(amended, 0) == first {
		t.Error("judge 0 at the next green attempt has the id of the attempt before, want its own")
	}
}
