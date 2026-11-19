package kinds

import (
	"fmt"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/reviewers"
	"github.com/joshsymonds/orrery/internal/store"
)

var _ Kind = Release{}

// releaseEpicID is the id of the epic the release tests decide, which holds the repository's release slot in the
// tests that say so, and releaseOtherEpicID the epic that holds it in the tests where another epic does.
const (
	releaseEpicID      = "epic-1"
	releaseOtherEpicID = "epic-2"
)

// releaseFrozen returns the review of a clean release epic: it froze and holds no verdict, ledger or round, so it names
// no gap.
func releaseFrozen() store.Review {
	return store.Review{Frozen: testRev}
}

// releaseGapped returns the review of a release epic whose security reviewer gave a gap verdict.
func releaseGapped() store.Review {
	return store.Review{
		Frozen: testRev,
		Verdicts: []store.ReviewVerdict{{
			Position: 1, Reviewer: "security", Verdict: "gap", Reason: "security gave no verdict twice: done: boom",
		}},
	}
}

// attemptWorkID returns the id of the work labeled label of the test run at attempt.
func attemptWorkID(attempt int64, label string) string {
	return store.NodeWorkID(testRunID, attempt, label)
}

// releaseNode returns a release node run of the test run at attempt, with revision and result.
func releaseNode(attempt int64, revision, result string) Node {
	return Node{Run: store.NodeRun{
		ID: testRunID, Attempt: attempt, State: store.NodeRunning, Revision: revision, Result: result,
	}}
}

// releaseStep returns a release step with text and undo.
func releaseStep(text, undo string) store.EpicReleaseStep {
	return store.EpicReleaseStep{Step: text, Target: "main", Undo: undo}
}

// releasingEpic returns an epic releasing with steps.
func releasingEpic(steps ...store.EpicReleaseStep) store.EpicRow {
	return store.EpicRow{ID: releaseEpicID, Phase: store.PhaseReleasing, ReleaseSteps: steps}
}

// candidateRun returns a candidate run of attempt that passed, at head.
func candidateRun(attempt int64, head string) store.ReleaseRun {
	return store.ReleaseRun{Part: store.ReleasePartCandidate, Attempt: attempt, Head: head, Passed: true}
}

// judgeRun returns a judge run of the first attempt that passed or not, for reason.
func judgeRun(passed bool, reason string) store.ReleaseRun {
	return store.ReleaseRun{Part: store.ReleasePartJudge, Attempt: 1, Passed: passed, Reason: reason}
}

// doneCheck returns the ended check of the done gate at position i of attempt, passed or not.
func doneCheck(attempt int64, i int, passed bool) store.NodeCheck {
	check := store.NodeCheck{
		ID: attemptWorkID(attempt, fmt.Sprintf("done %d", i)), State: store.NodeEnded,
	}
	if !passed {
		check.Exit = 1
	}
	return check
}

// TestReleasePhases checks the decisions that come before any release run: a released or done epic frees the slot or
// ends, a clean epic ends done with no steps or takes the slot, and any other phase but releasing gives none.
func TestReleasePhases(t *testing.T) {
	t.Parallel()
	steps := []store.EpicReleaseStep{releaseStep("tag the release", "none")}
	cases := []decideCase{
		{
			"released epic holding the slot frees it",
			releaseNode(1, "", ""),
			Rows{Epic: store.EpicRow{ID: releaseEpicID, Phase: phaseReleased}, Slot: releaseEpicID},
			Action{Kind: ActFree},
		},
		{
			"done epic holding the slot frees it",
			releaseNode(1, "", ""),
			Rows{Epic: store.EpicRow{ID: releaseEpicID, Phase: phaseDone}, Slot: releaseEpicID},
			Action{Kind: ActFree},
		},
		{
			"released epic whose slot is free ends passed",
			releaseNode(1, "", ""),
			Rows{Epic: store.EpicRow{ID: releaseEpicID, Phase: phaseReleased}},
			Action{Kind: ActEnd, Outcome: outcomePassed},
		},
		{
			"done epic whose slot another epic holds ends passed",
			releaseNode(1, "", ""),
			Rows{Epic: store.EpicRow{ID: releaseEpicID, Phase: phaseDone}, Slot: releaseOtherEpicID},
			Action{Kind: ActEnd, Outcome: outcomePassed},
		},
		{
			"clean epic with no release steps ends done with no gaps",
			releaseNode(1, "", ""),
			Rows{Epic: store.EpicRow{ID: releaseEpicID, Phase: store.PhaseClean}, Review: releaseFrozen()},
			Action{Kind: ActEnding, Outcome: outcomeDone, Output: EndingBody(phaseDone, nil, nil)},
		},
		{
			"clean epic with no release steps ends done whoever holds the slot",
			releaseNode(1, "", ""),
			Rows{
				Epic:   store.EpicRow{ID: releaseEpicID, Phase: store.PhaseClean},
				Slot:   releaseOtherEpicID,
				Review: releaseFrozen(),
			},
			Action{Kind: ActEnding, Outcome: outcomeDone, Output: EndingBody(phaseDone, nil, nil)},
		},
		{
			"clean epic with no release steps ends done naming the gap its review holds",
			releaseNode(1, "", ""),
			Rows{Epic: store.EpicRow{ID: releaseEpicID, Phase: store.PhaseClean}, Review: releaseGapped()},
			Action{
				Kind:    ActEnding,
				Outcome: outcomeDone,
				Output: EndingBody(
					phaseDone,
					[]string{"reviewer security: security gave no verdict twice: done: boom"},
					nil,
				),
			},
		},
		{
			"clean epic whose slot another epic holds gives none",
			releaseNode(1, "", ""),
			Rows{
				Epic: store.EpicRow{ID: releaseEpicID, Phase: store.PhaseClean, ReleaseSteps: steps},
				Slot: releaseOtherEpicID,
			},
			Action{Kind: ActNone},
		},
		{
			"clean epic with the slot free takes it",
			releaseNode(1, "", ""),
			Rows{Epic: store.EpicRow{ID: releaseEpicID, Phase: store.PhaseClean, ReleaseSteps: steps}},
			Action{Kind: ActTake},
		},
		{
			"clean epic that holds the slot takes it",
			releaseNode(1, "", ""),
			Rows{
				Epic: store.EpicRow{ID: releaseEpicID, Phase: store.PhaseClean, ReleaseSteps: steps},
				Slot: releaseEpicID,
			},
			Action{Kind: ActTake},
		},
		{
			"stopped epic gives none",
			releaseNode(1, "", ""),
			Rows{
				Epic: store.EpicRow{ID: releaseEpicID, Phase: store.PhaseStopped, ReleaseSteps: steps},
				Slot: releaseEpicID,
			},
			Action{Kind: ActNone},
		},
		{
			"accepted epic gives none",
			releaseNode(1, "", ""),
			Rows{Epic: store.EpicRow{ID: releaseEpicID, Phase: "accepted", ReleaseSteps: steps}},
			Action{Kind: ActNone},
		},
	}
	checkDecisions(t, Release{}, cases)
}

// TestReleaseIdlePhases checks the phases a release run waits in: Idle holds for the phases Decide gives no action in
// whatever the rows, ended_with_gaps and met_at_base among them, and for no phase Decide acts in. Each idle phase is
// decided over rows that would act in every other phase: the slot held, release steps, a replan of the run's attempt and
// an ended replan plan.
func TestReleaseIdlePhases(t *testing.T) {
	t.Parallel()
	steps := []store.EpicReleaseStep{releaseStep("tag the release", "none")}
	run := releaseNode(1, "", "")
	busy := func(phase string) Rows {
		return Rows{
			Epic: store.EpicRow{ID: releaseEpicID, Phase: phase, ReleaseSteps: steps},
			Slot: releaseEpicID,
			Releases: []store.ReleaseRun{
				{Part: store.ReleasePartReplan, Attempt: run.Run.Attempt, Reason: "the candidate needs a change"},
			},
			Runs: []store.NodeRun{{
				Path: ReplanPrefix(run.Run.Path, run.Run.Attempt) + "fix", Kind: kindPlan, State: store.NodeEnded,
			}},
		}
	}
	for _, phase := range []string{phaseEndedWithGaps, phaseMetAtBase, "accepted", "reviewing", ""} {
		if !(Release{}).Idle(phase) {
			t.Errorf("Idle(%q) = false, want a release run to wait in it", phase)
		}
		if got := (Release{}).Decide(run, busy(phase)); got.Kind != ActNone {
			t.Errorf("Decide in idle phase %q = %+v, want none", phase, got)
		}
	}
	for _, phase := range []string{
		phaseReleased, phaseDone, store.PhaseClean, store.PhaseReleasing, store.PhaseStopped, phaseCorrecting,
	} {
		if (Release{}).Idle(phase) {
			t.Errorf("Idle(%q) = true, want the phase decided", phase)
		}
		if got := (Release{}).Decide(run, busy(phase)); got.Kind == ActNone {
			t.Errorf("Decide in phase %q over rows that act = none, want an action", phase)
		}
	}
}

// TestReleaseCandidate checks how the candidate is decided: a passed candidate of any attempt is the candidate, unless
// this attempt found origin's main not in it, a failed one of this attempt stops, and otherwise the candidate is
// prepared, recorded, or held by its done gates.
func TestReleaseCandidate(t *testing.T) {
	t.Parallel()
	steps := []store.EpicReleaseStep{releaseStep("tag the release", "rollback")}
	gated := func(gates ...string) store.EpicRow {
		return store.EpicRow{ID: releaseEpicID, Phase: store.PhaseReleasing, DoneGates: gates}
	}
	cases := []decideCase{
		{
			// The interpreter checks that origin's main is in a candidate of an earlier attempt before its first step starts
			// from it, and marks the candidate when it is not. So the reuse holds while no mark of this attempt names it.
			"passed candidate of an earlier attempt that origin's main is in starts the first step from its head",
			releaseNode(2, "", ""),
			Rows{
				Epic:     releasingEpic(steps...),
				Slot:     releaseEpicID,
				Releases: []store.ReleaseRun{candidateRun(1, "cand-1")},
			},
			Action{Kind: ActStep, IDs: []string{attemptWorkID(2, "release 1")}, Items: []string{"release 1", "cand-1"}},
		},
		{
			"failed candidate of this attempt stops with its reason",
			releaseNode(1, "", ""),
			Rows{
				Epic: releasingEpic(steps...),
				Releases: []store.ReleaseRun{{
					Part: store.ReleasePartCandidate, Attempt: 1, Reason: "the tree is dirty",
				}},
			},
			Action{Kind: ActStop, Output: "the tree is dirty"},
		},
		{
			"failed candidate of an earlier attempt is prepared again",
			releaseNode(2, "", ""),
			Rows{
				Epic: releasingEpic(steps...),
				Releases: []store.ReleaseRun{{
					Part: store.ReleasePartCandidate, Attempt: 1, Reason: "the tree is dirty",
				}},
			},
			Action{Kind: ActPrepare},
		},
		{
			"paused epic without a revision gives none",
			releaseNode(1, "", ""),
			Rows{Epic: store.EpicRow{ID: releaseEpicID, Phase: store.PhaseReleasing, Paused: true}},
			Action{Kind: ActNone},
		},
		{
			"paused epic with a result error gives none",
			releaseNode(1, "", `{"error":"fetch failed"}`),
			Rows{Epic: store.EpicRow{ID: releaseEpicID, Phase: store.PhaseReleasing, Paused: true}},
			Action{Kind: ActNone},
		},
		{
			"result error without a revision stops with the error",
			releaseNode(1, "", `{"error":"fetch failed"}`),
			Rows{Epic: releasingEpic(steps...)},
			Action{Kind: ActStop, Output: "fetch failed"},
		},
		{
			"no revision and no result prepares",
			releaseNode(1, "", ""),
			Rows{Epic: releasingEpic(steps...)},
			Action{Kind: ActPrepare},
		},
		{
			"result without an error prepares",
			releaseNode(1, "", `{"behind":false}`),
			Rows{Epic: releasingEpic(steps...)},
			Action{Kind: ActPrepare},
		},
		{
			"revision not behind records the candidate",
			releaseNode(1, testRev, `{"behind":false}`),
			Rows{Epic: releasingEpic(steps...)},
			Action{Kind: ActRecord, Items: []string{"candidate", testRev}},
		},
		{
			"behind with no done gate records the candidate",
			releaseNode(1, testRev, `{"behind":true}`),
			Rows{Epic: gated()},
			Action{Kind: ActRecord, Items: []string{"candidate", testRev}},
		},
		{
			"behind with a done gate that has no check starts it",
			releaseNode(1, testRev, `{"behind":true}`),
			Rows{Epic: gated("lint")},
			Action{Kind: ActStart, IDs: []string{attemptWorkID(1, "done 1")}, Items: []string{"lint"}},
		},
		{
			"behind starts the first done gate without a check, not the later ones",
			releaseNode(1, testRev, `{"behind":true}`),
			Rows{Epic: gated("lint", "test"), Checks: []store.NodeCheck{doneCheck(1, 1, true)}},
			Action{Kind: ActStart, IDs: []string{attemptWorkID(1, "done 2")}, Items: []string{"test"}},
		},
		{
			"behind with a running done gate gives none",
			releaseNode(1, testRev, `{"behind":true}`),
			Rows{
				Epic: gated("lint"),
				Checks: []store.NodeCheck{{
					ID: attemptWorkID(1, "done 1"), State: store.NodeRunning,
				}},
			},
			Action{Kind: ActNone},
		},
		{
			"behind with a running done gate before a failed one gives none",
			releaseNode(1, testRev, `{"behind":true}`),
			Rows{
				Epic: gated("lint", "test"),
				Checks: []store.NodeCheck{
					{ID: attemptWorkID(1, "done 1"), State: store.NodeRunning},
					doneCheck(1, 2, false),
				},
			},
			Action{Kind: ActNone},
		},
		{
			"behind with a failed done gate stops naming the gate and the revision",
			releaseNode(1, testRev, `{"behind":true}`),
			Rows{Epic: gated("lint"), Checks: []store.NodeCheck{doneCheck(1, 1, false)}},
			Action{Kind: ActStop, Output: "done gate lint did not pass at " + testRev},
		},
		{
			"behind with a timed out done gate stops naming the gate and the revision",
			releaseNode(1, testRev, `{"behind":true}`),
			Rows{
				Epic: gated("lint"),
				Checks: []store.NodeCheck{{
					ID: attemptWorkID(1, "done 1"), State: store.NodeEnded, TimedOut: true,
				}},
			},
			Action{Kind: ActStop, Output: "done gate lint did not pass at " + testRev},
		},
		{
			"behind with a done gate lost at a restart twice stops with the park's reason",
			releaseNode(1, testRev, `{"behind":true}`),
			Rows{
				Epic: gated("lint"),
				Checks: []store.NodeCheck{
					{ID: attemptWorkID(1, "done 1"), State: store.NodeEnded, NotRun: store.LostAtRestart},
					{ID: attemptWorkID(1, "done 1 rerun"), State: store.NodeEnded, NotRun: store.LostAtRestart},
				},
			},
			Action{Kind: ActStop, Output: "integrate " + releaseNode(1, testRev, "").Run.Path +
				": Done gate lint lost at a restart twice"},
		},
		{
			"behind with a done check of another attempt starts the gate of this one",
			releaseNode(1, testRev, `{"behind":true}`),
			Rows{Epic: gated("lint"), Checks: []store.NodeCheck{doneCheck(2, 1, true)}},
			Action{Kind: ActStart, IDs: []string{attemptWorkID(1, "done 1")}, Items: []string{"lint"}},
		},
		{
			"behind with every done gate passed records the candidate",
			releaseNode(1, testRev, `{"behind":true}`),
			Rows{
				Epic:   gated("lint", "test"),
				Checks: []store.NodeCheck{doneCheck(1, 1, true), doneCheck(1, 2, true)},
			},
			Action{Kind: ActRecord, Items: []string{"candidate", testRev}},
		},
	}
	checkDecisions(t, Release{}, cases)
}

// TestReleaseMalformedResultStops checks that a result which is not a JSON object stops the run whether or not the
// run has its revision, since the run cannot tell what the prepare decided.
func TestReleaseMalformedResultStops(t *testing.T) {
	t.Parallel()
	for _, node := range []Node{releaseNode(1, "", "not json"), releaseNode(1, testRev, "not json")} {
		got := Release{}.Decide(node, Rows{Epic: releasingEpic()})
		malformed := strings.HasPrefix(got.Output, "release result is not a JSON object")
		if got.Kind != ActStop || !malformed {
			t.Errorf("Decide() = %+v, want a stop on a malformed result", got)
		}
	}
}

// TestReleaseSteps checks the walk over the release steps in order: a passed step of any attempt moves on, a failed
// step of this attempt stops, and a step with no row starts unless its undo is none and it failed before.
func TestReleaseSteps(t *testing.T) {
	t.Parallel()
	steps := []store.EpicReleaseStep{
		releaseStep("build the image", "rollback"),
		releaseStep("push the tag", "rollback"),
	}
	undoNone := []store.EpicReleaseStep{releaseStep("build the image", "none")}
	candidate := candidateRun(1, "cand-1")
	failedFirst := store.ReleaseRun{
		Part: store.ReleasePartStep, Position: 1, Attempt: 1, Reason: "push rejected",
	}
	undoNoneStop := "release step 1 (build the image) failed at an earlier attempt with undo none, so it runs again only when named"
	cases := []decideCase{
		{
			"first step with no row starts from the candidate head",
			releaseNode(1, "", ""),
			Rows{Epic: releasingEpic(steps...), Slot: releaseEpicID, Releases: []store.ReleaseRun{candidate}},
			Action{Kind: ActStep, IDs: []string{attemptWorkID(1, "release 1")}, Items: []string{"release 1", "cand-1"}},
		},
		{
			"passed step moves on, and the next step starts from its head",
			releaseNode(1, "", ""),
			Rows{
				Epic: releasingEpic(steps...),
				Slot: releaseEpicID,
				Releases: []store.ReleaseRun{
					candidate,
					store.ReleaseRun{
						Part:     store.ReleasePartStep,
						Position: 1,
						Attempt:  1,
						Head:     "step-1",
						Passed:   true,
					},
				},
			},
			Action{Kind: ActStep, IDs: []string{attemptWorkID(1, "release 2")}, Items: []string{"release 2", "step-1"}},
		},
		{
			"passed step of an earlier attempt moves on, and its head starts the next step",
			releaseNode(2, "", ""),
			Rows{
				Epic: releasingEpic(steps...),
				Slot: releaseEpicID,
				Releases: []store.ReleaseRun{
					candidate,
					store.ReleaseRun{
						Part:     store.ReleasePartStep,
						Position: 1,
						Attempt:  1,
						Head:     "step-1",
						Passed:   true,
					},
				},
			},
			Action{Kind: ActStep, IDs: []string{attemptWorkID(2, "release 2")}, Items: []string{"release 2", "step-1"}},
		},
		{
			"failed step of this attempt stops naming the step and its reason",
			releaseNode(1, "", ""),
			Rows{
				Epic:     releasingEpic(steps...),
				Slot:     releaseEpicID,
				Releases: []store.ReleaseRun{candidate, failedFirst},
			},
			Action{Kind: ActStop, Output: "release step 1 (build the image) failed: push rejected"},
		},
		{
			"failed step after a passed one stops naming the later step",
			releaseNode(1, "", ""),
			Rows{
				Epic: releasingEpic(steps...),
				Slot: releaseEpicID,
				Releases: []store.ReleaseRun{
					candidate,
					store.ReleaseRun{
						Part:     store.ReleasePartStep,
						Position: 1,
						Attempt:  1,
						Head:     "step-1",
						Passed:   true,
					},
					{Part: store.ReleasePartStep, Position: 2, Attempt: 1, Reason: "tag exists"},
				},
			},
			Action{Kind: ActStop, Output: "release step 2 (push the tag) failed: tag exists"},
		},
		{
			"running step gives none",
			releaseNode(1, "", ""),
			Rows{
				Epic:     releasingEpic(steps...),
				Slot:     releaseEpicID,
				Releases: []store.ReleaseRun{candidate},
				Steps:    []store.StepRow{{ID: attemptWorkID(1, "release 1")}},
			},
			Action{Kind: ActNone},
		},
		{
			"ended step is recorded with its start",
			releaseNode(1, "", ""),
			Rows{
				Epic:     releasingEpic(steps...),
				Slot:     releaseEpicID,
				Releases: []store.ReleaseRun{candidate},
				Steps:    []store.StepRow{{ID: attemptWorkID(1, "release 1"), End: "done"}},
			},
			Action{
				Kind:  ActRecord,
				IDs:   []string{attemptWorkID(1, "release 1")},
				Items: []string{"release 1", "cand-1"},
			},
		},
		{
			"step with no row stops when another epic holds the slot",
			releaseNode(1, "", ""),
			Rows{Epic: releasingEpic(steps...), Slot: releaseOtherEpicID, Releases: []store.ReleaseRun{candidate}},
			Action{
				Kind:   ActStop,
				Output: "release step 1 (build the image) did not start: this epic does not hold the release slot",
			},
		},
		{
			"step with no row stops when the slot is free",
			releaseNode(1, "", ""),
			Rows{Epic: releasingEpic(steps...), Releases: []store.ReleaseRun{candidate}},
			Action{
				Kind:   ActStop,
				Output: "release step 1 (build the image) did not start: this epic does not hold the release slot",
			},
		},
		{
			"step with no row gives none while paused",
			releaseNode(1, "", ""),
			Rows{
				Epic:     releasingEpicPaused(releasingEpic(steps...)),
				Slot:     releaseEpicID,
				Releases: []store.ReleaseRun{candidate},
			},
			Action{Kind: ActNone},
		},
		{
			"undo none after an earlier failure stops",
			releaseNode(2, "", ""),
			Rows{
				Epic:     releasingEpic(undoNone...),
				Slot:     releaseEpicID,
				Releases: []store.ReleaseRun{candidate, failedFirst},
			},
			Action{Kind: ActStop, Output: undoNoneStop},
		},
		{
			"undo none in any case and spacing counts as none",
			releaseNode(2, "", ""),
			Rows{
				Epic:     releasingEpic(releaseStep("build the image", "  NONE ")),
				Slot:     releaseEpicID,
				Releases: []store.ReleaseRun{candidate, failedFirst},
			},
			Action{Kind: ActStop, Output: undoNoneStop},
		},
		{
			"undo none after an earlier failure runs again when a named run exists at this attempt",
			releaseNode(2, "", ""),
			Rows{
				Epic: releasingEpic(undoNone...),
				Slot: releaseEpicID,
				Releases: []store.ReleaseRun{
					candidate,
					failedFirst,
					{Part: store.ReleasePartNamed, Position: 1, Attempt: 2},
				},
			},
			Action{Kind: ActStep, IDs: []string{attemptWorkID(2, "release 1")}, Items: []string{"release 1", "cand-1"}},
		},
		{
			"undo none after an earlier failure still stops when the named run is at an earlier attempt",
			releaseNode(2, "", ""),
			Rows{
				Epic: releasingEpic(undoNone...),
				Slot: releaseEpicID,
				Releases: []store.ReleaseRun{
					candidate,
					failedFirst,
					{Part: store.ReleasePartNamed, Position: 1, Attempt: 1},
				},
			},
			Action{Kind: ActStop, Output: undoNoneStop},
		},
		{
			"undo none with no earlier failure starts",
			releaseNode(1, "", ""),
			Rows{Epic: releasingEpic(undoNone...), Slot: releaseEpicID, Releases: []store.ReleaseRun{candidate}},
			Action{Kind: ActStep, IDs: []string{attemptWorkID(1, "release 1")}, Items: []string{"release 1", "cand-1"}},
		},
		{
			"undo rollback after an earlier failure starts again",
			releaseNode(2, "", ""),
			Rows{
				Epic:     releasingEpic(steps...),
				Slot:     releaseEpicID,
				Releases: []store.ReleaseRun{candidate, failedFirst},
			},
			Action{Kind: ActStep, IDs: []string{attemptWorkID(2, "release 1")}, Items: []string{"release 1", "cand-1"}},
		},
		{
			"step row of another attempt does not count",
			releaseNode(1, "", ""),
			Rows{
				Epic:     releasingEpic(steps...),
				Slot:     releaseEpicID,
				Releases: []store.ReleaseRun{candidate},
				Steps:    []store.StepRow{{ID: attemptWorkID(2, "release 1"), End: "done"}},
			},
			Action{Kind: ActStep, IDs: []string{attemptWorkID(1, "release 1")}, Items: []string{"release 1", "cand-1"}},
		},
	}
	checkDecisions(t, Release{}, cases)
}

// TestReleaseJudge checks the judge after the last step: a passed judge of this attempt ends the epic released, a
// failed one stops, and otherwise the judge starts, waits or is recorded like a step.
func TestReleaseJudge(t *testing.T) {
	t.Parallel()
	epic := releasingEpic(releaseStep("build the image", "rollback"))
	candidate := candidateRun(1, "cand-1")
	passedStep := store.ReleaseRun{Part: store.ReleasePartStep, Position: 1, Attempt: 1, Head: "step-1", Passed: true}
	cases := []decideCase{
		{
			"judge after the last step starts with the candidate",
			releaseNode(1, "", ""),
			Rows{Epic: epic, Slot: releaseEpicID, Releases: []store.ReleaseRun{candidate, passedStep}},
			Action{Kind: ActJudge, IDs: []string{attemptWorkID(1, "judge")}, Items: []string{"judge", "cand-1"}},
		},
		{
			"release with no steps judges the candidate",
			releaseNode(1, "", ""),
			Rows{Epic: releasingEpic(), Slot: releaseEpicID, Releases: []store.ReleaseRun{candidate}},
			Action{Kind: ActJudge, IDs: []string{attemptWorkID(1, "judge")}, Items: []string{"judge", "cand-1"}},
		},
		{
			"passed judge of this attempt ends released with no gaps",
			releaseNode(1, "", ""),
			Rows{
				Epic:     epic,
				Slot:     releaseEpicID,
				Releases: []store.ReleaseRun{candidate, passedStep, judgeRun(true, "")},
				Review:   releaseFrozen(),
			},
			Action{Kind: ActEnding, Outcome: outcomeReleased, Output: EndingBody(phaseReleased, nil, nil)},
		},
		{
			"passed judge of this attempt ends released naming the gap its review holds",
			releaseNode(1, "", ""),
			Rows{
				Epic:     epic,
				Slot:     releaseEpicID,
				Releases: []store.ReleaseRun{candidate, passedStep, judgeRun(true, "")},
				Review:   releaseGapped(),
			},
			Action{
				Kind:    ActEnding,
				Outcome: outcomeReleased,
				Output: EndingBody(
					phaseReleased,
					[]string{"reviewer security: security gave no verdict twice: done: boom"},
					nil,
				),
			},
		},
		{
			"failed judge of this attempt stops with its reason",
			releaseNode(1, "", ""),
			Rows{
				Epic:     epic,
				Slot:     releaseEpicID,
				Releases: []store.ReleaseRun{candidate, passedStep, judgeRun(false, "the tag is missing")},
			},
			Action{Kind: ActStop, Output: "the tag is missing"},
		},
		{
			"passed judge of an earlier attempt does not count",
			releaseNode(2, "", ""),
			Rows{
				Epic:     epic,
				Slot:     releaseEpicID,
				Releases: []store.ReleaseRun{candidate, passedStep, judgeRun(true, "")},
			},
			Action{Kind: ActJudge, IDs: []string{attemptWorkID(2, "judge")}, Items: []string{"judge", "cand-1"}},
		},
		{
			"judge with no row stops when another epic holds the slot",
			releaseNode(1, "", ""),
			Rows{Epic: epic, Slot: releaseOtherEpicID, Releases: []store.ReleaseRun{candidate, passedStep}},
			Action{Kind: ActStop, Output: "the judge did not start: this epic does not hold the release slot"},
		},
		{
			"judge with no row gives none while paused",
			releaseNode(1, "", ""),
			Rows{
				Epic:     releasingEpicPaused(epic),
				Slot:     releaseEpicID,
				Releases: []store.ReleaseRun{candidate, passedStep},
			},
			Action{Kind: ActNone},
		},
		{
			"running judge gives none",
			releaseNode(1, "", ""),
			Rows{
				Epic:     epic,
				Slot:     releaseEpicID,
				Releases: []store.ReleaseRun{candidate, passedStep},
				Steps:    []store.StepRow{{ID: attemptWorkID(1, "judge")}},
			},
			Action{Kind: ActNone},
		},
		{
			"ended judge is recorded with the candidate",
			releaseNode(1, "", ""),
			Rows{
				Epic:     epic,
				Slot:     releaseEpicID,
				Releases: []store.ReleaseRun{candidate, passedStep},
				Steps:    []store.StepRow{{ID: attemptWorkID(1, "judge"), End: "done"}},
			},
			Action{Kind: ActRecord, IDs: []string{attemptWorkID(1, "judge")}, Items: []string{"judge", "cand-1"}},
		},
	}
	checkDecisions(t, Release{}, cases)
}

// releasingEpicPaused returns epic paused.
func releasingEpicPaused(epic store.EpicRow) store.EpicRow {
	epic.Paused = true
	return epic
}

// releaseReviewed returns a releasing epic with steps and the default release reviewer, enabled or not as enabled says.
func releaseReviewed(enabled bool, steps ...store.EpicReleaseStep) store.EpicRow {
	epic := releasingEpic(steps...)
	epic.Reviewers = []reviewers.Reviewer{{
		Name: "release", Enabled: enabled, Stage: []string{reviewers.StageRelease},
		MustNot: []string{"The candidate carries a clear danger to the release's destination."},
	}}
	return epic
}

// releaseReviewRun returns the release reviewer's run of the first attempt, which held the candidate cand-1 or refused it
// for reason. It is the review of the candidate's first generation, at position 1.
func releaseReviewRun(passed bool, reason string) store.ReleaseRun {
	return store.ReleaseRun{
		Part: store.ReleasePartReview, Position: 1, Attempt: 1, Head: "cand-1", Step: "release", Passed: passed,
		Reason: reason,
	}
}

// TestReleaseReviewerGatesTheFirstStep: the enabled release reviewers review the candidate once before the first release
// step starts from it. The first step starts only once every one has held the candidate, a refusal stops the release
// with its reason, and no review gates a later step or an epic with no enabled release reviewer.
func TestReleaseReviewerGatesTheFirstStep(t *testing.T) {
	t.Parallel()
	steps := []store.EpicReleaseStep{releaseStep("build the image", "rollback")}
	candidate := candidateRun(1, "cand-1")
	review := attemptWorkID(1, "review release")
	reviewItems := []string{"review", "cand-1", "release", "1"}
	firstStep := Action{
		Kind:  ActStep,
		IDs:   []string{attemptWorkID(1, "release 1")},
		Items: []string{"release 1", "cand-1"},
	}
	const refusal = "the release reviewer refused the candidate: a tag would overwrite a published release"
	cases := []decideCase{
		{
			"candidate with an enabled release reviewer and no row starts the reviewer at the candidate",
			releaseNode(1, "", ""),
			Rows{Epic: releaseReviewed(true, steps...), Slot: releaseEpicID, Releases: []store.ReleaseRun{candidate}},
			Action{Kind: ActStep, IDs: []string{review}, Items: reviewItems},
		},
		{
			"running release reviewer gives none",
			releaseNode(1, "", ""),
			Rows{
				Epic:     releaseReviewed(true, steps...),
				Slot:     releaseEpicID,
				Releases: []store.ReleaseRun{candidate},
				Steps:    []store.StepRow{{ID: review}},
			},
			Action{Kind: ActNone},
		},
		{
			"ended release reviewer is recorded with the candidate",
			releaseNode(1, "", ""),
			Rows{
				Epic:     releaseReviewed(true, steps...),
				Slot:     releaseEpicID,
				Releases: []store.ReleaseRun{candidate},
				Steps:    []store.StepRow{{ID: review, End: "gate_failed"}},
			},
			Action{Kind: ActRecord, IDs: []string{review}, Items: reviewItems},
		},
		{
			"release reviewer that held the candidate lets the first step start",
			releaseNode(1, "", ""),
			Rows{
				Epic:     releaseReviewed(true, steps...),
				Slot:     releaseEpicID,
				Releases: []store.ReleaseRun{candidate, releaseReviewRun(true, "")},
			},
			firstStep,
		},
		{
			"release reviewer that refused the candidate stops naming its reason",
			releaseNode(1, "", ""),
			Rows{
				Epic:     releaseReviewed(true, steps...),
				Slot:     releaseEpicID,
				Releases: []store.ReleaseRun{candidate, releaseReviewRun(false, refusal)},
			},
			Action{Kind: ActStop, Output: refusal},
		},
		{
			"release reviewer that held an earlier attempt does not count",
			releaseNode(2, "", ""),
			Rows{
				Epic:     releaseReviewed(true, steps...),
				Slot:     releaseEpicID,
				Releases: []store.ReleaseRun{candidate, releaseReviewRun(true, "")},
			},
			Action{
				Kind:  ActStep,
				IDs:   []string{attemptWorkID(2, "review release")},
				Items: []string{"review", "cand-1", "release", "1"},
			},
		},
		{
			"release reviewer that held a replaced candidate does not count for the candidate prepared again",
			releaseNode(1, "", ""),
			Rows{
				Epic: releaseReviewed(true, steps...),
				Slot: releaseEpicID,
				Releases: []store.ReleaseRun{
					candidateRun(1, "cand-1"),
					{Part: store.ReleasePartVerify, Attempt: 1, Head: "cand-1"},
					candidateRun(1, "cand-2"),
					releaseReviewRun(true, ""),
				},
			},
			Action{
				Kind:  ActStep,
				IDs:   []string{store.NodeWorkID(testRunID+"-generation-1", 1, "review release")},
				Items: []string{"review", "cand-2", "release", "2"},
			},
		},
		{
			"release reviewer gives none while the epic is paused",
			releaseNode(1, "", ""),
			Rows{
				Epic:     releasingEpicPaused(releaseReviewed(true, steps...)),
				Slot:     releaseEpicID,
				Releases: []store.ReleaseRun{candidate},
			},
			Action{Kind: ActNone},
		},
		{
			"first step starts ungated when the release reviewer is disabled",
			releaseNode(1, "", ""),
			Rows{Epic: releaseReviewed(false, steps...), Slot: releaseEpicID, Releases: []store.ReleaseRun{candidate}},
			firstStep,
		},
		{
			"second step starts without a review of its own",
			releaseNode(1, "", ""),
			Rows{
				Epic: releaseReviewed(true,
					releaseStep("build the image", "rollback"), releaseStep("push the tag", "rollback")),
				Slot: releaseEpicID,
				Releases: []store.ReleaseRun{
					candidate,
					releaseReviewRun(true, ""),
					{Part: store.ReleasePartStep, Position: 1, Attempt: 1, Head: "step-1", Passed: true},
				},
			},
			Action{Kind: ActStep, IDs: []string{attemptWorkID(1, "release 2")}, Items: []string{"release 2", "step-1"}},
		},
	}
	checkDecisions(t, Release{}, cases)
}
