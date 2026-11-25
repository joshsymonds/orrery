package planner

import (
	"fmt"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/store"
)

// startKeptRunner starts a runner over s and steps that reads origin's main through enginehost.FetchOriginMain, as
// orreryd does, and returns it once its first pass has claimed the epic's first turn.
func startKeptRunner(t *testing.T, s *store.Store, steps *fakeSteps) *Runner {
	t.Helper()
	cfg := runnerConfig(s, steps, func() (string, error) { return runnerAgent, nil })
	cfg.OriginMain = enginehost.FetchOriginMain
	return runRunner(t, cfg)
}

// kickBaseStartEpic sends baseStartEpic an owner kick and passes runner, so the kick's trigger claims the epic's next
// turn. The turn starts its step on the runner's goroutines, and endKeptTurn waits for it.
func kickBaseStartEpic(t *testing.T, s *store.Store, runner *Runner) {
	t.Helper()
	maker := store.Maker{By: store.MakerOwner}
	if err := s.KickEpic(t.Context(), baseStartEpic, "plan the next task", maker, claimedAt()); err != nil {
		t.Fatalf("KickEpic %s: %v", baseStartEpic, err)
	}
	runner.Pass(t.Context())
}

// endKeptTurn waits until the n-th planner step of the test starts, ends it as done with result, and waits until the
// turn that started it succeeds. It returns the start.
func endKeptTurn(t *testing.T, s *store.Store, steps *fakeSteps, n int, result string) enginehost.StepStart {
	t.Helper()
	waitFor(t, &steps.changeNotice, fmt.Sprintf("planner step %d to start", n), func() bool {
		return len(steps.started()) == n
	})
	started := steps.started()[n-1]
	steps.end(fmt.Sprintf("step-%d", n), store.StepRow{End: "done", Result: result})
	waitFor(t, &steps.changeNotice, fmt.Sprintf("turn %d to succeed", n), func() bool {
		turns := baseStartRow(t, s).Turns
		return len(turns) >= n && turns[n-1].State == "succeeded"
	})
	return started
}

// requireOneTaskStatus asserts that baseStartEpic holds exactly one task, and that the task's status is want.
func requireOneTaskStatus(t *testing.T, s *store.Store, want string) {
	t.Helper()
	tasks := baseStartRow(t, s).Tasks
	if len(tasks) != 1 {
		t.Fatalf("epic holds %d tasks, want exactly one", len(tasks))
	}
	if tasks[0].Status != want {
		t.Fatalf("task status = %q, want %q", tasks[0].Status, want)
	}
}

// requireOriginMainAt asserts that fetching origin's main of repo's checkout names want, so the commit the planner's
// own fetch reads is the one the test moved origin's main to.
func requireOriginMainAt(t *testing.T, repo baseStartRepo, want string) {
	t.Helper()
	got, err := enginehost.FetchOriginMain(t.Context(), repo.checkout)
	if err != nil {
		t.Fatalf("FetchOriginMain: %v", err)
	}
	if got != want {
		t.Fatalf("origin's main fetches as %s, want %s", got, want)
	}
}

// requireNoMoveTell asserts that no tell of baseStartEpic names commit, which a move tell does, and that none says the
// base moved.
func requireNoMoveTell(t *testing.T, s *store.Store, commit string) {
	t.Helper()
	for _, text := range baseStartTells(t, s) {
		if strings.Contains(text, commit) || strings.Contains(text, "base moved") {
			t.Errorf("epic holds tell %q, want no move tell", text)
		}
	}
}

// TestEpicBaseKeptWithOpenThenDoneTask asserts that an epic keeps its base and stored base across three planner turns
// while origin's main moves ahead before the second and the third, and that no tell of the epic names the commit
// origin's main moved to before the turn that reads it. The first turn runs while origin's main is at the base, and
// plans one open task. The second turn runs with the task open. AcceptTask then marks the task done, and the third turn
// runs with it done. The base move reads origin's main through a real git fetch and ancestry check, and the epic's base
// is a real commit, so the turns need one invented repository with a bare origin, which newBaseStartRepo makes once for
// all three.
func TestEpicBaseKeptWithOpenThenDoneTask(t *testing.T) {
	t.Parallel()
	repo := newBaseStartRepo(t)
	s := openRunnerStore(t)
	createBaseStartEpic(t, s, repo)
	steps := newFakeSteps()
	steps.watchStore(s)
	runner := startKeptRunner(t, s, steps)

	requireStartsAt(t, endKeptTurn(t, s, steps, 1, validPlan), repo.base)
	requireOneTaskStatus(t, s, "open")

	openAhead := advanceOrigin(t, repo)
	requireOriginMainAt(t, repo, openAhead)
	kickBaseStartEpic(t, s, runner)

	requireStartsAt(t, endKeptTurn(t, s, steps, 2, emptyPlan), repo.base)
	requireBaseAt(t, s, repo.base)
	requireNoMoveTell(t, s, openAhead)

	task := baseStartRow(t, s).Tasks[0]
	if err := s.AcceptTask(t.Context(), baseStartEpic, task.Seq, task.Build, "reviewed"); err != nil {
		t.Fatalf("AcceptTask: %v", err)
	}
	requireOneTaskStatus(t, s, "done")

	doneAhead := advanceOrigin(t, repo)
	requireOriginMainAt(t, repo, doneAhead)
	kickBaseStartEpic(t, s, runner)

	requireStartsAt(t, endKeptTurn(t, s, steps, 3, emptyPlan), repo.base)
	requireBaseAt(t, s, repo.base)
	requireNoMoveTell(t, s, doneAhead)
}

// TestEpicBaseKeptWithBranch asserts that an epic with a branch keeps its base and stored base across two planner turns
// while origin's main moves ahead between them, and that no tell of the epic names origin's main. The branch is set
// before the first turn, and the epic has no task.
func TestEpicBaseKeptWithBranch(t *testing.T) {
	t.Parallel()
	repo := newBaseStartRepo(t)
	s := openRunnerStore(t)
	createBaseStartEpic(t, s, repo)
	if err := s.SetEpicBranch(t.Context(), baseStartEpic, "created"); err != nil {
		t.Fatalf("SetEpicBranch %s: %v", baseStartEpic, err)
	}
	steps := newFakeSteps()
	steps.watchStore(s)
	runner := startKeptRunner(t, s, steps)

	requireStartsAt(t, endKeptTurn(t, s, steps, 1, emptyPlan), repo.base)
	if branch := baseStartRow(t, s).Branch; branch == "" {
		t.Fatalf("epic branch is empty, want the branch the test set")
	}

	ahead := advanceOrigin(t, repo)
	requireOriginMainAt(t, repo, ahead)
	kickBaseStartEpic(t, s, runner)

	requireStartsAt(t, endKeptTurn(t, s, steps, 2, emptyPlan), repo.base)
	requireBaseAt(t, s, repo.base)
	requireNoMoveTell(t, s, ahead)
}
