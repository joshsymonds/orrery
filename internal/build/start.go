package build

import (
	"context"
	"fmt"
	"slices"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/guard"
	"github.com/joshsymonds/orrery/internal/reviewers"
	"github.com/joshsymonds/orrery/internal/roles"
	"github.com/joshsymonds/orrery/internal/store"
)

// The roles in the role file the implementer and verifier steps run under.
const (
	roleImplementer = "implementer"
	roleVerifier    = "verifier"
)

// reviewFloor is the task-stage reviewer whose prompt carries the notes its task's build holds and the guard's facts of
// the commit it reviews.
const reviewFloor = "floor"

// RoleError is a role file error: the role file does not name the agent a step's role runs under. The Runner parks on
// it.
type RoleError struct{ Err error }

// Error returns the text of the role file error.
func (e RoleError) Error() string { return e.Err.Error() }

// Unwrap returns the role file error.
func (e RoleError) Unwrap() error { return e.Err }

// startStep starts the step of action, under the id it carries, for the task of view in epic, and returns the id the
// engine started it under. An implement step runs the implementer's agent in its own worktree; a review step runs the
// task-stage reviewer its review gate names, read-only; a verify step runs the verifier's agent read-only on one
// finding; a design step runs the design judge read-only at the task's revision; an orchestrate step runs the
// orchestrator's agent read-only on the implementer's request. An error the engine returns on start is wrapped, so
// errors.Is still matches it.
func (r *Runner) startStep(ctx context.Context, epic store.EpicRow, view View, action Action) (string, error) {
	var (
		start enginehost.StepStart
		err   error
	)
	switch action.Unit {
	case StageImplement:
		start, err = r.implementerStart(ctx, epic, view.Task, action.ID)
	case StageReview:
		start, err = r.reviewStart(ctx, epic, view.Task, action.ID)
	case StageVerify:
		start, err = r.verifierStart(epic, view, action.ID)
	case StageDesign:
		start, err = r.designJudgeStart(epic, view, action.ID)
	case StageOrchestrate:
		start, err = r.orchestratorStart(ctx, epic, view, action.ID)
	default:
		return "", fmt.Errorf("unit %q has no step to start", action.Unit)
	}
	if err != nil {
		return "", err
	}
	// The engine's start runs to its end under a context a shutdown cannot cancel. A start cut short after its row is
	// committed ends the row failed under the derived id, and a restart then spends the attempt.
	id, err := r.steps.Start(context.WithoutCancel(ctx), start)
	if err != nil {
		return "", fmt.Errorf("start step %s: %w", start.ID, err)
	}
	return id, nil
}

// implementerStart returns the start of the implementer step id for task of epic. The step runs the roles file's
// implementer agent in the task's implement worktree (ImplementWorktree), added on a new task branch (TaskBranchRef) at
// the task's base when it does not exist and kept as it is when it does, so a later attempt or rerun continues from the
// earlier work on its branch. The step is checked from the task's base, with the brief, the task's files, and the
// command gate the epic names for the task. A gate that is an agent gate is an error. Rung 3 (store.OrchestratorRung)
// is the orchestrator's own writing attempt under the task's files, gate and reviews, so it runs the orchestrator's
// agent, or the planner's when the role file has no orchestrator key; no other rung changes the agent.
func (r *Runner) implementerStart(
	ctx context.Context, epic store.EpicRow, task store.EpicTask, id string,
) (enginehost.StepStart, error) {
	var (
		agent string
		err   error
	)
	if task.Build.Attempt == store.OrchestratorRung {
		agent, err = roles.AgentOr(r.getenv, roleOrchestrator, rolePlanner)
	} else {
		agent, err = roles.Agent(r.getenv, roleImplementer)
	}
	if err != nil {
		return enginehost.StepStart{}, RoleError{Err: err}
	}
	gate, err := ResolveGate(ctx, epic, task.Gate, r.getenv)
	if err != nil {
		return enginehost.StepStart{}, err
	}
	if gate.Agent != "" {
		return enginehost.StepStart{}, fmt.Errorf("gate %s is an agent gate, not a command gate", task.Gate)
	}
	path := ImplementWorktree(r.stateDir, epic.ID, task)
	err = r.git.AddTaskWorktree(ctx, epic.Repository, epic.ID, task.Seq, path, task.Build.Base)
	if err != nil {
		return enginehost.StepStart{}, fmt.Errorf("prepare worktree: %w", err)
	}
	red, redLog, redStderr := r.redEvidence(ctx, task)
	return enginehost.StepStart{
		ID:          id,
		Agent:       agent,
		Prompt:      Brief(epic, task, gate, red, redLog, redStderr, task.Build.Failure),
		Schema:      ImplementerSchema,
		Workspace:   path,
		Revision:    task.Build.Base,
		Owned:       task.Files,
		CommandGate: &gate,
		DoneValue:   "done",
		Epic:        epic.ID,
		Task:        taskLabel(task.Seq),
	}, nil
}

// redEvidence returns the red run of task, the stdout log the run left, and the stderr log it left. A log the run does
// not have is not read, and a log that cannot be read is logged and left empty, so the brief carries the run without
// that log.
func (r *Runner) redEvidence(ctx context.Context, task store.EpicTask) (store.EvidenceRun, []byte, []byte) {
	if task.Red == nil {
		return store.EvidenceRun{}, nil, nil
	}
	var redLog, redStderr []byte
	if task.Red.LogHash != "" {
		data, _, err := r.evidence.Artifact(ctx, task.Red.ID, "")
		if err != nil {
			r.logger.WarnContext(ctx, "read red log", "run", task.Red.ID, "err", err)
		} else {
			redLog = data
		}
	}
	if task.Red.StderrHash != "" {
		data, _, err := r.evidence.StderrLog(ctx, task.Red.ID)
		if err != nil {
			r.logger.WarnContext(ctx, "read red stderr log", "run", task.Red.ID, "err", err)
		} else {
			redStderr = data
		}
	}
	return *task.Red, redLog, redStderr
}

// reviewStart returns the start of the review step id for task of epic: the epic's task-stage reviewer named by the
// review gate at the task's gate position, run read-only at the revision the task's build reached. A model reviewer
// carries its inline agent, and an agent reviewer's prompt heads the review prompt. The test_parsimony reviewer's
// prompt carries the output of the gate the implementer's commit passed (passedGate), compared with the epic's base
// measurement of test cost (readBaseCost). The floor reviewer's prompt carries the facts guard.Check gives the commit
// between the task's base and revision, read now; a check that errs fails the start, as a git error does at any other
// start.
func (r *Runner) reviewStart(
	ctx context.Context, epic store.EpicRow, task store.EpicTask, id string,
) (enginehost.StepStart, error) {
	pos := task.Build.GatePos
	if pos < 0 || pos >= int64(len(epic.ReviewGates)) {
		return enginehost.StepStart{}, fmt.Errorf("review gate %d of epic %s is not defined", pos, epic.ID)
	}
	name := epic.ReviewGates[pos]
	taskReviewers := reviewers.ForStage(epic.Reviewers, reviewers.StageTask)
	i := slices.IndexFunc(taskReviewers, func(rev reviewers.Reviewer) bool { return rev.Name == name })
	if i < 0 {
		return enginehost.StepStart{}, fmt.Errorf(
			"review gate %s of epic %s is not a task-stage reviewer", name, epic.ID)
	}
	rev := taskReviewers[i]
	agents, err := rev.Agents()
	if err != nil {
		return enginehost.StepStart{}, fmt.Errorf("review gate %s: %w", name, err)
	}
	gate := rev.Gate()
	base, revision := task.Build.Base, task.Build.Revision
	var gateRun store.StepCheck
	var baseCost map[string]float64
	if name == reviewers.TestParsimony {
		gateRun = r.passedGate(ctx, epic.ID, task)
		baseCost = r.readBaseCost(ctx, epic.ID)
	}
	var facts []string
	if name == reviewFloor {
		judgments, factErr := guard.Check(ctx, epic.Repository, base, revision, guardSpec(epic, task))
		if factErr != nil {
			return enginehost.StepStart{}, fmt.Errorf("guard facts of %s: %w", taskLabel(task.Seq), factErr)
		}
		facts = guard.FactLines(judgments)
	}
	prompt := ReviewPrompt(epic, task, name, base, revision, gateRun, baseCost, facts...)
	if rev.Agent != "" && rev.Prompt != "" {
		prompt = rev.Prompt + "\n\n" + prompt
	}
	return enginehost.StepStart{
		ID:        id,
		ReadOnly:  true,
		Workspace: epic.Repository,
		Revision:  revision,
		AgentGate: &gate,
		Agents:    agents,
		Prompt:    prompt,
		Epic:      epic.ID,
		Task:      taskLabel(task.Seq),
	}, nil
}

// passedGate returns the gate check the task's newest implementer step ran last, the gate run its commit passed, with the
// kept tails of the gate's stdout and stderr. It returns the zero check when no implementer step of the task ran its
// gate. A store read that fails is logged and returns the zero check, so the review runs without the gate's output.
func (r *Runner) passedGate(ctx context.Context, epic string, task store.EpicTask) store.StepCheck {
	rows, err := r.store.LabelledSteps(ctx, epic, taskLabel(task.Seq))
	if err != nil {
		r.logger.WarnContext(ctx, "read task steps", "epic", epic, "task", task.Seq, "err", err)
		return store.StepCheck{}
	}
	for _, row := range slices.Backward(rows) {
		if row.Schema != ImplementerSchema {
			continue
		}
		for _, check := range slices.Backward(row.Checks) {
			if check.Name == gateCheck {
				return check
			}
		}
	}
	return store.StepCheck{}
}

// readBaseCost returns the CPU seconds of each package the epic's cost node measured at its base (reviewers.Costs of
// its cost checks' kept stdout and stderr). A store read that fails is logged and returns nil, so the review carries
// the gate's budget lines without a base to compare them with.
func (r *Runner) readBaseCost(ctx context.Context, epic string) map[string]float64 {
	checks, err := r.store.BaseCost(ctx, epic)
	if err != nil {
		r.logger.WarnContext(ctx, "read base cost", "epic", epic, "err", err)
		return nil
	}
	var outputs []string
	for _, check := range checks {
		outputs = append(outputs, check.Stdout, check.Stderr)
	}
	return reviewers.Costs(outputs...)
}

// verifierStart returns the start of the verifier step id for the finding at the task's finding position in the
// review of view. The step runs the roles file's verifier agent read-only at the revision the task's build reached,
// with the verifier schema and its return check.
func (r *Runner) verifierStart(epic store.EpicRow, view View, id string) (enginehost.StepStart, error) {
	agent, err := roles.Agent(r.getenv, roleVerifier)
	if err != nil {
		return enginehost.StepStart{}, RoleError{Err: err}
	}
	findings := reviewFindings(view)
	pos := view.Task.Build.FindingPos
	if pos < 0 || pos >= int64(len(findings)) {
		return enginehost.StepStart{}, fmt.Errorf("finding %d of the review of T%d is not defined", pos, view.Task.Seq)
	}
	task := view.Task
	subject := Subject{Epic: epic, Task: &task, Base: task.Build.Base, Revision: task.Build.Revision}
	return VerifierStart(subject, findings[pos], id, agent), nil
}

// designJudgeStart returns the start of the design judge step id for the design task of view in epic. The step runs the
// roles file's verifier agent read-only at the revision the task's build reached, in the epic's repository, with the
// design judge's prompt, schema and return check.
func (r *Runner) designJudgeStart(epic store.EpicRow, view View, id string) (enginehost.StepStart, error) {
	agent, err := roles.Agent(r.getenv, roleVerifier)
	if err != nil {
		return enginehost.StepStart{}, RoleError{Err: err}
	}
	task := view.Task
	return enginehost.StepStart{
		ID:          id,
		Agent:       agent,
		ReadOnly:    true,
		Workspace:   epic.Repository,
		Revision:    task.Build.Revision,
		Prompt:      DesignJudgePrompt(epic, task),
		Schema:      DesignJudgeSchema,
		ReturnCheck: DesignJudgeCheck,
		DoneValue:   statusDone,
		Epic:        epic.ID,
		Task:        taskLabel(task.Seq),
	}, nil
}

// orchestratorStart returns the start of the orchestrator step id that decides the request of view's task. The step runs
// the role file's orchestrator agent, or the planner's when the role file has no orchestrator key, read-only at the
// task's base in the epic's repository, with the orchestrator's prompt, schema and return check, which checks the
// return against the epic's tasks as they stand at the start.
func (r *Runner) orchestratorStart(
	ctx context.Context, epic store.EpicRow, view View, id string,
) (enginehost.StepStart, error) {
	agent, err := roles.AgentOr(r.getenv, roleOrchestrator, rolePlanner)
	if err != nil {
		return enginehost.StepStart{}, RoleError{Err: err}
	}
	peers, err := r.peers(ctx, epic)
	if err != nil {
		return enginehost.StepStart{}, err
	}
	prompt, err := r.orchestratorPrompt(ctx, epic, view, peers)
	if err != nil {
		return enginehost.StepStart{}, err
	}
	task := view.Task
	return enginehost.StepStart{
		ID:          id,
		Agent:       agent,
		ReadOnly:    true,
		Workspace:   epic.Repository,
		Revision:    task.Build.Base,
		Prompt:      prompt,
		Schema:      OrchestratorSchema,
		ReturnCheck: OrchestratorCheck(task, peers),
		DoneValue:   statusDone,
		Epic:        epic.ID,
		Task:        taskLabel(task.Seq),
	}, nil
}
