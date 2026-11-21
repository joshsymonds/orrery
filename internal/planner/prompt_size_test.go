package planner

import (
	"fmt"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// The prompt size fixture is sized like orrery-outside-controls, whose planner prompts grew from 35 KB to 108 KB over
// seven turns (DL5): four Requirements, three seed decisions, six succeeded turns of five decisions each, twenty tasks
// spread over those turns, and a seventh turn that claims the failure of one task.
const (
	promptSizeTaskCount        = 20
	promptSizeTurnCount        = 6
	promptSizeTurn             = 7
	promptSizeRequirementCount = 4
	promptSizeSeedCount        = 3
	promptSizeDecisionsPerTurn = 5
	promptSizeDecisionSize     = 200
	promptSizeMinSize          = 6000
	promptSizeMaxSize          = 14000
	promptSizeConclusionShare  = 3
	promptSizeFailedSeq        = 17
	promptSizeFailedSize       = 6000
	promptSizeRungEvery        = 4
	promptSizeOldFullBytes     = 100000
	promptSizeGrowthBound      = 15000
	promptSizeFailureCause     = "task T17 failed: checks_failed: owned: changed paths outside owned: " +
		"internal/fixture/part17/file2.go"
	promptSizeFailure = "checks_failed: owned: changed paths outside owned: internal/fixture/part17/file2.go\n" +
		"the unit check named internal/fixture/shared.go as a path the task needs\n" +
		"the check log follows, one line for each test that ran\n" +
		"--- FAIL: TestFixtureSeventeen (0.02s)\n" +
		"    fixture_test.go:41: the second case expected 3 and got 2\n" +
		"FAIL\tgithub.com/joshsymonds/orrery/internal/fixture\t0.031s"
)

// promptSizeText returns invented text of exactly n bytes: sentences that name label and count upward, cut to n.
func promptSizeText(label string, n int) string {
	var text strings.Builder
	for sentence := 1; text.Len() < n; sentence++ {
		fmt.Fprintf(&text, "%s, sentence %d, keeps the store and the planner in step. ", label, sentence)
	}
	return text.String()[:n]
}

// promptSizeRequirementID returns the id of the Requirement n counts to, R1 through R4 in turn.
func promptSizeRequirementID(n int64) string {
	return fmt.Sprintf("R%d", n%promptSizeRequirementCount+1)
}

// promptSizeRequirement returns a Requirement of the fixture with id and text, whose evidence item is a test.
func promptSizeRequirement(id, text string) store.EpicRequirement {
	return store.EpicRequirement{
		ID: id, Text: text, Evidence: "prompt size test", Kind: "test",
		Item: evidence.Item{
			Kind: evidence.KindTest, Capture: []string{"go", "test", "./internal/planner/"},
			Tests: []string{"internal/planner/prompt_size_test.go"}, Limit: 120,
			Conclusion: "the prompt size test passes", Judge: "haiku-high-ro",
		},
	}
}

// promptSizeContract returns the contract of the fixture epic, with four Requirements, one failure, one gate of each
// kind and the sections a contract fills, and no decisions, turns or tasks.
func promptSizeContract() store.EpicRow {
	return store.EpicRow{
		ID: "epic-p7q2", Title: "Keep the planner prompt small", Approved: "approved text",
		Intent:      "the planner reads a summary of the epic and asks for detail by part",
		Care:        "serious",
		Approach:    "summarize the prompt's tasks and decisions, and name the read command",
		ReleaseThen: "release it", Checks: "just check",
		Failures: []store.EpicFailure{
			{
				ID: "F1", Text: "a summary hides a deliverable the planner needs", Rating: "serious",
				Response: "prevent: R2",
			},
		},
		Requirements: []store.EpicRequirement{
			promptSizeRequirement("R1", "a turn sees each task as a summary unless a trigger names it"),
			promptSizeRequirement("R2", "a summary keeps the failure's first line and the needed files"),
			promptSizeRequirement("R3", "a decision older than the latest deciding turn is cut to its first sentence"),
			promptSizeRequirement("R4", "the read command names the epic and every part"),
		},
		Gates: []store.EpicGate{
			{
				Name: "check", Argv: []string{"just", "check"}, Limit: 600,
				Needs: []store.EpicGateNeed{{Pool: "gate", Count: 1}},
			},
			{Name: "review", Agent: "reviewer", Must: []string{"tests pass"}, MustNot: []string{"a skipped test"}},
		},
		DoneGates:   []string{"check"},
		ReviewGates: []string{"review"},
		Premises:    []store.EpicPremise{{ID: "P1", Text: "the step runs in process", IfWrong: "use the service"}},
		Exclusions:  []string{"no routing"},
		Alternatives: []store.EpicAlternative{{
			Alternative: "an MCP tool", WhyNot: "no MCP tools", Reconsider: "a rule needs it",
		}},
		ReleaseSteps: []store.EpicReleaseStep{{Step: "push main", Target: "the repository", Undo: "delete the tag"}},
	}
}

// promptSizeEpic returns the fixture epic before any turn ran: its contract and its seed decisions, which have makers.
func promptSizeEpic() store.EpicRow {
	epic := promptSizeContract()
	for seq := int64(1); seq <= promptSizeSeedCount; seq++ {
		epic.Decisions = append(epic.Decisions, store.EpicDecision{
			Seq: seq, ID: fmt.Sprintf("D%d", seq), Text: promptSizeText(fmt.Sprintf("Seed %d", seq), 120),
			Maker: store.Maker{By: store.MakerOwner},
		})
	}
	return epic
}

// promptSizeTurns returns turns 1 to 6, each succeeded with promptSizeDecisionsPerTurn decisions of
// promptSizeDecisionSize bytes and having seen every seed, then turn 7, which runs.
func promptSizeTurns() []store.EpicTurn {
	turns := make([]store.EpicTurn, 0, promptSizeTurnCount+1)
	for seq := int64(1); seq <= promptSizeTurnCount; seq++ {
		decisions := make([]string, 0, promptSizeDecisionsPerTurn)
		for i := 1; i <= promptSizeDecisionsPerTurn; i++ {
			label := fmt.Sprintf("Turn %d decision %d", seq, i)
			decisions = append(decisions, promptSizeText(label, promptSizeDecisionSize))
		}
		turns = append(turns, store.EpicTurn{
			Seq: seq, State: turnSucceeded, Seen: promptSizeSeedCount, Decisions: decisions,
		})
	}
	return append(turns, store.EpicTurn{Seq: promptSizeTurn, State: "running"})
}

// promptSizeTaskSize returns the size in bytes of task seq's deliverable and evidence conclusion together: spread from
// promptSizeMinSize for task 1 to promptSizeMaxSize for the last task, except the failed task, which is
// promptSizeFailedSize.
func promptSizeTaskSize(seq int64) int {
	if seq == promptSizeFailedSeq {
		return promptSizeFailedSize
	}
	return promptSizeMinSize + int(seq-1)*(promptSizeMaxSize-promptSizeMinSize)/(promptSizeTaskCount-1)
}

// promptSizeTaskTurn returns the turn that planned task seq: the tasks spread evenly over turns 1 to 6.
func promptSizeTaskTurn(seq int64) int64 {
	return (seq-1)*promptSizeTurnCount/promptSizeTaskCount + 1
}

// promptSizeStatus returns the status of task seq: the failed task, the last two tasks open, and the rest done.
func promptSizeStatus(seq int64) string {
	switch {
	case seq == promptSizeFailedSeq:
		return taskFailed
	case seq >= promptSizeTaskCount-1:
		return "open"
	default:
		return taskDone
	}
}

// promptSizeTask returns task seq of the fixture: a few files and Requirement ids, rungs on every fourth task, and for
// the failed task a multi-line failure and the file it needs.
func promptSizeTask(seq int64) store.EpicTask {
	size := promptSizeTaskSize(seq)
	conclusion := size / promptSizeConclusionShare
	task := store.EpicTask{
		Seq: seq, Turn: promptSizeTaskTurn(seq), Title: fmt.Sprintf("fixture task %d", seq),
		Deliverable: promptSizeText(fmt.Sprintf("Task %d deliverable", seq), size-conclusion),
		Files: []string{
			fmt.Sprintf("internal/fixture/part%d/one.go", seq),
			fmt.Sprintf("internal/fixture/part%d/two.go", seq),
			fmt.Sprintf("internal/fixture/part%d/three.go", seq),
		},
		Requirements: []string{promptSizeRequirementID(seq), promptSizeRequirementID(seq + 1)},
		Contributes:  []string{promptSizeRequirementID(seq + 2)},
		Gate:         "check",
		Evidence: evidence.Item{
			Capture: []string{"scripts/check"}, Limit: 120, Judge: "haiku-high-ro",
			Conclusion: promptSizeText(fmt.Sprintf("Task %d conclusion", seq), conclusion),
		},
		Status: promptSizeStatus(seq),
	}
	if seq%promptSizeRungEvery == 0 {
		task.Rungs = []store.TaskRung{
			{Rung: 1, Kind: store.RungAttempt},
			{
				Rung: 2, Kind: store.RungAttempt, MovedBy: store.MovedByUnit, Cause: "the unit check failed",
				Outcome: "passed", Reason: "the second attempt passed",
			},
		}
	}
	if task.Status == taskFailed {
		task.Build = store.TaskBuild{Failure: promptSizeFailure}
		task.NeedsFiles = []string{"internal/fixture/shared.go"}
	}
	return task
}

// promptSizeEpicAtTurn7 returns the fixture epic as turn 7 reads it: the contract, the seed decisions, turns 1 to 7 and
// the tasks of turns 1 to 6.
func promptSizeEpicAtTurn7() store.EpicRow {
	epic := promptSizeEpic()
	epic.Turns = promptSizeTurns()
	for seq := int64(1); seq <= promptSizeTaskCount; seq++ {
		epic.Tasks = append(epic.Tasks, promptSizeTask(seq))
	}
	return epic
}

// promptSizeFullRender returns the render the planner prompt had before the summary: the role text, the contract, the
// decisions before turn 7 in full, and every task in full, joined by newlines.
func promptSizeFullRender(t *testing.T, epic store.EpicRow) string {
	t.Helper()
	contract, err := ContractLines(epic)
	if err != nil {
		t.Fatalf("contract of the fixture: %v", err)
	}
	tasks, err := taskLines(epic.Tasks)
	if err != nil {
		t.Fatalf("tasks of the fixture: %v", err)
	}
	lines := append([]string{strings.TrimRight(role, "\n")}, contract...)
	lines = append(lines, DecisionLines(epic, promptSizeTurn)...)
	lines = append(lines, tasks...)
	return strings.Join(lines, "\n")
}

// TestPromptSize: the full render of the fixture epic passes promptSizeOldFullBytes, and the turn-7 prompt, which names
// the failed task T17, is within promptSizeGrowthBound bytes of the turn-1 prompt, which has no tasks and no turns.
func TestPromptSize(t *testing.T) {
	t.Parallel()
	epic := promptSizeEpicAtTurn7()
	full := promptSizeFullRender(t, epic)
	if len(full) <= promptSizeOldFullBytes {
		t.Fatalf("old full render is %d bytes, want more than %d", len(full), promptSizeOldFullBytes)
	}
	turn1 := promptOf(t, promptSizeEpic(), 1, []store.EpicTrigger{
		{ID: "created", Cause: "epic created", State: "claimed", Turn: 1},
	})
	turn7 := promptOf(t, epic, promptSizeTurn, []store.EpicTrigger{
		{ID: "T17-failed", Cause: promptSizeFailureCause, State: "claimed", Turn: promptSizeTurn},
	})
	growth := len(turn7) - len(turn1)
	t.Logf("old full render %d bytes, turn 1 prompt %d bytes, turn 7 prompt %d bytes, growth %d bytes",
		len(full), len(turn1), len(turn7), growth)
	if growth > promptSizeGrowthBound {
		t.Errorf("turn 7 prompt is %d bytes longer than turn 1's, want at most %d", growth, promptSizeGrowthBound)
	}
}
