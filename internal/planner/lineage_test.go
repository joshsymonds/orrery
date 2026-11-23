package planner

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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

func TestPlannerInfoPromptListsTheSpentLines(t *testing.T) {
	t.Parallel()
	prompt, err := Prompt(syntheticEpic(), 2, syntheticTriggers(), nil, []SpentLine{{Failed: []int64{17, 19}}})
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	want := "## Spent lines\n- T19 failed after replacing a failed task, so its line has failed 2 times (T17, T19). " +
		"A further try is your judgment: ask the owner a catastrophe question, or amend_evidence when the failure is " +
		"in a Requirement's evidence item, or plan a try that changes what the failures point at.\n\n## Failed turns\n"
	if !strings.Contains(prompt, want) {
		t.Fatalf("prompt lacks %q; prompt:\n%s", want, prompt)
	}
}

// failRunnerTask stores the planner's return for the runner epic's claimed turn turn, one task on R1 that touches
// internal/planner/runner.go, and fails that task seq as a build did before the task ladder: its status failed with
// its failure, and the pending trigger T<seq>-failed. No store write fails a task now, since every failure climbs it,
// so the failed task is written to the store file at path directly, as a task stored failed before the ladder reads.
func failRunnerTask(t *testing.T, s *store.Store, path string, turn, seq int64) {
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
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close %s: %v", path, err)
		}
	}()
	const failure = "confirmed: F1 the same finding"
	if _, err = db.ExecContext(ctx, `UPDATE epic_tasks SET status = 'failed', failure = ? WHERE epic = ? AND seq = ?`,
		failure, runnerEpicID, seq); err != nil {
		t.Fatalf("fail task %d: %v", seq, err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO epic_triggers (epic, id, cause, state, turn, created)
		VALUES (?, ?, ?, 'pending', 0, ?)`, runnerEpicID, fmt.Sprintf("T%d-failed", seq),
		fmt.Sprintf("task T%d failed: %s", seq, failure), claimedAt().Unix()); err != nil {
		t.Fatalf("insert failure trigger of task %d: %v", seq, err)
	}
}

// TestPlannerInfoAcceptsAThirdTryOfASpentLine: in the store, T1 fails, turn 2 replaces it with T2, which fails too, so
// the check of turn 3, which claims T2-failed, accepts a third try of the spent line: the spent line is information in
// the prompt, and no check refuses a return for it.
func TestPlannerInfoAcceptsAThirdTryOfASpentLine(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, path := openControlsStore(t)
	createRunnerEpic(t, s)
	for turn := int64(1); turn <= 2; turn++ {
		if _, err := s.ClaimTurn(ctx, runnerEpicID, claimedAt()); err != nil {
			t.Fatalf("ClaimTurn %d: %v", turn, err)
		}
		failRunnerTask(t, s, path, turn, turn)
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
	if got := check(json.RawMessage(validPlan)); got != nil {
		t.Errorf("check of a third try of a spent line = %q, want none", got)
	}
}
