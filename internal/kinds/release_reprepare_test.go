package kinds

import (
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
			Action{
				Kind:  ActStep,
				IDs:   []string{store.NodeWorkID(store.ReleaseGatedID(testRunID, 1), 2, "release 1")},
				Items: []string{"release 1", "cand-2"},
			},
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
			Action{
				Kind:  ActStep,
				IDs:   []string{store.NodeWorkID(store.ReleaseGatedID(testRunID, 1), 2, "release 1")},
				Items: []string{"release 1", "cand-1"},
			},
		},
	}
	checkDecisions(t, Release{}, cases)
}

// TestReleaseRepreparedCandidateIsRecordedWithoutGates checks what a release decides once it has prepared a candidate
// again at an attempt: a candidate that is not behind origin's main is recorded as the candidate of this attempt at
// once, with no Done gate run although the epic has one and the gate passed for the candidate it replaces, and a
// prepare that names origin's commit hands the merge to the release integrator under the candidate's generation.
func TestReleaseRepreparedCandidateIsRecordedWithoutGates(t *testing.T) {
	t.Parallel()
	gated := store.EpicRow{ID: releaseEpicID, Phase: store.PhaseReleasing, DoneGates: []string{"lint"}}
	replacedEarlier := []store.ReleaseRun{candidateRun(1, "cand-1"), staleRun(2, "cand-1")}
	replacedHere := []store.ReleaseRun{candidateRun(2, "cand-1"), staleRun(2, "cand-1")}
	cases := []decideCase{
		{
			"new candidate not behind origin's main is recorded although it replaces one of an earlier attempt",
			releaseNode(2, "cand-2", `{"behind":false}`),
			Rows{Epic: gated, Releases: replacedEarlier},
			Action{Kind: ActRecord, Items: []string{"candidate", "cand-2"}},
		},
		{
			"new candidate not behind origin's main is recorded although it replaces one of this attempt",
			releaseNode(2, "cand-2", `{"behind":false}`),
			Rows{Epic: gated, Releases: replacedHere},
			Action{Kind: ActRecord, Items: []string{"candidate", "cand-2"}},
		},
		{
			"new prepare that names origin's commit starts the release integrator under the new generation",
			releaseNode(2, "", cleanResult),
			Rows{Epic: gated, Releases: replacedHere},
			Action{
				Kind:  ActIntegrator,
				IDs:   []string{store.NodeWorkID(store.ReleaseGatedID(testRunID, 1), 2, integratorWork)},
				Items: []string{"4"},
			},
		},
	}
	checkDecisions(t, Release{}, cases)
}
