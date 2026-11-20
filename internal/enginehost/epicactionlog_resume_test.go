package enginehost

import (
	"slices"
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// TestEpicActionLogResumeOfLimitedStepsRecordsOneAction checks that an accepted ResumeEpic of an unpaused epic that holds
// limited task steps appends one resume action made by the owner, and that the action is owed once to the epic's steerer.
func TestEpicActionLogResumeOfLimitedStepsRecordsOneAction(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st := epicTestStore(t)
	steering := store.EngineAdd{ID: fixedSteerID, Dir: "/work/steering"}
	if err := st.ApplyEngine(ctx, []store.EngineOp{steering}, epicActionLogSeedAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
	steps := &resumeSteps{rows: []store.StepRow{limitedStep("step-a", "task-a")}}
	client := newEpicsClient(t, startControlServer(t, st, steps))
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))

	resumed, err := client.ResumeEpic(ctx, connect.NewRequest(&v1.ResumeEpicRequest{Id: epicServiceID}))
	if err != nil {
		t.Fatalf("ResumeEpic: %v", err)
	}
	if got := resumed.Msg.GetResumed(); !slices.Equal(got, []string{"step-a"}) {
		t.Fatalf("resumed = %v, want [step-a]", got)
	}

	history, err := st.EpicHistory(ctx, epicServiceID)
	if err != nil {
		t.Fatalf("EpicHistory: %v", err)
	}
	owner := store.Maker{By: store.MakerOwner}
	if len(history.Actions) != 1 || history.Actions[0].Kind != store.ActionResume || history.Actions[0].Maker != owner {
		t.Fatalf("actions = %+v, want one resume made by %+v", history.Actions, owner)
	}

	events, err := st.ActionEvents(ctx)
	if err != nil {
		t.Fatalf("ActionEvents: %v", err)
	}
	if len(events) != 1 || events[0].Action != store.ActionResume || events[0].Recipient.ID != fixedSteerID {
		t.Fatalf("ActionEvents = %+v, want one resume owed to %s", events, fixedSteerID)
	}
}
