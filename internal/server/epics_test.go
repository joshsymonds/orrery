package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"connectrpc.com/connect"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
)

// epicsNow returns the fixed clock the epics tests read the records against.
func epicsNow() time.Time {
	return time.Unix(1_700_000_000, 0)
}

// epicsAgo returns the unix seconds d before epicsNow().
func epicsAgo(d time.Duration) int64 {
	return epicsNow().Add(-d).Unix()
}

// epicsFake is a StepClient whose ListSteps answers steps, or listErr when it is set, and counts its calls. The
// epics handler calls no other StepClient method, so those answer an error.
type epicsFake struct {
	steps     []*enginev1.Step
	listErr   error
	listCalls int
}

func (f *epicsFake) StepStatus(
	context.Context,
	*connect.Request[enginev1.StepStatusRequest],
) (*connect.Response[enginev1.StepStatusResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("synthetic unused"))
}

func (f *epicsFake) ListSteps(
	context.Context,
	*connect.Request[enginev1.ListStepsRequest],
) (*connect.Response[enginev1.ListStepsResponse], error) {
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	return connect.NewResponse(&enginev1.ListStepsResponse{Steps: f.steps}), nil
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

// epicsServe serves newEpicsHandler over root and steps with the fixed clock, and returns a generated Epics client.
func epicsServe(t *testing.T, root string, steps StepClient) orreryv1connect.EpicsClient {
	t.Helper()
	path, handler := newEpicsHandler(root, steps, nil, 0, epicsNow)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return orreryv1connect.NewEpicsClient(srv.Client(), srv.URL)
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

func TestEpicsListLiveRuleAndStepCounts(t *testing.T) {
	root := t.TempDir()
	writeHead(t, root, "repoa", "fresh", `{"outcome":"shipped"}`, epicsNow().Add(-time.Hour))
	writeHead(t, root, "repoa", "old", `{"outcome":"shipped"}`, epicsNow().Add(-48*time.Hour))
	writeHead(t, root, "repoa", "stale", `{"outcome":"shipped"}`, epicsNow().Add(-48*time.Hour))
	writeHead(t, root, "repoa", "done", `{"outcome":"released"}`, epicsNow())
	writeHead(t, root, "repob", "quiet", `{"outcome":"released"}`, epicsNow().Add(-48*time.Hour))
	steps := &epicsFake{steps: []*enginev1.Step{
		{Id: "run-a", Epic: "repoa/old", End: "", Started: epicsAgo(3 * time.Hour)},
		{Id: "run-held", Epic: "repoa/old", End: "", Held: true, Started: epicsAgo(2 * time.Hour)},
		{
			Id: "old-ended", Epic: "repoa/old", End: "done",
			Started: epicsAgo(5 * time.Hour), Ended: epicsAgo(4 * time.Hour),
		},
		{Id: "unlabelled", Epic: "", End: "", Started: epicsAgo(time.Hour)},
		{Id: "run-quiet", Epic: "repob/quiet", End: "", Started: epicsAgo(time.Hour)},
		{
			Id: "done-failed", Epic: "repoa/done", End: "failed",
			Started: epicsAgo(time.Hour), Ended: epicsAgo(time.Minute),
		},
	}}
	client := epicsServe(t, root, steps)

	got := epicsList(t, client)
	ids := make([]string, 0, len(got))
	for _, epic := range got {
		ids = append(ids, epic.GetId())
	}
	wantIDs := []string{"repoa/done", "repoa/fresh", "repoa/old", "repoa/stale", "repob/quiet"}
	if !reflect.DeepEqual(ids, wantIDs) {
		t.Fatalf("ids = %v, want %v", ids, wantIDs)
	}

	want := map[string]struct {
		live    bool
		running []string
		ended   int32
	}{
		"repoa/done":  {live: false, running: []string{}, ended: 1},
		"repoa/fresh": {live: true, running: []string{}, ended: 0},
		"repoa/old":   {live: true, running: []string{"run-a", "run-held"}, ended: 1},
		"repoa/stale": {live: false, running: []string{}, ended: 0},
		"repob/quiet": {live: false, running: []string{"run-quiet"}, ended: 0},
	}
	for _, epic := range got {
		w := want[epic.GetId()]
		if epic.GetLive() != w.live {
			t.Errorf("%s live = %v, want %v", epic.GetId(), epic.GetLive(), w.live)
		}
		if ran := epicStepIDs(epic.GetRunningSteps()); !reflect.DeepEqual(ran, w.running) {
			t.Errorf("%s running_steps = %v, want %v", epic.GetId(), ran, w.running)
		}
		if epic.GetEndedSteps() != w.ended {
			t.Errorf("%s ended_steps = %d, want %d", epic.GetId(), epic.GetEndedSteps(), w.ended)
		}
		if epic.GetProblem() != "" {
			t.Errorf("%s problem = %q, want empty", epic.GetId(), epic.GetProblem())
		}
	}

	fresh := got[1]
	if fresh.GetRepository() != "repoa" || fresh.GetSlug() != "fresh" || fresh.GetOutcome() != "shipped" {
		t.Errorf("fresh = repository %q slug %q outcome %q, want repoa fresh shipped",
			fresh.GetRepository(), fresh.GetSlug(), fresh.GetOutcome())
	}
	if fresh.GetChanged() != epicsAgo(time.Hour) {
		t.Errorf("fresh changed = %d, want %d", fresh.GetChanged(), epicsAgo(time.Hour))
	}
	running := got[2].GetRunningSteps()
	if running[0].GetHeld() || !running[1].GetHeld() {
		t.Errorf("held = %v, %v; want false then true", running[0].GetHeld(), running[1].GetHeld())
	}
	if steps.listCalls != 1 {
		t.Errorf("ListSteps calls = %d, want 1", steps.listCalls)
	}
}

func TestEpicsGetReturnsTasksAndSteps(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "repo", "epic")
	writeHead(t, root, "repo", "epic", `{"outcome":"ended_with_gaps"}`, epicsNow())
	writeEffortState(t, dir, "2", `{"tasks":[{"slug":"two-a","subject":"Two A","status":"gated"}]}`)
	writeEffortState(t, dir, "1", `{"tasks":[{"slug":"one-a","subject":"One A","status":"unknown ✓ raw"}]}`)
	steps := &epicsFake{steps: []*enginev1.Step{
		{
			Id: "late", Epic: "repo/epic", Task: "one-a", Agent: "synthetic-agent", End: "done", Status: "ok",
			Started: epicsAgo(time.Hour), Ended: epicsAgo(30 * time.Minute), Doing: "ignored once ended",
			Reason: "synthetic reason",
			Checks: []*enginev1.StepCheck{
				{Round: 1, Name: "build", Ok: true},
				{Round: 2, Name: "test", Ok: false, Detail: "synthetic detail"},
			},
		},
		{
			Id: "early", Epic: "repo/epic", Agent: "synthetic-agent", Held: true,
			Started: epicsAgo(3 * time.Hour), Doing: "waiting",
		},
		{Id: "elsewhere", Epic: "repo/other", Started: epicsAgo(4 * time.Hour)},
		{Id: "unlabelled", Epic: "", Started: epicsAgo(4 * time.Hour)},
	}}
	client := epicsServe(t, root, steps)

	got := epicsGet(t, client, "repo/epic")
	if got.GetEpic().GetId() != "repo/epic" || got.GetEpic().GetRepository() != "repo" ||
		got.GetEpic().GetSlug() != "epic" || got.GetEpic().GetOutcome() != "ended_with_gaps" {
		t.Errorf("epic = %v, want repo/epic of repo, slug epic, outcome ended_with_gaps", got.GetEpic())
	}
	if !got.GetEpic().GetLive() {
		t.Errorf("epic live = false, want true for a running step")
	}
	if ran := epicStepIDs(got.GetEpic().GetRunningSteps()); !reflect.DeepEqual(ran, []string{"early"}) {
		t.Errorf("running_steps = %v, want [early]", ran)
	}
	if got.GetEpic().GetEndedSteps() != 1 {
		t.Errorf("ended_steps = %d, want 1", got.GetEpic().GetEndedSteps())
	}

	wantTasks := []struct {
		effort                int32
		slug, subject, status string
	}{
		{1, "one-a", "One A", "unknown ✓ raw"},
		{2, "two-a", "Two A", "gated"},
	}
	if len(got.GetTasks()) != len(wantTasks) {
		t.Fatalf("tasks = %v, want %d tasks", got.GetTasks(), len(wantTasks))
	}
	for i, w := range wantTasks {
		task := got.GetTasks()[i]
		if task.GetEffort() != w.effort || task.GetSlug() != w.slug || task.GetSubject() != w.subject ||
			task.GetStatus() != w.status {
			t.Errorf("task %d = %v, want effort %d slug %q subject %q status %q",
				i, task, w.effort, w.slug, w.subject, w.status)
		}
	}

	if ids := epicStepIDs(got.GetSteps()); !reflect.DeepEqual(ids, []string{"early", "late"}) {
		t.Fatalf("steps = %v, want [early late] oldest first, with no other epic and no unlabelled step", ids)
	}
	early, late := got.GetSteps()[0], got.GetSteps()[1]
	if early.GetEnd() != "" || !early.GetHeld() || early.GetTask() != "" || early.GetDoing() != "waiting" {
		t.Errorf("early = end %q held %v task %q doing %q, want running, held, no task, waiting",
			early.GetEnd(), early.GetHeld(), early.GetTask(), early.GetDoing())
	}
	if late.GetEnd() != "done" || late.GetTask() != "one-a" || late.GetStatus() != "ok" ||
		late.GetReason() != "synthetic reason" || late.GetAgent() != "synthetic-agent" {
		t.Errorf("late = end %q task %q status %q reason %q agent %q, want done, one-a, ok, "+
			"synthetic reason, synthetic-agent",
			late.GetEnd(), late.GetTask(), late.GetStatus(), late.GetReason(), late.GetAgent())
	}
	checks := late.GetChecks()
	if len(checks) != 2 || checks[0].GetRound() != 1 || checks[0].GetName() != "build" || !checks[0].GetOk() ||
		checks[1].GetRound() != 2 || checks[1].GetName() != "test" || checks[1].GetOk() {
		t.Errorf("checks = %v, want round 1 build ok, round 2 test failed", checks)
	}
	if steps.listCalls != 1 {
		t.Errorf("ListSteps calls = %d, want 1", steps.listCalls)
	}
}

func TestEpicsProblemsNameUnreadableRecords(t *testing.T) {
	root := t.TempDir()
	badDir := filepath.Join(root, "repo", "bad")
	writeHead(t, root, "repo", "bad", `{not json`, epicsNow())
	writeEffortState(t, badDir, "1", `{"tasks":{}}`)
	writeEffortState(t, badDir, "2", `{"tasks":[{"slug":"two-a","subject":"Two A","status":"gated"}]}`)
	writeHead(t, root, "repo", "ok", `{"outcome":"shipped"}`, epicsNow())
	client := epicsServe(t, root, &epicsFake{})

	const wantProblem = "repo/bad/state.json: does not parse; efforts/1/state.json: does not parse"
	for _, epic := range epicsList(t, client) {
		want := ""
		if epic.GetId() == "repo/bad" {
			want = wantProblem
		}
		if epic.GetProblem() != want {
			t.Errorf("List %s problem = %q, want %q", epic.GetId(), epic.GetProblem(), want)
		}
	}

	got := epicsGet(t, client, "repo/bad")
	if got.GetEpic().GetProblem() != wantProblem {
		t.Errorf("Get problem = %q, want %q", got.GetEpic().GetProblem(), wantProblem)
	}
	if len(got.GetTasks()) != 1 || got.GetTasks()[0].GetSlug() != "two-a" {
		t.Errorf("tasks = %v, want only the readable effort's two-a", got.GetTasks())
	}
}

func TestEpicsGetRefusesUnlistedIDs(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "records")
	writeHead(t, root, "repo", "epic", `{"outcome":"shipped"}`, epicsNow())
	// A head outside the record root that "../x" would reach if the id were joined into a path.
	writeHead(t, root, "..", "x", `{"outcome":"shipped"}`, epicsNow())
	client := epicsServe(t, root, &epicsFake{})

	for _, id := range []string{"../x", "repo/missing", ""} {
		_, err := client.Get(context.Background(), connect.NewRequest(&orreryv1.EpicsGetRequest{Id: id}))
		requireConnectError(t, err, connect.CodeNotFound, "epic not found")
	}
}

func TestEpicsStepServiceUnavailable(t *testing.T) {
	root := t.TempDir()
	writeHead(t, root, "repo", "epic", `{"outcome":"shipped"}`, epicsNow())
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
			client := epicsServe(t, root, tc.steps)
			_, err := client.List(context.Background(), connect.NewRequest(&orreryv1.EpicsListRequest{}))
			requireConnectError(t, err, connect.CodeUnavailable, "step service unavailable")
			_, err = client.Get(context.Background(), connect.NewRequest(&orreryv1.EpicsGetRequest{Id: "repo/epic"}))
			requireConnectError(t, err, connect.CodeUnavailable, "step service unavailable")
		})
	}
}

func TestEpicsGetAnswersInternalForEffortBeyondInt32(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "repo", "epic")
	writeHead(t, root, "repo", "epic", `{"outcome":"shipped"}`, epicsNow())
	writeEffortState(t, dir, "3000000000", `{"tasks":[{"slug":"big-a","subject":"Big A","status":"gated"}]}`)
	client := epicsServe(t, root, &epicsFake{})

	_, err := client.Get(context.Background(), connect.NewRequest(&orreryv1.EpicsGetRequest{Id: "repo/epic"}))
	requireConnectError(t, err, connect.CodeInternal, "epics read failed")
}
