package interpret

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
)

// amendmentLabelPrefix starts the label an amendment's reproduction check and the steps of its judges carry: the
// amendment's seq follows it.
const amendmentLabelPrefix = "amendment A"

// reproJudgment is what the judge of a reproduction rules on, and where its step runs: the Requirement whose test item
// the reproduction ran, with the item the judge rules for; the tasks whose change its tests claim to prove; the
// Requirement's red at the epic's base, nil when none is kept; the reproduction check; the base it ran at and the commit
// whose test files it laid over that base; and the label its step carries as its task, which the step is found by.
type reproJudgment struct {
	requirement store.EpicRequirement
	tasks       []store.EpicTask
	red         *store.EvidenceRun
	repro       store.NodeCheck
	base        string
	commit      string
	label       string
}

// reproJudge carries out one ActReproJudge action of an integrate run, as the first of its items says: the start of a
// judge's step, or the record of the ruling of a judge whose step has ended. The items after it name the Requirement,
// the reproduction check and the judge's number.
func (r *Runner) reproJudge(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	judge, err := strconv.ParseInt(act.Items[3], 10, 64)
	if err != nil {
		return fmt.Errorf("read the judge of node %s: %w", act.Path, err)
	}
	ruling := store.ReproRuling{Check: act.Items[2], Judge: judge}
	switch act.Items[0] {
	case kinds.ReproJudgeStart:
		judged, judgedErr := r.integrateJudgment(ctx, epic, act)
		if judgedErr != nil {
			return judgedErr
		}
		_, err = r.judgeReproduction(ctx, epic, judged, ruling, act.IDs[0], at)
		return err
	case kinds.ReproJudgeRecord:
		row, stepErr := r.taskStep(ctx, epic.ID, act.Path, act.IDs[0])
		if stepErr != nil {
			return stepErr
		}
		return r.recordJudgeRuling(ctx, epic, ruling, row, at)
	default:
		return fmt.Errorf("judge work %s is not one the executor carries out", act.Items[0])
	}
}

// integrateJudgment returns what the judge act starts rules on: the reproduction check act names of the Requirement it
// names, which the integrate run ran at the epic's base with the test files of its candidate, the batch's tasks that
// the run composed and that claim or contribute to the Requirement, and the Requirement's red at the epic's base. Its
// step is labeled with the run's path. A Requirement or check that is not stored is an error.
func (r *Runner) integrateJudgment(ctx context.Context, epic store.EpicRow, act Action) (reproJudgment, error) {
	requirement, err := requirementOf(epic, act.Items[1])
	if err != nil {
		return reproJudgment{}, err
	}
	flow, err := r.store.EpicFlow(ctx, epic.ID)
	if err != nil {
		return reproJudgment{}, fmt.Errorf("read epic %s: %w", epic.ID, err)
	}
	i := slices.IndexFunc(flow.Checks, func(check store.NodeCheck) bool { return check.ID == act.Items[2] })
	if i < 0 {
		return reproJudgment{}, fmt.Errorf("reproduction %s of node %s is not stored", act.Items[2], act.Path)
	}
	tasks := claimingTasks(epic.Tasks, requirement.ID, func(task store.EpicTask) bool {
		return task.Batch == act.Path && composedBy(act.Run, task.Seq)
	})
	return reproJudgment{
		requirement: requirement, tasks: tasks, red: baseRed(flow.Evidence, requirement.ID, epic.Base),
		repro: flow.Checks[i], base: epic.Base, commit: act.Run.Revision, label: act.Path,
	}, nil
}

// judgeReproduction starts the step id of the judge that ruling, its check and judge number, is for, to rule on judged
// for epic (reproJudgeStart). A start that cannot be built, or that the engine refuses for good (judgeRefused), records
// ruling as a judge that did not start, with why, so a judge that cannot start holds nothing up. Any other start error
// is returned, and the start is tried again on the next pass. It reports whether it recorded a ruling.
func (r *Runner) judgeReproduction(
	ctx context.Context, epic store.EpicRow, judged reproJudgment, ruling store.ReproRuling, id string, at time.Time,
) (bool, error) {
	if r.steps == nil {
		return false, missingService("steps")
	}
	start, err := r.reproJudgeStart(ctx, epic, judged, id)
	if err == nil {
		if _, err = r.steps.Start(ctx, start); err == nil {
			return false, nil
		}
		if !judgeRefused(err) {
			return false, fmt.Errorf("start reproduction judge %s: %w", id, err)
		}
	}
	ruling.Reason = err.Error()
	if recordErr := r.recordRuling(ctx, epic, ruling, at); recordErr != nil {
		return false, recordErr
	}
	return true, nil
}

// judgeRefused reports whether err refuses the start of a reproduction judge for good: a start refusedForGood refuses,
// or a gate the engine refuses, as the judge's agent gate is when its item names no judge. Either refuses every start
// of the same judge again.
func judgeRefused(err error) bool {
	return refusedForGood(err) || errors.Is(err, enginehost.ErrBadGate)
}

// reproJudgeStart returns the start of the judge step id that rules on judged for epic: a read-only step at the commit,
// in the epic's repository, that runs the judge the item names under the reproduction judge's agent gate
// (enginehost.ReproJudgeGate), labeled with the epic and judged's label. Its prompt is enginehost.ReproJudgePrompt's for
// the tasks, the item's conclusion, the red run, the reproduction and git's diff stat from the base to the commit, read
// now on the epic's host. A host that does not resolve, a diff that errs and a prompt that cannot be built fail the
// start.
func (r *Runner) reproJudgeStart(
	ctx context.Context, epic store.EpicRow, judged reproJudgment, id string,
) (enginehost.StepStart, error) {
	host, err := r.hostOf(epic)
	if err != nil {
		return enginehost.StepStart{}, err
	}
	stat, err := gitOutput(ctx, host, epic.Repository, "diff", "--stat", judged.base, judged.commit)
	if err != nil {
		return enginehost.StepStart{}, fmt.Errorf("diff stat of %s's reproduction: %w", judged.requirement.ID, err)
	}
	prompt, err := enginehost.ReproJudgePrompt(enginehost.ReproJudgeInput{
		Tasks: judged.tasks, Conclusion: judged.requirement.Item.Conclusion, Red: judged.red, Repro: judged.repro,
		Base: judged.base, Commit: judged.commit, DiffStat: stat,
	})
	if err != nil {
		return enginehost.StepStart{}, fmt.Errorf("judge prompt of %s's reproduction: %w", judged.requirement.ID, err)
	}
	gate := enginehost.ReproJudgeGate(judged.requirement.Item.Judge)
	return enginehost.StepStart{
		ID: id, ReadOnly: true, Workspace: epic.Repository, Revision: judged.commit, AgentGate: &gate, Prompt: prompt,
		Epic: epic.ID, Task: judged.label, Purpose: fmt.Sprintf("Judge %s's reproduction.", judged.requirement.ID),
	}, nil
}

// recordJudgeRuling records ruling, its check and judge number, from row, the step of that judge, which has ended. Its
// verdict and reason read as enginehost.ReproJudgeVerdict reads them: holds for a reproduction that does not contradict
// red, fails for one that does, and no verdict, with how the step ended, for any other end.
func (r *Runner) recordJudgeRuling(
	ctx context.Context, epic store.EpicRow, ruling store.ReproRuling, row store.StepRow, at time.Time,
) error {
	ruling.Step = row.ID
	ruling.Verdict, ruling.Reason = enginehost.ReproJudgeVerdict(row)
	return r.recordRuling(ctx, epic, ruling, at)
}

// recordRuling stores ruling, a ruling on a reproduction of epic, once (store.Store.RecordReproRuling).
func (r *Runner) recordRuling(ctx context.Context, epic store.EpicRow, ruling store.ReproRuling, at time.Time) error {
	if err := r.store.RecordReproRuling(ctx, epic.ID, ruling, at); err != nil {
		return fmt.Errorf("record ruling of judge %d of reproduction %s: %w", ruling.Judge, ruling.Check, err)
	}
	return nil
}

// claimingTasks returns the tasks among tasks, in their order, that claim or contribute to Requirement id and that
// counts reports are counted.
func claimingTasks(tasks []store.EpicTask, id string, counts func(task store.EpicTask) bool) []store.EpicTask {
	var claiming []store.EpicTask
	for _, task := range tasks {
		if (slices.Contains(task.Requirements, id) || slices.Contains(task.Contributes, id)) && counts(task) {
			claiming = append(claiming, task)
		}
	}
	return claiming
}

// baseRed returns the last, in the order of runs, of the ended red runs of Requirement id at base among runs, and nil
// when none is kept.
func baseRed(runs []store.EvidenceRun, id, base string) *store.EvidenceRun {
	var red *store.EvidenceRun
	for i := range runs {
		run := &runs[i]
		if run.Item == id && run.Phase == evidence.PhaseRed && run.Revision == base && run.State == evidenceEnded {
			red = run
		}
	}
	return red
}

// amendmentLabel returns the label of amended's reproduction check and of the steps of its judges: amendmentLabelPrefix
// and the amendment's seq.
func amendmentLabel(amended store.EvidenceAmendment) string {
	return amendmentLabelPrefix + strconv.FormatInt(amended.Seq, 10)
}

// ReadoptGate returns the agent gate a re-adopted reproduction judge step row that the interpreter started is restored
// with: the reproduction judge's gate (enginehost.ReproJudgeGate) for the judge its item names. The judge of an
// amendment's reproduction, labeled with the amendment (amendmentLabel), takes the judge of the item the amendment asks
// for. The judge of an integrate run's reproduction, labeled with the run's path, takes the judge of the item of the
// Requirement one of whose judge ids at the run's attempt (kinds.ReproJudgeID) is the row's. Any other row gets a nil
// gate. An epic that does not read, an amendment its epic does not hold, and a row whose id is no judge's of its
// integrate run are errors, so the row is not re-adopted.
func ReadoptGate(
	epics interface {
		EpicFlow(ctx context.Context, id string) (store.EpicFlow, error)
	},
) func(ctx context.Context, row store.StepRow) (*gates.Gate, error) {
	return func(ctx context.Context, row store.StepRow) (*gates.Gate, error) {
		if row.GateName != enginehost.ReproJudgeGateName {
			return nil, nil
		}
		flow, err := epics.EpicFlow(ctx, row.Epic)
		if err != nil {
			return nil, fmt.Errorf("read epic %s: %w", row.Epic, err)
		}
		if seq, ok := strings.CutPrefix(row.Task, amendmentLabelPrefix); ok {
			return amendmentJudgeGate(flow.Epic, seq)
		}
		i := slices.IndexFunc(flow.Runs, func(run store.NodeRun) bool {
			return run.Kind == kindIntegrate && run.Path == row.Task
		})
		if i < 0 {
			return nil, nil
		}
		return integrateJudgeGate(flow.Epic, flow.Runs[i], row)
	}
}

// integrateJudgeGate returns the reproduction judge's gate for the judge of the item of the Requirement of epic one of
// whose judge ids at the attempt of run, the integrate run row is labeled with, is row's id. A row whose id is no
// judge's of run is an error.
func integrateJudgeGate(epic store.EpicRow, run store.NodeRun, row store.StepRow) (*gates.Gate, error) {
	for _, requirement := range epic.Requirements {
		for judge := range int64(store.ReproJudges) {
			if kinds.ReproJudgeID(run, requirement.ID, judge) == row.ID {
				gate := enginehost.ReproJudgeGate(requirement.Item.Judge)
				return &gate, nil
			}
		}
	}
	return nil, fmt.Errorf("step %s is no reproduction judge of integrate run %s of epic %s", row.ID, run.Path,
		row.Epic)
}

// amendmentJudgeGate returns the reproduction judge's gate for the judge of the item the amendment of epic whose seq is
// seq asks for. A seq that does not read, or that names no amendment of epic, is an error.
func amendmentJudgeGate(epic store.EpicRow, seq string) (*gates.Gate, error) {
	amendmentSeq, err := strconv.ParseInt(seq, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("read the amendment of epic %s: %w", epic.ID, err)
	}
	i := slices.IndexFunc(epic.Amendments, func(amended store.EvidenceAmendment) bool {
		return amended.Seq == amendmentSeq
	})
	if i < 0 {
		return nil, fmt.Errorf("epic %s has no amendment A%d", epic.ID, amendmentSeq)
	}
	gate := enginehost.ReproJudgeGate(epic.Amendments[i].Item.Judge)
	return &gate, nil
}
