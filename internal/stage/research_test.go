package stage

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/enginehost"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
	"github.com/joshsymonds/orrery/internal/testrunner"
)

// stageDesign is the id of the design the Stage research tests store their steps on.
const stageDesign = "design-one"

// stageResearchRoles is the role file the Stage research tests run their roles under.
const stageResearchRoles = `scout = "scout-agent"` + "\n" + `test_runner = "runner-agent"` + "\n"

// stageResearchReturn is a scout's return and stageResearchRunnerReturn a test runner's, as the steps keep them.
const (
	stageResearchReturn       = `{"status":"done","answer":"Foo calls Bar.","evidence":[],"not_inspected":[]}`
	stageResearchRunnerReturn = `{"exit_code":0,"summary":"all tests pass","output":"ok  \tpkg\t0.01s"}`
)

// stageResearchNow is the time the Stage tests stamp their records with.
func stageResearchNow() time.Time {
	return time.Unix(1_700_000_000, 0)
}

// fakeStarter is a StepStarter that records each start it is given and stores a step row for it, as Steps.Start does,
// under an id it counts up from. A starter with err set refuses every start with err and stores nothing.
type fakeStarter struct {
	store  *store.Store
	err    error
	starts []enginehost.StepStart
}

// Start records start and stores its step row, and returns the row's id.
func (f *fakeStarter) Start(ctx context.Context, start enginehost.StepStart) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	id := fmt.Sprintf("%032x", len(f.starts)+1)
	f.starts = append(f.starts, start)
	add := store.StepAdd{
		ID: id, Agent: start.Agent, Prompt: start.Prompt, Schema: start.Schema, Workspace: start.Workspace,
		Revision: start.Revision, ReadOnly: start.ReadOnly, DoneValue: start.DoneValue, Epic: start.Epic,
		Task: start.Task, Caller: start.Caller,
	}
	if err := f.store.ApplyEngine(ctx, []store.EngineOp{add}, stageResearchNow()); err != nil {
		return "", fmt.Errorf("store step %s: %w", id, err)
	}
	return id, nil
}

// stageResearchFixture is a Stage service over a store that holds the design stageDesign, with a fake starter that
// records the starts the service makes.
type stageResearchFixture struct {
	service *Service
	store   *store.Store
	starter *fakeStarter
	getenv  func(string) string
}

// openStageStore returns a store that holds no rows, closed at cleanup.
func openStageStore(t *testing.T) *store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	s, err := store.Open(t.Context(), path, storetest.NoSync())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return s
}

// newStageResearchFixture returns a fixture whose store holds the design stageDesign.
func newStageResearchFixture(t *testing.T) stageResearchFixture {
	t.Helper()
	s := openStageStore(t)
	if err := s.CreateDesign(t.Context(), store.Design{
		ID: stageDesign, Title: "Design one", Conversation: "conversation-one", Created: stageResearchNow(),
	}); err != nil {
		t.Fatalf("CreateDesign: %v", err)
	}
	getenv := stageRoleHome(t, stageResearchRoles)
	starter := &fakeStarter{store: s}
	service := NewService(Config{Store: s, Steps: starter, Now: stageResearchNow, Getenv: getenv})
	return stageResearchFixture{service: service, store: s, starter: starter, getenv: getenv}
}

// stageSteps returns the steps GetStage reports for stageDesign.
func stageSteps(t *testing.T, service *Service) []*v1.StageStep {
	t.Helper()
	got, err := service.GetStage(t.Context(), connect.NewRequest(&v1.GetStageRequest{Design: stageDesign}))
	if err != nil {
		t.Fatalf("GetStage: %v", err)
	}
	return got.Msg.GetStage().GetSteps()
}

// applyStageSteps applies ops in one engine write.
func applyStageSteps(t *testing.T, s *store.Store, ops ...store.EngineOp) {
	t.Helper()
	if err := s.ApplyEngine(t.Context(), ops, stageResearchNow()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
}

func TestStageResearchStartsScoutForDesign(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	ctx := t.Context()
	const question = "Does Foo call Bar?"
	req := connect.NewRequest(&v1.ResearchRequest{
		Design: stageDesign, Repository: "/repo", Revision: "abc1234", Question: question,
	})
	req.Header().Set(protocol.HeaderSession, "session-one")

	reply, err := fx.service.Research(ctx, req)
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	wantStart := enginehost.StepStart{
		Agent: "scout-agent", Prompt: scoutContract + question, Schema: scoutSchema, Workspace: "/repo",
		Revision: "abc1234", ReadOnly: true, DoneValue: "done", Caller: store.EpicCaller{Session: "session-one"},
	}
	if len(fx.starter.starts) != 1 || !reflect.DeepEqual(fx.starter.starts[0], wantStart) {
		t.Fatalf("starts = %+v, want one start %+v", fx.starter.starts, wantStart)
	}

	id := reply.Msg.GetStep().GetStep()
	rows, err := fx.store.DesignSteps(ctx, stageDesign)
	if err != nil {
		t.Fatalf("DesignSteps: %v", err)
	}
	wantRow := store.DesignStep{
		Step: id, Design: stageDesign, Kind: store.StepResearch, Subject: question, Repository: "/repo",
		Revision: "abc1234", Created: stageResearchNow(),
	}
	if !reflect.DeepEqual(rows, []store.DesignStep{wantRow}) {
		t.Fatalf("design steps = %+v, want %+v", rows, wantRow)
	}

	running := stageSteps(t, fx.service)
	if len(running) != 1 || running[0].GetStep() != id || running[0].GetStatus() != "running" ||
		running[0].GetReport() != "" {
		t.Fatalf("stage steps before the return = %v, want step %s running with no report", running, id)
	}

	applyStageSteps(t, fx.store, store.StepReturn{ID: id, Result: stageResearchReturn})
	returned := stageSteps(t, fx.service)
	if len(returned) != 1 || returned[0].GetReport() != stageResearchReturn {
		t.Fatalf("stage steps after the return = %v, want the scout's return as the report", returned)
	}

	applyStageSteps(t, fx.store, store.StepEnd{ID: id, End: "done", Status: "done", Reason: "answered"})
	ended := stageSteps(t, fx.service)
	if len(ended) != 1 || ended[0].GetStatus() != "done" {
		t.Fatalf("stage steps after the end = %v, want the step with status done", ended)
	}
}

func TestStageResearchReproduceStartsTestRunnerAtRevision(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	ctx := t.Context()
	const command = "go test ./internal/store/"
	req := connect.NewRequest(&v1.ReproduceRequest{
		Design: stageDesign, Repository: "/repo", Revision: "abc1234", Command: command,
	})
	req.Header().Set(protocol.HeaderSession, "session-one")

	reply, err := fx.service.Reproduce(ctx, req)
	if err != nil {
		t.Fatalf("Reproduce: %v", err)
	}
	wantStart, err := testrunner.Start(
		fx.getenv, "/repo", "", testrunner.Request{Command: command, Revision: "abc1234"}, "", "",
	)
	if err != nil {
		t.Fatalf("testrunner.Start: %v", err)
	}
	wantStart.Caller = store.EpicCaller{Session: "session-one"}
	if len(fx.starter.starts) != 1 || !reflect.DeepEqual(fx.starter.starts[0], wantStart) {
		t.Fatalf("starts = %+v, want one start %+v", fx.starter.starts, wantStart)
	}
	if got := fx.starter.starts[0]; got.Agent != "runner-agent" || !got.ReadOnly || !got.Scratch {
		t.Fatalf("start = %+v, want the test runner's read-only scratch step under runner-agent", got)
	}

	id := reply.Msg.GetStep().GetStep()
	applyStageSteps(t, fx.store, store.StepReturn{ID: id, Result: stageResearchRunnerReturn})
	steps := stageSteps(t, fx.service)
	if len(steps) != 1 || steps[0].GetKind() != store.StepReproduce || steps[0].GetSubject() != command ||
		steps[0].GetRevision() != "abc1234" {
		t.Fatalf("stage steps = %v, want one reproduce step of %q at abc1234", steps, command)
	}
	if want := testrunner.Output(stageResearchRunnerReturn); steps[0].GetReport() != want {
		t.Fatalf("report = %q, want testrunner.Output of the return %q", steps[0].GetReport(), want)
	}
}

func TestStageResearchRefusesUnknownDesign(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	ctx := t.Context()
	const unknown = "no-such-design"

	_, err := fx.service.Research(ctx, connect.NewRequest(&v1.ResearchRequest{
		Design: unknown, Repository: "/repo", Question: "Does Foo call Bar?",
	}))
	if code := connect.CodeOf(err); code != connect.CodeNotFound {
		t.Fatalf("Research code = %v, want %v (err %v)", code, connect.CodeNotFound, err)
	}
	_, err = fx.service.Reproduce(ctx, connect.NewRequest(&v1.ReproduceRequest{
		Design: unknown, Repository: "/repo", Revision: "abc1234", Command: "go test ./...",
	}))
	if code := connect.CodeOf(err); code != connect.CodeNotFound {
		t.Fatalf("Reproduce code = %v, want %v (err %v)", code, connect.CodeNotFound, err)
	}
	if len(fx.starter.starts) != 0 {
		t.Fatalf("starts = %d, want none for an unknown design", len(fx.starter.starts))
	}
}

func TestStageResearchAcceptsContractOfOneAsDesign(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	ctx := t.Context()
	const contract = "contract-one"
	if err := fx.store.ProposeContract(ctx, contract, store.ContractVersion{
		Version: 1, Repository: "/repo", Title: "Contract one", Care: "serious",
		Body: `{"id":"contract-one"}`, Words: "words", Writer: "conversation-one", At: stageResearchNow().Unix(),
	}); err != nil {
		t.Fatalf("ProposeContract: %v", err)
	}

	if _, err := fx.service.Research(ctx, connect.NewRequest(&v1.ResearchRequest{
		Design: contract, Repository: "/repo", Question: "Does Foo call Bar?",
	})); err != nil {
		t.Fatalf("Research on a contract of one: %v", err)
	}
	rows, err := fx.store.DesignSteps(ctx, contract)
	if err != nil {
		t.Fatalf("DesignSteps: %v", err)
	}
	if len(rows) != 1 || rows[0].Design != contract || rows[0].Kind != store.StepResearch {
		t.Fatalf("design steps of the contract = %+v, want one research step of it", rows)
	}
}

func TestStageResearchStartFailureStoresNoRow(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	ctx := t.Context()
	fx.starter.err = errors.New("step caller resolves to no orrery session")

	_, err := fx.service.Research(ctx, connect.NewRequest(&v1.ResearchRequest{
		Design: stageDesign, Repository: "/repo", Question: "Does Foo call Bar?",
	}))
	if code := connect.CodeOf(err); code != connect.CodeFailedPrecondition {
		t.Fatalf("Research code = %v, want %v (err %v)", code, connect.CodeFailedPrecondition, err)
	}
	_, err = fx.service.Reproduce(ctx, connect.NewRequest(&v1.ReproduceRequest{
		Design: stageDesign, Repository: "/repo", Revision: "abc1234", Command: "go test ./...",
	}))
	if code := connect.CodeOf(err); code != connect.CodeFailedPrecondition {
		t.Fatalf("Reproduce code = %v, want %v (err %v)", code, connect.CodeFailedPrecondition, err)
	}
	rows, err := fx.store.DesignSteps(ctx, stageDesign)
	if err != nil {
		t.Fatalf("DesignSteps: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("design steps = %+v, want none after refused starts", rows)
	}
}

func TestStageResearchRequiresItsFields(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	ctx := t.Context()
	cases := map[string]func() error{
		"research without a repository": func() error {
			_, err := fx.service.Research(ctx, connect.NewRequest(&v1.ResearchRequest{
				Design: stageDesign, Question: "Does Foo call Bar?",
			}))
			return err
		},
		"research with a blank question": func() error {
			_, err := fx.service.Research(ctx, connect.NewRequest(&v1.ResearchRequest{
				Design: stageDesign, Repository: "/repo", Question: "  ",
			}))
			return err
		},
		"reproduce without a revision": func() error {
			_, err := fx.service.Reproduce(ctx, connect.NewRequest(&v1.ReproduceRequest{
				Design: stageDesign, Repository: "/repo", Command: "go test ./...",
			}))
			return err
		},
		"reproduce without a command": func() error {
			_, err := fx.service.Reproduce(ctx, connect.NewRequest(&v1.ReproduceRequest{
				Design: stageDesign, Repository: "/repo", Revision: "abc1234",
			}))
			return err
		},
	}
	for name, call := range cases {
		if code := connect.CodeOf(call()); code != connect.CodeInvalidArgument {
			t.Errorf("%s: code = %v, want %v", name, code, connect.CodeInvalidArgument)
		}
	}
	if len(fx.starter.starts) != 0 {
		t.Fatalf("starts = %d, want none for refused requests", len(fx.starter.starts))
	}
}
