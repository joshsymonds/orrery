package enginehost

import (
	"context"
	"fmt"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/reviewers"
)

// epicCreateSource names the contract's layer in the errors its reviewers raise.
const epicCreateSource = "epic_create"

// epicReviewers returns the reviewers an epic runs. It merges three layers by name: orrery's defaults, the repository's
// project file, then the contract's reviewers. It then generates the contract's assertions from its exclusions. An
// entry no layer can hold is refused with a *reviewers.Error naming its file or epic_create, its index and its key.
// getenv reads the environment for the machine's pool file, as checkEpicGateRefs does. The repository's project file is
// read through host.
func epicReviewers(
	ctx context.Context,
	host hostexec.Host,
	contract *v1.EpicContract,
	getenv func(string) string,
) ([]reviewers.Reviewer, error) {
	project, err := reviewers.Project(ctx, host, contract.GetRepository(), getenv)
	if err != nil {
		return nil, fmt.Errorf("read the reviewers of %s: %w", contract.GetRepository(), err)
	}
	layer := make([]reviewers.Entry, 0, len(contract.GetReviewers()))
	for i, rev := range contract.GetReviewers() {
		layer = append(layer, epicReviewerEntry(rev, i))
	}
	merged, err := reviewers.Merge(reviewers.Defaults(), project, layer)
	if err != nil {
		return nil, fmt.Errorf("merge the reviewers of %s: %w", contract.GetRepository(), err)
	}
	return reviewers.Generate(merged, contract.GetExclusions()), nil
}

// epicReviewerEntry returns the contract's reviewer at index as the entry of the epic layer. A nil reviewer is an
// empty one, which Merge refuses by name. An empty repeated field is unset in this layer, so it is nil.
func epicReviewerEntry(rev *v1.EpicReviewer, index int) reviewers.Entry {
	if rev == nil {
		rev = &v1.EpicReviewer{}
	}
	return reviewers.Entry{
		Source:  epicCreateSource,
		Index:   index,
		Name:    rev.GetName(),
		Enabled: rev.Enabled,
		Stage:   epicList(rev.GetStage()),
		Must:    epicList(rev.GetMust()),
		MustNot: epicList(rev.GetMustNot()),
		Prompt:  rev.Prompt,
		Agent:   rev.Agent,
		Model:   rev.Model,
		Tools:   epicList(rev.GetTools()),
		Effort:  rev.Effort,
		Limit:   rev.Limit,
		Verify:  rev.Verify,
	}
}

// epicList returns list, or nil when list is empty.
func epicList(list []string) []string {
	if len(list) == 0 {
		return nil
	}
	return list
}

// epicReviewerMessage returns rev as the EpicReviewer message the stored epic carries. Every key rev holds is set,
// except that a string rev leaves empty is unset, as an agent reviewer's model is.
func epicReviewerMessage(rev reviewers.Reviewer) *v1.EpicReviewer {
	msg := &v1.EpicReviewer{
		Name:    rev.Name,
		Enabled: &rev.Enabled,
		Stage:   rev.Stage,
		Must:    rev.Must,
		MustNot: rev.MustNot,
		Tools:   rev.Tools,
		Limit:   &rev.Limit,
		Verify:  &rev.Verify,
	}
	if rev.Prompt != "" {
		msg.Prompt = &rev.Prompt
	}
	if rev.Agent != "" {
		msg.Agent = &rev.Agent
	}
	if rev.Model != "" {
		msg.Model = &rev.Model
	}
	if rev.Effort != "" {
		msg.Effort = &rev.Effort
	}
	return msg
}
