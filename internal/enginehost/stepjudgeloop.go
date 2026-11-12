package enginehost

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/joshsymonds/orrery/internal/store"
)

// judgeLoopKind is the kind a judge verdict is stored with.
const judgeLoopKind = "judge"

// The actions a judge verdict is stored with. A met or second unmet verdict ends the step; a first unmet verdict sends it
// back once.
const (
	judgeLoopActionNone     = "none"
	judgeLoopActionContinue = "continue"
	judgeLoopActionEnd      = "end"
)

// judgeLoopEndGoalUnmet is the end a second unmet verdict stores. The step's return is kept.
const judgeLoopEndGoalUnmet = "goal_unmet"

// judgeLoopContinueText is the message a first unmet verdict sends the session back with, naming the judge's reason.
const judgeLoopContinueText = "A judge found that your return does not meet the task: %s\n" +
	"Return again with a result that meets it."

// judgeReturn judges a done return whose checks all passed and stores the verdict. A nil haiku makes no call and ends
// done. A met verdict ends done. A first unmet verdict sends the session back once with its reason. A second unmet
// verdict ends goal_unmet with the return kept. A failed call, a reply that does not parse or a diff stat that cannot be
// read stores its error with no verdict and ends done. It reports whether the run goes on.
func (r *stepRun) judgeReturn(ctx context.Context, round int, value json.RawMessage, commit, status string,
	checks []store.StepCheck) bool {
	if r.haiku == nil {
		r.finish(ctx, stepLoopEndDone, status, "")
		return false
	}
	verdict := store.StepVerdict{Kind: judgeLoopKind, Round: round, Action: judgeLoopActionNone}
	met, reason, err := r.judgeCall(ctx, value, commit, checks)
	if err != nil {
		verdict.Error = err.Error()
		if !r.judgeRecord(ctx, verdict) {
			return false
		}
		r.finish(ctx, stepLoopEndDone, status, "")
		return false
	}
	verdict.Detail = reason
	if met {
		verdict.Verdict = stepJudgeVerdictMet
		verdict.Action = judgeLoopActionEnd
		if !r.judgeRecord(ctx, verdict) {
			return false
		}
		r.finish(ctx, stepLoopEndDone, status, "")
		return false
	}
	verdict.Verdict = stepJudgeVerdictUnmet
	if r.judgeSent {
		verdict.Action = judgeLoopActionEnd
		if !r.judgeRecord(ctx, verdict) {
			return false
		}
		r.finish(ctx, judgeLoopEndGoalUnmet, "", reason)
		return false
	}
	r.judgeSent = true
	verdict.Action = judgeLoopActionContinue
	if !r.judgeRecord(ctx, verdict) {
		return false
	}
	if err = r.sessions.Continue(ctx, r.id, fmt.Sprintf(judgeLoopContinueText, reason)); err != nil {
		r.finish(ctx, stepLoopEndFailed, "", fmt.Sprintf("continue step %s: %v", r.id, err))
		return false
	}
	return true
}

// judgeCall asks the judge about a done return. A writing step's input carries the diff stat from start to commit and
// the gate check's detail from checks, and a read-only step's input carries the return alone. It returns the verdict
// and its reason, or an error when the diff stat, the call or the reply fails.
func (r *stepRun) judgeCall(ctx context.Context, value json.RawMessage, commit string,
	checks []store.StepCheck) (met bool, reason string, err error) {
	var diffStat string
	if r.writing {
		diffStat, err = r.judgeDiffStat(ctx, commit)
		if err != nil {
			return false, "", fmt.Errorf("diff stat of step %s: %w", r.id, err)
		}
	}
	input := stepJudgeInput(r.prompt, string(value), diffStat, judgeGateTail(checks))
	raw, err := r.haiku.Call(ctx, stepJudgeSchema, input)
	if err != nil {
		return false, "", fmt.Errorf("judge call: %w", err)
	}
	return parseStepJudge(raw)
}

// judgeDiffStat returns git diff --stat from the step's start to commit in its directory.
func (r *stepRun) judgeDiffStat(ctx context.Context, commit string) (string, error) {
	from, err := stepGitResolve(ctx, r.dir, r.start)
	if err != nil {
		return "", err
	}
	to, err := stepGitResolve(ctx, r.dir, commit)
	if err != nil {
		return "", err
	}
	return stepGit(ctx, r.dir, "diff", "--stat", from, to)
}

// judgeGateTail returns the detail of the gate check among checks, or empty when the round ran no gate.
func judgeGateTail(checks []store.StepCheck) string {
	for _, c := range checks {
		if c.Name == stepLoopGateName {
			return c.Detail
		}
	}
	return ""
}

// judgeRecord stores verdict on the step and logs it. It reports whether the store succeeded. A failed store ends the
// step failed.
func (r *stepRun) judgeRecord(ctx context.Context, verdict store.StepVerdict) bool {
	r.logger.InfoContext(ctx, "step judge verdict", "step", r.id, "round", verdict.Round,
		"verdict", verdict.Verdict, "action", verdict.Action, "err", verdict.Error)
	if err := r.writer.Commit(ctx, r.id, store.StepWatch{ID: r.id, Verdict: verdict}); err != nil {
		r.finish(ctx, stepLoopEndFailed, "", fmt.Sprintf("record verdict of step %s: %v", r.id, err))
		return false
	}
	return true
}
