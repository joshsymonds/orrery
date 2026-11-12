package server

import (
	"context"
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

// Get answers one epic by the id List gave it, with its tasks and its labeled steps, oldest first. An id the record
// root does not list answers not_found, and the id is never joined into a path.
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
		Epic:  epicMessage(rec, taskProblems, labeled, e.now()),
		Tasks: taskMessages,
		Steps: epicStepMessages(labeled),
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
