package enginehost

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// resumeSteps is a StepCommands that lists rows and answers each Resume with the reply its id is given. An id with no
// reply resumes. Its other methods come from the nil embedded StepCommands, so a call to one panics and fails the test.
type resumeSteps struct {
	StepCommands

	rows    []store.StepRow
	replies map[string]error

	mu      sync.Mutex
	resumes []string
}

// List returns the rows the fake holds.
func (r *resumeSteps) List(_ context.Context, _, _ string) ([]store.StepRow, error) {
	return slices.Clone(r.rows), nil
}

// Resume records id and returns its reply.
func (r *resumeSteps) Resume(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resumes = append(r.resumes, id)
	return r.replies[id]
}

// resumed returns a copy of the ids Resume was called with, in order.
func (r *resumeSteps) resumed() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.resumes)
}

// limitedStep returns a running step of the test epic that a usage limit holds, with task as its task.
func limitedStep(id, task string) store.StepRow {
	return store.StepRow{ID: id, Epic: epicServiceID, Task: task, LoopLimited: true}
}

func TestEpicResumeResumesLimitedTaskStepsOfUnpausedEpic(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	steps := &resumeSteps{rows: []store.StepRow{limitedStep("step-a", "task-a"), limitedStep("step-b", "task-b")}}
	st := epicTestStore(t)
	client := newEpicsClient(t, startControlServer(t, st, steps))
	createEpicOK(t, st, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))

	resumed, err := client.ResumeEpic(ctx, connect.NewRequest(&v1.ResumeEpicRequest{Id: epicServiceID}))
	if err != nil {
		t.Fatalf("ResumeEpic: %v", err)
	}
	if got := resumed.Msg.GetResumed(); !slices.Equal(got, []string{"step-a", "step-b"}) {
		t.Fatalf("resumed = %v, want [step-a step-b]", got)
	}
	if resumed.Msg.GetEpic().GetPaused() {
		t.Fatalf("paused = true in the ResumeEpic reply, want false")
	}
	if got := steps.resumed(); !slices.Equal(got, []string{"step-a", "step-b"}) {
		t.Fatalf("steps resumed = %v, want [step-a step-b]", got)
	}
	if got := getEpicOK(t, client); !proto.Equal(got, resumed.Msg.GetEpic()) {
		t.Fatalf("GetEpic = %v, want the resumed epic %v", got, resumed.Msg.GetEpic())
	}
}

func TestEpicResumeClearsPauseAndResumesLimitedStep(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	steps := &resumeSteps{rows: []store.StepRow{limitedStep("step-a", "task-a")}}
	st := epicTestStore(t)
	client := newEpicsClient(t, startControlServer(t, st, steps))
	createEpicOK(t, st, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	if _, err := client.PauseEpic(ctx, connect.NewRequest(&v1.PauseEpicRequest{Id: epicServiceID})); err != nil {
		t.Fatalf("PauseEpic: %v", err)
	}

	resumed, err := client.ResumeEpic(ctx, connect.NewRequest(&v1.ResumeEpicRequest{Id: epicServiceID}))
	if err != nil {
		t.Fatalf("ResumeEpic: %v", err)
	}
	if resumed.Msg.GetEpic().GetPaused() {
		t.Fatalf("paused = true in the ResumeEpic reply, want the pause cleared")
	}
	if got := resumed.Msg.GetResumed(); !slices.Equal(got, []string{"step-a"}) {
		t.Fatalf("resumed = %v, want [step-a]", got)
	}
	if got := steps.resumed(); !slices.Equal(got, []string{"step-a"}) {
		t.Fatalf("steps resumed = %v, want [step-a]", got)
	}
}

// TestEpicResumeSkipsStepsResumedOrEndedAfterTheList: a step that a resume finds no longer limited, or ended, after the
// list is neither resumed nor an error, and every listed step is tried in order.
func TestEpicResumeSkipsStepsResumedOrEndedAfterTheList(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	steps := &resumeSteps{
		rows: []store.StepRow{limitedStep("step-a", "task-a"), limitedStep("step-b", "task-b"),
			limitedStep("step-c", "task-c")},
		replies: map[string]error{
			"step-a": fmt.Errorf("resume step step-a: %w", ErrStepNotLimited),
			"step-c": ErrStepNotRunning,
		},
	}
	st := epicTestStore(t)
	client := newEpicsClient(t, startControlServer(t, st, steps))
	createEpicOK(t, st, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))

	resumed, err := client.ResumeEpic(ctx, connect.NewRequest(&v1.ResumeEpicRequest{Id: epicServiceID}))
	if err != nil {
		t.Fatalf("ResumeEpic: %v", err)
	}
	if got := resumed.Msg.GetResumed(); !slices.Equal(got, []string{"step-b"}) {
		t.Fatalf("resumed = %v, want [step-b]", got)
	}
	if got := steps.resumed(); !slices.Equal(got, []string{"step-a", "step-b", "step-c"}) {
		t.Fatalf("steps resumed = %v, want all three tried in order", got)
	}
}

func TestEpicResumeSkipsPlannerTurnStep(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	steps := &resumeSteps{rows: []store.StepRow{limitedStep("planner-step", ""), limitedStep("step-a", "task-a")}}
	st := epicTestStore(t)
	client := newEpicsClient(t, startControlServer(t, st, steps))
	createEpicOK(t, st, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))

	resumed, err := client.ResumeEpic(ctx, connect.NewRequest(&v1.ResumeEpicRequest{Id: epicServiceID}))
	if err != nil {
		t.Fatalf("ResumeEpic: %v", err)
	}
	if got := resumed.Msg.GetResumed(); !slices.Equal(got, []string{"step-a"}) {
		t.Fatalf("resumed = %v, want [step-a]", got)
	}
	if got := steps.resumed(); !slices.Equal(got, []string{"step-a"}) {
		t.Fatalf("steps resumed = %v, want the planner turn's step left alone", got)
	}
}

func TestEpicResumeRefusesUnpausedEpicWithoutLimitedTaskStep(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	steps := &resumeSteps{rows: []store.StepRow{limitedStep("planner-step", "")}}
	st := epicTestStore(t)
	client := newEpicsClient(t, startControlServer(t, st, steps))
	createEpicOK(t, st, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))

	_, err := client.ResumeEpic(ctx, connect.NewRequest(&v1.ResumeEpicRequest{Id: epicServiceID}))
	wantConnectError(t, err, connect.CodeFailedPrecondition, "epic "+epicServiceID+" is not paused")
	if got := steps.resumed(); len(got) != 0 {
		t.Fatalf("steps resumed = %v, want none", got)
	}
}

func TestEpicResumeUnknownEpicIsNotFound(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st := epicTestStore(t)
	client := newEpicsClient(t, startControlServer(t, st, &resumeSteps{}))

	_, err := client.ResumeEpic(ctx, connect.NewRequest(&v1.ResumeEpicRequest{Id: epicServiceID}))
	wantConnectError(t, err, connect.CodeNotFound, "id: no epic "+epicServiceID)
}

func TestEpicResumeReturnsOtherResumeErrors(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	steps := &resumeSteps{
		rows:    []store.StepRow{limitedStep("step-a", "task-a")},
		replies: map[string]error{"step-a": errors.New("boom")},
	}
	st := epicTestStore(t)
	client := newEpicsClient(t, startControlServer(t, st, steps))
	createEpicOK(t, st, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))

	_, err := client.ResumeEpic(ctx, connect.NewRequest(&v1.ResumeEpicRequest{Id: epicServiceID}))
	wantConnectError(t, err, connect.CodeInternal, "boom")
}
