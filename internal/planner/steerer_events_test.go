package planner

import (
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/store"
)

// TestCallerEventSteererEventsReachTheSteererOnly: the runner epic is steered by steerer and created by creator, and
// each of its four kinds of event is delivered to steerer and to no one else.
func TestCallerEventSteererEventsReachTheSteererOnly(t *testing.T) {
	t.Parallel()
	for _, c := range callerEpicEventCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			s := openRunnerStore(t)
			plannedCallerEpic(t, s, "steerer", c.plan)
			addCallerEngine(t, s, "steerer", store.KindEngine)
			addCallerEngine(t, s, "creator", store.KindEngine)
			recordRunnerCall(t, s, store.EpicCaller{Session: "creator"}, "epic_create")
			if err := c.reach(ctx, s); err != nil {
				t.Fatalf("reach %s: %v", c.name, err)
			}
			deliver := &fakeCallerDeliver{}
			passEpics(ctx, t, callerEpicEventsOver(s, callerSenderOver(s, deliver.deliver, noWake(t))))

			want := []enginehost.DeliverRequest{{ID: "steerer", Text: c.block, PromptID: c.event}}
			if !reflect.DeepEqual(deliver.delivered, want) {
				t.Fatalf("delivered = %+v, want %+v: the steerer only", deliver.delivered, want)
			}
		})
	}
}

// TestCallerEventSteererEventsAbsentRowSendsNothing: the runner epic's steerer has no sessions row, so its open
// question is not sent to the creator, and no caller_events mark is written for either session.
func TestCallerEventSteererEventsAbsentRowSendsNothing(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openRunnerStore(t)
	plannedCallerEpic(t, s, "steerer", callerEpicQuestionPlan())
	addCallerEngine(t, s, "creator", store.KindEngine)
	recordRunnerCall(t, s, store.EpicCaller{Session: "creator"}, "epic_create")

	passEpics(ctx, t, callerEpicEventsOver(s, callerSenderOver(s, noDeliver(t), noWake(t))))

	for _, session := range []string{"steerer", "creator"} {
		if marked, err := s.CallerEventMarked(ctx, callerEpicQuestionEvent, session); err != nil || marked {
			t.Fatalf("CallerEventMarked for %s = %v, %v, want not marked", session, marked, err)
		}
	}
}
