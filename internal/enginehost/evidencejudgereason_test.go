package enginehost

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// TestJudgeCarryVerdictReasonRead reads the reason judgeVerdict keeps with each verdict from the row of a judge step. A holds
// keeps the first result's evidence, trimmed; a fails keeps the claim and evidence of the finding citing its assertion; a
// holds whose return does not read keeps no reason.
func TestJudgeCarryVerdictReasonRead(t *testing.T) {
	t.Parallel()
	const assertion = "the build passes"
	holds := `{"status":"done","results":[{"assertion":"` + assertion + `","kind":"must","result":"holds",` +
		`"evidence":"  the build log ends with exit 0  "}],"findings":[]}`
	fails := `{"status":"done","results":[{"assertion":"` + assertion + `","kind":"must","result":"fails",` +
		`"evidence":"the link step reports an error"}],` +
		`"findings":[{"citation":"` + assertion + `","claim":"the link step fails","evidence":"ld: undefined symbol"}]}`
	failsUncited := `{"status":"done","results":[{"assertion":"` + assertion + `","kind":"must","result":"fails",` +
		`"evidence":"the link step reports an error"}],` +
		`"findings":[{"citation":"another assertion","claim":"unrelated claim","evidence":"unrelated evidence"}]}`
	cases := []struct {
		name        string
		row         store.StepRow
		wantVerdict string
		wantReason  string
	}{
		{
			name:        "holds reads the result evidence",
			row:         store.StepRow{End: stepLoopEndDone, Result: holds},
			wantVerdict: evidence.VerdictHolds,
			wantReason:  "the build log ends with exit 0",
		},
		{
			name:        "fails reads the citing finding",
			row:         store.StepRow{End: stepLoopEndGateFailed, Result: fails},
			wantVerdict: evidence.VerdictFails,
			wantReason:  "the link step fails: ld: undefined symbol",
		},
		{
			name:        "fails without a citing finding reads the result evidence",
			row:         store.StepRow{End: stepLoopEndGateFailed, Result: failsUncited},
			wantVerdict: evidence.VerdictFails,
			wantReason:  "the link step reports an error",
		},
		{
			name:        "holds whose return does not read has no reason",
			row:         store.StepRow{End: stepLoopEndDone, Result: "{"},
			wantVerdict: evidence.VerdictHolds,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			verdict, reason := judgeVerdict(tc.row)
			if verdict != tc.wantVerdict || reason != tc.wantReason {
				t.Errorf("judgeVerdict = (%q, %q), want (%q, %q)", verdict, reason, tc.wantVerdict, tc.wantReason)
			}
		})
	}
}

// TestJudgeCarryVerdictReasonStored runs a green capture through the runner over a real store, whose judge ends with a row
// whose result carries its reasoning. It reads the run back from the store, and its verdict and judge reason are the ones
// the judge wrote.
func TestJudgeCarryVerdictReasonStored(t *testing.T) {
	t.Parallel()
	const assertion = "the build passes"
	holds := `{"status":"done","results":[{"assertion":"` + assertion + `","kind":"must","result":"holds",` +
		`"evidence":"the build log ends with exit 0"}],"findings":[]}`
	fails := `{"status":"done","results":[{"assertion":"` + assertion + `","kind":"must","result":"fails",` +
		`"evidence":"the link step reports an error"}],` +
		`"findings":[{"citation":"` + assertion + `","claim":"the link step fails","evidence":"ld: undefined symbol"}]}`
	cases := []struct {
		name        string
		judge       evidenceRunJudge
		wantVerdict string
		wantReason  string
	}{
		{
			name:        "holds",
			judge:       evidenceRunJudge{end: store.StepRow{End: stepLoopEndDone, Result: holds}},
			wantVerdict: evidence.VerdictHolds,
			wantReason:  "the build log ends with exit 0",
		},
		{
			name:        "fails",
			judge:       evidenceRunJudge{end: store.StepRow{End: stepLoopEndGateFailed, Result: fails}},
			wantVerdict: evidence.VerdictFails,
			wantReason:  "the link step fails: ld: undefined symbol",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo, head := evidenceRunRepo(t, "exit 0\n")
			st := epicTestStore(t)
			evidenceRunSeed(t, st, repo, evidenceRunItem(), evidenceRunItem())
			steps := &evidenceRunSteps{judges: []evidenceRunJudge{tc.judge}}
			ev := evidenceRunNew(t, st, steps)

			started, _ := evidenceRunOnce(t, ev, st, head, evidence.PhaseGreen)
			got := judgeReasonStoredRun(t, st, started.ID)
			if got.Verdict != tc.wantVerdict || got.JudgeReason != tc.wantReason {
				t.Fatalf(
					"stored run = verdict %q judge reason %q, want %q %q",
					got.Verdict, got.JudgeReason, tc.wantVerdict, tc.wantReason,
				)
			}
		})
	}
}

// judgeReasonStoredRun returns the run with id among the stored runs of evidenceRunEpic, and fails the test when it is not
// stored there.
func judgeReasonStoredRun(t *testing.T, st *store.Store, id string) store.EvidenceRun {
	t.Helper()
	runs, err := st.EvidenceRuns(t.Context(), evidenceRunEpic)
	if err != nil {
		t.Fatalf("EvidenceRuns: %v", err)
	}
	for _, run := range runs {
		if run.ID == id {
			return run
		}
	}
	t.Fatalf("evidence run %s is not stored among %d runs", id, len(runs))
	return store.EvidenceRun{}
}
