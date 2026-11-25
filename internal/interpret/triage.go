package interpret

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/joshsymonds/orrery/internal/build"
	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/roles"
	"github.com/joshsymonds/orrery/internal/store"
)

// roleTriage is the role a gate triage runs under, read from the machine's role file. A role file with no triage role
// runs it under the judge role, the read-only agent that judges a release.
const roleTriage = "triage"

// TriageSchema is the schema a gate triage returns: its status, its verdict, the evidence it quotes for an environment
// or flake verdict, the failing line it cites for a change, and its reason.
const TriageSchema = `{
  "title": "Gate triage return",
  "type": "object",
  "additionalProperties": false,
  "required": ["status", "verdict", "evidence", "citation", "reason"],
  "properties": {
    "status": {"type": "string", "enum": ["done"]},
    "verdict": {"type": "string", "enum": ["environment", "flake", "change"]},
    "evidence": {
      "type": "string",
      "description": "For environment or flake, the lines of the output tails or key lines that show it, copied exactly."
    },
    "citation": {
      "type": "string",
      "description": "For change, the failing test or lint line copied exactly from the tails or key lines; empty if none."
    },
    "reason": {"type": "string", "description": "Why, in one or two sentences."}
  }
}`

// triage carries out one ActTriage action of a gate, command or integrate run, as the first of its items says: the
// start of the triage step of a check that ran and failed, or the record of its triage once that step has ended.
func (r *Runner) triage(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	switch act.Items[0] {
	case kinds.TriageStart:
		return r.startTriage(ctx, epic, act, at)
	case kinds.TriageRecord:
		return r.recordTriage(ctx, epic, act, at)
	default:
		return fmt.Errorf("triage work %s is not one the executor carries out", act.Items[0])
	}
}

// startTriage starts the triage step act names: a read-only step under the role file's triage agent, or its judge agent
// when it names no triage agent, at the triaged check's revision in the epic's repository, labeled with the run's path
// so the run's rows read it. Its prompt states the facts of the failure (triageFacts), and its return check is
// kinds.TriageFailures, so a quote the check's output does not hold is sent back to be fixed. A role that does not
// resolve, and a start refused for good, record the triage change with why at once, so the check fails as it would with
// no triage and the start is not tried again. Any other start error is returned, and the start is tried again on the
// next pass.
func (r *Runner) startTriage(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	if r.steps == nil {
		return missingService("steps")
	}
	flow, err := r.store.EpicFlow(ctx, epic.ID)
	if err != nil {
		return fmt.Errorf("read epic %s: %w", epic.ID, err)
	}
	check, found := checkOf(flow.Checks, act.IDs[1])
	if !found {
		return fmt.Errorf("check %s of epic %s is not stored", act.IDs[1], epic.ID)
	}
	agent, err := roles.AgentOr(r.getenv, roleTriage, roleJudge)
	if err != nil {
		return r.refuseTriage(ctx, epic, act, "the triage did not start: "+err.Error(), at)
	}
	facts, err := r.triageFacts(ctx, epic, act, check, flow)
	if err != nil {
		return err
	}
	start := enginehost.StepStart{
		ID: act.IDs[0], Agent: agent, Prompt: triagePrompt(facts), Schema: TriageSchema, Workspace: epic.Repository,
		Revision: check.Revision, ReadOnly: true, Epic: epic.ID, Task: act.Path,
		Purpose: fmt.Sprintf("Triage the failed %s of %s.", facts.what(), build.Slug(epic.ID)),
		ReturnCheck: func(value json.RawMessage) []string {
			return kinds.TriageFailures(check, value)
		},
	}
	if _, err = r.steps.Start(ctx, start); err != nil {
		if !refusedForGood(err) {
			return fmt.Errorf("start triage %s: %w", start.ID, err)
		}
		return r.refuseTriage(ctx, epic, act, "the triage did not start: "+err.Error(), at)
	}
	return nil
}

// recordTriage records the triage act names once its step has ended, as kinds.JudgeTriage judges the step's row against
// the triaged check, within the verdicts a rerun may still follow, the action's items after its mode and gate.
func (r *Runner) recordTriage(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	row, err := r.taskStep(ctx, epic.ID, act.Path, act.IDs[0])
	if err != nil {
		return err
	}
	check, err := r.store.NodeCheck(ctx, act.IDs[1])
	if err != nil {
		return fmt.Errorf("read check %s of epic %s: %w", act.IDs[1], epic.ID, err)
	}
	triage := kinds.JudgeTriage(check, row, act.Items[2:])
	triage.Check, triage.Path, triage.Step = check.ID, act.Path, row.ID
	return r.storeTriage(ctx, epic, triage, at)
}

// refuseTriage records the triage act names as change with reason, the triage whose step never started. Its step is the
// id the step would have run under.
func (r *Runner) refuseTriage(ctx context.Context, epic store.EpicRow, act Action, reason string, at time.Time) error {
	return r.storeTriage(ctx, epic, store.GateTriage{
		Check: act.IDs[1], Path: act.Path, Step: act.IDs[0], Verdict: kinds.TriageChange, Reason: reason,
	}, at)
}

// storeTriage stores triage of epic, once.
func (r *Runner) storeTriage(ctx context.Context, epic store.EpicRow, triage store.GateTriage, at time.Time) error {
	if err := r.store.RecordGateTriage(ctx, epic.ID, triage, at); err != nil {
		return fmt.Errorf("record triage of check %s of node %s: %w", triage.Check, triage.Path, err)
	}
	return nil
}

// checkOf returns the check with id among checks, and whether there is one.
func checkOf(checks []store.NodeCheck, id string) (store.NodeCheck, bool) {
	i := slices.IndexFunc(checks, func(check store.NodeCheck) bool { return check.ID == id })
	if i < 0 {
		return store.NodeCheck{}, false
	}
	return checks[i], true
}
