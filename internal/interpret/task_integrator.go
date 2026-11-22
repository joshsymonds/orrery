package interpret

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// taskIntegratorRole is the role text of the task integrator: the writing step that merges a task's revision into the
// candidate when the two conflict, and commits the merge, or returns clash when the two sides cannot both hold.
//
//go:embed task_integrator.md
var taskIntegratorRole string

// taskWorktree is the name of the worktree a task integrator round runs in under the directory of the round.
const taskWorktree = "wt"

// The verdicts a task integrator returns: merged when it commits the task's revision merged into the candidate, and
// clash when the two sides cannot both hold.
const (
	verdictMerged = "merged"
	verdictClash  = "clash"
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

// startTaskMerge makes the worktree of merge afresh under dir, detached at the candidate, and merges the task's
// revision into it without a commit (startMerge). The conflicts stay in place: it returns the paths they leave unmerged.
func startTaskMerge(ctx context.Context, merge integratorMerge, dir string) ([]string, error) {
	if err := startMerge(ctx, merge, dir); err != nil {
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
