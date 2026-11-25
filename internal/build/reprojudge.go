package build

import (
	"context"
	"errors"
	"fmt"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// reproExitZero is the outcome a reproduction note opens with when the reproduction ran and exited 0 without timing out.
// It states the exit only; what the exit means is the judge's ruling, which follows it.
const reproExitZero = "exited 0 on the base with the commit's test files"

// reproJudgeID returns the derived id of judge number judge, from 0, of the reproduction of task seq's green unit under
// the build's attempt and rerun, in scope, as StepID's scope describes. The judge number takes the finding position,
// which the unit has no other use for, so a second judge after a first that gave no verdict has its own id under the
// same rerun, and the reproduction does not run again.
func reproJudgeID(scope string, seq int64, build store.TaskBuild, judge int64) string {
	return StepID(scope, seq, unitReproJudge, build.Attempt, build.Rerun, 0, judge)
}

// decideReproJudge returns the action for the judge of a test item's reproduction that ran and exited 0 without timing
// out. Whether that contradicts red is the judge's ruling, never the exit's: a refactor, rename or removal task's tests
// are meant to pass on base code. With no judge started, the first starts under its derived id (reproJudgeID). A judge
// still running waits, a stopped one holds the build, and one the engine ended at a restart reruns the unit. A judge
// that ruled, holds for a reproduction that does not contradict red and fails for one that does, is a note with its
// ruling and reason (reproNoted), so the ruling gates nothing. A first judge that gave no verdict starts the second,
// and a second that gave none is a note with its reason. It reports false once the note is kept, so the green run
// starts.
func decideReproJudge(view View) (Action, bool) {
	build := view.Task.Build
	judges := view.ReproJudges
	if n := len(judges); n > 0 {
		step := judges[n-1]
		switch step.End {
		case "":
			return Action{Kind: ActNone}, true
		case endStopped:
			return holdStopped(build), true
		case endEngineRestarted:
			return restartUnit(build), true
		}
		verdict, reason := enginehost.ReproJudgeVerdict(step)
		switch {
		case verdict == evidence.VerdictHolds:
			return reproNoted(view, reproRuling("the judge ruled that it does not contradict red", reason))
		case verdict == evidence.VerdictFails:
			return reproNoted(view, reproRuling("the judge ruled that it contradicts red", reason))
		case n >= noVerdictLimit:
			return reproNoted(view, reproRuling("the judge gave no verdict twice", reason))
		}
	}
	return Action{
		Kind: ActStartStep, Unit: unitReproJudge,
		ID: reproJudgeID(unitScope(view.Epic, view.Task), view.Task.Seq, build, int64(len(judges))),
	}, true
}

// reproRuling returns the outcome a reproduction note records for a reproduction that exited 0 without timing out: the
// exit, then what its judge did, then the reason when there is one.
func reproRuling(ruling, reason string) string {
	outcome := reproExitZero + "; " + ruling
	if reason == "" {
		return outcome
	}
	return outcome + ": " + reason
}

// reproJudgeRefused returns the action for a reproduction judge whose start was refused with err: the refusal is a note
// on the build, as a ruling is, and once that note is kept the green run starts, so a judge that cannot start holds
// nothing up.
func reproJudgeRefused(view View, err error) Action {
	if action, noting := reproNoted(view, reproRuling("the judge did not start", err.Error())); noting {
		return action
	}
	return greenStart(view)
}

// reproJudgeStart returns the start of the reproduction judge step id for the task of view in epic. The step runs the
// judge the task's evidence item names, read-only at the revision the task's build reached, in the epic's repository,
// under the reproduction judge's agent gate (enginehost.ReproJudgeGate). Its prompt is enginehost.ReproJudgePrompt's for
// the task, its item's conclusion, its red run, the reproduction and git's diff stat from the task's base to its
// revision, read now on the epic's host. A view with no reproduction, a diff that errs and a prompt that cannot be built
// fail the start.
func (r *Runner) reproJudgeStart(
	ctx context.Context, epic store.EpicRow, view View, id string,
) (enginehost.StepStart, error) {
	task := view.Task
	if view.Repro == nil {
		return enginehost.StepStart{}, errors.New("the green unit has no reproduction to judge")
	}
	host, err := r.hostOf(epic)
	if err != nil {
		return enginehost.StepStart{}, err
	}
	base, revision := task.Build.Base, task.Build.Revision
	stat, err := runGit(ctx, host, epic.Repository, "diff", "--stat", base, revision)
	if err != nil {
		return enginehost.StepStart{}, fmt.Errorf("diff stat of %s: %w", taskLabel(task.Seq), err)
	}
	prompt, err := enginehost.ReproJudgePrompt(enginehost.ReproJudgeInput{
		Tasks: []store.EpicTask{task}, Conclusion: task.Evidence.Conclusion, Red: task.Red, Repro: *view.Repro,
		Base: base, Commit: revision, DiffStat: stat,
	})
	if err != nil {
		return enginehost.StepStart{}, fmt.Errorf("judge prompt of %s: %w", taskLabel(task.Seq), err)
	}
	gate := enginehost.ReproJudgeGate(task.Evidence.Judge)
	return enginehost.StepStart{
		ID:        id,
		ReadOnly:  true,
		Workspace: epic.Repository,
		Revision:  revision,
		AgentGate: &gate,
		Prompt:    prompt,
		Epic:      epic.ID,
		Task:      taskLabel(task.Seq),
		Purpose:   fmt.Sprintf("Judge %s's reproduction.", taskLabel(task.Seq)),
	}, nil
}

// readRepro sets the reproduction check of a test item's green unit in view, when it is stored, with the judge steps of
// that reproduction (reproJudgesOf).
func (r *Runner) readRepro(ctx context.Context, epic store.EpicRow, task store.EpicTask, view *View) error {
	repro, found, err := r.reproOf(ctx, epic, task)
	if err != nil || !found {
		return err
	}
	view.Repro = &repro
	view.ReproJudges, err = r.reproJudgesOf(ctx, epic, task, repro)
	return err
}

// reproJudgesOf returns the judge steps of repro, the reproduction of task's green unit, in judge order: each started
// under its derived id at the build's attempt and rerun, up to the first not started. A reproduction that did not exit
// 0 without timing out has no judge, so none are read for it.
func (r *Runner) reproJudgesOf(
	ctx context.Context, epic store.EpicRow, task store.EpicTask, repro store.NodeCheck,
) ([]store.StepRow, error) {
	if !store.PassedOnBase(repro) {
		return nil, nil
	}
	var judges []store.StepRow
	for judge := range int64(noVerdictLimit) {
		row, started, err := r.stepRow(ctx, reproJudgeID(unitScope(epic.ID, task), task.Seq, task.Build, judge))
		if err != nil {
			return nil, err
		}
		if !started {
			break
		}
		judges = append(judges, row)
	}
	return judges, nil
}
