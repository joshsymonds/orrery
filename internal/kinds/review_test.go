package kinds

import (
	"strconv"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// The findings a reviewer raises in the gate returns below, numbered 1 and 2. reviewRaised gives each one's fields.
const (
	reviewFindingOne = `{"id":"f1","claim":"claim 1","citation":"file1.go:1","location":"file1.go",` +
		`"evidence":"seen 1","verify_by":"read 1"}`
	reviewFindingTwo = `{"id":"f2","claim":"claim 2","citation":"file2.go:2","location":"file2.go",` +
		`"evidence":"seen 2","verify_by":"read 2"}`
)

// The kept returns of reviewer and verifier steps in these tests.
const (
	reviewTwoFindingsReturn = `{"status":"done","results":[{"result":"holds"}],"findings":[` +
		reviewFindingOne + `,` + reviewFindingTwo + `]}`
	reviewOneFindingReturn = `{"status":"done","results":[{"result":"holds"}],"findings":[` +
		reviewFindingOne + `]}`
	reviewUndeterminedReturn = `{"status":"done","results":[{"result":"undetermined"}],"findings":[` +
		reviewFindingOne + `]}`
	reviewFailsBesideUndetermined = `{"status":"done","results":[{"result":"fails"},{"result":"undetermined"}],"findings":[` +
		reviewFindingOne + `]}`
	reviewUndeterminedAlone = `{"status":"done","results":[{"result":"undetermined"}],"findings":[]}`
	reviewBlockedReturn     = `{"status":"blocked","results":[{"result":"holds"}],"findings":[` +
		reviewFindingOne + `]}`
	reviewNoFindingReturn    = `{"status":"done","results":[{"result":"holds"}],"findings":[]}`
	reviewConfirmedReturn    = `{"status":"done","results":[{"id":"f1","evidence":"the claim holds","result":"confirmed"}]}`
	reviewDroppedReturn      = `{"status":"done","results":[{"id":"f1","evidence":"the claim is wrong","result":"dropped"}]}`
	reviewUndeterminedVerify = `{"status":"done","results":[{"id":"f1","evidence":"cannot tell","result":"undetermined"}]}`
	reviewClosedReturn       = `{"status":"done","results":[{"id":"f1","evidence":"closed","result":"closed"}]}`
)

// reviewRaised returns the finding reviewer raises as number place, with the id the ledger gives it and nothing a
// verifier or the ledger adds.
func reviewRaised(reviewer string, place int) store.LedgerFinding {
	n := strconv.Itoa(place)
	return store.LedgerFinding{
		ID: reviewer + "-" + n, Reviewer: reviewer, Finding: "f" + n, Claim: "claim " + n,
		Citation: "file" + n + ".go:" + n, Location: "file" + n + ".go", Evidence: "seen " + n, VerifyBy: "read " + n,
	}
}

// reviewUnverifiedAt returns the finding reviewer raises as number place, as the ledger holds it at position with verify
// off.
func reviewUnverifiedAt(reviewer string, place int, position int64) store.LedgerFinding {
	finding := reviewRaised(reviewer, place)
	finding.Position = position
	finding.Result = "confirmed"
	finding.Unverified = true
	finding.Reason = "unverified: reviewer " + reviewer + " has verify off"
	return finding
}

func TestReviewPrepare(t *testing.T) {
	t.Parallel()
	reviewers := []Reviewer{{Name: "security", Verify: true}}
	unrevised := nodeWith("", false)
	checkDecisions(t, Review{}, []decideCase{
		{"unrevised run prepares", unrevised, Rows{Reviewers: reviewers}, Action{Kind: ActPrepare}},
		{
			"unrevised run on a paused epic gives none",
			unrevised,
			Rows{Epic: store.EpicRow{Paused: true}, Reviewers: reviewers},
			Action{Kind: ActNone},
		},
		{
			"unrevised run on a parked epic gives none",
			unrevised,
			Rows{Epic: store.EpicRow{Parked: "budget"}, Reviewers: reviewers},
			Action{Kind: ActNone},
		},
	})
}

func TestReviewStarts(t *testing.T) {
	t.Parallel()
	node := nodeWith(testRev, false)
	both := []Reviewer{{Name: "security", Verify: true}, {Name: "perf"}}
	checkDecisions(t, Review{}, []decideCase{
		{
			"every reviewer starts in one start in order",
			node,
			Rows{Reviewers: both},
			Action{
				Kind: ActStart,
				IDs:  []string{workID("reviewer 1"), workID("reviewer 2")},
				Units: []Unit{
					{ID: workID("reviewer 1"), Part: "reviewer 1", Reviewer: "security"},
					{ID: workID("reviewer 2"), Part: "reviewer 2", Reviewer: "perf"},
				},
			},
		},
		{
			"a reviewer with a step is not started again",
			node,
			Rows{Reviewers: both, Steps: []store.StepRow{{ID: workID("reviewer 1")}}},
			Action{
				Kind:  ActStart,
				IDs:   []string{workID("reviewer 2")},
				Units: []Unit{{ID: workID("reviewer 2"), Part: "reviewer 2", Reviewer: "perf"}},
			},
		},
		{
			"starts give none on a paused epic",
			node,
			Rows{Epic: store.EpicRow{Paused: true}, Reviewers: both},
			Action{Kind: ActNone},
		},
		{
			"starts give none on a parked epic",
			node,
			Rows{Epic: store.EpicRow{Parked: "budget"}, Reviewers: both},
			Action{Kind: ActNone},
		},
		{"no reviewer ends passed with no verdict", node, Rows{}, Action{Kind: ActEnd, Outcome: outcomePassed}},
	})
}

func TestReviewWaits(t *testing.T) {
	t.Parallel()
	node := nodeWith(testRev, false)
	security := []Reviewer{{Name: "security", Verify: true}}
	both := []Reviewer{{Name: "security"}, {Name: "perf"}}
	checkDecisions(t, Review{}, []decideCase{
		{
			"running reviewer waits",
			node,
			Rows{Reviewers: security, Steps: []store.StepRow{{ID: workID("reviewer 1")}}},
			Action{Kind: ActNone},
		},
		{
			"running verifier waits",
			node,
			Rows{Reviewers: security, Steps: []store.StepRow{
				{ID: workID("reviewer 1"), End: "gate_failed", Result: reviewOneFindingReturn},
				{ID: workID("verify 1 1")},
			}},
			Action{Kind: ActNone},
		},
		{
			"running reviewer waits beside a holding one",
			node,
			Rows{Reviewers: both, Steps: []store.StepRow{
				{ID: workID("reviewer 1"), End: "done"},
				{ID: workID("reviewer 2")},
			}},
			Action{Kind: ActNone},
		},
	})
}

func TestReviewHolds(t *testing.T) {
	t.Parallel()
	node := nodeWith(testRev, false)
	security := []Reviewer{{Name: "security", Verify: true}}
	both := []Reviewer{{Name: "security"}, {Name: "perf"}}
	stopped := store.StepRow{ID: workID("reviewer 1"), End: "stopped", Reason: "owner stopped it"}
	checkDecisions(t, Review{}, []decideCase{
		{
			"stopped reviewer step holds the epic once",
			node,
			Rows{Reviewers: security, Steps: []store.StepRow{stopped}},
			Action{Kind: ActHold, IDs: []string{workID("reviewer 1")}, Output: "reviewer 1 stopped: owner stopped it"},
		},
		{
			"a stop already held starts the next try",
			node,
			Rows{Reviewers: security, Steps: []store.StepRow{stopped}, Review: store.Review{
				Holds: []string{workID("reviewer 1")},
			}},
			Action{
				Kind:  ActStart,
				IDs:   []string{workID("reviewer 1 try 2")},
				Units: []Unit{{ID: workID("reviewer 1 try 2"), Part: "reviewer 1", Reviewer: "security"}},
			},
		},
		{
			"a hold comes before the starts",
			node,
			Rows{Reviewers: both, Steps: []store.StepRow{stopped}},
			Action{Kind: ActHold, IDs: []string{workID("reviewer 1")}, Output: "reviewer 1 stopped: owner stopped it"},
		},
		{
			"a hold is kept on a paused epic",
			node,
			Rows{Epic: store.EpicRow{Paused: true}, Reviewers: security, Steps: []store.StepRow{stopped}},
			Action{Kind: ActHold, IDs: []string{workID("reviewer 1")}, Output: "reviewer 1 stopped: owner stopped it"},
		},
		{
			"stopped verifier holds the epic with its part",
			node,
			Rows{Reviewers: security, Steps: []store.StepRow{
				{ID: workID("reviewer 1"), End: "gate_failed", Result: reviewOneFindingReturn},
				{ID: workID("verify 1 1"), End: "stopped", Reason: "owner stopped it"},
			}},
			Action{Kind: ActHold, IDs: []string{workID("verify 1 1")}, Output: "verify 1 1 stopped: owner stopped it"},
		},
	})
}

func TestReviewRetries(t *testing.T) {
	t.Parallel()
	node := nodeWith(testRev, false)
	security := []Reviewer{{Name: "security", Verify: true}}
	retry := Action{
		Kind:  ActStart,
		IDs:   []string{workID("reviewer 1 try 3")},
		Units: []Unit{{ID: workID("reviewer 1 try 3"), Part: "reviewer 1", Reviewer: "security"}},
	}
	checkDecisions(t, Review{}, []decideCase{
		{
			"engine restart gives a free next try",
			node,
			Rows{Reviewers: security, Steps: []store.StepRow{{ID: workID("reviewer 1"), End: "engine_restarted"}}},
			Action{
				Kind:  ActStart,
				IDs:   []string{workID("reviewer 1 try 2")},
				Units: []Unit{{ID: workID("reviewer 1 try 2"), Part: "reviewer 1", Reviewer: "security"}},
			},
		},
		{
			"a restart does not count against the part",
			node,
			Rows{Reviewers: security, Steps: []store.StepRow{
				{ID: workID("reviewer 1"), End: "engine_restarted"},
				{ID: workID("reviewer 1 try 2"), End: "overtime", Reason: "ran long"},
			}},
			retry,
		},
		{
			"one costly end gives the next try",
			node,
			Rows{Reviewers: security, Steps: []store.StepRow{{ID: workID("reviewer 1"), End: "overtime"}}},
			Action{
				Kind:  ActStart,
				IDs:   []string{workID("reviewer 1 try 2")},
				Units: []Unit{{ID: workID("reviewer 1 try 2"), Part: "reviewer 1", Reviewer: "security"}},
			},
		},
		{
			"a costly end then a verdict holds",
			node,
			Rows{Reviewers: security, Steps: []store.StepRow{
				{ID: workID("reviewer 1"), End: "overtime", Reason: "ran long"},
				{ID: workID("reviewer 1 try 2"), End: "done"},
			}},
			Action{
				Kind: ActEnd, Outcome: outcomePassed, Output: "security: holds",
				Verdicts: []store.ReviewVerdict{{
					Position: 1, Reviewer: "security", Step: workID("reviewer 1 try 2"), Verdict: "holds",
				}},
			},
		},
		{
			"two costly ends give a gap with the second end's reason",
			node,
			Rows{Reviewers: security, Steps: []store.StepRow{
				{ID: workID("reviewer 1"), End: "overtime", Reason: "ran long"},
				{ID: workID("reviewer 1 try 2"), End: "failed", Reason: "exit 2"},
			}},
			Action{
				Kind: ActEnd, Outcome: outcomePassed, Output: "security: gap",
				Verdicts: []store.ReviewVerdict{{
					Position: 1, Reviewer: "security", Step: workID("reviewer 1 try 2"), Verdict: "gap",
					Reason: "reviewer 1 gave no verdict twice: failed: exit 2",
				}},
			},
		},
		{
			"a restart between costly ends still counts toward the gap",
			node,
			Rows{Reviewers: security, Steps: []store.StepRow{
				{ID: workID("reviewer 1"), End: "overtime", Reason: "ran long"},
				{ID: workID("reviewer 1 try 2"), End: "engine_restarted"},
				{ID: workID("reviewer 1 try 3"), End: "failed", Reason: "exit 2"},
			}},
			Action{
				Kind: ActEnd, Outcome: outcomePassed, Output: "security: gap",
				Verdicts: []store.ReviewVerdict{{
					Position: 1, Reviewer: "security", Step: workID("reviewer 1 try 3"), Verdict: "gap",
					Reason: "reviewer 1 gave no verdict twice: failed: exit 2",
				}},
			},
		},
		{
			"gate_failed without findings is a costly end",
			node,
			Rows{Reviewers: security, Steps: []store.StepRow{
				{ID: workID("reviewer 1"), End: "gate_failed", Result: reviewNoFindingReturn, Reason: "no finding"},
				{ID: workID("reviewer 1 try 2"), End: "gate_failed", Result: reviewNoFindingReturn, Reason: "again"},
			}},
			Action{
				Kind: ActEnd, Outcome: outcomePassed, Output: "security: gap",
				Verdicts: []store.ReviewVerdict{{
					Position: 1, Reviewer: "security", Step: workID("reviewer 1 try 2"), Verdict: "gap",
					Reason: "reviewer 1 gave no verdict twice: gate_failed: again",
				}},
			},
		},
		{
			"gate_failed with a blocked status is a costly end",
			node,
			Rows{Reviewers: security, Steps: []store.StepRow{
				{ID: workID("reviewer 1"), End: "gate_failed", Result: reviewBlockedReturn, Reason: "blocked"},
				{ID: workID("reviewer 1 try 2"), End: "gate_failed", Result: reviewBlockedReturn, Reason: "again"},
			}},
			Action{
				Kind: ActEnd, Outcome: outcomePassed, Output: "security: gap",
				Verdicts: []store.ReviewVerdict{{
					Position: 1, Reviewer: "security", Step: workID("reviewer 1 try 2"), Verdict: "gap",
					Reason: "reviewer 1 gave no verdict twice: gate_failed: again",
				}},
			},
		},
	})
}

// TestReviewVerdicts checks the verdict each reviewer's settled step gives. A reviewer whose return leaves an assertion
// undetermined records an undetermined verdict with its step's reason, and the review goes on: it is not a costly end
// that a second try would repeat. The reason names every assertion that is not holds, so a finding raised beside it is
// not lost from the record.
func TestReviewVerdicts(t *testing.T) {
	t.Parallel()
	node := nodeWith(testRev, false)
	security := []Reviewer{{Name: "security", Verify: true}}
	checkDecisions(t, Review{}, []decideCase{
		{
			"undetermined reviewer records its verdict with the step's reason",
			node,
			Rows{Reviewers: security, Steps: []store.StepRow{
				{
					ID:     workID("reviewer 1"),
					End:    "gate_failed",
					Result: reviewUndeterminedAlone,
					Reason: "undetermined: the suite passes",
				},
			}},
			Action{
				Kind: ActEnd, Outcome: outcomePassed, Output: "security: undetermined",
				Verdicts: []store.ReviewVerdict{{
					Position: 1, Reviewer: "security", Step: workID("reviewer 1"), Verdict: "undetermined",
					Reason: "undetermined: the suite passes",
				}},
			},
		},
		{
			"undetermined reviewer beside a holding one ends the review with both",
			node,
			Rows{
				Reviewers: []Reviewer{{Name: "security"}, {Name: "perf"}},
				Steps: []store.StepRow{
					{
						ID:     workID("reviewer 1"),
						End:    "gate_failed",
						Result: reviewUndeterminedAlone,
						Reason: "undetermined: a",
					},
					{ID: workID("reviewer 2"), End: "done"},
				},
			},
			Action{
				Kind: ActEnd, Outcome: outcomePassed, Output: "security: undetermined\nperf: holds",
				Verdicts: []store.ReviewVerdict{
					{
						Position: 1, Reviewer: "security", Step: workID("reviewer 1"), Verdict: "undetermined",
						Reason: "undetermined: a",
					},
					{Position: 2, Reviewer: "perf", Step: workID("reviewer 2"), Verdict: "holds"},
				},
			},
		},
		{
			"undetermined assertion beside a finding is undetermined with the step's reason, and its finding is settled",
			node,
			Rows{Reviewers: []Reviewer{{Name: "perf"}}, Steps: []store.StepRow{
				{ID: workID("reviewer 1"), End: "gate_failed", Result: reviewUndeterminedReturn, Reason: "undecided"},
			}},
			Action{
				Kind: ActEnd, Outcome: outcomePassed, Output: "perf: undetermined",
				Verdicts: []store.ReviewVerdict{
					{
						Position: 1,
						Reviewer: "perf",
						Step:     workID("reviewer 1"),
						Verdict:  "undetermined",
						Reason:   "undecided",
					},
				},
				Ledger: []store.LedgerFinding{{
					ID: "perf-1", Position: 1, Reviewer: "perf", Finding: "f1", Claim: "claim 1",
					Citation: "file1.go:1", Location: "file1.go", Evidence: "seen 1", VerifyBy: "read 1",
					Result: "confirmed", Reason: "unverified: reviewer perf has verify off", Unverified: true,
				}},
			},
		},
		{
			"fails beside an undetermined assertion fails, and its finding is settled",
			node,
			Rows{Reviewers: []Reviewer{{Name: "perf"}}, Steps: []store.StepRow{
				{ID: workID("reviewer 1"), End: "gate_failed", Result: reviewFailsBesideUndetermined, Reason: "mixed"},
			}},
			Action{
				Kind:    ActEnd,
				Outcome: outcomePassed,
				Output:  "perf: fails",
				Verdicts: []store.ReviewVerdict{
					{Position: 1, Reviewer: "perf", Step: workID("reviewer 1"), Verdict: "fails"},
				},
				Ledger: []store.LedgerFinding{{
					ID: "perf-1", Position: 1, Reviewer: "perf", Finding: "f1", Claim: "claim 1",
					Citation: "file1.go:1", Location: "file1.go", Evidence: "seen 1", VerifyBy: "read 1",
					Result: "confirmed", Reason: "unverified: reviewer perf has verify off", Unverified: true,
				}},
			},
		},
		{
			"done reviewer holds",
			node,
			Rows{Reviewers: security, Steps: []store.StepRow{{ID: workID("reviewer 1"), End: "done"}}},
			Action{
				Kind: ActEnd, Outcome: outcomePassed, Output: "security: holds",
				Verdicts: []store.ReviewVerdict{{
					Position: 1, Reviewer: "security", Step: workID("reviewer 1"), Verdict: "holds",
				}},
			},
		},
		{
			"fails with verify off confirms its findings unverified",
			node,
			Rows{Reviewers: []Reviewer{{Name: "perf"}}, Steps: []store.StepRow{
				{ID: workID("reviewer 1"), End: "gate_failed", Result: reviewTwoFindingsReturn},
			}},
			Action{
				Kind: ActEnd, Outcome: outcomePassed, Output: "perf: fails",
				Verdicts: []store.ReviewVerdict{{
					Position: 1, Reviewer: "perf", Step: workID("reviewer 1"), Verdict: "fails",
				}},
				Ledger: []store.LedgerFinding{
					reviewUnverifiedAt("perf", 1, 1),
					reviewUnverifiedAt("perf", 2, 2),
				},
			},
		},
		{
			"ledger places findings in order across reviewers",
			node,
			Rows{Reviewers: []Reviewer{{Name: "security"}, {Name: "perf"}}, Steps: []store.StepRow{
				{ID: workID("reviewer 1"), End: "gate_failed", Result: reviewOneFindingReturn},
				{ID: workID("reviewer 2"), End: "gate_failed", Result: reviewTwoFindingsReturn},
			}},
			Action{
				Kind: ActEnd, Outcome: outcomePassed, Output: "security: fails\nperf: fails",
				Verdicts: []store.ReviewVerdict{
					{Position: 1, Reviewer: "security", Step: workID("reviewer 1"), Verdict: "fails"},
					{Position: 2, Reviewer: "perf", Step: workID("reviewer 2"), Verdict: "fails"},
				},
				Ledger: []store.LedgerFinding{
					reviewUnverifiedAt("security", 1, 1),
					reviewUnverifiedAt("perf", 1, 2),
					reviewUnverifiedAt("perf", 2, 3),
				},
			},
		},
		{
			"holds, fails and gap end one review",
			node,
			Rows{
				Reviewers: []Reviewer{{Name: "security", Verify: true}, {Name: "perf"}, {Name: "lint"}},
				Steps: []store.StepRow{
					{ID: workID("reviewer 1"), End: "done"},
					{ID: workID("reviewer 2"), End: "gate_failed", Result: reviewOneFindingReturn},
					{ID: workID("reviewer 3"), End: "overtime", Reason: "ran long"},
					{ID: workID("reviewer 3 try 2"), End: "failed", Reason: "exit 2"},
				},
			},
			Action{
				Kind: ActEnd, Outcome: outcomePassed, Output: "security: holds\nperf: fails\nlint: gap",
				Verdicts: []store.ReviewVerdict{
					{Position: 1, Reviewer: "security", Step: workID("reviewer 1"), Verdict: "holds"},
					{Position: 2, Reviewer: "perf", Step: workID("reviewer 2"), Verdict: "fails"},
					{
						Position: 3, Reviewer: "lint", Step: workID("reviewer 3 try 2"), Verdict: "gap",
						Reason: "reviewer 3 gave no verdict twice: failed: exit 2",
					},
				},
				Ledger: []store.LedgerFinding{reviewUnverifiedAt("perf", 1, 1)},
			},
		},
	})
}

func TestReviewVerifiers(t *testing.T) {
	t.Parallel()
	node := nodeWith(testRev, false)
	security := []Reviewer{{Name: "security", Verify: true}}
	failed := store.StepRow{ID: workID("reviewer 1"), End: "gate_failed", Result: reviewOneFindingReturn}
	verdictOf := func(result, reason string) store.LedgerFinding {
		finding := reviewRaised("security", 1)
		finding.Position = 1
		finding.Step = workID("verify 1 1")
		finding.Result = result
		finding.Reason = reason
		return finding
	}
	verdictEnd := func(ledger ...store.LedgerFinding) Action {
		return Action{
			Kind:    ActEnd,
			Outcome: outcomePassed,
			Output:  "security: fails",
			Verdicts: []store.ReviewVerdict{
				{Position: 1, Reviewer: "security", Step: workID("reviewer 1"), Verdict: "fails"},
			},
			Ledger: ledger,
		}
	}
	verifierStart := func(place int) Unit {
		return Unit{
			ID: workID("verify 1 " + strconv.Itoa(place)), Part: "verify 1 " + strconv.Itoa(place),
			Reviewer: "security", Finding: reviewRaised("security", place),
		}
	}
	checkDecisions(t, Review{}, []decideCase{
		{
			"each finding of a failed reviewer starts one verifier",
			node,
			Rows{Reviewers: security, Steps: []store.StepRow{
				{ID: workID("reviewer 1"), End: "gate_failed", Result: reviewTwoFindingsReturn},
			}},
			Action{
				Kind:  ActStart,
				IDs:   []string{workID("verify 1 1"), workID("verify 1 2")},
				Units: []Unit{verifierStart(1), verifierStart(2)},
			},
		},
		{
			"verifier starts give none on a paused epic",
			node,
			Rows{Epic: store.EpicRow{Paused: true}, Reviewers: security, Steps: []store.StepRow{
				{ID: workID("reviewer 1"), End: "gate_failed", Result: reviewTwoFindingsReturn},
			}},
			Action{Kind: ActNone},
		},
		{
			"confirming verifier keeps its finding confirmed",
			node,
			Rows{Reviewers: security, Steps: []store.StepRow{
				failed, {ID: workID("verify 1 1"), End: "done", Result: reviewConfirmedReturn},
			}},
			verdictEnd(verdictOf("confirmed", "the claim holds")),
		},
		{
			"dropping verifier drops its finding",
			node,
			Rows{Reviewers: security, Steps: []store.StepRow{
				failed, {ID: workID("verify 1 1"), End: "done", Result: reviewDroppedReturn},
			}},
			verdictEnd(verdictOf("dropped", "the claim is wrong")),
		},
		{
			"undetermined verifier leaves its finding undetermined",
			node,
			Rows{Reviewers: security, Steps: []store.StepRow{
				failed, {ID: workID("verify 1 1"), End: "done", Result: reviewUndeterminedVerify},
			}},
			verdictEnd(verdictOf("undetermined", "cannot tell")),
		},
		{
			"verifier gap leaves its finding undetermined with the gap's reason",
			node,
			Rows{Reviewers: security, Steps: []store.StepRow{
				failed,
				{ID: workID("verify 1 1"), End: "overtime", Reason: "ran long"},
				{ID: workID("verify 1 1 try 2"), End: "failed", Reason: "exit 2"},
			}},
			verdictEnd(func() store.LedgerFinding {
				finding := verdictOf("undetermined", "verify 1 1 gave no verdict twice: failed: exit 2")
				finding.Step = workID("verify 1 1 try 2")
				return finding
			}()),
		},
		{
			"verifier result other than its three costs one end",
			node,
			Rows{Reviewers: security, Steps: []store.StepRow{
				failed, {ID: workID("verify 1 1"), End: "done", Result: reviewClosedReturn},
			}},
			Action{
				Kind: ActStart,
				IDs:  []string{workID("verify 1 1 try 2")},
				Units: []Unit{
					{
						ID:       workID("verify 1 1 try 2"),
						Part:     "verify 1 1",
						Reviewer: "security",
						Finding:  reviewRaised("security", 1),
					},
				},
			},
		},
		{
			"running verifier gives none",
			node,
			Rows{Reviewers: security, Steps: []store.StepRow{failed, {ID: workID("verify 1 1")}}},
			Action{Kind: ActNone},
		},
	})
}
