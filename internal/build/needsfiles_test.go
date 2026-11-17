package build

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"

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

func TestNeedsFilesDecideCarriesThePathsOfANeedsContextReturn(t *testing.T) {
	t.Parallel()
	runDecideCases(t, Decide, []decideCase{
		{
			name: "needs_context carries the paths it names",
			view: needsFilesView(
				"needs_context",
				`{"status":"needs_context","summary":"the spec is short","needs_files":["a.go","b/c.go"]}`,
			),
			want: Action{
				Kind: ActFail, Reason: "returned needs_context: the spec is short",
				NeedsFiles: []string{"a.go", "b/c.go"},
			},
		},
		{
			name: "needs_context without needs_files fails as it did before the field",
			view: needsFilesView("needs_context", `{"status":"needs_context","summary":"the spec is short"}`),
			want: Action{Kind: ActFail, Reason: "returned needs_context: the spec is short"},
		},
		{
			name: "blocked carries no paths even when its return names some",
			view: needsFilesView(
				"blocked",
				`{"status":"blocked","summary":"the gate is broken","needs_files":["a.go"]}`,
			),
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
	got := Brief(briefEpic(), briefTask(), store.EvidenceRun{}, nil, nil, "")
	if !strings.HasSuffix(got, sentence) {
		t.Fatalf("brief does not end with the needs_files request; brief:\n%s", got)
	}
}

// TestNeedsFilesPassStoresThePathsWithTheFailedTask covers R3: an implementer that returns needs_context with
// needs_files fails the task with the paths stored under it.
func TestNeedsFilesPassStoresThePathsWithTheFailedTask(t *testing.T) {
	t.Parallel()
	f := newPassFixture(t)
	guardStartImplementer(f)
	f.steps.rows[passStep(StageImplement, 1, 0)] = store.StepRow{
		ID: passStep(StageImplement, 1, 0), End: "returned", Status: "needs_context",
		Result: `{"status":"needs_context","summary":"stuck","needs_files":["internal/other/a.go"]}`,
	}
	f.pass()
	task := f.task()
	if task.Status != "failed" || task.Build.Failure != "returned needs_context: stuck" {
		t.Errorf("task status %q failure %q, want failed with the return's reason", task.Status, task.Build.Failure)
	}
	if want := []string{"internal/other/a.go"}; !reflect.DeepEqual(task.NeedsFiles, want) {
		t.Errorf("NeedsFiles = %v, want %v", task.NeedsFiles, want)
	}
}

// TestNeedsFilesPassStoresNoPathsForABlockedReturn covers R3: a blocked return fails the task with no paths, even
// when it names some.
func TestNeedsFilesPassStoresNoPathsForABlockedReturn(t *testing.T) {
	t.Parallel()
	f := newPassFixture(t)
	guardStartImplementer(f)
	f.steps.rows[passStep(StageImplement, 1, 0)] = store.StepRow{
		ID: passStep(StageImplement, 1, 0), End: "returned", Status: "blocked",
		Result: `{"status":"blocked","summary":"the gate is broken","needs_files":["internal/other/a.go"]}`,
	}
	f.pass()
	task := f.task()
	if task.Status != "failed" {
		t.Errorf("task status %q, want failed", task.Status)
	}
	if len(task.NeedsFiles) != 0 {
		t.Errorf("NeedsFiles = %v, want none", task.NeedsFiles)
	}
}
