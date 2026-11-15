package build

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/roles"
	"github.com/joshsymonds/orrery/internal/store"
)

// The roles in the role file the implementer and verifier steps run under.
const (
	roleImplementer = "implementer"
	roleVerifier    = "verifier"
)

// RoleError is a role file error: the role file does not name the agent a step's role runs under. The Runner parks on
// it.
type RoleError struct{ Err error }

// Error returns the text of the role file error.
func (e RoleError) Error() string { return e.Err.Error() }

// Unwrap returns the role file error.
func (e RoleError) Unwrap() error { return e.Err }

// startStep starts the step of action, under the id it carries, for the task of view in epic, and returns the id the
// engine started it under. An implement step runs the implementer's agent in its own worktree; a review step runs the
// epic's review gate read-only; a verify step runs the verifier's agent read-only on one finding. An error the engine
// returns on start is wrapped, so errors.Is still matches it.
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
// implementer agent in its own detached worktree at the task's base, under the task's directory, with the brief, the
// task's files, and the command gate the epic names for the task. A gate that is an agent gate is an error.
func (r *Runner) implementerStart(
	ctx context.Context, epic store.EpicRow, task store.EpicTask, id string,
) (enginehost.StepStart, error) {
	agent, err := roles.Agent(r.getenv, roleImplementer)
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
	path := filepath.Join(TaskDir(r.stateDir, epic.ID, task.Seq), id)
	err = AddWorktree(ctx, epic.Repository, path, task.Build.Base)
	if err != nil {
		return enginehost.StepStart{}, err
	}
	red, redLog := r.redEvidence(ctx, task)
	return enginehost.StepStart{
		ID:          id,
		Agent:       agent,
		Prompt:      Brief(epic, task, red, redLog, task.Build.Failure),
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

// redEvidence returns the red run of task and the log the run left. A log that cannot be read is logged and left
// empty, so the brief carries the run without its log.
func (r *Runner) redEvidence(ctx context.Context, task store.EpicTask) (store.EvidenceRun, []byte) {
	if task.Red == nil {
		return store.EvidenceRun{}, nil
	}
	if task.Red.LogHash == "" {
		return *task.Red, nil
	}
	redLog, _, err := r.evidence.Artifact(ctx, task.Red.ID, "")
	if err != nil {
		r.logger.WarnContext(ctx, "read red log", "run", task.Red.ID, "err", err)
		return *task.Red, nil
	}
	return *task.Red, redLog
}

// reviewStart returns the start of the review step id for task of epic: the review gate at the task's gate position,
// run read-only at the revision the task's build reached. A gate that is a command gate is an error.
func (r *Runner) reviewStart(
	ctx context.Context, epic store.EpicRow, task store.EpicTask, id string,
) (enginehost.StepStart, error) {
	pos := task.Build.GatePos
	if pos < 0 || pos >= int64(len(epic.ReviewGates)) {
		return enginehost.StepStart{}, fmt.Errorf("review gate %d of epic %s is not defined", pos, epic.ID)
	}
	name := epic.ReviewGates[pos]
	gate, err := ResolveGate(ctx, epic, name, r.getenv)
	if err != nil {
		return enginehost.StepStart{}, err
	}
	if gate.Agent == "" {
		return enginehost.StepStart{}, fmt.Errorf("gate %s is a command gate, not an agent gate", name)
	}
	base, revision := task.Build.Base, task.Build.Revision
	return enginehost.StepStart{
		ID:        id,
		ReadOnly:  true,
		Workspace: epic.Repository,
		Revision:  revision,
		AgentGate: &gate,
		Prompt:    ReviewPrompt(epic, task, base, revision),
		Epic:      epic.ID,
		Task:      taskLabel(task.Seq),
	}, nil
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
	base, revision := task.Build.Base, task.Build.Revision
	return enginehost.StepStart{
		ID:          id,
		ReadOnly:    true,
		Workspace:   epic.Repository,
		Revision:    revision,
		Agent:       agent,
		Schema:      VerifierSchema,
		Prompt:      VerifierPrompt(epic, task, base, revision, findings[pos]),
		ReturnCheck: VerifierCheck,
		DoneValue:   "done",
		Epic:        epic.ID,
		Task:        taskLabel(task.Seq),
	}, nil
}
