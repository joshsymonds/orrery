package interpret

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
)

// releaseAgent and judgeAgent are the agents the role file releaseRoles writes for the release and judge roles.
const (
	releaseAgent = "release-agent"
	judgeAgent   = "judge-agent"
)

// wantReleaseStepSchema and wantReleaseJudgeSchema are the schemas the brief fixes for a release step's return and the
// judge's, which the tests check the starts against.
const (
	wantReleaseStepSchema = `{"type":"object","properties":{"status":{"type":"string","enum":["done","blocked"]},` +
		`"commit":{"type":"string"},"commits":{"type":"array","items":{"type":"string"}},"evidence":{"type":"string"}},` +
		`"required":["status"]}`
	wantReleaseJudgeSchema = `{"type":"object","properties":{"status":{"type":"string","enum":["done"]},` +
		`"released":{"type":"boolean"},"uncovered":{"type":"array","items":{"type":"string"}}},` +
		`"required":["status","released","uncovered"]}`
)

// publishStep is the second release step the step tests run: publishing the tag, which its undo takes back.
func publishStep() store.EpicReleaseStep {
	return store.EpicReleaseStep{Step: "publish the tag", Target: "origin", Undo: "delete the tag on origin"}
}

// releaseRoles returns the environment of a release test: its role file names releaseAgent for the release role and
// judgeAgent for the judge.
func releaseRoles(t *testing.T) func(string) string {
	t.Helper()
	config := t.TempDir()
	dir := filepath.Join(config, "orrery")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("make role directory: %v", err)
	}
	roles := fmt.Sprintf("release = %q\njudge = %q\n", releaseAgent, judgeAgent)
	if err := os.WriteFile(filepath.Join(dir, "roles.toml"), []byte(roles), 0o600); err != nil {
		t.Fatalf("write role file: %v", err)
	}
	return func(key string) string {
		if key == "XDG_CONFIG_HOME" {
			return config
		}
		return ""
	}
}

// orderedSteps is the step fake a release world gives its runner. Each start records the release runs the store holds
// when the start is made, so a test can see what the release had recorded before a step started.
type orderedSteps struct {
	*fakeSteps
	store  *store.Store
	before [][]store.ReleaseRun
}

// Start records the release runs of the start's epic, then starts the step on the fake.
func (o *orderedSteps) Start(ctx context.Context, start enginehost.StepStart) (string, error) {
	flow, err := o.store.EpicFlow(ctx, start.Epic)
	if err != nil {
		return "", fmt.Errorf("read epic %s: %w", start.Epic, err)
	}
	o.before = append(o.before, flow.Releases)
	return o.fakeSteps.Start(ctx, start)
}

// releaseRunsOf returns the release runs of part, in the order the store holds them.
func releaseRunsOf(runs []store.ReleaseRun, part string) []store.ReleaseRun {
	var out []store.ReleaseRun
	for _, run := range runs {
		if run.Part == part {
			out = append(out, run)
		}
	}
	return out
}

// stepID returns the id of the release step at position, as the executor derives it from the release run.
func (w *releaseWorld) stepID(position int) string {
	w.t.Helper()
	return workID(w.t, w.s, releaseNodePath, fmt.Sprintf("release %d", position))
}

// judgeID returns the id of the release judge, as the executor derives it from the release run.
func (w *releaseWorld) judgeID() string {
	w.t.Helper()
	return workID(w.t, w.s, releaseNodePath, store.ReleasePartJudge)
}

// stepWorktree returns the worktree the release step id runs in, under the world's state directory.
func (w *releaseWorld) stepWorktree(id string) string {
	return filepath.Join(w.state, "release-worktrees", id, "wt")
}

// endStep ends the step id as the step service does: its return is stored as result, and the step ends with end and
// status.
func (w *releaseWorld) endStep(id, end, status string, result map[string]any) {
	w.t.Helper()
	encoded, err := json.Marshal(result)
	if err != nil {
		w.t.Fatalf("encode return of step %s: %v", id, err)
	}
	ops := []store.EngineOp{
		store.StepReturn{ID: id, Result: string(encoded)},
		store.StepEnd{ID: id, End: end, Status: status},
	}
	if err = w.s.ApplyEngine(w.t.Context(), ops, runClock()); err != nil {
		w.t.Fatalf("end step %s: %v", id, err)
	}
}

// finishStep does the work of the release step at position as its agent would: it commits a file in the step's
// worktree, and ends the step done with a return that lists that commit and evidence. It returns the commit.
func (w *releaseWorld) finishStep(position int, evidence string) string {
	w.t.Helper()
	id := w.stepID(position)
	commit := commitIn(w.t, w.stepWorktree(id), fmt.Sprintf("step%d.txt", position))
	w.endStep(id, "done", "done", map[string]any{
		"status": "done", "commit": commit, "commits": []string{commit}, "evidence": evidence,
	})
	return commit
}

// commitIn writes file in the worktree wt, commits it as the step's agent would, and returns the commit.
func commitIn(t *testing.T, wt, file string) string {
	t.Helper()
	nodeWrite(t, wt, file, "step\n")
	nodeGit(t, wt, "add", file)
	nodeGit(t, wt, "commit", "-q", "-m", "step "+file)
	return nodeGit(t, wt, "rev-parse", "HEAD")
}

// mergeSideCommit merges a commit made on a side branch of wt into start, as a step that merged would, and returns the
// merge commit.
func mergeSideCommit(t *testing.T, wt, start string) string {
	t.Helper()
	nodeGit(t, wt, "checkout", "-q", "-b", "side-step")
	side := commitIn(t, wt, "side.txt")
	nodeGit(t, wt, "checkout", "-q", "--detach", start)
	nodeGit(t, wt, "merge", "-q", "--no-ff", "-m", "merge side", side)
	return nodeGit(t, wt, "rev-parse", "HEAD")
}

// blockWorktrees makes the release worktrees directory of state a file, so no worktree of a release step can be added.
func blockWorktrees(t *testing.T, state string) {
	t.Helper()
	if err := os.MkdirAll(state, 0o750); err != nil {
		t.Fatalf("make state directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(state, "release-worktrees"), []byte("blocked\n"), 0o600); err != nil {
		t.Fatalf("write blocking file: %v", err)
	}
}

// TestReleaseStepStartsUnownedWritingInItsWorktree: the first release step starts at the candidate as an unowned
// writing step in a worktree of its own, detached at that commit. Its prompt names that step, its target and its undo,
// and not the next step. The step's target and undo are strings the prompt's fixed text does not hold, so the prompt
// can only hold them by naming this step.
func TestReleaseStepStartsUnownedWritingInItsWorktree(t *testing.T) {
	t.Parallel()
	w := newReleaseWorld(t, &recordingEnding{})
	mergeRemote(t, w.repo)
	nodeEpicBranch(t, w.repo, "epic.txt")
	lantern := store.EpicReleaseStep{
		Step: "cut the lantern tag", Target: "lantern-channel-7", Undo: "retract quartz-pin-9",
	}
	w.createEpic(runEpic, store.PhaseClean, lantern, publishStep())
	w.pass()

	candidate := runAt(t, w.s, releaseNodePath).Revision
	id := w.stepID(1)
	wt := w.stepWorktree(id)
	if len(w.fakes.steps.calls) != 1 {
		t.Fatalf("step starts = %+v, want the first release step started", w.fakes.steps.calls)
	}
	got := w.fakes.steps.calls[0]
	placed := got.Workspace == wt && got.Revision == candidate
	if got.ID != id || got.Epic != runEpic || got.Agent != releaseAgent || !placed {
		t.Errorf(
			"step start = %+v, want id %s, agent %s, workspace %s, revision %s",
			got,
			id,
			releaseAgent,
			wt,
			candidate,
		)
	}
	if got.ReadOnly || !got.Unowned || got.DoneValue != "done" || got.Schema != wantReleaseStepSchema {
		t.Errorf(
			"step start = %+v, want an unowned writing step whose done value is done and whose schema is fixed",
			got,
		)
	}
	for _, want := range []string{lantern.Step, lantern.Target, lantern.Undo, candidate} {
		if !strings.Contains(got.Prompt, want) {
			t.Errorf("prompt = %q, want it to name %q", got.Prompt, want)
		}
	}
	if strings.Contains(got.Prompt, "publish the tag") {
		t.Errorf("prompt = %q names the next release step, want only this one", got.Prompt)
	}
	if head := nodeGit(t, wt, "rev-parse", "HEAD"); head != candidate {
		t.Errorf("worktree HEAD = %q, want the candidate %q", head, candidate)
	}
}

// TestReleaseStepDoneWithListedCommitRecordsPassedBeforeNextStep: a release step that ends done, listing the one commit
// it added, is recorded as passed, and the next step starts after that record, at the commit the first one ended at.
// The first step's worktree is removed once it is recorded.
func TestReleaseStepDoneWithListedCommitRecordsPassedBeforeNextStep(t *testing.T) {
	t.Parallel()
	w := newReleaseWorld(t, &recordingEnding{})
	mergeRemote(t, w.repo)
	nodeEpicBranch(t, w.repo, "epic.txt")
	w.createEpic(runEpic, store.PhaseClean, tagStep(), publishStep())
	w.pass()
	candidate := runAt(t, w.s, releaseNodePath).Revision
	first := w.stepWorktree(w.stepID(1))
	commit := w.finishStep(1, "tagged main")
	w.pass()

	want := store.ReleaseRun{
		Part:     store.ReleasePartStep,
		Position: 1,
		Attempt:  runAt(t, w.s, releaseNodePath).Attempt,
		Step:     "tag the release",
		Start:    candidate,
		Head:     commit,
		End:      "done",
		Commits:  []string{commit},
		Evidence: "tagged main",
		Passed:   true,
		At:       runClock(),
	}
	got := releaseRunsOf(w.flowOf(runEpic).Releases, store.ReleasePartStep)
	if !reflect.DeepEqual(got, []store.ReleaseRun{want}) {
		t.Errorf("step runs = %+v, want the one passed run %+v", got, want)
	}
	if len(w.fakes.steps.calls) != 2 {
		t.Fatalf("step starts = %+v, want the second release step started", w.fakes.steps.calls)
	}
	second := w.fakes.steps.calls[1]
	if second.ID != w.stepID(2) || second.Revision != commit || second.Workspace != w.stepWorktree(w.stepID(2)) {
		t.Errorf("second step start = %+v, want it at %s in its own worktree", second, commit)
	}
	stored := releaseRunsOf(w.ordered.before[1], store.ReleasePartStep)
	if !reflect.DeepEqual(stored, []store.ReleaseRun{want}) {
		t.Errorf("step runs stored when the second step started = %+v, want the first step recorded passed", stored)
	}
	if _, err := os.Stat(first); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("first step worktree stat err = %v, want it removed once recorded", err)
	}
}

// TestReleaseContinueWhenListedCommitsDifferFromAdded: a release step that ends done and added commits, but lists none
// of them or lists a commit it did not add, is recorded as passed, since the judge checks the postconditions itself.
// The release continues and the next step starts.
func TestReleaseContinueWhenListedCommitsDifferFromAdded(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		do   func(t *testing.T, wt, start string) []string
	}{
		{
			name: "commit added, none listed",
			do: func(t *testing.T, wt, _ string) []string {
				t.Helper()
				commitIn(t, wt, "extra.txt")
				return []string{}
			},
		},
		{
			name: "commit added, a commit it did not add listed",
			do: func(t *testing.T, wt, start string) []string {
				t.Helper()
				commitIn(t, wt, "extra.txt")
				return []string{start}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newReleaseWorld(t, &recordingEnding{})
			mergeRemote(t, w.repo)
			nodeEpicBranch(t, w.repo, "epic.txt")
			w.createEpic(runEpic, store.PhaseClean, tagStep(), publishStep())
			w.pass()
			start := runAt(t, w.s, releaseNodePath).Revision
			id := w.stepID(1)
			listed := tc.do(t, w.stepWorktree(id), start)
			w.endStep(id, "done", "done", map[string]any{
				"status": "done", "commits": listed, "evidence": "evidence",
			})
			w.pass()

			runs := releaseRunsOf(w.flowOf(runEpic).Releases, store.ReleasePartStep)
			if len(runs) != 1 || !runs[0].Passed {
				t.Errorf("step runs = %+v, want one passed run", runs)
			}
			if got := len(w.fakes.steps.calls); got != 2 {
				t.Errorf("step starts = %d, want the second step started", got)
			}
		})
	}
}

// TestReleaseStepFailureStopsReleaseNamingIt: a release step whose end is not done, whose HEAD does not descend from
// its start, or that added a merge, is recorded as failed with the reason named. The release stops there and the next
// step does not start.
func TestReleaseStepFailureStopsReleaseNamingIt(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		end    string
		status string
		do     func(t *testing.T, wt, start string) []string
		reason string
	}{
		{
			name: "end returned", end: "returned", status: "blocked",
			do:     func(*testing.T, string, string) []string { return []string{} },
			reason: "step ended returned with status blocked",
		},
		{
			name: "HEAD does not descend from its start", end: "done", status: "done",
			do: func(t *testing.T, wt, _ string) []string {
				t.Helper()
				nodeGit(t, wt, "checkout", "-q", "--detach", "main")
				return []string{}
			},
			reason: "does not descend from the step's start",
		},
		{
			name: "merge commit added", end: "done", status: "done",
			do: func(t *testing.T, wt, start string) []string {
				t.Helper()
				return []string{mergeSideCommit(t, wt, start)}
			},
			reason: "added a merge commit",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newReleaseWorld(t, &recordingEnding{})
			mergeRemote(t, w.repo)
			nodeEpicBranch(t, w.repo, "epic.txt")
			w.createEpic(runEpic, store.PhaseClean, tagStep(), publishStep())
			w.pass()
			start := runAt(t, w.s, releaseNodePath).Revision
			id := w.stepID(1)
			listed := tc.do(t, w.stepWorktree(id), start)
			w.endStep(id, tc.end, tc.status, map[string]any{
				"status": tc.status, "commits": listed, "evidence": "evidence",
			})
			w.pass()

			runs := releaseRunsOf(w.flowOf(runEpic).Releases, store.ReleasePartStep)
			if len(runs) != 1 || runs[0].Passed || !strings.Contains(runs[0].Reason, tc.reason) {
				t.Errorf("step runs = %+v, want one failed run whose reason names %q", runs, tc.reason)
			}
			if phase := w.flowOf(runEpic).Epic.Phase; phase != store.PhaseStopped {
				t.Errorf("epic phase = %q, want stopped", phase)
			}
			if got := len(w.fakes.steps.calls); got != 1 {
				t.Errorf("step starts = %d, want only the first step started", got)
			}
		})
	}
}

// TestReleaseStepWorktreeGoneRecordsFailure: a release step whose worktree is gone when its end is recorded is recorded
// failed and the release stops there. A step that ended done names the worktree as gone, since its HEAD cannot be
// read. A step that did not end done is named by its end, which needs no HEAD.
func TestReleaseStepWorktreeGoneRecordsFailure(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		end    string
		status string
		reason string
	}{
		{name: "ended done", end: "done", status: "done", reason: "step ended done, but its worktree is gone"},
		{name: "ended not done", end: "returned", status: "blocked", reason: "step ended returned with status blocked"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newReleaseWorld(t, &recordingEnding{})
			mergeRemote(t, w.repo)
			nodeEpicBranch(t, w.repo, "epic.txt")
			w.createEpic(runEpic, store.PhaseClean, tagStep(), publishStep())
			w.pass()
			id := w.stepID(1)
			if err := os.RemoveAll(w.stepWorktree(id)); err != nil {
				t.Fatalf("remove worktree of step %s: %v", id, err)
			}
			w.endStep(id, tc.end, tc.status, map[string]any{
				"status": tc.status, "commits": []string{}, "evidence": "evidence",
			})
			w.pass()

			runs := releaseRunsOf(w.flowOf(runEpic).Releases, store.ReleasePartStep)
			if len(runs) != 1 || runs[0].Passed || !strings.Contains(runs[0].Reason, tc.reason) {
				t.Errorf("step runs = %+v, want one failed run whose reason names %q", runs, tc.reason)
			}
			if phase := w.flowOf(runEpic).Epic.Phase; phase != store.PhaseStopped {
				t.Errorf("epic phase = %q, want stopped", phase)
			}
		})
	}
}

// TestReleaseStepRefusedForGoodRecordsFailure: a release step whose role names no agent, whose worktree cannot be
// added, or whose start is refused for good is recorded as failed at once, with the reason named, and the release stops
// there. A worktree made for a refused start is removed.
func TestReleaseStepRefusedForGoodRecordsFailure(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		arrange func(t *testing.T, w *releaseWorld)
		starts  int
		cleaned bool
	}{
		{
			name: "role names no release agent",
			arrange: func(_ *testing.T, w *releaseWorld) {
				w.runner.getenv = func(string) string { return "" }
			},
			starts: 0, cleaned: true,
		},
		{
			name: "worktree cannot be added",
			arrange: func(t *testing.T, w *releaseWorld) {
				t.Helper()
				blockWorktrees(t, w.state)
			},
			starts: 0,
		},
		{
			name: "start refused for good",
			arrange: func(_ *testing.T, w *releaseWorld) {
				w.fakes.steps.refuse = enginehost.ErrBadSchema
			},
			starts: 1, cleaned: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newReleaseWorld(t, &recordingEnding{})
			mergeRemote(t, w.repo)
			nodeEpicBranch(t, w.repo, "epic.txt")
			w.createEpic(runEpic, store.PhaseClean, tagStep(), publishStep())
			tc.arrange(t, w)
			w.pass()

			runs := releaseRunsOf(w.flowOf(runEpic).Releases, store.ReleasePartStep)
			if len(runs) != 1 || runs[0].Passed || !strings.Contains(runs[0].Reason, "did not start") {
				t.Errorf("step runs = %+v, want one failed run whose reason says the step did not start", runs)
			}
			if phase := w.flowOf(runEpic).Epic.Phase; phase != store.PhaseStopped {
				t.Errorf("epic phase = %q, want stopped", phase)
			}
			if got := len(w.fakes.steps.calls); got != tc.starts {
				t.Errorf("step starts = %d, want %d", got, tc.starts)
			}
			if tc.cleaned {
				if _, err := os.Stat(
					filepath.Join(w.state, "release-worktrees", w.stepID(1)),
				); !errors.Is(
					err,
					fs.ErrNotExist,
				) {
					t.Errorf("step directory stat err = %v, want it removed", err)
				}
			}
		})
	}
}

// TestReleaseStepWithoutStateDirIsNotStarted: a runner with no state directory has nowhere to add a release step's
// worktree, so it does not start the step and says why.
func TestReleaseStepWithoutStateDirIsNotStarted(t *testing.T) {
	t.Parallel()
	w := newReleaseWorld(t, &recordingEnding{})
	mergeRemote(t, w.repo)
	nodeEpicBranch(t, w.repo, "epic.txt")
	w.createEpic(runEpic, store.PhaseClean, tagStep())
	w.runner.stateDir = ""
	logs := w.pass()

	if !strings.Contains(logs, "the runner was not given a state directory") {
		t.Errorf("logs = %q, want the missing state directory named", logs)
	}
	if len(w.fakes.steps.calls) != 0 {
		t.Errorf("step starts = %+v, want none", w.fakes.steps.calls)
	}
}

// TestReleaseJudgeStartsReadOnlyWithEvidence: once the release's steps have passed, the judge starts read-only at the
// candidate, in the repository, with the release promise and the commits and evidence of each passed step in its
// prompt. Its prompt tells it it may run read-only commands to check the postconditions, and forbids none.
func TestReleaseJudgeStartsReadOnlyWithEvidence(t *testing.T) {
	t.Parallel()
	w := newReleaseWorld(t, &recordingEnding{})
	mergeRemote(t, w.repo)
	nodeEpicBranch(t, w.repo, "epic.txt")
	w.createEpic(runEpic, store.PhaseClean, tagStep())
	w.pass()
	candidate := runAt(t, w.s, releaseNodePath).Revision
	commit := w.finishStep(1, "tagged main")
	w.pass()

	if len(w.fakes.steps.calls) != 2 {
		t.Fatalf("step starts = %+v, want the judge started after the step", w.fakes.steps.calls)
	}
	got := w.fakes.steps.calls[1]
	if got.ID != w.judgeID() || got.Epic != runEpic || got.Agent != judgeAgent || got.Schema != wantReleaseJudgeSchema {
		t.Errorf("judge start = %+v, want id %s, agent %s and the fixed judge schema", got, w.judgeID(), judgeAgent)
	}
	if !got.ReadOnly || got.Workspace != w.repo || got.Revision != candidate {
		t.Errorf("judge start = %+v, want read-only in the repository at candidate %s", got, candidate)
	}
	for _, want := range []string{releaseThen, commit, "tagged main", "read-only commands"} {
		if !strings.Contains(got.Prompt, want) {
			t.Errorf("judge prompt = %q, want it to hold %q", got.Prompt, want)
		}
	}
	if strings.Contains(got.Prompt, "Run no command") {
		t.Errorf("judge prompt = %q, want it to forbid no command", got.Prompt)
	}
}

// TestReleaseJudgeDecidesFromItsReturn: a judge that returns released with nothing uncovered ends the release released.
// Any other return records the judge failed, with the postconditions it left uncovered, or the end it ended with,
// named, and the release stops.
func TestReleaseJudgeDecidesFromItsReturn(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		end      string
		status   string
		result   map[string]any
		released bool
		reason   string
	}{
		{
			name: "released with nothing uncovered", end: "done", status: "done",
			result:   map[string]any{"status": "done", "released": true, "uncovered": []string{}},
			released: true,
		},
		{
			name: "released with an uncovered postcondition", end: "done", status: "done",
			result: map[string]any{
				"status": "done", "released": true, "uncovered": []string{"the docs are published"},
			},
			reason: "the docs are published",
		},
		{
			name: "not released", end: "done", status: "done",
			result: map[string]any{"status": "done", "released": false, "uncovered": []string{}},
			reason: "did not release",
		},
		{
			name: "judge ended returned", end: "returned", status: "blocked",
			result: map[string]any{"status": "blocked", "released": false, "uncovered": []string{}},
			reason: "judge ended returned with status blocked",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ending := &recordingEnding{}
			w := newReleaseWorld(t, ending)
			mergeRemote(t, w.repo)
			nodeEpicBranch(t, w.repo, "epic.txt")
			w.createEpic(runEpic, store.PhaseClean, tagStep())
			w.pass()
			w.finishStep(1, "tagged main")
			w.pass()
			w.endStep(w.judgeID(), tc.end, tc.status, tc.result)
			w.pass()

			runs := releaseRunsOf(w.flowOf(runEpic).Releases, store.ReleasePartJudge)
			if tc.released {
				if len(runs) != 1 || !runs[0].Passed {
					t.Errorf("judge runs = %+v, want the one passed judge", runs)
				}
				want := endedEpic{
					epic:  runEpic,
					phase: "released",
					body:  kinds.EndingBody("released", nil, nil),
					at:    runClock(),
				}
				if len(ending.calls) == 0 {
					t.Fatalf("EndEpic calls = none, want %+v", want)
				}
				for _, call := range ending.calls {
					if !reflect.DeepEqual(call, want) {
						t.Errorf("EndEpic call = %+v, want %+v", call, want)
					}
				}
				return
			}
			if len(runs) != 1 || runs[0].Passed || !strings.Contains(runs[0].Reason, tc.reason) {
				t.Errorf("judge runs = %+v, want one failed judge whose reason names %q", runs, tc.reason)
			}
			if phase := w.flowOf(runEpic).Epic.Phase; phase != store.PhaseStopped {
				t.Errorf("epic phase = %q, want stopped", phase)
			}
			if len(ending.calls) != 0 {
				t.Errorf("EndEpic calls = %+v, want none for a release that did not release", ending.calls)
			}
		})
	}
}

// TestReleasePostconditionsShown: a release step's prompt carries the release's postconditions as the contract gives
// them, and says that a judge reads only the step's evidence, so the step's evidence must show each postcondition it
// affects with the output that proves it.
func TestReleasePostconditionsShown(t *testing.T) {
	t.Parallel()
	t.Run("a release step's prompt holds the contract's postconditions", func(t *testing.T) {
		t.Parallel()
		w := newReleaseWorld(t, &recordingEnding{})
		mergeRemote(t, w.repo)
		nodeEpicBranch(t, w.repo, "epic.txt")
		w.createEpic(runEpic, store.PhaseClean, tagStep())
		w.pass()
		if len(w.fakes.steps.calls) != 1 {
			t.Fatalf("step starts = %+v, want the first release step started", w.fakes.steps.calls)
		}
		want := "The release promises: " + releaseThen
		if prompt := w.fakes.steps.calls[0].Prompt; !strings.Contains(prompt, want) {
			t.Errorf("prompt = %q, want it to hold %q", prompt, want)
		}
	})
	t.Run("a release step's prompt says its judge reads only the step's evidence", func(t *testing.T) {
		t.Parallel()
		const judgeParagraph = "A judge then decides whether the release met its postconditions from the evidence " +
			"its steps return and nothing else, and it runs no command. So the evidence this step returns must show " +
			"each postcondition this step affects, with the output that proves it, such as the command this step ran " +
			"and that command's output."
		prompt := releaseStepPrompt(tagStep(), "0123abcd", releaseThen)
		if !strings.Contains(prompt, judgeParagraph) {
			t.Errorf("prompt = %q, want it to hold %q", prompt, judgeParagraph)
		}
	})
}
