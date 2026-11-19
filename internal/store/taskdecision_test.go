package store_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// requestDecision is the decision the task decision tests record.
func requestDecision() store.TaskDecision {
	return store.TaskDecision{ID: "T1-request-1", Text: "T1 rerun: the code says so"}
}

// lastDecision returns the newest decision of buildEpic.
func lastDecision(ctx context.Context, t *testing.T, s *store.Store) store.EpicDecision {
	t.Helper()
	row, err := s.Epic(ctx, buildEpic)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if len(row.Decisions) == 0 {
		t.Fatal("epic has no decisions")
	}
	return row.Decisions[len(row.Decisions)-1]
}

// openRequestStore returns a build store whose task 1 is at openBuildOf, with open task 2 owning b.go.
func openRequestStore(ctx context.Context, t *testing.T) *store.Store {
	t.Helper()
	s, raw := openBuildStore(ctx, t)
	addRawTask(t, raw, 2, "open", "b.go")
	if err := s.SetTaskBuild(ctx, buildEpic, 1, store.TaskBuild{}, openBuildOf()); err != nil {
		t.Fatalf("SetTaskBuild: %v", err)
	}
	return s
}

func TestRerunTaskGrantsBehindAWaitedTaskAndRecordsTheDecision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRequestStore(ctx, t)
	to := grantedBuild()
	to.Held, to.After, to.Decisions = "waiting", "2", 1
	err := s.RerunTask(
		ctx,
		buildEpic,
		1,
		openBuildOf(),
		to,
		store.TaskVersionInput{Source: store.SourceOrchestrator, Files: []string{"b.go"}},
		[]int64{2},
		requestDecision(),
		writtenAt(),
	)
	if err != nil {
		t.Fatalf("RerunTask: %v", err)
	}
	task := buildTaskOf(ctx, t, s)
	if want := []string{"a.go", "b.go"}; !reflect.DeepEqual(task.Files, want) || task.Build != to {
		t.Errorf("Files %v build %+v, want %v and %+v", task.Files, task.Build, want, to)
	}
	if got := lastDecision(ctx, t, s); got.ID != "T1-request-1" || got.Text != "T1 rerun: the code says so" {
		t.Errorf("decision = %+v, want the rerun recorded", got)
	}
}

func TestRerunTaskRefusesAFileAnOpenTaskOwnsWhenNotBehindIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRequestStore(ctx, t)
	version := store.TaskVersionInput{Source: store.SourceOrchestrator, Files: []string{"b.go"}}
	err := s.RerunTask(ctx, buildEpic, 1, openBuildOf(), grantedBuild(), version, nil, requestDecision(),
		writtenAt())
	var owned store.FileOwnedError
	if !errors.As(err, &owned) || owned.Task != 2 {
		t.Fatalf("RerunTask = %v, want the file owned by T2", err)
	}
	if task := buildTaskOf(ctx, t, s); task.Build != openBuildOf() {
		t.Errorf("build = %+v, want it unmoved", task.Build)
	}
	row, err := s.Epic(ctx, buildEpic)
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(row.Decisions, func(d store.EpicDecision) bool { return d.ID == "T1-request-1" }) {
		t.Error("the refused rerun recorded its decision")
	}
}

func TestRerunTaskOfAMovedBuildWritesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRequestStore(ctx, t)
	err := s.RerunTask(ctx, buildEpic, 1, grantedBuild(), openBuildOf(), store.TaskVersionInput{}, nil,
		requestDecision(), writtenAt())
	if !errors.Is(err, store.ErrTaskMoved) {
		t.Fatalf("RerunTask from a stale build = %v, want ErrTaskMoved", err)
	}
}

func TestFailTaskDecidedKeepsTheLongerFailureAndRecordsTheDecision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRequestStore(ctx, t)
	failure := strings.Repeat("é", store.TaskDecisionMax)
	err := s.FailTaskDecided(ctx, buildEpic, 1, openBuildOf(), failure, []string{"b.go"}, requestDecision(),
		writtenAt())
	if err != nil {
		t.Fatalf("FailTaskDecided: %v", err)
	}
	task := buildTaskOf(ctx, t, s)
	if task.Status != "failed" || task.Build.Failure != failure[:store.TaskDecisionMax] {
		t.Fatalf("task %q failure of %d bytes, want failed with %d bytes cut at a character", task.Status,
			len(task.Build.Failure), store.TaskDecisionMax)
	}
	if !reflect.DeepEqual(task.NeedsFiles, []string{"b.go"}) {
		t.Errorf("NeedsFiles = %v, want [b.go]", task.NeedsFiles)
	}
	if got := lastDecision(ctx, t, s); got.ID != "T1-request-1" {
		t.Errorf("decision = %+v, want the fail recorded", got)
	}
}

// requestQuestion is a question an orchestrator asks the owner.
func requestQuestion() store.EpicQuestionInput {
	return store.EpicQuestionInput{
		Label: "capture-broken", Text: "T1 (contract-cannot-hold): the capture exits 2",
		Choices:     []store.EpicChoice{{ID: "edit", Does: "edit the contract"}, {ID: "end", Does: "end the epic"}},
		Recommended: "edit", Why: "nothing in the epic changes it", Confidence: "high",
	}
}

func TestAskTaskOpensTheQuestionAndHoldsTheBuildOnIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRequestStore(ctx, t)
	to := openBuildOf()
	to.Held, to.Decisions = "asked", 1
	question := requestQuestion()
	if err := s.AskTask(ctx, buildEpic, 1, openBuildOf(), to, question, question.Text, requestDecision(),
		writtenAt()); err != nil {
		t.Fatalf("AskTask: %v", err)
	}
	row, err := s.Epic(ctx, buildEpic)
	if err != nil {
		t.Fatal(err)
	}
	asked := row.Questions[len(row.Questions)-1]
	if asked.State != store.QuestionOpen || asked.Turn != 0 || asked.Label != question.Label ||
		asked.Text != question.Text || !reflect.DeepEqual(asked.Choices, question.Choices) {
		t.Fatalf("question = %+v, want the orchestrator's question open", asked)
	}
	task := buildTaskOf(ctx, t, s)
	if task.Build.Question != asked.Seq || task.Build.Held != "asked" {
		t.Errorf("build held %q on Q%d, want asked on Q%d", task.Build.Held, task.Build.Question, asked.Seq)
	}
	if got := lastDecision(ctx, t, s); got.ID != "T1-request-1" {
		t.Errorf("decision = %+v, want the ask recorded", got)
	}
}

func TestAskTaskRefusesABadQuestionAndWritesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRequestStore(ctx, t)
	question := requestQuestion()
	question.Choices = question.Choices[:1]
	err := s.AskTask(ctx, buildEpic, 1, openBuildOf(), grantedBuild(), question, "tell", requestDecision(),
		writtenAt())
	if !errors.Is(err, store.ErrBadQuestion) {
		t.Fatalf("AskTask = %v, want ErrBadQuestion", err)
	}
	if task := buildTaskOf(ctx, t, s); task.Build != openBuildOf() {
		t.Errorf("build = %+v, want it unmoved", task.Build)
	}
}
