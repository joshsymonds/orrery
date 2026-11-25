package interpret

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/store"
)

// roundWorld is an integrate world whose accepted tasks 1 and 2 both change b.go, so task 2's merge conflicts with the
// candidate that task 1 composed. Its runner has a state directory and a role file that names an integrator, so a
// round's step starts. second is task 2's revision.
type roundWorld struct {
	*integrateWorld
	second string
}

// mergeIdentity gives the world's repository the identity the task merge's commit is written under, as a release merge's
// test repository does.
func (w *integrateWorld) mergeIdentity() {
	w.t.Helper()
	w.git("config", "user.name", "Test")
	w.git("config", "user.email", "test@example.invalid")
}

// newRoundWorld returns a roundWorld whose batch is claimed, so task 2's round 1 has started.
func newRoundWorld(t *testing.T) roundWorld {
	t.Helper()
	w := newIntegrateWorld(t)
	w.mergeIdentity()
	w.runner.stateDir = t.TempDir()
	w.runner.getenv = integratorRoles(t)
	first := w.commit(w.base, "b.go", "one\n")
	second := w.commit(w.base, "b.go", "two\n")
	w.pass()
	w.planTurn()
	w.accept(1, w.base, first)
	w.accept(2, w.base, second)
	w.pass()
	return roundWorld{integrateWorld: w, second: second}
}

// roundID returns the id of the step of round of task 2's integrator.
func (w roundWorld) roundID(round int) string {
	w.t.Helper()
	return workID(w.t, w.s, integrateBatch, fmt.Sprintf("task integrator T2 %d", round))
}

// roundStart returns the start of round's step, which must have started.
func (w roundWorld) roundStart(round int) enginehost.StepStart {
	w.t.Helper()
	id := w.roundID(round)
	for _, start := range w.fakes.steps.calls {
		if start.ID == id {
			return start
		}
	}
	w.t.Fatalf("no start of step %s of round %d among %+v", id, round, w.fakes.steps.calls)
	return enginehost.StepStart{}
}

// roundStarts returns the starts of task integrator steps, in order.
func (w roundWorld) roundStarts() []enginehost.StepStart {
	var starts []enginehost.StepStart
	for _, start := range w.fakes.steps.calls {
		if start.Schema == TaskIntegratorSchema {
			starts = append(starts, start)
		}
	}
	return starts
}

// roundRecord returns the recorded round of task 2 at round, which must be recorded.
func (w roundWorld) roundRecord(round int64) store.IntegrateRound {
	w.t.Helper()
	flow, err := w.s.EpicFlow(w.t.Context(), runEpic)
	if err != nil {
		w.t.Fatalf("EpicFlow: %v", err)
	}
	i := slices.IndexFunc(flow.Rounds, func(record store.IntegrateRound) bool {
		return record.Task == 2 && record.Round == round
	})
	if i < 0 {
		w.t.Fatalf("no record of round %d of task 2 among %+v", round, flow.Rounds)
	}
	return flow.Rounds[i]
}

// mergeRound finishes the merge in start's worktree as the task integrator would: b.go holds text, and the merge commit
// carries the message of the merge, with the candidate and then task 2 as its parents. It returns the commit.
func (w roundWorld) mergeRound(start enginehost.StepStart, text string) string {
	w.t.Helper()
	nodeWrite(w.t, start.Workspace, "b.go", text)
	nodeGit(w.t, start.Workspace, "add", "b.go")
	nodeGit(w.t, start.Workspace, "commit", "-q", "-m", taskMergeMessage(2, w.second))
	return nodeGit(w.t, start.Workspace, "rev-parse", "HEAD")
}

// gateAt returns the id of the first Done gate check started at revision.
func (w roundWorld) gateAt(revision string) string {
	w.t.Helper()
	for _, call := range w.fakes.checks.calls {
		if call.Revision == revision {
			return call.ID
		}
	}
	w.t.Fatalf("no Done gate check started at %s", revision)
	return ""
}

// captureAt returns the id of the first capture started at revision.
func (w roundWorld) captureAt(revision string) string {
	w.t.Helper()
	for _, call := range w.fakes.evidence.calls {
		if call.revision == revision {
			return call.id
		}
	}
	w.t.Fatalf("no capture started at %s", revision)
	return ""
}

// endGateAt ends the Done gate check started at revision with exit and stderr.
func (w roundWorld) endGateAt(revision string, exit int, stderr string) {
	w.t.Helper()
	nodeEndCheck(w.t, w.s, w.gateAt(revision), exit, stderr)
}

// endTaskStep ends the step with id as its agent did: its return stored as result when result is not nil, then its end
// and status.
func endTaskStep(t *testing.T, s *store.Store, id, end, status string, result map[string]any) {
	t.Helper()
	var ops []store.EngineOp
	if result != nil {
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatalf("encode return of step %s: %v", id, err)
		}
		ops = append(ops, store.StepReturn{ID: id, Result: string(encoded)})
	}
	ops = append(ops, store.StepEnd{ID: id, End: end, Status: status})
	if err := s.ApplyEngine(t.Context(), ops, runClock()); err != nil {
		t.Fatalf("end step %s: %v", id, err)
	}
}

// TestConflictIntegratorStartsOneStepInTheMergeWorktree: a task whose merge conflicts starts exactly one task integrator
// step, in a worktree detached at the candidate, with task 2's revision as MERGE_HEAD and b.go conflicted. The brief
// carries task 2, the task being merged, and task 1, the task that changed b.go on the candidate's side.
func TestConflictIntegratorStartsOneStepInTheMergeWorktree(t *testing.T) {
	t.Parallel()
	w := newRoundWorld(t)
	if n := len(w.roundStarts()); n != 1 {
		t.Fatalf("task integrator steps = %d, want one", n)
	}
	start := w.roundStart(1)
	candidate := w.candidate(integrateBatch)
	if start.Agent != integratorAgent || start.Revision != candidate || start.Task != integrateBatch ||
		!start.Unowned || start.DoneValue != "done" || start.Epic != runEpic || start.ReturnCheck == nil {
		t.Errorf("start = %+v, want an unowned writing step under %s at %s, labeled %s", start, integratorAgent,
			candidate, integrateBatch)
	}
	wt := start.Workspace
	if head := nodeGit(t, wt, "rev-parse", "HEAD"); head != candidate {
		t.Errorf("worktree HEAD = %s, want the candidate %s", head, candidate)
	}
	if mergeHead := nodeGit(t, wt, "rev-parse", "MERGE_HEAD"); mergeHead != w.second {
		t.Errorf("MERGE_HEAD = %s, want task 2's revision %s", mergeHead, w.second)
	}
	if unmerged := nodeGit(t, wt, "diff", "--name-only", "--diff-filter=U"); unmerged != "b.go" {
		t.Errorf("unmerged paths = %q, want b.go", unmerged)
	}
	prompt := start.Prompt
	for _, want := range []string{
		"\n## Task\n- T2 second\n",
		"\n## Conflicted paths\n- b.go\n  candidate T1 first, merged as ",
		"\n## Previous attempt\nNone.\n",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt)
		}
	}
}

// TestConflictIntegratorMergedRoundComposesTheTask: a merged return whose commit passes runs the Done gate and then the
// capture at the commit, and once both pass the commit composes task 2 into the candidate. The commit is pinned, its
// parents are the candidate and then task 2, and the worktree is removed. The batch then ends its own gates at the
// commit, the epic's branch moves there, and task 2 is not sent back.
func TestConflictIntegratorMergedRoundComposesTheTask(t *testing.T) {
	t.Parallel()
	w := newRoundWorld(t)
	candidate := w.candidate(integrateBatch)
	start := w.roundStart(1)
	head := w.mergeRound(start, "both\n")
	endTaskStep(t, w.s, w.roundID(1), "done", "done", taskReturn(verdictMerged, head, "b.go keeps both lines"))
	w.pass()

	if got := w.roundRecord(1); got.Verdict != verdictMerged || got.Head != head || got.Outcome != "" {
		t.Errorf("round 1 = %+v, want merged at %s and not yet decided", got, head)
	}
	if _, err := os.Stat(start.Workspace); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("worktree %s stat error = %v, want it removed once the round is recorded", start.Workspace, err)
	}
	if refs := nodeGit(
		t,
		w.repo,
		"for-each-ref",
		"--format=%(objectname)",
		"refs/orrery/",
	); !strings.Contains(
		refs,
		head,
	) {
		t.Errorf("refs under refs/orrery/ = %q, want the merged commit %s pinned", refs, head)
	}
	w.endGateAt(head, 0, "")
	w.pass()
	nodeEndEvidence(t, w.s, w.captureAt(head))
	w.pass()

	if got := w.roundRecord(1); got.Outcome != "composed" {
		t.Errorf("round 1 outcome = %q, want composed", got.Outcome)
	}
	run := runAt(t, w.s, integrateBatch)
	if run.Revision != head {
		t.Errorf("batch revision = %s, want the composed commit %s", run.Revision, head)
	}
	if parents := w.git("rev-list", "--parents", "-n1", head); parents != head+" "+candidate+" "+w.second {
		t.Errorf("merge commit %q, want parents the candidate %s then task 2 %s", parents, candidate, w.second)
	}
	if !strings.Contains(run.Result, fmt.Sprintf(`{"task":2,"commit":%q}`, head)) {
		t.Errorf("batch result = %s, want task 2 composed into %s", run.Result, head)
	}
	w.endGate(integrateBatch, 0, "")
	w.pass()
	w.endCapture(integrateBatch)
	w.pass()
	if tip := w.git("rev-parse", integrateBranch); tip != head {
		t.Errorf("branch = %s, want it advanced to the composed commit %s", tip, head)
	}
	if status := taskStatus(t, w.s, 2); status != taskDone {
		t.Errorf("task 2 status = %q, want done: a composed task is not sent back", status)
	}
}

// TestConflictIntegratorFailedGateStartsTheNextRound: a Done gate that fails on a round's commit, and whose triage finds a
// change, records the round failed with the gate's output and the triage's citation, and the next round starts with
// that output as its previous attempt. Its merge then composes task 2 once its gate passes.
func TestConflictIntegratorFailedGateStartsTheNextRound(t *testing.T) {
	t.Parallel()
	w := newRoundWorld(t)
	head1 := w.mergeRound(w.roundStart(1), "both\n")
	endTaskStep(t, w.s, w.roundID(1), "done", "done", taskReturn(verdictMerged, head1, ""))
	w.pass()
	w.endGateAt(head1, 1, "lint: unused import")
	w.pass()
	w.answerTriage(head1, "change", "", "lint: unused import")

	failed := w.roundRecord(1)
	if failed.Outcome != "failed" || !strings.Contains(failed.Failure, "Done gate lint failed on commit "+head1) ||
		!strings.Contains(failed.Failure, `triage: change, citing "lint: unused import": judged so`) {
		t.Errorf("round 1 = %+v, want failed on its gate at %s with the triage's citation", failed, head1)
	}
	start := w.roundStart(2)
	if !strings.Contains(start.Prompt, "\n## Previous attempt\nDone gate lint failed on commit "+head1) {
		t.Errorf("round 2 prompt lacks round 1's failure:\n%s", start.Prompt)
	}
	head2 := w.mergeRound(start, "both again\n")
	endTaskStep(t, w.s, w.roundID(2), "done", "done", taskReturn(verdictMerged, head2, ""))
	w.pass()
	w.endGateAt(head2, 0, "")
	w.pass()
	nodeEndEvidence(t, w.s, w.captureAt(head2))
	w.pass()
	if got := w.roundRecord(2); got.Outcome != "composed" || got.Head != head2 {
		t.Errorf("round 2 = %+v, want composed at %s", got, head2)
	}
	if n := len(w.roundStarts()); n != 2 {
		t.Errorf("task integrator steps = %d, want two", n)
	}
}

// TestConflictIntegratorUnjudgedCaptureStartsTheNextRound: a capture that did not run on a round's commit fails the round
// with the capture's reason, and the next round starts with it.
func TestConflictIntegratorUnjudgedCaptureStartsTheNextRound(t *testing.T) {
	t.Parallel()
	w := newRoundWorld(t)
	head1 := w.mergeRound(w.roundStart(1), "both\n")
	endTaskStep(t, w.s, w.roundID(1), "done", "done", taskReturn(verdictMerged, head1, ""))
	w.pass()
	w.endGateAt(head1, 0, "")
	w.pass()
	id := w.captureAt(head1)
	if err := w.s.RecordEvidenceCapture(t.Context(), id, store.EvidenceCapture{
		NotCaptured: "the scripts/check did not start", At: runClock(),
	}); err != nil {
		t.Fatalf("RecordEvidenceCapture %s: %v", id, err)
	}
	w.pass()

	if got := w.roundRecord(1); got.Outcome != "failed" ||
		!strings.Contains(got.Failure, "The capture of R1 did not run on commit "+head1) {
		t.Errorf("round 1 = %+v, want failed on its capture at %s", got, head1)
	}
	if prompt := w.roundStart(
		2,
	).Prompt; !strings.Contains(
		prompt,
		"\n## Previous attempt\nThe capture of R1 did not run",
	) {
		t.Errorf("round 2 prompt lacks round 1's capture failure:\n%s", prompt)
	}
}

// TestConflictIntegratorThirdFailedRoundLeavesTheTaskOut: three rounds whose commits fail their Done gate, each triaged
// a change, start three steps and no more. The task is then left out, and the batch goes on at its candidate: the Done
// gate walks there, and the task is sent back.
func TestConflictIntegratorThirdFailedRoundLeavesTheTaskOut(t *testing.T) {
	t.Parallel()
	w := newRoundWorld(t)
	candidate := w.candidate(integrateBatch)
	for round := 1; round <= 3; round++ {
		head := w.mergeRound(w.roundStart(round), fmt.Sprintf("round %d\n", round))
		endTaskStep(t, w.s, w.roundID(round), "done", "done", taskReturn(verdictMerged, head, ""))
		w.pass()
		w.endGateAt(head, 1, "still red")
		w.pass()
		w.answerTriage(head, "change", "", "still red")
	}
	if n := len(w.roundStarts()); n != 3 {
		t.Fatalf("task integrator steps = %d, want three", n)
	}
	if got := w.roundRecord(3); got.Outcome != "failed" {
		t.Errorf("round 3 = %+v, want failed", got)
	}
	w.endGate(integrateBatch, 0, "")
	w.pass()
	w.endCapture(integrateBatch)
	w.pass()
	if tip := w.git("rev-parse", integrateBranch); tip != candidate {
		t.Errorf("branch = %s, want the batch's candidate %s", tip, candidate)
	}
	if status := taskStatus(t, w.s, 2); status != taskOpen {
		t.Errorf("task 2 status = %q, want open: a task its rounds left out is sent back", status)
	}
}

// TestConflictIntegratorClashLeavesTheTaskOutAndSendsItBack: a clash return records the round clash with its explanation,
// starts no further round, and leaves the task out: the batch goes on at its candidate and sends the task back.
func TestConflictIntegratorClashLeavesTheTaskOutAndSendsItBack(t *testing.T) {
	t.Parallel()
	w := newRoundWorld(t)
	candidate := w.candidate(integrateBatch)
	endTaskStep(t, w.s, w.roundID(1), "done", "done",
		taskReturn(verdictClash, "", "both sides decide the title"))
	w.pass()
	if got := w.roundRecord(1); got.Verdict != verdictClash || got.Explanation != "both sides decide the title" {
		t.Errorf("round 1 = %+v, want clash with its explanation", got)
	}
	if n := len(w.roundStarts()); n != 1 {
		t.Errorf("task integrator steps = %d, want one: a clash ends the rounds", n)
	}
	w.endGate(integrateBatch, 0, "")
	w.pass()
	w.endCapture(integrateBatch)
	w.pass()
	if tip := w.git("rev-parse", integrateBranch); tip != candidate {
		t.Errorf("branch = %s, want the batch's candidate %s", tip, candidate)
	}
	if status := taskStatus(t, w.s, 2); status != taskOpen {
		t.Errorf("task 2 status = %q, want open: a clashed task is sent back", status)
	}
}

// TestConflictIntegratorEndedStepEndsTheRounds: a step that ends without a done return records its round ended with how
// it ended, and no further round starts.
func TestConflictIntegratorEndedStepEndsTheRounds(t *testing.T) {
	t.Parallel()
	w := newRoundWorld(t)
	endTaskStep(t, w.s, w.roundID(1), "stopped", "", nil)
	w.pass()
	got := w.roundRecord(1)
	if got.Verdict != "ended" || !strings.Contains(got.Explanation, "task integrator ended stopped") {
		t.Errorf("round 1 = %+v, want ended with how the step ended", got)
	}
	if n := len(w.roundStarts()); n != 1 {
		t.Errorf("task integrator steps = %d, want one: an ended step ends the rounds", n)
	}
}

// TestConflictIntegratorRefusedCommitEndsTheRounds: a merged return whose commit has the task before the candidate is
// refused when its round is recorded, so the round is recorded ended with the refusal and no further round starts.
func TestConflictIntegratorRefusedCommitEndsTheRounds(t *testing.T) {
	t.Parallel()
	w := newRoundWorld(t)
	start := w.roundStart(1)
	candidate := w.candidate(integrateBatch)
	nodeWrite(t, start.Workspace, "b.go", "both\n")
	nodeGit(t, start.Workspace, "add", "b.go")
	tree := nodeGit(t, start.Workspace, "write-tree")
	reversed := nodeGit(t, start.Workspace, "commit-tree", tree, "-p", w.second, "-p", candidate, "-m", "reversed")
	endTaskStep(t, w.s, w.roundID(1), "done", "done", taskReturn(verdictMerged, reversed, ""))
	w.pass()
	got := w.roundRecord(1)
	refused := "the task integrator's commit was refused: "
	if got.Verdict != "ended" || !strings.HasPrefix(got.Explanation, refused) ||
		!strings.Contains(got.Explanation, "parents: commit "+reversed+" has parents") {
		t.Errorf("round 1 = %+v, want ended with the parents refused", got)
	}
	if n := len(w.roundStarts()); n != 1 {
		t.Errorf("task integrator steps = %d, want one", n)
	}
}

// TestConflictIntegratorComposedTaskIsNotSentBack: the result a round writes when its commit composes task 2 lists task 2
// as composed and no longer as left out, so the batch's end sends back only the task it still leaves out, task 3.
func TestConflictIntegratorComposedTaskIsNotSentBack(t *testing.T) {
	t.Parallel()
	result := `{"base":"b","candidate":"c","composed":[{"task":1,"commit":"c1"}],"leftOut":[` +
		`{"task":2,"reason":"T2 conflicts","conflicts":["b.go"]},{"task":3,"reason":"T3 conflicts","conflicts":["a.go"]}]}`
	composed, err := composedRound(result, 2, "m1")
	if err != nil {
		t.Fatalf("composedRound: %v", err)
	}
	if !strings.Contains(composed, `{"task":2,"commit":"m1"}`) {
		t.Errorf("result = %s, want task 2 composed into m1", composed)
	}
	sends := sendBacks(composed)
	if len(sends) != 1 || sends[0].Seq != 3 {
		t.Errorf("send backs = %+v, want only task 3", sends)
	}
}

// taskStatus returns the status of task seq of the test epic.
func taskStatus(t *testing.T, s *store.Store, seq int64) string {
	t.Helper()
	tasks := epicOf(t, s).Tasks
	i := slices.IndexFunc(tasks, func(task store.EpicTask) bool { return task.Seq == seq })
	if i < 0 {
		t.Fatalf("no task %d", seq)
	}
	return tasks[i].Status
}
