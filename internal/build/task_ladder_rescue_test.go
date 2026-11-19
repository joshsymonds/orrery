package build

import (
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// The agents and ids the rescue tests use. They are invented, and each role names its own agent. The implementer's
// agent is the one newPassFixture's role file names.
const (
	ladderRescueImplementer  = "claude-impl"
	ladderRescueOrchestrator = "rescue-orchestrator"
	ladderRescuePlanner      = "rescue-planner"
	ladderRescueStepID       = "rescue-step"
)

// ladderRescueVersion returns a rescue version that changes nothing, with the fields given set.
func ladderRescueVersion(fields map[string]any) map[string]any {
	version := map[string]any{
		"changed": []string{}, "deliverable": "", "test_changes": []string{}, "gate": "",
		"evidence": map[string]any{
			"capture": []string{}, "limit": 0, "artifacts": []string{}, "supplied": []string{}, "conclusion": "",
			"judge": "", "kind": "", "tests": []string{},
		},
	}
	maps.Copy(version, fields)
	return version
}

// ladderRescueEvidence returns a capture item a rescue version may set as the task's evidence.
func ladderRescueEvidence() map[string]any {
	return map[string]any{
		"capture": []string{"scripts/check"}, "limit": 60, "artifacts": []string{}, "supplied": []string{},
		"conclusion": "the thing holds", "judge": "the judge", "kind": "capture", "tests": []string{},
	}
}

// ladderRescueReturn renders a rescue return: a done return that changes the deliverable and gives a reason, with the
// fields given set.
func ladderRescueReturn(t *testing.T, fields map[string]any) string {
	t.Helper()
	ret := map[string]any{
		"status": statusDone, "evidence": "a.go:3 calls b", "reason": "the deliverable names the field it needs",
		"version": ladderRescueVersion(map[string]any{
			"changed": []string{"deliverable"}, "deliverable": "The thing holds.",
		}),
	}
	maps.Copy(ret, fields)
	data, err := json.Marshal(ret)
	if err != nil {
		t.Fatalf("encode return: %v", err)
	}
	return string(data)
}

// ladderRescueBuild is task 3's build on rung 3 at its rescue unit, with a red run held.
func ladderRescueBuild() store.TaskBuild {
	return store.TaskBuild{Stage: StageOrchestrate, Base: decideBranch, Attempt: store.RescueRung, RedRun: "red-run"}
}

// ladderRescueView returns the view of open task 3 of decideEpic on build, with versions stored and step as its rescue
// step.
func ladderRescueView(build store.TaskBuild, step *store.StepRow, versions ...store.TaskVersion) View {
	task := store.EpicTask{Seq: 3, Files: []string{"a.go"}, Status: taskOpen, Build: build, Versions: versions}
	return View{Epic: decideEpic, Task: task, Step: step}
}

// ladderRescueStep returns a rescue step of task 3 ended with end and kept result.
func ladderRescueStep(end, result string) *store.StepRow {
	return &store.StepRow{ID: ladderRescueStepID, End: end, Result: result, Schema: RescueSchema}
}

// ladderRescueCheckStarts fails the test unless decideRescue starts a rescue with no step under its derived id, and
// starts nothing while the epic is paused or parked.
func ladderRescueCheckStarts(t *testing.T) {
	t.Helper()
	got := decideRescue(ladderRescueView(ladderRescueBuild(), nil))
	want := StepID(decideEpic, 3, StageOrchestrate, store.RescueRung, 0, 0, 0)
	if got.Kind != ActStartStep || got.Unit != StageOrchestrate || got.ID != want {
		t.Errorf("decideRescue with no step = %+v, want the rescue started under %s", got, want)
	}
	paused := ladderRescueView(ladderRescueBuild(), nil)
	paused.Paused = true
	parked := ladderRescueView(ladderRescueBuild(), nil)
	parked.Parked = true
	for _, view := range []View{paused, parked} {
		if got := decideRescue(view); got.Kind != ActNone {
			t.Errorf("decideRescue while paused or parked = %+v, want ActNone", got)
		}
	}
}

// ladderRescueCheckDecision fails the test unless a done return with status done is carried out as the rescue's
// version: stored under the rescue source with its decision, the build at implement one rerun on.
func ladderRescueCheckDecision(t *testing.T) {
	t.Helper()
	deliverable := "The thing holds."
	reason := "the deliverable names the field it needs"
	got := decideRescue(ladderRescueView(ladderRescueBuild(), ladderRescueStep(endDone, ladderRescueReturn(t, nil))))
	want := Action{
		Kind: ActDecide,
		Next: store.TaskBuild{
			Stage: StageImplement, Base: decideBranch, Attempt: store.RescueRung, Rerun: 1, RedRun: "red-run",
		},
		Decision: &store.TaskDecision{ID: "T3-rescue", Text: "T3 rescue: " + reason},
		Version: store.TaskVersionInput{
			Source: store.SourceRescue, Step: ladderRescueStepID, Reason: reason, Deliverable: &deliverable,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("decideRescue of a done return:\n got %+v\nwant %+v", got, want)
	}
}

// ladderRescueCheckStage fails the test unless the rescue's build goes to red when its version changes the evidence or
// when no red run is held, and to implement otherwise.
func ladderRescueCheckStage(t *testing.T) {
	t.Helper()
	noRed := ladderRescueBuild()
	noRed.RedRun = ""
	evidenceReturn := ladderRescueReturn(t, map[string]any{"version": ladderRescueVersion(map[string]any{
		"changed": []string{"evidence"}, "evidence": ladderRescueEvidence(),
	})})
	for _, tc := range []struct {
		name  string
		build store.TaskBuild
		ret   string
		stage string
	}{
		{"the evidence changes", ladderRescueBuild(), evidenceReturn, StageRed},
		{"no red run is held", noRed, ladderRescueReturn(t, nil), StageRed},
		{"the deliverable changes with a red run held", ladderRescueBuild(), ladderRescueReturn(t, nil), StageImplement},
	} {
		got := decideRescue(ladderRescueView(tc.build, ladderRescueStep(endDone, tc.ret)))
		if got.Kind != ActDecide || got.Next.Stage != tc.stage {
			t.Errorf("%s: decideRescue = %+v, want ActDecide to stage %s", tc.name, got, tc.stage)
		}
	}
}

// ladderRescueCheckClimbs fails the test unless a blocked return, a return that does not decode and a done return whose
// changed names nothing each climb the task with a failure that starts `rescue:` and says what happened.
func ladderRescueCheckClimbs(t *testing.T) {
	t.Helper()
	const evidence = "the base has no b.go: ls shows none"
	for _, tc := range []struct {
		name string
		end  string
		ret  string
		want string
	}{
		{
			"blocked", endReturned, ladderRescueReturn(t, map[string]any{
				"status": "blocked", "evidence": evidence, "version": ladderRescueVersion(nil),
			}), "blocked: " + evidence,
		},
		{"undecodable", endDone, "[", "does not decode"},
		{
			"changed names nothing", endDone,
			ladderRescueReturn(t, map[string]any{"version": ladderRescueVersion(nil)}),
			"version.changed: empty on a rescue",
		},
	} {
		got := decideRescue(ladderRescueView(ladderRescueBuild(), ladderRescueStep(tc.end, tc.ret)))
		if got.Kind != ActFail || !strings.HasPrefix(got.Reason, "rescue: ") || !strings.Contains(got.Reason, tc.want) {
			t.Errorf("%s: decideRescue = %+v, want ActFail starting `rescue:` and naming %q", tc.name, got, tc.want)
		}
	}
}

// ladderRescueCheckHolds fails the test unless a stopped rescue holds the build, and a rescue step with no end is
// waited on.
func ladderRescueCheckHolds(t *testing.T) {
	t.Helper()
	stopped := decideRescue(ladderRescueView(ladderRescueBuild(), ladderRescueStep(endStopped, "")))
	if stopped.Kind != ActPause || stopped.Next.Held != HeldStopped {
		t.Errorf("decideRescue of a stopped step = %+v, want the build held stopped", stopped)
	}
	running := decideRescue(ladderRescueView(ladderRescueBuild(), ladderRescueStep("", "")))
	if running.Kind != ActNone {
		t.Errorf("decideRescue of a step with no end = %+v, want ActNone", running)
	}
}

// TestTaskLadderRescueStartsOnRungThree: a task on rung 3 that has begun no unit of the rung moves to its rescue, from
// implement or red, while a task on another attempt, or one with a rescue version, snapshots its refs as before. A
// rescue the engine refuses to start climbs the task.
func TestTaskLadderRescueStartsOnRungThree(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		view View
	}{
		{"implement", ladderRescueView(store.TaskBuild{
			Stage: StageImplement, Base: decideBranch, Attempt: store.RescueRung, RedRun: "red-run",
		}, nil)},
		{"red", ladderRescueView(store.TaskBuild{Stage: StageRed, Base: decideBranch, Attempt: store.RescueRung}, nil)},
	} {
		if got := Decide(tc.view); got.Kind != ActMove || got.Next.Stage != StageOrchestrate || got.Next.Step != "" {
			t.Errorf("%s on rung 3 = %+v, want a move to the rescue's orchestrate unit with no step", tc.name, got)
		}
	}
	implement := store.TaskBuild{Stage: StageImplement, Base: decideBranch, Attempt: 2, RedRun: "red-run"}
	for _, tc := range []struct {
		name string
		view View
	}{
		{"attempt 2", ladderRescueView(implement, nil)},
		{"attempt 4", ladderRescueView(store.TaskBuild{
			Stage: StageImplement, Base: decideBranch, Attempt: store.OrchestratorRung, RedRun: "red-run",
		}, nil)},
		{"a rescue version", ladderRescueView(
			store.TaskBuild{Stage: StageImplement, Base: decideBranch, Attempt: store.RescueRung, RedRun: "red-run"},
			nil, store.TaskVersion{Version: 2, Source: store.SourceRescue, State: store.VersionApplied},
		)},
	} {
		if got := Decide(tc.view); got.Kind != ActSnapshot {
			t.Errorf("%s = %+v, want the implement unit to snapshot its refs as before", tc.name, got)
		}
	}
	start := DecideStartError(ladderRescueView(ladderRescueBuild(), nil), errors.New("the engine refused it"))
	if start.Kind != ActFail || start.Reason != "rescue did not start: the engine refused it" {
		t.Errorf("DecideStartError of a rescue = %+v, want ActFail naming `rescue did not start`", start)
	}
}

// TestTaskLadderRescueDecides: decideRescue starts the rescue under its derived id, carries a done return out as the
// rescue's version, sends the build to red or implement, climbs the task off a refused return, and holds or waits as
// the step and the epic say.
func TestTaskLadderRescueDecides(t *testing.T) {
	t.Parallel()
	t.Run("starts under its derived id", func(t *testing.T) {
		t.Parallel()
		ladderRescueCheckStarts(t)
	})
	t.Run("a done return is the rescue's version", func(t *testing.T) {
		t.Parallel()
		ladderRescueCheckDecision(t)
	})
	t.Run("the build goes to red for new evidence or no red run", func(t *testing.T) {
		t.Parallel()
		ladderRescueCheckStage(t)
	})
	t.Run("a refused return climbs the task", func(t *testing.T) {
		t.Parallel()
		ladderRescueCheckClimbs(t)
	})
	t.Run("a stopped step holds and an unended one waits", func(t *testing.T) {
		t.Parallel()
		ladderRescueCheckHolds(t)
	})
}

// ladderRescueFixture returns a fixture whose task 1 has climbed onto rung 3 with nothing of rung 3 begun, and whose
// role file has the lines given.
func ladderRescueFixture(t *testing.T, roleLines string) *passFixture {
	t.Helper()
	f := newPassFixture(t)
	redRun := passRunID(StageRed, 1)
	f.addRun(redRun, evidence.PhaseRed, f.base, 1, evidence.VerdictFails)
	f.setBuild(store.TaskBuild{Stage: StageImplement, Base: f.base, Attempt: 1, Revision: f.base, RedRun: redRun})
	ladderClimbExecute(f, Action{Kind: ActFail, Reason: "returned blocked: first cause"})
	ladderClimbExecute(f, Action{Kind: ActFail, Reason: "returned blocked: second cause"})
	f.writeRoles(roleLines)
	return f
}

// ladderRescueStart returns the start of task 1's rescue step on f, the fixture of ladderRescueFixture, from
// rescueStart.
func ladderRescueStart(t *testing.T, f *passFixture) enginehost.StepStart {
	t.Helper()
	id := StepID(passEpic, 1, StageOrchestrate, store.RescueRung, 0, 0, 0)
	start, err := f.runner.rescueStart(f.ctx, f.epic(), View{Epic: passEpic, Task: f.task()}, id)
	if err != nil {
		t.Fatalf("rescueStart: %v", err)
	}
	return start
}

// ladderRescueCheckAgent fails the test unless the rescue runs the role file's orchestrator agent, and the planner's
// when the role file names no orchestrator.
func ladderRescueCheckAgent(t *testing.T) {
	t.Helper()
	orchestrator := ladderRescueFixture(t, "orchestrator = \""+ladderRescueOrchestrator+"\"\n"+
		"planner = \""+ladderRescuePlanner+"\"\n")
	if got := ladderRescueStart(t, orchestrator).Agent; got != ladderRescueOrchestrator {
		t.Errorf("rescue Agent = %q, want the orchestrator's agent %q", got, ladderRescueOrchestrator)
	}
	planner := ladderRescueFixture(t, "planner = \""+ladderRescuePlanner+"\"\n")
	if got := ladderRescueStart(t, planner).Agent; got != ladderRescuePlanner {
		t.Errorf("rescue Agent = %q, want the planner's agent %q when the role file has no orchestrator", got,
			ladderRescuePlanner)
	}
}

// ladderRescueCheckReadOnly fails the test unless the rescue is read-only at the task's base in the epic's repository,
// with RescueSchema, its return check, the done value and the epic and task it names.
func ladderRescueCheckReadOnly(t *testing.T, f *passFixture, start enginehost.StepStart) {
	t.Helper()
	task := f.task()
	want := StepID(passEpic, 1, StageOrchestrate, store.RescueRung, 0, 0, 0)
	if start.ID != want || !start.ReadOnly || start.Workspace != f.repo || start.Revision != task.Build.Base ||
		start.Schema != RescueSchema || start.ReturnCheck == nil || start.DoneValue != "done" ||
		start.Epic != passEpic || start.Task != "T1" {
		t.Errorf("start = %+v, want a read-only rescue %s at the task's base with RescueSchema", start, want)
	}
}

// ladderRescueCheckPrompt fails the test unless the rescue's prompt starts with RescueRole and carries the task's
// ladder, one line for each rung in the planner's rung format.
func ladderRescueCheckPrompt(t *testing.T, start enginehost.StepStart) {
	t.Helper()
	if !strings.HasPrefix(start.Prompt, strings.TrimRight(RescueRole, "\n")+"\n") {
		t.Errorf("prompt does not start with RescueRole:\n%s", start.Prompt)
	}
	for _, want := range []string{
		"\n## Ladder\n",
		"  rung 1 attempt -> failed: returned blocked: first cause",
		"  rung 2 attempt moved by unit: returned blocked: first cause -> failed: returned blocked: second cause",
		"  rung 3 rescue moved by unit: returned blocked: second cause",
		"\n## Worktree\n", "\n## Tasks\n", "\n## History\n",
	} {
		if !strings.Contains(start.Prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, start.Prompt)
		}
	}
}

// ladderRescueCheckReturn fails the test unless the rescue's return check refuses a test change outside the task's
// files and passes a well-formed return.
func ladderRescueCheckReturn(t *testing.T, check func(json.RawMessage) []string) {
	t.Helper()
	outside := ladderRescueReturn(t, map[string]any{"version": ladderRescueVersion(map[string]any{
		"changed": []string{"test_changes"}, "test_changes": []string{"internal/build/other_test.go"},
	})})
	want := []string{"version.test_changes[0]: not one of the task's files"}
	if got := check(json.RawMessage(outside)); !slices.Equal(got, want) {
		t.Errorf("check of a test change outside the task's files = %q, want %q", got, want)
	}
	if got := check(json.RawMessage(ladderRescueReturn(t, nil))); len(got) != 0 {
		t.Errorf("check of a well-formed return = %q, want none", got)
	}
}

// TestTaskLadderRescueStart: the rescue runs the orchestrator's agent, or the planner's, read-only with RescueSchema.
// Its prompt starts with RescueRole and carries the ladder, and its return check refuses a test change outside the
// task's files and passes a well-formed return.
func TestTaskLadderRescueStart(t *testing.T) {
	t.Parallel()
	t.Run("runs the orchestrator's agent, or the planner's", func(t *testing.T) {
		t.Parallel()
		ladderRescueCheckAgent(t)
	})
	t.Run("is read-only with the rescue schema", func(t *testing.T) {
		t.Parallel()
		f := ladderRescueFixture(t, "orchestrator = \""+ladderRescueOrchestrator+"\"\n")
		ladderRescueCheckReadOnly(t, f, ladderRescueStart(t, f))
	})
	t.Run("prompt starts with the role and carries the ladder", func(t *testing.T) {
		t.Parallel()
		f := ladderRescueFixture(t, "orchestrator = \""+ladderRescueOrchestrator+"\"\n")
		ladderRescueCheckPrompt(t, ladderRescueStart(t, f))
	})
	t.Run("return check refuses a test change outside the task's files", func(t *testing.T) {
		t.Parallel()
		f := ladderRescueFixture(t, "orchestrator = \""+ladderRescueOrchestrator+"\"\n")
		ladderRescueCheckReturn(t, ladderRescueStart(t, f).ReturnCheck)
	})
}

// ladderRescueCheckStarted fails the test unless task 1 is at its rescue unit on rung 3 with no step, and the rescue's
// step started under id with the orchestrator's agent and RescueSchema.
func ladderRescueCheckStarted(t *testing.T, f *passFixture, id string) {
	t.Helper()
	if build := f.task().Build; build.Stage != StageOrchestrate || build.Step != "" ||
		store.RungOf(build) != store.RescueRung {
		t.Fatalf("build %+v, want the rescue unit on rung 3 with no step", build)
	}
	if start := f.lastStart(); start.ID != id || start.Agent != ladderRescueOrchestrator ||
		start.Schema != RescueSchema {
		t.Fatalf("last start %s under %q with schema %q, want the rescue %s under the orchestrator's agent",
			start.ID, start.Agent, start.Schema, id)
	}
}

// ladderRescueCheckDecided fails the test unless the rescue's done return stored an applied rescue version under
// T1-rescue, recorded that decision, and moved task 1 to implement at attempt 3, rerun 1.
func ladderRescueCheckDecided(t *testing.T, f *passFixture, id string) {
	t.Helper()
	task := f.task()
	if len(task.Versions) != 1 {
		t.Fatalf("versions = %+v, want the rescue's one version", task.Versions)
	}
	version := task.Versions[0]
	if version.Source != store.SourceRescue || version.State != store.VersionApplied ||
		version.Decision != "T1-rescue" || version.Step != id {
		t.Errorf("version %+v, want an applied rescue version of %s under decision T1-rescue", version, id)
	}
	if decisions := f.epic().Decisions; len(decisions) == 0 || decisions[len(decisions)-1].ID != "T1-rescue" {
		t.Errorf("decisions = %+v, want T1-rescue recorded last", decisions)
	}
	if build := task.Build; build.Stage != StageImplement || build.Attempt != store.RescueRung || build.Rerun != 1 {
		t.Errorf("build stage %q attempt %d rerun %d, want implement at attempt 3, rerun 1",
			build.Stage, build.Attempt, build.Rerun)
	}
}

// TestTaskLadderRescueRunsOnceThenOneAttempt: a task climbed onto rung 3 moves to its rescue and starts it. The
// rescue's done return stores its version and moves the build to implement at attempt 3, where the implementer starts
// under that version. When that attempt fails the task climbs to attempt 4, and no second rescue starts there.
func TestTaskLadderRescueRunsOnceThenOneAttempt(t *testing.T) {
	t.Parallel()
	f := ladderRescueFixture(t, "orchestrator = \""+ladderRescueOrchestrator+"\"\n")
	rescueID := StepID(passEpic, 1, StageOrchestrate, store.RescueRung, 0, 0, 0)
	implementID := passStep(StageImplement, store.RescueRung, 1)

	f.pass()
	ladderRescueCheckStarted(t, f, rescueID)

	f.setStep(rescueID, endDone, ladderRescueReturn(t, nil))
	f.pass()
	ladderRescueCheckDecided(t, f, rescueID)
	if start := f.lastStart(); start.ID != implementID || start.Agent != ladderRescueImplementer {
		t.Fatalf("last start %s under %q, want the implementer %s under %q at attempt 3", start.ID, start.Agent,
			implementID, ladderRescueImplementer)
	}

	f.setStep(implementID, endChecksFailed, "")
	f.pass()
	if build := f.task().Build; build.Attempt != store.OrchestratorRung {
		t.Fatalf("build attempt = %d after the failed attempt, want the orchestrator's rung %d",
			build.Attempt, store.OrchestratorRung)
	}
	f.pass()
	if got := len(f.steps.starts); got != 3 {
		t.Errorf("starts = %d, want the rescue, the attempt-3 implementer and the attempt-4 implementer only", got)
	}
	if last := f.lastStart(); last.ID != passStep(StageImplement, store.OrchestratorRung, 0) {
		t.Errorf("last start %s, want the attempt-4 implementer", last.ID)
	}
	if n := f.startCount(rescueID); n != 1 {
		t.Errorf("rescue started %d times, want once", n)
	}
}
