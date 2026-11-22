package interpret

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/build"
	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/roles"
	"github.com/joshsymonds/orrery/internal/store"
)

// taskIntegratorRole is the role text of the task integrator: the writing step that merges a task's revision into the
// candidate when the two conflict, and commits the merge, or returns clash when the two sides cannot both hold.
//
//go:embed task_integrator.md
var taskIntegratorRole string

// taskWorktree is the name of the worktree a task integrator round runs in under the directory of the round.
const taskWorktree = "wt"

// The verdicts a task integrator returns: merged when it commits the task's revision merged into the candidate, and
// clash when the two sides cannot both hold. A round is recorded with the same verdicts.
const (
	verdictMerged = kinds.RoundMerged
	verdictClash  = kinds.RoundClash
)

// TaskIntegratorSchema is the schema the task integrator returns: its status, its verdict, the merge commit when the
// verdict is merged, the account of the merge or the reason for clash, and each change outside the conflicts.
const TaskIntegratorSchema = `{
  "title": "Task integrator return",
  "type": "object",
  "additionalProperties": false,
  "required": ["status", "verdict", "commit", "explanation", "other_changes"],
  "properties": {
    "status": {"type": "string", "enum": ["done"]},
    "verdict": {"type": "string", "enum": ["merged", "clash"]},
    "commit": {
      "type": "string",
      "description": "The full id of the merge commit when verdict is merged; empty when verdict is clash."
    },
    "explanation": {
      "type": "string",
      "description": "When verdict is clash, why both sides cannot hold, path by path; otherwise the account of the merge."
    },
    "other_changes": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["path", "reason"],
        "properties": {"path": {"type": "string"}, "reason": {"type": "string"}}
      }
    }
  }
}`

// taskIntegratorReturn is the structured return of a task integrator. Its other_changes are read by
// parseTaskIntegratorReturn, as the release integrator's are.
type taskIntegratorReturn struct {
	Verdict      string                 `json:"verdict"`
	Commit       string                 `json:"commit"`
	Explanation  string                 `json:"explanation"`
	OtherChanges []taskIntegratorChange `json:"-"`
}

// taskIntegratorChange is one change a task integrator made outside the conflicts: the path and why the task needed it.
type taskIntegratorChange struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// parseTaskIntegratorReturn decodes raw, a task integrator's return, with its other_changes. A return that is not a
// JSON object of that shape is an error.
func parseTaskIntegratorReturn(raw []byte) (taskIntegratorReturn, error) {
	var ret taskIntegratorReturn
	if err := json.Unmarshal(raw, &ret); err != nil {
		return taskIntegratorReturn{}, fmt.Errorf("the return is not a JSON object: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return taskIntegratorReturn{}, fmt.Errorf("the return is not a JSON object: %w", err)
	}
	if changes, ok := fields[otherChangesKey]; ok {
		if err := json.Unmarshal(changes, &ret.OtherChanges); err != nil {
			return taskIntegratorReturn{}, fmt.Errorf("%s: %w", otherChangesKey, err)
		}
	}
	return ret, nil
}

// taskMerge returns the merge a task integrator round finishes: the task's revision, as origin, merged into the
// candidate in repo, in the worktree under dir. Its conflicts are not set until the merge is started.
func taskMerge(repo, dir, candidate, revision string) integratorMerge {
	return integratorMerge{repo: repo, worktree: filepath.Join(dir, taskWorktree), tip: candidate, origin: revision}
}

// startTaskMerge makes the worktree of merge afresh under dir on host, detached at the candidate, and merges the task's
// revision into it without a commit (startMerge). The conflicts stay in place: it returns the paths they leave unmerged.
func startTaskMerge(ctx context.Context, host hostexec.Host, merge integratorMerge, dir string) ([]string, error) {
	if err := startMerge(ctx, host, merge, dir); err != nil {
		return nil, fmt.Errorf("merge the task's revision %s into %s: %w", merge.origin, merge.tip, err)
	}
	return unmergedPaths(ctx, merge.worktree)
}

// taskIntegratorCheck returns the return check of a task integrator step that finishes merge. A merged return's commit
// is checked by the checks a release integrator's commit meets (integratorFailures), with its other_changes as the
// changes listed outside the conflicts. A clash return is checked by clashFailures, and any other verdict fails. The
// tree git's own merge writes is computed once, here.
func taskIntegratorCheck(ctx context.Context, merge integratorMerge) (func(json.RawMessage) []string, error) {
	tree, err := mergeTreeOf(ctx, merge)
	if err != nil {
		return nil, err
	}
	checkCtx := context.WithoutCancel(ctx)
	return func(value json.RawMessage) []string {
		ret, decodeErr := parseTaskIntegratorReturn(value)
		if decodeErr != nil {
			return []string{"task integrator return: " + decodeErr.Error()}
		}
		switch ret.Verdict {
		case verdictMerged:
			return integratorFailures(checkCtx, merge, tree, mergedAsIntegrator(ret))
		case verdictClash:
			return clashFailures(ret)
		default:
			return []string{fmt.Sprintf("verdict: %q is neither %s nor %s", ret.Verdict, verdictMerged, verdictClash)}
		}
	}, nil
}

// mergedAsIntegrator returns ret, a merged return, as the release integrator's return the commit checks read: its
// commit and its changes outside the conflicts, each with its reason as its change.
func mergedAsIntegrator(ret taskIntegratorReturn) integratorReturn {
	changes := make([]integratorChange, 0, len(ret.OtherChanges))
	for _, change := range ret.OtherChanges {
		changes = append(changes, integratorChange{Path: change.Path, Change: change.Reason})
	}
	return integratorReturn{Commit: ret.Commit, OtherChanges: changes}
}

// clashFailures returns the lines on which the clash return ret fails: it gives no explanation, or it names a commit.
func clashFailures(ret taskIntegratorReturn) []string {
	var failures []string
	if strings.TrimSpace(ret.Explanation) == "" {
		failures = append(failures, "explanation: a clash return says why both sides cannot hold")
	}
	if strings.TrimSpace(ret.Commit) != "" {
		failures = append(failures, "commit: a clash return names no commit, so it must be empty")
	}
	return failures
}

// taskIntegrator carries out one ActTaskIntegrator action of an integrate run, as the first of its items says: the start
// of a round's step, the record of a round whose step ended, the compose of a round's merged commit into the candidate,
// or the failure of a round.
func (r *Runner) taskIntegrator(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	switch act.Items[0] {
	case kinds.TaskStart:
		return r.startTaskRound(ctx, epic, act, at)
	case kinds.TaskRecord:
		return r.recordTaskRound(ctx, epic, act, at)
	case kinds.TaskCompose:
		return r.composeTaskRound(ctx, epic, act, at)
	case kinds.TaskFail:
		return r.failTaskRound(ctx, epic, act, at)
	default:
		return fmt.Errorf("task integrator work %s is not one the executor carries out", act.Items[0])
	}
}

// roundOf returns the round act is for, which is its second item.
func roundOf(act Action) (int64, error) {
	round, err := strconv.ParseInt(act.Items[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("read the round of node %s: %w", act.Path, err)
	}
	return round, nil
}

// startTaskRound starts the step of the round act names: the task integrator, under the role file's integrator agent, in
// a worktree that holds the merge of the task's revision into the candidate with its conflicts in place. A round whose
// step cannot start is recorded ended with why, which leaves its task out, and so is a start refused for good.
func (r *Runner) startTaskRound(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	if r.steps == nil {
		return missingService("steps")
	}
	round, err := roundOf(act)
	if err != nil {
		return err
	}
	dir := r.taskRoundDir(act.IDs[0])
	start, err := r.taskRoundStart(ctx, epic, act, dir)
	if err != nil {
		return r.endTaskRound(ctx, epic, act, round, dir, "the task integrator did not start: "+err.Error(), at)
	}
	if _, err = r.steps.Start(ctx, start); err != nil {
		if !refusedForGood(err) {
			return fmt.Errorf("start task integrator %s: %w", start.ID, err)
		}
		return r.endTaskRound(ctx, epic, act, round, dir, "the task integrator did not start: "+err.Error(), at)
	}
	return nil
}

// taskRoundDir returns the directory the worktree of the step id runs in is under. A runner with no state directory
// makes no worktree, so its directory is "".
func (r *Runner) taskRoundDir(id string) string {
	if r.stateDir == "" {
		return ""
	}
	return filepath.Join(r.stateDir, releaseWorktrees, id)
}

// taskRoundStart returns the start of the step of the round act names. It merges the task's revision into the candidate
// in a worktree under dir, leaving the conflicts in place, and its prompt carries the task, the merge, the conflicted
// paths and the failure of the round before (Output). Its return check is taskIntegratorCheck's. It fails when dir is
// "", since the runner has no state directory to make the worktree under.
func (r *Runner) taskRoundStart(
	ctx context.Context, epic store.EpicRow, act Action, dir string,
) (enginehost.StepStart, error) {
	if dir == "" {
		return enginehost.StepStart{}, errors.New("the runner was not given a state directory")
	}
	agent, err := roles.AgentOr(r.getenv, roleIntegrator, roleRelease)
	if err != nil {
		return enginehost.StepStart{}, fmt.Errorf("read the role of the task integrator: %w", err)
	}
	task, found := taskBySeq(epic.Tasks, act.Tasks[0])
	if !found {
		return enginehost.StepStart{}, fmt.Errorf("task %d of epic %s is not stored", act.Tasks[0], epic.ID)
	}
	merge := taskMerge(epic.Repository, dir, act.Items[2], act.Items[3])
	host, err := r.hostOf(epic)
	if err != nil {
		return enginehost.StepStart{}, err
	}
	conflicts, err := startTaskMerge(ctx, host, merge, dir)
	if err != nil {
		return enginehost.StepStart{}, err
	}
	merge.conflicts = conflicts
	prompt, err := taskIntegratorPrompt(ctx, merge, epic.Tasks, task, act.Output)
	if err != nil {
		return enginehost.StepStart{}, err
	}
	check, err := taskIntegratorCheck(ctx, merge)
	if err != nil {
		return enginehost.StepStart{}, err
	}
	return enginehost.StepStart{
		ID: act.IDs[0], Agent: agent, Prompt: prompt, Schema: TaskIntegratorSchema, Workspace: merge.worktree,
		Revision: merge.tip, Unowned: true, DoneValue: releaseDone, Epic: epic.ID, Task: act.Path, ReturnCheck: check,
	}, nil
}

// recordTaskRound records the round act names once its step has ended: merged with the commit its return names, pinned
// under the round's own ref, when the return merged the task; clash with its explanation when it found the sides cannot
// hold; and otherwise ended with how the step ended. The round's worktree is removed once it is recorded.
func (r *Runner) recordTaskRound(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	round, err := roundOf(act)
	if err != nil {
		return err
	}
	row, err := r.taskStep(ctx, epic.ID, act.Path, act.IDs[0])
	if err != nil {
		return err
	}
	dir := r.taskRoundDir(act.IDs[0])
	seq := act.Tasks[0]
	verdict, err := r.judgeTaskRound(ctx, epic, row, seq)
	if err != nil {
		return err
	}
	if verdict.verdict == verdictMerged {
		host, hostErr := r.hostOf(epic)
		if hostErr != nil {
			return fmt.Errorf("pin the task integrator merge of node %s: %w", act.Path, hostErr)
		}
		if err = build.Pin(ctx, host, epic.Repository, taskPin(epic, act.Run, seq, round), verdict.head); err != nil {
			return fmt.Errorf("pin the task integrator merge of node %s: %w", act.Path, err)
		}
	}
	record := roundRecordOf(act, round)
	record.Verdict, record.Head, record.Explanation = verdict.verdict, verdict.head, verdict.explanation
	return r.storeRound(ctx, epic, record, dir, at)
}

// roundVerdict is the verdict a round is recorded with: its verdict, the commit a merged round left the candidate on and
// the explanation its step gave.
type roundVerdict struct {
	verdict, head, explanation string
}

// decodeTaskReturn returns the return a step stored as raw, with "" as its reason, or the reason raw is not one.
func decodeTaskReturn(raw string) (taskIntegratorReturn, string) {
	ret, err := parseTaskIntegratorReturn([]byte(raw))
	if err != nil {
		return taskIntegratorReturn{}, err.Error()
	}
	return ret, ""
}

// judgeTaskRound returns the verdict the step row of the round of task seq records. A step that did not end done, or
// whose return does not decode, is ended with how it ended. The return is checked again here, because a return stored
// after a restart is not checked by its return check: a merged return whose commit fails the engine's checks, or a clash
// that names a commit or gives no explanation, is ended too.
func (r *Runner) judgeTaskRound(
	ctx context.Context, epic store.EpicRow, row store.StepRow, seq int64,
) (roundVerdict, error) {
	if row.End != releaseDone {
		return roundVerdict{verdict: kinds.RoundEnded, explanation: releaseEndReason("task integrator", row)}, nil
	}
	ret, reason := decodeTaskReturn(row.Result)
	if reason != "" {
		return roundVerdict{verdict: kinds.RoundEnded, explanation: reason}, nil
	}
	task, found := taskBySeq(epic.Tasks, seq)
	if !found {
		return roundVerdict{}, fmt.Errorf("task %d of epic %s is not stored", seq, epic.ID)
	}
	merge := integratorMerge{
		repo: epic.Repository, worktree: row.Workspace, tip: row.Revision, origin: task.Build.Revision,
	}
	conflicts, err := taskConflicts(ctx, merge)
	if err != nil {
		return roundVerdict{}, err
	}
	merge.conflicts = conflicts
	switch ret.Verdict {
	case verdictMerged:
		return judgeMerged(ctx, merge, ret)
	case verdictClash:
		if failures := clashFailures(ret); len(failures) > 0 {
			return roundVerdict{
				verdict:     kinds.RoundEnded,
				explanation: "the task integrator's clash was refused: " + strings.Join(failures, "; "),
			}, nil
		}
		return roundVerdict{verdict: kinds.RoundClash, explanation: ret.Explanation}, nil
	default:
		return roundVerdict{
			verdict:     kinds.RoundEnded,
			explanation: fmt.Sprintf("verdict: %q is neither %s nor %s", ret.Verdict, verdictMerged, verdictClash),
		}, nil
	}
}

// judgeMerged returns the verdict of a merged return ret: merged with the commit it names when the commit passes the
// engine's checks, and ended with each check it fails otherwise.
func judgeMerged(ctx context.Context, merge integratorMerge, ret taskIntegratorReturn) (roundVerdict, error) {
	head, failures, err := integratorCommit(ctx, merge, mergedAsIntegrator(ret))
	if err != nil {
		return roundVerdict{}, err
	}
	if len(failures) > 0 {
		return roundVerdict{
			verdict:     kinds.RoundEnded,
			explanation: "the task integrator's commit was refused: " + strings.Join(failures, "; "),
		}, nil
	}
	return roundVerdict{verdict: kinds.RoundMerged, head: head, explanation: ret.Explanation}, nil
}

// composeTaskRound composes the task of the round act names into the candidate: the run's revision becomes the round's
// commit, and its result lists the task as composed rather than left out, in one write.
func (r *Runner) composeTaskRound(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	round, err := roundOf(act)
	if err != nil {
		return err
	}
	seq, commit := act.Tasks[0], act.Items[2]
	result, err := composedRound(act.Run.Result, seq, commit)
	if err != nil {
		return err
	}
	err = r.store.ComposeIntegrateRound(ctx, epic.ID, act.Path, act.Run.Attempt, seq, round, commit, result, at)
	if err != nil {
		return fmt.Errorf("compose task %d of node %s: %w", seq, act.Path, err)
	}
	return nil
}

// failTaskRound fails the round act names with its Output, the failure the next round reads.
func (r *Runner) failTaskRound(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	round, err := roundOf(act)
	if err != nil {
		return err
	}
	err = r.store.FailIntegrateRound(ctx, epic.ID, act.Path, act.Run.Attempt, act.Tasks[0], round, act.Output, at)
	if err != nil {
		return fmt.Errorf("fail task %d of node %s: %w", act.Tasks[0], act.Path, err)
	}
	return nil
}

// endTaskRound records the round act names as ended with explanation, and removes the worktree its step ran in under dir.
func (r *Runner) endTaskRound(
	ctx context.Context, epic store.EpicRow, act Action, round int64, dir, explanation string, at time.Time,
) error {
	record := roundRecordOf(act, round)
	record.Verdict, record.Explanation = kinds.RoundEnded, explanation
	return r.storeRound(ctx, epic, record, dir, at)
}

// storeRound stores record, the round of an integrate run's task integrator, once, and then removes the worktree of its
// step under dir, which the stored round no longer needs. A dir of "" has no worktree to remove.
func (r *Runner) storeRound(
	ctx context.Context, epic store.EpicRow, record store.IntegrateRound, dir string, at time.Time,
) error {
	if err := r.store.RecordIntegrateRound(ctx, epic.ID, record, at); err != nil {
		return fmt.Errorf("record round %d of task %d of node %s: %w", record.Round, record.Task, record.Path, err)
	}
	if dir == "" {
		return nil
	}
	host, err := r.hostOf(epic)
	if err != nil {
		return fmt.Errorf("remove the worktree of task integrator %s: %w", record.Step, err)
	}
	if err = build.RemoveWorktrees(ctx, host, epic.Repository, dir); err != nil {
		return fmt.Errorf("remove the worktree of task integrator %s: %w", record.Step, err)
	}
	return nil
}

// roundRecordOf returns the record of the round act names, round, with the run, task and step it is for.
func roundRecordOf(act Action, round int64) store.IntegrateRound {
	return store.IntegrateRound{
		Path: act.Path, Attempt: act.Run.Attempt, Task: act.Tasks[0], Round: round, Step: act.IDs[0],
	}
}

// taskStep returns the step with id among the steps labeled with the integrate run at path of epic.
func (r *Runner) taskStep(ctx context.Context, epic, path, id string) (store.StepRow, error) {
	labeled, err := r.store.LabelledSteps(ctx, epic, path)
	if err != nil {
		return store.StepRow{}, fmt.Errorf("read the steps of node %s of epic %s: %w", path, epic, err)
	}
	i := slices.IndexFunc(labeled, func(row store.StepRow) bool { return row.ID == id })
	if i < 0 {
		return store.StepRow{}, fmt.Errorf("step %s of epic %s is not stored", id, epic)
	}
	return labeled[i], nil
}

// roundSteps returns the steps of the task integrator rounds of the running integrate runs among runs. EpicFlow reads no
// step of an integrate run, and a round's step is labeled with its integrate run's path, so each running integrate run's
// labeled steps are read here for the rows its rounds are decided on.
func (r *Runner) roundSteps(ctx context.Context, epic string, runs []store.NodeRun) ([]store.StepRow, error) {
	var steps []store.StepRow
	for _, run := range runs {
		if run.Kind != kindIntegrate || run.State != store.NodeRunning {
			continue
		}
		labeled, err := r.store.LabelledSteps(ctx, epic, run.Path)
		if err != nil {
			return nil, fmt.Errorf("read the task integrator steps of node %s of epic %s: %w", run.Path, epic, err)
		}
		steps = append(steps, labeled...)
	}
	return steps, nil
}

// taskConflicts returns the paths git's merge of the task's revision into the candidate conflicts at, as git's merge tree
// names them.
func taskConflicts(ctx context.Context, merge integratorMerge) ([]string, error) {
	out, _, err := mergeTree(ctx, merge.repo, merge.tip, merge.origin)
	if err != nil {
		return nil, err
	}
	_, conflicts := mergedTree(out)
	return conflicts, nil
}

// taskPin returns the ref the merged commit of a round is pinned under: the epic's slug, the integrate run and its
// attempt, then the task and the round, so each round pins its own commit.
func taskPin(epic store.EpicRow, run store.NodeRun, seq, round int64) string {
	return fmt.Sprintf("refs/orrery/%s/integrate/%s-%d-T%d-%d", build.Slug(epic.ID), run.ID, run.Attempt, seq, round)
}

// taskBySeq returns the task with seq among tasks, and whether there is one.
func taskBySeq(tasks []store.EpicTask, seq int64) (store.EpicTask, bool) {
	i := slices.IndexFunc(tasks, func(task store.EpicTask) bool { return task.Seq == seq })
	if i < 0 {
		return store.EpicTask{}, false
	}
	return tasks[i], true
}
