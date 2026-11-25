package enginehost

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/store"
)

// reproJudgeTestInput returns the input of a reproduction judge for task T4, a rename at version 2, whose
// reproduction exited 0 with go test -v output that shows one test passing.
func reproJudgeTestInput() ReproJudgeInput {
	deliverable := "Rename Widget to Gadget everywhere"
	task := store.EpicTask{
		Seq: 4, Title: "Rename Widget", Deliverable: deliverable, Version: 2,
		Versions: []store.TaskVersion{{
			Version: 2, Source: store.SourceOrchestrator, Reason: "name the test too", State: store.VersionApplied,
			Deliverable: &deliverable,
		}},
	}
	return ReproJudgeInput{
		Tasks:      []store.EpicTask{task},
		Conclusion: "TestGadget passes",
		Red: &store.EvidenceRun{
			Revision: "base-1", Verdict: evidence.VerdictFails, JudgeReason: "no TestGadget at the base",
		},
		Repro: store.NodeCheck{
			ID: "repro-1", Revision: "overlay-1", Argv: []string{"go", "test", "./widget/"},
			Stdout: "=== RUN   TestGadget\n--- PASS: TestGadget (0.00s)\nPASS\nok  \tacme/widget\t0.01s\n",
			Stderr: "warning: cache\n",
		},
		Base:     "base-1",
		Commit:   "commit-1",
		DiffStat: " widget/widget.go | 4 ++--\n 1 file changed, 2 insertions(+), 2 deletions(-)\n",
	}
}

func TestReproJudgeGateFields(t *testing.T) {
	t.Parallel()
	want := gates.Gate{
		Name:       "repro_judge",
		Agent:      "sonnet-high-ro",
		Limit:      20 * time.Minute,
		Assertions: []gates.Assertion{{Text: reproJudgeAssertion, Kind: gates.KindMust}},
	}
	if got := ReproJudgeGate("sonnet-high-ro"); !reflect.DeepEqual(got, want) {
		t.Errorf("ReproJudgeGate = %+v, want %+v", got, want)
	}
}

// TestReproJudgePromptCarriesWhatTheJudgeRulesOn: the prompt asks whether the reproduction contradicts red, says that a
// refactor, rename or removal task's tests passing on base code do not, and carries the task with its version lines
// and deliverable, the conclusion, red's verdict and reason, the reproduction's command, revision, exit and logs, the
// test facts read from the logs as orrery's reading, and the diff stat.
func TestReproJudgePromptCarriesWhatTheJudgeRulesOn(t *testing.T) {
	t.Parallel()
	prompt, err := ReproJudgePrompt(reproJudgeTestInput())
	if err != nil {
		t.Fatalf("ReproJudgePrompt: %v", err)
	}
	wants := []string{
		"Decide whether this reproduction contradicts red: do the tests fail to prove the change",
		"as a refactor, rename or removal task's tests are, do not contradict red by passing there",
		"on the base base-1 with only the test files of the commit commit-1 laid over it",
		"Task T4: Rename Widget\nSpec: version 2 of T4 is this task's current spec",
		"\nDeliverable: Rename Widget to Gadget everywhere\n",
		"\nConclusion: TestGadget passes\n",
		"\nRed at the base base-1: judged fails against the conclusion; the judge's reason: no TestGadget at the base\n",
		"\nReproduction command: [\"go\",\"test\",\"./widget/\"]\n",
		"\nReproduction revision: overlay-1\n",
		"\nExit: 0\nTimed out: false\n",
		"\nStdout, its last 4096 bytes as orrery keeps them:\n    === RUN   TestGadget\n    --- PASS: TestGadget",
		"\nStderr, its last 4096 bytes as orrery keeps them:\n    warning: cache\n",
		"\n" + judgeFactsHeading + "\n  Formats recognized: go test\n",
		"\n  Passed (1): TestGadget\n",
		"\nDiff stat (git diff --stat base-1 commit-1):\n     widget/widget.go | 4 ++--\n     1 file changed",
	}
	for _, want := range wants {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt)
		}
	}
}

// TestReproJudgePromptSaysWhatIsMissing: a reproduction with no red run kept, empty logs and no diff says so, and its
// test facts say that no runner's output was recognized.
func TestReproJudgePromptSaysWhatIsMissing(t *testing.T) {
	t.Parallel()
	in := reproJudgeTestInput()
	in.Red = nil
	in.Repro.Stdout, in.Repro.Stderr = "", "\n"
	in.DiffStat = ""
	prompt, err := ReproJudgePrompt(in)
	if err != nil {
		t.Fatalf("ReproJudgePrompt: %v", err)
	}
	for _, want := range []string{
		"\nRed: no red run of the item is kept.\n",
		"\nStdout: empty\nStderr: empty\n",
		"\n  No test runner's output was recognized.\n",
		"\nDiff stat (git diff --stat base-1 commit-1):\n    (no change)",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt)
		}
	}
}

// TestReproJudgeVerdictReadsAsTheEvidenceJudgeDoes: a done judge holds, a gate_failed one whose first result fails
// fails with its finding's claim and evidence, and any other end is no verdict with how it ended.
func TestReproJudgeVerdictReadsAsTheEvidenceJudgeDoes(t *testing.T) {
	t.Parallel()
	holds := `{"status":"done","results":[{"assertion":"a","kind":"must","result":"holds",` +
		`"evidence":" a rename's test passes on base code "}],"findings":[]}`
	fails := `{"status":"done","results":[{"assertion":"a","kind":"must","result":"fails","evidence":"e"}],` +
		`"findings":[{"citation":"a","claim":"TestGadget passes without the change","evidence":"it only renames"}]}`
	cases := []struct {
		name        string
		row         store.StepRow
		wantVerdict string
		wantReason  string
	}{
		{
			name: "done holds", row: store.StepRow{End: "done", Result: holds},
			wantVerdict: evidence.VerdictHolds, wantReason: "a rename's test passes on base code",
		},
		{
			name: "gate_failed fails", row: store.StepRow{End: "gate_failed", Result: fails},
			wantVerdict: evidence.VerdictFails, wantReason: "TestGadget passes without the change: it only renames",
		},
		{
			name: "overtime is no verdict", row: store.StepRow{End: "overtime", Reason: "limit reached"},
			wantReason: "overtime: limit reached",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			verdict, reason := ReproJudgeVerdict(tc.row)
			if verdict != tc.wantVerdict || reason != tc.wantReason {
				t.Errorf("ReproJudgeVerdict = (%q, %q), want (%q, %q)", verdict, reason, tc.wantVerdict, tc.wantReason)
			}
		})
	}
}
