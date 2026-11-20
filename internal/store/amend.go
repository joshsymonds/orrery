package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// The amenders of an evidence amendment: the owner, through AmendEvidence, which Epics.AmendEvidence calls, or the
// planner, through an amend_evidence action.
const (
	AmenderOwner   = "owner"
	AmenderPlanner = "planner"
)

// The states of an evidence amendment. An amendment is checking while the red run of its new item at the epic's base is
// judged, refused when that run, or the epic, refuses it, capturing once its new item is applied and its green run and
// reproduction run at the stored base, and applied once it is done.
const (
	AmendmentChecking  = "checking"
	AmendmentCapturing = "capturing"
	AmendmentApplied   = "applied"
	AmendmentRefused   = "refused"
)

// The parts of an amendment's work, which AmendmentWorkID names: the red run of the new item at the epic's base, and the
// green run and reproduction at the stored base.
const (
	AmendmentRed   = "red"
	AmendmentGreen = "green"
	AmendmentRepro = "repro"
)

// amendmentNamespace starts the parts every amendment work id hashes.
const amendmentNamespace = "orrery-amendment"

// amendmentReruns is how many times an amendment runs a part of its work again after a run that a restart lost or that
// exited for its environment: store.EnvironmentReruns, as the integrate kind's captures rerun.
const amendmentReruns = EnvironmentReruns

// ErrAmendmentDeferred reports that an amendment whose red run passed is not applied yet, because an evidence,
// integrate or sync node run of its epic has its work under way, or its release captures a merge of its release
// integrator. That work captures the Requirement's item under ids the item does not name, so the item stays as it is
// until the work ends, and the amendment is settled again on a later pass.
var ErrAmendmentDeferred = errors.New("amendment waits for an evidence, integrate, sync or release capture under way")

// Amendment is what an amendment asks for: the Requirement it amends, by its id, the evidence item it asks the
// Requirement to hold, and why.
type Amendment struct {
	Requirement string
	Item        evidence.Item
	Reason      string
}

// EvidenceAmendment is one stored amendment of a Requirement's evidence item: the epic and its seq there, the Requirement,
// its amender, the turn and 1-based position of the planner action that asked for it (0 for both otherwise), the item the
// Requirement held when it was asked for and the item it asks for, the reason, the epic's base, and its state. RedRun is
// the id of the red run of New at Base at RedAttempt, which an amendment that keeps the conclusion and kind never runs.
// StoredBase is the stored base the green run GreenRun, and for a test item the reproduction check ReproCheck, run at,
// at GreenAttempt; all three are empty while the amendment is checking, and for an amendment applied with no green run.
// Refusal is why a refused amendment was refused. Settled is when it left checking, and zero while it is checking.
type EvidenceAmendment struct {
	Epic         string
	Seq          int64
	Requirement  string
	Amender      string
	Turn         int64
	Position     int64
	Prior        evidence.Item
	Item         evidence.Item
	Reason       string
	Base         string
	State        string
	RedAttempt   int64
	RedRun       string
	StoredBase   string
	GreenAttempt int64
	GreenRun     string
	ReproCheck   string
	Refusal      string
	Created      time.Time
	Settled      time.Time
}

// amendStart is an amendment as it starts: its amender, the turn and position of the action that asks for it, and what
// it asks for.
type amendStart struct {
	amender        string
	turn, position int64
	Amendment
}

// AmendmentWorkID returns the id of part of the work of amendment seq of epic at attempt: the first 32 hex digits of the
// lowercase SHA-256 of `orrery-amendment`, the epic, the seq, the part and the attempt, joined with NUL bytes. The same
// inputs give the same id, so the work started again under its id is that work.
func AmendmentWorkID(epic string, seq int64, part string, attempt int64) string {
	return nodeHash(amendmentNamespace, epic, strconv.FormatInt(seq, 10), part, strconv.FormatInt(attempt, 10))
}

// AmendEvidence stores, in one write, the owner's amendment of the evidence item of a Requirement of epic, and returns
// it. An amend_evidence action makes the same write by the planner (see CarryOutAction). An amendment that keeps the
// Requirement's conclusion and kind, changing only how it is shown, applies in that write (see applyUnchanged). Any other
// amendment starts checking: the item does not change until the red run of the new item at the epic's base is judged not
// to hold (see SettleAmendment). An unknown epic is ErrNoEpic. An amendment the store refuses is a RefusalError and
// writes nothing; startAmendment gives the refusals.
func (s *Store) AmendEvidence(
	ctx context.Context,
	epic string,
	amend Amendment,
	at time.Time,
) (EvidenceAmendment, error) {
	var stored EvidenceAmendment
	err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		started, err := startAmendment(ctx, queries, epic, amendStart{amender: AmenderOwner, Amendment: amend}, at)
		if err != nil {
			return err
		}
		stored, err = applyUnchanged(ctx, queries, started, at)
		return err
	})
	if err != nil {
		return EvidenceAmendment{}, err
	}
	return stored, nil
}

// startAmendment stores start as the next amendment of epic in the caller's write, checking, with the item the
// Requirement holds as its old item, the epic's base as its base, and its red run named for attempt 0, and returns it. The
// caller applies it when it changes neither the conclusion nor the kind (see applyUnchanged). A refusal is a RefusalError
// and writes nothing: an epic in an ended phase, an epic with no base, a Requirement the epic does not hold, and the
// refusals amendmentRefusal gives.
func startAmendment(
	ctx context.Context, queries *sqlcgen.Queries, epic string, start amendStart, at time.Time,
) (EvidenceAmendment, error) {
	head, err := epicHead(ctx, queries, epic)
	if err != nil {
		return EvidenceAmendment{}, err
	}
	if endedPhase(head.Phase) {
		return EvidenceAmendment{}, RefusalError(fmt.Sprintf(
			"epic %s is %s, an ended phase, and takes no evidence amendment", epic, head.Phase))
	}
	if head.Base == "" {
		return EvidenceAmendment{}, RefusalError(fmt.Sprintf("epic %s has no base to check a red run at", epic))
	}
	requirement, err := epicRequirement(ctx, queries, epic, start.Requirement)
	if err != nil {
		return EvidenceAmendment{}, err
	}
	refusal, err := amendmentRefusal(ctx, queries, epic, requirement, start)
	if err != nil {
		return EvidenceAmendment{}, err
	}
	if refusal != "" {
		return EvidenceAmendment{}, RefusalError(refusal)
	}
	highest, err := queries.MaxEpicAmendmentSeq(ctx, epic)
	if err != nil {
		return EvidenceAmendment{}, fmt.Errorf("read amendments of epic %s: %w", epic, err)
	}
	seq := highest + 1
	priorItem, err := encodeItem(requirement.Item)
	if err != nil {
		return EvidenceAmendment{}, fmt.Errorf("old item of amendment %d of epic %s: %w", seq, epic, err)
	}
	amendedItem, err := encodeItem(start.Item)
	if err != nil {
		return EvidenceAmendment{}, fmt.Errorf("new item of amendment %d of epic %s: %w", seq, epic, err)
	}
	if err = queries.InsertEpicAmendment(ctx, sqlcgen.InsertEpicAmendmentParams{
		Epic: epic, Seq: seq, Requirement: requirement.ID, Amender: start.amender, Turn: start.turn,
		Position: start.position, PriorItem: priorItem, Item: amendedItem, Reason: start.Reason, Base: head.Base,
		RedRun: AmendmentWorkID(epic, seq, AmendmentRed, 0), Created: at.Unix(),
	}); err != nil {
		return EvidenceAmendment{}, fmt.Errorf("insert amendment %d of epic %s: %w", seq, epic, err)
	}
	return readAmendment(ctx, queries, epic, seq)
}

// epicRequirement returns the Requirement of epic named id as queries read it. A Requirement the epic does not hold is a
// RefusalError.
func epicRequirement(ctx context.Context, queries *sqlcgen.Queries, epic, id string) (EpicRequirement, error) {
	stored, err := queries.ListEpicRequirements(ctx, epic)
	if err != nil {
		return EpicRequirement{}, fmt.Errorf("list requirements of epic %s: %w", epic, err)
	}
	for _, row := range stored {
		if row.ID == id {
			return requirementOf(row)
		}
	}
	return EpicRequirement{}, RefusalError(fmt.Sprintf("epic %s has no Requirement %q", epic, id))
}

// amendmentRefusal returns why start may not amend requirement of epic, or "" when it may: a Requirement dropped as
// already met at the base, an item evidence.Check refuses, with each of its lines, an item the Requirement already holds,
// whatever empty lists it gives as null or [], a planner amendment that changes the conclusion or the kind, which only the
// owner may change, a Requirement with an amendment that is checking or capturing, and an amendment that keeps the
// conclusion and kind while evidence work of the epic is under way. A planner that changed the kind could drop what the
// kind checks, such as the reproduction of a test item. An amendment that keeps them applies in the write that stores it,
// so it is refused rather than deferred while that work runs: its new item would leave the stored runs of the work stale.
func amendmentRefusal(
	ctx context.Context, queries *sqlcgen.Queries, epic string, requirement EpicRequirement, start amendStart,
) (string, error) {
	id := requirement.ID
	if requirement.DroppedBy != 0 {
		return fmt.Sprintf("%s was dropped as already met at the epic's base by decision %d", id,
			requirement.DroppedBy), nil
	}
	if lines := evidence.Check(start.Item); len(lines) > 0 {
		return "item: " + strings.Join(lines, "; "), nil
	}
	if evidence.Current(listsAsNull(requirement.Item), listsAsNull(start.Item)) {
		return fmt.Sprintf("%s already holds this item", id), nil
	}
	if start.amender == AmenderPlanner && start.Item.Conclusion != requirement.Item.Conclusion {
		return fmt.Sprintf(
			"the planner may not change the conclusion of %s; only the owner amends a conclusion",
			id,
		), nil
	}
	if start.amender == AmenderPlanner && start.Item.Kind != requirement.Item.Kind {
		return fmt.Sprintf("the planner may not change the kind of %s; only the owner amends a kind", id), nil
	}
	open, err := queries.CountOpenRequirementAmendments(ctx, sqlcgen.CountOpenRequirementAmendmentsParams{
		Epic: epic, Requirement: id,
	})
	if err != nil {
		return "", fmt.Errorf("count open amendments of %s of epic %s: %w", id, epic, err)
	}
	if open > 0 {
		return fmt.Sprintf("%s has an amendment that is not settled yet", id), nil
	}
	if claimChanged(requirement.Item, start.Item) {
		return "", nil
	}
	working, err := queries.CountWorkingEvidenceNodeRuns(ctx, epic)
	if err != nil {
		return "", fmt.Errorf("count working evidence runs of epic %s: %w", epic, err)
	}
	if working > 0 {
		return fmt.Sprintf("%s keeps its conclusion and kind, so it applies at once, which waits for the evidence, "+
			"integrate, sync or release run under way to end", id), nil
	}
	return "", nil
}

// claimChanged reports whether item changes what the Requirement claims, its conclusion or its kind, from prior, the item
// it holds. Only such a change needs its red run at the epic's base. How the Requirement is shown, its capture, limit,
// artifacts, supplied files, judge and tests, may change with no red run.
func claimChanged(prior, item evidence.Item) bool {
	return item.Conclusion != prior.Conclusion || item.Kind != prior.Kind
}

// applyUnchanged applies amendment, stored in the caller's write, when it keeps the conclusion and kind of the Requirement,
// since such an amendment needs no red run at the base, and returns it as it then stands. An amendment that changes either
// is returned checking, for its red run to settle it (see SettleAmendment).
func applyUnchanged(
	ctx context.Context, queries *sqlcgen.Queries, amendment EvidenceAmendment, at time.Time,
) (EvidenceAmendment, error) {
	if claimChanged(amendment.Prior, amendment.Item) {
		return amendment, nil
	}
	if err := applyAmendment(ctx, queries, amendment, unchangedProof, at); err != nil {
		return EvidenceAmendment{}, err
	}
	return readAmendment(ctx, queries, amendment.Epic, amendment.Seq)
}

// unchangedProof is the account a decision gives of why an amendment that keeps the conclusion and kind applies with no
// red run.
const unchangedProof = " It keeps the conclusion and kind, so it needs no red run at the epic's base."

// startPlannedAmendment starts the amendment action asks for, an amend_evidence action, in one write, as AmendEvidence
// starts one, by the planner, keeping the action's turn and position on it. The same write links the action to the park
// it answers, as any action is linked when it is carried out, and then applies the amendment when it keeps the conclusion
// and kind, which a planner's amendment must (see applyUnchanged), so the action is taken in that write. A planner
// amendment that would change them is refused. An action whose amendment is stored already changes nothing. A refusal is
// recorded as CarryOutAction records any refusal of an action, and the amendment is not stored.
func (s *Store) startPlannedAmendment(ctx context.Context, action PlannerAction, at time.Time) error {
	err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		_, err := queries.GetActionAmendment(ctx, sqlcgen.GetActionAmendmentParams{
			Epic: action.Epic, Turn: action.Turn, Position: action.Position,
		})
		if err == nil {
			return errActionSettled
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read amendment of action %d of turn %d of epic %s: %w",
				action.Position, action.Turn, action.Epic, err)
		}
		head, err := epicHead(ctx, queries, action.Epic)
		if err != nil {
			return err
		}
		park, err := actionPark(ctx, queries, action, head)
		if err != nil {
			return err
		}
		amended, err := startAmendment(ctx, queries, action.Epic, amendStart{
			amender: AmenderPlanner, turn: action.Turn, position: action.Position,
			Amendment: Amendment{Requirement: action.Requirement, Item: action.Item, Reason: action.Reason},
		}, at)
		if err != nil {
			return err
		}
		if err = queries.LinkPlannerActionPark(ctx, sqlcgen.LinkPlannerActionParkParams{
			Park: park, Epic: action.Epic, Turn: action.Turn, Position: action.Position,
		}); err != nil {
			return fmt.Errorf("link action %d of turn %d of epic %s to its park: %w",
				action.Position, action.Turn, action.Epic, err)
		}
		_, err = applyUnchanged(ctx, queries, amended, at)
		return err
	})
	var refusal RefusalError
	switch {
	case err == nil, errors.Is(err, errActionSettled):
		return nil
	case errors.As(err, &refusal):
		return s.refuse(ctx, action, refusal, at)
	default:
		return err
	}
}

// OpenAmendments returns the amendments of every epic that are checking or capturing, ordered by epic, then seq.
func (s *Store) OpenAmendments(ctx context.Context) ([]EvidenceAmendment, error) {
	rows, err := sqlcgen.New(s.db).ListOpenEpicAmendments(ctx)
	if err != nil {
		return nil, fmt.Errorf("list open amendments: %w", err)
	}
	return convertInOrder(rows, amendmentOf)
}

// SettleAmendment settles amendment seq of epic in one write, by the work its state waits on, and does nothing while that
// work has not ended. An amendment that is neither checking nor capturing changes nothing, and so does one whose work is
// not stored yet.
//
// A checking amendment is settled by its red run. A red run that a restart lost, or that exited for its environment, is
// run again under the next attempt, at most amendmentReruns times. A red run that passed, which is a red judged fails
// at the epic's base, applies the amendment (see applyAmendment), unless an evidence, integrate or sync node run of the
// epic has work under way, or its release captures a merge of its release integrator: then nothing is written and the
// error matches ErrAmendmentDeferred. Any other red run refuses it (see refuseAmendment).
//
// A capturing amendment is settled once its green run, and its reproduction check when it has one, have ended. A run or
// check that a restart lost, or that exited for its environment, runs both again under the next attempt, at most
// amendmentReruns times. Otherwise the amendment is applied, with the outcome of its green run and reproduction as the
// cause of its trigger (see finishAmendment).
func (s *Store) SettleAmendment(ctx context.Context, epic string, seq int64, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		amendment, err := readAmendment(ctx, queries, epic, seq)
		if err != nil {
			return err
		}
		switch amendment.State {
		case AmendmentChecking:
			return settleRed(ctx, queries, amendment, at)
		case AmendmentCapturing:
			return settleGreen(ctx, queries, amendment, at)
		}
		return nil
	})
}

// StopAmendment settles amendment seq of epic, whose work cannot start for reason, in one write. A checking amendment is
// refused with the reason that its red run could not start (see refuseAmendment), and the Requirement keeps its item. A
// capturing amendment, whose item is applied already, is applied, with the reason its green run could not start as its
// outcome (see finishAmendment). Any other amendment changes nothing.
func (s *Store) StopAmendment(ctx context.Context, epic string, seq int64, reason string, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		amendment, err := readAmendment(ctx, queries, epic, seq)
		if err != nil {
			return err
		}
		switch amendment.State {
		case AmendmentChecking:
			refusal := fmt.Sprintf("the red run %s of the new item at the epic's base %s could not start: %s",
				amendment.RedRun, amendment.Base, reason)
			return refuseAmendment(ctx, queries, amendment, refusal, at)
		case AmendmentCapturing:
			return applyCaptured(ctx, queries, amendment, fmt.Sprintf(
				"the green run %s at the stored base %s could not start: %s", amendment.GreenRun, amendment.StoredBase,
				reason), at)
		}
		return nil
	})
}

// settleRed settles a checking amendment by its red run in the caller's write, as SettleAmendment gives. A run stored
// under the red's id that is not a red run of the amendment's item at its base refuses it, since only such a run guards
// the Requirement.
func settleRed(ctx context.Context, queries *sqlcgen.Queries, amendment EvidenceAmendment, at time.Time) error {
	run, found, err := endedRun(ctx, queries, amendment.RedRun)
	if err != nil || !found {
		return err
	}
	if !amendmentRun(amendment, run, evidence.PhaseRed, amendment.Base) {
		return refuseAmendment(ctx, queries, amendment, fmt.Sprintf(
			"the run %s is not a red run of the new item at the epic's base %s", run.ID, amendment.Base), at)
	}
	if rerunnableRun(run) && amendment.RedAttempt < amendmentReruns {
		next := amendment.RedAttempt + 1
		_, err = queries.RetryEpicAmendmentRed(ctx, sqlcgen.RetryEpicAmendmentRedParams{
			RedAttempt: next, RedRun: AmendmentWorkID(amendment.Epic, amendment.Seq, AmendmentRed, next),
			Epic: amendment.Epic, Seq: amendment.Seq,
		})
		if err != nil {
			return fmt.Errorf("rerun red of amendment %d of epic %s: %w", amendment.Seq, amendment.Epic, err)
		}
		return nil
	}
	if run.Passed {
		return applyAmendment(ctx, queries, amendment, fmt.Sprintf(
			" The red run %s of the new item at the epic's base %s was judged fails.", run.ID, amendment.Base), at)
	}
	return refuseAmendment(ctx, queries, amendment, redRefusal(amendment, run), at)
}

// amendmentRun reports whether run is a run of amendment's item, for its Requirement, in phase at revision.
func amendmentRun(amendment EvidenceAmendment, run EvidenceRun, phase, revision string) bool {
	return run.Item == amendment.Requirement && run.Phase == phase && run.Revision == revision &&
		evidence.Current(run.Spec, amendment.Item)
}

// listsAsNull returns item with each empty list as nil, so two items that differ only in an empty list given as null or
// as [] encode the same.
func listsAsNull(item evidence.Item) evidence.Item {
	for _, list := range []*[]string{&item.Capture, &item.Artifacts, &item.Supplied, &item.Tests} {
		if len(*list) == 0 {
			*list = nil
		}
	}
	return item
}

// redRefusal returns why the red run of amendment refuses it: its new item holds at the epic's base, which would weaken
// what the Requirement claims, or the run gave no verdict.
func redRefusal(amendment EvidenceAmendment, run EvidenceRun) string {
	where := fmt.Sprintf("the red run %s of the new item at the epic's base %s", run.ID, amendment.Base)
	switch {
	case run.NotCaptured != "":
		return where + " captured nothing: " + run.NotCaptured
	case run.Verdict == evidence.VerdictHolds:
		return where + " was judged holds, so the new item does not fail at the base: " + run.JudgeReason
	}
	return where + " gave no verdict: " + run.JudgeReason
}

// applyAmendment applies amendment in the caller's write, whose proof is the account its decision gives of why it may
// apply: its red run passed, or it keeps the conclusion and kind (see applyUnchanged). It returns ErrAmendmentDeferred,
// having written nothing, while an evidence, integrate or sync node run of the epic has a revision and is running, or
// while its release node run is running, the epic is releasing, and the latest round of its release integrator that
// passed at the run's attempt made a merge that is not yet a candidate of that attempt: the release walks that merge's
// Done gates and captures until it records the merge as its candidate (see CountWorkingEvidenceNodeRuns). A release
// run doing anything else, such as waiting on the plan its replan started, which waits on the amendment in turn, holds
// nothing back. Otherwise the Requirement takes the new item, a decision records the amendment,
// and a planner action that asked for it is marked taken. When the epic's stored base has moved past its base, the
// amendment moves to capturing, with its green run and, for a test item, its reproduction named at the stored base.
// Otherwise it is applied at once, and the next integrate captures the new item at its candidate.
func applyAmendment(
	ctx context.Context, queries *sqlcgen.Queries, amendment EvidenceAmendment, proof string, at time.Time,
) error {
	working, err := queries.CountWorkingEvidenceNodeRuns(ctx, amendment.Epic)
	if err != nil {
		return fmt.Errorf("count working evidence runs of epic %s: %w", amendment.Epic, err)
	}
	if working > 0 {
		return ErrAmendmentDeferred
	}
	head, err := epicHead(ctx, queries, amendment.Epic)
	if err != nil {
		return err
	}
	amendedItem, err := encodeItem(amendment.Item)
	if err != nil {
		return fmt.Errorf("new item of amendment %d of epic %s: %w", amendment.Seq, amendment.Epic, err)
	}
	if setErr := setRequirementItem(ctx, queries, amendment, amendedItem); setErr != nil {
		return setErr
	}
	text := fmt.Sprintf(
		"%s amended the evidence of %s (A%d): %s.%s%s",
		amendment.Amender,
		amendment.Requirement,
		amendment.Seq,
		amendment.Reason,
		proof,
		itemsText(amendment.Prior, amendment.Item),
	)
	if _, err = appendDecision(ctx, queries, amendment.Epic, amendmentDecisionID(amendment), text, 0, at); err != nil {
		return err
	}
	if takeErr := takeAmendmentAction(ctx, queries, amendment); takeErr != nil {
		return takeErr
	}
	storedBase := head.StoredBase
	if storedBase == "" {
		storedBase = head.Base
	}
	if storedBase == amendment.Base {
		if _, err = queries.ApplyEpicAmendment(ctx, sqlcgen.ApplyEpicAmendmentParams{
			Settled: at.Unix(), Epic: amendment.Epic, Seq: amendment.Seq,
		}); err != nil {
			return fmt.Errorf("apply amendment %d of epic %s: %w", amendment.Seq, amendment.Epic, err)
		}
		return finishAmendment(ctx, queries, amendment, "the next integrate captures the new item at its candidate", at)
	}
	green, repro := amendmentGreenIDs(amendment, 0)
	if _, err = queries.CaptureEpicAmendment(ctx, sqlcgen.CaptureEpicAmendmentParams{
		StoredBase: storedBase, GreenRun: green, ReproCheck: repro, Settled: at.Unix(),
		Epic: amendment.Epic, Seq: amendment.Seq,
	}); err != nil {
		return fmt.Errorf("capture amendment %d of epic %s: %w", amendment.Seq, amendment.Epic, err)
	}
	return nil
}

// setRequirementItem stores item, encoded, as the item of the Requirement amendment names, in the caller's write. A
// Requirement the epic no longer holds is an error.
func setRequirementItem(ctx context.Context, queries *sqlcgen.Queries, amendment EvidenceAmendment, item string) error {
	rows, err := queries.SetEpicRequirementItem(ctx, sqlcgen.SetEpicRequirementItemParams{
		Item: item, Epic: amendment.Epic, ID: amendment.Requirement,
	})
	if err != nil {
		return fmt.Errorf("set item of %s of epic %s: %w", amendment.Requirement, amendment.Epic, err)
	}
	if rows == 0 {
		return fmt.Errorf("epic %s holds no Requirement %s to amend", amendment.Epic, amendment.Requirement)
	}
	return nil
}

// amendmentGreenIDs returns the ids of the green run and, for a test item, the reproduction check of amendment at
// attempt. An item that is not a test item has no reproduction, so its id is "".
func amendmentGreenIDs(amendment EvidenceAmendment, attempt int64) (string, string) {
	green := AmendmentWorkID(amendment.Epic, amendment.Seq, AmendmentGreen, attempt)
	if amendment.Item.Kind != evidence.KindTest {
		return green, ""
	}
	return green, AmendmentWorkID(amendment.Epic, amendment.Seq, AmendmentRepro, attempt)
}

// takeAmendmentAction marks the planner action that asked for amendment taken, in the caller's write, keeping the park
// startPlannedAmendment linked it to. An owner amendment has no action. An action that is no longer pending is left as
// it is.
func takeAmendmentAction(ctx context.Context, queries *sqlcgen.Queries, amendment EvidenceAmendment) error {
	if amendment.Turn == 0 {
		return nil
	}
	action := amendmentAction(amendment)
	park, err := linkedPark(ctx, queries, action)
	if err != nil {
		return err
	}
	err = setActionState(ctx, queries, action, actionTaken, "", park)
	if errors.Is(err, errActionSettled) {
		return nil
	}
	return err
}

// linkedPark returns the park action is linked to, as startPlannedAmendment linked it.
func linkedPark(ctx context.Context, queries *sqlcgen.Queries, action PlannerAction) (int64, error) {
	park, err := queries.GetPlannerActionPark(ctx, sqlcgen.GetPlannerActionParkParams{
		Epic: action.Epic, Turn: action.Turn, Position: action.Position,
	})
	if err != nil {
		return 0, fmt.Errorf("read the park of action %d of turn %d of epic %s: %w",
			action.Position, action.Turn, action.Epic, err)
	}
	return park, nil
}

// amendmentAction returns the planner action that asked for amendment.
func amendmentAction(amendment EvidenceAmendment) PlannerAction {
	return PlannerAction{
		Epic: amendment.Epic, Turn: amendment.Turn, Position: amendment.Position,
		EpicActionInput: EpicActionInput{
			Kind:        ActionAmendEvidence,
			Requirement: amendment.Requirement,
			Item:        amendment.Item,
			Reason:      amendment.Reason,
		},
	}
}

// refuseAmendment refuses amendment with refusal in the caller's write, and the Requirement keeps its item. The planner
// action that asked for a planner amendment is refused as any refused action is, linked to the park startPlannedAmendment
// linked it to (see refuseLinkedAction), which records the refusal as the action's decision and wakes the planner, or
// binds its park. The owner's amendment gets a decision with the id amend-<seq>-refused that holds the refusal, its reason
// and both items, and a tell that says so.
func refuseAmendment(
	ctx context.Context, queries *sqlcgen.Queries, amendment EvidenceAmendment, refusal string, at time.Time,
) error {
	if _, err := queries.RefuseEpicAmendment(ctx, sqlcgen.RefuseEpicAmendmentParams{
		Refusal: refusal, Settled: at.Unix(), Epic: amendment.Epic, Seq: amendment.Seq,
	}); err != nil {
		return fmt.Errorf("refuse amendment %d of epic %s: %w", amendment.Seq, amendment.Epic, err)
	}
	if amendment.Turn != 0 {
		action := amendmentAction(amendment)
		park, err := linkedPark(ctx, queries, action)
		if err != nil {
			return err
		}
		err = refuseLinkedAction(ctx, queries, action, RefusalError(refusal), park, at)
		if errors.Is(err, errActionSettled) {
			return nil
		}
		return err
	}
	id := amendmentDecisionID(amendment) + "-refused"
	text := fmt.Sprintf("%s amendment A%d of the evidence of %s refused: %s. Its reason: %s.%s", amendment.Amender,
		amendment.Seq, amendment.Requirement, refusal, amendment.Reason, itemsText(amendment.Prior, amendment.Item))
	if _, err := appendDecision(ctx, queries, amendment.Epic, id, text, 0, at); err != nil {
		return err
	}
	tell := fmt.Sprintf("Evidence amendment A%d of %s refused: %s", amendment.Seq, amendment.Requirement, refusal)
	return insertTell(ctx, queries, amendment.Epic, tell, at)
}

// settleGreen settles a capturing amendment by its green run and reproduction in the caller's write, as SettleAmendment
// gives.
func settleGreen(ctx context.Context, queries *sqlcgen.Queries, amendment EvidenceAmendment, at time.Time) error {
	green, found, err := endedRun(ctx, queries, amendment.GreenRun)
	if err != nil || !found {
		return err
	}
	if !amendmentRun(amendment, green, evidence.PhaseGreen, amendment.StoredBase) {
		return applyCaptured(ctx, queries, amendment, fmt.Sprintf(
			"the run %s is not a green run of the new item at the stored base %s", green.ID, amendment.StoredBase), at)
	}
	var repro NodeCheck
	if amendment.ReproCheck != "" {
		repro, found, err = endedCheck(ctx, queries, amendment.ReproCheck)
		if err != nil || !found {
			return err
		}
	}
	rerun := rerunnableRun(green) || (amendment.ReproCheck != "" && rerunnableCheck(repro))
	if rerun && amendment.GreenAttempt < amendmentReruns {
		next := amendment.GreenAttempt + 1
		greenID, reproID := amendmentGreenIDs(amendment, next)
		if _, err = queries.RetryEpicAmendmentGreen(ctx, sqlcgen.RetryEpicAmendmentGreenParams{
			GreenAttempt: next, GreenRun: greenID, ReproCheck: reproID, Epic: amendment.Epic, Seq: amendment.Seq,
		}); err != nil {
			return fmt.Errorf("rerun green of amendment %d of epic %s: %w", amendment.Seq, amendment.Epic, err)
		}
		return nil
	}
	return applyCaptured(ctx, queries, amendment, greenOutcome(amendment, green, repro), at)
}

// applyCaptured applies a capturing amendment, whose item is applied already, with outcome as the outcome of its green,
// in the caller's write (see finishAmendment).
func applyCaptured(
	ctx context.Context, queries *sqlcgen.Queries, amendment EvidenceAmendment, outcome string, at time.Time,
) error {
	if _, err := queries.ApplyEpicAmendment(ctx, sqlcgen.ApplyEpicAmendmentParams{
		Settled: at.Unix(), Epic: amendment.Epic, Seq: amendment.Seq,
	}); err != nil {
		return fmt.Errorf("apply amendment %d of epic %s: %w", amendment.Seq, amendment.Epic, err)
	}
	return finishAmendment(ctx, queries, amendment, outcome, at)
}

// greenOutcome returns the outcome of the green run and reproduction of a capturing amendment, as its trigger's cause
// carries it: whether green passed at the stored base, and for a test item how its reproduction at the epic's base ended.
func greenOutcome(amendment EvidenceAmendment, green EvidenceRun, repro NodeCheck) string {
	outcome := fmt.Sprintf("the green run %s at the stored base %s ", green.ID, amendment.StoredBase)
	switch {
	case green.NotCaptured != "":
		outcome += "captured nothing: " + green.NotCaptured
	case green.Passed:
		outcome += nodePassedOutcome
	case green.Verdict == "":
		outcome += "gave no verdict: " + green.JudgeReason
	default:
		outcome += fmt.Sprintf("did not pass: verdict %s, exit %d", green.Verdict, green.Exit)
	}
	if amendment.ReproCheck == "" {
		return outcome
	}
	outcome += fmt.Sprintf("; its reproduction %s at the epic's base ", repro.ID)
	switch {
	case repro.NotRun != "":
		return outcome + "did not run: " + repro.NotRun
	case repro.TimedOut:
		return outcome + "timed out"
	}
	return outcome + fmt.Sprintf("exited %d", repro.Exit)
}

// finishAmendment ends an amendment that is now applied, in the caller's write. It adds a pending trigger with the id
// amend-<seq> whose cause names the amendment and outcome, so the planner plans on the new item, and the owner's amendment
// also adds a tell with the same text.
func finishAmendment(
	ctx context.Context, queries *sqlcgen.Queries, amendment EvidenceAmendment, outcome string, at time.Time,
) error {
	cause := fmt.Sprintf("evidence of %s amended by the %s (A%d): %s", amendment.Requirement, amendment.Amender,
		amendment.Seq, outcome)
	if err := insertAmendmentTrigger(
		ctx,
		queries,
		amendment.Epic,
		amendmentTriggerID(amendment),
		cause,
		at,
	); err != nil {
		return err
	}
	if amendment.Amender != AmenderOwner {
		return nil
	}
	return insertTell(ctx, queries, amendment.Epic, capitalized(cause), at)
}

// insertAmendmentTrigger adds a pending trigger to epic with id and cause, in the caller's write.
func insertAmendmentTrigger(ctx context.Context, queries *sqlcgen.Queries, epic, id, cause string, at time.Time) error {
	if err := queries.InsertEpicTrigger(ctx, sqlcgen.InsertEpicTriggerParams{
		Epic: epic, ID: id, Cause: cause, State: triggerPending, Turn: 0, Created: at.Unix(),
	}); err != nil {
		return fmt.Errorf("insert trigger %s of epic %s: %w", id, epic, err)
	}
	return nil
}

// settleClosedAmendments settles each open amendment of epic in the caller's write of its close: a checking one is
// refused, the Requirement keeping its item, with its planner action refused with it, and a capturing one, whose item is
// applied already, is applied. Neither writes a decision, a tell or a trigger, since the epic is closed.
func settleClosedAmendments(ctx context.Context, queries *sqlcgen.Queries, end closeEnd) error {
	stored, err := queries.ListEpicAmendments(ctx, end.epic)
	if err != nil {
		return fmt.Errorf("list amendments of epic %s: %w", end.epic, err)
	}
	refusal := fmt.Sprintf("epic %s closed as %s", end.epic, end.kind)
	for _, row := range stored {
		switch row.State {
		case AmendmentChecking:
			if _, err = queries.RefuseEpicAmendment(ctx, sqlcgen.RefuseEpicAmendmentParams{
				Refusal: refusal, Settled: end.at.Unix(), Epic: end.epic, Seq: row.Seq,
			}); err != nil {
				return fmt.Errorf("refuse amendment %d of epic %s: %w", row.Seq, end.epic, err)
			}
			if row.Turn == 0 {
				continue
			}
			action := PlannerAction{Epic: end.epic, Turn: row.Turn, Position: row.Position}
			err = setActionState(ctx, queries, action, actionRefused, refusal, 0)
			if err != nil && !errors.Is(err, errActionSettled) {
				return err
			}
		case AmendmentCapturing:
			if _, err = queries.ApplyEpicAmendment(ctx, sqlcgen.ApplyEpicAmendmentParams{
				Settled: end.at.Unix(), Epic: end.epic, Seq: row.Seq,
			}); err != nil {
				return fmt.Errorf("apply amendment %d of epic %s: %w", row.Seq, end.epic, err)
			}
		}
	}
	return nil
}

// endedRun returns the evidence run with id and true when it is stored and has ended, and false with no error when it
// is not stored or has not ended.
func endedRun(ctx context.Context, queries *sqlcgen.Queries, id string) (EvidenceRun, bool, error) {
	stored, err := queries.GetEvidenceRun(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return EvidenceRun{}, false, nil
	}
	if err != nil {
		return EvidenceRun{}, false, fmt.Errorf("get evidence run %s: %w", id, err)
	}
	if stored.State != runEnded {
		return EvidenceRun{}, false, nil
	}
	run, err := evidenceRunOf(ctx, queries, stored)
	if err != nil {
		return EvidenceRun{}, false, err
	}
	return run, true, nil
}

// endedCheck returns the node check with id and true when it is stored and has ended, and false with no error when it
// is not stored or is still running.
func endedCheck(ctx context.Context, queries *sqlcgen.Queries, id string) (NodeCheck, bool, error) {
	stored, err := queries.GetEpicNodeCheck(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return NodeCheck{}, false, nil
	}
	if err != nil {
		return NodeCheck{}, false, fmt.Errorf("get node check %s: %w", id, err)
	}
	if stored.State == NodeRunning {
		return NodeCheck{}, false, nil
	}
	check, err := nodeCheckOf(stored)
	if err != nil {
		return NodeCheck{}, false, err
	}
	return check, true, nil
}

// rerunnableRun reports whether run, an ended evidence run, gave no result of its own: a restart lost its capture, or
// its capture exited for its environment.
func rerunnableRun(run EvidenceRun) bool {
	return run.NotCaptured == LostAtRestart || run.NotCaptured == NotCapturedEnvironment ||
		(run.NotCaptured == "" && EnvironmentExited(run.Exit, run.TimedOut))
}

// rerunnableCheck reports whether check, an ended node check, gave no result of its own: a restart lost it, or it exited
// for its environment.
func rerunnableCheck(check NodeCheck) bool {
	return check.NotRun == LostAtRestart || (check.NotRun == "" && EnvironmentExited(check.Exit, check.TimedOut))
}

// amendmentDecisionID is the id of the decision that records amendment applied: the decision id of the planner action
// that asked for it, as a taken action's decision has, and amend-<seq> for any other amendment.
func amendmentDecisionID(amendment EvidenceAmendment) string {
	if amendment.Turn != 0 {
		return actionDecisionID(amendmentAction(amendment))
	}
	return fmt.Sprintf("amend-%d", amendment.Seq)
}

// amendmentTriggerID is the id of the trigger an applied amendment adds: amend-<seq>.
func amendmentTriggerID(amendment EvidenceAmendment) string {
	return fmt.Sprintf("amend-%d", amendment.Seq)
}

// itemsText returns the prior and new items of an amendment as a decision holds them, each as the compact JSON an item
// is stored in, after a space: ` Prior item: <prior>. New item: <item>.`.
func itemsText(prior, item evidence.Item) string {
	return fmt.Sprintf(" Prior item: %s. New item: %s.", itemJSON(prior), itemJSON(item))
}

// itemJSON returns item as the compact JSON an item is stored in, or its encoding error's text.
func itemJSON(item evidence.Item) string {
	encoded, err := json.Marshal(item)
	if err != nil {
		return "unencodable item: " + err.Error()
	}
	return string(encoded)
}

// capitalized returns text with its first letter upper case, for a tell that leads with a cause's text.
func capitalized(text string) string {
	if text == "" {
		return text
	}
	return strings.ToUpper(text[:1]) + text[1:]
}

// readAmendment returns amendment seq of epic as queries read it.
func readAmendment(ctx context.Context, queries *sqlcgen.Queries, epic string, seq int64) (EvidenceAmendment, error) {
	row, err := queries.GetEpicAmendment(ctx, sqlcgen.GetEpicAmendmentParams{Epic: epic, Seq: seq})
	if errors.Is(err, sql.ErrNoRows) {
		return EvidenceAmendment{}, fmt.Errorf("epic %s has no amendment %d", epic, seq)
	}
	if err != nil {
		return EvidenceAmendment{}, fmt.Errorf("get amendment %d of epic %s: %w", seq, epic, err)
	}
	return amendmentOf(row)
}

// loadAmendments fills the amendments of row from the rows stored under row.ID, in seq order.
func loadAmendments(ctx context.Context, queries *sqlcgen.Queries, row *EpicRow) error {
	stored, err := queries.ListEpicAmendments(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("list amendments: %w", err)
	}
	row.Amendments, err = convertInOrder(stored, amendmentOf)
	return err
}

// amendmentOf returns the amendment a stored row holds, its items decoded.
func amendmentOf(row sqlcgen.EpicAmendment) (EvidenceAmendment, error) {
	priorItem, err := decodeItem(row.PriorItem)
	if err != nil {
		return EvidenceAmendment{}, fmt.Errorf("old item of amendment %d of epic %s: %w", row.Seq, row.Epic, err)
	}
	amendedItem, err := decodeItem(row.Item)
	if err != nil {
		return EvidenceAmendment{}, fmt.Errorf("new item of amendment %d of epic %s: %w", row.Seq, row.Epic, err)
	}
	amendment := EvidenceAmendment{
		Epic:         row.Epic,
		Seq:          row.Seq,
		Requirement:  row.Requirement,
		Amender:      row.Amender,
		Turn:         row.Turn,
		Position:     row.Position,
		Prior:        priorItem,
		Item:         amendedItem,
		Reason:       row.Reason,
		Base:         row.Base,
		State:        row.State,
		RedAttempt:   row.RedAttempt,
		RedRun:       row.RedRun,
		StoredBase:   row.StoredBase,
		GreenAttempt: row.GreenAttempt,
		GreenRun:     row.GreenRun,
		ReproCheck:   row.ReproCheck,
		Refusal:      row.Refusal,
		Created:      time.Unix(row.Created, 0),
	}
	if row.Settled != 0 {
		amendment.Settled = time.Unix(row.Settled, 0)
	}
	return amendment, nil
}
