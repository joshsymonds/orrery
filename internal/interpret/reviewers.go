package interpret

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/joshsymonds/orrery/internal/build"
	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/reviewers"
	"github.com/joshsymonds/orrery/internal/store"
)

// EpicReviewers is the production Reviewers: the epic-stage reviewers an epic's merged reviewers enable, and the
// read-only step of one of them at a revision of the epic's repository.
type EpicReviewers struct{}

// NewEpicReviewers returns the production Reviewers.
func NewEpicReviewers() *EpicReviewers {
	return &EpicReviewers{}
}

// Compile-time check that EpicReviewers satisfies the Reviewers the Runner takes.
var _ Reviewers = (*EpicReviewers)(nil)

// Epic returns the enabled epic-stage reviewers of epic in their merged order, each with its verify switch.
func (EpicReviewers) Epic(epic store.EpicRow) []kinds.Reviewer {
	stage := reviewers.ForStage(epic.Reviewers, reviewers.StageEpic)
	out := make([]kinds.Reviewer, 0, len(stage))
	for _, rev := range stage {
		out = append(out, kinds.Reviewer{Name: rev.Name, Verify: rev.Verify})
	}
	return out
}

// Start returns the read-only step of the enabled reviewer name, run at revision of epic's repository over the change from
// base. An epic-stage reviewer's step carries the reviewer's agent gate and its inline agents, and its prompt is the epic
// review prompt, headed by the reviewer's own prompt when it is an agent reviewer that sets one. The test_parsimony
// reviewer's prompt ends with the Test cost section of doneGates, the checks of the Done gates that ran at revision,
// compared with baseCost, the epic's cost checks at its base. A release-stage reviewer's step is the same, with the
// release review prompt, which names the promise and the steps of the release. A reviewer enabled at both stages is
// started as an epic-stage reviewer. A name that is not an enabled reviewer of either stage is an error.
func (EpicReviewers) Start(
	_ context.Context, epic store.EpicRow, name, base, revision string, doneGates, baseCost []store.NodeCheck,
) (enginehost.StepStart, error) {
	var (
		rev    reviewers.Reviewer
		prompt string
	)
	if epicRev, inEpic := namedReviewer(reviewers.ForStage(epic.Reviewers, reviewers.StageEpic), name); inEpic {
		rev, prompt = epicRev, epicStageReviewPrompt(epic, base, revision)
	} else if releaseRev, inRelease := namedReviewer(
		reviewers.ForStage(epic.Reviewers, reviewers.StageRelease), name,
	); inRelease {
		rev, prompt = releaseRev, releaseReviewPrompt(epic, base, revision)
	} else {
		return enginehost.StepStart{}, fmt.Errorf(
			"review %s of epic %s is not an enabled epic-stage or release-stage reviewer", name, epic.ID)
	}
	agents, err := rev.Agents()
	if err != nil {
		return enginehost.StepStart{}, fmt.Errorf("review %s: %w", name, err)
	}
	gate := rev.Gate()
	if name == reviewers.TestParsimony {
		costs := reviewers.Costs(checkOutputs(baseCost)...)
		prompt += "\n" + strings.Join(reviewers.TestCost(costs, checkOutputs(doneGates)...), "\n")
	}
	if rev.Agent != "" && rev.Prompt != "" {
		prompt = rev.Prompt + "\n\n" + prompt
	}
	return enginehost.StepStart{
		ReadOnly:  true,
		Workspace: epic.Repository,
		Revision:  revision,
		AgentGate: &gate,
		Agents:    agents,
		Prompt:    prompt,
		Epic:      epic.ID,
	}, nil
}

// namedReviewer returns the reviewer named name among rs, and whether there is one.
func namedReviewer(rs []reviewers.Reviewer, name string) (reviewers.Reviewer, bool) {
	i := slices.IndexFunc(rs, func(rev reviewers.Reviewer) bool { return rev.Name == name })
	if i < 0 {
		return reviewers.Reviewer{}, false
	}
	return rs[i], true
}

// reviewedChange states the change from base to revision under review, which the worktree is checked out at revision.
func reviewedChange(base, revision string) string {
	return fmt.Sprintf(
		"The change under review is %s..%s; this worktree is checked out at %s. Inspect it with `git diff %s %s`.",
		base, revision, revision, base, revision)
}

// releaseReviewPrompt renders the prompt of a release review step: the epic, the promise its release makes, each release
// step with its target and undo, its Must Not Ship entries, and the change from base to revision.
func releaseReviewPrompt(epic store.EpicRow, base, revision string) string {
	steps := make([]string, len(epic.ReleaseSteps))
	for i, step := range epic.ReleaseSteps {
		steps[i] = fmt.Sprintf("%s (target %s, undo %s)", step.Step, step.Target, step.Undo)
	}
	return strings.Join(slices.Concat(
		[]string{
			fmt.Sprintf("Release of epic %s (%s)", epic.ID, epic.Title),
			"The release promises:",
			epic.ReleaseThen,
			"Release steps:",
		},
		epicStageBullets(steps),
		[]string{"Must Not Ship:"},
		epicStageBullets(epic.Exclusions),
		[]string{reviewedChange(base, revision)},
	), "\n")
}

// epicStageReviewPrompt renders the prompt of an epic-stage review step: the epic and every Requirement it declares,
// its Must Not Ship entries, the amendments of its Requirements' evidence items when it has any (see amendmentBullets),
// the gate-needed changes its tasks declared under the heading that says when each is in scope
// (build.EpicGateChangeItems), and the change from base to revision.
func epicStageReviewPrompt(epic store.EpicRow, base, revision string) string {
	requirements := make([]string, len(epic.Requirements))
	for i, requirement := range epic.Requirements {
		requirements[i] = requirement.ID + ": " + requirement.Text
	}
	var amendments []string
	if len(epic.Amendments) > 0 {
		amendments = append([]string{"Evidence amendments:"}, epicStageBullets(amendmentBullets(epic.Amendments))...)
	}
	return strings.Join(slices.Concat(
		[]string{fmt.Sprintf("Epic %s (%s)", epic.ID, epic.Title), "Requirements:"},
		epicStageBullets(requirements),
		[]string{"Must Not Ship:"},
		epicStageBullets(epic.Exclusions),
		amendments,
		[]string{build.EpicDeclaredHeading},
		epicStageBullets(build.EpicGateChangeItems(epic.Tasks)),
		[]string{reviewedChange(base, revision)},
	), "\n")
}

// checkOutputs returns the kept stdout and then stderr of each of checks, in order.
func checkOutputs(checks []store.NodeCheck) []string {
	outputs := make([]string, 0, 2*len(checks))
	for _, check := range checks {
		outputs = append(outputs, check.Stdout, check.Stderr)
	}
	return outputs
}

// amendmentBullets returns one line per amendment of a Requirement's evidence item, so a reviewer reads how the evidence
// of each Requirement changed while the epic ran: `A<seq> <Requirement> by <amender>, <state>: <reason>; prior item:
// <json>; new item: <json>`, with `; refused: <refusal>` after a refused one.
func amendmentBullets(amendments []store.EvidenceAmendment) []string {
	lines := make([]string, 0, len(amendments))
	for _, amended := range amendments {
		line := fmt.Sprintf("A%d %s by %s, %s: %s; prior item: %s; new item: %s", amended.Seq, amended.Requirement,
			amended.Amender, amended.State, amended.Reason, compactItem(amended.Prior), compactItem(amended.Item))
		if amended.State == store.AmendmentRefused {
			line += "; refused: " + amended.Refusal
		}
		lines = append(lines, line)
	}
	return lines
}

// compactItem returns item as compact JSON, or the encoding error's text.
func compactItem(item evidence.Item) string {
	encoded, err := json.Marshal(item)
	if err != nil {
		return "unencodable item: " + err.Error()
	}
	return string(encoded)
}

// epicStageBullets prefixes each item with "- ", or returns the single line "- none" when there are no items.
func epicStageBullets(items []string) []string {
	if len(items) == 0 {
		return []string{"- none"}
	}
	lines := make([]string, len(items))
	for i, item := range items {
		lines[i] = "- " + item
	}
	return lines
}
