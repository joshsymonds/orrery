package interpret

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/planner"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// parkSteerer is the headless session that steers the park tests' epic, so a park question reaches it.
const parkSteerer = "session-steerer"

// parkReviewFlow is the workflow the no-plan park test pins for a review: one review node at the top level.
const parkReviewFlow = "name: review\nnodes:\n  - id: review\n    kind: review\n"

// parkStallReason is the reason the stall the park tests write has as its cause, before the reason a park adds.
const parkStallReason = stallWakeCause

// parkDeliver is the Deliver of the park tests. It accepts each request and records it in delivered.
type parkDeliver struct {
	delivered []enginehost.DeliverRequest
}

func (d *parkDeliver) deliver(_ context.Context, req enginehost.DeliverRequest) error {
	d.delivered = append(d.delivered, req)
	return nil
}

// flowPin returns the pin of the workflow text flow, as the store keeps it for an epic.
func flowPin(t *testing.T, flow string) store.EpicWorkflow {
	t.Helper()
	name := strings.TrimPrefix(strings.SplitN(flow, "\n", 2)[0], "name: ")
	pinned, err := workflow.Load(name, func(string) ([]byte, bool, error) { return []byte(flow), true, nil })
	if err != nil {
		t.Fatalf("Load %s: %v", name, err)
	}
	return store.EpicWorkflow{
		Name: pinned.Name, Text: pinned.Text, SHA256: pinned.SHA256, Nodes: pinnedNodes(pinned.Workflow.Nodes, ""),
	}
}

// createParkEpic stores the test epic pinned to pin, in phase, steered by steerer, with the base and times the run
// tests give it.
func createParkEpic(t *testing.T, s *store.Store, pin store.EpicWorkflow, phase, steerer string) {
	t.Helper()
	if err := s.CreateEpic(context.Background(), store.EpicRow{
		ID: runEpic, Repository: runRepo, Base: runBase, Title: "Run the workflow", Phase: phase,
		Created: runClock(), Changed: runClock(), Steerer: steerer, Workflow: pin,
	}); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
}

// newParkRig returns a stall rig whose epic is steered by parkSteerer. Its plan run is running, and its planner has had
// its first turn, which ended as a wait, as newStallRig leaves it.
func newParkRig(t *testing.T) stallRig {
	t.Helper()
	ctx := context.Background()
	s := openRunStore(t)
	steerer := store.EngineAdd{ID: parkSteerer, Dir: t.TempDir(), State: "done", Kind: store.KindEngine}
	if err := s.ApplyEngine(ctx, []store.EngineOp{steerer}, runClock()); err != nil {
		t.Fatalf("ApplyEngine add steerer: %v", err)
	}
	createParkEpic(t, s, planBuildPin(), runPhase, parkSteerer)
	wake := &stallWakeStore{Store: s}
	planner := &claimingPlanner{fakePlanner: &fakePlanner{known: []string{runEpic}}, t: t, store: s, claims: true}
	runner := New(Config{Store: wake, Planner: planner, Builder: &fakeBuilder{}, Now: runClock})
	runner.Pass(ctx)
	if !hasRunningPlan(runsOf(t, s)) {
		t.Fatal("the pass that starts the plan run left no plan run running")
	}
	endRunningTurn(t, s)
	planner.claims = false
	return stallRig{store: s, wake: wake, planner: planner, runner: runner}
}

// endRunningTurn ends the one running planner turn of the test epic as a wait, which adds no task, node run or action.
func endRunningTurn(t *testing.T, s *store.Store) {
	t.Helper()
	turns, err := s.RunningTurns(context.Background())
	if err != nil || len(turns) != 1 {
		t.Fatalf("running turns = %v, %v; want one turn to end", turns, err)
	}
	wait := store.EpicPlan{Outcome: "wait", Outline: "wait"}
	if err = s.CompleteTurn(context.Background(), runEpic, turns[0].Seq, wait, runClock()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
}

// parkTurnWaits gives the planner of rig one turn, which claims the pending trigger of the epic, and ends the turn as a
// wait. Afterwards the epic has nothing pending and no turn running.
func parkTurnWaits(t *testing.T, rig stallRig) {
	t.Helper()
	rig.planner.claims = true
	rig.runner.Pass(context.Background())
	rig.planner.claims = false
	endRunningTurn(t, rig.store)
}

// parkStallOf returns the stall the test epic has at seq, and fails the test when it has none.
func parkStallOf(t *testing.T, s *store.Store, seq int64) store.EpicStall {
	t.Helper()
	latest, ok, err := s.LatestStall(context.Background(), runEpic)
	if err != nil || !ok || latest.Seq != seq {
		t.Fatalf("LatestStall = %+v, %v, %v; want stall %d", latest, ok, err, seq)
	}
	return latest
}

// TestEpicStallParksAfterAnUnchangedStall checks that an idle pass after a stall, with no task, node run or action
// added since it, parks the epic with a stall park. The park reason says the last stall changed nothing, and no second
// stall trigger is written.
func TestEpicStallParksAfterAnUnchangedStall(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rig := newParkRig(t)
	rig.wake.edit = stallOpenTask
	rig.runner.Pass(ctx)
	parkTurnWaits(t, rig)
	rig.runner.Pass(ctx)

	want := store.EpicStall{Seq: 2, At: runClock(), Mark: store.StallMark{Runs: 1}, Parked: true}
	if latest := parkStallOf(t, rig.store, 2); !reflect.DeepEqual(latest, want) {
		t.Errorf("LatestStall = %+v, want %+v", latest, want)
	}
	row := epicOf(t, rig.store)
	if reason := parkStallReason + "; the last stall changed nothing"; row.Parked != reason {
		t.Errorf("park reason = %q, want %q", row.Parked, reason)
	}
	if row.ParkClass != store.ParkClassStall {
		t.Errorf("park class = %q, want %q", row.ParkClass, store.ParkClassStall)
	}
	if got := stallCausesOf(t, rig.store); len(got) != 1 {
		t.Errorf("stall causes = %q, want the one stall the first pass wrote", got)
	}
}

// TestEpicStallParksQuestionReachesTheSteerer checks that the park an unchanged stall makes reaches the steering
// conversation as a park question event, sent to the session that steers the epic.
func TestEpicStallParksQuestionReachesTheSteerer(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rig := newParkRig(t)
	rig.wake.edit = stallOpenTask
	rig.runner.Pass(ctx)
	parkTurnWaits(t, rig)
	rig.runner.Pass(ctx)

	deliver := &parkDeliver{}
	sender := enginehost.NewCallerSender(enginehost.CallerSenderConfig{
		Deliver: deliver.deliver, Store: rig.store, Now: runClock,
	})
	pass := planner.NewEpicCallerEvents(planner.EpicCallerEventsConfig{Store: rig.store, Sender: sender})
	if err := pass.Pass(ctx); err != nil {
		t.Fatalf("epic caller-event pass: %v", err)
	}

	event := store.EpicQuestionEvent(runEpic, 1)
	var question []enginehost.DeliverRequest
	for _, req := range deliver.delivered {
		if req.PromptID == event {
			question = append(question, req)
		}
	}
	if len(question) != 1 {
		t.Fatalf("delivered %d park question events %s, want one: delivered %+v", len(question), event,
			deliver.delivered)
	}
	if question[0].ID != parkSteerer {
		t.Errorf("park question sent to %q, want the steerer %q", question[0].ID, parkSteerer)
	}
	head := `<orrery-event kind="park" id="` + event + `">`
	text := question[0].Text
	if !strings.HasPrefix(text, head) || !strings.Contains(text, "no progress since the last pass") {
		t.Errorf("park question text = %q, want a park event %q that names the stall", text, head)
	}
}

// TestEpicStallParksRetryEndsParkAndPlannerClaimsStallRetry checks that retrying a stall park ends the park and leaves
// the pending stall-retry trigger, which the planner claims on its next turn.
func TestEpicStallParksRetryEndsParkAndPlannerClaimsStallRetry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rig := newParkRig(t)
	rig.wake.edit = stallOpenTask
	rig.runner.Pass(ctx)
	parkTurnWaits(t, rig)
	rig.runner.Pass(ctx)
	parkStallOf(t, rig.store, 2)

	if err := rig.store.RetryEpic(ctx, runEpic, store.Maker{By: store.MakerOwner}, runClock()); err != nil {
		t.Fatalf("RetryEpic: %v", err)
	}
	if parked := epicOf(t, rig.store).Parked; parked != "" {
		t.Fatalf("park after RetryEpic = %q, want none", parked)
	}
	history, err := rig.store.EpicHistory(ctx, runEpic)
	if err != nil {
		t.Fatalf("EpicHistory: %v", err)
	}
	const retry = "stall-retry:2"
	idx := slices.IndexFunc(history.Triggers, func(trigger store.EpicTrigger) bool { return trigger.ID == retry })
	if idx < 0 || history.Triggers[idx].State != "pending" {
		t.Fatalf("triggers after RetryEpic = %+v, want %s pending", history.Triggers, retry)
	}

	rig.planner.claims = true
	rig.runner.Pass(ctx)
	turns, err := rig.store.RunningTurns(ctx)
	if err != nil || len(turns) != 1 {
		t.Fatalf("running turns after retry = %v, %v; want the planner's turn", turns, err)
	}
	triggers, err := rig.store.TurnTriggers(ctx, runEpic, turns[0].Seq)
	if err != nil {
		t.Fatalf("TurnTriggers: %v", err)
	}
	if !slices.ContainsFunc(triggers, func(trigger store.EpicTrigger) bool { return trigger.ID == retry }) {
		t.Errorf(
			"triggers claimed by turn %d = %+v, want %s: the planner claims the retry",
			turns[0].Seq,
			triggers,
			retry,
		)
	}
}

// TestEpicStallParksChangeWakesAgain checks that a stall after an action on the epic wakes the planner again with a new
// stall trigger, rather than parking: the mark has moved since the last stall.
func TestEpicStallParksChangeWakesAgain(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rig := newParkRig(t)
	rig.wake.edit = stallOpenTask
	rig.runner.Pass(ctx)
	parkTurnWaits(t, rig)

	owner := store.Maker{By: store.MakerOwner}
	if err := rig.store.PauseEpic(ctx, runEpic, owner, runClock()); err != nil {
		t.Fatalf("PauseEpic: %v", err)
	}
	if err := rig.store.ResumeEpic(ctx, runEpic, owner, runClock()); err != nil {
		t.Fatalf("ResumeEpic: %v", err)
	}
	rig.runner.Pass(ctx)

	latest := parkStallOf(t, rig.store, 2)
	want := store.EpicStall{Seq: 2, At: runClock(), Mark: store.StallMark{Runs: 1, Actions: 2}}
	if !reflect.DeepEqual(latest, want) {
		t.Errorf("LatestStall = %+v, want the wake %+v", latest, want)
	}
	if got := stallCausesOf(t, rig.store); len(got) != 2 {
		t.Errorf("stall causes = %q, want the first stall and a second one", got)
	}
	if epicOf(t, rig.store).Parked != "" {
		t.Errorf("epic parked after a change, want it woken")
	}
}

// TestEpicStallParksIdleWithNoPlanRunParksAtOnce checks that an idle epic with no running plan run is parked by the
// first pass that finds it idle, with a stall park whose reason says no plan run was running. Each case is an epic at a
// state the pass leaves alone. A review node is idle when its kind is not registered, which is so when the runner has
// no reviewers and no ending, as these runners have.
func TestEpicStallParksIdleWithNoPlanRunParksAtOnce(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		flow    string
		phase   string
		node    string
		kind    string
		failed  bool
		running string
	}{
		{name: "review", flow: parkReviewFlow, phase: runPhase, node: "review", kind: reviewNodeKind,
			running: "review"},
		{name: "failed top-level node", flow: releaseFlow, phase: runPhase, node: releaseNodePath, kind: kindRelease,
			failed: true},
		{name: "idle release", flow: releaseFlow, phase: runPhase, node: releaseNodePath, kind: kindRelease,
			running: releaseNodePath},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			s := openRunStore(t)
			createParkEpic(t, s, flowPin(t, tc.flow), tc.phase, "")
			run := store.NodeRun{
				Path: tc.node, ID: store.NodeRunID(runEpic, tc.node, 0, "start"), Node: tc.node, Kind: tc.kind,
			}
			if err := s.StartNodes(ctx, runEpic, []store.NodeRun{run}, nil, runClock()); err != nil {
				t.Fatalf("StartNodes: %v", err)
			}
			if tc.failed {
				ends := []store.NodeEnd{{Path: tc.node, Outcome: outcomeFailed}}
				if err := s.EndNodes(ctx, runEpic, ends, runClock()); err != nil {
					t.Fatalf("EndNodes: %v", err)
				}
			}
			runner := New(Config{Store: s, Planner: &fakePlanner{}, Builder: &fakeBuilder{}, Now: runClock})
			runner.Pass(ctx)

			running := "none"
			if tc.running != "" {
				running = tc.running
			}
			want := "no progress since the last pass; phase " + tc.phase + "; open tasks: none; running node runs: " +
				running + "; no plan run was running"
			row := epicOf(t, s)
			if row.Parked != want {
				t.Errorf("park reason = %q, want %q", row.Parked, want)
			}
			if row.ParkClass != store.ParkClassStall {
				t.Errorf("park class = %q, want %q", row.ParkClass, store.ParkClassStall)
			}
			parkStallOf(t, s, 1)
		})
	}
}
