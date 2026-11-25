package enginehost

import (
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/releasestep"
	"github.com/joshsymonds/orrery/internal/store"
)

// storeReleaseStep returns the stored form of a contract's release step: its text, target and undo, and its kind with
// the kind's parameters.
func storeReleaseStep(step *v1.EpicReleaseStep) store.EpicReleaseStep {
	params := step.GetParams()
	return store.EpicReleaseStep{
		Step: step.GetStep(), Target: step.GetTarget(), Undo: step.GetUndo(), Kind: step.GetKind(),
		Params: releasestep.Params{
			Branch: params.GetBranch(), Prefix: params.GetPrefix(), Flake: params.GetFlake(), Input: params.GetInput(),
			URL: params.GetUrl(), Run: params.GetRun(), Units: params.GetUnits(),
		},
	}
}

// releaseStepMessage returns the contract form of a stored release step. A step that sets no parameter, which every step
// with no kind is, carries no params message.
func releaseStepMessage(step store.EpicReleaseStep) *v1.EpicReleaseStep {
	message := &v1.EpicReleaseStep{Step: step.Step, Target: step.Target, Undo: step.Undo, Kind: step.Kind}
	params := step.Params
	if params.Branch != "" || params.Prefix != "" || params.Flake != "" || params.Input != "" || params.URL != "" ||
		len(params.Run) > 0 || len(params.Units) > 0 {
		message.Params = &v1.EpicReleaseParams{
			Branch: params.Branch, Prefix: params.Prefix, Flake: params.Flake, Input: params.Input, Url: params.URL,
			Run: params.Run, Units: params.Units,
		}
	}
	return message
}
