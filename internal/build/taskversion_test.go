package build

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// schemaNode is the part of a JSON schema the version tests read: the keys an object requires and its properties.
type schemaNode struct {
	Required   []string                   `json:"required"`
	Properties map[string]json.RawMessage `json:"properties"`
}

func TestTaskVersionOrchestratorSchemaReturnsAVersionInPlaceOfNoteAndFiles(t *testing.T) {
	t.Parallel()
	var top schemaNode
	if err := json.Unmarshal([]byte(OrchestratorSchema), &top); err != nil {
		t.Fatalf("OrchestratorSchema is not JSON: %v", err)
	}
	if !slices.Contains(top.Required, "version") {
		t.Errorf("required = %v, want version", top.Required)
	}
	for _, gone := range []string{"note", "files"} {
		if slices.Contains(top.Required, gone) {
			t.Errorf("required names %s, want version in its place", gone)
		}
		if _, ok := top.Properties[gone]; ok {
			t.Errorf("properties names %s, want it removed", gone)
		}
	}
	var version schemaNode
	if err := json.Unmarshal(top.Properties["version"], &version); err != nil {
		t.Fatalf("properties.version is not an object: %v", err)
	}
	for _, want := range []string{"changed", "deliverable", "files", "test_changes", "gate", "evidence"} {
		if !slices.Contains(version.Required, want) {
			t.Errorf("version requires %v, want %s", version.Required, want)
		}
	}
}

// versionCase is one orchestrator return of the version checks: its fields, and the refusals OrchestratorCheck gives.
type versionCase struct {
	name   string
	fields map[string]any
	want   []string
}

// versionRefusalCases returns the returns that break a rule of a version or of its decision's shape, with the refusals
// each gives.
func versionRefusalCases() []versionCase {
	return []versionCase{
		{
			"a field changed names twice is refused",
			map[string]any{"decision": "rerun", "version": versionWith(map[string]any{
				"changed": []string{"gate", "gate"}, "gate": "check-fast",
			})},
			[]string{"version.changed[1]: gate is named twice"},
		},
		{
			"a field the changed list does not name is refused when set",
			map[string]any{"decision": "rerun", "version": versionWith(map[string]any{
				"deliverable": "The thing holds.",
			})},
			[]string{"version.deliverable: not empty when changed does not name it"},
		},
		{
			"a named deliverable that is blank is refused",
			map[string]any{
				"decision": "rerun",
				"version":  versionWith(map[string]any{"changed": []string{"deliverable"}}),
			},
			[]string{"version.deliverable: empty"},
		},
		{
			"a named gate that is blank is refused",
			map[string]any{"decision": "rerun", "version": versionWith(map[string]any{"changed": []string{"gate"}})},
			[]string{"version.gate: empty"},
		},
		{
			"a grant naming files with none is refused",
			map[string]any{"decision": "grant", "version": versionWith(map[string]any{"changed": []string{"files"}})},
			[]string{"version.files: empty on a grant"},
		},
		{
			"a grant naming no files is refused",
			map[string]any{"decision": "grant"},
			[]string{"version.changed: files not named on a grant"},
		},
		{
			"a rerun naming files is refused",
			map[string]any{"decision": "rerun", "version": filesVersion("b.go")},
			[]string{"version.changed: files named on a rerun"},
		},
		{
			"a named evidence item gets the refusals CheckTask gives",
			map[string]any{"decision": "rerun", "version": versionWith(map[string]any{
				"changed": []string{"evidence"},
				"evidence": evidence.Item{
					Kind:       evidence.KindCapture,
					Conclusion: "the thing holds",
					Judge:      "the judge",
				},
			})},
			[]string{
				"version.evidence.capture: none, name a capture or supplied files",
				"version.evidence.kind: capture without a capture",
			},
		},
		{
			"a named closure evidence item is refused",
			map[string]any{"decision": "rerun", "version": versionWith(map[string]any{
				"changed":  []string{"evidence"},
				"evidence": evidence.Item{Kind: evidence.KindClosure, Conclusion: "the round's check settles it"},
			})},
			[]string{"version.evidence.kind: closure is not a kind a version sets"},
		},
		{
			"a version file the task's state refuses is refused as files are",
			map[string]any{"decision": "grant", "version": filesVersion(".git/config")},
			[]string{"version.files[0]: .git/config is refused: inside a .git directory"},
		},
		{
			"a test change that is none of the task's files is refused",
			map[string]any{"decision": "rerun", "version": versionWith(map[string]any{
				"changed": []string{"test_changes"}, "test_changes": []string{"x_test.go"},
			})},
			[]string{"version.test_changes[0]: not one of the task's files"},
		},
		{
			"a fail with a version is refused",
			map[string]any{"decision": "fail", "failure": "f", "version": versionWith(map[string]any{
				"changed": []string{"gate"}, "gate": "check-fast",
			})},
			[]string{"version: not empty on fail"},
		},
		{
			"an ask with a version is refused",
			map[string]any{"decision": "ask", "question": askQuestion(), "version": versionWith(map[string]any{
				"changed": []string{"gate"}, "gate": "check-fast",
			})},
			[]string{"version: not empty on ask"},
		},
	}
}

// versionAcceptedCases returns the returns whose versions pass the shape and state checks.
func versionAcceptedCases() []versionCase {
	return []versionCase{
		{"a rerun with nothing changed passes", map[string]any{"decision": "rerun"}, nil},
		{
			"a rerun changing deliverable, test_changes, gate and evidence passes",
			map[string]any{"decision": "rerun", "version": versionWith(map[string]any{
				"changed":      []string{"deliverable", "test_changes", "gate", "evidence"},
				"deliverable":  "The thing holds.",
				"test_changes": []string{"a.go"},
				"gate":         "check-fast",
				"evidence": evidence.Item{
					Capture: []string{"scripts/check"}, Limit: 60, Conclusion: "the thing holds", Judge: "the judge",
					Kind: evidence.KindCapture,
				},
			})},
			nil,
		},
		{
			"a grant whose test_changes names a granted file passes",
			map[string]any{"decision": "grant", "version": versionWith(map[string]any{
				"changed": []string{"files", "test_changes"}, "files": []string{"internal/b_test.go"},
				"test_changes": []string{"internal/b_test.go"},
			})},
			nil,
		},
	}
}

// TestTaskVersionOrchestratorCheckRefusesAVersionOutOfShape: each rule of a rerun's or grant's version, and of a fail's
// or ask's, refuses the return that breaks it with the line that names it, and a version within the rules passes.
func TestTaskVersionOrchestratorCheckRefusesAVersionOutOfShape(t *testing.T) {
	t.Parallel()
	task := store.EpicTask{Seq: 3, Files: []string{"a.go"}, Status: taskOpen}
	check := OrchestratorCheck(task, []Peer{{Task: task}})
	for _, tc := range append(versionRefusalCases(), versionAcceptedCases()...) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := check(json.RawMessage(orchReturn(t, tc.fields))); !slices.Equal(got, tc.want) {
				t.Errorf("check = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestTaskVersionOrchestratorRerunStoresAVersionWithItsDecision: a carried rerun stores its version as version 2 under
// its decision, from the orchestrator's step, with the return's reason and the runner's clock, and the task reads back
// the new deliverable and gate. The build keeps orreryd's failure, which names neither. The decision is carried out by
// execute rather than a pass: a pass would start the implementer, and the epic defines no second command gate for the
// new gate to name, so the start would refuse it.
func TestTaskVersionOrchestratorRerunStoresAVersionWithItsDecision(t *testing.T) {
	t.Parallel()
	f := newRequestFixture(t)
	const deliverable = "Thing holds: thing.go:3 names the field."
	const gate = "check-fast"
	const reason = "thing.go:3 is the only reader"
	ret := orchReturn(t, map[string]any{
		"decision": "rerun", "reason": reason, "version": versionWith(map[string]any{
			"changed": []string{"deliverable", "gate"}, "deliverable": deliverable, "gate": gate,
		}),
	})
	view := View{
		Epic: passEpic, Task: f.task(),
		Returned: &store.StepRow{
			ID: passStep(StageImplement, 1, 0), End: endReturned, Status: "blocked",
			Result: `{"status":"blocked","summary":"the spec does not say which field"}`,
		},
		Step: &store.StepRow{ID: orchID(0, 0), End: endDone, Status: statusDone, Result: ret},
	}
	if !f.runner.execute(f.ctx, f.epic(), view, Decide(view)) {
		t.Fatal("execute changed nothing, want the rerun carried out")
	}
	task := f.task()
	if len(task.Versions) != 1 {
		t.Fatalf("Versions = %+v, want the one version the rerun stored", task.Versions)
	}
	got := task.Versions[0]
	if got.Version != 2 || got.Source != store.SourceOrchestrator || got.Decision != "T1-request-1" ||
		got.Step != orchID(0, 0) || got.Reason != reason || got.State != "applied" {
		t.Errorf("version = %+v, want version 2 of the orchestrator's decision T1-request-1 at its step", got)
	}
	if want := []string{"deliverable", "gate"}; !slices.Equal(got.Changed(), want) {
		t.Errorf("Changed() = %v, want %v", got.Changed(), want)
	}
	if !got.Created.Equal(passClock()) {
		t.Errorf("Created = %v, want the runner's clock %v", got.Created, passClock())
	}
	if task.Deliverable != deliverable || task.Gate != gate || task.Version != 2 {
		t.Errorf("task deliverable %q, gate %q at version %d, want the version's", task.Deliverable, task.Gate,
			task.Version)
	}
	const failure = "returned blocked: the spec does not say which field"
	if task.Build.Failure != failure {
		t.Errorf("Build.Failure = %q, want orreryd's %q", task.Build.Failure, failure)
	}
	for _, text := range []string{deliverable, reason} {
		if strings.Contains(task.Build.Failure, text) {
			t.Errorf("Build.Failure %q names %q, want orreryd's text alone", task.Build.Failure, text)
		}
	}
}

// TestTaskVersionOrchestratorGrantStoresAVersionWithItsFiles: a carried grant stores the files it adds as a version
// under its decision, whose files are the task's whole list.
func TestTaskVersionOrchestratorGrantStoresAVersionWithItsFiles(t *testing.T) {
	t.Parallel()
	f := newRequestFixture(t)
	f.endOrchestrator(orchID(0, 0), orchReturn(t, map[string]any{
		"decision": "grant", "version": filesVersion("internal/b.go"),
	}))
	f.pass()
	task := f.task()
	if len(task.Versions) != 1 {
		t.Fatalf("Versions = %+v, want the one version the grant stored", task.Versions)
	}
	got := task.Versions[0]
	if got.Version != 2 || got.Source != store.SourceOrchestrator || got.Decision != "T1-request-1" {
		t.Errorf("version = %+v, want version 2 of the orchestrator's decision T1-request-1", got)
	}
	if want := []string{"files"}; !slices.Equal(got.Changed(), want) {
		t.Errorf("Changed() = %v, want %v", got.Changed(), want)
	}
	if want := []string{"thing.go", "internal/b.go"}; got.Files == nil || !slices.Equal(*got.Files, want) {
		t.Errorf("Files = %v, want the task's whole list %v", got.Files, want)
	}
}

// TestTaskVersionRuleGrantStoresAVersionWithNoDecision: a needs_context for a free file that orreryd grants by rule
// stores a grant version with no decision, from the implementer's step, and the epic's decisions are unchanged.
func TestTaskVersionRuleGrantStoresAVersionWithNoDecision(t *testing.T) {
	t.Parallel()
	f := newPassFixture(t)
	guardStartImplementer(f)
	step := passStep(StageImplement, 1, 0)
	f.steps.rows[step] = store.StepRow{
		ID: step, End: endReturned, Status: "needs_context",
		Result: `{"status":"needs_context","summary":"stuck","needs_files":["internal/other/a.go"]}`,
	}
	decisions := f.epic().Decisions
	f.pass()
	task := f.task()
	if len(task.Versions) != 1 {
		t.Fatalf("Versions = %+v, want the one version the grant stored", task.Versions)
	}
	got := task.Versions[0]
	if got.Version != 2 || got.Source != store.SourceGrant || got.Decision != "" || got.Step != step {
		t.Errorf("version = %+v, want version 2 of a grant with no decision at %s", got, step)
	}
	if after := f.epic().Decisions; !reflect.DeepEqual(after, decisions) {
		t.Errorf("decisions = %+v, want them unchanged from %+v", after, decisions)
	}
}

// requestView returns the view of task 1 whose implementer step ended returned blocked, and whose orchestrator step
// ended done with ret, the request the decision tests carry out.
func requestView(f *passFixture, ret string) View {
	return View{
		Epic: passEpic, Task: f.task(),
		Returned: &store.StepRow{
			ID: passStep(StageImplement, 1, 0), End: endReturned, Status: "blocked",
			Result: `{"status":"blocked","summary":"the spec does not say which field"}`,
		},
		Step: &store.StepRow{ID: orchID(0, 0), End: endDone, Status: statusDone, Result: ret},
	}
}

// TestTaskVersionOrchestratorRerunStoresTestChangesAndEvidence: a carried rerun that changes test_changes and evidence
// stores both as its version, which names the two fields and holds the value each return gives.
func TestTaskVersionOrchestratorRerunStoresTestChangesAndEvidence(t *testing.T) {
	t.Parallel()
	f := newRequestFixture(t)
	item := evidence.Item{
		Capture: []string{"scripts/check"}, Limit: 60, Conclusion: "thing.go:3 holds the field", Judge: "the judge",
		Kind: evidence.KindCapture,
	}
	ret := orchReturn(t, map[string]any{
		"decision": "rerun", "reason": "the spec names the wrong test", "version": versionWith(map[string]any{
			"changed": []string{"test_changes", "evidence"}, "test_changes": []string{"thing.go"}, "evidence": item,
		}),
	})
	view := requestView(f, ret)
	if !f.runner.execute(f.ctx, f.epic(), view, Decide(view)) {
		t.Fatal("execute changed nothing, want the rerun carried out")
	}
	task := f.task()
	if len(task.Versions) != 1 {
		t.Fatalf("Versions = %+v, want the one version the rerun stored", task.Versions)
	}
	got := task.Versions[0]
	if want := []string{"test_changes", "evidence"}; !slices.Equal(got.Changed(), want) {
		t.Errorf("Changed() = %v, want %v", got.Changed(), want)
	}
	if got.TestChanges == nil || !slices.Equal(*got.TestChanges, []string{"thing.go"}) {
		t.Errorf("TestChanges = %v, want the version's [thing.go]", got.TestChanges)
	}
	evid := got.Evidence
	if evid == nil || !slices.Equal(evid.Capture, item.Capture) || evid.Limit != item.Limit ||
		evid.Conclusion != item.Conclusion || evid.Judge != item.Judge || evid.Kind != item.Kind {
		t.Errorf("Evidence = %+v, want the version's item %+v", evid, item)
	}
}

// TestTaskVersionRerunWithNoChangeStoresNoVersion: a rerun that changes nothing carries its decision out and records
// it, and stores no version, so the task stays at version 1.
func TestTaskVersionRerunWithNoChangeStoresNoVersion(t *testing.T) {
	t.Parallel()
	f := newRequestFixture(t)
	f.endOrchestrator(orchID(0, 0), orchReturn(t, map[string]any{"decision": "rerun"}))
	f.pass()
	task := f.task()
	if task.Build.Stage != StageImplement || task.Build.Rerun != 1 || task.Build.Decisions != 1 {
		t.Fatalf("build = %+v, want the rerun carried out at rerun 1 with one decision", task.Build)
	}
	if decisions := f.epic().Decisions; len(decisions) == 0 || decisions[len(decisions)-1].ID != "T1-request-1" {
		t.Errorf("decisions = %+v, want T1-request-1 recorded", decisions)
	}
	if task.Version != 1 || len(task.Versions) != 0 {
		t.Errorf(
			"Version = %d with %d versions stored, want version 1 and none stored",
			task.Version,
			len(task.Versions),
		)
	}
}

// TestTaskVersionRefusedDecisionKeepsOrrerydsText: a grant of a file another open task owns, without the task named in
// after_tasks, is refused, so it stores no version and the task fails with orreryd's refusal.
func TestTaskVersionRefusedDecisionKeepsOrrerydsText(t *testing.T) {
	t.Parallel()
	f := newWaitFixture(t)
	f.endOrchestrator(orchID(0, 0), orchReturn(t, map[string]any{
		"decision": "grant", "version": filesVersion(waitFile),
	}))
	f.runner.PassTasks(f.ctx, passEpic, []int64{1})
	task := f.task()
	if len(task.Versions) != 0 || task.Version != 1 {
		t.Errorf(
			"Version = %d with %d versions stored, want version 1 and none stored",
			task.Version,
			len(task.Versions),
		)
	}
	if task.Status != "failed" || !strings.Contains(task.Build.Failure, "orreryd refused the orchestrator's grant") {
		t.Errorf("task %q failure %q, want failed with orreryd's refusal", task.Status, task.Build.Failure)
	}
}
