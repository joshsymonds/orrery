package interpret

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/joshsymonds/orrery/internal/build"
	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/roles"
	"github.com/joshsymonds/orrery/internal/store"
)

// integratorRole is the role text of the release integrator: the writing step that finishes a merge of origin's commit
// into the release tip that conflicts, and commits it.
//
//go:embed integrator.md
var integratorRole string

// roleIntegrator is the role the release integrator runs under. A role file with no key for it runs the integrator under
// the agent the release role names (roles.AgentOr).
const roleIntegrator = "integrator"

// integratorPart names the directory of a release run's integrator worktree: store.NodeWorkID of the run's id, its
// attempt and this part. Every round of the attempt runs in that one worktree.
const integratorPart = "integrator"

// integratorStepName is the step a release integrator's release run records.
const integratorStepName = "release integrator"

// IntegratorSchema is the schema the release integrator returns: its status, the merge commit, how each conflicted path
// was resolved, each change outside the conflicts, the checks it ran, each path it stopped on with the side its decision
// is on, and an account of the merge.
const IntegratorSchema = `{
  "title": "Release integrator return",
  "type": "object",
  "additionalProperties": false,
  "required": ["status", "commit", "resolutions", "other_changes", "checks", "unresolved", "evidence"],
  "properties": {
    "status": {"type": "string", "enum": ["done", "blocked"]},
    "commit": {"type": "string"},
    "resolutions": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["path", "took", "kept"],
        "properties": {
          "path": {"type": "string"},
          "took": {"type": "string", "enum": ["both", "origin", "candidate", "regenerated"]},
          "kept": {
            "type": "string",
            "description": "What stayed from each side; when took is origin or candidate, why the other side loses nothing."
          }
        }
      }
    },
    "other_changes": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["path", "change"],
        "properties": {"path": {"type": "string"}, "change": {"type": "string"}}
      }
    },
    "checks": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["command", "result"],
        "properties": {"command": {"type": "string"}, "result": {"type": "string"}}
      }
    },
    "unresolved": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["path", "candidate", "origin", "decision", "side"],
        "properties": {
          "path": {"type": "string"},
          "candidate": {"type": "string"},
          "origin": {"type": "string"},
          "decision": {"type": "string", "description": "The decision that would settle this path."},
          "side": {
            "type": "string",
            "enum": ["candidate", "merge"],
            "description": "candidate when the decision is a change to the epic's own candidate, made before the merge; merge when it is how to resolve the merge itself."
          }
        }
      }
    },
    "evidence": {"type": "string"}
  }
}`

// integratorReturn is the structured return of a release integrator, as the step stores it.
type integratorReturn struct {
	Status      string `json:"status"`
	Commit      string `json:"commit"`
	Resolutions []struct {
		Path string `json:"path"`
		Took string `json:"took"`
		Kept string `json:"kept"`
	} `json:"resolutions"`
	OtherChanges []integratorChange     `json:"-"`
	Unresolved   []integratorUnresolved `json:"unresolved"`
	Evidence     string                 `json:"evidence"`
}

// otherChangesKey is the key of the changes a release integrator's return lists outside the conflicts, which
// parseIntegratorReturn reads into its OtherChanges.
const otherChangesKey = "other_changes"

// parseIntegratorReturn decodes raw, a release integrator's return, with its other_changes. A return that is not a JSON
// object of that shape is an error.
func parseIntegratorReturn(raw []byte) (integratorReturn, error) {
	var ret integratorReturn
	if err := json.Unmarshal(raw, &ret); err != nil {
		return integratorReturn{}, fmt.Errorf("the return is not a JSON object: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return integratorReturn{}, fmt.Errorf("the return is not a JSON object: %w", err)
	}
	if changes, ok := fields[otherChangesKey]; ok {
		if err := json.Unmarshal(changes, &ret.OtherChanges); err != nil {
			return integratorReturn{}, fmt.Errorf("%s: %w", otherChangesKey, err)
		}
	}
	return ret, nil
}

// integratorChange is one change a release integrator made outside the conflicts: the path and what changed.
type integratorChange struct {
	Path   string `json:"path"`
	Change string `json:"change"`
}

// integratorUnresolved is one path a release integrator stopped on: what the candidate meant, what origin meant, the
// decision that would settle it, and its side: candidate when the decision is a change to the epic's own candidate, and
// merge when it is how to resolve the merge.
type integratorUnresolved struct {
	Path      string `json:"path"`
	Candidate string `json:"candidate"`
	Origin    string `json:"origin"`
	Decision  string `json:"decision"`
	Side      string `json:"side"`
}

// sideCandidate is the side of an unresolved path whose decision is a change to the epic's own candidate.
const sideCandidate = "candidate"

// candidateSide reports whether ret, a blocked return, stopped only on decisions that are changes to the epic's own
// candidate: it names at least one unresolved path, and every one is on the candidate's side. Such a return sends the
// release back to planning; any other blocked return stops it.
func candidateSide(ret integratorReturn) bool {
	return len(ret.Unresolved) > 0 && !slices.ContainsFunc(ret.Unresolved, func(path integratorUnresolved) bool {
		return path.Side != sideCandidate
	})
}

// integratorMerge is the merge a release integrator finishes: the repository, the worktree the merge is in, the release
// tip, origin's commit and the paths that conflict.
type integratorMerge struct {
	repo, worktree, tip, origin string
	conflicts                   []string
}

// The limits of what the prompt of a release integrator lists: the commits of each side, and the commits of each side
// that touched one conflicted path.
const (
	integratorLogLimit  = 200
	integratorPathLimit = 20
)

// integratorMarkers matches, as a git grep -E pattern, the conflict-marker lines no path the merge commit changes may
// hold: one that opens with seven < and a space, one of seven = alone, and one that opens with seven > and a space.
const integratorMarkers = "^(<<<<<<< |=======$|>>>>>>> )"

// grepNoMatch is the exit status of git grep when no line matches, which is an answer and not a failure.
const grepNoMatch = 1

// integratorDir returns the directory the release integrator worktree of run is under, which is named by the run's id
// and attempt under the release worktrees directory. A runner with no state directory has none, so it is an error.
func (r *Runner) integratorDir(run store.NodeRun) (string, error) {
	return r.releaseStepDir(store.NodeWorkID(run.ID, run.Attempt, integratorPart))
}

// removeIntegrator removes the release integrator worktree of run, whose Result names the merge it finished. A Result
// whose merge did not conflict ran no integrator, and a runner with no state directory added no worktree, so neither
// has one to remove.
func (r *Runner) removeIntegrator(ctx context.Context, epic store.EpicRow, run store.NodeRun) error {
	if !mergeConflicted(run) || r.stateDir == "" {
		return nil
	}
	dir, err := r.integratorDir(run)
	if err != nil {
		return err
	}
	host, err := r.hostOf(epic)
	if err != nil {
		return err
	}
	if err = build.RemoveWorktrees(ctx, host, epic.Repository, dir); err != nil {
		return fmt.Errorf("remove release integrator worktree of epic %s: %w", epic.ID, err)
	}
	return nil
}

// mergeConflicted reports whether run's Result records a merge of origin's commit that conflicts. A Result that does
// not decode records none: the release kind stops the run on it.
func mergeConflicted(run store.NodeRun) bool {
	result, err := kinds.ParseReleaseResult(run.Result)
	return err == nil && len(result.Conflicts) > 0
}

// startIntegrator starts the round of the release integrator act names, as an unowned writing step in the run's
// integrator worktree. The first round adds the worktree afresh at the release tip and starts the merge of origin's
// commit there with merge.conflictStyle zdiff3, leaving its conflicts in place (startMerge). A later round starts from
// the merge commit the round before it made, which must still be the worktree's HEAD. The step is labeled with the
// epic and the run's path, and carries the integrator's return check (integratorCheck). A role, worktree, merge, prompt
// or return check that fails, or a start refused for good, records the round as failed at once, and the release stops
// at it on its next decision.
func (r *Runner) startIntegrator(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	position, err := strconv.ParseInt(act.Items[0], 10, 64)
	if err != nil {
		return releaseRefused(act)
	}
	id, head := act.IDs[0], act.Items[1]
	result, err := kinds.ParseReleaseResult(act.Run.Result)
	if err != nil {
		return fmt.Errorf("read result of node %s: %w", act.Path, err)
	}
	dir, err := r.integratorDir(act.Run)
	if err != nil {
		return err
	}
	merge := integratorMerge{
		repo: epic.Repository, worktree: filepath.Join(dir, releaseWorktree), tip: result.Tip, origin: result.Origin,
		conflicts: result.Conflicts,
	}
	fail := func(cause error) error { return r.failIntegrator(ctx, epic, act, position, merge, at, cause) }
	agent, err := roles.AgentOr(r.getenv, roleIntegrator, roleRelease)
	if err != nil {
		return fail(err)
	}
	if head == "" {
		var host hostexec.Host
		if host, err = r.hostOf(epic); err == nil {
			err = startMerge(ctx, host, merge, dir)
		}
	} else {
		err = keptMerge(ctx, merge, head)
	}
	if err != nil {
		return fail(err)
	}
	prompt, err := r.integratorPrompt(ctx, epic, merge, result.Branch, head, act.Output)
	if err != nil {
		return fail(err)
	}
	check, err := integratorCheck(ctx, merge)
	if err != nil {
		return fail(err)
	}
	_, err = r.steps.Start(ctx, enginehost.StepStart{
		ID: id, Agent: agent, Prompt: prompt, Schema: IntegratorSchema, Workspace: merge.worktree,
		Revision: merge.tip, Unowned: true, DoneValue: releaseDone, Epic: epic.ID, Task: act.Path,
		ReturnCheck: check,
	})
	if err == nil {
		return nil
	}
	if !refusedForGood(err) {
		return fmt.Errorf("start release integrator %s: %w", id, err)
	}
	return fail(err)
}

// failIntegrator records the round of the release integrator at position as a failed run of act's attempt, with the
// conflict merge names and the reason the round did not start.
func (r *Runner) failIntegrator(
	ctx context.Context, epic store.EpicRow, act Action, position int64, merge integratorMerge, at time.Time,
	cause error,
) error {
	run := store.ReleaseRun{
		Part: store.ReleasePartIntegrator, Position: position, Attempt: act.Run.Attempt, Step: integratorStepName,
		Start: merge.tip,
		Reason: conflictMessage(merge.tip, merge.origin, merge.conflicts) +
			"; the release integrator did not start: " + cause.Error(),
		At: at,
	}
	if err := r.store.RecordRelease(ctx, epic.ID, run); err != nil {
		return fmt.Errorf("record release integrator of epic %s: %w", epic.ID, err)
	}
	return nil
}

// startMerge adds the worktree of merge afresh under dir, detached at the release tip, after removing any worktree an
// earlier start left there, and runs the merge of origin's commit in it with merge.conflictStyle zdiff3 and neither a
// fast-forward nor a commit. The merge's conflicts stay in place: git's exit for a merge that conflicts is no error. A
// merge that is not in progress afterwards is an error.
func startMerge(ctx context.Context, host hostexec.Host, merge integratorMerge, dir string) error {
	if err := build.RemoveWorktrees(ctx, host, merge.repo, dir); err != nil {
		return fmt.Errorf("remove the worktree an earlier start left: %w", err)
	}
	if err := build.AddWorktree(ctx, host, merge.repo, merge.worktree, merge.tip); err != nil {
		return fmt.Errorf("add the release integrator worktree: %w", err)
	}
	_, err := gitOutput(ctx, merge.worktree, "-c", "merge.conflictStyle=zdiff3", "merge", "--no-ff", "--no-commit",
		merge.origin)
	var exit *exec.ExitError
	if err != nil && (!errors.As(err, &exit) || exit.ExitCode() != mergeConflict) {
		return err
	}
	if _, err = gitOutput(ctx, merge.worktree, "rev-parse", "--verify", "--quiet", "MERGE_HEAD"); err != nil {
		return fmt.Errorf("the merge of origin's commit %s is not in progress in %s: %w", merge.origin, merge.worktree,
			err)
	}
	return nil
}

// keptMerge checks that the worktree of merge still holds head, the merge commit the last round made, as its HEAD.
func keptMerge(ctx context.Context, merge integratorMerge, head string) error {
	got, err := gitOutput(ctx, merge.worktree, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return fmt.Errorf("the worktree %s of the last round cannot be read: %w", merge.worktree, err)
	}
	if got = strings.TrimSpace(got); got != head {
		return fmt.Errorf("the worktree %s is at %s, not at %s, the merge the last round made", merge.worktree, got,
			head)
	}
	return nil
}

// pinIntegrator pins head, the commit of the release integrator's merge, under run's release ref on the epic's host.
func (r *Runner) pinIntegrator(ctx context.Context, epic store.EpicRow, run store.NodeRun, head string) error {
	host, err := r.hostOf(epic)
	if err != nil {
		return fmt.Errorf("pin release integrator merge: %w", err)
	}
	if err = build.Pin(ctx, host, epic.Repository, runPin(epic, run), head); err != nil {
		return fmt.Errorf("pin release integrator merge: %w", err)
	}
	return nil
}

// recordIntegrator records the round of the release integrator act names once its step's end is stored. The round
// passes only when the step ended done and its commit passes the engine's checks again (integratorFailures); the commit
// is then pinned under the run's release ref and recorded as the round's head. A round that does not pass is recorded
// failed: a blocked return with its report (blockedReason), and any other end with the conflict and how the step ended.
// A blocked return whose every unresolved path is on the candidate's side (candidateSide) asks for a replan, which the
// store records with the round (store.Store.RecordIntegratorRound).
func (r *Runner) recordIntegrator(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	position, err := strconv.ParseInt(act.Items[1], 10, 64)
	if err != nil {
		return releaseRefused(act)
	}
	row, err := r.storedStep(ctx, epic.ID, act.IDs[0])
	if err != nil {
		return err
	}
	result, err := kinds.ParseReleaseResult(act.Run.Result)
	if err != nil {
		return fmt.Errorf("read result of node %s: %w", act.Path, err)
	}
	merge := integratorMerge{
		repo: epic.Repository, worktree: row.Workspace, tip: result.Tip, origin: result.Origin,
		conflicts: result.Conflicts,
	}
	var decodeReason string
	ret, err := parseIntegratorReturn([]byte(row.Result))
	if err != nil {
		decodeReason = err.Error()
	}
	run := store.ReleaseRun{
		Part: store.ReleasePartIntegrator, Position: position, Attempt: act.Run.Attempt, Step: integratorStepName,
		Start: merge.tip, End: row.End, Evidence: ret.Evidence, At: at,
	}
	replan := false
	switch {
	case row.End == releaseDone && decodeReason == "":
		head, failures, checkErr := integratorCommit(ctx, merge, ret)
		if checkErr != nil {
			return checkErr
		}
		if len(failures) > 0 {
			run.Reason = conflictMessage(merge.tip, merge.origin, merge.conflicts) +
				"; the release integrator's commit was refused: " + strings.Join(failures, "; ")
			break
		}
		if pinErr := r.pinIntegrator(ctx, epic, act.Run, head); pinErr != nil {
			return pinErr
		}
		run.Head, run.Commits, run.Passed = head, []string{head}, true
	case row.End == stepReturned && ret.Status == releaseBlocked && decodeReason == "":
		run.Reason = blockedReason(merge, ret)
		replan = candidateSide(ret)
	case row.End == releaseDone:
		run.Reason = conflictMessage(merge.tip, merge.origin, merge.conflicts) + "; " + decodeReason
	default:
		run.Reason = conflictMessage(merge.tip, merge.origin, merge.conflicts) + "; " +
			releaseEndReason("release integrator", row)
	}
	if err = r.store.RecordIntegratorRound(ctx, epic.ID, run, replan); err != nil {
		return fmt.Errorf("record release integrator of epic %s: %w", epic.ID, err)
	}
	return nil
}

// stepReturned is the end of a step that returned a status other than its done value, and releaseBlocked the status
// a release integrator that stopped returns.
const (
	stepReturned   = "returned"
	releaseBlocked = "blocked"
)

// integratorCommit resolves the commit the done return ret names in the worktree of merge, and runs the engine's checks
// on it again before it is pinned. It returns the commit and each check it fails.
func integratorCommit(ctx context.Context, merge integratorMerge, ret integratorReturn) (string, []string, error) {
	tree, err := mergeTreeOf(ctx, merge)
	if err != nil {
		return "", nil, err
	}
	failures := integratorFailures(ctx, merge, tree, ret)
	if len(failures) > 0 {
		return "", failures, nil
	}
	head, err := gitOutput(ctx, merge.worktree, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "", nil, err
	}
	return strings.TrimSpace(head), nil, nil
}

// mergeTreeOf returns the tree git's merge of origin's commit into the release tip writes, with the conflicted paths
// holding their conflict markers.
func mergeTreeOf(ctx context.Context, merge integratorMerge) (string, error) {
	out, _, err := mergeTree(ctx, merge.repo, merge.tip, merge.origin)
	if err != nil {
		return "", err
	}
	tree, _ := mergedTree(out)
	return tree, nil
}

// integratorCheck returns the return check of a release integrator step that finishes merge. Each return is checked for
// its shape (integratorShape), and a done return's commit is checked by integratorFailures. Each failure is one line,
// which the step loop sends back to the integrator. The tree git's own merge writes is computed once, here.
func integratorCheck(ctx context.Context, merge integratorMerge) (func(json.RawMessage) []string, error) {
	tree, err := mergeTreeOf(ctx, merge)
	if err != nil {
		return nil, err
	}
	checkCtx := context.WithoutCancel(ctx)
	return func(value json.RawMessage) []string {
		ret, decodeErr := parseIntegratorReturn(value)
		if decodeErr != nil {
			return []string{"release integrator return: " + decodeErr.Error()}
		}
		failures := integratorShape(ret, merge.conflicts)
		if ret.Status != releaseDone {
			return failures
		}
		return append(failures, integratorFailures(checkCtx, merge, tree, ret)...)
	}, nil
}

// integratorShape returns the lines on which the shape of ret fails. A done return names its commit, has exactly one
// resolution for each conflicted path and none for any other path, leaves unresolved empty and gives evidence. A
// blocked return names no commit, and gives unresolved or evidence.
func integratorShape(ret integratorReturn, conflicts []string) []string {
	if ret.Status != releaseDone {
		var failures []string
		if ret.Commit != "" {
			failures = append(failures, "commit: a blocked return names no commit, so it must be empty")
		}
		if len(ret.Unresolved) == 0 && strings.TrimSpace(ret.Evidence) == "" {
			failures = append(failures, "unresolved: a blocked return names each path it stopped on, or gives evidence")
		}
		return failures
	}
	var failures []string
	if strings.TrimSpace(ret.Commit) == "" {
		failures = append(failures, "commit: a done return names the full id of the merge commit")
	}
	counts := make(map[string]int, len(ret.Resolutions))
	for _, resolution := range ret.Resolutions {
		counts[resolution.Path]++
	}
	for _, path := range conflicts {
		switch counts[path] {
		case 0:
			failures = append(failures, fmt.Sprintf("resolutions: conflicted path %s has no resolution", path))
		case 1:
		default:
			failures = append(failures, fmt.Sprintf("resolutions: conflicted path %s has %d resolutions, want one",
				path, counts[path]))
		}
	}
	for _, resolution := range ret.Resolutions {
		if !slices.Contains(conflicts, resolution.Path) {
			failures = append(failures, fmt.Sprintf(
				"resolutions: %s is not a conflicted path; list a change outside the conflicts in other_changes",
				resolution.Path))
		}
	}
	if len(ret.Unresolved) > 0 {
		failures = append(failures, "unresolved: a done return stopped on no path, so it must be empty")
	}
	if strings.TrimSpace(ret.Evidence) == "" {
		failures = append(failures, "evidence: a done return gives an account of the merge")
	}
	return failures
}

// integratorFailures returns the lines on which the commit a done return ret names fails the engine's checks, in the
// worktree of merge, against tree, the tree git's own merge writes. HEAD is the commit. The commit's parents are
// exactly the release tip and then origin's commit, so it is one commit on the tip and the only one. git ls-files -u
// lists no path. No path the commit changes from either parent holds a conflict-marker line. Every path where the
// commit differs from tree is a conflicted path or is listed in other_changes. A check git cannot make fails with git's
// error.
func integratorFailures(ctx context.Context, merge integratorMerge, tree string, ret integratorReturn) []string {
	commit, err := gitOutput(ctx, merge.worktree, "rev-parse", "--verify", "--end-of-options", ret.Commit+"^{commit}")
	if err != nil {
		return []string{fmt.Sprintf("commit: %q does not name a commit: %v", ret.Commit, err)}
	}
	commit = strings.TrimSpace(commit)
	var failures []string
	head, err := gitOutput(ctx, merge.worktree, "rev-parse", "--verify", "HEAD^{commit}")
	switch {
	case err != nil:
		failures = append(failures, fmt.Sprintf("head: HEAD cannot be read: %v", err))
	case strings.TrimSpace(head) != commit:
		failures = append(failures, fmt.Sprintf("head: HEAD is %s, not the commit %s", strings.TrimSpace(head), commit))
	}
	for _, check := range []func(context.Context, integratorMerge, string, string, integratorReturn) string{
		parentsFailure, unmergedFailure, markerFailure, unlistedFailure,
	} {
		if failure := check(ctx, merge, commit, tree, ret); failure != "" {
			failures = append(failures, failure)
		}
	}
	return failures
}

// parentsFailure names a commit whose parents are not exactly the release tip and then origin's commit.
func parentsFailure(ctx context.Context, merge integratorMerge, commit, _ string, _ integratorReturn) string {
	out, err := gitOutput(ctx, merge.worktree, "rev-list", "--parents", "-n", "1", commit)
	if err != nil {
		return fmt.Sprintf("parents: %v", err)
	}
	parents := strings.Fields(out)[1:]
	if slices.Equal(parents, []string{merge.tip, merge.origin}) {
		return ""
	}
	return fmt.Sprintf(
		"parents: commit %s has parents [%s], want exactly the release tip %s and then origin's commit %s, in one "+
			"commit and no other", commit, strings.Join(parents, " "), merge.tip, merge.origin)
}

// unmergedFailure names the paths git ls-files -u lists in the worktree.
func unmergedFailure(ctx context.Context, merge integratorMerge, _, _ string, _ integratorReturn) string {
	paths, err := unmergedPaths(ctx, merge.worktree)
	if err != nil {
		return fmt.Sprintf("unmerged: %v", err)
	}
	if len(paths) == 0 {
		return ""
	}
	return "unmerged: git ls-files -u lists " + listPaths(paths)
}

// unmergedPaths returns the paths git ls-files -u lists in dir, each once: the paths the index leaves unmerged.
func unmergedPaths(ctx context.Context, dir string) ([]string, error) {
	out, err := gitOutput(ctx, dir, "ls-files", "-u", "-z")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, record := range strings.Split(out, "\x00") {
		_, path, found := strings.Cut(record, "\t")
		if found && !slices.Contains(paths, path) {
			paths = append(paths, path)
		}
	}
	return paths, nil
}

// markerFailure names each path that commit changes from either parent and that holds a conflict-marker line at commit.
func markerFailure(ctx context.Context, merge integratorMerge, commit, _ string, _ integratorReturn) string {
	changed, err := changedPaths(ctx, merge.worktree, merge.tip, commit)
	if err != nil {
		return fmt.Sprintf("markers: %v", err)
	}
	fromOrigin, err := changedPaths(ctx, merge.worktree, merge.origin, commit)
	if err != nil {
		return fmt.Sprintf("markers: %v", err)
	}
	changed = append(changed, fromOrigin...)
	out, err := gitOutput(ctx, merge.worktree, "grep", "-l", "-z", "-I", "-E", "-e", integratorMarkers, commit)
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == grepNoMatch {
		return ""
	}
	if err != nil {
		return fmt.Sprintf("markers: %v", err)
	}
	var marked []string
	for _, record := range strings.Split(out, "\x00") {
		path := strings.TrimPrefix(record, commit+":")
		if record != "" && slices.Contains(changed, path) && !slices.Contains(marked, path) {
			marked = append(marked, path)
		}
	}
	if len(marked) == 0 {
		return ""
	}
	return "markers: conflict-marker lines remain in " + listPaths(marked)
}

// unlistedFailure names each path where commit differs from tree, the tree git's own merge writes, that is neither a
// conflicted path nor listed in other_changes.
func unlistedFailure(ctx context.Context, merge integratorMerge, commit, tree string, ret integratorReturn) string {
	differ, err := changedPaths(ctx, merge.worktree, tree, commit)
	if err != nil {
		return fmt.Sprintf("other_changes: %v", err)
	}
	var unlisted []string
	for _, path := range differ {
		listed := slices.ContainsFunc(ret.OtherChanges, func(change integratorChange) bool {
			return change.Path == path
		})
		if !listed && !slices.Contains(merge.conflicts, path) {
			unlisted = append(unlisted, path)
		}
	}
	if len(unlisted) == 0 {
		return ""
	}
	return "other_changes: the commit changes what git merged without conflict in " + listPaths(unlisted) +
		", and other_changes does not list it"
}

// changedPaths returns the paths that differ between from and to, both sides of a rename included.
func changedPaths(ctx context.Context, dir, from, to string) ([]string, error) {
	out, err := gitOutput(ctx, dir, "diff", "--name-only", "-z", "--no-renames", from, to)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, path := range strings.Split(out, "\x00") {
		if path != "" {
			paths = append(paths, path)
		}
	}
	return paths, nil
}

// listPaths joins paths with commas, naming at most integratorPathLimit of them and how many more there are.
func listPaths(paths []string) string {
	shown := paths
	if len(paths) > integratorPathLimit {
		shown = paths[:integratorPathLimit]
	}
	list := strings.Join(shown, ", ")
	if left := len(paths) - len(shown); left > 0 {
		list += fmt.Sprintf(" and %d more", left)
	}
	return list
}

// blockedReason returns the reason a release integrator that returned blocked stops the release with: the merge it
// stopped, then each path it stopped on, with the side its decision is on, the decision that would settle it, what the
// candidate meant and what origin meant, or its evidence when it named no path. The reason fits store.TurnReasonMax, the most of it an epic's
// park keeps: each path gets an equal share of what the merge's line leaves, at least minShare bytes, cut at its end,
// and the paths no share is left for are counted in a last line that room is always kept for.
func blockedReason(merge integratorMerge, ret integratorReturn) string {
	head := fmt.Sprintf("the release integrator stopped the merge of origin's commit %s into release tip %s",
		merge.origin, merge.tip)
	entries := make([]string, 0, len(ret.Unresolved))
	for _, path := range ret.Unresolved {
		entries = append(entries, fmt.Sprintf("\n- %s: side: %s; decision: %s; candidate: %s; origin: %s",
			path.Path, path.Side, path.Decision, path.Candidate, path.Origin))
	}
	if len(entries) == 0 {
		return cutText(head+": "+ret.Evidence, store.TurnReasonMax)
	}
	const minShare, moreRoom = 160, 32
	budget := store.TurnReasonMax - len(head) - 1 - moreRoom
	share := max(budget/len(entries), minShare)
	reason := head + ":"
	for i, entry := range entries {
		room := store.TurnReasonMax - len(reason) - moreRoom
		if room < min(share, len(entry)) {
			return reason + fmt.Sprintf("\npaths not shown: %d", len(entries)-i)
		}
		reason += cutText(entry, min(share, room))
	}
	return reason
}

// cutText returns text cut to at most limit bytes at a character boundary, ending in an ellipsis when it was cut.
func cutText(text string, limit int) string {
	const ellipsis = "..."
	if len(text) <= limit {
		return text
	}
	cut := max(limit-len(ellipsis), 0)
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + ellipsis
}

// ReadoptCheck returns the return check a re-adopted release integrator step row is restored with: the check its start
// gave it, rebuilt from the running release run the row's Task label names and the Result that run recorded. Any other
// row gets a nil check. A release run that is not running, or whose Result names another merge, is an error, so the row
// is not re-adopted.
func ReadoptCheck(
	epics interface {
		EpicFlow(ctx context.Context, id string) (store.EpicFlow, error)
	},
) func(ctx context.Context, row store.StepRow) (func(json.RawMessage) []string, error) {
	return func(ctx context.Context, row store.StepRow) (func(json.RawMessage) []string, error) {
		if row.Schema != IntegratorSchema {
			return nil, nil
		}
		flow, err := epics.EpicFlow(ctx, row.Epic)
		if err != nil {
			return nil, fmt.Errorf("read epic %s: %w", row.Epic, err)
		}
		i := slices.IndexFunc(flow.Runs, func(run store.NodeRun) bool {
			return run.Path == row.Task && run.Kind == kindRelease && run.State == store.NodeRunning
		})
		if i < 0 {
			return nil, fmt.Errorf("epic %s has no running release run %s", row.Epic, row.Task)
		}
		result, err := kinds.ParseReleaseResult(flow.Runs[i].Result)
		if err != nil {
			return nil, fmt.Errorf("release run %s of epic %s: %w", row.Task, row.Epic, err)
		}
		if len(result.Conflicts) == 0 || result.Tip != row.Revision {
			return nil, fmt.Errorf("release run %s of epic %s records no conflicting merge from %s", row.Task,
				row.Epic, row.Revision)
		}
		return integratorCheck(ctx, integratorMerge{
			repo: flow.Epic.Repository, worktree: row.Workspace, tip: result.Tip, origin: result.Origin,
			conflicts: result.Conflicts,
		})
	}
}
