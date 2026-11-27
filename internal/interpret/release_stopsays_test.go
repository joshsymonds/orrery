package interpret

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/releasestep"
	"github.com/joshsymonds/orrery/internal/store"
)

// The parts of the stop a release of stopSaysSteps gives, which the stop tests read: the header naming step 3 by number
// and target, the two lines naming the steps that landed with their targets and undos, and the failed check's exit.
const (
	stopSaysHeader    = "release step 3 (target ~/nix-config, branch main on its origin) failed:"
	stopSaysLandedOne = "- step 1 (target github.com/joshsymonds/orrery, branch main), undo: " +
		"Push a revert of the change to main."
	stopSaysLandedTwo = "- step 2 (target github.com/joshsymonds/orrery, the new tag), undo: " +
		"git push --delete origin <tag> and delete the local tag."
	stopSaysExit = "the step exited 1"
)

// The shape of the failed check's output: each stream holds stopSaysLines lines, and the stop keeps the newest
// stopSaysKept of them.
const (
	stopSaysLines  = 30
	stopSaysKept   = 20
	stopSaysStdout = "stdout"
	stopSaysStderr = "stderr"
)

// The label of the question the planner writes on the stop, and the choices it offers: replan, resume and give up.
const stopSaysLabel = "release-stop"

// stopSaysSteps returns the four release steps of the stop tests, with the step texts, targets and undos of orrery's
// own project file: steps 1 and 2 are a push and a tag, step 3 pins a flake, and step 4 updates the machine.
func stopSaysSteps() []store.EpicReleaseStep {
	return []store.EpicReleaseStep{
		{
			Step: "1 Fast-forward origin's main to the epic's release candidate and push it, so main holds the " +
				"change; do not wait for CI. A refused push stops the release, and the step never merges, rebases " +
				"or force-pushes.",
			Target: "github.com/joshsymonds/orrery, branch main", Undo: "Push a revert of the change to main.",
			Kind: releasestep.KindPushMain, Params: releasestep.Params{Branch: "main"},
		},
		{
			Step: "2 Push a signed annotated tag at the candidate, named the next patch version above the newest v* " +
				"tag, with a message naming the change. A newer v* tag on origin that contains the candidate meets " +
				"the step.",
			Target: "github.com/joshsymonds/orrery, the new tag",
			Undo:   "git push --delete origin <tag> and delete the local tag.",
			Kind:   releasestep.KindTag, Params: releasestep.Params{Prefix: "v"},
		},
		{
			Step: "3 Pin ~/nix-config's orrery input to the newest v* tag that contains the candidate, in flake.nix " +
				"and flake.lock, and push the pin to nix-config's origin main; never force-push.",
			Target: "~/nix-config, branch main on its origin",
			Undo:   "Commit flake.nix and flake.lock back to the previous tag.",
			Kind:   releasestep.KindPinFlake,
			Params: releasestep.Params{
				Branch: "main", Prefix: "v", Flake: "~/nix-config", Input: "orrery",
				URL: "github:joshsymonds/orrery/{tag}",
			},
		},
		{
			Step: "4 Bring ~/nix-config up to its origin main, run `update`, and check that orreryd and orrery-serve " +
				"run a build that contains the candidate.",
			Target: "vermissian user services and the orrery store",
			Undo: "Undo step 3, then run `update`; when the tag added a store migration, fix forward with a new " +
				"release instead, since an older orreryd refuses the migrated store.",
			Kind: releasestep.KindUpdate,
			Params: releasestep.Params{
				Branch: "main", Flake: "~/nix-config", Run: []string{"update"},
				Units: []string{"orreryd", "orrery-serve"},
			},
		},
	}
}

// stopSaysStream returns the lines of one stream of step 3's failed check, named name: stopSaysLines numbered lines.
func stopSaysStream(name string) []string {
	lines := make([]string, 0, stopSaysLines)
	for i := range stopSaysLines {
		lines = append(lines, fmt.Sprintf("%s line %02d", name, i+1))
	}
	return lines
}

// stopSaysKeptOf returns the newest stopSaysKept lines of lines, which the failed check keeps.
func stopSaysKeptOf(lines []string) []string {
	return lines[len(lines)-stopSaysKept:]
}

// stopSaysText returns the whole stop a release gives when step 3's check exits 1 with the streams stopSaysStream
// gives: the header naming step 3 by number and target, then the steps that landed at this attempt with their
// targets and undos, then the exit and the newest kept lines of each stream. The stop is at most store.TurnReasonMax
// bytes, so the park keeps all of it.
func stopSaysText() string {
	head := []string{stopSaysHeader, "landed:", stopSaysLandedOne, stopSaysLandedTwo, stopSaysExit}
	lines := slices.Concat(
		head,
		[]string{stopSaysStdout + ":"},
		stopSaysKeptOf(stopSaysStream(stopSaysStdout)),
		[]string{stopSaysStderr + ":"},
		stopSaysKeptOf(stopSaysStream(stopSaysStderr)),
	)
	return strings.Join(lines, "\n")
}

// stopSaysStop returns the world of a release of stopSaysSteps stopped at step 3: steps 1 and 2 passed at the first
// attempt, and step 3's check exited 1 with the streams stopSaysStream gives. The epic is parked and stopped.
func stopSaysStop(t *testing.T) replanWorld {
	t.Helper()
	w := newStopWorld(t, anyStopSetup{steps: stopSaysSteps()})
	w.pass()
	w.endCheck(w.stepID(1), 0, "origin's main contains the candidate\n", "")
	w.pass()
	w.endCheck(w.stepID(2), 0, "origin's tag v0.1.1 peels to the candidate\n", "")
	w.pass()
	w.endCheck(w.stepID(3), 1, strings.Join(stopSaysStream(stopSaysStdout), "\n")+"\n",
		strings.Join(stopSaysStream(stopSaysStderr), "\n")+"\n")
	w.pass()
	if phase := w.flowOf(runEpic).Epic.Phase; phase != store.PhaseStopped {
		t.Fatalf("epic phase = %q, want stopped at step 3", phase)
	}
	return w
}

// stopSaysQuestion is the question the planner writes on the stop. Its three choices are replan, which sends the
// release back to planning, resume, which resumes it at its next attempt, and give up, which closes the epic as
// abandoned.
func stopSaysQuestion() store.EpicQuestionInput {
	return store.EpicQuestionInput{
		Label: stopSaysLabel,
		Text:  "The release stopped at step 3. What should happen to it?",
		Choices: []store.EpicChoice{
			{ID: "replan", Does: "send the release back to planning, so the planner plans the fix it needs"},
			{ID: "resume", Does: "resume the release at its next attempt, running step 3 again"},
			{ID: "give-up", Does: "close the epic as abandoned and leave the release stopped"},
		},
		Recommended: "replan",
		Why:         "the check may be fixed, so a replan reads the stop before anything runs again",
		Confidence:  "medium",
	}
}

// stopSaysAnswered has the planner write stopSaysQuestion on the stop of w, the owner answer choice to it, and the
// planner's next turn return action. The engine carries action out. It fails the test when the question is not open
// after the stop's turn, or when the answer's trigger does not wake the next turn.
func stopSaysAnswered(t *testing.T, w replanWorld, choice string, action store.EpicActionInput) {
	t.Helper()
	ctx := t.Context()
	stop, _ := w.claimReplanTurn()
	if err := w.s.CompleteTurn(ctx, runEpic, stop.Seq, store.EpicPlan{
		Decisions: []string{}, Outline: "the release stopped at step 3", Outcome: "wait",
		Questions: []store.EpicQuestionInput{stopSaysQuestion()},
	}, runClock()); err != nil {
		t.Fatalf("CompleteTurn %d: %v", stop.Seq, err)
	}
	open, err := w.s.OpenQuestions(ctx, runEpic)
	if err != nil {
		t.Fatalf("OpenQuestions: %v", err)
	}
	if len(open) != 1 || open[0].Label != stopSaysLabel {
		t.Fatalf("open questions = %+v, want the planner's question alone", open)
	}
	decision := "owner-" + choice
	if _, err = w.s.AnswerQuestion(
		ctx, runEpic, decision, "", open[0].Seq, choice, store.Maker{By: store.MakerOwner}, runClock(),
	); err != nil {
		t.Fatalf("AnswerQuestion %s: %v", choice, err)
	}
	next, _ := w.claimReplanTurn()
	if causes := triggerCauses(t, w.s, "answer:"); len(causes) != 1 {
		t.Fatalf("answer triggers = %q, want the one the answer stored", causes)
	}
	if err = w.s.CompleteTurn(ctx, runEpic, next.Seq, store.EpicPlan{
		Decisions: []string{}, Outline: "the owner answered " + choice, Outcome: "work",
		Actions: []store.EpicActionInput{action},
	}, runClock()); err != nil {
		t.Fatalf("CompleteTurn %d: %v", next.Seq, err)
	}
	w.carryTurn(next)
}

// TestStopSaysParkNamesLandedAndTheExit: a release stopped at step 3 parks with the whole stop as its reason and as its
// tell. The stop names steps 1 and 2, which passed at this attempt, by number with their targets and undos, whole. It
// names step 3's exit and the newest twenty lines each of its stdout and its stderr, and not the older lines, which the
// check does not keep. The stop fits store.TurnReasonMax, so the park cuts none of it. The release run holds the same
// stop as its output.
func TestStopSaysParkNamesLandedAndTheExit(t *testing.T) {
	t.Parallel()
	w := stopSaysStop(t)
	want := stopSaysText()
	if len(want) > store.TurnReasonMax {
		t.Fatalf("stop is %d bytes, over the %d the park keeps, so it would be cut", len(want), store.TurnReasonMax)
	}
	flow := w.flowOf(runEpic)
	if flow.Epic.ParkClass != store.ParkClassReleaseStep || flow.Epic.Parked != want {
		t.Errorf("epic class %q parked %q, want a release step park with the whole stop %q",
			flow.Epic.ParkClass, flow.Epic.Parked, want)
	}
	if tells := nodeTells(t, w.path); !slices.Contains(tells, want) {
		t.Errorf("tells = %q, want the whole stop as a tell", tells)
	}
	if run := runAt(t, w.s, releaseNodePath); run.State != store.NodeParked || run.Output != want {
		t.Errorf("release run = %+v, want parked with the whole stop as its output", run)
	}
	steps := releaseRunsOf(flow.Releases, store.ReleasePartStep)
	if len(steps) != 3 || !steps[0].Passed || !steps[1].Passed || steps[2].Passed || steps[2].Attempt != 0 {
		t.Errorf("step runs = %+v, want steps 1 and 2 passed and step 3 failed at attempt 0", steps)
	}
}

// TestStopSaysParkOpensNoQuestion: the stop of a release at step 3 opens no park question. The planner is woken for the
// stop, so no canned retry question stands in for its decision.
func TestStopSaysParkOpensNoQuestion(t *testing.T) {
	t.Parallel()
	w := stopSaysStop(t)
	open, err := w.s.OpenQuestions(t.Context(), runEpic)
	if err != nil {
		t.Fatalf("OpenQuestions: %v", err)
	}
	if len(open) != 0 {
		t.Errorf("open questions = %+v, want none: the stop parks with no park question", open)
	}
}

// TestStopSaysPlannerPromptHoldsStopText: the prompt the planner gets for its turn on the stop, built by the planner's
// prompt builder from the epic's rows, holds the whole stop text, as the trigger that wakes the turn does.
func TestStopSaysPlannerPromptHoldsStopText(t *testing.T) {
	t.Parallel()
	w := stopSaysStop(t)
	want := stopSaysText()
	_, prompt := w.claimReplanTurn()
	if !strings.Contains(prompt, want) {
		t.Errorf("planner prompt holds no whole stop %q:\n%s", want, prompt)
	}
	if causes := triggerCauses(t, w.s, "park:"); !slices.Contains(causes, want) {
		t.Errorf("park trigger causes = %q, want the whole stop %q", causes, want)
	}
}

// TestStopSaysReplanChoiceReplans: the owner answers the planner's question with replan, the planner's next turn
// returns retry_epic, and the engine carries it out: the epic goes back to planning with a replan of the stop, as a
// retry does.
func TestStopSaysReplanChoiceReplans(t *testing.T) {
	t.Parallel()
	w := stopSaysStop(t)
	stopSaysAnswered(t, w, "replan", store.EpicActionInput{
		Kind: store.ActionRetryEpic, Reason: "the owner chose to replan the release",
	})
	w.passQuiet()
	w.requireStopReplanned(stopSaysText(), "", 0)
	w.requireNotRefused()
}

// TestStopSaysResumeChoiceRerunsStepThree: the owner answers the planner's question with resume, the planner's next
// turn returns resume_release, and the engine carries it out: the release goes on at its next attempt, and step 3 runs
// again.
func TestStopSaysResumeChoiceRerunsStepThree(t *testing.T) {
	t.Parallel()
	w := stopSaysStop(t)
	checks := len(w.fakes.checks.calls)
	stopSaysAnswered(t, w, "resume", store.EpicActionInput{
		Kind: store.ActionResumeRelease, Reason: "the owner chose to resume the release",
	})
	w.passQuiet()
	flow := w.flowOf(runEpic)
	if flow.Epic.Phase != store.PhaseReleasing || flow.Epic.Parked != "" {
		t.Fatalf("epic phase %q parked %q, want releasing and unparked", flow.Epic.Phase, flow.Epic.Parked)
	}
	if run := runAt(t, w.s, releaseNodePath); run.State != store.NodeRunning || run.Attempt != 1 {
		t.Errorf("release run = %+v, want running at attempt 1", run)
	}
	if started := w.fakes.checks.calls[checks:]; len(started) != 1 || started[0].ID != w.stepID(3) {
		t.Errorf("checks started = %+v, want step 3 alone started again at attempt 1", started)
	}
	w.requireNotRefused()
}

// TestStopSaysGiveUpChoiceClosesAbandoned: the owner answers the planner's question with give up, the planner's next
// turn returns close_epic as abandoned, and the engine carries it out: the epic is closed as abandoned, and no step
// runs.
func TestStopSaysGiveUpChoiceClosesAbandoned(t *testing.T) {
	t.Parallel()
	w := stopSaysStop(t)
	checks := len(w.fakes.checks.calls)
	stopSaysAnswered(t, w, "give-up", store.EpicActionInput{
		Kind: store.ActionCloseEpic, Close: "abandoned", Reason: "the owner chose to give up the release",
	})
	w.passQuiet()
	if phase := w.flowOf(runEpic).Epic.Phase; phase != store.PhaseClosedAbandoned {
		t.Errorf("epic phase = %q, want %q", phase, store.PhaseClosedAbandoned)
	}
	if n := len(w.fakes.checks.calls); n != checks {
		t.Errorf("checks started = %+v, want none after the epic closed", w.fakes.checks.calls[checks:])
	}
}
