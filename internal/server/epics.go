package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"connectrpc.com/connect"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	enginev1connect "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/redact"
	"github.com/joshsymonds/orrery/internal/rpcretry"
)

// epicNotFound is the fixed message for a Get of an id that the record root does not list.
const epicNotFound = "epic not found"

// epicsFailed is the fixed message for an epics read that fails for an unexpected reason.
const epicsFailed = "epics read failed"

// epicStoreUnavailable is the fixed message for an epic store read that fails, other than an older orreryd's
// unimplemented answer or a Get's not_found.
const epicStoreUnavailable = "epic store unavailable"

// epicSourceRecord and epicSourceOrreryd are the Epic.source of an epic read from the records only and of an epic that
// orreryd's database holds.
const (
	epicSourceRecord  = "record"
	epicSourceOrreryd = "orreryd"
)

// epicsServer answers orrery.v1.Epics from the records under root, the epics store holds, the runs of evidence, and the
// labeled steps of steps. A nil steps answers unavailable. A nil store lists no database epics, a nil commands answers
// the epic commands unavailable, and a nil evidence client attaches no runs. Each rpc of the service is a method here.
type epicsServer struct {
	root     string
	steps    StepClient
	store    EpicStoreClient
	commands enginev1connect.EpicsClient
	evidence EvidenceClient
	timeout  time.Duration
	now      func() time.Time
	clock    rpcretry.Clock
	heads    *headCache
}

// List answers every epic under the record root and every epic orreryd's database holds, live or folded, sorted by id.
// An epic in both is answered once, with the database's fields and the record's problem. It asks ListEpics and ListSteps
// once each, and again only to retry an unavailable answer.
func (e epicsServer) List(
	ctx context.Context,
	_ *connect.Request[orreryv1.EpicsListRequest],
) (*connect.Response[orreryv1.EpicsListResponse], error) {
	recs, err := readEpics(e.root, e.heads)
	if err != nil {
		return nil, listError(connect.CodeInternal, epicsFailed)
	}
	stored, err := e.listStored(ctx)
	if err != nil {
		return nil, err
	}
	steps, err := e.listSteps(ctx, "")
	if err != nil {
		return nil, err
	}
	byEpic := stepsByEpic(steps)
	now := e.now()
	storedByID := make(map[string]*enginev1.EpicSummary, len(stored))
	for _, summary := range stored {
		storedByID[summary.GetId()] = summary
	}
	epics := make([]*orreryv1.Epic, 0, len(recs)+len(stored))
	for _, rec := range recs {
		tasks, taskProblems, readErr := readTasks(rec.Dir, e.heads)
		if readErr != nil {
			return nil, listError(connect.CodeInternal, epicsFailed)
		}
		summary, shared := storedByID[rec.ID]
		if !shared {
			epics = append(epics, epicMessage(rec, tasks, taskProblems, byEpic[rec.ID], now))
			continue
		}
		epics = append(epics, storedEpicMessage(
			rec.ID, summary.GetPhase(), summary.GetChanged(), recordProblems(rec, taskProblems), byEpic[rec.ID],
		))
		delete(storedByID, rec.ID)
	}
	for id, summary := range storedByID {
		epics = append(epics, storedEpicMessage(id, summary.GetPhase(), summary.GetChanged(), nil, byEpic[id]))
	}
	sort.Slice(epics, func(i, j int) bool { return epics[i].GetId() < epics[j].GetId() })
	return connect.NewResponse(&orreryv1.EpicsListResponse{Epics: epics}), nil
}

// Get answers one epic by the id List gave it, with its tasks, its labeled steps oldest first, and its slices. An epic
// orreryd's database holds answers as List does, with its Requirements in contract order, each with its conclusion and
// the evidence runs of its item, its workflow nodes in the engine's order, its build_tasks in plan order, its open
// questions, its contract with its acceptance time, its summary, whether it is paused and why it is parked, and the
// tasks of its record when one exists. An id that neither the database nor the record root lists answers not_found,
// and the id is never joined into a path.
func (e epicsServer) Get(
	ctx context.Context,
	req *connect.Request[orreryv1.EpicsGetRequest],
) (*connect.Response[orreryv1.EpicsGetResponse], error) {
	id := req.Msg.GetId()
	stored, inStore, err := e.getStored(ctx, id)
	if err != nil {
		return nil, err
	}
	rec, found, err := findEpic(e.root, id, e.heads)
	if err != nil {
		return nil, listError(connect.CodeInternal, epicsFailed)
	}
	if !inStore && !found {
		return nil, listError(connect.CodeNotFound, epicNotFound)
	}
	steps, err := e.listSteps(ctx, id)
	if err != nil {
		return nil, err
	}
	var tasks []taskRecord
	var taskProblems []string
	if found {
		tasks, taskProblems, err = readTasks(rec.Dir, e.heads)
		if err != nil {
			return nil, listError(connect.CodeInternal, epicsFailed)
		}
	}
	taskMessages, err := epicTaskMessages(tasks)
	if err != nil {
		return nil, listError(connect.CodeInternal, epicsFailed)
	}
	labeled := stepsByEpic(steps)[id]
	resp := &orreryv1.EpicsGetResponse{
		Tasks:  taskMessages,
		Steps:  epicStepMessages(labeled),
		Slices: epicSlices(labeled),
	}
	if inStore {
		resp.Epic = storedEpicMessage(
			id, stored.GetPhase(), stored.GetChanged(), recordProblems(rec, taskProblems), labeled,
		)
		runs := e.listEvidence(ctx, id)
		roles := storedStepRoles(stored, runs)
		applyStoredRoles(resp.GetSteps(), roles)
		applyStoredRoles(resp.GetEpic().GetRunningSteps(), roles)
		resp.Requirements = epicRequirementMessages(stored.GetContract().GetRequirements(), runs)
		resp.Nodes = epicNodeMessages(stored.GetNodes(), runs)
		resp.BuildTasks = epicBuildTaskMessages(stored.GetTasks())
		resp.Questions = epicQuestionMessages(stored.GetQuestions(), stored.GetTurns())
		resp.Contract = epicContractMessage(stored.GetContract(), stored.GetCreated())
		resp.Summary = stored.GetSummary()
		resp.Paused = stored.GetPaused()
		resp.Parked = stored.GetParked()
	} else {
		resp.Epic = epicMessage(rec, tasks, taskProblems, labeled, e.now())
	}
	return connect.NewResponse(resp), nil
}

// listSteps asks the step client for the labeled steps, narrowed to epic when epic is not empty. A nil client, or a
// ListSteps that still fails after its unavailable retries, answers unavailable.
func (e epicsServer) listSteps(ctx context.Context, epic string) ([]*enginev1.Step, error) {
	if e.steps == nil {
		return nil, listError(connect.CodeUnavailable, stepsUnavailable)
	}
	var resp *connect.Response[enginev1.ListStepsResponse]
	err := retryUnder(ctx, e.clock, e.timeout, rpcretry.Unavailable, func(ctx context.Context) error {
		var err error
		resp, err = e.steps.ListSteps(ctx, connect.NewRequest(&enginev1.ListStepsRequest{Epic: epic}))
		if err != nil {
			return fmt.Errorf("server: list steps: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, listError(connect.CodeUnavailable, stepsUnavailable)
	}
	return resp.Msg.GetSteps(), nil
}

// listEvidence asks the evidence client for the runs of epic, in the order it lists them. A nil client, or a
// ListEvidence that still fails after its unavailable retries, lists none, so the epic is answered without runs. Each
// attempt runs under the timeout.
func (e epicsServer) listEvidence(ctx context.Context, epic string) []*enginev1.EvidenceRun {
	if e.evidence == nil {
		return nil
	}
	var resp *connect.Response[enginev1.ListEvidenceResponse]
	err := retryUnder(ctx, e.clock, e.timeout, rpcretry.Unavailable, func(ctx context.Context) error {
		var err error
		resp, err = e.evidence.ListEvidence(ctx, connect.NewRequest(&enginev1.ListEvidenceRequest{Epic: epic}))
		if err != nil {
			return fmt.Errorf("server: list evidence: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil
	}
	return resp.Msg.GetRuns()
}

// listStored asks the epic store for its epics. A nil store lists none, and so does a ListEpics that is unimplemented, as
// an older orreryd answers. An unavailable answer is retried as listSteps retries it. Any other failure answers
// unavailable. Each attempt runs under the timeout.
func (e epicsServer) listStored(ctx context.Context) ([]*enginev1.EpicSummary, error) {
	if e.store == nil {
		return nil, nil
	}
	var resp *connect.Response[enginev1.ListEpicsResponse]
	err := retryUnder(ctx, e.clock, e.timeout, rpcretry.Unavailable, func(ctx context.Context) error {
		var err error
		resp, err = e.store.ListEpics(ctx, connect.NewRequest(&enginev1.ListEpicsRequest{}))
		return errors.Join(err)
	})
	if err != nil {
		if connect.CodeOf(err) == connect.CodeUnimplemented {
			return nil, nil
		}
		return nil, listError(connect.CodeUnavailable, epicStoreUnavailable)
	}
	return resp.Msg.GetEpics(), nil
}

// getStored asks the epic store for the epic of id. It reports false, with no error, when there is no store or the store
// answers not_found or unimplemented, so that the record answers. An unavailable answer is retried as listSteps retries
// it. Any other failure answers unavailable. Each attempt runs under the timeout.
func (e epicsServer) getStored(ctx context.Context, id string) (*enginev1.StoredEpic, bool, error) {
	if e.store == nil {
		return nil, false, nil
	}
	var resp *connect.Response[enginev1.GetEpicResponse]
	err := retryUnder(ctx, e.clock, e.timeout, rpcretry.Unavailable, func(ctx context.Context) error {
		var err error
		resp, err = e.store.GetEpic(ctx, connect.NewRequest(&enginev1.GetEpicRequest{Id: id}))
		return errors.Join(err)
	})
	if err != nil {
		if code := connect.CodeOf(err); code == connect.CodeNotFound || code == connect.CodeUnimplemented {
			return nil, false, nil
		}
		return nil, false, listError(connect.CodeUnavailable, epicStoreUnavailable)
	}
	return resp.Msg.GetEpic(), true, nil
}

// stepsByEpic groups the labeled steps by their epic, each group oldest first. A step with no epic label belongs to no
// epic and is left out.
func stepsByEpic(steps []*enginev1.Step) map[string][]*enginev1.Step {
	sort.SliceStable(steps, func(i, j int) bool { return steps[i].GetStarted() < steps[j].GetStarted() })
	byEpic := make(map[string][]*enginev1.Step)
	for _, step := range steps {
		if step.GetEpic() == "" {
			continue
		}
		byEpic[step.GetEpic()] = append(byEpic[step.GetEpic()], step)
	}
	return byEpic
}

// epicMessage returns the Epic for rec, an epic the records alone hold, with the tasks its efforts hold. Its problem is
// recordProblems. Its running steps are the labeled steps that have not ended, held ones included, and the rest count as
// ended. It is waiting when it is live, has no outcome, no running step and no task: accepted and yet to start.
func epicMessage(
	rec epicRecord,
	tasks []taskRecord,
	taskProblems []string,
	labeled []*enginev1.Step,
	now time.Time,
) *orreryv1.Epic {
	running, ended := epicSteps(labeled)
	live := epicLive(rec, now, len(running) > 0)
	return &orreryv1.Epic{
		Id:            rec.ID,
		Repository:    rec.Repository,
		Slug:          rec.Slug,
		Outcome:       rec.Outcome,
		OutcomeDetail: rec.OutcomeDetail,
		Changed:       rec.Changed.Unix(),
		Live:          live,
		Waiting:       live && rec.Outcome == "" && len(running) == 0 && len(tasks) == 0,
		RunningSteps:  running,
		EndedSteps:    ended,
		Problem:       strings.Join(recordProblems(rec, taskProblems), "; "),
		Source:        epicSourceRecord,
	}
}

// storedEpicMessage returns the Epic of an epic orreryd's database holds: its outcome is the phase, its changed time is
// the database's, and it is live unless the phase is released. Its problem joins problems, which are the record's when a
// record of the id exists and none otherwise. Its steps are the labeled steps, as epicMessage's are.
func storedEpicMessage(id, phase string, changed int64, problems []string, labeled []*enginev1.Step) *orreryv1.Epic {
	running, ended := epicSteps(labeled)
	repository, slug, _ := strings.Cut(id, "/")
	return &orreryv1.Epic{
		Id:           id,
		Repository:   repository,
		Slug:         slug,
		Outcome:      phase,
		Changed:      changed,
		Live:         phase != "released",
		RunningSteps: running,
		EndedSteps:   ended,
		Problem:      strings.Join(problems, "; "),
		Source:       epicSourceOrreryd,
	}
}

// recordProblems returns the problems of a record: its head's problem, then each unreadable effort's problem.
func recordProblems(rec epicRecord, taskProblems []string) []string {
	var problems []string
	if rec.Problem != "" {
		problems = append(problems, rec.Problem)
	}
	return append(problems, taskProblems...)
}

// epicSteps splits labeled into the running steps, which have not ended and include held ones, and a count of the rest.
func epicSteps(labeled []*enginev1.Step) ([]*orreryv1.EpicStep, int32) {
	var running []*orreryv1.EpicStep
	var ended int32
	for _, step := range labeled {
		if step.GetEnd() == "" {
			running = append(running, epicStepMessage(step))
		} else {
			ended++
		}
	}
	return running, ended
}

// epicRequirementMessages converts each requirement of a contract to its EpicRequirement, in order. A requirement with
// an item carries the item's conclusion and the runs whose item is its id, in the order runs lists them. A requirement
// with no item, a legacy one, carries no conclusion and no runs.
func epicRequirementMessages(
	requirements []*enginev1.EpicRequirement,
	runs []*enginev1.EvidenceRun,
) []*orreryv1.EpicRequirement {
	runsByItem := make(map[string][]*enginev1.EvidenceRun)
	for _, run := range runs {
		runsByItem[run.GetItem()] = append(runsByItem[run.GetItem()], run)
	}
	out := make([]*orreryv1.EpicRequirement, 0, len(requirements))
	for _, req := range requirements {
		msg := &orreryv1.EpicRequirement{
			Id:       req.GetId(),
			Text:     req.GetText(),
			Evidence: req.GetEvidence(),
			Kind:     req.GetKind(),
		}
		if item := req.GetItem(); item != nil {
			msg.Conclusion = item.GetConclusion()
			msg.Runs = epicRunMessages(runsByItem[req.GetId()])
		}
		out = append(out, msg)
	}
	return out
}

// epicQuestionOpen is the state of a question the owner has not answered. Only open questions are answered.
const epicQuestionOpen = "open"

// epicQuestionMessages converts each open question to its EpicQuestion, in order, and leaves out the answered ones. A
// question's asked is the end of the turn whose seq is the question's turn; a turn that no seq matches gives zero.
func epicQuestionMessages(questions []*enginev1.EpicQuestion, turns []*enginev1.EpicTurn) []*orreryv1.EpicQuestion {
	endedBySeq := make(map[int64]int64, len(turns))
	for _, turn := range turns {
		endedBySeq[turn.GetSeq()] = turn.GetEnded()
	}
	out := make([]*orreryv1.EpicQuestion, 0, len(questions))
	for _, question := range questions {
		if question.GetState() != epicQuestionOpen {
			continue
		}
		out = append(out, &orreryv1.EpicQuestion{
			Id:    question.GetId(),
			Text:  question.GetText(),
			Turn:  question.GetTurn(),
			Asked: endedBySeq[question.GetTurn()],
		})
	}
	return out
}

// epicContractMessage converts contract to its EpicContract: its sections in the contract's order, and accepted, which
// is the epic's created time. The contract's requirements are not part of it; Get answers them as its requirements.
func epicContractMessage(contract *enginev1.EpicContract, accepted int64) *orreryv1.EpicContract {
	return &orreryv1.EpicContract{
		Title:        contract.GetTitle(),
		Approved:     contract.GetApproved(),
		Intent:       contract.GetIntent(),
		Failures:     epicFailureMessages(contract.GetFailures()),
		Care:         contract.GetCare(),
		Additions:    epicAdditionMessages(contract.GetAdditions()),
		Premises:     epicPremiseMessages(contract.GetPremises()),
		Exclusions:   contract.GetExclusions(),
		Approach:     contract.GetApproach(),
		Alternatives: epicAlternativeMessages(contract.GetAlternatives()),
		ReleaseSteps: epicReleaseStepMessages(contract.GetReleaseSteps()),
		ReleaseThen:  contract.GetReleaseThen(),
		Checks:       contract.GetChecks(),
		Base:         contract.GetBase(),
		Accepted:     accepted,
	}
}

// epicFailureMessages converts each failure of a contract to its EpicFailure, in order.
func epicFailureMessages(failures []*enginev1.EpicFailure) []*orreryv1.EpicFailure {
	out := make([]*orreryv1.EpicFailure, 0, len(failures))
	for _, failure := range failures {
		out = append(out, &orreryv1.EpicFailure{
			Id:       failure.GetId(),
			Text:     failure.GetText(),
			Rating:   failure.GetRating(),
			Response: failure.GetResponse(),
		})
	}
	return out
}

// epicAdditionMessages converts each addition of a contract to its EpicAddition, in order.
func epicAdditionMessages(additions []*enginev1.EpicAddition) []*orreryv1.EpicAddition {
	out := make([]*orreryv1.EpicAddition, 0, len(additions))
	for _, addition := range additions {
		out = append(out, &orreryv1.EpicAddition{
			Location: addition.GetLocation(),
			What:     addition.GetWhat(),
			Why:      addition.GetWhy(),
			Cost:     addition.GetCost(),
		})
	}
	return out
}

// epicPremiseMessages converts each premise of a contract to its EpicPremise, in order.
func epicPremiseMessages(premises []*enginev1.EpicPremise) []*orreryv1.EpicPremise {
	out := make([]*orreryv1.EpicPremise, 0, len(premises))
	for _, premise := range premises {
		out = append(out, &orreryv1.EpicPremise{
			Id:      premise.GetId(),
			Text:    premise.GetText(),
			IfWrong: premise.GetIfWrong(),
		})
	}
	return out
}

// epicAlternativeMessages converts each alternative of a contract to its EpicAlternative, in order.
func epicAlternativeMessages(alternatives []*enginev1.EpicAlternative) []*orreryv1.EpicAlternative {
	out := make([]*orreryv1.EpicAlternative, 0, len(alternatives))
	for _, alternative := range alternatives {
		out = append(out, &orreryv1.EpicAlternative{
			Alternative: alternative.GetAlternative(),
			WhyNot:      alternative.GetWhyNot(),
			Reconsider:  alternative.GetReconsider(),
		})
	}
	return out
}

// epicReleaseStepMessages converts each release step of a contract to its EpicReleaseStep, in order.
func epicReleaseStepMessages(steps []*enginev1.EpicReleaseStep) []*orreryv1.EpicReleaseStep {
	out := make([]*orreryv1.EpicReleaseStep, 0, len(steps))
	for _, step := range steps {
		out = append(out, &orreryv1.EpicReleaseStep{
			Step:   step.GetStep(),
			Target: step.GetTarget(),
			Undo:   step.GetUndo(),
		})
	}
	return out
}

// epicNodeMessages converts each workflow node run to its EpicNode, in order.
func epicNodeMessages(nodes []*enginev1.EpicNode, runs []*enginev1.EvidenceRun) []*orreryv1.EpicNode {
	runsByID := make(map[string]*enginev1.EvidenceRun, len(runs))
	for _, run := range runs {
		runsByID[run.GetId()] = run
	}
	out := make([]*orreryv1.EpicNode, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, epicNodeMessage(node, runsByID))
	}
	return out
}

// epicNodeMessage converts node to its EpicNode, with every field the engine gave. An evidence node's runs are the runs
// its ids name, in the order it names them; an id that runsByID does not hold is left out, as a requirement's runs are.
func epicNodeMessage(node *enginev1.EpicNode, runsByID map[string]*enginev1.EvidenceRun) *orreryv1.EpicNode {
	var runs []*orreryv1.EvidenceRun
	for _, id := range node.GetRuns() {
		if run, ok := runsByID[id]; ok {
			runs = append(runs, epicRunMessage(run))
		}
	}
	return &orreryv1.EpicNode{
		Path:      node.GetPath(),
		Node:      node.GetNode(),
		Kind:      node.GetKind(),
		State:     node.GetState(),
		Outcome:   node.GetOutcome(),
		Round:     node.GetRound(),
		LoopRound: node.GetLoopRound(),
		Attempt:   node.GetAttempt(),
		Output:    node.GetOutput(),
		Revision:  node.GetRevision(),
		Turns:     node.GetTurns(),
		Tasks:     node.GetTasks(),
		Runs:      runs,
		Check:     epicNodeCheckMessage(node.GetCheck()),
		Step:      node.GetStep(),
	}
}

// epicNodeCheckMessage converts check to its EpicNodeCheck. An unstored check, which is nil, stays nil.
func epicNodeCheckMessage(check *enginev1.EpicNodeCheck) *orreryv1.EpicNodeCheck {
	if check == nil {
		return nil
	}
	return &orreryv1.EpicNodeCheck{
		State:    check.GetState(),
		Exit:     check.GetExit(),
		TimedOut: check.GetTimedOut(),
		NotRun:   check.GetNotRun(),
		Stdout:   check.GetStdout(),
		Stderr:   check.GetStderr(),
	}
}

// epicRunMessages converts each evidence run to its EvidenceRun, in order.
func epicRunMessages(runs []*enginev1.EvidenceRun) []*orreryv1.EvidenceRun {
	out := make([]*orreryv1.EvidenceRun, 0, len(runs))
	for _, run := range runs {
		out = append(out, epicRunMessage(run))
	}
	return out
}

// epicRunMessage converts run to its EvidenceRun. Each artifact carries its path, state and size, and the media type of
// its path. The run's log hash, its judge's step and each artifact's blob hash stay with orreryd.
func epicRunMessage(run *enginev1.EvidenceRun) *orreryv1.EvidenceRun {
	artifacts := make([]*orreryv1.EvidenceArtifact, 0, len(run.GetArtifacts()))
	for _, artifact := range run.GetArtifacts() {
		artifacts = append(artifacts, &orreryv1.EvidenceArtifact{
			Path:      artifact.GetPath(),
			State:     artifact.GetState(),
			Size:      artifact.GetSize(),
			MediaType: artifactMediaType(artifact.GetPath()),
		})
	}
	return &orreryv1.EvidenceRun{
		Id:          run.GetId(),
		Phase:       run.GetPhase(),
		Source:      run.GetSource(),
		State:       run.GetState(),
		Exit:        run.GetExit(),
		TimedOut:    run.GetTimedOut(),
		NotCaptured: run.GetNotCaptured(),
		Verdict:     run.GetVerdict(),
		JudgeReason: run.GetJudgeReason(),
		Passed:      run.GetPassed(),
		Revision:    run.GetRevision(),
		Created:     run.GetCreated(),
		Ended:       run.GetEnded(),
		LogBytes:    run.GetLogBytes(),
		StderrBytes: run.GetStderrBytes(),
		Artifacts:   artifacts,
	}
}

// epicStepMessages converts each step to its EpicStep, in order.
func epicStepMessages(steps []*enginev1.Step) []*orreryv1.EpicStep {
	out := make([]*orreryv1.EpicStep, 0, len(steps))
	for _, step := range steps {
		out = append(out, epicStepMessage(step))
	}
	return out
}

// epicStepMessage converts step to its EpicStep. Each check's detail and its two tails are carried, redacted. Stdout and
// stderr are carried only for a failed gate check, and a check that is not a gate check carries none of the four. The
// summary is the latest result's string summary. The verdicts are the step's judge verdicts. The role is implementer
// for a writing step; a read-only step's role comes from its stored epic, through applyStoredRoles.
func epicStepMessage(step *enginev1.Step) *orreryv1.EpicStep {
	checks := make([]*orreryv1.EpicStepCheck, 0, len(step.GetChecks()))
	for _, check := range step.GetChecks() {
		out := &orreryv1.EpicStepCheck{
			Round:      check.GetRound(),
			Name:       check.GetName(),
			Ok:         check.GetOk(),
			Detail:     redact.Text(check.GetDetail()),
			TailStdout: redact.Text(check.GetStdout()),
			TailStderr: redact.Text(check.GetStderr()),
		}
		if check.GetName() == epicGateCheck && !check.GetOk() {
			out.Stdout = check.GetStdout()
			out.Stderr = check.GetStderr()
		}
		checks = append(checks, out)
	}
	role := ""
	if !step.GetReadOnly() {
		role = epicRoleImplementer
	}
	return &orreryv1.EpicStep{
		Id:           step.GetId(),
		Task:         step.GetTask(),
		Agent:        step.GetAgent(),
		End:          step.GetEnd(),
		Status:       step.GetStatus(),
		Held:         step.GetHeld(),
		Started:      step.GetStarted(),
		Ended:        step.GetEnded(),
		Doing:        step.GetDoing(),
		Checks:       checks,
		Reason:       step.GetReason(),
		Stage:        step.GetStage(),
		StageStarted: step.GetStageStarted(),
		GateName:     step.GetGateName(),
		GateLimit:    step.GetGateLimitSeconds(),
		Limited:      step.GetLimited(),
		LimitKind:    step.GetLimitKind(),
		LimitResets:  step.GetLimitResets(),
		ReadOnly:     step.GetReadOnly(),
		Role:         role,
		Summary:      redact.Text(stepResultSummary(step.GetResult())),
		Verdicts:     epicStepVerdictMessages(step.GetVerdicts()),
	}
}

// epicStepVerdictMessages converts each judge verdict of verdicts to its EpicStepVerdict, in order. A monitor's verdict
// is left out, and each reason is redacted.
func epicStepVerdictMessages(verdicts []*enginev1.StepVerdict) []*orreryv1.EpicStepVerdict {
	out := make([]*orreryv1.EpicStepVerdict, 0, len(verdicts))
	for _, verdict := range verdicts {
		if verdict.GetKind() != epicVerdictJudge {
			continue
		}
		out = append(out, &orreryv1.EpicStepVerdict{
			Round:   verdict.GetRound(),
			Verdict: verdict.GetVerdict(),
			Reason:  redact.Text(verdict.GetDetail()),
		})
	}
	return out
}

// storedStepRoles maps the id of each step that stored holds to its role: planner for a planner turn's step, reviewer
// for a task review's step, and evidence for an evidence run's judge step. An empty id is no step and is left out.
func storedStepRoles(stored *enginev1.StoredEpic, runs []*enginev1.EvidenceRun) map[string]string {
	roles := make(map[string]string)
	for _, turn := range stored.GetTurns() {
		if id := turn.GetStep(); id != "" {
			roles[id] = epicRolePlanner
		}
	}
	for _, task := range stored.GetTasks() {
		for _, review := range task.GetReviews() {
			if id := review.GetStep(); id != "" {
				roles[id] = epicRoleReviewer
			}
		}
	}
	for _, run := range runs {
		if id := run.GetJudgeStep(); id != "" {
			roles[id] = epicRoleEvidence
		}
	}
	return roles
}

// applyStoredRoles sets the role of each step that has none from roles, by step id. A writing step already has its role,
// implementer, and keeps it.
func applyStoredRoles(steps []*orreryv1.EpicStep, roles map[string]string) {
	for _, step := range steps {
		if step.GetRole() == "" {
			step.Role = roles[step.GetId()]
		}
	}
}

// epicEndDone is the end of a step that ended done. epicEndChecksFailed and epicEndGoalUnmet are the ends that name the
// ring they errored at. epicGateCheck is the name of the gate check that a writing step's done return runs, as the step
// loop stores it.
const (
	epicEndDone         = "done"
	epicEndChecksFailed = "checks_failed"
	epicEndGoalUnmet    = "goal_unmet"
	epicGateCheck       = "gate"
)

// The roles an EpicStep plays, as the proto names them, and the kind of a step's judge verdict.
const (
	epicRoleImplementer = "implementer"
	epicRoleReviewer    = "reviewer"
	epicRolePlanner     = "planner"
	epicRoleEvidence    = "evidence"
	epicVerdictJudge    = "judge"
)

// epicSlices returns a slice per task label among the writing steps of steps, oldest first, in order of each label's
// first start. A writing step is one that is not read-only and has a task label. A slice reads its cells from its
// label's latest writing step. Read-only and untasked steps add none.
func epicSlices(steps []*enginev1.Step) []*orreryv1.EpicSlice {
	var labels []string
	latest := make(map[string]*enginev1.Step)
	for _, step := range steps {
		if step.GetReadOnly() || step.GetTask() == "" {
			continue
		}
		if _, seen := latest[step.GetTask()]; !seen {
			labels = append(labels, step.GetTask())
		}
		latest[step.GetTask()] = step
	}
	out := make([]*orreryv1.EpicSlice, 0, len(labels))
	for _, label := range labels {
		out = append(out, epicSliceMessage(label, latest[label]))
	}
	return out
}

// epicSliceMessage returns the EpicSlice of label, whose cells are read from step, the label's latest writing step. A
// running or held step's at is the ring it is working toward, and it has no error. An ended step has no at and no held,
// and errors at the ring its end names, or at the first ring not reached when the end names none.
func epicSliceMessage(label string, step *enginev1.Step) *orreryv1.EpicSlice {
	fields := stepResultFields(step.GetResult())
	redTest := stepResultEvidence(fields, "red_evidence")
	testsPass := stepResultEvidence(fields, "green_evidence")
	checks := epicChecksPass(step.GetChecks())
	gateAccepted := step.GetEnd() == epicEndDone
	slice := &orreryv1.EpicSlice{
		Task:         label,
		Hue:          epicSliceHue(step.GetEnd()),
		RedTest:      redTest,
		Working:      true,
		TestsPass:    testsPass,
		Checks:       checks,
		GateAccepted: gateAccepted,
	}
	firstOpen := epicFirstOpenRing(redTest, testsPass, checks, gateAccepted)
	end := step.GetEnd()
	switch {
	case end == "":
		slice.At = firstOpen
		slice.Held = step.GetHeld()
	case end == epicEndChecksFailed:
		slice.ErrorAt = ringChecks
	case end == epicEndGoalUnmet:
		slice.ErrorAt = ringGateAccepted
	case end != epicEndDone:
		slice.ErrorAt = firstOpen
	}
	return slice
}

// epicSliceHue is "dispatched" while a step has not ended, "done" when it ended done, and "gap" for any other end.
func epicSliceHue(end string) string {
	switch end {
	case "":
		return "dispatched"
	case epicEndDone:
		return "done"
	default:
		return "gap"
	}
}

// The rings a slice's cells are numbered by, as the proto numbers them from red_test, the outermost, in to
// gate_accepted, the innermost. A step that ended checks_failed errors at checks, and one that ended goal_unmet at
// gate_accepted.
const (
	ringChecks       int32 = 4
	ringGateAccepted int32 = 5
)

// epicFirstOpenRing is the first ring not reached, counting from red_test inward: 1 for red_test through 5 for
// gate_accepted, or 0 when every ring is reached, which only a step that ended done is. working is reached by every
// listed step, since it has run.
func epicFirstOpenRing(redTest, testsPass, checks, gateAccepted bool) int32 {
	for i, reached := range []bool{redTest, true, testsPass, checks, gateAccepted} {
		if !reached {
			return int32(i + 1)
		}
	}
	return 0
}

// stepResultFields parses a step's result as a JSON object. A result that does not parse, or is not an object, has no
// fields.
func stepResultFields(result string) map[string]json.RawMessage {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(result), &fields); err != nil {
		return nil
	}
	return fields
}

// stepResultSummary returns the string summary of a step's result. A result with no summary, or one whose summary is not
// a string, has none.
func stepResultSummary(result string) string {
	raw, ok := stepResultFields(result)["summary"]
	if !ok {
		return ""
	}
	var summary string
	if err := json.Unmarshal(raw, &summary); err != nil {
		return ""
	}
	return summary
}

// stepResultEvidence reports whether fields holds key as a string that, trimmed, is non-empty and does not start with
// NOT RUN in any case. A value that is not a string is no evidence.
func stepResultEvidence(fields map[string]json.RawMessage, key string) bool {
	raw, ok := fields[key]
	if !ok {
		return false
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return false
	}
	text = strings.TrimSpace(text)
	return text != "" && !strings.HasPrefix(strings.ToUpper(text), "NOT RUN")
}

// epicChecksPass reports whether every check of the highest round is ok and one of them is the gate. No checks is
// false.
func epicChecksPass(checks []*enginev1.StepCheck) bool {
	var top uint32
	for _, check := range checks {
		top = max(top, check.GetRound())
	}
	gate := false
	for _, check := range checks {
		if check.GetRound() != top {
			continue
		}
		if !check.GetOk() {
			return false
		}
		gate = gate || check.GetName() == epicGateCheck
	}
	return gate
}

// epicTaskMessages converts each task to its EpicTask, in order. An effort number that the wire's int32 cannot hold is
// an error.
func epicTaskMessages(tasks []taskRecord) ([]*orreryv1.EpicTask, error) {
	out := make([]*orreryv1.EpicTask, 0, len(tasks))
	for _, task := range tasks {
		if task.Effort < 0 || task.Effort > math.MaxInt32 {
			return nil, fmt.Errorf("server: effort %d does not fit int32", task.Effort)
		}
		out = append(out, &orreryv1.EpicTask{
			Effort:       int32(task.Effort),
			Slug:         task.Slug,
			Subject:      task.Subject,
			Status:       task.Status,
			Requirements: task.Requirements,
		})
	}
	return out, nil
}

// newEpicsHandler returns the path and handler that serve Epics over the records under root, the epics store holds, the
// epic commands commands forwards, the runs evidence lists, and the labeled steps of steps, reading at most
// maxControlBody bytes of each request. A zero timeout is defaultControlTimeout. now dates each record epic's liveness,
// and clock times the retries of the calls to orreryd.
func newEpicsHandler(
	root string,
	steps StepClient,
	store EpicStoreClient,
	commands enginev1connect.EpicsClient,
	evidence EvidenceClient,
	timeout time.Duration,
	now func() time.Time,
	clock rpcretry.Clock,
	opts ...connect.HandlerOption,
) (string, http.Handler) {
	if timeout == 0 {
		timeout = defaultControlTimeout
	}
	options := append([]connect.HandlerOption{connect.WithReadMaxBytes(maxControlBody)}, opts...)
	return orreryv1connect.NewEpicsHandler(
		epicsServer{
			root: root, steps: steps, store: store, commands: commands, evidence: evidence, timeout: timeout,
			now: now, clock: clock, heads: newHeadCache(),
		},
		options...,
	)
}
