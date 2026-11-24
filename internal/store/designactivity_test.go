package store

import (
	"slices"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// activityStart is the second the activity tests start their steps at.
const activityStart int64 = 1700000200

// activityFixture returns a design step of design, kind and subject, started at the unix second at.
func activityFixture(step, design, kind, subject string, at int64) DesignStep {
	return DesignStep{
		Step: step, Design: design, Kind: kind, Subject: subject, Repository: "/repo", Revision: "abc1234",
		Created: time.Unix(at, 0),
	}
}

// addActivityStep stores step as a step of its design, started at the unix second its Created names: its step row is
// started by a StepAdd at that second, and its design step row is stored with the same Created.
func addActivityStep(t *testing.T, s *Store, step DesignStep) {
	t.Helper()
	add := StepAdd{ID: step.Step, Agent: "researcher", Prompt: "work it"}
	if err := s.ApplyEngine(t.Context(), []EngineOp{add}, step.Created); err != nil {
		t.Fatalf("ApplyEngine add step %s: %v", step.Step, err)
	}
	if err := s.AddDesignStep(t.Context(), step); err != nil {
		t.Fatalf("AddDesignStep %s: %v", step.Step, err)
	}
}

// checkRunningSteps fails the test unless the running steps of session, read through the Store and through the Reader
// of path, are want.
func checkRunningSteps(t *testing.T, s *Store, path, session string, want []DesignStep) {
	t.Helper()
	got, err := s.RunningDesignSteps(t.Context(), session)
	if err != nil {
		t.Fatalf("RunningDesignSteps of %s: %v", session, err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("RunningDesignSteps of %s = %+v, want %+v", session, got, want)
	}
	read, err := NewReader(path).RunningDesignSteps(t.Context(), session)
	if err != nil {
		t.Fatalf("Reader.RunningDesignSteps of %s: %v", session, err)
	}
	if !slices.Equal(read, want) {
		t.Fatalf("Reader.RunningDesignSteps of %s = %+v, want %+v", session, read, want)
	}
}

// TestRunningDesignStepsRecordsActivityOfWorkingConversation checks that RunningDesignSteps lists, for a session, the
// running steps of the designs the session works, oldest first. design-one is worked by session-one, the conversation
// it was made in, so its running research and discovery steps list for session-one. A step of another session's design
// does not list for it. design-two was made in session-old, but its newest conversation row names session-two, so
// session-two works it and session-old does not. contract-lone is a design of one, worked by its contract's
// conversation. Once a step ends it no longer lists, and the other running steps still do.
func TestRunningDesignStepsRecordsActivityOfWorkingConversation(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, path := openDesignLogStore(t)
	if err := s.CreateDesign(ctx, Design{
		ID: "design-one", Title: "Design one", Conversation: "session-one", Created: time.Unix(activityStart, 0),
	}); err != nil {
		t.Fatalf("CreateDesign design-one: %v", err)
	}
	if err := s.CreateDesign(ctx, Design{
		ID: "design-two", Title: "Design two", Conversation: "session-old", Created: time.Unix(activityStart, 0),
	}); err != nil {
		t.Fatalf("CreateDesign design-two: %v", err)
	}
	if err := s.fixtureQueries().InsertDesignConversation(ctx, sqlcgen.InsertDesignConversationParams{
		Design: "design-two", Seq: 1, Session: "session-two", Previous: "session-old", At: activityStart,
	}); err != nil {
		t.Fatalf("InsertDesignConversation design-two: %v", err)
	}
	if err := s.ProposeContract(ctx, "contract-lone", designLogProposal("session-lone")); err != nil {
		t.Fatalf("ProposeContract contract-lone: %v", err)
	}

	research := activityFixture("step-research", "design-one", StepResearch,
		"Does the scout keep state?\nRead the store.", activityStart)
	discovery := activityFixture("step-discovery", "design-one", StepDiscovery, "Find the gaps", activityStart+1)
	reproduce := activityFixture("step-reproduce", "design-one", StepReproduce, "go test ./internal/queue",
		activityStart+2)
	other := activityFixture("step-two", "design-two", StepResearch, "Who reads design two?", activityStart)
	lone := activityFixture("step-lone", "contract-lone", StepResearch, "Is the lone contract read?", activityStart)
	for _, step := range []DesignStep{research, discovery, reproduce, other, lone} {
		addActivityStep(t, s, step)
	}

	checkRunningSteps(t, s, path, "session-one", []DesignStep{research, discovery, reproduce})
	checkRunningSteps(t, s, path, "session-two", []DesignStep{other})
	checkRunningSteps(t, s, path, "session-lone", []DesignStep{lone})
	checkRunningSteps(t, s, path, "session-old", nil)
	checkRunningSteps(t, s, path, "session-none", nil)

	if err := s.ApplyEngine(ctx, []EngineOp{
		StepEnd{ID: "step-reproduce", End: "done", Status: "done", Reason: "returned"},
	}, time.Unix(activityStart+3, 0)); err != nil {
		t.Fatalf("ApplyEngine end step-reproduce: %v", err)
	}
	checkRunningSteps(t, s, path, "session-one", []DesignStep{research, discovery})
}
