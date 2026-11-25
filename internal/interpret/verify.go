package interpret

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/build"
	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/roles"
	"github.com/joshsymonds/orrery/internal/store"
)

// roleVerify is the role the verification step runs under. A role file with no key for it runs the step under the
// judge role.
const roleVerify = "verify"

// roundComposed is the outcome of a task integrator round whose merged commit composed its task into the candidate.
const roundComposed = "composed"

// verifyTarget is what a verification step verifies: the batch's candidate when seq is 0, and otherwise the merged
// commit of round round of the task integrator of task seq.
type verifyTarget struct {
	seq, round int64
}

// verifyRoundItems is how many items the ActVerify action of a task integrator round carries: its mode, the task's
// seq and the round.
const verifyRoundItems = 3

// verifyTargetOf returns the target act's verification is for: the round its second and third items name, or the
// batch's own when it carries no more than its mode.
func verifyTargetOf(act Action) (verifyTarget, error) {
	if len(act.Items) < verifyRoundItems {
		return verifyTarget{}, nil
	}
	seq, err := strconv.ParseInt(act.Items[1], 10, 64)
	if err != nil {
		return verifyTarget{}, fmt.Errorf("read the task of the verification of node %s: %w", act.Path, err)
	}
	round, err := strconv.ParseInt(act.Items[2], 10, 64)
	if err != nil {
		return verifyTarget{}, fmt.Errorf("read the round of the verification of node %s: %w", act.Path, err)
	}
	return verifyTarget{seq: seq, round: round}, nil
}

// verify carries out one ActVerify action of an integrate run, as the first of its items says: the start of the
// verification step of the batch's candidate or of a task integrator round's merged commit, or the record of the choice
// of a step that has ended.
func (r *Runner) verify(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	target, err := verifyTargetOf(act)
	if err != nil {
		return err
	}
	switch act.Items[0] {
	case kinds.VerifyStart:
		return r.startVerify(ctx, epic, act, target, at)
	case kinds.VerifyRecord:
		return r.recordVerify(ctx, epic, act, target, at)
	default:
		return fmt.Errorf("verification work %s is not one the executor carries out", act.Items[0])
	}
}

// startVerify starts the verification step act names (verifyStart). A start that cannot be built, or that the engine
// refuses for good, records the fallback choice with why (recordFallback), so a step that cannot start holds nothing up
// and the commit runs what it ran before a choice was made. Any other start error is returned, and the start is tried
// again on the next pass.
func (r *Runner) startVerify(
	ctx context.Context,
	epic store.EpicRow,
	act Action,
	target verifyTarget,
	at time.Time,
) error {
	if r.steps == nil {
		return missingService("steps")
	}
	start, err := r.verifyStart(ctx, epic, act, target)
	if err == nil {
		if _, err = r.steps.Start(ctx, start); err == nil {
			return nil
		}
		if !refusedForGood(err) {
			return fmt.Errorf("start verification %s: %w", act.IDs[0], err)
		}
	}
	return r.recordFallback(ctx, epic, act, target, "the verification step did not start: "+err.Error(), at)
}

// verifyStart returns the start of the verification step act names: a read-only step at the commit it verifies, in the
// epic's repository, under the agent the role file gives the verify role, or else the judge role, labeled with the
// epic and the run's path, so the run's rows hold it. Its prompt is verifyPrompt's of the facts verifyFacts reads, and
// its return is VerifySchema's. A role that does not resolve, facts that do not read and a prompt that cannot be built
// fail the start.
func (r *Runner) verifyStart(
	ctx context.Context, epic store.EpicRow, act Action, target verifyTarget,
) (enginehost.StepStart, error) {
	agent, err := roles.AgentOr(r.getenv, roleVerify, roleJudge)
	if err != nil {
		return enginehost.StepStart{}, fmt.Errorf("read the role of the verification step: %w", err)
	}
	facts, err := r.verifyFacts(ctx, epic, act, target)
	if err != nil {
		return enginehost.StepStart{}, err
	}
	prompt, err := verifyPrompt(facts)
	if err != nil {
		return enginehost.StepStart{}, err
	}
	return enginehost.StepStart{
		ID: act.IDs[0], Agent: agent, Prompt: prompt, Schema: VerifySchema, Workspace: epic.Repository,
		Revision: facts.head, ReadOnly: true, Epic: epic.ID, Task: act.Path,
		Purpose: fmt.Sprintf("Choose the verification of %s.", facts.what),
	}, nil
}

// recordVerify records the choice of the verification step act names, once its step has ended: the choice settled
// from the step's end and return with code's floors (kinds.SettleVerification), each Done gate's always-ness read as
// its check resolves it (resolveDoneGates), within the scope of act's target (kinds.VerifyScopeOf).
func (r *Runner) recordVerify(
	ctx context.Context, epic store.EpicRow, act Action, target verifyTarget, at time.Time,
) error {
	row, err := r.taskStep(ctx, epic.ID, act.Path, act.IDs[0])
	if err != nil {
		return err
	}
	end := kinds.VerifyEnd{Step: row.ID, Done: row.End == releaseDone, Return: row.Result}
	if !end.Done {
		end.Why = releaseEndReason("it", row)
	}
	done := doneGatesOf(r.resolveDoneGates(ctx, epic))
	scope := kinds.VerifyScopeOf(act.Run, kinds.Rows{Epic: epic}, target.seq)
	return r.storeVerification(ctx, epic, act, target, kinds.SettleVerification(end, done, scope), at)
}

// recordFallback records the fallback choice of act's target, for why: every Done gate runs, and the Requirements its
// scope's fallback holds are captured (kinds.FallbackVerification). No gate is resolved for it, since every one runs.
func (r *Runner) recordFallback(
	ctx context.Context, epic store.EpicRow, act Action, target verifyTarget, why string, at time.Time,
) error {
	done := make([]kinds.DoneGate, 0, len(epic.DoneGates))
	for _, name := range epic.DoneGates {
		done = append(done, kinds.DoneGate{Name: name})
	}
	scope := kinds.VerifyScopeOf(act.Run, kinds.Rows{Epic: epic}, target.seq)
	return r.storeVerification(ctx, epic, act, target, kinds.FallbackVerification(act.IDs[0], why, done, scope), at)
}

// storeVerification records chosen on the result of act's run, as the batch's verification or as the verification of
// act's round, in one write that swaps the result the run held when act was decided (store.SwapIntegrateRunResult). A
// result that has moved since writes nothing, and the next pass decides again on what the result holds then.
func (r *Runner) storeVerification(
	ctx context.Context, epic store.EpicRow, act Action, target verifyTarget, chosen kinds.Verification, at time.Time,
) error {
	updated, err := withVerification(act.Run.Result, target, chosen)
	if err != nil {
		return fmt.Errorf("record the verification of node %s: %w", act.Path, err)
	}
	if _, err = r.store.SwapIntegrateRunResult(
		ctx, epic.ID, act.Path, act.Run.Attempt, act.Run.Result, updated, at,
	); err != nil {
		return fmt.Errorf("record the verification of node %s: %w", act.Path, err)
	}
	return nil
}

// withVerification returns result, an integrate run's batch result, with chosen recorded for target: as its
// verification for the batch, or added to its round verifications for a round.
func withVerification(result string, target verifyTarget, chosen kinds.Verification) (string, error) {
	var batch batchResult
	if err := json.Unmarshal([]byte(result), &batch); err != nil {
		return "", fmt.Errorf("integrate result does not decode: %w", err)
	}
	if target.seq == 0 {
		batch.Verification = &chosen
	} else {
		batch.RoundVerifications = append(batch.RoundVerifications, kinds.RoundVerification{
			Task: target.seq, Round: target.round, Verification: chosen,
		})
	}
	data, err := json.Marshal(batch)
	if err != nil {
		return "", fmt.Errorf("encode batch result: %w", err)
	}
	return string(data), nil
}

// resolvedGate is one Done gate of an epic as its check resolves it: its name and the gate, or why it does not resolve
// to a command gate, "" when it does.
type resolvedGate struct {
	name       string
	gate       gates.Gate
	unresolved string
}

// resolveDoneGates returns each Done gate of epic, in order, resolved as its check resolves it (resolveGatesOn), on the
// epic's host.
func (r *Runner) resolveDoneGates(ctx context.Context, epic store.EpicRow) []resolvedGate {
	host, hostErr := r.hostOf(epic)
	return resolveGatesOn(ctx, host, hostErr, epic, r.getenv)
}

// resolveGatesOn returns each Done gate of epic, in order, resolved as its check resolves it (build.ResolveGate): from
// the epic's gates, else the project file of its repository, read on host, which hostErr says did not resolve when it is
// set. getenv reads the environment the project file is found by. A gate that does not resolve, every one when the host
// does not, and an agent gate, which a check does not run, carry why.
func resolveGatesOn(
	ctx context.Context, host hostexec.Host, hostErr error, epic store.EpicRow, getenv func(string) string,
) []resolvedGate {
	out := make([]resolvedGate, 0, len(epic.DoneGates))
	for _, name := range epic.DoneGates {
		if hostErr != nil {
			out = append(out, resolvedGate{name: name, unresolved: hostErr.Error()})
			continue
		}
		gate, err := build.ResolveGate(ctx, host, epic, name, getenv)
		switch {
		case err != nil:
			out = append(out, resolvedGate{name: name, unresolved: err.Error()})
		case gate.Agent != "":
			out = append(out, resolvedGate{
				name: name, gate: gate, unresolved: "it is an agent gate, and a check runs argv gates only",
			})
		default:
			out = append(out, resolvedGate{name: name, gate: gate})
		}
	}
	return out
}

// doneGatesOf returns resolved as the floors read them: each gate's name, whether it is declared always, and why it does
// not resolve.
func doneGatesOf(resolved []resolvedGate) []kinds.DoneGate {
	out := make([]kinds.DoneGate, 0, len(resolved))
	for _, gate := range resolved {
		out = append(out, kinds.DoneGate{Name: gate.name, Always: gate.gate.Always, Unresolved: gate.unresolved})
	}
	return out
}

// verifyFacts is what the prompt of a verification step gives: what it verifies, the commit its change runs from and
// the commit it verifies, git's diff stat and name-status lines between them, the conflict resolutions that made the
// commit, the tasks whose change it holds with the one a round merged first, the epic's Done gates as their checks
// resolve them, the Done gates whose checks already passed at the commit under a task integrator round of the run, its
// Requirements, and the scope its Requirements settle within.
type verifyFacts struct {
	what         string
	base, head   string
	stat         string
	paths        string
	resolutions  []string
	tasks        []store.EpicTask
	merged       int64
	gates        []resolvedGate
	passed       []kinds.PassedGate
	requirements []store.EpicRequirement
	scope        kinds.VerifyScope
}

// verifyFacts reads the facts of act's target from the store and git. The batch's change runs from the base its result
// names to the run's revision, its candidate, and its tasks are the ones it composed; its resolutions are those of its
// rounds that composed a task. A round's change runs from the candidate its step merged into to its merged commit, act's
// revision; its tasks are its own and the ones composed before it, and its resolution is its own. Either way the gates
// that passed at the commit are read from the run's rows (kinds.GatesPassedAt): a batch whose last conflicting round
// composed has that round's merged commit as its candidate, so the round's passed checks ran on the same tree. A result
// that does not decode, a round that is not recorded and a git read that fails are errors.
func (r *Runner) verifyFacts(
	ctx context.Context, epic store.EpicRow, act Action, target verifyTarget,
) (verifyFacts, error) {
	var batch batchResult
	if err := json.Unmarshal([]byte(act.Run.Result), &batch); err != nil {
		return verifyFacts{}, fmt.Errorf("integrate result of node %s does not decode: %w", act.Path, err)
	}
	flow, err := r.store.EpicFlow(ctx, epic.ID)
	if err != nil {
		return verifyFacts{}, fmt.Errorf("read epic %s: %w", epic.ID, err)
	}
	facts := verifyFacts{
		what: "the candidate of " + act.Path, base: batch.Base, head: act.Run.Revision,
		requirements: epic.Requirements, scope: kinds.VerifyScopeOf(act.Run, kinds.Rows{Epic: epic}, target.seq),
	}
	rounds := slices.DeleteFunc(slices.Clone(flow.Rounds), func(round store.IntegrateRound) bool {
		return round.Path != act.Path || round.Attempt != act.Run.Attempt
	})
	seqs := make([]int64, 0, len(batch.Composed)+1)
	if target.seq != 0 {
		seqs = append(seqs, target.seq)
		facts.merged, facts.what = target.seq, fmt.Sprintf("T%d's round %d merge in %s", target.seq, target.round,
			act.Path)
		rounds = slices.DeleteFunc(rounds, func(round store.IntegrateRound) bool {
			return round.Task != target.seq || round.Round != target.round
		})
		if len(rounds) == 0 {
			return verifyFacts{}, fmt.Errorf("round %d of task %d of node %s is not stored", target.round, target.seq,
				act.Path)
		}
		row, stepErr := r.taskStep(ctx, epic.ID, act.Path, rounds[0].Step)
		if stepErr != nil {
			return verifyFacts{}, stepErr
		}
		facts.base = row.Revision
	} else {
		rounds = slices.DeleteFunc(
			rounds,
			func(round store.IntegrateRound) bool { return round.Outcome != roundComposed },
		)
	}
	for _, composed := range batch.Composed {
		seqs = append(seqs, composed.Task)
	}
	for _, seq := range seqs {
		if task, found := taskBySeq(epic.Tasks, seq); found && !slices.ContainsFunc(facts.tasks,
			func(listed store.EpicTask) bool { return listed.Seq == seq }) {
			facts.tasks = append(facts.tasks, task)
		}
	}
	if facts.resolutions, err = r.roundResolutions(ctx, epic, act.Path, rounds); err != nil {
		return verifyFacts{}, err
	}
	if changeErr := r.readChange(ctx, epic, &facts); changeErr != nil {
		return verifyFacts{}, changeErr
	}
	facts.gates = r.resolveDoneGates(ctx, epic)
	facts.passed = kinds.GatesPassedAt(act.Run, flowRows(flow), facts.head)
	return facts, nil
}

// readChange fills facts with git's diff stat and name-status lines from its base to its head, read in the epic's
// repository on its host.
func (r *Runner) readChange(ctx context.Context, epic store.EpicRow, facts *verifyFacts) error {
	host, err := r.hostOf(epic)
	if err != nil {
		return err
	}
	if facts.stat, err = gitOutput(ctx, host, epic.Repository, "diff", "--stat", facts.base, facts.head); err != nil {
		return fmt.Errorf("diff stat of %s: %w", facts.what, err)
	}
	facts.paths, err = gitOutput(ctx, host, epic.Repository, "diff", "--name-status", "--no-renames", facts.base,
		facts.head)
	if err != nil {
		return fmt.Errorf("changed paths of %s: %w", facts.what, err)
	}
	return nil
}

// roundResolutions returns the lines of the conflict resolutions of rounds, task integrator rounds of the integrate run
// at path, in their order: for each, the task and its round with the paths git's merge of the task's revision into the
// candidate its step merged into conflicts at, read on the epic's host (taskConflicts), the integrator's account of
// the merge, and each change it made outside the conflicts, read from its step's return.
func (r *Runner) roundResolutions(
	ctx context.Context, epic store.EpicRow, path string, rounds []store.IntegrateRound,
) ([]string, error) {
	if len(rounds) == 0 {
		return nil, nil
	}
	host, err := r.hostOf(epic)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, round := range rounds {
		row, stepErr := r.taskStep(ctx, epic.ID, path, round.Step)
		if stepErr != nil {
			return nil, stepErr
		}
		conflicts, mergeErr := taskConflicts(ctx, host, integratorMerge{
			repo: epic.Repository, tip: row.Revision, origin: taskRevision(epic.Tasks, round.Task),
		})
		if mergeErr != nil {
			return nil, fmt.Errorf("conflicts of T%d's round %d: %w", round.Task, round.Round, mergeErr)
		}
		lines = append(lines,
			fmt.Sprintf("- T%d round %d, conflicted at: %s", round.Task, round.Round, joinedOrNone(conflicts)),
			"  account: "+round.Explanation)
		if ret, reason := decodeTaskReturn(row.Result); reason == "" {
			for _, change := range ret.OtherChanges {
				lines = append(lines, fmt.Sprintf("  changed %s outside the conflicts: %s", change.Path, change.Reason))
			}
		}
	}
	return lines, nil
}

// taskRevision returns the revision the build of the task seq of tasks holds, and "" when no task has seq.
func taskRevision(tasks []store.EpicTask, seq int64) string {
	task, _ := taskBySeq(tasks, seq)
	return task.Build.Revision
}

// joinedOrNone returns names joined by commas, or None. when there are none.
func joinedOrNone(names []string) string {
	if len(names) == 0 {
		return integratorNone
	}
	return strings.Join(names, ", ")
}
