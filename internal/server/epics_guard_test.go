package server_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"connectrpc.com/connect"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/server"
)

// epicsStepsProcedures lists the Connect procedures of the Epics service and the Steps service that serve mounts: the
// two record reads and the three calls on one labeled step. None of them is a start.
func epicsStepsProcedures() []string {
	return []string{
		orreryv1connect.EpicsListProcedure,
		orreryv1connect.EpicsGetProcedure,
		orreryv1connect.StepsHoldProcedure,
		orreryv1connect.StepsNudgeProcedure,
		orreryv1connect.StepsStopProcedure,
	}
}

// TestEpicsAndStepsRequireLoginAndClientHeaderOnEveryPath probes every Epics and Steps procedure with the requests the
// guard must refuse, and checks that a well-formed Connect GET carrying login and the client header is answered 405 on
// every path, since none declares NO_SIDE_EFFECTS.
func TestEpicsAndStepsRequireLoginAndClientHeaderOnEveryPath(t *testing.T) {
	handler := newHandler(t, newTmuxClient(t))
	probes := []struct {
		name   string
		method string
		header http.Header
	}{
		{"POST no login", http.MethodPost, connectHeader("", "application/proto", true)},
		{"POST stranger login", http.MethodPost, connectHeader(otherLogin, "application/proto", true)},
		{"POST non-Connect content type", http.MethodPost, connectHeader(allowedLogin, "text/plain", true)},
		{"POST no client header", http.MethodPost, connectHeader(allowedLogin, "application/proto", false)},
		{"GET no client header", http.MethodGet, connectHeader(allowedLogin, "", false)},
		{"PUT no client header", http.MethodPut, connectHeader(allowedLogin, "application/proto", false)},
		{"DELETE no client header", http.MethodDelete, connectHeader(allowedLogin, "application/proto", false)},
	}
	for _, path := range epicsStepsProcedures() {
		for _, probe := range probes {
			rec := serve(t, handler, probe.method, path, probe.header, "")
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s %s with %s: status = %d, want 403", probe.method, path, probe.name, rec.Code)
			}
		}
		getPath := path + "?connect=v1&encoding=json&message=" + url.QueryEscape("{}")
		rec := serve(t, handler, http.MethodGet, getPath, connectHeader(allowedLogin, "", true), "")
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("GET %s with login and client header: status = %d, want 405 (no NO_SIDE_EFFECTS)", path, rec.Code)
		}
	}
}

// wiringStepID is the id of the one step wiringSteps labels, and wiringEpic is that step's epic.
const (
	wiringStepID = "step-labeled"
	wiringEpic   = "repoa/epic-one"
)

// wiringSteps is a StepClient that labels wiringStepID with wiringEpic, lists it, and records each Hold it receives.
// Nudge and Stop answer unimplemented, since the test does not send them.
type wiringSteps struct {
	mu    sync.Mutex
	holds []string
}

func (w *wiringSteps) StepStatus(
	_ context.Context,
	req *connect.Request[enginev1.StepStatusRequest],
) (*connect.Response[enginev1.StepStatusResponse], error) {
	if req.Msg.GetId() != wiringStepID {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("synthetic step not found"))
	}
	step := &enginev1.Step{Id: wiringStepID, Epic: wiringEpic}
	return connect.NewResponse(&enginev1.StepStatusResponse{Step: step}), nil
}

func (w *wiringSteps) ListSteps(
	context.Context,
	*connect.Request[enginev1.ListStepsRequest],
) (*connect.Response[enginev1.ListStepsResponse], error) {
	step := &enginev1.Step{Id: wiringStepID, Epic: wiringEpic}
	return connect.NewResponse(&enginev1.ListStepsResponse{Steps: []*enginev1.Step{step}}), nil
}

func (w *wiringSteps) HoldStep(
	_ context.Context,
	req *connect.Request[enginev1.HoldStepRequest],
) (*connect.Response[enginev1.HoldStepResponse], error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.holds = append(w.holds, req.Msg.GetId())
	return connect.NewResponse(&enginev1.HoldStepResponse{}), nil
}

func (w *wiringSteps) NudgeStep(
	context.Context,
	*connect.Request[enginev1.NudgeStepRequest],
) (*connect.Response[enginev1.NudgeStepResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("synthetic unused"))
}

func (w *wiringSteps) StopStep(
	context.Context,
	*connect.Request[enginev1.StopStepRequest],
) (*connect.Response[enginev1.StopStepResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("synthetic unused"))
}

// wiringHeaders is an http.RoundTripper that sets the login and client headers a phone request carries.
type wiringHeaders struct {
	next http.RoundTripper
}

func (h wiringHeaders) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set(server.HeaderLogin, allowedLogin)
	req.Header.Set(server.HeaderClient, client)
	return h.next.RoundTrip(req)
}

// TestServeAnswersEpicsListAndStepsHoldWithLoginAndClientHeader serves the whole guarded handler with a synthetic
// record root and a labeled step, and checks that Epics.List reads the epic and Steps.Hold forwards to the step client.
func TestServeAnswersEpicsListAndStepsHoldWithLoginAndClientHeader(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "repoa", "epic-one")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir record: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"outcome":"shipped"}`), 0o600); err != nil {
		t.Fatalf("write head: %v", err)
	}
	steps := &wiringSteps{}
	handler, err := server.New(server.Config{
		Logins:  []string{allowedLogin},
		Tmux:    newTmuxClient(t),
		Push:    newPushStore(t),
		Records: root,
		Steps:   steps,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	httpClient := &http.Client{Transport: wiringHeaders{next: http.DefaultTransport}}

	list, err := orreryv1connect.NewEpicsClient(httpClient, srv.URL).
		List(t.Context(), connect.NewRequest(&orreryv1.EpicsListRequest{}))
	if err != nil {
		t.Fatalf("Epics.List: %v", err)
	}
	epics := list.Msg.GetEpics()
	if len(epics) != 1 || epics[0].GetId() != wiringEpic || epics[0].GetOutcome() != "shipped" {
		t.Fatalf("Epics.List = %v, want one epic %s with outcome shipped", epics, wiringEpic)
	}

	_, err = orreryv1connect.NewStepsClient(httpClient, srv.URL).
		Hold(t.Context(), connect.NewRequest(&orreryv1.StepsHoldRequest{Id: wiringStepID}))
	if err != nil {
		t.Fatalf("Steps.Hold: %v", err)
	}
	steps.mu.Lock()
	defer steps.mu.Unlock()
	if len(steps.holds) != 1 || steps.holds[0] != wiringStepID {
		t.Fatalf("HoldStep calls = %v, want [%s]", steps.holds, wiringStepID)
	}
}
