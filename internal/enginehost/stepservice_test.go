package enginehost

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"github.com/joshsymonds/orrery/internal/agents"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// waitCall is one StepCommands.Wait invocation with the arguments it received.
type waitCall struct {
	ids     []string
	timeout time.Duration
}

// listCall is one StepCommands.List invocation with the labels it received.
type listCall struct {
	epic, task string
}

// nudgeCall is one StepCommands.Nudge invocation with the arguments it received.
type nudgeCall struct {
	id, text string
}

// fakeStepCommands records every StepCommands call and answers with the configured values. held lists the held step
// ids. err is returned by every method that returns an error.
type fakeStepCommands struct {
	mu      sync.Mutex
	starts  []StepStart
	stops   []string
	status  []string
	waits   []waitCall
	startID string
	row     store.StepRow
	ended   *store.StepRow
	rows    []store.StepRow
	listed  []store.StepRow
	lists   []listCall
	holds   []string
	nudges  []nudgeCall
	held    map[string]bool
	err     error
}

func (f *fakeStepCommands) Start(_ context.Context, start StepStart) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts = append(f.starts, start)
	return f.startID, f.err
}

func (f *fakeStepCommands) Stop(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops = append(f.stops, id)
	return f.err
}

func (f *fakeStepCommands) Status(_ context.Context, id string) (store.StepRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status = append(f.status, id)
	return f.row, f.err
}

func (f *fakeStepCommands) Wait(
	_ context.Context,
	ids []string,
	timeout time.Duration,
) (*store.StepRow, []store.StepRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.waits = append(f.waits, waitCall{ids: slices.Clone(ids), timeout: timeout})
	return f.ended, f.rows, f.err
}

func (f *fakeStepCommands) List(_ context.Context, epic, task string) ([]store.StepRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lists = append(f.lists, listCall{epic: epic, task: task})
	return f.listed, f.err
}

func (f *fakeStepCommands) Hold(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.holds = append(f.holds, id)
	return f.err
}

func (f *fakeStepCommands) Held(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.held[id]
}

func (f *fakeStepCommands) Nudge(_ context.Context, id, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nudges = append(f.nudges, nudgeCall{id: id, text: text})
	return f.err
}

func (f *fakeStepCommands) recordedStarts() []StepStart {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.starts)
}

func (f *fakeStepCommands) recordedStops() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.stops)
}

func (f *fakeStepCommands) recordedStatus() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.status)
}

func (f *fakeStepCommands) recordedWaits() []waitCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.waits)
}

func (f *fakeStepCommands) recordedLists() []listCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.lists)
}

func (f *fakeStepCommands) recordedHolds() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.holds)
}

func (f *fakeStepCommands) recordedNudges() []nudgeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.nudges)
}

func (f *fakeStepCommands) setErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// newStepClient returns a Steps client that dials the socket at path.
func newStepClient(t *testing.T, path string) enginev1connect.StepsClient {
	t.Helper()
	return enginev1connect.NewStepsClient(unixHTTPClient(t, path), "http://steps.invalid")
}

// fullStepRow is a step row with every field set, and fullStep is the Step its reply carries.
func fullStepRow() store.StepRow {
	return store.StepRow{
		ID:        "s1",
		Agent:     "reviewer",
		Prompt:    "check the change",
		Schema:    `{"type":"object"}`,
		Workspace: "/work",
		Revision:  "abc123",
		Worktree:  "/wt/s1",
		DoneValue: "pass",
		ReadOnly:  true,
		Owned:     []string{"a.go"},
		Epic:      "orrery",
		Task:      "h3e1",
		Gate:      []string{"go", "test"},
		GateLimit: time.Minute,
		Started:   time.Unix(1700000000, 0),
		Checks: []store.StepCheck{
			{Round: 1, Name: "gate", OK: false, Detail: "exit 1"},
			{Round: 2, Name: "gate", OK: true, Detail: ""},
		},
		Result: "all good",
		Status: "pass",
		Doing:  "reading the diff",
		End:    "done",
		Reason: "returned",
		Ended:  time.Unix(1700000600, 0),
	}
}

func fullStep() *v1.Step {
	return &v1.Step{
		Id:        "s1",
		Agent:     "reviewer",
		Epic:      "orrery",
		Task:      "h3e1",
		Workspace: "/work",
		Revision:  "abc123",
		ReadOnly:  true,
		Worktree:  "/wt/s1",
		End:       "done",
		Status:    "pass",
		Result:    "all good",
		Doing:     "reading the diff",
		Checks: []*v1.StepCheck{
			{Round: 1, Name: "gate", Ok: false, Detail: "exit 1"},
			{Round: 2, Name: "gate", Ok: true, Detail: ""},
		},
		Reason:  "returned",
		Started: 1700000000,
		Ended:   1700000600,
	}
}

// callAllSteps calls each of the seven step RPCs once with fixed arguments and returns each error in order: StartStep,
// StopStep, StepStatus, WaitSteps, ListSteps, HoldStep, NudgeStep.
func callAllSteps(t *testing.T, client enginev1connect.StepsClient) []error {
	t.Helper()
	ctx := context.Background()
	_, startErr := client.StartStep(ctx, connect.NewRequest(&v1.StartStepRequest{
		Agent: "reviewer", Prompt: "check", Schema: "{}", Workspace: "/work",
	}))
	_, stopErr := client.StopStep(ctx, connect.NewRequest(&v1.StopStepRequest{Id: "s1"}))
	_, statusErr := client.StepStatus(ctx, connect.NewRequest(&v1.StepStatusRequest{Id: "s1"}))
	_, waitErr := client.WaitSteps(ctx, connect.NewRequest(&v1.WaitStepsRequest{Ids: []string{"s1"}}))
	_, listErr := client.ListSteps(ctx, connect.NewRequest(&v1.ListStepsRequest{Epic: "orrery"}))
	_, holdErr := client.HoldStep(ctx, connect.NewRequest(&v1.HoldStepRequest{Id: "s1"}))
	_, nudgeErr := client.NudgeStep(ctx, connect.NewRequest(&v1.NudgeStepRequest{Id: "s1", Text: "continue"}))
	return []error{startErr, stopErr, statusErr, waitErr, listErr, holdErr, nudgeErr}
}

func TestStepStartForwardsEveryField(t *testing.T) {
	fake := &fakeStepCommands{startID: "0123456789abcdef0123456789abcdef"}
	path, stop := startServerWithSteps(t, nil, fake)
	client := newStepClient(t, path)

	started, err := client.StartStep(context.Background(), connect.NewRequest(&v1.StartStepRequest{
		Agent:             "reviewer",
		Prompt:            "check the change",
		Schema:            `{"type":"object"}`,
		Workspace:         "/work",
		Revision:          "abc123",
		ReadOnly:          true,
		OwnedPaths:        []string{"a.go", "b/c.go"},
		GateName:          "lint",
		GatesFile:         "/repo/gates.toml",
		DoneValue:         "pass",
		ModelLimitSeconds: 90,
		Epic:              "orrery",
		Task:              "h3e1",
	}))
	if err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	if err := stop(); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	if got := started.Msg.GetId(); got != fake.startID {
		t.Fatalf("StartStep id = %q, want %q", got, fake.startID)
	}
	want := []StepStart{{
		Agent:      "reviewer",
		Prompt:     "check the change",
		Schema:     `{"type":"object"}`,
		Workspace:  "/work",
		Revision:   "abc123",
		ReadOnly:   true,
		Owned:      []string{"a.go", "b/c.go"},
		GateName:   "lint",
		GatesFile:  "/repo/gates.toml",
		ModelLimit: 90 * time.Second,
		DoneValue:  "pass",
		Epic:       "orrery",
		Task:       "h3e1",
	}}
	if got := fake.recordedStarts(); !reflect.DeepEqual(got, want) {
		t.Fatalf("StartStep reached commands as %+v, want %+v", got, want)
	}
}

func TestStartStepRefusesGateArgvAndGateLimit(t *testing.T) {
	cases := []struct {
		name  string
		gate  []string
		limit uint32
	}{
		{name: "gate argv", gate: []string{"go", "test"}},
		{name: "gate limit", limit: 90},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeStepCommands{startID: "0123456789abcdef0123456789abcdef"}
			path, stop := startServerWithSteps(t, nil, fake)
			client := newStepClient(t, path)

			_, err := client.StartStep(context.Background(), connect.NewRequest(&v1.StartStepRequest{
				Agent: "reviewer", Prompt: "check", Schema: "{}", Workspace: "/work",
				Gate: tc.gate, GateLimitSeconds: tc.limit,
			}))
			if stopErr := stop(); stopErr != nil {
				t.Fatalf("Serve: %v", stopErr)
			}
			if got := connect.CodeOf(err); got != connect.CodeInvalidArgument {
				t.Fatalf("StartStep code = %v, want %v (error %v)", got, connect.CodeInvalidArgument, err)
			}
			var connectErr *connect.Error
			if !errors.As(err, &connectErr) || !strings.Contains(connectErr.Message(), ErrBadGate.Error()) {
				t.Fatalf("StartStep error = %v, want a connect error naming %q", err, ErrBadGate)
			}
			if got := fake.recordedStarts(); len(got) != 0 {
				t.Fatalf("refused StartStep reached commands as %+v, want no start", got)
			}
		})
	}
}

func TestStartStepZeroModelLimitHandsZero(t *testing.T) {
	fake := &fakeStepCommands{startID: "0123456789abcdef0123456789abcdef"}
	path, stop := startServerWithSteps(t, nil, fake)
	client := newStepClient(t, path)

	if _, err := client.StartStep(context.Background(), connect.NewRequest(&v1.StartStepRequest{
		Agent: "reviewer", Prompt: "check", Schema: "{}", Workspace: "/work",
	})); err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	if err := stop(); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	starts := fake.recordedStarts()
	if len(starts) != 1 || starts[0].ModelLimit != 0 {
		t.Fatalf("StartStep reached commands as %+v, want one start with a zero ModelLimit", starts)
	}
}

func TestStepStatusReturnsEveryRowField(t *testing.T) {
	fake := &fakeStepCommands{row: fullStepRow()}
	path, stop := startServerWithSteps(t, nil, fake)
	client := newStepClient(t, path)

	status, err := client.StepStatus(context.Background(), connect.NewRequest(&v1.StepStatusRequest{Id: "s1"}))
	if err != nil {
		t.Fatalf("StepStatus: %v", err)
	}
	if err := stop(); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	if got := fake.recordedStatus(); !slices.Equal(got, []string{"s1"}) {
		t.Fatalf("StepStatus reached commands with ids %v, want [s1]", got)
	}
	if got := status.Msg.GetStep(); !proto.Equal(got, fullStep()) {
		t.Fatalf("StepStatus step = %v, want %v", got, fullStep())
	}
}

func TestStepStatusReportsRunningStepWithZeroEnded(t *testing.T) {
	fake := &fakeStepCommands{row: runningStepRow("s1")}
	path, _ := startServerWithSteps(t, nil, fake)
	client := newStepClient(t, path)

	status, err := client.StepStatus(context.Background(), connect.NewRequest(&v1.StepStatusRequest{Id: "s1"}))
	if err != nil {
		t.Fatalf("StepStatus: %v", err)
	}
	got := status.Msg.GetStep()
	if got.GetEnded() != 0 || got.GetEnd() != "" || got.GetReason() != "" || got.GetStatus() != "" {
		t.Fatalf("running step = %v, want zero ended, end, reason and status", got)
	}
	if got.GetStarted() != 1700000000 {
		t.Fatalf("running step started = %d, want 1700000000", got.GetStarted())
	}
}

func TestStepStatusRefusesCheckRoundOutsideUint32AsInternal(t *testing.T) {
	row := fullStepRow()
	row.Checks = []store.StepCheck{{Round: 1 << 40, Name: "gate"}}
	fake := &fakeStepCommands{row: row}
	path, _ := startServerWithSteps(t, nil, fake)
	client := newStepClient(t, path)

	_, err := client.StepStatus(context.Background(), connect.NewRequest(&v1.StepStatusRequest{Id: "s1"}))
	if got := connect.CodeOf(err); got != connect.CodeInternal {
		t.Fatalf("StepStatus code = %v, want %v (error %v)", got, connect.CodeInternal, err)
	}
}

func TestStepStatusReportsHeldStep(t *testing.T) {
	fake := &fakeStepCommands{row: runningStepRow("s1"), held: map[string]bool{"s1": true}}
	path, stop := startServerWithSteps(t, nil, fake)
	client := newStepClient(t, path)

	status, err := client.StepStatus(context.Background(), connect.NewRequest(&v1.StepStatusRequest{Id: "s1"}))
	if err != nil {
		t.Fatalf("StepStatus: %v", err)
	}
	if err := stop(); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if got := status.Msg.GetStep().GetHeld(); !got {
		t.Fatalf("StepStatus held = %v, want true", got)
	}
}

func TestWaitStepsReportsHeldRows(t *testing.T) {
	fake := &fakeStepCommands{rows: []store.StepRow{runningStepRow("s1")}, held: map[string]bool{"s1": true}}
	path, stop := startServerWithSteps(t, nil, fake)
	client := newStepClient(t, path)

	waited, err := client.WaitSteps(context.Background(), connect.NewRequest(&v1.WaitStepsRequest{
		Ids:            []string{"s1"},
		TimeoutSeconds: 1,
	}))
	if err != nil {
		t.Fatalf("WaitSteps: %v", err)
	}
	if err := stop(); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	steps := waited.Msg.GetSteps()
	if len(steps) != 1 || !steps[0].GetHeld() {
		t.Fatalf("WaitSteps steps = %v, want one held step", steps)
	}
}

func TestListStepsForwardsLabelsAndReturnsRowsInOrder(t *testing.T) {
	fake := &fakeStepCommands{
		listed: []store.StepRow{fullStepRow(), runningStepRow("s2")},
		held:   map[string]bool{"s2": true},
	}
	path, stop := startServerWithSteps(t, nil, fake)
	client := newStepClient(t, path)

	listed, err := client.ListSteps(context.Background(), connect.NewRequest(&v1.ListStepsRequest{
		Epic: "orrery",
		Task: "h3e1",
	}))
	if err != nil {
		t.Fatalf("ListSteps: %v", err)
	}
	if err := stop(); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	if got, want := fake.recordedLists(), []listCall{{epic: "orrery", task: "h3e1"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ListSteps reached commands as %+v, want %+v", got, want)
	}
	heldSecond := runningStep("s2")
	heldSecond.Held = true
	wantSteps := []*v1.Step{fullStep(), heldSecond}
	got := listed.Msg.GetSteps()
	if len(got) != len(wantSteps) {
		t.Fatalf("ListSteps returned %d steps, want %d", len(got), len(wantSteps))
	}
	for i := range wantSteps {
		if !proto.Equal(got[i], wantSteps[i]) {
			t.Fatalf("ListSteps step %d = %v, want %v", i, got[i], wantSteps[i])
		}
	}
}

func TestHoldStepForwardsID(t *testing.T) {
	fake := &fakeStepCommands{}
	path, stop := startServerWithSteps(t, nil, fake)
	client := newStepClient(t, path)

	if _, err := client.HoldStep(context.Background(), connect.NewRequest(&v1.HoldStepRequest{Id: "s9"})); err != nil {
		t.Fatalf("HoldStep: %v", err)
	}
	if err := stop(); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if got := fake.recordedHolds(); !slices.Equal(got, []string{"s9"}) {
		t.Fatalf("HoldStep reached commands with ids %v, want [s9]", got)
	}
}

func TestNudgeStepForwardsIDAndText(t *testing.T) {
	fake := &fakeStepCommands{}
	path, stop := startServerWithSteps(t, nil, fake)
	client := newStepClient(t, path)

	if _, err := client.NudgeStep(context.Background(), connect.NewRequest(&v1.NudgeStepRequest{
		Id:   "s9",
		Text: "keep going",
	})); err != nil {
		t.Fatalf("NudgeStep: %v", err)
	}
	if err := stop(); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	want := []nudgeCall{{id: "s9", text: "keep going"}}
	if got := fake.recordedNudges(); !reflect.DeepEqual(got, want) {
		t.Fatalf("NudgeStep reached commands as %+v, want %+v", got, want)
	}
}

func TestStopStepForwardsID(t *testing.T) {
	fake := &fakeStepCommands{}
	path, stop := startServerWithSteps(t, nil, fake)
	client := newStepClient(t, path)

	if _, err := client.StopStep(context.Background(), connect.NewRequest(&v1.StopStepRequest{Id: "s9"})); err != nil {
		t.Fatalf("StopStep: %v", err)
	}
	if err := stop(); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if got := fake.recordedStops(); !slices.Equal(got, []string{"s9"}) {
		t.Fatalf("StopStep reached commands with ids %v, want [s9]", got)
	}
}

func TestWaitStepsTimeoutReturnsNoEndedStep(t *testing.T) {
	first := runningStepRow("s1")
	second := runningStepRow("s2")
	fake := &fakeStepCommands{ended: nil, rows: []store.StepRow{first, second}}
	path, stop := startServerWithSteps(t, nil, fake)
	client := newStepClient(t, path)

	waited, err := client.WaitSteps(context.Background(), connect.NewRequest(&v1.WaitStepsRequest{
		Ids:            []string{"s1", "s2"},
		TimeoutSeconds: 5,
	}))
	if err != nil {
		t.Fatalf("WaitSteps: %v", err)
	}
	if err := stop(); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	if waited.Msg.Ended != nil {
		t.Fatalf("WaitSteps at timeout ended = %v, want unset", waited.Msg.GetEnded())
	}
	wantWaits := []waitCall{{ids: []string{"s1", "s2"}, timeout: 5 * time.Second}}
	if got := fake.recordedWaits(); !reflect.DeepEqual(got, wantWaits) {
		t.Fatalf("WaitSteps reached commands as %+v, want %+v", got, wantWaits)
	}
	wantSteps := []*v1.Step{runningStep("s1"), runningStep("s2")}
	got := waited.Msg.GetSteps()
	if len(got) != len(wantSteps) {
		t.Fatalf("WaitSteps returned %d steps, want %d", len(got), len(wantSteps))
	}
	for i := range wantSteps {
		if !proto.Equal(got[i], wantSteps[i]) {
			t.Fatalf("WaitSteps step %d = %v, want %v", i, got[i], wantSteps[i])
		}
	}
}

func TestWaitStepsReturnsFirstEndedStep(t *testing.T) {
	ended := fullStepRow()
	fake := &fakeStepCommands{ended: &ended, rows: []store.StepRow{ended, runningStepRow("s2")}}
	path, stop := startServerWithSteps(t, nil, fake)
	client := newStepClient(t, path)

	waited, err := client.WaitSteps(context.Background(), connect.NewRequest(&v1.WaitStepsRequest{
		Ids:            []string{"s1", "s2"},
		TimeoutSeconds: 600,
	}))
	if err != nil {
		t.Fatalf("WaitSteps: %v", err)
	}
	if err := stop(); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	if got := waited.Msg.GetEnded(); !proto.Equal(got, fullStep()) {
		t.Fatalf("WaitSteps ended = %v, want %v", got, fullStep())
	}
	if got := len(waited.Msg.GetSteps()); got != 2 {
		t.Fatalf("WaitSteps returned %d steps, want 2", got)
	}
	if got := fake.recordedWaits()[0].timeout; got != 600*time.Second {
		t.Fatalf("WaitSteps timeout = %v, want 600s", got)
	}
}

func TestWaitStepsZeroTimeoutWaitsNotAtAll(t *testing.T) {
	fake := &fakeStepCommands{}
	path, stop := startServerWithSteps(t, nil, fake)
	client := newStepClient(t, path)

	if _, err := client.WaitSteps(context.Background(), connect.NewRequest(&v1.WaitStepsRequest{
		Ids: []string{"s1"},
	})); err != nil {
		t.Fatalf("WaitSteps: %v", err)
	}
	if err := stop(); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if got := fake.recordedWaits(); len(got) != 1 || got[0].timeout != 0 {
		t.Fatalf("WaitSteps calls = %+v, want one call with a zero timeout", got)
	}
}

func TestStepErrorsMapToConnectCodes(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code connect.Code
	}{
		{name: "cap refusal", err: ErrCapReached, code: connect.CodeResourceExhausted},
		{name: "no such step", err: wrapf(store.ErrNoStep, "read step s1"), code: connect.CodeNotFound},
		{name: "empty prompt", err: ErrEmptyPrompt, code: connect.CodeInvalidArgument},
		{name: "bad schema", err: ErrBadSchema, code: connect.CodeInvalidArgument},
		{name: "bad agent", err: ErrBadAgent, code: connect.CodeInvalidArgument},
		{name: "unresolved agent", err: wrapf(agents.ErrUnresolved, "reviewer"), code: connect.CodeInvalidArgument},
		{name: "bad directory", err: ErrBadDir, code: connect.CodeInvalidArgument},
		{name: "bad owned path", err: ErrBadOwned, code: connect.CodeInvalidArgument},
		{name: "bad gate", err: ErrBadGate, code: connect.CodeInvalidArgument},
		{name: "not a git tree", err: ErrNotGit, code: connect.CodeInvalidArgument},
		{name: "main worktree", err: ErrMainWorktree, code: connect.CodeInvalidArgument},
		{name: "no turn running", err: ErrStepNoTurn, code: connect.CodeFailedPrecondition},
		{name: "step already held", err: ErrStepHeld, code: connect.CodeFailedPrecondition},
		{name: "step running its gate", err: ErrStepGating, code: connect.CodeFailedPrecondition},
		{name: "step not running", err: ErrStepNotRunning, code: connect.CodeFailedPrecondition},
		{name: "other error is internal", err: errors.New("disk on fire"), code: connect.CodeInternal},
	}
	fake := &fakeStepCommands{}
	path, _ := startServerWithSteps(t, nil, fake)
	client := newStepClient(t, path)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake.setErr(tc.err)
			for i, err := range callAllSteps(t, client) {
				if err == nil {
					t.Fatalf("step RPC %d returned no error, want %v", i, tc.code)
				}
				if got := connect.CodeOf(err); got != tc.code {
					t.Fatalf("step RPC %d code = %v, want %v (error %v)", i, got, tc.code, err)
				}
				var connectErr *connect.Error
				if !errors.As(err, &connectErr) {
					t.Fatalf("step RPC %d error %T is not a connect error", i, err)
				}
				if got := connectErr.Message(); got != tc.err.Error() {
					t.Fatalf("step RPC %d message = %q, want %q", i, got, tc.err.Error())
				}
			}
		})
	}
}

func TestNilStepCommandsRefusesEveryStepRPCUnavailable(t *testing.T) {
	path, _ := startServerWithSteps(t, &fakeCommands{}, nil)
	client := newStepClient(t, path)

	for i, err := range callAllSteps(t, client) {
		if got := connect.CodeOf(err); got != connect.CodeUnavailable {
			t.Fatalf("step RPC %d code = %v, want %v (error %v)", i, got, connect.CodeUnavailable, err)
		}
		var connectErr *connect.Error
		if !errors.As(err, &connectErr) {
			t.Fatalf("step RPC %d error %T is not a connect error", i, err)
		}
		if got := connectErr.Message(); got != stepsUnavailableMessage {
			t.Fatalf("step RPC %d message = %q, want %q", i, got, stepsUnavailableMessage)
		}
	}
}

func TestEngineRPCsStillWorkWhenStepsAreServed(t *testing.T) {
	engine := &fakeCommands{startID: "0123456789abcdef0123456789abcdef"}
	steps := &fakeStepCommands{row: fullStepRow()}
	path, stop := startServerWithSteps(t, engine, steps)
	ctx := context.Background()

	started, err := enginev1connect.NewEngineClient(unixHTTPClient(t, path), "http://engine.invalid").Start(
		ctx,
		connect.NewRequest(&v1.StartRequest{Dir: "/work", Prompt: "hello"}),
	)
	if err != nil {
		t.Fatalf("Engine Start: %v", err)
	}
	status, err := newStepClient(t, path).StepStatus(ctx, connect.NewRequest(&v1.StepStatusRequest{Id: "s1"}))
	if err != nil {
		t.Fatalf("StepStatus: %v", err)
	}
	if err := stop(); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	if got := started.Msg.GetId(); got != engine.startID {
		t.Fatalf("Engine Start id = %q, want %q", got, engine.startID)
	}
	if got := engine.recorded(); !slices.Equal(got, []call{{method: "Start", dir: "/work", prompt: "hello"}}) {
		t.Fatalf("engine commands received %+v", got)
	}
	if got := status.Msg.GetStep(); !proto.Equal(got, fullStep()) {
		t.Fatalf("StepStatus step = %v, want %v", got, fullStep())
	}
}

// runningStepRow returns the row of a step with id that has not ended: its end, status, reason and ended time are
// clear. runningStep is the Step its reply carries.
func runningStepRow(id string) store.StepRow {
	row := fullStepRow()
	row.ID = id
	row.End = ""
	row.Status = ""
	row.Reason = ""
	row.Ended = time.Time{}
	return row
}

func runningStep(id string) *v1.Step {
	step := fullStep()
	step.Id = id
	step.End = ""
	step.Status = ""
	step.Reason = ""
	step.Ended = 0
	return step
}
