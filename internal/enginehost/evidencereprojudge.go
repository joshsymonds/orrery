package enginehost

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/taskspec"
	"github.com/joshsymonds/orrery/internal/testfacts"
)

// ReproJudgeGateName is the name of the agent gate that judges whether a test item's reproduction contradicts its red.
const ReproJudgeGateName = "repro_judge"

// reproJudgeAssertion is the one must assertion the reproduction judge checks. It holds when the reproduction does not
// contradict red and fails when it does, so a fails verdict's finding says what the tests fail to prove.
const reproJudgeAssertion = "This reproduction does not contradict red: the tests it ran prove the change their " +
	"tasks claim, although they passed on base code without the rest of that change."

// ReproJudgeGate returns the agent gate that judges a reproduction: agent, the judge of the reproduction's evidence item,
// checks the one must assertion reproJudgeAssertion within the evidence judge's limit, as judgeGate gives an evidence
// run's gate.
func ReproJudgeGate(agent string) gates.Gate {
	gate := judgeGate(evidence.Item{Judge: agent, Conclusion: reproJudgeAssertion})
	gate.Name = ReproJudgeGateName
	return gate
}

// ReproJudgeInput is what the judge of a reproduction reads. Tasks are the tasks whose change the reproduction's tests
// claim to prove, Conclusion is their evidence item's conclusion, and Red is the item's red run at the base, nil when
// none is kept. Repro is the reproduction: the item's capture run at Base with the test files of Commit laid over it.
// DiffStat is git's diff stat from Base to Commit.
type ReproJudgeInput struct {
	Tasks      []store.EpicTask
	Conclusion string
	Red        *store.EvidenceRun
	Repro      store.NodeCheck
	Base       string
	Commit     string
	DiffStat   string
}

// ReproJudgePrompt returns the prompt of the judge of in's reproduction, which ran and exited 0 without timing out. It
// says what the reproduction ran and asks whether it contradicts red: whether the tests fail to prove the change the
// tasks claim. It says that tests meant to pass on base code, as a refactor, rename or removal task's are, do not
// contradict red by passing there. Then it names each task with its title, the lines taskspec.Lines gives it and its
// deliverable, the conclusion, red's verdict and its judge's reason, the reproduction's command as a JSON array of its
// argv, its revision, exit and timed out, the stdout and stderr tails the check keeps, the test facts testfacts reads
// from those tails under judgeFactsHeading, as the evidence judge is given them, and the diff stat. It returns an error
// when the argv cannot be encoded.
func ReproJudgePrompt(in ReproJudgeInput) (string, error) {
	argv, err := json.Marshal(in.Repro.Argv)
	if err != nil {
		return "", fmt.Errorf("command of reproduction %s: %w", in.Repro.ID, err)
	}
	lines := []string{
		"Read only what is below. Repository files may be read only as reference for it; this worktree is at the " +
			"commit whose tests the reproduction ran.",
		fmt.Sprintf("The reproduction ran the evidence item's capture on the base %s with only the test files of "+
			"the commit %s laid over it, so the tests ran on base code without the rest of the change. "+
			"It exited 0 and did not time out: they passed there.", in.Base, in.Commit),
		"Decide whether this reproduction contradicts red: do the tests fail to prove the change the tasks below " +
			"claim? Tests that pass on base code although they are meant to show what the change adds contradict " +
			"red. Tests that are meant to pass on base code, as a refactor, rename or removal task's tests are, do " +
			"not contradict red by passing there.",
		"The assertion holds when the reproduction does not contradict red, and fails when it does. When it fails, " +
			"say in the finding what the tests fail to prove.",
		"",
	}
	for _, task := range in.Tasks {
		lines = append(lines, fmt.Sprintf("Task T%d: %s", task.Seq, task.Title))
		lines = append(lines, taskspec.Lines(task)...)
		lines = append(lines, "Deliverable: "+task.Deliverable)
	}
	lines = append(lines,
		"Conclusion: "+in.Conclusion,
		reproRedLine(in.Red),
		"Reproduction command: "+string(argv),
		"Reproduction revision: "+in.Repro.Revision,
		fmt.Sprintf("Exit: %d", in.Repro.Exit),
		fmt.Sprintf("Timed out: %t", in.Repro.TimedOut),
	)
	lines = append(lines, reproLogLines("Stdout", in.Repro.Stdout)...)
	lines = append(lines, reproLogLines("Stderr", in.Repro.Stderr)...)
	lines = append(lines, judgeFactsHeading)
	for _, fact := range testfacts.Read(in.Repro.Stdout, in.Repro.Stderr).Lines() {
		lines = append(lines, "  "+fact)
	}
	lines = append(lines, fmt.Sprintf("Diff stat (git diff --stat %s %s):", in.Base, in.Commit))
	if strings.TrimSpace(in.DiffStat) != "" {
		lines = append(lines, reproIndent(in.DiffStat))
	} else {
		lines = append(lines, "    (no change)")
	}
	return strings.Join(lines, "\n"), nil
}

// ReproJudgeVerdict reads the verdict and reason of a reproduction judge's step from its row, as judgeVerdict reads an
// evidence judge's: holds is a reproduction that does not contradict red, fails one that does, and an empty verdict is
// no verdict, whose reason is how the step ended.
func ReproJudgeVerdict(row store.StepRow) (verdict, reason string) {
	return judgeVerdict(row)
}

// reproRedLine returns the line that names red's verdict at its revision and its judge's reason, or says that no red
// run is kept when red is nil.
func reproRedLine(red *store.EvidenceRun) string {
	if red == nil {
		return "Red: no red run of the item is kept."
	}
	verdict := red.Verdict
	if verdict == "" {
		verdict = "no verdict"
	}
	return fmt.Sprintf("Red at the base %s: judged %s against the conclusion; the judge's reason: %s",
		red.Revision, verdict, red.JudgeReason)
}

// reproLogLines returns the lines that show a reproduction's log tail under label, indented, or say it is empty.
func reproLogLines(label, tail string) []string {
	if strings.TrimSpace(tail) == "" {
		return []string{label + ": empty"}
	}
	return []string{
		fmt.Sprintf("%s, its last %d bytes as orrery keeps them:", label, store.NodeOutputMax),
		reproIndent(tail),
	}
}

// reproIndent returns each line of text indented by four spaces, its trailing newlines dropped.
func reproIndent(text string) string {
	return "    " + strings.ReplaceAll(strings.TrimRight(text, "\n"), "\n", "\n    ")
}
