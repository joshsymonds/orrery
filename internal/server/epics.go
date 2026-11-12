package server

import (
	"context"
	"encoding/json"
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
)

// epicNotFound is the fixed message for a Get of an id that the record root does not list.
const epicNotFound = "epic not found"

// epicsFailed is the fixed message for an epics read that fails for an unexpected reason.
const epicsFailed = "epics read failed"

// epicsServer answers orrery.v1.Epics from the records under root and the labeled steps of steps. A nil steps answers
// unavailable.
type epicsServer struct {
	root    string
	steps   StepClient
	timeout time.Duration
	now     func() time.Time
}

// List answers every epic under the record root, live or folded, sorted by id. It asks ListSteps once.
func (e epicsServer) List(
	ctx context.Context,
	_ *connect.Request[orreryv1.EpicsListRequest],
) (*connect.Response[orreryv1.EpicsListResponse], error) {
	recs, err := readEpics(e.root)
	if err != nil {
		return nil, listError(connect.CodeInternal, epicsFailed)
	}
	steps, err := e.listSteps(ctx, "")
	if err != nil {
		return nil, err
	}
	byEpic := stepsByEpic(steps)
	now := e.now()
	epics := make([]*orreryv1.Epic, 0, len(recs))
	for _, rec := range recs {
		_, taskProblems, readErr := readTasks(rec.Dir)
		if readErr != nil {
			return nil, listError(connect.CodeInternal, epicsFailed)
		}
		epics = append(epics, epicMessage(rec, taskProblems, byEpic[rec.ID], now))
	}
	return connect.NewResponse(&orreryv1.EpicsListResponse{Epics: epics}), nil
}

// Get answers one epic by the id List gave it, with its tasks, its labeled steps oldest first, and its slices. An id
// the record root does not list answers not_found, and the id is never joined into a path.
func (e epicsServer) Get(
	ctx context.Context,
	req *connect.Request[orreryv1.EpicsGetRequest],
) (*connect.Response[orreryv1.EpicsGetResponse], error) {
	id := req.Msg.GetId()
	rec, found, err := findEpic(e.root, id)
	if err != nil {
		return nil, listError(connect.CodeInternal, epicsFailed)
	}
	if !found {
		return nil, listError(connect.CodeNotFound, epicNotFound)
	}
	steps, err := e.listSteps(ctx, id)
	if err != nil {
		return nil, err
	}
	tasks, taskProblems, err := readTasks(rec.Dir)
	if err != nil {
		return nil, listError(connect.CodeInternal, epicsFailed)
	}
	taskMessages, err := epicTaskMessages(tasks)
	if err != nil {
		return nil, listError(connect.CodeInternal, epicsFailed)
	}
	labeled := stepsByEpic(steps)[id]
	return connect.NewResponse(&orreryv1.EpicsGetResponse{
		Epic:   epicMessage(rec, taskProblems, labeled, e.now()),
		Tasks:  taskMessages,
		Steps:  epicStepMessages(labeled),
		Slices: epicSlices(labeled),
	}), nil
}

// listSteps asks the step client for the labeled steps, narrowed to epic when epic is not empty. A nil client, or a
// ListSteps that fails, answers unavailable. The call runs under the timeout.
func (e epicsServer) listSteps(ctx context.Context, epic string) ([]*enginev1.Step, error) {
	if e.steps == nil {
		return nil, listError(connect.CodeUnavailable, stepsUnavailable)
	}
	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	resp, err := e.steps.ListSteps(ctx, connect.NewRequest(&enginev1.ListStepsRequest{Epic: epic}))
	if err != nil {
		return nil, listError(connect.CodeUnavailable, stepsUnavailable)
	}
	return resp.Msg.GetSteps(), nil
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

// epicMessage returns the Epic for rec. Its problem joins the head's problem and each unreadable effort's problem with
// "; ". Its running steps are the labeled steps that have not ended, held ones included, and the rest count as ended.
func epicMessage(rec epicRecord, taskProblems []string, labeled []*enginev1.Step, now time.Time) *orreryv1.Epic {
	var problems []string
	if rec.Problem != "" {
		problems = append(problems, rec.Problem)
	}
	problems = append(problems, taskProblems...)
	var running []*orreryv1.EpicStep
	var ended int32
	for _, step := range labeled {
		if step.GetEnd() == "" {
			running = append(running, epicStepMessage(step))
		} else {
			ended++
		}
	}
	return &orreryv1.Epic{
		Id:           rec.ID,
		Repository:   rec.Repository,
		Slug:         rec.Slug,
		Outcome:      rec.Outcome,
		Changed:      rec.Changed.Unix(),
		Live:         epicLive(rec, now, len(running) > 0),
		RunningSteps: running,
		EndedSteps:   ended,
		Problem:      strings.Join(problems, "; "),
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

// epicEndDone is the end of a step that ended done. epicGateCheck is the name of the gate check that a writing step's
// done return runs, as the step loop stores it.
const (
	epicEndDone   = "done"
	epicGateCheck = "gate"
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

// epicSliceMessage returns the EpicSlice of label, whose cells are read from step, the label's latest writing step.
func epicSliceMessage(label string, step *enginev1.Step) *orreryv1.EpicSlice {
	fields := stepResultFields(step.GetResult())
	redTest := stepResultEvidence(fields, "red_evidence")
	testsPass := stepResultEvidence(fields, "green_evidence")
	checks := epicChecksPass(step.GetChecks())
	return &orreryv1.EpicSlice{
		Task:         label,
		Hue:          epicSliceHue(step.GetEnd()),
		RedTest:      redTest,
		Working:      true,
		TestsPass:    testsPass,
		Checks:       checks,
		GateAccepted: step.GetEnd() == epicEndDone,
		At:           epicSliceAt(step.GetEnd(), testsPass, checks),
	}
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

// The rings a running slice can be at, numbered as the proto numbers them from red_test, the outermost, in to
// gate_accepted, the innermost. The innermost reached ring is the furthest phase reached. working is always reached,
// so red_test is never the innermost reached ring, and gate_accepted needs an end.
const (
	ringWorking   int32 = 2
	ringTestsPass int32 = 3
	ringChecks    int32 = 4
)

// epicSliceAt is the innermost ring reached while a step runs or is held, 0 once it ended.
func epicSliceAt(end string, testsPass, checks bool) int32 {
	switch {
	case end != "":
		return 0
	case checks:
		return ringChecks
	case testsPass:
		return ringTestsPass
	default:
		return ringWorking
	}
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

// newEpicsHandler returns the path and handler that serve Epics over the records under root and the labeled steps of
// steps, reading at most maxControlBody bytes of each request. A zero timeout is defaultControlTimeout. now dates each
// epic's liveness.
func newEpicsHandler(
	root string,
	steps StepClient,
	timeout time.Duration,
	now func() time.Time,
) (string, http.Handler) {
	if timeout == 0 {
		timeout = defaultControlTimeout
	}
	return orreryv1connect.NewEpicsHandler(
		epicsServer{root: root, steps: steps, timeout: timeout, now: now},
		connect.WithReadMaxBytes(maxControlBody),
	)
}
