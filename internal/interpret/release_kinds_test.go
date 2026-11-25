package interpret

import (
	"errors"
	"io/fs"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/releasestep"
	"github.com/joshsymonds/orrery/internal/store"
)

// testOrrery is the orrery binary the kind tests give their runner, which the argv of each check names.
const testOrrery = "/opt/orrery-test/bin/orrery"

// pushMainStep is a release step that names the push-main kind, as orrery's own project file names its first step.
func pushMainStep() store.EpicReleaseStep {
	return store.EpicReleaseStep{
		Step: "fast-forward main to the candidate", Target: "origin's main", Undo: "push a revert",
		Kind: releasestep.KindPushMain, Params: releasestep.Params{Branch: "main"},
	}
}

// tagKindStep is a release step that names the tag kind, with v as its version prefix.
func tagKindStep() store.EpicReleaseStep {
	return store.EpicReleaseStep{
		Step: "push a signed tag", Target: "origin's tags", Undo: "delete the tag on origin",
		Kind: releasestep.KindTag, Params: releasestep.Params{Prefix: "v"},
	}
}

// newKindWorld returns a release world whose runner runs testOrrery, with origin set up and the epic's branch one
// commit on main, and with the test epic stored clean with steps as its release steps.
func newKindWorld(t *testing.T, ending Ending, steps ...store.EpicReleaseStep) *releaseWorld {
	t.Helper()
	w := newReleaseWorld(t, ending)
	w.runner.orrery = testOrrery
	mergeRemote(t, w.repo)
	nodeEpicBranch(t, w.repo, "epic.txt")
	w.createEpic(runEpic, store.PhaseClean, steps...)
	return w
}

// endCheck ends the check id as the node checks service does once its command has exited with exit, having written
// stdout and stderr.
func (w *releaseWorld) endCheck(id string, exit int, stdout, stderr string) {
	w.t.Helper()
	end := store.NodeCheckEnd{Exit: exit, Stdout: stdout, Stderr: stderr}
	if err := w.s.EndNodeCheck(w.t.Context(), id, end, runClock()); err != nil {
		w.t.Fatalf("end check %s: %v", id, err)
	}
}

// TestReleaseKindStepStartsAsCheck: a release step that names a kind starts as a check of the release run under the
// step's id, at the candidate, running `orrery release-step` with the step's kind and parameters within the kind's
// limit. No agent step starts for it and no release worktree is made for it.
func TestReleaseKindStepStartsAsCheck(t *testing.T) {
	t.Parallel()
	w := newKindWorld(t, &recordingEnding{}, pushMainStep(), tagKindStep())
	w.pass()

	candidate := runAt(t, w.s, releaseNodePath).Revision
	want := store.NodeCheck{
		ID:       w.stepID(1),
		Epic:     runEpic,
		Path:     releaseNodePath,
		Revision: candidate,
		Argv: releasestep.Argv(
			testOrrery,
			releasestep.KindPushMain,
			pushMainStep().Params,
			candidate,
			"Release the epic",
		),
		Limit: int64(releasestep.Limit(releasestep.KindPushMain).Seconds()),
	}
	if !reflect.DeepEqual(w.fakes.checks.calls, []store.NodeCheck{want}) {
		t.Errorf("check starts = %+v, want the one check %+v", w.fakes.checks.calls, want)
	}
	if len(w.fakes.steps.calls) != 0 {
		t.Errorf("step starts = %+v, want no agent step for a step that names a kind", w.fakes.steps.calls)
	}
	if _, err := os.Stat(w.stepWorktree(w.stepID(1))); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("release worktree stat err = %v, want none made for a step that names a kind", err)
	}
}

// TestReleaseKindStepPassesOnExitZero: a running check of a step that names a kind holds the release, and once it exits
// 0 the step is recorded passed with what the check wrote to its stdout as its evidence, its start as its head, and no
// commits. The next step that names a kind then starts at that head, with the epic's title for the change.
func TestReleaseKindStepPassesOnExitZero(t *testing.T) {
	t.Parallel()
	w := newKindWorld(t, &recordingEnding{}, pushMainStep(), tagKindStep())
	w.pass()
	candidate := runAt(t, w.s, releaseNodePath).Revision
	w.pass()
	if len(w.fakes.checks.calls) != 1 || len(releaseRunsOf(w.flowOf(runEpic).Releases, store.ReleasePartStep)) != 0 {
		t.Fatalf("check starts = %+v, want the first step's check alone, unrecorded while it runs",
			w.fakes.checks.calls)
	}

	w.endCheck(w.stepID(1), 0, "origin's main is at "+candidate+", which contains "+candidate+"\n", "")
	w.pass()

	want := store.ReleaseRun{
		Part: store.ReleasePartStep, Position: 1, Attempt: runAt(t, w.s, releaseNodePath).Attempt,
		Step: pushMainStep().Step, Start: candidate, Head: candidate, End: "done",
		Evidence: "origin's main is at " + candidate + ", which contains " + candidate, Passed: true, At: runClock(),
	}
	if got := releaseRunsOf(w.flowOf(runEpic).Releases, store.ReleasePartStep); !reflect.DeepEqual(
		got, []store.ReleaseRun{want},
	) {
		t.Errorf("step runs = %+v, want the one passed run %+v", got, want)
	}
	if len(w.fakes.checks.calls) != 2 {
		t.Fatalf("check starts = %+v, want the second step's check started", w.fakes.checks.calls)
	}
	second := w.fakes.checks.calls[1]
	wantArgv := releasestep.Argv(testOrrery, releasestep.KindTag, tagKindStep().Params, candidate, "Release the epic")
	if second.ID != w.stepID(2) || second.Revision != candidate || !reflect.DeepEqual(second.Argv, wantArgv) {
		t.Errorf("second check = %+v, want %s at %s running %v", second, w.stepID(2), candidate, wantArgv)
	}
}

// TestReleaseKindStepFailureStopsTheRelease: a check of a step that names a kind that exits nonzero records the step
// failed, with its exit and the last line it wrote to its stderr as the reason, and the release stops at it.
func TestReleaseKindStepFailureStopsTheRelease(t *testing.T) {
	t.Parallel()
	w := newKindWorld(t, &recordingEnding{}, pushMainStep(), tagKindStep())
	w.pass()
	candidate := runAt(t, w.s, releaseNodePath).Revision
	w.endCheck(w.stepID(1), 0, "origin's main contains "+candidate+"\n", "")
	w.pass()
	w.endCheck(w.stepID(2), 1, "", "fetched origin's tags\norigin refused the tag v0.1.1: it already exists\n")
	w.pass()
	w.pass()

	wantReason := "the step exited 1: origin refused the tag v0.1.1: it already exists"
	runs := releaseRunsOf(w.flowOf(runEpic).Releases, store.ReleasePartStep)
	if len(runs) != 2 || runs[1].Passed || runs[1].Reason != wantReason || runs[1].End != "failed" {
		t.Errorf("step runs = %+v, want the second failed with reason %q", runs, wantReason)
	}
	if phase := w.flowOf(runEpic).Epic.Phase; phase != store.PhaseStopped {
		t.Errorf("epic phase = %q, want stopped at the failed step", phase)
	}
}

// TestKindStepReason: a check that did not run, ran past its limit, or exited nonzero names why, and one that exited 0
// passes. A nonzero exit names the last line its stderr holds that is not blank.
func TestKindStepReason(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		check store.NodeCheck
		want  string
	}{
		{name: "passed", check: store.NodeCheck{Exit: 0, Stdout: "met"}, want: ""},
		{
			name:  "did not run",
			check: store.NodeCheck{NotRun: "lost at a restart"},
			want:  "the step did not run: lost at a restart",
		},
		{
			name:  "ran past its limit",
			check: store.NodeCheck{TimedOut: true, Limit: 600, Exit: -1},
			want:  "the step ran past its limit of 600s",
		},
		{
			name:  "exited nonzero",
			check: store.NodeCheck{Exit: 1, Stderr: "built\nunit orreryd runs an older build\n\n"},
			want:  "the step exited 1: unit orreryd runs an older build",
		},
		{name: "exited nonzero with no stderr", check: store.NodeCheck{Exit: 2}, want: "the step exited 2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := kindStepReason(tc.check); got != tc.want {
				t.Errorf("kindStepReason(%+v) = %q, want %q", tc.check, got, tc.want)
			}
		})
	}
}

// TestReleaseOfKindStepsEndsReleasedWithoutJudge: a release whose every step names a kind ends released once its last
// step passes, and no judge starts, since code checked each step's postcondition.
func TestReleaseOfKindStepsEndsReleasedWithoutJudge(t *testing.T) {
	t.Parallel()
	ending := &recordingEnding{}
	w := newKindWorld(t, ending, pushMainStep(), tagKindStep())
	w.pass()
	candidate := runAt(t, w.s, releaseNodePath).Revision
	w.endCheck(w.stepID(1), 0, "origin's main contains "+candidate+"\n", "")
	w.pass()
	w.endCheck(w.stepID(2), 0, "origin's tag v0.1.1 peels to "+candidate+"\n", "")
	w.pass()

	// The fake ending leaves the epic's phase as it is, so the pass ends it again at each of its steps; each end is the
	// same.
	want := endedEpic{epic: runEpic, phase: "released", body: kinds.EndingBody("released", nil, nil), at: runClock()}
	if len(ending.calls) == 0 {
		t.Fatalf("EndEpic calls = none, want %+v", want)
	}
	for _, call := range ending.calls {
		if !reflect.DeepEqual(call, want) {
			t.Errorf("EndEpic call = %+v, want %+v", call, want)
		}
	}
	if len(w.fakes.steps.calls) != 0 {
		t.Errorf("step starts = %+v, want no judge and no agent step", w.fakes.steps.calls)
	}
}

// TestReleaseOfMixedStepsJudgesTheAgentSteps: a release with a step that names no kind is still judged, once its last
// step passes, and the judge's prompt takes the steps that name a kind as met.
func TestReleaseOfMixedStepsJudgesTheAgentSteps(t *testing.T) {
	t.Parallel()
	w := newKindWorld(t, &recordingEnding{}, pushMainStep(), publishStep())
	w.pass()
	candidate := runAt(t, w.s, releaseNodePath).Revision
	w.endCheck(w.stepID(1), 0, "origin's main contains "+candidate+"\n", "")
	w.pass()
	w.finishStep(2, "published the tag")
	w.pass()

	calls := w.fakes.steps.calls
	if len(calls) != 2 || calls[1].ID != w.judgeID() {
		t.Fatalf("step starts = %+v, want the agent step and then the judge", calls)
	}
	prompt := calls[1].Prompt
	agent, code, found := strings.Cut(prompt, "These release steps ran as code")
	if !found {
		t.Fatalf("judge prompt = %q, want a section of the steps that ran as code", prompt)
	}
	if !strings.Contains(agent, "published the tag") || strings.Contains(agent, pushMainStep().Step) {
		t.Errorf("judge prompt's agent steps = %q, want the agent step's evidence and not the coded step", agent)
	}
	if !strings.Contains(code, pushMainStep().Step) || !strings.Contains(code, "origin's main contains "+candidate) {
		t.Errorf("judge prompt's coded steps = %q, want the push-main step with its evidence", code)
	}
	if !strings.Contains(prompt, releaseHeldRule) {
		t.Errorf("judge prompt = %q, want the rule that a commit containing the released one meets it", prompt)
	}
}

// TestReleaseKindFirstStepAfterOriginMovedPreparesAgain: a first step that names a kind and fails after origin's main
// moved past its candidate is no failure of the release. No failed run is recorded, and the candidate is marked stale,
// so the release prepares it again.
func TestReleaseKindFirstStepAfterOriginMovedPreparesAgain(t *testing.T) {
	t.Parallel()
	w := newReleaseWorld(t, &recordingEnding{})
	w.runner.orrery = testOrrery
	origin := mergeRemote(t, w.repo)
	nodeEpicBranch(t, w.repo, "epic.txt")
	w.createEpic(runEpic, store.PhaseClean, pushMainStep(), tagKindStep())
	w.pass()
	candidate := runAt(t, w.s, releaseNodePath).Revision
	mergeOriginCommit(t, origin, "origin-moved.txt")

	w.endCheck(w.stepID(1), 1, "", "origin's main has moved and is not in the candidate\n")
	w.pass()

	flow := w.flowOf(runEpic)
	if runs := releaseRunsOf(flow.Releases, store.ReleasePartStep); len(runs) != 0 {
		t.Errorf("step runs = %+v, want none: the failure is not a failure of the release", runs)
	}
	verify := releaseRunsOf(flow.Releases, store.ReleasePartVerify)
	if len(verify) != 1 || verify[0].Head != candidate || verify[0].Passed ||
		!strings.Contains(verify[0].Reason, "the step exited 1: origin's main has moved") {
		t.Errorf("verify runs = %+v, want candidate %s marked stale with the step's reason", verify, candidate)
	}
	if flow.Epic.Phase != store.PhaseReleasing {
		t.Errorf("epic phase = %q, want releasing: the release goes on", flow.Epic.Phase)
	}
}

// TestReleaseStepPromptNeverMovesATargetBack: an agent release step's prompt says that a target already holding a
// commit that contains the step's start meets the postcondition, so the step installs nothing older over it.
func TestReleaseStepPromptNeverMovesATargetBack(t *testing.T) {
	t.Parallel()
	prompt := releaseStepPrompt(publishStep(), "start123", releaseThen)
	for _, want := range []string{releaseHeldRule, "older one", "start123"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt = %q, want it to hold %q", prompt, want)
		}
	}
}
