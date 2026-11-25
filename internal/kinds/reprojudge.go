package kinds

import (
	"fmt"
	"slices"
	"strconv"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// The modes of an ActReproJudge action, its first item. ReproJudgeStart starts a judge's step, and ReproJudgeRecord
// records the ruling of a judge whose step has ended. The items after the mode are the Requirement, the id of the
// reproduction check the judge rules on, and the judge's number from 0.
const (
	ReproJudgeStart  = "start"
	ReproJudgeRecord = "record"
)

// ReproJudgeID returns the step id of judge number judge, from 0, of the reproduction of Requirement id that run
// decides on: NodeWorkID of the run's id and attempt with the work `repro judge <id> <judge>`. Each judge has its own
// id, so a second judge after a first that gave no verdict starts under its own, and the reproduction does not run
// again.
func ReproJudgeID(run store.NodeRun, id string, judge int64) string {
	return store.NodeWorkID(run.ID, run.Attempt, fmt.Sprintf("repro judge %s %d", id, judge))
}

// reproJudges decides the judges of the reproductions run decides on, once every reproduction has ended. Whether a
// reproduction that ran and exited 0 without timing out (store.PassedOnBase) contradicts red is its judge's ruling,
// never its exit: a refactor, rename or removal task's tests are meant to pass on base code. Any other reproduction
// needs no judge, and neither does one its rulings settle (store.SettledRuling). For each other reproduction, in
// contract order, the judge numbered by how many rulings it has decides: a judge with no step starts
// (ReproJudgeStart), and one whose step has ended is recorded (ReproJudgeRecord), so a first judge that gave no
// verdict is recorded and the second starts. A judge whose step runs makes the run wait once no other judge needs
// either. A ruling adds no cause, so it returns settled once every such reproduction is settled.
func reproJudges(run store.NodeRun, rows Rows) (Action, bool) {
	running := false
	for _, requirement := range testRequirements(rows.Epic.Requirements) {
		check, _, _ := reproCheck(rows.Checks, run, requirement.ID)
		if !store.PassedOnBase(check) {
			continue
		}
		rulings := store.CheckRulings(rows.Rulings, check.ID)
		if _, settled := store.SettledRuling(rulings); settled {
			continue
		}
		judge := int64(len(rulings))
		id := ReproJudgeID(run, requirement.ID, judge)
		step, started := firstMatch(rows.Steps, func(row store.StepRow) bool { return row.ID == id })
		if started && step.End == "" {
			running = true
			continue
		}
		mode := ReproJudgeStart
		if started {
			mode = ReproJudgeRecord
		}
		return Action{
			Kind: ActReproJudge, IDs: []string{id},
			Items: []string{mode, requirement.ID, check.ID, strconv.FormatInt(judge, 10)},
		}, false
	}
	if running {
		return Action{Kind: ActNone}, false
	}
	return Action{}, true
}

// Disagreement returns the line that names test-item Requirement id when a judge ruled that one of its reproductions at
// the epic's base contradicts red: of the reproductions baseReproductions gives, each read by the ruling that settles it
// (store.SettledRuling), the last whose ruling is evidence.VerdictFails. The line names the Requirement, the epic's base
// and that judge's reason. A reproduction's exit is never read, so a reproduction with no ruling, one ruled not to
// contradict red, and one whose judges gave no verdict give nothing. It returns "" for any other Requirement too.
func Disagreement(rows Rows, id string) string {
	if !slices.ContainsFunc(rows.Epic.Requirements, func(requirement store.EpicRequirement) bool {
		return requirement.ID == id && isTestItem(requirement)
	}) {
		return ""
	}
	line := ""
	for _, check := range baseReproductions(rows, id) {
		ruling, settled := store.SettledRuling(store.CheckRulings(rows.Rulings, check.ID))
		if !settled || ruling.Verdict != evidence.VerdictFails {
			continue
		}
		line = fmt.Sprintf("%s: the judge ruled that its reproduction at the epic's base %s contradicts red",
			id, rows.Epic.Base)
		if ruling.Reason != "" {
			line += ": " + ruling.Reason
		}
	}
	return line
}
