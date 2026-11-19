package planner

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// turnTriggers is a triggerReader over the triggers each turn claimed, keyed by turn. A turn it does not hold claimed
// none. err, when set, is what every read returns.
type turnTriggers struct {
	claimed map[int64][]string
	err     error
}

func (r turnTriggers) TurnTriggers(_ context.Context, _ string, turn int64) ([]store.EpicTrigger, error) {
	if r.err != nil {
		return nil, r.err
	}
	triggers := make([]store.EpicTrigger, 0, len(r.claimed[turn]))
	for _, id := range r.claimed[turn] {
		triggers = append(triggers, store.EpicTrigger{ID: id, State: "consumed", Turn: turn})
	}
	return triggers, nil
}

// lineTask is task seq, planned by turn, with status, contributing to contributes and touching files.
func lineTask(seq, turn int64, status string, contributes, files []string) store.EpicTask {
	return store.EpicTask{
		Seq: seq, Turn: turn, Title: "task", Status: status, Contributes: contributes, Files: files,
	}
}

// claimedOf returns the triggers a turn claimed, by id.
func claimedOf(ids ...string) []store.EpicTrigger {
	triggers := make([]store.EpicTrigger, len(ids))
	for i, id := range ids {
		triggers[i] = store.EpicTrigger{ID: id, State: "claimed"}
	}
	return triggers
}

func TestSpentLines(t *testing.T) {
	t.Parallel()
	wiring := []string{"cmd/orreryd/eventwake_wiring_test.go"}
	cases := []struct {
		name    string
		tasks   []store.EpicTask
		turns   map[int64][]string
		claimed []store.EpicTrigger
		want    []SpentLine
	}{
		{
			// The no-step-wait epic: T17 failed red, T19 replaced it in the turn that claimed T17-failed and failed
			// green, and the turn that claims T19-failed may not try R3's pane test a third time.
			name: "a replacement that failed spends the line",
			tasks: []store.EpicTask{
				lineTask(17, 9, taskFailed, []string{"R3"}, wiring),
				lineTask(18, 9, taskFailed, []string{"R6"}, []string{"internal/enginehost/callerless_mcp_test.go"}),
				lineTask(19, 10, taskFailed, []string{"R3"}, wiring),
			},
			turns:   map[int64][]string{10: {"T17-failed"}},
			claimed: claimedOf("T19-failed", "T18-failed"),
			want:    []SpentLine{{Failed: []int64{17, 19}}},
		},
		{
			// The web-catalog epic: T2, T3 and T4 replaced each other, linked by their shared files.
			name: "a line is followed back through every replacement",
			tasks: []store.EpicTask{
				lineTask(2, 1, taskFailed, nil, []string{"web/src/fixtures/transport.ts"}),
				lineTask(3, 2, taskFailed, nil, []string{"web/src/fixtures/transport.ts"}),
				lineTask(4, 4, taskFailed, nil, []string{"web/src/fixtures/transport.ts", "web/src/page.tsx"}),
			},
			turns:   map[int64][]string{2: {"T2-failed"}, 4: {"T3-failed"}},
			claimed: claimedOf("T4-failed"),
			want:    []SpentLine{{Failed: []int64{2, 3, 4}}},
		},
		{
			name:    "a first failure is not spent",
			tasks:   []store.EpicTask{lineTask(9, 2, taskFailed, []string{"R4"}, wiring)},
			claimed: claimedOf("T9-failed"),
		},
		{
			name: "a task planned after the owner answered starts its line again",
			tasks: []store.EpicTask{
				lineTask(21, 17, taskFailed, []string{"R2"}, wiring),
				lineTask(23, 20, taskFailed, []string{"R2"}, wiring),
			},
			turns:   map[int64][]string{20: {"answer:owner-Q1-clear-grant"}},
			claimed: claimedOf("T23-failed"),
		},
		{
			name: "a failed task that shares no Requirement or file with the next is not its predecessor",
			tasks: []store.EpicTask{
				lineTask(5, 1, taskFailed, []string{"R1"}, []string{"a.go"}),
				lineTask(6, 2, taskFailed, []string{"R2"}, []string{"b.go"}),
			},
			turns:   map[int64][]string{2: {"T5-failed"}},
			claimed: claimedOf("T6-failed"),
		},
		{
			name: "a task that is not failed has no line",
			tasks: []store.EpicTask{
				lineTask(5, 1, taskFailed, []string{"R1"}, wiring),
				lineTask(6, 2, "done", []string{"R1"}, wiring),
			},
			turns:   map[int64][]string{2: {"T5-failed"}},
			claimed: claimedOf("T6-failed", "created", "T6-failedx", "T06-failed"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			epic := store.EpicRow{ID: "orrery-1a2b3c4/lines", Tasks: tc.tasks}
			got, err := spentLines(context.Background(), turnTriggers{claimed: tc.turns}, epic, tc.claimed)
			if err != nil {
				t.Fatalf("spentLines: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("spentLines = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSpentLinesReturnsATriggerReadError(t *testing.T) {
	t.Parallel()
	epic := store.EpicRow{ID: "e", Tasks: []store.EpicTask{lineTask(1, 1, taskFailed, nil, []string{"a.go"})}}
	boom := errors.New("boom")
	_, err := spentLines(context.Background(), turnTriggers{err: boom}, epic, claimedOf("T1-failed"))
	if !errors.Is(err, boom) {
		t.Fatalf("spentLines error = %v, want it to wrap %v", err, boom)
	}
}

// spentTasks are the failed tasks of a spent line: T17 then T19, which contribute to R3 and touch the pane test.
func spentTasks() []store.EpicTask {
	wiring := []string{"cmd/orreryd/eventwake_wiring_test.go"}
	return []store.EpicTask{
		lineTask(17, 9, taskFailed, []string{"R3"}, wiring),
		lineTask(19, 10, taskFailed, []string{"R3"}, wiring),
	}
}

func TestCheckSpentLines(t *testing.T) {
	t.Parallel()
	lines := []SpentLine{{Failed: []int64{17, 19}}}
	again := Task{Title: "the pane test again", Contributes: []string{"R3"}, Files: []string{"cmd/orreryd/other.go"}}
	sameFile := Task{Title: "same file", Files: []string{"cmd/orreryd/eventwake_wiring_test.go"}}
	other := Task{Title: "other work", Requirements: []string{"R1"}, Files: []string{"internal/x.go"}}
	ask := Question{Label: "pane-test"}
	const triesAgain = `: tries again the line of T19, which has failed 2 times (T17, T19); plan no further try: ` +
		`ask the owner a catastrophe question, or amend_evidence when the failure is in a Requirement's evidence item`
	const askOwner = "questions: the line of T19 has failed 2 times (T17, T19); ask the owner a catastrophe question " +
		"about it, or amend_evidence when the failure is in a Requirement's evidence item"
	cases := []struct {
		name  string
		ret   Return
		lines []SpentLine
		want  []string
	}{
		{name: "no spent line refuses nothing", ret: Return{Tasks: []Task{again}}},
		{
			name: "a task that shares a Requirement or a file with the line is refused, and the owner is asked",
			ret:  Return{Tasks: []Task{other, again, sameFile}}, lines: lines,
			want: []string{
				`tasks[1] "the pane test again"` + triesAgain, `tasks[2] "same file"` + triesAgain, askOwner,
			},
		},
		{
			name: "a question settles the line, and other work may start beside it",
			ret:  Return{Tasks: []Task{other}, Questions: []Question{ask}}, lines: lines,
		},
		{
			name: "a question does not let the line be tried again in the same turn",
			ret:  Return{Tasks: []Task{again}, Questions: []Question{ask}}, lines: lines,
			want: []string{`tasks[0] "the pane test again"` + triesAgain},
		},
		{
			name:  "amending a Requirement's evidence settles the line",
			ret:   Return{Actions: []EngineAction{{Kind: store.ActionAmendEvidence, Requirement: "R3"}}},
			lines: lines,
		},
		{
			name:  "closing the epic settles the line",
			ret:   Return{Actions: []EngineAction{{Kind: store.ActionCloseEpic, Close: "abandoned"}}},
			lines: lines,
		},
		{
			name:  "another action does not settle the line",
			ret:   Return{Actions: []EngineAction{{Kind: store.ActionRetryEpic}}},
			lines: lines,
			want:  []string{askOwner},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := checkSpentLines(tc.ret, tc.lines, spentTasks()); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("checkSpentLines =\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

func TestPromptListsTheSpentLines(t *testing.T) {
	t.Parallel()
	prompt, err := Prompt(syntheticEpic(), 2, syntheticTriggers(), nil, []SpentLine{{Failed: []int64{17, 19}}})
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	want := "## Spent lines\n- T19 failed after replacing a failed task, so its line has failed 2 times (T17, T19). " +
		"Plan no further try of it: ask the owner a catastrophe question, or amend_evidence when the failure is in a " +
		"Requirement's evidence item.\n\n## Last failure\n"
	if !strings.Contains(prompt, want) {
		t.Fatalf("prompt lacks %q; prompt:\n%s", want, prompt)
	}
}

// failRunnerTask stores the planner's return for the runner epic's claimed turn turn, one task on R1 that touches
// internal/planner/runner.go, and fails that task seq as its build would.
func failRunnerTask(t *testing.T, s *store.Store, turn, seq int64) {
	t.Helper()
	ctx := context.Background()
	if err := s.CompleteTurn(ctx, runnerEpicID, turn, store.EpicPlan{
		Outline: "one task", Outcome: "work",
		Tasks: []store.EpicTaskInput{{
			Title: "Build it", Deliverable: "the runner", Files: []string{"internal/planner/runner.go"},
			Requirements: []string{"R1"}, Gate: "check", Evidence: runnerEvidenceItem(),
		}},
	}, claimedAt()); err != nil {
		t.Fatalf("CompleteTurn %d: %v", turn, err)
	}
	if err := s.FailTask(ctx, runnerEpicID, seq, store.TaskBuild{}, "confirmed: F1 the same finding", nil,
		claimedAt()); err != nil {
		t.Fatalf("FailTask %d: %v", seq, err)
	}
}

// TestReadoptCheckRefusesAThirdTryOfASpentLine: in the store, T1 fails, turn 2 replaces it with T2, which fails too, so
// the check of turn 3, which claims T2-failed, refuses a third try and a return that does not ask the owner, and passes
// one that asks.
func TestReadoptCheckRefusesAThirdTryOfASpentLine(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	for turn := int64(1); turn <= 2; turn++ {
		if _, err := s.ClaimTurn(ctx, runnerEpicID, claimedAt()); err != nil {
			t.Fatalf("ClaimTurn %d: %v", turn, err)
		}
		failRunnerTask(t, s, turn, turn)
	}
	turn, err := s.ClaimTurn(ctx, runnerEpicID, claimedAt())
	if err != nil {
		t.Fatalf("ClaimTurn 3: %v", err)
	}
	if err = s.SetTurnStep(ctx, runnerEpicID, turn.Seq, "step-3"); err != nil {
		t.Fatalf("SetTurnStep: %v", err)
	}

	check, err := ReadoptCheck(s, testResolver{})(ctx, store.StepRow{ID: "step-3"})
	if err != nil {
		t.Fatalf("ReadoptCheck: %v", err)
	}
	want := []string{`tasks[0] "Build it": tries again the line of T2, which has failed 2 times (T1, T2); plan no ` +
		`further try: ask the owner a catastrophe question, or amend_evidence when the failure is in a ` +
		`Requirement's evidence item`}
	if got := check(json.RawMessage(validPlan)); !reflect.DeepEqual(got, want) {
		t.Errorf("check of a third try = %q, want %q", got, want)
	}
	asks := `{"status":"done","decisions":[],"tasks":[],"outline":"ask","questions":[` + storeQuestion +
		`],"outcome":"wait"}`
	if got := check(json.RawMessage(asks)); got != nil {
		t.Errorf("check of a return that asks the owner = %q, want none", got)
	}
}
