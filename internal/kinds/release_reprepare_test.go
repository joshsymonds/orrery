package kinds

import (
	"fmt"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// staleRun returns a verify run of attempt that did not pass: origin's main was found not in the candidate head.
func staleRun(attempt int64, head string) store.ReleaseRun {
	return store.ReleaseRun{
		Part: store.ReleasePartVerify, Attempt: attempt, Head: head,
		Reason: "origin's main is not in candidate " + head + ", so the candidate is prepared again",
	}
}

// TestReleaseRepreparesCandidateOriginMainIsNotIn checks what a release does once a candidate of an earlier attempt is
// marked stale at this attempt: the candidate is not reused, so the release prepares a new one at this attempt, or waits
// while it is paused. The latest candidate that passed is the one reused, and a mark stands only against the attempt and
// the head it names.
func TestReleaseRepreparesCandidateOriginMainIsNotIn(t *testing.T) {
	t.Parallel()
	steps := []store.EpicReleaseStep{releaseStep("tag the release", "rollback")}
	cases := []decideCase{
		{
			"candidate of an earlier attempt that origin's main is not in is prepared again at this attempt",
			releaseNode(2, "", ""),
			Rows{
				Epic:     releasingEpic(steps...),
				Releases: []store.ReleaseRun{candidateRun(1, "cand-1"), staleRun(2, "cand-1")},
			},
			Action{Kind: ActPrepare},
		},
		{
			"candidate origin's main is not in waits to be prepared again while the epic is paused",
			releaseNode(2, "", ""),
			Rows{
				Epic:     releasingEpicPaused(releasingEpic(steps...)),
				Releases: []store.ReleaseRun{candidateRun(1, "cand-1"), staleRun(2, "cand-1")},
			},
			Action{Kind: ActNone},
		},
		{
			"latest candidate that origin's main is in is reused, not an earlier one that a mark names",
			releaseNode(2, "cand-2", `{"behind":false}`),
			Rows{
				Epic: releasingEpic(steps...),
				Releases: []store.ReleaseRun{
					candidateRun(1, "cand-1"),
					staleRun(2, "cand-1"),
					candidateRun(2, "cand-2"),
				},
			},
			Action{Kind: ActStep, IDs: []string{attemptWorkID(2, "release 1")}, Items: []string{"release 1", "cand-2"}},
		},
		{
			"latest candidate that origin's main is not in at this attempt is prepared again",
			releaseNode(3, "", ""),
			Rows{
				Epic: releasingEpic(steps...),
				Releases: []store.ReleaseRun{
					candidateRun(1, "cand-1"),
					candidateRun(2, "cand-2"),
					staleRun(3, "cand-2"),
				},
			},
			Action{Kind: ActPrepare},
		},
		{
			"candidate of this attempt that origin's main is not in is prepared again at this attempt",
			releaseNode(2, "", ""),
			Rows{
				Epic:     releasingEpic(steps...),
				Releases: []store.ReleaseRun{candidateRun(2, "cand-1"), staleRun(2, "cand-1")},
			},
			Action{Kind: ActPrepare},
		},
		{
			"candidate of this attempt with no mark is reused for the first step",
			releaseNode(2, "", ""),
			Rows{
				Epic:     releasingEpic(steps...),
				Releases: []store.ReleaseRun{candidateRun(2, "cand-1")},
			},
			Action{Kind: ActStep, IDs: []string{attemptWorkID(2, "release 1")}, Items: []string{"release 1", "cand-1"}},
		},
		{
			"mark of an earlier attempt does not stand against the candidate reused at this attempt",
			releaseNode(2, "", ""),
			Rows{
				Epic:     releasingEpic(steps...),
				Releases: []store.ReleaseRun{candidateRun(1, "cand-1"), staleRun(1, "cand-1")},
			},
			Action{Kind: ActStep, IDs: []string{attemptWorkID(2, "release 1")}, Items: []string{"release 1", "cand-1"}},
		},
		{
			"mark of another head does not stand against the candidate reused at this attempt",
			releaseNode(2, "", ""),
			Rows{
				Epic:     releasingEpic(steps...),
				Releases: []store.ReleaseRun{candidateRun(1, "cand-1"), staleRun(2, "cand-0")},
			},
			Action{Kind: ActStep, IDs: []string{attemptWorkID(2, "release 1")}, Items: []string{"release 1", "cand-1"}},
		},
	}
	checkDecisions(t, Release{}, cases)
}

// generationWorkID returns the id of part of attempt's work under generation of the test run, as the kind walks the Done
// gates of a candidate prepared again.
func generationWorkID(attempt, generation int64, part string) string {
	return store.NodeWorkID(gatedRun(store.NodeRun{ID: testRunID}, generation).ID, attempt, part)
}

// generationCheck returns the Done gate check i of attempt under generation of the test run, passed or not.
func generationCheck(attempt, generation int64, i int, passed bool) store.NodeCheck {
	check := doneCheck(attempt, i, passed)
	check.ID = generationWorkID(attempt, generation, fmt.Sprintf("done %d", i))
	return check
}

// TestReleaseRepreparesRunsDoneGatesOnNewCandidate checks that a candidate prepared again at an attempt walks the Done
// gates of its own: the checks for the candidate it replaces do not decide them, whether that candidate was prepared at
// an earlier attempt or at this one, and the new candidate is recorded only once its own gates have passed.
func TestReleaseRepreparesRunsDoneGatesOnNewCandidate(t *testing.T) {
	t.Parallel()
	gated := func(gates ...string) store.EpicRow {
		return store.EpicRow{ID: releaseEpicID, Phase: store.PhaseReleasing, DoneGates: gates}
	}
	replacedEarlier := []store.ReleaseRun{candidateRun(1, "cand-1"), staleRun(2, "cand-1")}
	replacedHere := []store.ReleaseRun{candidateRun(2, "cand-1"), staleRun(2, "cand-1")}
	cases := []decideCase{
		{
			"new candidate starts its Done gate under its generation although the gate passed for the candidate of an earlier attempt it replaces",
			releaseNode(2, "cand-2", `{"behind":true}`),
			Rows{
				Epic:     gated("lint"),
				Checks:   []store.NodeCheck{doneCheck(1, 1, true)},
				Releases: replacedEarlier,
			},
			Action{Kind: ActStart, IDs: []string{generationWorkID(2, 1, "done 1")}, Items: []string{"lint"}},
		},
		{
			"new candidate starts its Done gate under its generation although the gate passed for the candidate of this attempt it replaces",
			releaseNode(2, "cand-2", `{"behind":true}`),
			Rows{
				Epic:     gated("lint"),
				Checks:   []store.NodeCheck{doneCheck(2, 1, true)},
				Releases: replacedHere,
			},
			Action{Kind: ActStart, IDs: []string{generationWorkID(2, 1, "done 1")}, Items: []string{"lint"}},
		},
		{
			"new candidate that is not behind still walks its Done gates under its generation",
			releaseNode(2, "cand-2", `{"behind":false}`),
			Rows{
				Epic:     gated("lint"),
				Releases: replacedHere,
			},
			Action{Kind: ActStart, IDs: []string{generationWorkID(2, 1, "done 1")}, Items: []string{"lint"}},
		},
		{
			"new candidate whose Done gate did not pass stops naming the gate and the new revision",
			releaseNode(2, "cand-2", `{"behind":true}`),
			Rows{
				Epic:     gated("lint"),
				Checks:   []store.NodeCheck{generationCheck(2, 1, 1, false)},
				Releases: replacedEarlier,
			},
			Action{Kind: ActStop, Output: "done gate lint did not pass at cand-2"},
		},
		{
			"new candidate with every Done gate passed under its generation is recorded as the candidate of this attempt",
			releaseNode(2, "cand-2", `{"behind":true}`),
			Rows{
				Epic:     gated("lint"),
				Checks:   []store.NodeCheck{generationCheck(2, 1, 1, true)},
				Releases: replacedHere,
			},
			Action{Kind: ActRecord, Items: []string{"candidate", "cand-2"}},
		},
	}
	checkDecisions(t, Release{}, cases)
}

// TestReleaseGateReplanDecisions checks what a Done gate that did not pass on a candidate decides: a gate that ran and
// failed on a candidate that merged origin's commit sends the release back to planning with its report, and once an
// earlier replan recorded that origin commit, the same gate stops the release. A gate that did not run, timed out, or
// failed on a candidate with no origin stops it.
func TestReleaseGateReplanDecisions(t *testing.T) {
	t.Parallel()
	gated := store.EpicRow{ID: releaseEpicID, Phase: store.PhaseReleasing, DoneGates: []string{"lint"}}
	earlier := []store.ReleaseRun{candidateRun(1, "cand-1"), staleRun(2, "cand-1")}
	replanned := []store.ReleaseRun{
		candidateRun(1, "cand-1"), staleRun(2, "cand-1"),
		{Part: store.ReleasePartReplan, Attempt: 1, Head: "origin-2", Reason: "the report of the first replan"},
	}
	const stop = "done gate lint did not pass at cand-2"
	// gateOf returns the Done gate check of the candidate cand-2 walks: attempt 2 under its generation 1.
	gateOf := func(passed bool) store.NodeCheck {
		check := doneCheck(2, 1, passed)
		check.ID = generationWorkID(2, 1, "done 1")
		return check
	}
	ran := gateOf(false)
	notRun := gateOf(true)
	notRun.NotRun = "the check did not start"
	timedOut := gateOf(true)
	timedOut.TimedOut = true
	cases := []decideCase{
		{
			"gate that ran and failed on a candidate that merged origin's commit replans with its report",
			releaseNode(2, "cand-2", `{"behind":true,"origin":"origin-2"}`),
			Rows{Epic: gated, Checks: []store.NodeCheck{ran}, Releases: earlier},
			Action{
				Kind: ActReplan, Output: "Done gate lint failed on commit cand-2: it exited 1.", Reason: stop,
				Items: []string{store.ReleasePartReplan, "origin-2"},
			},
		},
		{
			"gate that ran and failed on the origin commit an earlier replan recorded stops, since origin has not moved",
			releaseNode(2, "cand-2", `{"behind":true,"origin":"origin-2"}`),
			Rows{Epic: gated, Checks: []store.NodeCheck{ran}, Releases: replanned},
			Action{Kind: ActStop, Output: stop + "; origin's main has not moved since the last replan"},
		},
		{
			"gate that did not run stops without a replan",
			releaseNode(2, "cand-2", `{"behind":true,"origin":"origin-2"}`),
			Rows{Epic: gated, Checks: []store.NodeCheck{notRun}, Releases: earlier},
			Action{Kind: ActStop, Output: stop},
		},
		{
			"gate that timed out stops without a replan",
			releaseNode(2, "cand-2", `{"behind":true,"origin":"origin-2"}`),
			Rows{Epic: gated, Checks: []store.NodeCheck{timedOut}, Releases: earlier},
			Action{Kind: ActStop, Output: stop},
		},
		{
			"gate that ran and failed on a candidate with no origin stops without a replan",
			releaseNode(2, "cand-2", `{"behind":true}`),
			Rows{Epic: gated, Checks: []store.NodeCheck{ran}, Releases: earlier},
			Action{Kind: ActStop, Output: stop},
		},
	}
	checkDecisions(t, Release{}, cases)
}
