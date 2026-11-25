package planner

import (
	"context"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// steererFirstClasses returns the park classes the owner answered directly before every park went to the epic's steerer
// first: each opens a park question.
func steererFirstClasses() []store.ParkClass {
	return []store.ParkClass{
		store.ParkClassReleaseUndoNone, store.ParkClassReleaseOwnerStop, store.ParkClassBranch, store.ParkClassRefMove,
		store.ParkClassMetAtBase, store.ParkClassOrchestrator, store.ParkClassRetryBound,
	}
}

// steererFirstReason is the reason a test parks the runner epic with as class.
func steererFirstReason(class store.ParkClass) string {
	return "a " + string(class) + " catastrophe"
}

// requireNoOwnerNotice fails unless one attention pass over s routes no notice to the owner.
func requireNoOwnerNotice(t *testing.T, s *store.Store, what string) {
	t.Helper()
	if router := attendRunner(s); len(router.notices) != 0 {
		t.Fatalf("owner notices %s = %d, want none", what, len(router.notices))
	}
}

// TestSteererFirstParkOfEachQuestionClass covers the rule that nothing goes over the steerer's head: a park of each
// class that opens a park question, on an epic a steering session hears, opens the question, tells the steerer of it as
// a park event, and routes no notice to the owner. Once the steerer escalates the park question, the next pass routes
// exactly one owner notice, which leads with the escalation line.
func TestSteererFirstParkOfEachQuestionClass(t *testing.T) {
	t.Parallel()
	for _, class := range steererFirstClasses() {
		t.Run(string(class), func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			s := openRunnerStore(t)
			plannedCallerEpic(t, s, "steerer", store.EpicPlan{Outcome: "wait"})
			addCallerEngine(t, s, "steerer", store.KindEngine)
			reason := steererFirstReason(class)
			if err := s.ParkEpic(ctx, runnerEpicID, class, reason, claimedAt()); err != nil {
				t.Fatalf("ParkEpic: %v", err)
			}

			deliver := &fakeCallerDeliver{}
			passEpics(ctx, t, callerEpicEventsOver(s, callerSenderOver(s, deliver.deliver, noWake(t))))
			if len(deliver.delivered) != 1 {
				t.Fatalf("events delivered = %+v, want the one park event", deliver.delivered)
			}
			event := deliver.delivered[0]
			if event.ID != "steerer" || event.PromptID != store.EpicQuestionEvent(runnerEpicID, 1) ||
				!strings.Contains(event.Text, `kind="park"`) || !strings.Contains(event.Text, "parked: "+reason) {
				t.Errorf("event delivered = %+v, want the park of Q1 with its reason, to the steerer", event)
			}
			requireNoOwnerNotice(t, s, "of a park its steerer holds")
			requireNoOwnerNotice(t, s, "on a second pass")

			const why = "this needs the owner"
			maker := store.Maker{By: store.MakerConversation, Session: "steerer"}
			if _, err := s.EscalateQuestion(ctx, runnerEpicID, 1, why, maker, claimedAt()); err != nil {
				t.Fatalf("EscalateQuestion: %v", err)
			}
			router := attendRunner(s)
			if len(router.notices) != 1 {
				t.Fatalf("owner notices after the escalation = %d, want exactly one", len(router.notices))
			}
			body := shownOf(t, router.notices[0]).desk.Body
			if want := "escalated by conversation steerer: " + why + "\n"; !strings.HasPrefix(body, want) {
				t.Errorf("escalation notice body = %q, want it to lead with %q", body, want)
			}
			requireNoOwnerNotice(t, s, "after the escalation was told")
		})
	}
}

// TestSteererFirstParkWithNoSteererGoesToTheOwner checks that a park of each question class on an epic with no steerer
// is the owner's directly: one pass routes one notice, which carries the reason and opens on the park question.
func TestSteererFirstParkWithNoSteererGoesToTheOwner(t *testing.T) {
	t.Parallel()
	for _, class := range steererFirstClasses() {
		t.Run(string(class), func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			s := openRunnerStore(t)
			createRunnerEpic(t, s)
			reason := steererFirstReason(class)
			if err := s.ParkEpic(ctx, runnerEpicID, class, reason, claimedAt()); err != nil {
				t.Fatalf("ParkEpic: %v", err)
			}
			requireOneNotice(ctx, t, s, reason)
		})
	}
}
