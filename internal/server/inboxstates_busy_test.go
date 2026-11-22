package server

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// Row, epic and step ids of the busy tests, with invented values. The cases of TestInboxStatesBusy share one store, so
// each case has a pane and a headless session of its own. The headless ids are 32 hex digits each, as session ids are.
const (
	busyLivePane     = "orrery-busy-live"
	busyLiveHead     = "4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a"
	busyRunPane      = "orrery-busy-running"
	busyRunHead      = "4b4b4b4b4b4b4b4b4b4b4b4b4b4b4b4b"
	busyOncePane     = "orrery-busy-once"
	busyOnceHead     = "4c4c4c4c4c4c4c4c4c4c4c4c4c4c4c4c"
	busyDonePane     = "orrery-busy-done"
	busyDoneHead     = "4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d"
	busyWaitPane     = "orrery-busy-waiting"
	busyWaitHead     = "4e4e4e4e4e4e4e4e4e4e4e4e4e4e4e4e"
	busyNilPane      = "orrery-busy-nil"
	busyNilHead      = "4f4f4f4f4f4f4f4f4f4f4f4f4f4f4f4f"
	busyFailPane     = "orrery-busy-failing"
	busyFailHead     = "50505050505050505050505050505050"
	busyPaneEpic     = "repoa/busy-pane"
	busyHeadEpic     = "repoa/busy-headless"
	busyStepPane     = "busy-step-pane"
	busyStepHeadless = "busy-step-headless"
)

// The states the busy tests list rows in.
const (
	busyDone    = "done"
	busyWorking = "working"
	busyWaiting = "waiting"
)

// busyStart is the fixed clock of the busy tests.
func busyStart() time.Time {
	return time.Unix(1_700_000_000, 0)
}

// busyChangedAt is the change time of the busy rows, ten minutes before busyStart.
func busyChangedAt() time.Time {
	return busyStart().Add(-10 * time.Minute)
}

// busyUnimplemented is the error the busy fakes answer for the calls they do not serve.
func busyUnimplemented() error {
	return connect.NewError(connect.CodeUnimplemented, errors.New("synthetic unimplemented"))
}

// busyEpics is the EpicStoreClient of the busy tests. ListEpics lists summaries, and GetEpic is unimplemented.
type busyEpics struct {
	summaries []*enginev1.EpicSummary
}

func (f *busyEpics) ListEpics(
	context.Context,
	*connect.Request[enginev1.ListEpicsRequest],
) (*connect.Response[enginev1.ListEpicsResponse], error) {
	return connect.NewResponse(&enginev1.ListEpicsResponse{Epics: f.summaries}), nil
}

func (f *busyEpics) GetEpic(
	context.Context,
	*connect.Request[enginev1.GetEpicRequest],
) (*connect.Response[enginev1.GetEpicResponse], error) {
	return nil, busyUnimplemented()
}

// busySteps is the StepClient of the busy tests. ListSteps records each request, then answers the running steps, or
// listErr when it is set. Its other calls are unimplemented.
type busySteps struct {
	running []*enginev1.Step
	listErr error
	calls   []*enginev1.ListStepsRequest
}

func (f *busySteps) ListSteps(
	_ context.Context,
	req *connect.Request[enginev1.ListStepsRequest],
) (*connect.Response[enginev1.ListStepsResponse], error) {
	f.calls = append(f.calls, req.Msg)
	if f.listErr != nil {
		return nil, f.listErr
	}
	return connect.NewResponse(&enginev1.ListStepsResponse{Steps: f.running}), nil
}

func (f *busySteps) StepStatus(
	context.Context,
	*connect.Request[enginev1.StepStatusRequest],
) (*connect.Response[enginev1.StepStatusResponse], error) {
	return nil, busyUnimplemented()
}

func (f *busySteps) HoldStep(
	context.Context,
	*connect.Request[enginev1.HoldStepRequest],
) (*connect.Response[enginev1.HoldStepResponse], error) {
	return nil, busyUnimplemented()
}

func (f *busySteps) NudgeStep(
	context.Context,
	*connect.Request[enginev1.NudgeStepRequest],
) (*connect.Response[enginev1.NudgeStepResponse], error) {
	return nil, busyUnimplemented()
}

func (f *busySteps) StopStep(
	context.Context,
	*connect.Request[enginev1.StopStepRequest],
) (*connect.Response[enginev1.StopStepResponse], error) {
	return nil, busyUnimplemented()
}

// busyEpic returns the summary of the epic id in phase, steered by the session row steerer.
func busyEpic(id, phase, steerer string) *enginev1.EpicSummary {
	return &enginev1.EpicSummary{Id: id, Phase: phase, Steerer: steerer}
}

// busyStep returns a running step whose caller is the session row caller.
func busyStep(id, caller string) *enginev1.Step {
	return &enginev1.Step{Id: id, Caller: caller}
}

// seedBusyCases writes the rows of the cases of TestInboxStatesBusy into the store at path. The headless sessions are
// added in one write at busyChangedAt, and each pane is synced at that time: done, except the waiting case's pane.
func seedBusyCases(t *testing.T, path string) {
	t.Helper()
	changed := busyChangedAt()
	applyHeadlessAt(t, path, changed,
		store.EngineAdd{ID: busyLiveHead, Dir: "/work/busy", State: busyDone},
		store.EngineAdd{ID: busyRunHead, Dir: "/work/busy", State: busyDone},
		store.EngineAdd{ID: busyOnceHead, Dir: "/work/busy", State: busyDone},
		store.EngineAdd{ID: busyDoneHead, Dir: "/work/busy", State: busyDone},
		store.EngineAdd{ID: busyWaitHead, Dir: "/work/busy-waiting", State: busyWaiting},
		store.EngineAdd{ID: busyNilHead, Dir: "/work/busy", State: busyDone},
		store.EngineAdd{ID: busyFailHead, Dir: "/work/busy", State: busyDone},
	)
	for _, pane := range []string{busyLivePane, busyRunPane, busyOncePane, busyDonePane, busyNilPane, busyFailPane} {
		syncEndedPane(t, path, pane, busyDone, changed)
	}
	syncEndedPane(t, path, busyWaitPane, busyWaiting, changed)
}

// busyClient serves Sessions over the store at path on the clock busyStart, with epics as the epic store and steps as
// the step client, and returns a client for it.
func busyClient(t *testing.T, path string, epics EpicStoreClient, steps StepClient) orreryv1connect.SessionsClient {
	t.Helper()
	handlerPath, handler := newSessionsHandler(
		store.NewReader(path), "", busyStart, steps, epics, newChangeHub(nil, nil), nil, 0, 0, nil,
	)
	mux := http.NewServeMux()
	mux.Handle(handlerPath, handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return orreryv1connect.NewSessionsClient(srv.Client(), srv.URL)
}

// busyRow is a list row with an id and a state.
type busyRow interface {
	GetId() string
	GetState() string
}

// busyStates maps the id of each row a list answered to its state.
func busyStates[T busyRow](rows []T) map[string]string {
	states := make(map[string]string, len(rows))
	for _, row := range rows {
		states[row.GetId()] = row.GetState()
	}
	return states
}

// expectBusyStates fails the test unless list gives the states of want, and no other row.
func expectBusyStates(t *testing.T, list string, got, want map[string]string) {
	t.Helper()
	if !maps.Equal(got, want) {
		t.Fatalf("%s state = %v, want %v", list, got, want)
	}
}

// expectBusy lists the store through sessions, by Sessions.List and ListHeadless, and fails the test unless the pane and
// the headless session of one case list wantPane and wantHeadless.
func expectBusy(
	t *testing.T, step string, sessions orreryv1connect.SessionsClient, pane, headless, wantPane, wantHeadless string,
) {
	t.Helper()
	panes := pickStates(busyStates(listSessionRows(t, sessions)), pane)
	expectBusyStates(t, step+" Sessions.List", panes, map[string]string{pane: wantPane})
	headlessStates := pickStates(busyStates(listHeadlessRows(t, sessions)), headless)
	expectBusyStates(t, step+" ListHeadless", headlessStates, map[string]string{headless: wantHeadless})
}

// expectRunningCalls fails the test unless steps recorded calls ListSteps requests, each of them for the running steps
// alone.
func expectRunningCalls(t *testing.T, steps *busySteps, calls int) {
	t.Helper()
	if len(steps.calls) != calls {
		t.Fatalf("ListSteps calls = %d, want %d", len(steps.calls), calls)
	}
	for _, req := range steps.calls {
		if !proto.Equal(req, &enginev1.ListStepsRequest{Running: true}) {
			t.Fatalf("ListSteps request = %v, want the running steps", req)
		}
	}
}

// expectBusyUnavailable fails the test unless err is an unavailable answer with the fixed step message.
func expectBusyUnavailable(t *testing.T, call string, err error) {
	t.Helper()
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("%s error = %v, want code %v", call, err, connect.CodeUnavailable)
	}
	if want := "unavailable: " + stepsUnavailable; err.Error() != want {
		t.Fatalf("%s error = %q, want %q", call, err.Error(), want)
	}
}

// TestInboxStatesBusy holds the cases of the working rule: a done conversation that steers a live epic, or is the caller
// of a running step, lists working. The cases share the store seedBusyCases writes. The case whose epic goes final and
// whose step stops runs in order, before the cases that only read, and those run in parallel on their own rows.
func TestInboxStatesBusy(t *testing.T) {
	t.Parallel()
	path := archiveStorePath(t)
	seedBusyCases(t, path)

	t.Run("LiveEpicSteererListsWorking", func(t *testing.T) {
		t.Parallel()
		epics := &busyEpics{summaries: []*enginev1.EpicSummary{
			busyEpic(busyPaneEpic, "accepted", busyLivePane),
			busyEpic(busyHeadEpic, "accepted", busyLiveHead),
		}}
		sessions := busyClient(t, path, epics, &busySteps{})
		expectBusy(t, "live epics", sessions, busyLivePane, busyLiveHead, busyWorking, busyWorking)
	})

	t.Run("RunningStepCallerListsWorking", func(t *testing.T) {
		t.Parallel()
		steps := &busySteps{running: []*enginev1.Step{
			busyStep(busyStepPane, busyRunPane),
			busyStep(busyStepHeadless, busyRunHead),
		}}
		sessions := busyClient(t, path, &busyEpics{}, steps)
		expectBusy(t, "running steps", sessions, busyRunPane, busyRunHead, busyWorking, busyWorking)
		expectRunningCalls(t, steps, 2)
	})

	t.Run("ListsDoneOnceNeitherHolds", func(t *testing.T) {
		epics := &busyEpics{summaries: []*enginev1.EpicSummary{busyEpic(busyPaneEpic, "accepted", busyOncePane)}}
		steps := &busySteps{running: []*enginev1.Step{busyStep(busyStepHeadless, busyOnceHead)}}
		sessions := busyClient(t, path, epics, steps)
		expectBusy(t, "live epic and running step", sessions, busyOncePane, busyOnceHead, busyWorking, busyWorking)

		epics.summaries = []*enginev1.EpicSummary{busyEpic(busyPaneEpic, "released", busyOncePane)}
		expectBusy(t, "epic final, step running", sessions, busyOncePane, busyOnceHead, busyDone, busyWorking)

		steps.running = nil
		expectBusy(t, "epic final, no running step", sessions, busyOncePane, busyOnceHead, busyDone, busyDone)
	})

	t.Run("OnlyDoneRowsChange", func(t *testing.T) {
		t.Parallel()
		epics := &busyEpics{summaries: []*enginev1.EpicSummary{
			busyEpic(busyPaneEpic, "accepted", busyWaitPane),
			busyEpic(busyHeadEpic, "accepted", busyWaitHead),
		}}
		steps := &busySteps{running: []*enginev1.Step{
			busyStep(busyStepPane, busyWaitPane),
			busyStep(busyStepHeadless, busyWaitHead),
		}}
		sessions := busyClient(t, path, epics, steps)
		panes := pickStates(busyStates(listSessionRows(t, sessions)), busyDonePane, busyWaitPane)
		expectBusyStates(t, "Sessions.List", panes, map[string]string{
			busyDonePane: busyDone, busyWaitPane: busyWaiting,
		})
		headless := pickStates(busyStates(listHeadlessRows(t, sessions)), busyDoneHead, busyWaitHead)
		expectBusyStates(t, "ListHeadless", headless, map[string]string{
			busyDoneHead: busyDone, busyWaitHead: busyWaiting,
		})
	})

	t.Run("NilStepsReadsNone", func(t *testing.T) {
		t.Parallel()
		epics := &busyEpics{summaries: []*enginev1.EpicSummary{busyEpic(busyPaneEpic, "accepted", busyNilPane)}}
		sessions := busyClient(t, path, epics, nil)
		expectBusy(t, "nil steps", sessions, busyNilPane, busyNilHead, busyWorking, busyDone)
	})

	t.Run("StepFailureAnswersUnavailable", func(t *testing.T) {
		t.Parallel()
		steps := &busySteps{listErr: connect.NewError(connect.CodeInternal, errors.New("synthetic failure"))}
		sessions := busyClient(t, path, &busyEpics{}, steps)

		_, err := sessions.List(t.Context(), connect.NewRequest(&orreryv1.ListRequest{}))
		expectBusyUnavailable(t, "List", err)
		expectRunningCalls(t, steps, 1)

		_, err = sessions.ListHeadless(t.Context(), connect.NewRequest(&orreryv1.ListHeadlessRequest{}))
		expectBusyUnavailable(t, "ListHeadless", err)
		expectRunningCalls(t, steps, 2)
	})
}
