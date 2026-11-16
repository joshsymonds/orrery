package interpret

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/joshsymonds/orrery/internal/enginehost"
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

// Start returns the read-only step of the epic-stage reviewer name, run at revision of epic's repository over the
// change from base. The step carries the reviewer's agent gate and its inline agents, and its prompt is the epic review
// prompt, headed by the reviewer's own prompt when it is an agent reviewer that sets one. A name that is not an enabled
// epic-stage reviewer of epic is an error.
func (EpicReviewers) Start(
	_ context.Context, epic store.EpicRow, name, base, revision string,
) (enginehost.StepStart, error) {
	stage := reviewers.ForStage(epic.Reviewers, reviewers.StageEpic)
	i := slices.IndexFunc(stage, func(rev reviewers.Reviewer) bool { return rev.Name == name })
	if i < 0 {
		return enginehost.StepStart{}, fmt.Errorf(
			"review %s of epic %s is not an enabled epic-stage reviewer", name, epic.ID)
	}
	rev := stage[i]
	agents, err := rev.Agents()
	if err != nil {
		return enginehost.StepStart{}, fmt.Errorf("review %s: %w", name, err)
	}
	gate := rev.Gate()
	prompt := epicStageReviewPrompt(epic, base, revision)
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

// epicStageReviewPrompt renders the prompt of an epic-stage review step: the epic and every Requirement it declares,
// its Must Not Ship entries, and the change from base to revision.
func epicStageReviewPrompt(epic store.EpicRow, base, revision string) string {
	requirements := make([]string, len(epic.Requirements))
	for i, requirement := range epic.Requirements {
		requirements[i] = requirement.ID + ": " + requirement.Text
	}
	change := fmt.Sprintf(
		"The change under review is %s..%s; this worktree is checked out at %s. Inspect it with `git diff %s %s`.",
		base, revision, revision, base, revision)
	return strings.Join(slices.Concat(
		[]string{fmt.Sprintf("Epic %s (%s)", epic.ID, epic.Title), "Requirements:"},
		epicStageBullets(requirements),
		[]string{"Must Not Ship:"},
		epicStageBullets(epic.Exclusions),
		[]string{change},
	), "\n")
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
