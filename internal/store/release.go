package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// The parts a release run records: the candidate the release starts from, a release step, the judge's verdict, a named
// position the owner set, a verify run, which records that origin's main was not in a candidate and so the candidate
// was prepared again, a round of the release integrator, which finished a merge of origin's commit that conflicted, a
// replan, which sends the release back to planning for a change to its candidate that a blocked round asked for, and a
// review, which records whether a release reviewer held the candidate before the first release step started from it.
const (
	ReleasePartCandidate  = "candidate"
	ReleasePartStep       = "release"
	ReleasePartJudge      = "judge"
	ReleasePartNamed      = "named"
	ReleasePartVerify     = "verify"
	ReleasePartIntegrator = "integrator"
	ReleasePartReplan     = "replan"
	ReleasePartReview     = "review"
)

// The phases an epic's release moves through: clean until it takes the release slot, releasing while it holds the
// slot, and stopped when a release step stops it. Clean is also the phase an epic whose frozen review found it clean
// ends in, and EndEpic stores the epic's stored base as its review's clean revision then.
const (
	PhaseClean     = "clean"
	PhaseReleasing = "releasing"
	PhaseStopped   = "stopped"
)

// releaseStopOutcome is the outcome a stopped release's node run parks with.
const releaseStopOutcome = "failed"

// ReleaseRun is one record of an epic's release. Part is the ReleasePart it belongs to and Position numbers it within
// its part. Attempt is the attempt of the release node run it was recorded under. Step names the release step, or for a
// review the release reviewer that ran it, Start, Head and End are the revisions it recorded, and Commits the commits it
// made. Evidence names the evidence it ran, and Passed and Reason say whether it passed and why not. At is the time it
// was recorded, stored at whole seconds.
type ReleaseRun struct {
	Part     string
	Position int64
	Attempt  int64
	Step     string
	Start    string
	Head     string
	End      string
	Commits  []string
	Evidence string
	Passed   bool
	Reason   string
	At       time.Time
}

// RecordRelease stores run as a release run of epic, in one write. A run with the same part, position and attempt as
// one stored already changes nothing, so a repeat is a no-op.
func (s *Store) RecordRelease(ctx context.Context, epic string, run ReleaseRun) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		return recordRelease(ctx, queries, epic, run)
	})
}

// recordRelease stores run as a release run of epic in the caller's write, as RecordRelease does.
func recordRelease(ctx context.Context, queries *sqlcgen.Queries, epic string, run ReleaseRun) error {
	commits, err := encodeList(run.Commits)
	if err != nil {
		return fmt.Errorf("encode commits of release run %s of epic %s: %w", run.Part, epic, err)
	}
	if err = queries.InsertEpicReleaseRun(ctx, sqlcgen.InsertEpicReleaseRunParams{
		Epic: epic, Part: run.Part, Position: run.Position, Attempt: run.Attempt, Step: run.Step,
		Start: run.Start, Head: run.Head, Finish: run.End, Commits: commits, Evidence: run.Evidence,
		Passed: run.Passed, Reason: run.Reason, At: run.At.Unix(),
	}); err != nil {
		return fmt.Errorf("insert release run %s of epic %s: %w", run.Part, epic, err)
	}
	return nil
}

// RecordCandidate stores run as a candidate run of epic, in one write, at the next position among the candidate runs epic
// holds at run's attempt. A candidate prepared again at the attempt of the one it replaces is numbered after it, and is
// not dropped as a repeat of its key, as RecordRelease would drop it.
func (s *Store) RecordCandidate(ctx context.Context, epic string, run ReleaseRun) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		run.Part = ReleasePartCandidate
		return appendRelease(ctx, queries, epic, run)
	})
}

// MarkCandidateStale records, in one write, that origin's main is not in the candidate head of the running release run
// at path and attempt of epic. It stores a verify run of that attempt that did not pass, with reason, and clears the
// run's revision and result, so the run prepares its candidate again at this attempt. A run that is not running changes
// nothing, and so records nothing.
func (s *Store) MarkCandidateStale(
	ctx context.Context, epic, path string, attempt int64, head, reason string, at time.Time,
) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		cleared, err := queries.ClearEpicNodeRevision(ctx, sqlcgen.ClearEpicNodeRevisionParams{
			Epic: epic, Path: path, Attempt: attempt,
		})
		if err != nil {
			return fmt.Errorf("clear revision of node run %s of epic %s: %w", path, epic, err)
		}
		if cleared == 0 {
			return nil
		}
		return appendRelease(ctx, queries, epic, ReleaseRun{
			Part: ReleasePartVerify, Attempt: attempt, Head: head, Reason: reason, At: at,
		})
	})
}

// appendRelease stores run as a release run of epic in the caller's write, at the next position of its part among the
// release runs epic holds at run's attempt. A candidate and a mark of an attempt are stored this way, so a second one of
// the attempt is numbered after the first and is not dropped as a repeat of its key.
func appendRelease(ctx context.Context, queries *sqlcgen.Queries, epic string, run ReleaseRun) error {
	stored, err := readReleaseRuns(ctx, queries, epic)
	if err != nil {
		return err
	}
	run.Position = 0
	for _, prior := range stored {
		if prior.Part == run.Part && prior.Attempt == run.Attempt {
			run.Position++
		}
	}
	return recordRelease(ctx, queries, epic, run)
}

// TakeReleaseSlot takes the release slot of epic's repository for epic, in one write, and returns the slot's holder
// after the write. An epic in clean, releasing or stopped takes the slot when it is free, or keeps it when it already
// holds it, and only a clean epic that holds it moves to releasing. When another epic holds the slot nothing changes
// and that epic is returned. An epic in any other phase takes nothing. It returns an error matching ErrNoEpic when no
// epic has the id.
func (s *Store) TakeReleaseSlot(ctx context.Context, epic string, at time.Time) (string, error) {
	var holder string
	err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		row, err := queries.GetEpic(ctx, epic)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("epic %s: %w", epic, ErrNoEpic)
		}
		if err != nil {
			return fmt.Errorf("get epic %s: %w", epic, err)
		}
		if takesReleaseSlot(row.Phase) {
			if err = queries.InsertEpicReleaseSlot(ctx, sqlcgen.InsertEpicReleaseSlotParams{
				Repository: row.Repository, Epic: epic,
			}); err != nil {
				return fmt.Errorf("take release slot of epic %s: %w", epic, err)
			}
		}
		if holder, err = releaseSlotHolder(ctx, queries, row.Repository); err != nil {
			return err
		}
		if holder == epic && row.Phase == PhaseClean {
			if err = queries.StartEpicRelease(
				ctx,
				sqlcgen.StartEpicReleaseParams{PhaseAt: at.Unix(), ID: epic},
			); err != nil {
				return fmt.Errorf("start release of epic %s: %w", epic, err)
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return holder, nil
}

// takesReleaseSlot reports whether an epic in phase may take the release slot of its repository.
func takesReleaseSlot(phase string) bool {
	return phase == PhaseClean || phase == PhaseReleasing || phase == PhaseStopped
}

// releaseSlotHolder returns the epic that holds the release slot of repository, "outside:" and the label of an outside
// holder, or "" when the slot is free.
func releaseSlotHolder(ctx context.Context, queries *sqlcgen.Queries, repository string) (string, error) {
	row, err := queries.GetEpicReleaseSlotHolder(ctx, repository)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read release slot of %s: %w", repository, err)
	}
	if row.HolderKind == ReleaseSlotOutside {
		return "outside:" + row.Label, nil
	}
	return row.Epic, nil
}

// FreeReleaseSlot frees the release slot epic holds, in one write. A slot another epic holds is left as it is.
func (s *Store) FreeReleaseSlot(ctx context.Context, epic string) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.FreeEpicReleaseSlot(ctx, epic); err != nil {
			return fmt.Errorf("free release slot of epic %s: %w", epic, err)
		}
		return nil
	})
}

// StopRelease stops the release of epic at the running node run at path and attempt, in one write. The run parks with
// outcome failed and reason as its output, the epic parks with reason as the class releaseStopClass gives, and the epic
// moves from releasing to stopped. A run at another attempt and a run that is not running change nothing and add no
// tell, so a repeat is a no-op.
func (s *Store) StopRelease(ctx context.Context, epic, path string, attempt int64, reason string, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		parked, err := queries.ParkEpicNodeRun(ctx, sqlcgen.ParkEpicNodeRunParams{
			Outcome: releaseStopOutcome, Output: capNodeOutput(reason), Epic: epic, Path: path, Attempt: attempt,
		})
		if err != nil {
			return fmt.Errorf("park node run %s of epic %s: %w", path, epic, err)
		}
		if parked == 0 {
			return nil
		}
		class, err := releaseStopClass(ctx, queries, epic, attempt)
		if err != nil {
			return err
		}
		return stopEpicRelease(ctx, queries, epic, class, reason, at)
	})
}

// releaseStopClass returns the class of the park a release of epic stopped at attempt takes, from the store's rows. It is
// release_owner_stop when epic holds no release slot or a release reviewer refused its candidate at attempt,
// release_undo_none when the release step that failed at attempt has undo none, and release_step otherwise.
func releaseStopClass(ctx context.Context, queries *sqlcgen.Queries, epic string, attempt int64) (ParkClass, error) {
	head, err := epicHead(ctx, queries, epic)
	if err != nil {
		return "", err
	}
	holder, err := releaseSlotHolder(ctx, queries, head.Repository)
	if err != nil {
		return "", err
	}
	if holder != epic {
		return ParkClassReleaseOwnerStop, nil
	}
	runs, err := readReleaseRuns(ctx, queries, epic)
	if err != nil {
		return "", err
	}
	if slices.ContainsFunc(runs, func(run ReleaseRun) bool {
		return run.Part == ReleasePartReview && run.Attempt == attempt && !run.Passed
	}) {
		return ParkClassReleaseOwnerStop, nil
	}
	failed := slices.IndexFunc(runs, func(run ReleaseRun) bool {
		return run.Part == ReleasePartStep && run.Attempt == attempt && !run.Passed
	})
	if failed < 0 {
		return ParkClassReleaseStep, nil
	}
	steps, err := queries.ListEpicReleaseSteps(ctx, epic)
	if err != nil {
		return "", fmt.Errorf("list release steps of epic %s: %w", epic, err)
	}
	for _, step := range steps {
		if step.Position == runs[failed].Position && isUndoNone(step.Undo) {
			return ParkClassReleaseUndoNone, nil
		}
	}
	return ParkClassReleaseStep, nil
}

// releaseParkSubject is the subject of the park a stopped release makes: the release, whatever its node path.
const releaseParkSubject = "release"

// stopEpicRelease parks epic with reason as class, then moves epic from releasing to stopped, in the caller's write.
func stopEpicRelease(
	ctx context.Context, queries *sqlcgen.Queries, epic string, class ParkClass, reason string, at time.Time,
) error {
	if err := parkEpicAs(ctx, queries, epic, class, releaseParkSubject, reason, at); err != nil {
		return err
	}
	if err := queries.StopEpicRelease(withEpicEvent(ctx), sqlcgen.StopEpicReleaseParams{
		PhaseAt: at.Unix(), ID: epic,
	}); err != nil {
		return fmt.Errorf("stop release of epic %s: %w", epic, err)
	}
	return nil
}

// readReleaseRuns returns the release runs of epic as queries read them, ordered by attempt, part and position.
func readReleaseRuns(ctx context.Context, queries *sqlcgen.Queries, epic string) ([]ReleaseRun, error) {
	stored, err := queries.ListEpicReleaseRuns(ctx, epic)
	if err != nil {
		return nil, fmt.Errorf("list release runs of epic %s: %w", epic, err)
	}
	var runs []ReleaseRun
	for _, row := range stored {
		var commits []string
		if commits, err = decodeList[string](row.Commits); err != nil {
			return nil, fmt.Errorf("decode commits of release run %s of epic %s: %w", row.Part, epic, err)
		}
		runs = append(runs, ReleaseRun{
			Part: row.Part, Position: row.Position, Attempt: row.Attempt, Step: row.Step, Start: row.Start,
			Head: row.Head, End: row.Finish, Commits: commits, Evidence: row.Evidence, Passed: row.Passed,
			Reason: row.Reason, At: time.Unix(row.At, 0),
		})
	}
	return runs, nil
}

// The states a release step reads as in its ReleaseProof.
const (
	ReleaseWaiting = "waiting"
	ReleaseRunning = "running"
	ReleasePassed  = "passed"
	ReleaseFailed  = "failed"
)

// ReleaseProof is one contract release step of an epic with the state its stored release runs give it. Position
// numbers the step from 1 in contract order, and Step, Target and Undo are the step as the contract names it. State
// is one of the ReleaseWaiting, ReleaseRunning, ReleasePassed and ReleaseFailed constants. Evidence, Reason, Attempt
// and At are those of the run that decides the state, and are empty and zero when no run does.
type ReleaseProof struct {
	Position                                    int64
	Step, Target, Undo, State, Evidence, Reason string
	Attempt                                     int64
	At                                          time.Time
}

// ReleaseProofs returns each contract release step of flow's epic in contract order, with the state its release runs
// give it. For a position p, the step passed when a release run of part `release` at p passed, the latest passed run
// deciding. Otherwise it is running when the running release node run has a work step for `release p` with no end. Its
// work step is the one under the run's id, attempt and part. Otherwise it failed when a release run at p did not pass,
// the highest-attempt run deciding. Otherwise it is waiting.
func ReleaseProofs(flow EpicFlow) []ReleaseProof {
	proofs := make([]ReleaseProof, 0, len(flow.Epic.ReleaseSteps))
	for i, step := range flow.Epic.ReleaseSteps {
		proofs = append(proofs, releaseProofOf(flow, int64(i+1), step))
	}
	return proofs
}

// releaseProofOf decides the proof of the release step at position of flow's epic, as ReleaseProofs describes.
func releaseProofOf(flow EpicFlow, position int64, step EpicReleaseStep) ReleaseProof {
	proof := ReleaseProof{
		Position: position, Step: step.Step, Target: step.Target, Undo: step.Undo, State: ReleaseWaiting,
	}
	runs := releaseStepRuns(flow.Releases, position)
	if run, ok := highestAttempt(runs, func(r ReleaseRun) bool { return r.Passed }); ok {
		return decidedProof(proof, ReleasePassed, run)
	}
	if runningStep(flow, position) {
		proof.State = ReleaseRunning
		return proof
	}
	if run, ok := highestAttempt(runs, func(r ReleaseRun) bool { return !r.Passed }); ok {
		return decidedProof(proof, ReleaseFailed, run)
	}
	return proof
}

// releaseStepRuns returns the release runs of part `release` at position, in the order of runs.
func releaseStepRuns(runs []ReleaseRun, position int64) []ReleaseRun {
	var matched []ReleaseRun
	for _, run := range runs {
		if run.Part == ReleasePartStep && run.Position == position {
			matched = append(matched, run)
		}
	}
	return matched
}

// highestAttempt returns the run of runs that keep accepts with the highest attempt, and whether one is accepted.
func highestAttempt(runs []ReleaseRun, keep func(ReleaseRun) bool) (ReleaseRun, bool) {
	var best ReleaseRun
	var found bool
	for _, run := range runs {
		if keep(run) && (!found || run.Attempt > best.Attempt) {
			best, found = run, true
		}
	}
	return best, found
}

// runningStep reports whether a running release node run of flow's epic has the work step for `release position` at
// its attempt, and that step has no end yet.
func runningStep(flow EpicFlow, position int64) bool {
	label := fmt.Sprintf("release %d", position)
	for _, run := range flow.Runs {
		if run.Kind != releaseKind || run.State != NodeRunning {
			continue
		}
		id := NodeWorkID(run.ID, run.Attempt, label)
		for _, step := range flow.Steps {
			if step.ID == id && step.End == "" {
				return true
			}
		}
	}
	return false
}

// decidedProof returns proof in state, with the evidence, reason, attempt and time of run, the run that decides it.
func decidedProof(proof ReleaseProof, state string, run ReleaseRun) ReleaseProof {
	proof.State = state
	proof.Evidence = run.Evidence
	proof.Reason = run.Reason
	proof.Attempt = run.Attempt
	proof.At = run.At
	return proof
}
