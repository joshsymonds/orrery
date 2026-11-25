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
	"unicode/utf8"

	"github.com/joshsymonds/orrery/internal/build"
	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/roles"
	"github.com/joshsymonds/orrery/internal/store"
)

// integratorRole is the role text of the release integrator: the writing step that merges origin's commit into a release
// tip that lacks it, resolves any conflicts, runs the verifications the merged tree needs, and commits the merge.
//
//go:embed integrator.md
var integratorRole string

// roleIntegrator is the role the release integrator runs under. A role file with no key for it runs the integrator under
// the agent the release role names (roles.AgentOr).
const roleIntegrator = "integrator"

// integratorPart names the directory of a release run's integrator worktree: store.NodeWorkID of the run's id, its
// attempt and this part. The round of each generation of the attempt runs in that one directory, in a worktree added
// afresh.
const integratorPart = "integrator"

// integratorStepName is the step a release integrator's release run records.
const integratorStepName = "release integrator"

// IntegratorSchema is the schema the release integrator returns: its status, the merge commit, how each conflicted path
// was resolved, each change outside the conflicts, each command it ran to verify the merged tree with its result, each
// path it stopped on with the side its decision is on, and an account of the merge.
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
	OtherChanges []integratorChange `json:"-"`
	Checks       []struct {
		Command string `json:"command"`
		Result  string `json:"result"`
	} `json:"checks"`
	Unresolved []integratorUnresolved `json:"unresolved"`
	Evidence   string                 `json:"evidence"`
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

// namesDecisions reports whether ret, a blocked return, names at least one unresolved path, on either side. Such a
// return sends the release back to planning with its decisions; a blocked return that names none stops it.
func namesDecisions(ret integratorReturn) bool {
	return len(ret.Unresolved) > 0
}

// integratorMerge is the merge a release integrator makes: the repository, the worktree the merge is in, the release
// tip, origin's commit and the paths git's merge of the two conflicts in, none for a clean merge. held is set when the
// tip already holds origin's commit, so the round makes no merge and its commit, on the tip alone, renumbers the tip's
// migrations. migrations is how its commit is checked for migration collisions, none when the project declares no
// migrations.
type integratorMerge struct {
	repo, worktree, tip, origin string
	conflicts                   []string
	held                        bool
	migrations                  migrationCheck
}

// releaseMerge returns the merge the release integrator of a release run whose prepare recorded result makes in
// worktree of repo. A result whose tip is not behind origin's commit holds it, so the merge is held. Its commit is
// checked against origin's commit for migration collisions in the directory the result names.
func releaseMerge(repo, worktree string, result kinds.ReleaseResult) integratorMerge {
	return integratorMerge{
		repo: repo, worktree: worktree, tip: result.Tip, origin: result.Origin, conflicts: result.Conflicts,
		held:       !result.Behind,
		migrations: migrationCheck{dir: result.Migrations, origin: result.Origin},
	}
}

// The limits of what the prompt of a release integrator lists: the commits of each side, the commits of each side that
// touched one conflicted path, and the paths each side changed.
const (
	integratorLogLimit    = 200
	integratorPathLimit   = 20
	integratorChangeLimit = 200
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

// removeIntegrator removes the release integrator worktree of run, whose Result names the merge it made. A Result that
// merges no origin commit ran no integrator, and a runner with no state directory added no worktree, so neither has one
// to remove.
func (r *Runner) removeIntegrator(ctx context.Context, epic store.EpicRow, run store.NodeRun) error {
	if !mergesOrigin(run) || r.stateDir == "" {
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

// mergesOrigin reports whether run's Result records origin's commit, so the release integrator makes the candidate: the
// merge of that commit into the release tip, whether the merge conflicts or not, or the renumber of a tip that already
// holds it. A Result that does not decode records none: the release kind stops the run on it.
func mergesOrigin(run store.NodeRun) bool {
	result, err := kinds.ParseReleaseResult(run.Result)
	return err == nil && result.Origin != ""
}

// startIntegrator starts the round of the release integrator act names, as an unowned writing step in the run's
// integrator worktree. The worktree is added afresh at the release tip, and the merge of origin's commit is started
// there with merge.conflictStyle zdiff3, leaving any conflicts in place, unless the tip already holds that commit
// (startMerge). Its prompt carries the migration collisions the prepare recorded. The step is labeled with the epic
// and the run's path, and carries the integrator's return check (integratorCheck). A role, worktree, merge, prompt or
// return check that fails, or a start refused for good, records the round as failed at once, and the release stops at
// it on its next decision.
func (r *Runner) startIntegrator(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	position, err := strconv.ParseInt(act.Items[0], 10, 64)
	if err != nil {
		return releaseRefused(act)
	}
	id := act.IDs[0]
	result, err := kinds.ParseReleaseResult(act.Run.Result)
	if err != nil {
		return fmt.Errorf("read result of node %s: %w", act.Path, err)
	}
	dir, err := r.integratorDir(act.Run)
	if err != nil {
		return err
	}
	merge := releaseMerge(epic.Repository, filepath.Join(dir, releaseWorktree), result)
	fail := func(cause error) error { return r.failIntegrator(ctx, epic, act, position, merge, at, cause) }
	agent, err := roles.AgentOr(r.getenv, roleIntegrator, roleRelease)
	if err != nil {
		return fail(err)
	}
	host, err := r.hostOf(epic)
	if err != nil {
		return fail(err)
	}
	if err = startMerge(ctx, host, merge, dir); err != nil {
		return fail(err)
	}
	prompt, err := r.integratorPrompt(ctx, host, epic, merge, result.Branch, result.Collisions)
	if err != nil {
		return fail(err)
	}
	check, err := integratorCheck(ctx, host, merge)
	if err != nil {
		return fail(err)
	}
	purpose := fmt.Sprintf("Merge main into %s's release candidate.", build.Slug(epic.ID))
	if merge.held {
		purpose = fmt.Sprintf("Renumber %s's migrations after main's.", build.Slug(epic.ID))
	}
	_, err = r.steps.Start(ctx, enginehost.StepStart{
		ID: id, Agent: agent, Prompt: prompt, Schema: IntegratorSchema, Workspace: merge.worktree,
		Revision: merge.tip, Unowned: true, DoneValue: releaseDone, Epic: epic.ID, Task: act.Path,
		ReturnCheck: check, Purpose: purpose,
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
// merge it makes (integratorReason) and the reason the round did not start.
func (r *Runner) failIntegrator(
	ctx context.Context, epic store.EpicRow, act Action, position int64, merge integratorMerge, at time.Time,
	cause error,
) error {
	run := store.ReleaseRun{
		Part: store.ReleasePartIntegrator, Position: position, Attempt: act.Run.Attempt, Step: integratorStepName,
		Start:  merge.tip,
		Reason: integratorReason(merge) + "; the release integrator did not start: " + cause.Error(),
		At:     at,
	}
	if err := r.store.RecordRelease(ctx, epic.ID, run); err != nil {
		return fmt.Errorf("record release integrator of epic %s: %w", epic.ID, err)
	}
	return nil
}

// startMerge adds the worktree of merge afresh under dir, detached at the release tip, after removing any worktree an
// earlier start left there, and runs the merge of origin's commit in it with merge.conflictStyle zdiff3 and neither a
// fast-forward nor a commit. A clean merge stops before its commit, and a merge that conflicts leaves its conflicts in
// place: git's exit for a merge that conflicts is no error. A merge that is not in progress afterwards is an error. A
// held merge runs no merge: the tip already holds origin's commit, so the worktree is the tip alone.
func startMerge(ctx context.Context, host hostexec.Host, merge integratorMerge, dir string) error {
	if err := build.RemoveWorktrees(ctx, host, merge.repo, dir); err != nil {
		return fmt.Errorf("remove the worktree an earlier start left: %w", err)
	}
	if err := build.AddWorktree(ctx, host, merge.repo, merge.worktree, merge.tip); err != nil {
		return fmt.Errorf("add the release integrator worktree: %w", err)
	}
	if merge.held {
		return nil
	}
	_, err := gitOutput(
		ctx,
		host,
		merge.worktree,
		"-c",
		"merge.conflictStyle=zdiff3",
		"merge",
		"--no-ff",
		"--no-commit",
		merge.origin,
	)
	var gitErr *hostexec.GitError
	if err != nil && (!errors.As(err, &gitErr) || gitErr.ExitCode != mergeConflict) {
		return err
	}
	if _, err = gitOutput(ctx, host, merge.worktree, "rev-parse", "--verify", "--quiet", "MERGE_HEAD"); err != nil {
		return fmt.Errorf("the merge of origin's commit %s is not in progress in %s: %w", merge.origin, merge.worktree,
			err)
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
// failed: a blocked return with its report (blockedReason), and any other end with the merge (integratorReason) and
// how the step ended.
// A blocked return that names at least one unresolved path, on either side (namesDecisions), asks for a replan, which
// the store records with the round (store.Store.RecordIntegratorRound).
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
	merge := releaseMerge(epic.Repository, row.Workspace, result)
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
		head, failures, checkErr := r.integratorCommitOf(ctx, epic, merge, ret)
		if checkErr != nil {
			return checkErr
		}
		if len(failures) > 0 {
			run.Reason = integratorReason(merge) + "; the release integrator's commit was refused: " +
				strings.Join(failures, "; ")
			break
		}
		if pinErr := r.pinIntegrator(ctx, epic, act.Run, head); pinErr != nil {
			return pinErr
		}
		run.Head, run.Commits, run.Passed = head, []string{head}, true
	case row.End == stepReturned && ret.Status == releaseBlocked && decodeReason == "":
		run.Reason = blockedReason(merge, ret)
		replan = namesDecisions(ret)
	case row.End == releaseDone:
		run.Reason = integratorReason(merge) + "; " + decodeReason
	default:
		run.Reason = integratorReason(merge) + "; " + releaseEndReason("release integrator", row)
	}
	if err = r.store.RecordIntegratorRound(ctx, epic.ID, run, replan); err != nil {
		return fmt.Errorf("record release integrator of epic %s: %w", epic.ID, err)
	}
	return nil
}

// integratorReason is the merge a round of the release integrator of merge makes, as the reason of a round that did not
// pass opens with it: the merge of origin's commit into the release tip, with the paths it conflicts in when git's
// merge of the two conflicts (conflictMessage). A clean merge is not named as conflicting, and a held one is named as
// the renumber of the tip's migrations.
func integratorReason(merge integratorMerge) string {
	if merge.held {
		return fmt.Sprintf("renumber of the migrations of release tip %s after origin's commit %s", merge.tip,
			merge.origin)
	}
	if len(merge.conflicts) == 0 {
		return fmt.Sprintf("merge of origin's commit %s into %s", merge.origin, merge.tip)
	}
	return conflictMessage(merge.tip, merge.origin, merge.conflicts)
}

// stepReturned is the end of a step that returned a status other than its done value, and releaseBlocked the status
// a release integrator that stopped returns.
const (
	stepReturned   = "returned"
	releaseBlocked = "blocked"
)

// integratorCommitOf is integratorCommit on the host the epic lives on.
func (r *Runner) integratorCommitOf(
	ctx context.Context, epic store.EpicRow, merge integratorMerge, ret integratorReturn,
) (string, []string, error) {
	host, err := r.hostOf(epic)
	if err != nil {
		return "", nil, fmt.Errorf("check the release integrator's commit: %w", err)
	}
	return integratorCommit(ctx, host, merge, ret)
}

// integratorCommit resolves the commit the done return ret names in the worktree of merge on host, and runs the engine's
// checks on it again before it is pinned. It returns the commit and each check it fails.
func integratorCommit(
	ctx context.Context, host hostexec.Host, merge integratorMerge, ret integratorReturn,
) (string, []string, error) {
	tree, err := mergeTreeOf(ctx, host, merge)
	if err != nil {
		return "", nil, err
	}
	failures := integratorFailures(ctx, host, merge, tree, ret)
	if len(failures) > 0 {
		return "", failures, nil
	}
	head, err := gitOutput(ctx, host, merge.worktree, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "", nil, err
	}
	return strings.TrimSpace(head), nil, nil
}

// mergeTreeOf returns the tree git's merge of origin's commit into the release tip writes on host, with the conflicted
// paths holding their conflict markers.
func mergeTreeOf(ctx context.Context, host hostexec.Host, merge integratorMerge) (string, error) {
	out, _, err := mergeTree(ctx, host, merge.repo, merge.tip, merge.origin)
	if err != nil {
		return "", err
	}
	tree, _ := mergedTree(out)
	return tree, nil
}

// integratorCheck returns the return check of a release integrator step on host that finishes merge. Each return is
// checked for its shape (integratorShape), and a done return's commit is checked by integratorFailures. Each failure is
// one line, which the step loop sends back to the integrator. The tree git's own merge writes is computed once, here.
func integratorCheck(
	ctx context.Context, host hostexec.Host, merge integratorMerge,
) (func(json.RawMessage) []string, error) {
	tree, err := mergeTreeOf(ctx, host, merge)
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
		return append(failures, integratorFailures(checkCtx, host, merge, tree, ret)...)
	}, nil
}

// integratorShape returns the lines on which the shape of ret fails. A done return names its commit, has exactly one
// resolution for each conflicted path and none for any other path, so a clean merge has none, lists at least one
// command it ran to verify the merged tree, leaves unresolved empty and gives evidence. A blocked return names no
// commit, and gives unresolved or evidence.
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
	if len(ret.Checks) == 0 {
		failures = append(failures, "checks: a done return lists each command it ran to verify the merged tree")
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
// worktree of merge on host, against tree, the tree git's own merge writes. HEAD is the commit. The commit's parents are
// exactly the release tip and then origin's commit, or the tip alone for a held merge, so it is one commit on the tip
// and the only one. git ls-files -u lists no path. No path the commit changes from either parent holds a conflict-marker
// line. Every path where the commit differs from tree is a conflicted path or is listed in other_changes. No new
// migration of the commit has a version origin's main uses for another file, when merge checks its migrations. A check
// git cannot make fails with git's error.
func integratorFailures(
	ctx context.Context, host hostexec.Host, merge integratorMerge, tree string, ret integratorReturn,
) []string {
	commit, err := gitOutput(
		ctx,
		host,
		merge.worktree,
		"rev-parse",
		"--verify",
		"--end-of-options",
		ret.Commit+"^{commit}",
	)
	if err != nil {
		return []string{fmt.Sprintf("commit: %q does not name a commit: %v", ret.Commit, err)}
	}
	commit = strings.TrimSpace(commit)
	var failures []string
	head, err := gitOutput(ctx, host, merge.worktree, "rev-parse", "--verify", "HEAD^{commit}")
	switch {
	case err != nil:
		failures = append(failures, fmt.Sprintf("head: HEAD cannot be read: %v", err))
	case strings.TrimSpace(head) != commit:
		failures = append(failures, fmt.Sprintf("head: HEAD is %s, not the commit %s", strings.TrimSpace(head), commit))
	}
	for _, check := range []func(
		context.Context, hostexec.Host, integratorMerge, string, string, integratorReturn,
	) string{
		parentsFailure, unmergedFailure, markerFailure, unlistedFailure, migrationFailure,
	} {
		if failure := check(ctx, host, merge, commit, tree, ret); failure != "" {
			failures = append(failures, failure)
		}
	}
	return failures
}

// parentsFailure names a commit whose parents are not exactly the release tip and then origin's commit, or, for a held
// merge, not the release tip alone.
func parentsFailure(
	ctx context.Context, host hostexec.Host, merge integratorMerge, commit, _ string, _ integratorReturn,
) string {
	out, err := gitOutput(ctx, host, merge.worktree, "rev-list", "--parents", "-n", "1", commit)
	if err != nil {
		return fmt.Sprintf("parents: %v", err)
	}
	parents := strings.Fields(out)[1:]
	if merge.held {
		if slices.Equal(parents, []string{merge.tip}) {
			return ""
		}
		return fmt.Sprintf(
			"parents: commit %s has parents [%s], want exactly the release tip %s, which already holds origin's "+
				"commit %s, in one commit and no other", commit, strings.Join(parents, " "), merge.tip, merge.origin)
	}
	if slices.Equal(parents, []string{merge.tip, merge.origin}) {
		return ""
	}
	return fmt.Sprintf(
		"parents: commit %s has parents [%s], want exactly the release tip %s and then origin's commit %s, in one "+
			"commit and no other", commit, strings.Join(parents, " "), merge.tip, merge.origin)
}

// unmergedFailure names the paths git ls-files -u lists in the worktree.
func unmergedFailure(
	ctx context.Context, host hostexec.Host, merge integratorMerge, _, _ string, _ integratorReturn,
) string {
	paths, err := unmergedPaths(ctx, host, merge.worktree)
	if err != nil {
		return fmt.Sprintf("unmerged: %v", err)
	}
	if len(paths) == 0 {
		return ""
	}
	return "unmerged: git ls-files -u lists " + listPaths(paths)
}

// unmergedPaths returns the paths git ls-files -u lists in dir on host, each once: the paths the index leaves unmerged.
func unmergedPaths(ctx context.Context, host hostexec.Host, dir string) ([]string, error) {
	out, err := gitOutput(ctx, host, dir, "ls-files", "-u", "-z")
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
func markerFailure(
	ctx context.Context, host hostexec.Host, merge integratorMerge, commit, _ string, _ integratorReturn,
) string {
	changed, err := changedPaths(ctx, host, merge.worktree, merge.tip, commit)
	if err != nil {
		return fmt.Sprintf("markers: %v", err)
	}
	fromOrigin, err := changedPaths(ctx, host, merge.worktree, merge.origin, commit)
	if err != nil {
		return fmt.Sprintf("markers: %v", err)
	}
	changed = append(changed, fromOrigin...)
	out, err := gitOutput(ctx, host, merge.worktree, "grep", "-l", "-z", "-I", "-E", "-e", integratorMarkers, commit)
	var gitErr *hostexec.GitError
	if errors.As(err, &gitErr) && gitErr.ExitCode == grepNoMatch {
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
func unlistedFailure(
	ctx context.Context, host hostexec.Host, merge integratorMerge, commit, tree string, ret integratorReturn,
) string {
	differ, err := changedPaths(ctx, host, merge.worktree, tree, commit)
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

// changedPaths returns the paths that differ between from and to in dir on host, both sides of a rename included.
func changedPaths(ctx context.Context, host hostexec.Host, dir, from, to string) ([]string, error) {
	out, err := gitOutput(ctx, host, dir, "diff", "--name-only", "-z", "--no-renames", from, to)
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
// and the paths no share is left for are counted in a last line that room is always kept for. A held merge's line names
// the renumber it stopped (integratorReason).
func blockedReason(merge integratorMerge, ret integratorReturn) string {
	head := fmt.Sprintf("the release integrator stopped the merge of origin's commit %s into release tip %s",
		merge.origin, merge.tip)
	if merge.held {
		head = "the release integrator stopped the " + integratorReason(merge)
	}
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

// ReadoptCheck returns the return check a re-adopted step row of the interpreter is restored with: the check its start
// gave it. A release integrator step's is rebuilt from the running release run the row's Task label names and the
// Result that run recorded, whether that merge conflicts or not, and its git runs on the host the row's epic names in
// hosts. A gate triage step's is kinds.TriageFailures against the check it triages, read from the store as its start
// read it (readoptTriageCheck). Any other row gets a nil check. A release run that is not running, a Result that merges
// no origin commit into the row's revision, and a triage step whose check is not stored are errors, so the row is not
// re-adopted.
func ReadoptCheck(
	epics interface {
		EpicFlow(ctx context.Context, id string) (store.EpicFlow, error)
	},
	hosts hostexec.Hosts,
) func(ctx context.Context, row store.StepRow) (func(json.RawMessage) []string, error) {
	return func(ctx context.Context, row store.StepRow) (func(json.RawMessage) []string, error) {
		if row.Schema == TriageSchema {
			return readoptTriageCheck(ctx, epics, row)
		}
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
		if result.Origin == "" || result.Tip != row.Revision {
			return nil, fmt.Errorf("release run %s of epic %s records no merge of origin's commit into %s", row.Task,
				row.Epic, row.Revision)
		}
		host, err := hosts.Host(flow.Epic.Host)
		if err != nil {
			return nil, fmt.Errorf("host of epic %s: %w", row.Epic, err)
		}
		return integratorCheck(ctx, host, releaseMerge(flow.Epic.Repository, row.Workspace, result))
	}
}
