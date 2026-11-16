package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// phaseReviewing is the phase an epic is in while its end-of-epic review runs, from the freeze of its revision.
const phaseReviewing = "reviewing"

// Review is an epic's end-of-epic review. The zero Review is an epic whose review has not started.
type Review struct {
	Frozen   string          // the revision the review froze; "" before it starts
	Clean    string          // the revision the epic ended clean at; "" otherwise
	Verdicts []ReviewVerdict // by Position
	Ledger   []LedgerFinding // by Position, each with its Closures by Round
	Rounds   []ReviewRound   // by Round
	Holds    []string        // ids of the stopped review steps that paused the epic, in id order
}

// ReviewVerdict is one reviewer's verdict in a review.
type ReviewVerdict struct {
	Position        int64  // 1-based place of the reviewer among the epic's enabled epic-stage reviewers
	Reviewer, Step  string // reviewer name; the step that gave the verdict (the last try's step for a gap)
	Verdict, Reason string // holds | fails | gap
}

// LedgerFinding is one finding a reviewer returned, as the review's ledger holds it.
type LedgerFinding struct {
	ID                                            string // "<reviewer>-<k>", k the 1-based finding position
	Position                                      int64  // 1-based place in the ledger
	Reviewer, Finding                             string // reviewer name; the finding id the reviewer returned
	Claim, Citation, Location, Evidence, VerifyBy string
	// Step is the verifier step, "" when unverified. Result is confirmed, dropped or undetermined, and Reason says why.
	Step, Result, Reason string
	Unverified           bool // verify off: Result confirmed, Step ""
	Closures             []Closure
}

// Closure is the closure verifier's result for one ledger finding in one closure round.
type Closure struct {
	Finding              string // ledger id
	Round                int64
	Step, Result, Reason string // closed | open | undetermined
}

// ReviewRound is one closure round of a review: the correction tasks it ran and how its closure run ended.
type ReviewRound struct {
	Round    int64
	Revision string  // the stored base the round's closure verifiers ran at
	Tasks    []int64 // the round's correction tasks, seq order
	Outcome  string  // passed | failed (the closure run's outcome)
}

// EpicReview returns the review of epic with its verdicts, ledger, rounds and holds, all read in one read transaction,
// so they describe one state of the store. An epic whose review has not started returns the zero Review.
func (s *Store) EpicReview(ctx context.Context, epic string) (Review, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Review{}, fmt.Errorf("begin review read: %w", err)
	}
	review, err := readReview(ctx, sqlcgen.New(tx), epic)
	if err != nil {
		return Review{}, errors.Join(err, tx.Rollback())
	}
	if err = tx.Commit(); err != nil {
		return Review{}, fmt.Errorf("end review read: %w", err)
	}
	return review, nil
}

// readReview returns the review of epic as queries read it, and the zero Review when the epic has none.
func readReview(ctx context.Context, queries *sqlcgen.Queries, epic string) (Review, error) {
	head, err := queries.GetEpicReview(ctx, epic)
	if errors.Is(err, sql.ErrNoRows) {
		return Review{}, nil
	}
	if err != nil {
		return Review{}, fmt.Errorf("get review of epic %s: %w", epic, err)
	}
	review := Review{Frozen: head.Frozen, Clean: head.Clean}
	if review.Verdicts, err = readReviewVerdicts(ctx, queries, epic); err != nil {
		return Review{}, err
	}
	if review.Ledger, err = readLedger(ctx, queries, epic); err != nil {
		return Review{}, err
	}
	if review.Rounds, err = readReviewRounds(ctx, queries, epic); err != nil {
		return Review{}, err
	}
	if review.Holds, err = readReviewHolds(ctx, queries, epic); err != nil {
		return Review{}, err
	}
	return review, nil
}

// readReviewVerdicts returns the verdicts of epic as queries read them, by position.
func readReviewVerdicts(ctx context.Context, queries *sqlcgen.Queries, epic string) ([]ReviewVerdict, error) {
	stored, err := queries.ListEpicReviewVerdicts(ctx, epic)
	if err != nil {
		return nil, fmt.Errorf("list verdicts of epic %s: %w", epic, err)
	}
	return mapInOrder(stored, func(v sqlcgen.EpicReviewVerdict) ReviewVerdict {
		return ReviewVerdict{
			Position: v.Position, Reviewer: v.Reviewer, Step: v.Step, Verdict: v.Verdict, Reason: v.Reason,
		}
	}), nil
}

// readLedger returns the ledger of epic as queries read it, by position, each finding with its closures by round.
func readLedger(ctx context.Context, queries *sqlcgen.Queries, epic string) ([]LedgerFinding, error) {
	stored, err := queries.ListEpicLedger(ctx, epic)
	if err != nil {
		return nil, fmt.Errorf("list ledger of epic %s: %w", epic, err)
	}
	storedClosures, err := queries.ListEpicClosures(ctx, epic)
	if err != nil {
		return nil, fmt.Errorf("list closures of epic %s: %w", epic, err)
	}
	closures := map[string][]Closure{}
	for _, c := range storedClosures {
		closures[c.Finding] = append(closures[c.Finding], Closure{
			Finding: c.Finding, Round: c.Round, Step: c.Step, Result: c.Result, Reason: c.Reason,
		})
	}
	ledger := mapInOrder(stored, ledgerFindingOf)
	for i := range ledger {
		ledger[i].Closures = closures[ledger[i].ID]
	}
	return ledger, nil
}

// ledgerFindingOf returns the ledger finding a stored row holds, without its closures.
func ledgerFindingOf(l sqlcgen.EpicLedger) LedgerFinding {
	return LedgerFinding{
		ID: l.ID, Position: l.Position, Reviewer: l.Reviewer, Finding: l.Finding, Claim: l.Claim,
		Citation: l.Citation, Location: l.Location, Evidence: l.Evidence, VerifyBy: l.VerifyBy, Step: l.Step,
		Result: l.Result, Unverified: l.Unverified != 0, Reason: l.Reason,
	}
}

// readReviewRounds returns the closure rounds of epic as queries read them, by round, each with its tasks in seq order.
func readReviewRounds(ctx context.Context, queries *sqlcgen.Queries, epic string) ([]ReviewRound, error) {
	stored, err := queries.ListEpicReviewRounds(ctx, epic)
	if err != nil {
		return nil, fmt.Errorf("list rounds of epic %s: %w", epic, err)
	}
	var rounds []ReviewRound
	for _, r := range stored {
		tasks, decodeErr := decodeList[int64](r.Tasks)
		if decodeErr != nil {
			return nil, fmt.Errorf("decode tasks of round %d of epic %s: %w", r.Round, epic, decodeErr)
		}
		rounds = append(rounds, ReviewRound{Round: r.Round, Revision: r.Revision, Tasks: tasks, Outcome: r.Outcome})
	}
	return rounds, nil
}

// readReviewHolds returns the ids of the review steps that paused epic, in id order.
func readReviewHolds(ctx context.Context, queries *sqlcgen.Queries, epic string) ([]string, error) {
	stored, err := queries.ListEpicReviewHolds(ctx, epic)
	if err != nil {
		return nil, fmt.Errorf("list holds of epic %s: %w", epic, err)
	}
	return mapInOrder(stored, func(h sqlcgen.EpicReviewHold) string { return h.Step }), nil
}

// FreezeReview freezes the review of epic at revision, gives the running run at path of epic at attempt the frozen
// revision and sets the epic's phase to reviewing, in one write. The frozen revision is stored once, so a stored one
// wins over revision. A run that has a revision, a run at another attempt and a run that is not running change nothing,
// so the review is frozen once.
func (s *Store) FreezeReview(ctx context.Context, epic, path string, attempt int64, revision string) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		frozen, err := frozenRevision(ctx, queries, epic, revision)
		if err != nil {
			return err
		}
		frozeRun, err := queries.FreezeEpicNodeRun(ctx, sqlcgen.FreezeEpicNodeRunParams{
			Revision: frozen, Epic: epic, Path: path, Attempt: attempt,
		})
		if err != nil {
			return fmt.Errorf("freeze node run %s of epic %s: %w", path, epic, err)
		}
		if frozeRun == 0 {
			return nil
		}
		if err = queries.InsertEpicReview(ctx, sqlcgen.InsertEpicReviewParams{Epic: epic, Frozen: frozen}); err != nil {
			return fmt.Errorf("insert review of epic %s: %w", epic, err)
		}
		return setEpicPhase(ctx, queries, epic, phaseReviewing)
	})
}

// frozenRevision returns the revision the review of epic froze at, or revision when the review has not frozen yet.
func frozenRevision(ctx context.Context, queries *sqlcgen.Queries, epic, revision string) (string, error) {
	stored, err := queries.GetEpicReview(ctx, epic)
	if errors.Is(err, sql.ErrNoRows) {
		return revision, nil
	}
	if err != nil {
		return "", fmt.Errorf("read review of epic %s: %w", epic, err)
	}
	return stored.Frozen, nil
}
