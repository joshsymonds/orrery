package server

import (
	"context"
	"slices"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// The states of a requirement's work, as ContractRequirementWork.state names them.
const (
	requirementNotStarted = "not started"
	requirementWorked     = "being worked"
	requirementProven     = "proven"
	requirementFailed     = "failed"
)

// runPhaseGreen is the phase of the evidence run that proves a requirement, as EvidenceRun.phase names it.
const runPhaseGreen = "green"

// contractWork answers the work of each requirement of the contract listed as listing, in contract order, from versions,
// the contract's versions oldest first. A contract that is not accepted has no work. An accepted contract's work is
// read from the stored epic of its id, which takes the contract's id: its requirements are the epic's, and its tasks
// and runs are read as Epics.Get reads them. An accepted contract whose epic orreryd does not hold yet has each
// requirement of its newest version not started, with no tasks. A failed epic read answers as Epics.Get answers it.
func (c contractsServer) contractWork(
	ctx context.Context,
	listing store.ContractListing,
	versions []*orreryv1.ContractVersion,
) ([]*orreryv1.ContractRequirementWork, error) {
	if listing.State != store.ContractAccepted {
		return nil, nil
	}
	reads := epicsServer{store: c.store, evidence: c.evidence, timeout: c.timeout, clock: c.clock}
	stored, inStore, err := reads.getStored(ctx, listing.ID)
	if err != nil {
		return nil, err
	}
	if !inStore {
		var newest []*orreryv1.EpicRequirement
		if n := len(versions); n > 0 {
			newest = versions[n-1].GetRequirements()
		}
		return requirementsWork(newest, nil, false), nil
	}
	runs := reads.listEvidence(ctx, listing.ID)
	requirements := epicRequirementMessages(stored.GetContract().GetRequirements(), runs)
	builds := epicBuildTaskMessages(stored.GetTasks())
	ended := store.EndedPhase(stored.GetPhase())
	// The steps are not read. A task's place comes from its status and stage, not its steps, and an ended epic fails
	// each requirement no run proves whatever its open tasks' tones, so the steps change no requirement's state.
	epicBuildLifecycles(builds, stored.GetTasks(), nil, ended, stored.GetPaused() || stored.GetParked() != "")
	return requirementsWork(requirements, builds, ended), nil
}

// requirementsWork answers the work of each requirement in order, from the build tasks builds, which name the
// requirements they cover, and whether the epic's phase has ended.
func requirementsWork(
	requirements []*orreryv1.EpicRequirement,
	builds []*orreryv1.EpicBuildTask,
	ended bool,
) []*orreryv1.ContractRequirementWork {
	out := make([]*orreryv1.ContractRequirementWork, 0, len(requirements))
	for _, req := range requirements {
		out = append(out, requirementWork(req, builds, ended))
	}
	return out
}

// requirementWork answers the work of req: each build task that names req, as a ContractRequirementTask, and its state.
// Its state is proven when a green run of its runs passed, with that run as proof. It is failed when no run passed and
// either a task naming it is a gap, whose tone is bad, or the epic's phase has ended. It is being worked when a task
// naming it has left planned, and not started otherwise.
func requirementWork(
	req *orreryv1.EpicRequirement,
	builds []*orreryv1.EpicBuildTask,
	ended bool,
) *orreryv1.ContractRequirementWork {
	work := &orreryv1.ContractRequirementWork{Id: req.GetId(), Text: req.GetText()}
	var gap, worked bool
	for _, build := range builds {
		if !slices.Contains(build.GetRequirements(), req.GetId()) {
			continue
		}
		work.Tasks = append(work.Tasks, &orreryv1.ContractRequirementTask{
			Id: build.GetId(), Title: build.GetTitle(), Status: build.GetStatus(), Place: build.GetPlace(),
		})
		gap = gap || build.GetTone() == toneBad
		worked = worked || build.GetPlace() != placePlanned
	}
	switch proof := passedGreenRun(req.GetRuns()); {
	case proof != nil:
		work.State, work.Proof = requirementProven, proof
	case gap || ended:
		work.State = requirementFailed
	case worked:
		work.State = requirementWorked
	default:
		work.State = requirementNotStarted
	}
	return work
}

// passedGreenRun returns the first run of runs that is green and passed, or nil when none is.
func passedGreenRun(runs []*orreryv1.EvidenceRun) *orreryv1.EvidenceRun {
	for _, run := range runs {
		if run.GetPhase() == runPhaseGreen && run.GetPassed() {
			return run
		}
	}
	return nil
}
