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
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
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
// labeled steps of steps. A nil steps answers unavailable. A nil store lists no database epics, and a nil evidence
// client attaches no runs.
type epicsServer struct {
	root     string
	steps    StepClient
	store    EpicStoreClient
	evidence EvidenceClient
	timeout  time.Duration
	now      func() time.Time
	clock    rpcretry.Clock
}

// List answers every epic under the record root and every epic orreryd's database holds, live or folded, sorted by id.
// An epic in both is answered once, with the database's fields and the record's problem. It asks ListEpics and ListSteps
// once each, and again only to retry an unavailable answer.
func (e epicsServer) List(
	ctx context.Context,
	_ *connect.Request[orreryv1.EpicsListRequest],
) (*connect.Response[orreryv1.EpicsListResponse], error) {
	recs, err := readEpics(e.root)
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
		_, taskProblems, readErr := readTasks(rec.Dir)
		if readErr != nil {
			return nil, listError(connect.CodeInternal, epicsFailed)
		}
		summary, shared := storedByID[rec.ID]
		if !shared {
			epics = append(epics, epicMessage(rec, taskProblems, byEpic[rec.ID], now))
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
// the evidence runs of its item, and the tasks of its record when one exists. An id that neither the database nor the
// record root lists answers not_found, and the id is never joined into a path.
func (e epicsServer) Get(
	ctx context.Context,
	req *connect.Request[orreryv1.EpicsGetRequest],
) (*connect.Response[orreryv1.EpicsGetResponse], error) {
	id := req.Msg.GetId()
	stored, inStore, err := e.getStored(ctx, id)
	if err != nil {
		return nil, err
	}
	rec, found, err := findEpic(e.root, id)
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
		tasks, taskProblems, err = readTasks(rec.Dir)
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
		resp.Requirements = epicRequirementMessages(stored.GetContract().GetRequirements(), runs)
	} else {
		resp.Epic = epicMessage(rec, taskProblems, labeled, e.now())
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

// epicMessage returns the Epic for rec, an epic the records alone hold. Its problem is recordProblems. Its running steps
// are the labeled steps that have not ended, held ones included, and the rest count as ended.
func epicMessage(rec epicRecord, taskProblems []string, labeled []*enginev1.Step, now time.Time) *orreryv1.Epic {
	running, ended := epicSteps(labeled)
	return &orreryv1.Epic{
		Id:           rec.ID,
		Repository:   rec.Repository,
		Slug:         rec.Slug,
		Outcome:      rec.Outcome,
		Changed:      rec.Changed.Unix(),
		Live:         epicLive(rec, now, len(running) > 0),
		RunningSteps: running,
		EndedSteps:   ended,
		Problem:      strings.Join(recordProblems(rec, taskProblems), "; "),
		Source:       epicSourceRecord,
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

// epicStepMessage converts step to its EpicStep. A check's detail is not carried, so the phone never shows it.
func epicStepMessage(step *enginev1.Step) *orreryv1.EpicStep {
	checks := make([]*orreryv1.EpicStepCheck, 0, len(step.GetChecks()))
	for _, check := range step.GetChecks() {
		checks = append(checks, &orreryv1.EpicStepCheck{
			Round: check.GetRound(),
			Name:  check.GetName(),
			Ok:    check.GetOk(),
		})
	}
	return &orreryv1.EpicStep{
		Id:      step.GetId(),
		Task:    step.GetTask(),
		Agent:   step.GetAgent(),
		End:     step.GetEnd(),
		Status:  step.GetStatus(),
		Held:    step.GetHeld(),
		Started: step.GetStarted(),
		Ended:   step.GetEnded(),
		Doing:   step.GetDoing(),
		Checks:  checks,
		Reason:  step.GetReason(),
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

// stepResultFields parses a writing step's result as a JSON object. A result that does not parse, or is not an object,
// has no fields.
func stepResultFields(result string) map[string]json.RawMessage {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(result), &fields); err != nil {
		return nil
	}
	return fields
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
			Effort:  int32(task.Effort),
			Slug:    task.Slug,
			Subject: task.Subject,
			Status:  task.Status,
		})
	}
	return out, nil
}

// newEpicsHandler returns the path and handler that serve Epics over the records under root, the epics store holds, the
// runs evidence lists, and the labeled steps of steps, reading at most maxControlBody bytes of each request. A zero
// timeout is defaultControlTimeout. now dates each record epic's liveness, and clock times the retries of the calls to
// orreryd.
func newEpicsHandler(
	root string,
	steps StepClient,
	store EpicStoreClient,
	evidence EvidenceClient,
	timeout time.Duration,
	now func() time.Time,
	clock rpcretry.Clock,
) (string, http.Handler) {
	if timeout == 0 {
		timeout = defaultControlTimeout
	}
	return orreryv1connect.NewEpicsHandler(
		epicsServer{
			root: root, steps: steps, store: store, evidence: evidence, timeout: timeout, now: now, clock: clock,
		},
		connect.WithReadMaxBytes(maxControlBody),
	)
}
