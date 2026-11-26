package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

// closeNext is the epic a superseded close names as the epic that replaces the closed one.
const closeNext = "orrery-1a2b3c4/epic-next"

// closeAt is the second the close tests close at.
func closeAt() time.Time {
	return time.Unix(1700001000, 0)
}

// closeRow returns the stored epic with id as the store reads it back.
func closeRow(t *testing.T, s *Store, id string) EpicRow {
	t.Helper()
	row, err := s.Epic(context.Background(), id)
	if err != nil {
		t.Fatalf("Epic %s: %v", id, err)
	}
	return row
}

// openTriggerCount counts the triggers of epic that are pending or claimed, which a close leaves none of.
func openTriggerCount(t *testing.T, s *Store, epic string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRowContext(
		context.Background(),
		`SELECT COUNT(*) FROM epic_triggers WHERE epic = ? AND state IN ('pending', 'claimed')`,
		epic,
	).Scan(&n); err != nil {
		t.Fatalf("count open triggers of %s: %v", epic, err)
	}
	return n
}

// createParkedEpic stores epic in the accepted phase and parks it, which opens its park question.
func createParkedEpic(t *testing.T, s *Store, epic string) {
	t.Helper()
	createEpic(t, s, fullEpic(epic))
	if err := s.ParkEpic(context.Background(), epic, ParkClassBranch, "credit ran out", completedAt()); err != nil {
		t.Fatalf("ParkEpic of %s: %v", epic, err)
	}
}

// TestCloseEpicEachKindEndsAParkedEpicAndOvertakesItsQuestion closes a parked epic as each kind, and checks that the
// phase reads the closed phase of the kind, that the closer and the epic that replaces it are recorded, that the park
// ends, and that the open park question is overtaken, with orreryd's decision and a tell that name the closed phase.
func TestCloseEpicEachKindEndsAParkedEpicAndOvertakesItsQuestion(t *testing.T) {
	t.Parallel()
	cases := []struct{ kind, by, phase string }{
		{kind: "superseded", by: closeNext, phase: PhaseClosedSuperseded},
		{kind: "shipped", phase: PhaseClosedShipped},
		{kind: "abandoned", phase: PhaseClosedAbandoned},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			s := openEpicStore(t)
			createParkedEpic(t, s, claimEpic)
			if _, err := s.CloseEpic(
				ctx,
				claimEpic,
				tc.kind,
				tc.by,
				CloserOwner,
				Maker{By: MakerOwner},
				closeAt(),
			); err != nil {
				t.Fatalf("CloseEpic as %s: %v", tc.kind, err)
			}

			row := closeRow(t, s, claimEpic)
			if row.Phase != tc.phase || row.ClosedBy != CloserOwner || row.SupersededBy != tc.by || row.Parked != "" {
				t.Errorf(
					"closed as %s: phase %q closed by %q superseded by %q parked %q, want %q closed by %q superseded by %q not parked",
					tc.kind,
					row.Phase,
					row.ClosedBy,
					row.SupersededBy,
					row.Parked,
					tc.phase,
					CloserOwner,
					tc.by,
				)
			}
			if len(row.Questions) != 1 || row.Questions[0].State != QuestionOvertaken || row.Questions[0].Choice != "" {
				t.Errorf("questions after the close = %+v, want the park question overtaken", row.Questions)
			}
			why := "the epic is " + tc.phase + ", and it ended without its answer"
			wantDecision := EpicDecision{
				Seq: 3, ID: "overtaken:Q1", Text: "Q1 park closed as overtaken: " + why + ".", Time: closeAt(),
				Maker: Maker{By: MakerOrreryd},
			}
			if got := row.Decisions[len(row.Decisions)-1]; !reflect.DeepEqual(got, wantDecision) {
				t.Errorf("last decision after the close = %+v, want %+v", got, wantDecision)
			}
			wantTells := []string{"credit ran out", "Q1 park is overtaken and takes no answer: " + why + "."}
			if got := claimTells(t, s); !reflect.DeepEqual(got, wantTells) {
				t.Errorf("tells after the close = %q, want %q", got, wantTells)
			}
			open, err := s.OpenQuestions(ctx, claimEpic)
			if err != nil {
				t.Fatalf("OpenQuestions: %v", err)
			}
			if len(open) != 0 {
				t.Errorf("open questions after the close = %+v, want none", open)
			}
		})
	}
}

// TestCloseEpicEndsTheRunningTurnNodeRunsAndTriggers closes an epic whose turn runs, with a running node run and a pending
// trigger. The close returns the turn's step, ends the turn free, ends the node run with no outcome, and leaves no trigger
// pending or claimed.
func TestCloseEpicEndsTheRunningTurnNodeRunsAndTriggers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	turn, err := s.ClaimTurn(ctx, claimEpic, claimedAt())
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err = s.SetTurnStep(ctx, claimEpic, turn.Seq, "step-1"); err != nil {
		t.Fatalf("SetTurnStep: %v", err)
	}
	runs := []NodeRun{{Path: "plan", ID: "id-plan", Node: "plan", Kind: "plan", Attempt: 1}}
	if err = s.StartNodes(ctx, claimEpic, runs, nil, claimedAt()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}
	addPendingTrigger(t, s)

	step, err := s.CloseEpic(ctx, claimEpic, "abandoned", "", CloserOwner, Maker{By: MakerOwner}, closeAt())
	if err != nil {
		t.Fatalf("CloseEpic: %v", err)
	}
	if step != "step-1" {
		t.Errorf("CloseEpic returned step %q, want the turn's step step-1", step)
	}
	row := closeRow(t, s, claimEpic)
	if len(row.Turns) != 1 || row.Turns[0].State != "free" || row.Turns[0].Reason != "epic closed as abandoned" {
		t.Errorf("turns after the close = %+v, want the one turn free with the close as its reason", row.Turns)
	}
	nodeRuns, err := s.NodeRuns(ctx, claimEpic)
	if err != nil {
		t.Fatalf("NodeRuns: %v", err)
	}
	if len(nodeRuns) != 1 || nodeRuns[0].State != NodeEnded || nodeRuns[0].Outcome != "" {
		t.Errorf("node runs after the close = %+v, want the plan run ended with no outcome", nodeRuns)
	}
	if n := openTriggerCount(t, s, claimEpic); n != 0 {
		t.Errorf("open triggers after the close = %d, want none", n)
	}
}

// TestCloseEpicOfAReleasingEpicLeavesItsRepositoryFree closes an epic that is releasing, and checks that it is closed
// and that a clean epic of its repository starts its release at once afterwards.
func TestCloseEpicOfAReleasingEpicLeavesItsRepositoryFree(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const sibling = "orrery-1a2b3c4/epic-sibling"
	s := openEpicStore(t)
	row := fullEpic(claimEpic)
	row.Phase = PhaseClean
	createEpic(t, s, row)
	other := fullEpic(sibling)
	other.Phase = PhaseClean
	createEpic(t, s, other)
	if err := s.StartRelease(ctx, claimEpic, claimedAt()); err != nil {
		t.Fatalf("StartRelease: %v", err)
	}

	if _, err := s.CloseEpic(
		ctx,
		claimEpic,
		"abandoned",
		"",
		CloserOwner,
		Maker{By: MakerOwner},
		closeAt(),
	); err != nil {
		t.Fatalf("CloseEpic: %v", err)
	}
	if got := closeRow(t, s, claimEpic).Phase; got != PhaseClosedAbandoned {
		t.Errorf("phase of the closed epic = %q, want %q", got, PhaseClosedAbandoned)
	}
	if err := s.StartRelease(ctx, sibling, closeAt()); err != nil {
		t.Fatalf("StartRelease of %s: %v", sibling, err)
	}
	if got := closeRow(t, s, sibling).Phase; got != PhaseReleasing {
		t.Errorf("phase of %s = %q, want %q", sibling, got, PhaseReleasing)
	}
}

// TestCloseEpicStopsItsBuildsAndItsNextTurn closes an epic that holds an open task, and checks that the builder no longer
// lists it and the planner can claim no turn for it, even with a new pending trigger.
func TestCloseEpicStopsItsBuildsAndItsNextTurn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	row := fullEpic(claimEpic)
	row.Base = "main"
	createEpic(t, s, row)
	turn, err := s.ClaimTurn(ctx, claimEpic, claimedAt())
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err = s.CompleteTurn(ctx, claimEpic, turn.Seq, plannedTurn(), completedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	if got, err := s.BuildEpics(ctx); err != nil || len(got) != 1 || got[0] != claimEpic {
		t.Fatalf("BuildEpics before the close = %v, %v; want %s", got, err, claimEpic)
	}

	if _, err = s.CloseEpic(ctx, claimEpic, "shipped", "", CloserOwner, Maker{By: MakerOwner}, closeAt()); err != nil {
		t.Fatalf("CloseEpic: %v", err)
	}
	if got, err := s.BuildEpics(ctx); err != nil || len(got) != 0 {
		t.Errorf("BuildEpics after the close = %v, %v; want none", got, err)
	}
	addPendingTrigger(t, s)
	if _, err = s.ClaimTurn(ctx, claimEpic, closeAt()); !errors.Is(err, ErrNotClaimable) {
		t.Errorf("ClaimTurn after the close: err %v, want ErrNotClaimable", err)
	}
}

// TestCloseEpicPlannerCloseRoutesTheReportAndOwnerCloseRoutesNone closes one parked epic as the planner and another as the
// owner. Only the planner's epic needs a report, and only the planner's close adds the tell that ends it. Each close
// overtakes its park question with a tell the steerer is owed; the owner's is stored told, so the owner gets no notice
// of their own close, and the planner's is not.
func TestCloseEpicPlannerCloseRoutesTheReportAndOwnerCloseRoutesNone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const owned = "orrery-1a2b3c4/epic-owned"
	createParkedEpic(t, s, claimEpic)
	createParkedEpic(t, s, owned)

	if _, err := s.CloseEpic(
		ctx,
		claimEpic,
		"superseded",
		closeNext,
		CloserPlanner,
		Maker{By: MakerOwner},
		closeAt(),
	); err != nil {
		t.Fatalf("planner CloseEpic: %v", err)
	}
	if _, err := s.CloseEpic(ctx, owned, "shipped", "", CloserOwner, Maker{By: MakerOwner}, closeAt()); err != nil {
		t.Fatalf("owner CloseEpic: %v", err)
	}

	unreported, err := s.UnreportedEpics(ctx)
	if err != nil {
		t.Fatalf("UnreportedEpics: %v", err)
	}
	if len(unreported) != 1 || unreported[0].ID != claimEpic {
		t.Errorf("unreported epics = %v, want only %s", epicIDsOf(unreported), claimEpic)
	}
	if got := lastTellText(t, s, claimEpic); got != "Closed as superseded by "+closeNext+"." {
		t.Errorf("last tell of the planner's close = %q, want the closed-as tell naming %s", got, closeNext)
	}
	overtakenTell := func(phase string) string {
		return "Q1 park is overtaken and takes no answer: the epic is " + phase + ", and it ended without its answer."
	}
	wantPlanner := [][]any{
		{"credit ran out", int64(0), int64(0)},
		{overtakenTell(PhaseClosedSuperseded), int64(0), int64(0)},
		{"Closed as superseded by " + closeNext + ".", int64(0), int64(0)},
	}
	if got := tellMarksOf(t, s, claimEpic); !reflect.DeepEqual(got, wantPlanner) {
		t.Errorf("tells of the planner's close (text, told, delivered) = %v, want %v", got, wantPlanner)
	}
	wantOwner := [][]any{
		{"credit ran out", int64(0), int64(0)},
		{overtakenTell(PhaseClosedShipped), int64(1), int64(0)},
	}
	if got := tellMarksOf(t, s, owned); !reflect.DeepEqual(got, wantOwner) {
		t.Errorf("tells of the owner's close (text, told, delivered) = %v, want %v", got, wantOwner)
	}
}

// tellMarksOf returns each tell of epic in seq order as its text, its told mark and its steerer's delivered mark.
func tellMarksOf(t *testing.T, s *Store, epic string) [][]any {
	t.Helper()
	tells, err := s.fixtureQueries().ListEpicTells(context.Background(), epic)
	if err != nil {
		t.Fatalf("tells of %s: %v", epic, err)
	}
	marks := make([][]any, 0, len(tells))
	for _, tell := range tells {
		marks = append(marks, []any{tell.Text, tell.Told, tell.SteererDelivered})
	}
	return marks
}

// epicIDsOf returns the ids of rows, in order.
func epicIDsOf(rows []EpicRow) []string {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids
}

// lastTellText returns the text of the newest tell of epic.
func lastTellText(t *testing.T, s *Store, epic string) string {
	t.Helper()
	tells, err := s.fixtureQueries().ListEpicTells(context.Background(), epic)
	if err != nil || len(tells) == 0 {
		t.Fatalf("tells of %s = %v, %v; want at least one", epic, tells, err)
	}
	return tells[len(tells)-1].Text
}

// TestCloseEpicRefusesSupersededWithoutByAndWritesNothing attempts a superseded close that names no epic, and an unknown
// kind, and checks that each is a RefusalError and that the parked epic is left as it was.
func TestCloseEpicRefusesSupersededWithoutByAndWritesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createParkedEpic(t, s, claimEpic)

	cases := []struct {
		name, kind string
	}{
		{name: "superseded without by", kind: "superseded"},
		{name: "unknown kind", kind: "dropped"},
	}
	for _, tc := range cases {
		_, err := s.CloseEpic(ctx, claimEpic, tc.kind, "", CloserOwner, Maker{By: MakerOwner}, closeAt())
		if _, ok := errors.AsType[RefusalError](err); !ok {
			t.Errorf("%s: CloseEpic(%s, %q) = %v, want a RefusalError", tc.name, claimEpic, tc.kind, err)
		}
	}
	if _, err := s.CloseEpic(
		ctx,
		"orrery-1a2b3c4/no-such-epic",
		"shipped",
		"",
		CloserOwner,
		Maker{By: MakerOwner},
		closeAt(),
	); !errors.Is(
		err,
		ErrNoEpic,
	) {
		t.Errorf("CloseEpic of an unknown epic: err %v, want ErrNoEpic", err)
	}

	row := closeRow(t, s, claimEpic)
	if row.Phase != "accepted" || row.Parked != "credit ran out" || len(row.Questions) != 1 ||
		row.Questions[0].State != "open" {
		t.Errorf("parked epic after the refused closes: phase %q parked %q questions %+v; want it untouched",
			row.Phase, row.Parked, row.Questions)
	}
}
