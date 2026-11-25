package interpret

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/agents"
	"github.com/joshsymonds/orrery/internal/build"
	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// The kinds of the check nodes whose work the executor carries out, and the position base names in their settings. A
// position other than base is head.
const (
	kindGate     = "gate"
	kindCost     = "cost"
	kindCommand  = "command"
	kindEvidence = "evidence"
	kindOwned    = "owned"
	kindAgent    = "agent"
	atBase       = "base"
)

// stepPart is the part an agent run's step is stored under: NodeWorkID of the run and its attempt with stepPart. The
// store reads the step under the same part.
const stepPart = "step"

// kindRerun is the kind of the rerun run a conclusion starts for a rerun_review action: one reviewer's try chain walked
// again at the epic's stored base. Its work is a closure's, and its starts are a review's.
const kindRerun = "rerun"

// work carries out one work action of a check node run: the prepare, start, capture, repro, refs check or advance its
// kind asks for. An integrate run's prepare claims its batch, and its advance moves the epic's branch. A review, closure
// or rerun run's prepare and start are its own. The work of a release run is carried out by release.
func (r *Runner) work(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	if act.NodeKind == kindRelease {
		return r.release(ctx, epic, act, at)
	}
	switch act.Work {
	case kinds.ActPrepare:
		switch act.NodeKind {
		case kindIntegrate:
			return r.prepareBatch(ctx, epic, act)
		case reviewNodeKind:
			return r.prepareReview(ctx, epic, act, at)
		case closureNodeKind, kindRerun:
			return r.prepareClosure(ctx, epic, act)
		}
		return r.prepare(ctx, epic, act)
	case kinds.ActStart:
		return r.start(ctx, epic, act)
	case kinds.ActCapture:
		return r.capture(ctx, epic, act)
	case kinds.ActRepro:
		return r.repro(ctx, epic, act)
	case kinds.ActRefs:
		return r.checkRefs(ctx, epic, act, at)
	case kinds.ActAdvance:
		return r.advance(ctx, epic, act, at)
	case kinds.ActTaskIntegrator:
		return r.taskIntegrator(ctx, epic, act, at)
	default:
		return fmt.Errorf("work %s is not one the executor carries out", act.Work)
	}
}

// prepare records the revision a check node run's work runs at. A gate, command or evidence run also records the refs it
// is checked against, and an agent run none, since no ref compare applies to it.
func (r *Runner) prepare(ctx context.Context, epic store.EpicRow, act Action) error {
	revision, err := r.revisionOf(ctx, epic, act.Def.Settings.At)
	if err != nil {
		return err
	}
	var refs string
	if act.NodeKind != kindAgent {
		if refs, err = r.refSnapshot(ctx, epic); err != nil {
			return err
		}
	}
	if err = r.store.PrepareNode(ctx, epic.ID, act.Path, act.Run.Attempt, revision, refs); err != nil {
		return fmt.Errorf("prepare node %s: %w", act.Path, err)
	}
	return nil
}

// revisionOf returns the revision a node names with at: the epic's base for base, and the head of the epic's branch for
// head.
func (r *Runner) revisionOf(ctx context.Context, epic store.EpicRow, at string) (string, error) {
	if at == atBase {
		if epic.Base == "" {
			return "", fmt.Errorf("epic %s has no base to run at", epic.ID)
		}
		return epic.Base, nil
	}
	return r.branchHead(ctx, epic)
}

// branchHead returns the commit of the epic's branch, read on the host the epic lives on. A branch that does not exist
// is an error.
func (r *Runner) branchHead(ctx context.Context, epic store.EpicRow) (string, error) {
	host, err := r.hostOf(epic)
	if err != nil {
		return "", fmt.Errorf("read branch of epic %s: %w", epic.ID, err)
	}
	tip, ok, err := build.BranchTip(ctx, host, epic.Repository, epic.ID)
	if err != nil {
		return "", fmt.Errorf("read branch of epic %s: %w", epic.ID, err)
	}
	if !ok {
		return "", fmt.Errorf("epic %s has no branch", epic.ID)
	}
	return tip, nil
}

// refSnapshot returns the refs of epic's repository and of its primary checkout's HEAD, as build.Snapshot writes them. It
// leaves out the branch of every epic that shares the repository, as build does, since an epic's branch moves with the
// epic's own work.
func (r *Runner) refSnapshot(ctx context.Context, epic store.EpicRow) (string, error) {
	epics, err := r.store.Epics(ctx)
	if err != nil {
		return "", fmt.Errorf("list epics: %w", err)
	}
	var exclude []string
	for _, other := range epics {
		if other.Repository == epic.Repository {
			exclude = append(exclude, build.BranchRef(other.ID))
		}
	}
	host, err := r.hostOf(epic)
	if err != nil {
		return "", fmt.Errorf("snapshot refs of epic %s: %w", epic.ID, err)
	}
	snapshot, err := build.Snapshot(ctx, host, epic.Repository, exclude)
	if err != nil {
		return "", fmt.Errorf("snapshot refs of epic %s: %w", epic.ID, err)
	}
	return snapshot, nil
}

// start carries out the start of a check node run's work: its check, its evidence runs or its agent step, or the record of
// an owned run's result. A review, closure or rerun run starts the units it asks for. The work of a kind whose service
// the runner was not given is an error, not a start.
func (r *Runner) start(ctx context.Context, epic store.EpicRow, act Action) error {
	switch act.NodeKind {
	case reviewNodeKind, closureNodeKind, kindRerun:
		if r.steps == nil {
			return missingService("steps")
		}
		if r.reviewers == nil {
			return missingService("reviewers")
		}
		return r.startReviewUnits(ctx, epic, act)
	case kindGate, kindCost, kindCommand, kindIntegrate:
		if r.checks == nil {
			return missingService("checks")
		}
		return r.startCheck(ctx, epic, act)
	case kindEvidence:
		if r.evidence == nil {
			return missingService("evidence")
		}
		return r.startEvidence(ctx, epic, act)
	case kindAgent:
		if r.steps == nil {
			return missingService("steps")
		}
		return r.startAgent(ctx, epic, act)
	case kindOwned:
		return r.recordOwned(ctx, epic, act)
	default:
		return fmt.Errorf("node kind %s has no work to start", act.NodeKind)
	}
}

// missingService is the error the work of a node returns when the runner was not given the service the work runs on.
func missingService(service string) error {
	return fmt.Errorf("the runner was not given a %s service", service)
}

// startCheck starts the check of a gate, cost, command or integrate run under the id its action names, at the run's
// revision. The check of an integrate run is one of its Done gates, which the action names, and the check of a cost
// run is the epic's first Done gate.
func (r *Runner) startCheck(ctx context.Context, epic store.EpicRow, act Action) error {
	check := store.NodeCheck{ID: act.IDs[0], Epic: epic.ID, Path: act.Path, Revision: act.Run.Revision}
	var err error
	switch act.NodeKind {
	case kindGate:
		check, err = r.withGate(ctx, epic, act.Def.Settings.Gate, check)
	case kindCost:
		check, err = r.withCostGate(ctx, epic, check)
	case kindIntegrate:
		check, err = r.withGate(ctx, epic, act.Items[0], check)
	default:
		check.Argv = act.Def.Settings.Argv
		check.Limit = int64(act.Def.Settings.Limit)
		check.Needs = commandNeeds(act.Def.Settings.Needs)
	}
	if err != nil {
		return err
	}
	if _, err = r.checks.Run(ctx, check); err != nil {
		return fmt.Errorf("run check %s: %w", check.ID, err)
	}
	return nil
}

// withCostGate fills check with the epic's first Done gate, which a cost run measures at the base. An epic with no Done
// gate has nothing to measure, so check ends with the reason it did not run.
func (r *Runner) withCostGate(ctx context.Context, epic store.EpicRow, check store.NodeCheck) (store.NodeCheck, error) {
	if len(epic.DoneGates) == 0 {
		check.NotRun = "no Done gate to measure at the base"
		return check, nil
	}
	return r.withGate(ctx, epic, epic.DoneGates[0], check)
}

// withGate fills check with the gate named name: its argv, limit and needs. A gate that does not resolve, and an agent
// gate, are not run, so check ends with the reason it did not run.
func (r *Runner) withGate(
	ctx context.Context, epic store.EpicRow, name string, check store.NodeCheck,
) (store.NodeCheck, error) {
	host, err := r.hostOf(epic)
	if err != nil {
		return store.NodeCheck{}, fmt.Errorf("check %s: %w", check.ID, err)
	}
	gate, err := build.ResolveGate(ctx, host, epic, name, r.getenv)
	switch {
	case errors.Is(err, gates.ErrUnknown):
		check.NotRun = err.Error()
		return check, nil
	case err != nil:
		return store.NodeCheck{}, fmt.Errorf("check %s: %w", check.ID, err)
	case gate.Agent != "":
		check.NotRun = fmt.Sprintf("gate %s is an agent gate, and a check node runs argv gates only", name)
		return check, nil
	}
	check.Argv = gate.Argv
	check.Limit = int64(gate.Limit / time.Second)
	check.Needs = gateNeeds(gate.Needs)
	return check, nil
}

// gateNeeds returns needs as the store holds the pools a check draws on.
func gateNeeds(needs []gates.Need) []store.EpicGateNeed {
	out := make([]store.EpicGateNeed, 0, len(needs))
	for _, need := range needs {
		out = append(out, store.EpicGateNeed{Pool: need.Pool, Count: int64(need.Count)})
	}
	return out
}

// commandNeeds returns needs as the store holds the pools a check draws on.
func commandNeeds(needs []workflow.Need) []store.EpicGateNeed {
	out := make([]store.EpicGateNeed, 0, len(needs))
	for _, need := range needs {
		out = append(out, store.EpicGateNeed{Pool: need.Pool, Count: int64(need.Count)})
	}
	return out
}

// startEvidence starts the evidence run of each Requirement the action names, under the id it names, at the run's revision.
// The phase is red at base and green at head.
func (r *Runner) startEvidence(ctx context.Context, epic store.EpicRow, act Action) error {
	phase := evidence.PhaseGreen
	if act.Def.Settings.At == atBase {
		phase = evidence.PhaseRed
	}
	return r.runItems(ctx, epic, act, phase)
}

// runItems runs the evidence item of each Requirement the action names, under the id it names, at the run's revision in
// phase.
func (r *Runner) runItems(ctx context.Context, epic store.EpicRow, act Action, phase string) error {
	for i, id := range act.IDs {
		if _, err := r.evidence.RunItem(ctx, id, epic.ID, act.Items[i], act.Run.Revision, phase); err != nil {
			return fmt.Errorf("run evidence %s: %w", act.Items[i], err)
		}
	}
	return nil
}

// startAgent starts the read-only step of an agent run at the run's revision, under the id derived from the run. A
// start refused for good records the refusal as the run's result, once, and returns it: the pass stops there, and the
// next pass ends the run failed with that result. Any other start error is returned as it is, and the start is tried
// again on the next pass.
func (r *Runner) startAgent(ctx context.Context, epic store.EpicRow, act Action) error {
	settings := act.Def.Settings
	start := enginehost.StepStart{
		ID: store.NodeWorkID(act.Run.ID, act.Run.Attempt, stepPart), Agent: settings.Agent, Prompt: settings.Prompt,
		Schema: settings.Schema, Workspace: epic.Repository, Revision: act.Run.Revision, ReadOnly: true, Epic: epic.ID,
	}
	if _, err := r.steps.Start(ctx, start); err != nil {
		startErr := fmt.Errorf("start step %s: %w", start.ID, err)
		if !refusedForGood(err) {
			return startErr
		}
		result := "not run: " + err.Error()
		if recErr := r.store.RecordNodeResult(ctx, epic.ID, act.Path, act.Run.Attempt, result); recErr != nil {
			return errors.Join(startErr, fmt.Errorf("record refusal of node %s: %w", act.Path, recErr))
		}
		return startErr
	}
	return nil
}

// refusedForGood reports whether err refuses a step's start for good: a step schema the step rules do not accept, an
// agent name that is not valid, or an agent that does not resolve.
func refusedForGood(err error) bool {
	return errors.Is(err, enginehost.ErrBadSchema) || errors.Is(err, enginehost.ErrBadAgent) ||
		errors.Is(err, agents.ErrUnresolved)
}

// recordOwned records the paths the epic's branch changes over its base as a JSON array, which is [] when none changed.
// --no-renames lists a renamed path under both its names, so a rename out of an owned path is not missed.
func (r *Runner) recordOwned(ctx context.Context, epic store.EpicRow, act Action) error {
	head, err := r.branchHead(ctx, epic)
	if err != nil {
		return err
	}
	host, err := r.hostOf(epic)
	if err != nil {
		return fmt.Errorf("list paths changed by epic %s: %w", epic.ID, err)
	}
	listing, err := gitOutput(ctx, host, epic.Repository, "diff", "--name-only", "-z", "--no-renames", epic.Base, head)
	if err != nil {
		return fmt.Errorf("list paths changed by epic %s: %w", epic.ID, err)
	}
	changed := []string{}
	for path := range strings.SplitSeq(listing, "\x00") {
		if path != "" {
			changed = append(changed, path)
		}
	}
	data, err := json.Marshal(changed)
	if err != nil {
		return fmt.Errorf("encode paths changed by epic %s: %w", epic.ID, err)
	}
	if err = r.store.RecordNodeResult(ctx, epic.ID, act.Path, act.Run.Attempt, string(data)); err != nil {
		return fmt.Errorf("record result of node %s: %w", act.Path, err)
	}
	return nil
}

// checkRefs compares the refs a check node run was prepared with to the refs now. A check node makes no commits, so
// build.ScopeMoves keeps only the moves of the epic's own refs, and no move parks it: each move kept is told.
func (r *Runner) checkRefs(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	now, err := r.refSnapshot(ctx, epic)
	if err != nil {
		return err
	}
	moves, err := build.Compare(act.Run.Refs, now)
	if err != nil {
		return fmt.Errorf("compare refs of node %s: %w", act.Path, err)
	}
	host, err := r.hostOf(epic)
	if err != nil {
		return fmt.Errorf("scope refs of node %s: %w", act.Path, err)
	}
	moves, err = build.ScopeMoves(ctx, host, epic.Repository, epic.ID, moves, "", "")
	if err != nil {
		return fmt.Errorf("scope refs of node %s: %w", act.Path, err)
	}
	tells := make([]string, 0, len(moves))
	for _, move := range moves {
		tells = append(
			tells,
			fmt.Sprintf("workflow node %s moved %s (%s to %s)", act.Path, move.Ref, move.From, move.To),
		)
	}
	if err = r.store.CheckNodeRefs(ctx, epic.ID, act.Path, act.Run.Attempt, tells, at); err != nil {
		return fmt.Errorf("check refs of node %s: %w", act.Path, err)
	}
	return nil
}

// gitOutput runs git in dir on host and returns its stdout as git wrote it, untrimmed. A git that ran and failed returns
// its *hostexec.GitError, whose text carries git's command and stderr, so the error reads as it always has.
func gitOutput(ctx context.Context, host hostexec.Host, dir string, args ...string) (string, error) {
	out, err := host.Git(ctx, hostexec.GitCall{Dir: dir, Args: args})
	if err != nil {
		return "", fmt.Errorf("%w", err)
	}
	return string(out), nil
}
