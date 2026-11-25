package interpret

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
)

// reproTestFile is the test file the test item of a repro test's Requirement names, and the first task of its batch
// changes.
const reproTestFile = "internal/a/x_test.go"

// reproBuiltBatch is the path of the batch of a repro test on the built-in epic: the settle walk of its one task.
const reproBuiltBatch = "plan/S1/integrate"

// reproRequirements are the Requirements of a repro test's epic: R1, a test item that names reproTestFile and captures
// scripts/check at a limit of 60 seconds.
func reproRequirements() []store.EpicRequirement {
	return []store.EpicRequirement{{
		ID: "R1", Text: "the runner runs", Item: evidence.Item{
			Capture: []string{"scripts/check"}, Limit: 60, Artifacts: []string{}, Supplied: []string{},
			Conclusion: "the runner runs", Judge: "a judge", Kind: evidence.KindTest, Tests: []string{reproTestFile},
		},
	}}
}

// newReproWorld returns an integrate world whose epic holds the test-item Requirement of reproRequirements, and is
// otherwise the world newIntegrateWorld makes.
func newReproWorld(t *testing.T, opts ...integrateOption) *integrateWorld {
	t.Helper()
	var o integrateOptions
	for _, opt := range opts {
		opt(&o)
	}
	s, path := openNodeStore(t)
	repo, base := nodeRepo(t)
	nodeGit(t, repo, "update-ref", integrateBranch, base)
	if err := s.CreateEpic(context.Background(), store.EpicRow{
		ID: runEpic, Repository: repo, Base: base, Title: "Reproduce the batches", Phase: runPhase,
		Created: runClock(), Changed: runClock(), Gates: nodeGates(), DoneGates: []string{integrateDoneGate},
		Requirements: reproRequirements(), Workflow: o.pin,
	}); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	fakes := newNodeFakes(s)
	logs := &bytes.Buffer{}
	runner := runNodes(s, fakes)
	runner.logger = slog.New(slog.NewTextHandler(logs, nil))
	return &integrateWorld{
		t: t, s: s, path: path, repo: repo, base: base, fakes: fakes, runner: runner, logs: logs,
		viaRunner: o.viaRunner,
	}
}

// toReproGate runs the first batch of a repro world up to its Done gate. The plan starts, its two tasks are planned,
// the test file's task and b.go's, and both are accepted on the base. The pass that prepares the batch starts the gate,
// and the candidate the batch was claimed at is returned.
func (w *integrateWorld) toReproGate() string {
	w.t.Helper()
	first := w.commit(w.base, reproTestFile, "one\n")
	second := w.commit(w.base, "b.go", "two\n")
	w.pass()
	completeTurn(w.t, w.s, store.EpicPlan{
		Decisions: []string{}, Tasks: []store.EpicTaskInput{runTask("test", reproTestFile), runTask("other", "b.go")},
		Outline: "two tasks", Outcome: "complete",
	})
	w.pass()
	w.accept(1, w.base, first)
	w.accept(2, w.base, second)
	w.pass()
	return w.candidate(integrateBatch)
}

// toBuiltReproduction runs a built-in repro world until the reproduction of its batch is running. Red is judged fails
// at base, the plan's one task is built and accepted, and the batch's Done gate and capture pass.
func toBuiltReproduction(t *testing.T) *integrateWorld {
	t.Helper()
	w := newReproWorld(t, pinnedTo(builtinPin(t)), viaRunnerPass())
	w.pass()
	w.endRed()
	w.pass()
	endCost(t, w.s)
	w.pass()
	completeTurn(t, w.s, store.EpicPlan{
		Decisions: []string{}, Tasks: []store.EpicTaskInput{runTask("only", reproTestFile)}, Outline: "one task",
		Outcome: "complete",
	})
	w.pass()
	first := w.commit(w.base, reproTestFile, "one\n")
	w.accept(1, w.base, first)
	w.pass()
	w.endGate(reproBuiltBatch, 0, "")
	w.pass()
	w.endCapture(reproBuiltBatch)
	w.pass()
	return w
}

// reproCheckOf returns the id of the reproduction check of R1 that the built-in world's batch at reproBuiltBatch runs,
// and fails the test when the check is not stored.
func reproCheckOf(t *testing.T, w *integrateWorld) string {
	t.Helper()
	id := workID(t, w.s, reproBuiltBatch, "repro R1")
	if _, err := w.s.NodeCheck(context.Background(), id); err != nil {
		t.Fatalf("reproduction check %s of %s: %v", id, reproBuiltBatch, err)
	}
	return id
}

// TestReproductionChecksCaptureAtOverlayOfBase: once the batch's Done gate and capture pass, the next pass makes the
// overlay of the epic's base with the candidate's test file, pins it under the repro ref of its work, and runs the test
// item's capture at the overlay as the reproduction check, under the work id with the item's capture and limit.
func TestReproductionChecksCaptureAtOverlayOfBase(t *testing.T) {
	t.Parallel()
	w := newReproWorld(t)
	candidate := w.toReproGate()
	w.endGate(integrateBatch, 0, "")
	w.pass()
	w.endCapture(integrateBatch)
	ran := len(w.fakes.checks.calls)

	w.pass()

	if got := len(w.fakes.checks.calls) - ran; got != 1 {
		t.Fatalf("checks the pass ran = %d, want the one reproduction", got)
	}
	id := workID(t, w.s, integrateBatch, "repro R1")
	check := w.fakes.checks.calls[ran]
	overlay := check.Revision
	want := store.NodeCheck{
		ID: id, Epic: runEpic, Path: integrateBatch, Revision: overlay, Argv: []string{"scripts/check"}, Limit: 60,
	}
	if !reflect.DeepEqual(check, want) {
		t.Errorf("reproduction check = %+v, want %+v", check, want)
	}
	if parent := w.git("rev-parse", overlay+"^"); parent != w.base {
		t.Errorf("overlay parent = %s, want the epic's base %s", parent, w.base)
	}
	if files := w.git("ls-tree", "-r", "--name-only", overlay); files != "README\n"+reproTestFile {
		t.Errorf("overlay files = %q, want the base's files and the candidate's test file only", files)
	}
	got := w.git("rev-parse", overlay+":"+reproTestFile)
	if want := w.git("rev-parse", candidate+":"+reproTestFile); got != want {
		t.Errorf("overlay's %s = %s, want the candidate's %s", reproTestFile, got, want)
	}
	ref := "refs/orrery/epic-runner/repro/" + id
	if got := w.git("rev-parse", "--verify", ref); got != overlay {
		t.Errorf("%s = %s, want the overlay %s", ref, got, overlay)
	}
}

// TestBuiltInReproductionDecidesBuilt: on the built-in epic whose red is judged fails, a reproduction whatever it exits,
// and whatever its judge rules, holds nothing against the plan's completion. Each lets the plan end built with the epic
// not parked: a reproduction that exits 0 goes to its judge, whose ruling that it contradicts red the owner is not
// asked about, and one that exits 1 agrees with red and needs no judge.
func TestBuiltInReproductionDecidesBuilt(t *testing.T) {
	t.Parallel()
	for _, exit := range []int{0, 1} {
		t.Run(fmt.Sprintf("exit %d lets the plan end built", exit), func(t *testing.T) {
			t.Parallel()
			w := toBuiltReproduction(t)
			nodeEndCheck(t, w.s, reproCheckOf(t, w), exit, "")
			w.pass()
			if exit == 0 {
				judge := kinds.ReproJudgeID(runAt(t, w.s, reproBuiltBatch), "R1", 0)
				endTaskStep(t, w.s, judge, judgeGateFailed, "failed", judgeFails("it asserts nothing the base lacks"))
				w.pass()
			}
			completeTurn(
				t,
				w.s,
				store.EpicPlan{Decisions: []string{}, Outline: "the batch is green", Outcome: "complete"},
			)
			w.pass()

			if plan := runAt(t, w.s, "plan"); plan.State != store.NodeEnded || plan.Outcome != "complete" {
				t.Errorf("plan = %+v, want ended complete", plan)
			}
			epic := epicOf(t, w.s)
			if epic.Phase != "built" || epic.Parked != "" {
				t.Errorf("epic phase = %q and parked = %q, want built and not parked", epic.Phase, epic.Parked)
			}
			for _, cause := range triggerCausesOf(t, w.s, epic.ID) {
				if strings.HasPrefix(cause, "epic not built") {
					t.Errorf("trigger cause %q, want no planner wake for a plan that ends built", cause)
				}
			}
		})
	}
}
