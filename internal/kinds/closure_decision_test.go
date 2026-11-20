package kinds

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// The finding ids, question texts, choices and answers of these tests, in the shape of two epics whose owner kept the
// change a finding named: orrery-web-catalog, which kept an app.test.tsx edit that conformance-1 named, and
// orrery-say-now, whose director recorded test_parsimony-1 as an observation.
const (
	webCatalogFinding  = "conformance-1"
	webCatalogParsimon = "test_parsimony-1"
	webCatalogKeep     = "keep-fix"
	webCatalogRevert   = "revert-fix"
	webCatalogAnswerID = "owner-Q2-keep-fix"
	webCatalogAnswer   = "Keep the app.test.tsx edit and close conformance-1 with no code change."
	sayNowFinding      = "test_parsimony-1"
	sayNowKeep         = "accept-as-observation"
	sayNowAnswerID     = "director-Q1"
	sayNowAnswer       = "Keep the test and record test_parsimony-1 as an observation."
)

// ledgerFinding returns the confirmed ledger finding id that reviewer raised at position, with closures.
func ledgerFinding(id, reviewer string, position int64, closures ...store.Closure) store.LedgerFinding {
	return store.LedgerFinding{
		Position: position, ID: id, Reviewer: reviewer, Claim: "claim of " + id, Result: reviewResultConfirmed,
		Reason: "confirmed", Closures: closures,
	}
}

// webCatalogQuestions returns the questions of the web-catalog shape: Q1, an answered question about a stuck grant
// that closes no finding, and Q2, which asks whether to keep the edit conformance-1 names and was answered with choice.
func webCatalogQuestions(choice string) []store.EpicQuestion {
	return []store.EpicQuestion{
		{
			Seq: 1, Turn: 19, Label: "gate-grant-stuck", State: store.QuestionAnswered, Choice: "clear-grant",
			Choices: []store.EpicChoice{
				{ID: "clear-grant", Does: "You clear the stuck gate-1 grant."},
				{ID: "replan-as-is", Does: "I plan the same task again."},
			},
		},
		{
			Seq: 2, Turn: 22, Label: "review-finding-scope", State: store.QuestionAnswered, Choice: choice,
			Choices: []store.EpicChoice{
				{
					ID:     webCatalogKeep,
					Does:   "Accept the app.test.tsx edit, and close conformance-1 with no code change.",
					Closes: []string{webCatalogFinding},
				},
				{
					ID:   webCatalogRevert,
					Does: "Plan a task that restores the test exactly as it was at the epic's base.",
				},
			},
		},
	}
}

// webCatalogDecisions returns the decisions of the web-catalog shape: a note, the answer to Q1, and the answer to Q2,
// with text.
func webCatalogDecisions(text string) []store.EpicDecision {
	return []store.EpicDecision{
		{Seq: 1, ID: "DL1", Text: "Scout step answered the research questions."},
		{Seq: 18, ID: "owner-Q1-clear-grant", Text: "Cleared.", Question: 1},
		{Seq: 19, ID: webCatalogAnswerID, Text: text, Question: 2},
	}
}

// webCatalogRows returns the closure rows of the web-catalog shape in closureRound: conformance-1 and test_parsimony-1
// open, Q2 answered with choice by text, and the steps given.
func webCatalogRows(choice, text string, steps ...store.StepRow) Rows {
	rows := closureRows(store.Review{Ledger: []store.LedgerFinding{
		ledgerFinding(webCatalogFinding, "conformance", 1),
		ledgerFinding(webCatalogParsimon, "test_parsimony", 2),
	}}, steps...)
	rows.Epic.Questions, rows.Epic.Decisions = webCatalogQuestions(choice), webCatalogDecisions(text)
	return rows
}

// webCatalogClosed is the closure Q2's keep-fix answer gives conformance-1 in closureRound.
func webCatalogClosed() store.Closure {
	return store.Closure{
		Finding: webCatalogFinding, Round: closureRound, Result: closureResultClosed,
		Reason: "closed by decision " + webCatalogAnswerID + " (answers Q2 with " + webCatalogKeep + "): " +
			webCatalogAnswer,
	}
}

// closeUnitOf returns the unit the close chain starts for finding.
func closeUnitOf(finding store.LedgerFinding) Unit {
	return closureUnit("close "+finding.ID, finding)
}

func TestClosureClosesAFindingTheOwnerKeptByAnswer(t *testing.T) {
	t.Parallel()
	node := closureNode(testRev)
	parsimony := ledgerFinding(webCatalogParsimon, "test_parsimony", 2)
	parsimonyClosed := store.StepRow{
		ID: workID("close " + webCatalogParsimon), End: "done", Result: closureClosedReturn,
	}
	conformanceOpen := store.StepRow{
		ID: workID("close " + webCatalogFinding), End: "done", Result: closureOpenReturn,
	}
	checkDecisions(t, Closure{}, []decideCase{
		{
			"an answer whose choice closes the finding starts no verifier for it",
			node,
			webCatalogRows(webCatalogKeep, webCatalogAnswer),
			closureStarted(closeUnitOf(parsimony)),
		},
		{
			"the run ends passed with the answer's closure, citing the decision, beside the verifier's",
			node,
			webCatalogRows(webCatalogKeep, webCatalogAnswer, parsimonyClosed),
			closureEnd(outcomePassed, []int64{2, 3}, webCatalogClosed(), store.Closure{
				Finding: webCatalogParsimon, Round: closureRound, Step: parsimonyClosed.ID,
				Result: closureResultClosed, Reason: "the fix holds",
			}),
		},
		{
			"the answer closes the finding though its verifier already ruled it open on the unchanged code",
			node,
			webCatalogRows(webCatalogKeep, webCatalogAnswer, conformanceOpen, parsimonyClosed),
			closureEnd(outcomePassed, []int64{2, 3}, webCatalogClosed(), store.Closure{
				Finding: webCatalogParsimon, Round: closureRound, Step: parsimonyClosed.ID,
				Result: closureResultClosed, Reason: "the fix holds",
			}),
		},
		{
			"the answer closes the finding without waiting on its running verifier",
			node,
			webCatalogRows(webCatalogKeep, webCatalogAnswer,
				store.StepRow{ID: workID("close " + webCatalogFinding)}, parsimonyClosed),
			closureEnd(outcomePassed, []int64{2, 3}, webCatalogClosed(), store.Closure{
				Finding: webCatalogParsimon, Round: closureRound, Step: parsimonyClosed.ID,
				Result: closureResultClosed, Reason: "the fix holds",
			}),
		},
	})
}

func TestClosureLeavesAFindingToItsVerifierWithoutAClosingAnswer(t *testing.T) {
	t.Parallel()
	node := closureNode(testRev)
	conformance := ledgerFinding(webCatalogFinding, "conformance", 1)
	parsimony := ledgerFinding(webCatalogParsimon, "test_parsimony", 2)
	both := closureStarted(closeUnitOf(conformance), closeUnitOf(parsimony))
	unanswered := webCatalogRows("", "")
	unanswered.Epic.Questions[1].State = store.QuestionOpen
	unanswered.Epic.Decisions = unanswered.Epic.Decisions[:2]
	reasked := webCatalogRows(webCatalogKeep, webCatalogAnswer)
	reasked.Epic.Questions = append(reasked.Epic.Questions, store.EpicQuestion{
		Seq: 3, Turn: 24, Label: "review-finding-scope", State: store.QuestionAnswered, Choice: webCatalogRevert,
		Choices: reasked.Epic.Questions[1].Choices,
	})
	reasked.Epic.Decisions = append(reasked.Epic.Decisions,
		store.EpicDecision{Seq: 20, ID: "owner-Q3-revert", Question: 3})
	checkDecisions(t, Closure{}, []decideCase{
		{
			"a question not yet answered closes nothing",
			node,
			unanswered,
			both,
		},
		{
			"an answer with a choice that plans a correction closes nothing",
			node,
			webCatalogRows(webCatalogRevert, ""),
			both,
		},
		{
			"an answer in the owner's own words closes nothing",
			node,
			webCatalogRows(store.ChoiceOther, "Keep it, I suppose."),
			both,
		},
		{
			"a later answer that keeps the finding open overrides an earlier one that closed it",
			node,
			reasked,
			both,
		},
	})
}

func TestClosureClosesTheSayNowObservationInTheSecondRound(t *testing.T) {
	t.Parallel()
	node := closureNode(testRev)
	// The first round's verifier ruled the finding open; Q1 was asked in the second round's plan and answered by the
	// director before the second round's closure ran.
	rows := closureRows(store.Review{Ledger: []store.LedgerFinding{
		ledgerFinding(sayNowFinding, "test_parsimony", 1, store.Closure{
			Finding: sayNowFinding, Round: 1, Step: "close-round-1", Result: closureResultOpen,
			Reason: "the finding is partly fixed but still holds",
		}),
		ledgerFinding("test_parsimony-2", "test_parsimony", 2, store.Closure{
			Finding: "test_parsimony-2", Round: 1, Step: "close-round-1-b", Result: closureResultClosed,
			Reason: "holdertest.Main builds it",
		}),
	}})
	rows.Epic.Questions = []store.EpicQuestion{{
		Seq: 1, Turn: 26, Label: "review-finding-scope", State: store.QuestionAnswered, Choice: sayNowKeep,
		Choices: []store.EpicChoice{
			{
				ID:     sayNowKeep,
				Does:   "Keep the test as it is. test_parsimony-1 is recorded as an observation, nothing changes.",
				Closes: []string{sayNowFinding},
			},
			{ID: "add-tick-seam", Does: "Authorize a small production seam."},
		},
	}}
	rows.Epic.Decisions = []store.EpicDecision{{Seq: 19, ID: sayNowAnswerID, Text: sayNowAnswer, Question: 1}}
	checkDecisions(t, Closure{}, []decideCase{{
		"the director's answer closes the finding the first round left open, and the round passes",
		node,
		rows,
		closureEnd(outcomePassed, []int64{2, 3}, store.Closure{
			Finding: sayNowFinding, Round: closureRound, Result: closureResultClosed,
			Reason: "closed by decision " + sayNowAnswerID + " (answers Q1 with " + sayNowKeep + "): " + sayNowAnswer,
		}),
	}})
}

func TestClosureAnswerWithNoTextCitesTheDecisionAlone(t *testing.T) {
	t.Parallel()
	rows := webCatalogRows(webCatalogKeep, "",
		store.StepRow{ID: workID("close " + webCatalogParsimon), End: "done", Result: closureClosedReturn})
	got := Closure{}.Decide(closureNode(testRev), rows)
	want := "closed by decision " + webCatalogAnswerID + " (answers Q2 with " + webCatalogKeep + ")"
	if len(got.Closures) == 0 || got.Closures[0].Reason != want {
		t.Fatalf("Decide() closures = %+v, want the first closed with reason %q", got.Closures, want)
	}
}

func TestConcludeNamesTheFindingsClosedByDecision(t *testing.T) {
	t.Parallel()
	node := nodeWith(testRev, false)
	fails := store.ReviewVerdict{Position: 1, Reviewer: "conformance", Verdict: "fails"}
	decided := ledgerFinding(webCatalogFinding, "conformance", 1, webCatalogClosed())
	verified := ledgerFinding(webCatalogParsimon, "test_parsimony", 2, store.Closure{
		Finding: webCatalogParsimon, Round: closureRound, Step: "close-step", Result: closureResultClosed,
	})
	stillOpen := ledgerFinding("security-1", "security", 3,
		store.Closure{Finding: "security-1", Round: 1, Step: "close-a", Result: closureResultOpen},
		store.Closure{Finding: "security-1", Round: 2, Step: "close-b", Result: closureResultOpen})
	rounds := []store.ReviewRound{{Round: 1, Outcome: outcomeFailed}, {Round: 2, Outcome: outcomePassed}}
	clean := store.Review{
		Frozen: testRev, Verdicts: []store.ReviewVerdict{fails}, Ledger: []store.LedgerFinding{decided, verified},
		Rounds: rounds,
	}
	gapped := clean
	gapped.Ledger = []store.LedgerFinding{decided, verified, stillOpen}
	checkDecisions(t, Conclude{}, []decideCase{
		{
			"a finding closed by decision is no gap, and the clean ending names it",
			node,
			concludeRows(clean),
			concludeClean("epic " + concludeEpicID + " ended clean at " + concludeBase +
				"\nClosed by decision:\n- " + webCatalogFinding + " in round 2"),
		},
		{
			"an ending with gaps names the finding closed by decision after its gaps",
			node,
			declare(concludeRows(gapped), "open:security-1"),
			gapsEnd("Ended with 1 gap:\n- security-1: open after 2 rounds"+
				"\nClosed by decision:\n- "+webCatalogFinding+" in round 2",
				correctFor([]string{"security-1: open after 2 rounds (planner: the planner cannot resolve it)"},
					"open:security-1")),
		},
	})
}
