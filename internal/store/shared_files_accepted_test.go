package store

import (
	"context"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
)

// sharedHolderTask adds task seq of climbEpic in status, as the planner's turn plans a task that another task may share
// a file with: open, or done and not yet integrated.
func sharedHolderTask(t *testing.T, s *Store, seq int64, status string) {
	t.Helper()
	if _, err := s.fixtureExec(context.Background(), `INSERT INTO epic_tasks (epic, seq, turn, title,
		deliverable, status) VALUES (?, ?, 1, 'Other', 'the other change', ?)`, climbEpic, seq, status); err != nil {
		t.Fatalf("insert task %d: %v", seq, err)
	}
}

// TestSharedFilesAcceptedGrant: a grant of a file another open task owns adds it to the task's files, after its own,
// and leaves the other task's files as they were.
func TestSharedFilesAcceptedGrant(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	from := climbFrom()
	seedClimbTask(t, s, from)
	ladderApproachOwn(t, s, 1, "a.go")
	sharedHolderTask(t, s, 2, "open")
	ladderApproachOwn(t, s, 2, "shared.go")
	to := from
	to.Rerun = 1
	grant := TaskVersionInput{Source: SourceGrant, Files: []string{"shared.go"}}
	if err := s.GrantTaskFiles(ctx, climbEpic, 1, from, to, grant, climbedAt()); err != nil {
		t.Fatalf("GrantTaskFiles of shared.go, which open T2 owns: %v", err)
	}
	if want := []string{"a.go", "shared.go"}; !reflect.DeepEqual(climbTaskOf(ctx, t, s, 1).Files, want) {
		t.Errorf("Files of task 1 = %v, want %v", climbTaskOf(ctx, t, s, 1).Files, want)
	}
	if want := []string{"shared.go"}; !reflect.DeepEqual(climbTaskOf(ctx, t, s, 2).Files, want) {
		t.Errorf("Files of task 2 = %v, want %v: the holder keeps its file", climbTaskOf(ctx, t, s, 2).Files, want)
	}
}

// TestSharedFilesAcceptedChangeApproach: a change of approach that adds a file another task of the epic owns is granted
// when that task is open, and when it is accepted and no batch has integrated it: the file joins the task's files.
func TestSharedFilesAcceptedChangeApproach(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		status string
	}{
		{"an open task owns the file", "open"},
		{"an accepted task has not integrated the file", "done"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			s := openEpicStore(t)
			ladderApproachOnRung5(t, s, nil)
			ladderApproachOwn(t, s, 1, "thing.go")
			sharedHolderTask(t, s, 2, tc.status)
			ladderApproachOwn(t, s, 2, "held.go")
			action := ladderApproachFilesAction(t, s, 1, []string{"held.go"}, nil)

			stop, err := s.CarryOutAction(ctx, action, closeAt())
			if err != nil || stop != nil {
				t.Fatalf("CarryOutAction = %v, %v; want the change taken with no error", stop, err)
			}
			if state, refusal := ladderApproachState(t, s, action); state != actionTaken || refusal != "" {
				t.Errorf(
					"action 1 of epic %s = state %q, refusal %q; want taken with no refusal", climbEpic, state, refusal,
				)
			}
			if want := []string{"thing.go", "held.go"}; !reflect.DeepEqual(climbTaskOf(ctx, t, s, 1).Files, want) {
				t.Errorf("Files of task 1 = %v, want %v", climbTaskOf(ctx, t, s, 1).Files, want)
			}
		})
	}
}

// TestSharedFilesAcceptedSettleVersion: a checking version whose list adds a file another task of the epic owns settles
// applied, and the task's files become the list, the shared file among them.
func TestSharedFilesAcceptedSettleVersion(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		status string
	}{
		{"an open task owns the file", "open"},
		{"an accepted task has not integrated the file", "done"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			s := openEpicStore(t)
			from := climbFrom()
			seedClimbTask(t, s, from)
			ladderApproachOwn(t, s, 1, "a.go")
			sharedHolderTask(t, s, 2, tc.status)
			ladderApproachOwn(t, s, 2, "held.go")
			to := from
			to.Rerun = 1
			checking := evidence.Item{
				Capture: []string{"go", "test", "./internal/store/"}, Limit: 300,
				Conclusion: "the store holds", Judge: "read the output",
			}
			version := TaskVersionInput{
				Source: SourceOrchestrator, Step: "step-2", Reason: "the evidence and files move", Evidence: &checking,
				Files: []string{"a.go", "held.go"},
			}
			decision := TaskDecision{ID: "T1-request-1", Text: "T1 checks held.go"}
			if err := s.RerunTask(ctx, climbEpic, 1, from, to, version, decision, climbedAt()); err != nil {
				t.Fatalf("RerunTask: %v", err)
			}
			settle := TaskVersionSettle{Version: 2, State: VersionApplied, RedRun: "5a1c0e9f3b7d4c2a8e6f1b0d9c3a7e52"}
			if err := s.SettleTaskVersion(ctx, climbEpic, 1, to, to, settle, climbedAt()); err != nil {
				t.Fatalf("SettleTaskVersion of a list that adds held.go, which another task owns: %v", err)
			}
			if want := []string{"a.go", "held.go"}; !reflect.DeepEqual(climbTaskOf(ctx, t, s, 1).Files, want) {
				t.Errorf("Files of task 1 = %v, want %v", climbTaskOf(ctx, t, s, 1).Files, want)
			}
		})
	}
}
