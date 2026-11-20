package build

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// implementerReturnError returns what ImplementerSchema says of the return ret: nil when it accepts it.
func implementerReturnError(t *testing.T, ret string) error {
	t.Helper()
	var schema jsonschema.Schema
	if err := json.Unmarshal([]byte(ImplementerSchema), &schema); err != nil {
		t.Fatalf("ImplementerSchema is not a schema: %v", err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatalf("resolve ImplementerSchema: %v", err)
	}
	var instance any
	if err := json.Unmarshal([]byte(ret), &instance); err != nil {
		t.Fatalf("return %s is not JSON: %v", ret, err)
	}
	return resolved.Validate(instance)
}

func TestNeedsFilesSchemaAcceptsNeedsFiles(t *testing.T) {
	t.Parallel()
	ret := `{"status":"needs_context","summary":"stuck","needs_files":["internal/other/a.go","internal/other/b.go"]}`
	if err := implementerReturnError(t, ret); err != nil {
		t.Fatalf("schema refused a return with needs_files: %v", err)
	}
}

func TestNeedsFilesSchemaRefusesUnknownKeysAndNonStringPaths(t *testing.T) {
	t.Parallel()
	for _, ret := range []string{
		`{"status":"done","summary":"built it","extra":true}`,
		`{"status":"needs_context","summary":"stuck","needs_file":["a.go"]}`,
		`{"status":"needs_context","summary":"stuck","needs_files":[7]}`,
		`{"status":"needs_context","summary":"stuck","needs_files":"a.go"}`,
	} {
		if err := implementerReturnError(t, ret); err == nil {
			t.Errorf("schema accepted %s, want a refusal", ret)
		}
	}
}

func TestNeedsFilesSchemaAcceptsReturnWithoutNeedsFiles(t *testing.T) {
	t.Parallel()
	ret := `{"status":"needs_context","summary":"stuck"}`
	if err := implementerReturnError(t, ret); err != nil {
		t.Fatalf("schema refused a return without needs_files: %v", err)
	}
}

// needsFilesView is the view of task 3 whose implementer step ended returned, with status and kept result.
func needsFilesView(status, result string) View {
	build := store.TaskBuild{
		Stage: StageImplement, Base: decideBranch, Attempt: 1, Rerun: 1, NoVerdict: 1, Step: "impl-step",
		RedRun: "red-run",
	}
	return View{
		Epic: decideEpic,
		Step: &store.StepRow{ID: "impl-step", End: endReturned, Status: status, Result: result},
		Task: store.EpicTask{Seq: 3, Build: build},
	}
}

// exhausted returns view with its build past the orchestrator's decision limit, so a request is settled as it was
// before the orchestrator.
func exhausted(view View) View {
	view.Task.Build.Decisions = decisionLimit
	return view
}

// atRerun returns view with its build at rerun.
func atRerun(view View, rerun int64) View {
	view.Task.Build.Rerun = rerun
	return view
}

// owning returns view with its task owning files.
func owning(view View, files ...string) View {
	view.Task.Files = files
	return view
}

func TestNeedsFilesDecideCarriesThePathsOfANeedsContextReturn(t *testing.T) {
	t.Parallel()
	runDecideCases(t, Decide, []decideCase{
		{
			name: "needs_context past the grant and decision limits fails carrying the paths it names",
			view: exhausted(atRerun(needsFilesView(
				"needs_context",
				`{"status":"needs_context","summary":"the spec is short","needs_files":["a.go","b/c.go"]}`,
			), grantLimit)),
			want: Action{
				Kind: ActFail, Reason: "returned needs_context: the spec is short",
				NeedsFiles: []string{"a.go", "b/c.go"},
			},
		},
		{
			name: "needs_context naming a path inside .git with no decisions left fails carrying the paths it names",
			view: exhausted(needsFilesView(
				"needs_context",
				`{"status":"needs_context","summary":"the spec is short","needs_files":["a.go",".git/config"]}`,
			)),
			want: Action{
				Kind: ActFail, Reason: "returned needs_context: the spec is short",
				NeedsFiles: []string{"a.go", ".git/config"},
			},
		},
		{
			name: "needs_context naming only files the task owns with no decisions left fails carrying them",
			view: exhausted(owning(needsFilesView(
				"needs_context",
				`{"status":"needs_context","summary":"the spec is short","needs_files":["a.go"]}`,
			), "a.go")),
			want: Action{
				Kind: ActFail, Reason: "returned needs_context: the spec is short", NeedsFiles: []string{"a.go"},
			},
		},
		{
			name: "needs_context naming files the task does not own grants the new ones and reruns the implementer",
			view: owning(needsFilesView(
				"needs_context",
				`{"status":"needs_context","summary":"the spec is short","needs_files":["a.go","b/c.go","b/c.go"]}`,
			), "a.go"),
			want: Action{
				Kind: ActGrant, Reason: "returned needs_context: the spec is short", NeedsFiles: []string{"b/c.go"},
				Next: store.TaskBuild{
					Stage: StageImplement, Base: decideBranch, Attempt: 1, Rerun: 2, RedRun: "red-run",
					Failure: "you returned needs_context for files outside your list, and these are now yours: " +
						"b/c.go. Your earlier work is in this worktree; continue from it.",
				},
			},
		},
		{
			name: "needs_context without needs_files and no decisions left fails as it did before the field",
			view: exhausted(
				needsFilesView("needs_context", `{"status":"needs_context","summary":"the spec is short"}`),
			),
			want: Action{Kind: ActFail, Reason: "returned needs_context: the spec is short"},
		},
		{
			name: "blocked with no decisions left carries no paths even when its return names some",
			view: exhausted(needsFilesView(
				"blocked",
				`{"status":"blocked","summary":"the gate is broken","needs_files":["a.go"]}`,
			)),
			want: Action{Kind: ActFail, Reason: "returned blocked: the gate is broken"},
		},
	})
}

func TestNeedsFilesMalformedListGivesNoVerdictLikeAnyUnreadableReturn(t *testing.T) {
	t.Parallel()
	view := needsFilesView("needs_context", `{"status":"needs_context","summary":"stuck","needs_files":"a.go"}`)
	view.Task.Build.Rerun = 0
	view.Task.Build.NoVerdict = 0
	runDecideCases(t, Decide, []decideCase{
		{
			name: "needs_context whose needs_files is not a list reruns the implementer once",
			view: view,
			want: Action{Kind: ActMove, Next: store.TaskBuild{
				Stage: StageImplement, Base: decideBranch, Attempt: 1, Rerun: 1, NoVerdict: 1, Step: "impl-step",
				RedRun: "red-run",
			}},
		},
	})
}

func TestNeedsFilesBriefEndsByAskingForThem(t *testing.T) {
	t.Parallel()
	const sentence = "When the work needs files beyond the owned list, return needs_context and name each one in " +
		"needs_files."
	got := Brief(briefEpic(), briefTask(), briefGate(), store.EvidenceRun{}, nil, nil, "")
	if !strings.HasSuffix(got, sentence) {
		t.Fatalf("brief does not end with the needs_files request; brief:\n%s", got)
	}
}

// newOwnerFixture returns a fixture whose epic has task 1 as newPassFixture plans it and an open task 2 that owns file.
func newOwnerFixture(t *testing.T, file string) *passFixture {
	t.Helper()
	plan := passTwoTaskPlan()
	plan.Tasks[1].Files = []string{file}
	return newPlanFixture(t, plan)
}

// TestNeedsFilesPassStoresThePathsWithTheClimb covers R3: an implementer that returns needs_context with needs_files
// that cannot be granted, since another open task owns one, on a task with no orchestrator decisions left, climbs the
// task with the paths stored under it.
func TestNeedsFilesPassStoresThePathsWithTheClimb(t *testing.T) {
	t.Parallel()
	f := newOwnerFixture(t, "internal/other/a.go")
	first := []int64{1}
	f.runner.PassTasks(f.ctx, passEpic, first)
	f.addRun(RunID(passEpic, 1, StageRed, 0, 0), evidence.PhaseRed, f.base, 1, evidence.VerdictFails)
	f.runner.PassTasks(f.ctx, passEpic, first)
	spent := f.task().Build
	spent.Decisions = decisionLimit
	f.setBuild(spent)
	f.steps.rows[passStep(StageImplement, 1, 0)] = store.StepRow{
		ID: passStep(StageImplement, 1, 0), End: "returned", Status: "needs_context",
		Result: `{"status":"needs_context","summary":"stuck","needs_files":["internal/other/a.go"]}`,
	}
	before := ladderClimbBefore(f, 1)
	f.runner.PassTasks(f.ctx, passEpic, first)
	task := f.task()
	want := "returned needs_context: stuck"
	if task.Build.Failure != want {
		t.Errorf("task failure %q, want %q", task.Build.Failure, want)
	}
	ladderClimbCheck(f, store.RungOf(before), store.MovedByUnit)
	if want := []string{"internal/other/a.go"}; !reflect.DeepEqual(task.NeedsFiles, want) {
		t.Errorf("NeedsFiles = %v, want %v", task.NeedsFiles, want)
	}
}

// TestNeedsFilesPassStoresNoPathsForABlockedReturn covers R3: a blocked return on a task with no orchestrator
// decisions left climbs the task with no paths, even when it names some.
func TestNeedsFilesPassStoresNoPathsForABlockedReturn(t *testing.T) {
	t.Parallel()
	f := newPassFixture(t)
	guardStartImplementer(f)
	spent := f.task().Build
	spent.Decisions = decisionLimit
	f.setBuild(spent)
	f.steps.rows[passStep(StageImplement, 1, 0)] = store.StepRow{
		ID: passStep(StageImplement, 1, 0), End: "returned", Status: "blocked",
		Result: `{"status":"blocked","summary":"the gate is broken","needs_files":["internal/other/a.go"]}`,
	}
	before := ladderClimbBefore(f, 1)
	f.pass()
	ladderClimbCheck(f, store.RungOf(before), store.MovedByUnit)
	if task := f.task(); len(task.NeedsFiles) != 0 {
		t.Errorf("NeedsFiles = %v, want none", task.NeedsFiles)
	}
}

func TestNeedsFilesPassGrantsFreeFilesAndRerunsTheImplementerInTheSameWorktree(t *testing.T) {
	t.Parallel()
	f := newPassFixture(t)
	guardStartImplementer(f)
	worktree := ImplementWorktree(f.stateDir, passEpic, store.EpicTask{Seq: 1})
	if err := os.WriteFile(filepath.Join(worktree, "thing.go"), []byte("package thing\n"), 0o600); err != nil {
		t.Fatalf("write earlier work: %v", err)
	}
	f.steps.rows[passStep(StageImplement, 1, 0)] = store.StepRow{
		ID: passStep(StageImplement, 1, 0), End: "returned", Status: "needs_context",
		Result: `{"status":"needs_context","summary":"stuck","needs_files":["internal/other/a.go"]}`,
	}
	f.pass()
	task := f.task()
	if task.Status != "open" || task.Build.Rerun != 1 || task.Build.Attempt != 1 {
		t.Fatalf("task status %q attempt %d rerun %d, want open at attempt 1, rerun 1",
			task.Status, task.Build.Attempt, task.Build.Rerun)
	}
	if want := []string{"thing.go", "internal/other/a.go"}; !reflect.DeepEqual(task.Files, want) {
		t.Fatalf("Files = %v, want %v", task.Files, want)
	}
	f.pass()
	start := f.lastStart()
	if start.ID != passStep(StageImplement, 1, 1) || start.Workspace != worktree {
		t.Fatalf("start %s in %s, want %s in %s", start.ID, start.Workspace, passStep(StageImplement, 1, 1), worktree)
	}
	if !slices.Contains(start.Owned, "internal/other/a.go") {
		t.Errorf("Owned = %v, want it to hold the granted file", start.Owned)
	}
	if !strings.Contains(start.Prompt, "these are now yours: internal/other/a.go") {
		t.Errorf("prompt does not name the granted file:\n%s", start.Prompt)
	}
	if data, err := os.ReadFile(filepath.Join(worktree, "thing.go")); err != nil || string(data) != "package thing\n" {
		t.Errorf("earlier work = %q, %v, want it kept in the worktree", data, err)
	}
}
