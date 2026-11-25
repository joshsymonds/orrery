package store

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// ReproJudges is how many judges rule on one reproduction at most: a first, and a second after a first that gave no
// verdict, as a task's reproduction is judged.
const ReproJudges = 2

// ReproRuling is the ruling of one judge of a test item's reproduction that ran and exited 0 without timing out
// (PassedOnBase): whether the reproduction contradicts red. Check is the reproduction check's id and Judge the judge's
// number from 0. Step is the judge's step, and empty for a judge that did not start. Verdict is evidence.VerdictHolds
// when the judge ruled that the reproduction does not contradict red, evidence.VerdictFails when it ruled that it does,
// and empty for no verdict. Reason is the judge's reason, how its step ended when it gave no verdict, or why it did not
// start. Created is the second the ruling was recorded at.
type ReproRuling struct {
	Check   string
	Judge   int64
	Step    string
	Verdict string
	Reason  string
	Created time.Time
}

// PassedOnBase reports whether a reproduction check ran and exited 0 without timing out: the one outcome whose meaning a
// reproduction judge rules on. Any other exit is what red predicts, so it needs no ruling.
func PassedOnBase(check NodeCheck) bool {
	return check.State == NodeEnded && check.NotRun == "" && check.Exit == 0 && !check.TimedOut
}

// CheckRulings returns the rulings among rulings on the reproduction check, in judge order.
func CheckRulings(rulings []ReproRuling, check string) []ReproRuling {
	var on []ReproRuling
	for _, ruling := range rulings {
		if ruling.Check == check {
			on = append(on, ruling)
		}
	}
	slices.SortFunc(on, func(a, b ReproRuling) int { return cmp.Compare(a.Judge, b.Judge) })
	return on
}

// SettledRuling returns the ruling that settles a reproduction, from rulings, the rulings on it in judge order
// (CheckRulings), and whether one does: the first that gave a verdict, the first whose judge did not start, or else the
// last of ReproJudges rulings that gave none. A reproduction with fewer rulings than that and none of those is not
// settled, so its next judge starts.
func SettledRuling(rulings []ReproRuling) (ReproRuling, bool) {
	for _, ruling := range rulings {
		if ruling.Verdict != "" || ruling.Step == "" {
			return ruling, true
		}
	}
	if len(rulings) >= ReproJudges {
		return rulings[len(rulings)-1], true
	}
	return ReproRuling{}, false
}

// RulingOutcome returns what a settled ruling says of its reproduction, as a note or a cause carries it: that the judge
// ruled that the reproduction does or does not contradict red, gave no verdict twice, or did not start, then its reason
// after ": " when it has one.
func RulingOutcome(ruling ReproRuling) string {
	var outcome string
	switch {
	case ruling.Verdict == evidence.VerdictHolds:
		outcome = "the judge ruled that it does not contradict red"
	case ruling.Verdict == evidence.VerdictFails:
		outcome = "the judge ruled that it contradicts red"
	case ruling.Step == "":
		outcome = "the judge did not start"
	default:
		outcome = "the judge gave no verdict twice"
	}
	if ruling.Reason == "" {
		return outcome
	}
	return outcome + ": " + ruling.Reason
}

// RecordReproRuling stores ruling of epic in one write, created at the second at. The ruling's Created is not stored:
// at is. A ruling already stored under its epic, check and judge writes nothing, so a repeat leaves the first record as
// it is.
func (s *Store) RecordReproRuling(ctx context.Context, epic string, ruling ReproRuling, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.InsertReproRuling(ctx, sqlcgen.InsertReproRulingParams{
			Epic: epic, ReproCheck: ruling.Check, Judge: ruling.Judge, Step: ruling.Step, Verdict: ruling.Verdict,
			Reason: ruling.Reason, Created: at.Unix(),
		}); err != nil {
			return fmt.Errorf("record ruling of judge %d of reproduction %s of epic %s: %w",
				ruling.Judge, ruling.Check, epic, err)
		}
		return nil
	})
}

// ReproRulings returns the rulings on the reproductions of epic, in the order of their check and judge.
func (s *Store) ReproRulings(ctx context.Context, epic string) ([]ReproRuling, error) {
	return readReproRulings(ctx, sqlcgen.New(s.read), epic)
}

// readReproRulings returns the rulings on the reproductions of epic as queries read them, in the order of their check
// and judge.
func readReproRulings(ctx context.Context, queries *sqlcgen.Queries, epic string) ([]ReproRuling, error) {
	rows, err := queries.ListEpicReproRulings(ctx, epic)
	if err != nil {
		return nil, fmt.Errorf("list reproduction rulings of epic %s: %w", epic, err)
	}
	return mapInOrder(rows, func(row sqlcgen.ReproRuling) ReproRuling {
		return ReproRuling{
			Check: row.ReproCheck, Judge: row.Judge, Step: row.Step, Verdict: row.Verdict, Reason: row.Reason,
			Created: time.Unix(row.Created, 0),
		}
	}), nil
}
