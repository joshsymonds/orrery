package interpret

import (
	"context"
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
	"github.com/joshsymonds/orrery/internal/releasestep"
	"github.com/joshsymonds/orrery/internal/roles"
	"github.com/joshsymonds/orrery/internal/store"
)

// The roles a release step and its judge run under, read from the machine's role file.
const (
	roleRelease = "release"
	roleJudge   = "judge"
)

// releaseStepLabel starts the label a release step is named by: the word release and then its position.
const releaseStepLabel = "release "

// releaseWorktrees is the directory under the state directory that holds one directory per release step id, and
// releaseWorktree the worktree a release step runs in under its directory.
const (
	releaseWorktrees = "release-worktrees"
	releaseWorktree  = "wt"
)

// releaseDone is the end and the status a release step or judge ends done with.
const releaseDone = "done"

// releaseStepSchema is the schema a release step returns: its status, the commit HEAD is at, every commit it added and
// the evidence of its effect.
const releaseStepSchema = `{"type":"object","properties":{"status":{"type":"string","enum":["done","blocked"]},` +
	`"commit":{"type":"string"},"commits":{"type":"array","items":{"type":"string"}},"evidence":{"type":"string"}},` +
	`"required":["status"]}`

// releaseJudgeSchema is the schema the judge returns: whether the release is released, and each postcondition the
// evidence does not cover.
const releaseJudgeSchema = `{"type":"object","properties":{"status":{"type":"string","enum":["done"]},` +
	`"released":{"type":"boolean"},"uncovered":{"type":"array","items":{"type":"string"}}},` +
	`"required":["status","released","uncovered"]}`

// releaseStepResult is the structured return of a release step, as the step stores it.
type releaseStepResult struct {
	Commits  []string `json:"commits"`
	Evidence string   `json:"evidence"`
}

// releaseJudgeResult is the structured return of a release judge, as the judge stores it.
type releaseJudgeResult struct {
	Released  bool     `json:"released"`
	Uncovered []string `json:"uncovered"`
}

// releaseStepPosition returns the position of the release step label names, and whether label names one.
func releaseStepPosition(label string) (int64, bool) {
	digits, ok := strings.CutPrefix(label, releaseStepLabel)
	if !ok {
		return 0, false
	}
	position, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || position < 1 {
		return 0, false
	}
	return position, true
}

// releaseStepAt returns the release step at position of epic.
func releaseStepAt(epic store.EpicRow, position int64) (store.EpicReleaseStep, error) {
	if position > int64(len(epic.ReleaseSteps)) {
		return store.EpicReleaseStep{}, fmt.Errorf("epic %s has no release step %d", epic.ID, position)
	}
	return epic.ReleaseSteps[position-1], nil
}

// releaseStepDir returns the directory the worktree of the release step id is under. A runner with no state directory
// has nowhere to put one, so it is an error.
func (r *Runner) releaseStepDir(id string) (string, error) {
	if r.stateDir == "" {
		return "", errors.New("the runner was not given a state directory")
	}
	return filepath.Join(r.stateDir, releaseWorktrees, id), nil
}

// startReleaseStep starts the release step act names, at the start revision act names. A step that names a kind starts
// as the check startKindStep makes. Any other step starts as an unowned writing step in a worktree of its own, detached
// at that commit. The first step starts only once origin's main is verified to be in its candidate, whether the
// candidate was passed at an earlier attempt or recorded at this one, since origin's main may have moved past it while
// its release integrator ran. Its role, worktree or start refused for good records the step as failed at once, and the
// release stops at it on its next decision.
func (r *Runner) startReleaseStep(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	position, ok := releaseStepPosition(act.Items[0])
	if !ok {
		return releaseRefused(act)
	}
	step, err := releaseStepAt(epic, position)
	if err != nil {
		return err
	}
	id, start := act.IDs[0], act.Items[1]
	if position == 1 {
		current, verifyErr := r.verifyCandidate(ctx, epic, act, start, at)
		if verifyErr != nil || !current {
			return verifyErr
		}
	}
	if step.Kind != "" {
		return r.startKindStep(ctx, epic, act, position, step, at)
	}
	dir, err := r.releaseStepDir(id)
	if err != nil {
		return err
	}
	agent, err := roles.Agent(r.getenv, roleRelease)
	if err != nil {
		return r.failReleaseStep(ctx, epic, act, position, step, at, err)
	}
	host, err := r.hostOf(epic)
	if err != nil {
		return r.failReleaseStep(ctx, epic, act, position, step, at, err)
	}
	wt := filepath.Join(dir, releaseWorktree)
	if err = build.AddWorktree(ctx, host, epic.Repository, wt, start); err != nil {
		return r.failReleaseStep(ctx, epic, act, position, step, at, err)
	}
	_, err = r.steps.Start(ctx, enginehost.StepStart{
		ID: id, Agent: agent, Prompt: releaseStepPrompt(step, start, epic.ReleaseThen), Schema: releaseStepSchema,
		Workspace: wt, Revision: start, Unowned: true, DoneValue: releaseDone, Epic: epic.ID,
		Task: releaseStepTask(act, position, id), Purpose: fmt.Sprintf("Release step %d: %s.", position, step.Step),
	})
	if err == nil {
		return nil
	}
	if !refusedForGood(err) {
		return fmt.Errorf("start release step %s: %w", id, err)
	}
	return errors.Join(
		r.failReleaseStep(ctx, epic, act, position, step, at, err),
		build.RemoveWorktrees(ctx, host, epic.Repository, dir),
	)
}

// startKindStep starts the release step at position of epic, which names a kind, as a check of the release run act
// names, stored under act's id, so the release reads the check as the step's work. The check runs `orrery release-step`
// with the step's kind and parameters (releasestep.Argv) under a holder, in a worktree of the epic's repository detached
// at act's start, within the kind's limit (releasestep.Limit). That command carries out the step's mechanics and checks
// its postcondition in code, and the step passes when it exits 0 (recordKindStep). The command names the epic's title
// as the change. A runner given no orrery binary records the step failed at once.
func (r *Runner) startKindStep(
	ctx context.Context, epic store.EpicRow, act Action, position int64, step store.EpicReleaseStep, at time.Time,
) error {
	if r.orrery == "" {
		return r.failReleaseStep(ctx, epic, act, position, step, at, errNoOrrery)
	}
	start := act.Items[1]
	check := store.NodeCheck{
		ID: act.IDs[0], Epic: epic.ID, Path: act.Path, Revision: start,
		Argv:  releasestep.Argv(r.orrery, step.Kind, step.Params, start, epic.Title),
		Limit: int64(releasestep.Limit(step.Kind) / time.Second),
	}
	if _, err := r.checks.Run(ctx, check); err != nil {
		return fmt.Errorf("start release step %d of epic %s: %w", position, epic.ID, err)
	}
	return nil
}

// errNoOrrery is why a release step that names a kind does not start on a runner given no orrery binary.
var errNoOrrery = errors.New("the runner was not given the orrery binary a release step that names a kind runs")

// releaseStepTask returns the task the start of the release step at position, under id, carries. The rows of a release
// read a step under its run's id, so a step under any other id, such as the first step of a candidate prepared again,
// carries the release run's path as its task, as the release reviewer's start does, and is read under that path. Any
// other step carries no task.
func releaseStepTask(act Action, position int64, id string) string {
	own := store.NodeWorkID(act.Run.ID, act.Run.Attempt, fmt.Sprintf("%s%d", releaseStepLabel, position))
	if id == own {
		return ""
	}
	return act.Path
}

// failReleaseStep records the release step act names as a failed run of its attempt, with the reason it did not start.
func (r *Runner) failReleaseStep(
	ctx context.Context, epic store.EpicRow, act Action, position int64, step store.EpicReleaseStep, at time.Time,
	cause error,
) error {
	run := store.ReleaseRun{
		Part: store.ReleasePartStep, Position: position, Attempt: act.Run.Attempt, Step: step.Step, Start: act.Items[1],
		Reason: "the step did not start: " + cause.Error(), At: at,
	}
	if err := r.store.RecordRelease(ctx, epic.ID, run); err != nil {
		return fmt.Errorf("record release step %d of epic %s: %w", position, epic.ID, err)
	}
	return nil
}

// releaseStepPrompt returns the prompt of a release step: the one step, its target and undo, the release's
// postconditions then, the commit it starts at, what it may not do, that it never moves a target back past the commit
// (releaseHeldRule), and how a judge decides the postconditions from the evidence the step returns.
func releaseStepPrompt(step store.EpicReleaseStep, start, then string) string {
	return fmt.Sprintf("Carry out this one release step and no other: %s.\n\n"+
		"Its target is %s, and its undo is %s.\n\n"+
		"The release promises: %s\n\n"+
		"This worktree is checked out detached at %s, the commit the step starts from.\n\n"+
		"Do only this step. Do not carry out any other release step.\n\n"+
		releaseHeldRule+" So when the step's target already holds or runs a commit or build that contains the commit "+
		"the step starts from, the step's postcondition is met: carry out nothing that would replace it with an "+
		"older one, such as installing or deploying the step's own build over a newer one, and return status done with "+
		"evidence naming what the target holds and that it contains the start commit.\n\n"+
		"When the step is carried out, return status done. Return commits as the full id of every commit you "+
		"added on top of %s, and an empty list when you added none. Return commit as the full id of HEAD. "+
		"Return evidence showing the effect of the step: what changed and where it can be seen. If you cannot "+
		"carry out the step, return status blocked with evidence of why.\n\n"+
		"A judge then decides whether the release met its postconditions from the evidence its steps return and "+
		"read-only checks of its own. So the evidence this step returns should show each postcondition this step "+
		"affects, with the output that proves it, such as the command this step ran and that command's output.",
		step.Step, step.Target, step.Undo, then, start, start)
}

// releaseHeldRule is how a release step and the judge read a postcondition that names the released commit: a target
// that has moved past the commit still holds it, since another release may have shipped a newer commit that contains
// it while this one ran.
const releaseHeldRule = "A postcondition that names the released commit is met by any commit or build that contains " +
	"it: origin's main, a tag or a running build that has moved past the released commit still holds it, since a " +
	"later release may have shipped a newer commit that contains it."

// recordReleaseStep records the release step act names once its work has ended. A step that names a kind is recorded
// from its check (recordKindStep). Any other step passes only when it ended done and HEAD descends from its start.
// Otherwise it is recorded failed with the reason named. Its worktree is removed once the run is recorded. The first
// step's failure is not recorded when origin's main has moved past the candidate it started from, since that is no
// failure of the release: redoCandidate marks the candidate stale instead. A check of origin's main that errors records
// the failure with the error named, and the release stops.
func (r *Runner) recordReleaseStep(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	position, ok := releaseStepPosition(act.Items[0])
	if !ok {
		return releaseRefused(act)
	}
	step, err := releaseStepAt(epic, position)
	if err != nil {
		return err
	}
	if step.Kind != "" {
		return r.recordKindStep(ctx, epic, act, position, step, at)
	}
	id, start := act.IDs[0], act.Items[1]
	row, err := r.storedStep(ctx, epic.ID, id)
	if err != nil {
		return err
	}
	dir, err := r.releaseStepDir(id)
	if err != nil {
		return err
	}
	host, err := r.hostOf(epic)
	if err != nil {
		return err
	}
	wt := filepath.Join(dir, releaseWorktree)
	result, decodeReason := decodeReturn[releaseStepResult](row.Result)
	head, reason, err := releaseStepEnd(ctx, host, wt, start, row, decodeReason)
	if err != nil {
		return err
	}
	if position == 1 && reason != "" {
		var stale bool
		if reason, stale, err = r.staleFirstStep(ctx, host, epic, act, start, reason, at); err != nil || stale {
			return errors.Join(err, removeReleaseWorktree(ctx, host, epic, act, dir))
		}
	}
	run := store.ReleaseRun{
		Part:     store.ReleasePartStep,
		Position: position,
		Attempt:  act.Run.Attempt,
		Step:     step.Step,
		Start:    start,
		Head:     head,
		End:      row.End,
		Commits:  result.Commits,
		Evidence: result.Evidence,
		Passed:   reason == "",
		Reason:   reason,
		At:       at,
	}
	if err = r.store.RecordRelease(ctx, epic.ID, run); err != nil {
		return fmt.Errorf("record release step %d of epic %s: %w", position, epic.ID, err)
	}
	return removeReleaseWorktree(ctx, host, epic, act, dir)
}

// removeReleaseWorktree removes the worktree of the release step act names, which is under dir.
func removeReleaseWorktree(ctx context.Context, host hostexec.Host, epic store.EpicRow, act Action, dir string) error {
	if err := build.RemoveWorktrees(ctx, host, epic.Repository, dir); err != nil {
		return fmt.Errorf("remove worktree of release step %s: %w", act.IDs[0], err)
	}
	return nil
}

// staleFirstStep handles the first release step act names, which failed for reason from the candidate start, and
// returns the reason the step is recorded failed with and whether it is not recorded at all. When origin's main has
// moved past the candidate, the failure is no failure of the release, so no failed run is recorded for it: the
// candidate is marked stale at act's attempt, and the release prepares it again, merged with origin's main, gates,
// reviews and starts its first step under a new generation. When origin's main is in the candidate, the step's failure
// stands as reason gives it. A check of origin's main that errors adds the error to the reason, and the failure stands.
func (r *Runner) staleFirstStep(
	ctx context.Context, host hostexec.Host, epic store.EpicRow, act Action, start, reason string, at time.Time,
) (string, bool, error) {
	contained, failure := r.originHeld(ctx, host, epic, start)
	switch {
	case failure != "":
		return reason + "; origin's main could not be checked against the step's start: " + failure, false, nil
	case contained:
		return reason, false, nil
	}
	stale := fmt.Sprintf("release step 1 failed after origin's main moved past candidate %s: %s", start, reason)
	if err := r.store.MarkCandidateStale(ctx, epic.ID, act.Path, act.Run.Attempt, start, stale, at); err != nil {
		return "", false, fmt.Errorf("mark candidate %s of epic %s stale: %w", start, epic.ID, err)
	}
	return reason, true, nil
}

// originHeld reports whether origin's current main is in start (candidateContainsOrigin), or why that could not be
// checked, which is "" when it could.
func (r *Runner) originHeld(
	ctx context.Context, host hostexec.Host, epic store.EpicRow, start string,
) (bool, string) {
	contained, err := r.candidateContainsOrigin(ctx, host, epic, start)
	if err != nil {
		return false, err.Error()
	}
	return contained, ""
}

// releaseCheckFailed is the end a release step that names a kind is recorded with when its check did not pass. One that
// passed is recorded with releaseDone.
const releaseCheckFailed = "failed"

// releaseReasonMax caps how many bytes of the last line a failed check wrote to its stderr the step's reason carries.
const releaseReasonMax = 1024

// recordKindStep records the release step at position of epic, which names a kind, once its check has ended. The step
// passes when the check ran, did not time out and exited 0, which `orrery release-step` does only once it has checked
// the step's postcondition in code and found it held. Its evidence is what the check wrote to its stdout, the facts that
// show the postcondition. It added no commit, so its head is its start. Otherwise it is recorded failed with the reason
// kindStepReason names. The first step's failure is handled as staleFirstStep does.
func (r *Runner) recordKindStep(
	ctx context.Context, epic store.EpicRow, act Action, position int64, step store.EpicReleaseStep, at time.Time,
) error {
	start := act.Items[1]
	check, err := r.store.NodeCheck(ctx, act.IDs[0])
	if err != nil {
		return fmt.Errorf("read the check of release step %d of epic %s: %w", position, epic.ID, err)
	}
	reason := kindStepReason(check)
	if position == 1 && reason != "" {
		host, hostErr := r.hostOf(epic)
		if hostErr != nil {
			return hostErr
		}
		var stale bool
		if reason, stale, err = r.staleFirstStep(ctx, host, epic, act, start, reason, at); err != nil || stale {
			return err
		}
	}
	end := releaseDone
	if reason != "" {
		end = releaseCheckFailed
	}
	run := store.ReleaseRun{
		Part: store.ReleasePartStep, Position: position, Attempt: act.Run.Attempt, Step: step.Step, Start: start,
		Head: start, End: end, Evidence: strings.TrimSpace(check.Stdout), Passed: reason == "", Reason: reason, At: at,
	}
	if err = r.store.RecordRelease(ctx, epic.ID, run); err != nil {
		return fmt.Errorf("record release step %d of epic %s: %w", position, epic.ID, err)
	}
	return nil
}

// kindStepReason returns why the check of a release step that names a kind did not pass, or "" when it did: the reason
// it did not run, its limit when it ran past it, or the status it exited with and the last line it wrote to its stderr,
// which is where the command says why the step failed.
func kindStepReason(check store.NodeCheck) string {
	switch {
	case check.NotRun != "":
		return "the step did not run: " + check.NotRun
	case check.TimedOut:
		return fmt.Sprintf("the step ran past its limit of %ds", check.Limit)
	case check.Exit != 0:
		reason := fmt.Sprintf("the step exited %d", check.Exit)
		if line := lastLine(check.Stderr); line != "" {
			reason += ": " + line
		}
		return reason
	}
	return ""
}

// lastLine returns the last line of text that is not blank, trimmed and cut to releaseReasonMax bytes, or "" when every
// line is blank.
func lastLine(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	line := strings.TrimSpace(lines[len(lines)-1])
	if len(line) > releaseReasonMax {
		line = strings.ToValidUTF8(line[:releaseReasonMax], "")
	}
	return line
}

// releaseStepEnd returns the HEAD of the release step whose row is row, which ran in worktree wt on host from start, and
// why it did not pass, or "" when it did. A step that did not end done is named by its end, and its HEAD is not read. A
// step that ended done whose worktree cannot be read is named as lost, with HEAD "".
func releaseStepEnd(
	ctx context.Context, host hostexec.Host, wt, start string, row store.StepRow, decodeReason string,
) (string, string, error) {
	if row.End != releaseDone {
		return "", releaseEndReason("step", row), nil
	}
	head, err := gitOutput(ctx, host, wt, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "", fmt.Sprintf("step ended done, but its worktree is gone, so HEAD cannot be read: %v", err), nil
	}
	head = strings.TrimSpace(head)
	reason, err := releaseStepReason(ctx, host, wt, start, head, decodeReason)
	return head, reason, err
}

// releaseStepReason returns why the release step that ended done, ran in worktree wt on host from start and has HEAD
// head did not pass. decodeReason says why its return did not decode, or is "" when it did. It returns "" when the step
// passed. Otherwise the first check it fails is named.
func releaseStepReason(ctx context.Context, host hostexec.Host, wt, start, head, decodeReason string) (string, error) {
	if decodeReason != "" {
		return decodeReason, nil
	}
	descends, err := isAncestor(ctx, host, wt, start, head)
	if err != nil {
		return "", err
	}
	if !descends {
		return fmt.Sprintf("HEAD %s does not descend from the step's start %s", head, start), nil
	}
	return "", nil
}

// releaseEndReason names the end of a row that is not done, for who, which is "step" or "judge": the end, the status it
// returned, the reason it stored, or else the first check it failed.
func releaseEndReason(who string, row store.StepRow) string {
	reason := who + " ended " + row.End
	if row.Status != "" {
		reason += " with status " + row.Status
	}
	if row.Reason != "" {
		reason += ": " + row.Reason
	}
	if i := slices.IndexFunc(row.Checks, func(check store.StepCheck) bool { return !check.OK }); i >= 0 {
		reason += ", failed check " + row.Checks[i].Name
	}
	return reason
}

// startJudge starts the release judge act names, read-only at the candidate act names, in the repository. The judge
// decides the postconditions from the release steps that have passed. Its role or start refused for good records it
// failed at once.
func (r *Runner) startJudge(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	id, candidate := act.IDs[0], act.Items[1]
	agent, err := roles.Agent(r.getenv, roleJudge)
	if err != nil {
		return r.failJudge(ctx, epic, act, at, err)
	}
	flow, err := r.store.EpicFlow(ctx, epic.ID)
	if err != nil {
		return fmt.Errorf("read epic %s: %w", epic.ID, err)
	}
	_, err = r.steps.Start(ctx, enginehost.StepStart{
		ID: id, Agent: agent, Prompt: releaseJudgePrompt(epic.ReleaseThen, epic.ReleaseSteps, flow.Releases),
		Schema: releaseJudgeSchema, Workspace: epic.Repository, Revision: candidate, ReadOnly: true, Epic: epic.ID,
		Purpose: fmt.Sprintf("Judge %s's release.", build.Slug(epic.ID)),
	})
	if err == nil {
		return nil
	}
	if !refusedForGood(err) {
		return fmt.Errorf("start judge %s: %w", id, err)
	}
	return r.failJudge(ctx, epic, act, at, err)
}

// failJudge records the release judge act names as a failed run of its attempt, with the reason it did not start.
func (r *Runner) failJudge(ctx context.Context, epic store.EpicRow, act Action, at time.Time, cause error) error {
	run := store.ReleaseRun{
		Part:    store.ReleasePartJudge,
		Attempt: act.Run.Attempt,
		Reason:  "the judge did not start: " + cause.Error(),
		At:      at,
	}
	if err := r.store.RecordRelease(ctx, epic.ID, run); err != nil {
		return fmt.Errorf("record judge of epic %s: %w", epic.ID, err)
	}
	return nil
}

// releaseJudgePrompt returns the prompt of a release judge: the release's promise, how a postcondition that names the
// released commit is read (releaseHeldRule), the step, commits and evidence of each agent step of steps that passed,
// and the step and evidence of each step that names a kind and passed, whose postcondition code checked and found held,
// so the judge takes it as met. The judge decides from that and from read-only checks of the repository.
func releaseJudgePrompt(then string, steps []store.EpicReleaseStep, runs []store.ReleaseRun) string {
	var prompt strings.Builder
	prompt.WriteString(
		"Judge whether this release met its postconditions, from the evidence below and read-only checks of the " +
			"repository.\n\n",
	)
	fmt.Fprintf(&prompt, "The release promised:\n%s\n\n%s\n\n", then, releaseHeldRule)
	var agentRuns, kindRuns []store.ReleaseRun
	for _, run := range runs {
		if run.Part != store.ReleasePartStep || !run.Passed {
			continue
		}
		if run.Position >= 1 && run.Position <= int64(len(steps)) && steps[run.Position-1].Kind != "" {
			kindRuns = append(kindRuns, run)
		} else {
			agentRuns = append(agentRuns, run)
		}
	}
	prompt.WriteString("The release steps that passed, each with the commits it added and the evidence it returned:\n")
	for _, run := range agentRuns {
		fmt.Fprintf(&prompt, "- %s\n  commits: %s\n  evidence: %s\n",
			run.Step, strings.Join(run.Commits, ", "), run.Evidence)
	}
	if len(kindRuns) > 0 {
		prompt.WriteString(
			"\nThese release steps ran as code, which checked each one's postcondition against origin and " +
				"the machine after the step and found it held. Take the postconditions they show as met, and do not check " +
				"them again:\n",
		)
		for _, run := range kindRuns {
			fmt.Fprintf(&prompt, "- %s\n  evidence: %s\n", run.Step, run.Evidence)
		}
	}
	prompt.WriteString(
		"\nYou may run read-only commands, such as git ls-remote or git log, to check each postcondition the evidence " +
			"does not cover. For each postcondition still uncovered, list it in uncovered.\n\n",
	)
	prompt.WriteString("Return status done. Return released true only when the evidence covers every postcondition, " +
		"and then uncovered is empty. Return released false otherwise, with uncovered naming each postcondition the " +
		"evidence does not cover.")
	return prompt.String()
}

// recordJudge records the release judge act names once its end is stored. The judge passes only when it ended done,
// released the epic and left no postcondition uncovered. Otherwise it is recorded failed with the reason named.
func (r *Runner) recordJudge(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	row, err := r.storedStep(ctx, epic.ID, act.IDs[0])
	if err != nil {
		return err
	}
	result, decodeReason := decodeReturn[releaseJudgeResult](row.Result)
	reason := releaseJudgeReason(row, result, decodeReason)
	run := store.ReleaseRun{
		Part:    store.ReleasePartJudge,
		Attempt: act.Run.Attempt,
		End:     row.End,
		Passed:  reason == "",
		Reason:  reason,
		At:      at,
	}
	if err = r.store.RecordRelease(ctx, epic.ID, run); err != nil {
		return fmt.Errorf("record judge of epic %s: %w", epic.ID, err)
	}
	return nil
}

// releaseJudgeReason returns why the judge whose row is row did not pass, or "" when it passed. decodeReason says why its
// return did not decode into result, or is "" when it did.
func releaseJudgeReason(row store.StepRow, result releaseJudgeResult, decodeReason string) string {
	switch {
	case row.End != releaseDone:
		return releaseEndReason("judge", row)
	case decodeReason != "":
		return decodeReason
	case len(result.Uncovered) > 0:
		return "uncovered postconditions: " + strings.Join(result.Uncovered, "; ")
	case !result.Released:
		return "the judge did not release the epic"
	}
	return ""
}

// storedStep returns the stored step row with id of epic, read from the epic's rows.
func (r *Runner) storedStep(ctx context.Context, epic, id string) (store.StepRow, error) {
	flow, err := r.store.EpicFlow(ctx, epic)
	if err != nil {
		return store.StepRow{}, fmt.Errorf("read epic %s: %w", epic, err)
	}
	i := slices.IndexFunc(flow.Steps, func(row store.StepRow) bool { return row.ID == id })
	if i < 0 {
		return store.StepRow{}, fmt.Errorf("step %s of epic %s is not stored", id, epic)
	}
	return flow.Steps[i], nil
}

// decodeReturn decodes the structured return a release step or judge stored as raw JSON. It returns why raw is not a
// JSON object of T, or "" when it is one.
func decodeReturn[T any](raw string) (T, string) {
	var value T
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return value, "the return is not a JSON object: " + err.Error()
	}
	return value, ""
}
