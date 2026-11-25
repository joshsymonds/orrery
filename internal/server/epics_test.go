package server

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// epicsNow returns the fixed clock the epics tests date their epics against.
func epicsNow() time.Time {
	return time.Unix(1_700_000_000, 0)
}

// epicsAgo returns the unix seconds d before epicsNow().
func epicsAgo(d time.Duration) int64 {
	return epicsNow().Add(-d).Unix()
}

// epicsFake is a StepClient whose ListSteps answers from steps as orreryd's Steps service answers (see
// listStepsAnswer), or listErr when it is set, and records each request. The epics handler calls no other StepClient
// method, so those answer an error.
type epicsFake struct {
	steps   []*enginev1.Step
	listErr error

	// mu guards listCalls and requests, which the handler's goroutine writes.
	mu        sync.Mutex
	listCalls int
	requests  []*enginev1.ListStepsRequest
}

// listRequests returns a copy of each ListSteps request the fake was asked, in order.
func (f *epicsFake) listRequests() []*enginev1.ListStepsRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*enginev1.ListStepsRequest(nil), f.requests...)
}

// calls returns how many ListSteps calls reached the fake.
func (f *epicsFake) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.listCalls
}

// listStepsAnswer answers req over steps as orreryd's Steps service does: the labeled steps of req's epic and task,
// each as its listing, with its result's summary and no result. A request for the running steps answers
// only those that have not ended, and counts the ended ones by epic.
func listStepsAnswer(steps []*enginev1.Step, req *enginev1.ListStepsRequest) *enginev1.ListStepsResponse {
	resp := &enginev1.ListStepsResponse{}
	if req.GetRunning() {
		resp.Ended = make(map[string]int32)
	}
	for _, step := range steps {
		if step.GetEpic() == "" || (req.GetEpic() != "" && step.GetEpic() != req.GetEpic()) ||
			(req.GetTask() != "" && step.GetTask() != req.GetTask()) {
			continue
		}
		if req.GetRunning() && step.GetEnd() != "" {
			resp.Ended[step.GetEpic()]++
			continue
		}
		resp.Steps = append(resp.Steps, listedStep(step))
	}
	return resp
}

// listedStep returns step as a ListSteps answer carries it: its summary read from its result as orreryd reads it, and
// no result. A step with no result keeps the summary it was given.
func listedStep(step *enginev1.Step) *enginev1.Step {
	listed := proto.CloneOf(step)
	if result := step.GetResult(); result != "" {
		listed.Summary = store.StepRow{Result: result}.Listing().Summary
	}
	listed.Result = ""
	return listed
}

func (f *epicsFake) StepStatus(
	context.Context,
	*connect.Request[enginev1.StepStatusRequest],
) (*connect.Response[enginev1.StepStatusResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("synthetic unused"))
}

func (f *epicsFake) ListSteps(
	_ context.Context,
	req *connect.Request[enginev1.ListStepsRequest],
) (*connect.Response[enginev1.ListStepsResponse], error) {
	f.mu.Lock()
	f.listCalls++
	f.requests = append(f.requests, req.Msg)
	f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	return connect.NewResponse(listStepsAnswer(f.steps, req.Msg)), nil
}

func (f *epicsFake) HoldStep(
	context.Context,
	*connect.Request[enginev1.HoldStepRequest],
) (*connect.Response[enginev1.HoldStepResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("synthetic unused"))
}

func (f *epicsFake) NudgeStep(
	context.Context,
	*connect.Request[enginev1.NudgeStepRequest],
) (*connect.Response[enginev1.NudgeStepResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("synthetic unused"))
}

func (f *epicsFake) StopStep(
	context.Context,
	*connect.Request[enginev1.StopStepRequest],
) (*connect.Response[enginev1.StopStepResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("synthetic unused"))
}

// epicsList calls List and returns the epics it answered.
func epicsList(t *testing.T, client orreryv1connect.EpicsClient) []*orreryv1.Epic {
	t.Helper()
	resp, err := client.List(context.Background(), connect.NewRequest(&orreryv1.EpicsListRequest{}))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return resp.Msg.GetEpics()
}

// epicsGet calls Get on id and returns the answer.
func epicsGet(t *testing.T, client orreryv1connect.EpicsClient, id string) *orreryv1.EpicsGetResponse {
	t.Helper()
	resp, err := client.Get(context.Background(), connect.NewRequest(&orreryv1.EpicsGetRequest{Id: id}))
	if err != nil {
		t.Fatalf("Get %q: %v", id, err)
	}
	return resp.Msg
}

// epicStepIDs returns the ids of steps, in order.
func epicStepIDs(steps []*orreryv1.EpicStep) []string {
	ids := make([]string, 0, len(steps))
	for _, step := range steps {
		ids = append(ids, step.GetId())
	}
	return ids
}

func TestEpicsStepServiceUnavailable(t *testing.T) {
	t.Parallel()
	stored := &epicStoreFake{stored: []*enginev1.StoredEpic{
		storedEpicOf("repo/epic", "accepted", epicsAgo(time.Hour)),
	}}
	cases := []struct {
		name  string
		steps StepClient
	}{
		{name: "nil client", steps: nil},
		{
			name: "failed ListSteps",
			steps: &epicsFake{listErr: connect.NewError(
				connect.CodeInternal, errors.New("synthetic list failure"),
			)},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := epicStoreServe(t, tc.steps, stored)
			_, err := client.List(context.Background(), connect.NewRequest(&orreryv1.EpicsListRequest{}))
			requireConnectError(t, err, connect.CodeUnavailable, "step service unavailable")
			_, err = client.Get(context.Background(), connect.NewRequest(&orreryv1.EpicsGetRequest{Id: "repo/epic"}))
			requireConnectError(t, err, connect.CodeUnavailable, "step service unavailable")
		})
	}
}
