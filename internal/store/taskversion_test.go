package store_test

import (
	"context"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

func TestTaskVersionRerunStoresAnOrchestratorVersionWithItsDecision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRequestStore(ctx, t)
	deliverable := "Each end writes the version"
	version := store.TaskVersionInput{
		Source: store.SourceOrchestrator, Step: "step-2", Reason: "T1 rerun: the code says so",
		Deliverable: &deliverable, Files: []string{"b.go"},
	}
	err := s.RerunTask(ctx, buildEpic, 1, openBuildOf(), grantedBuild(), version, requestDecision(),
		writtenAt())
	if err != nil {
		t.Fatalf("RerunTask: %v", err)
	}

	task := buildTaskOf(ctx, t, s)
	if len(task.Versions) != 1 {
		t.Fatalf("Versions = %+v, want the one version the rerun stored", task.Versions)
	}
	got := task.Versions[0]
	if got.Version != 2 || got.Source != store.SourceOrchestrator || got.Decision != requestDecision().ID ||
		got.Step != "step-2" || got.Reason != version.Reason || got.State != "applied" {
		t.Errorf("version = %+v, want version 2 of the orchestrator's decision %s at step-2, applied",
			got, requestDecision().ID)
	}
	if want := []string{"deliverable", "files"}; !slices.Equal(got.Changed(), want) {
		t.Errorf("Changed() = %v, want %v", got.Changed(), want)
	}
	if !got.Created.Equal(writtenAt()) {
		t.Errorf("Created = %v, want %v", got.Created, writtenAt())
	}
	if want := []string{"a.go", "b.go"}; got.Files == nil || !reflect.DeepEqual(*got.Files, want) {
		t.Errorf("Files = %v, want the task's whole file list %v", got.Files, want)
	}
	if got.Deliverable == nil || *got.Deliverable != deliverable {
		t.Errorf("Deliverable = %v, want %q", got.Deliverable, deliverable)
	}
	if task.Version != 2 {
		t.Errorf("task Version = %d, want 2", task.Version)
	}
}

func TestTaskVersionGrantStoresAGrantVersionWithItsTimeAndNoDecision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openBuildStore(ctx, t)
	if err := s.SetTaskBuild(ctx, buildEpic, 1, store.TaskBuild{}, openBuildOf()); err != nil {
		t.Fatalf("SetTaskBuild: %v", err)
	}
	decisions := len(epicOf(ctx, t, s).Decisions)

	grant := store.TaskVersionInput{
		Source: store.SourceGrant, Step: "step-1", Reason: "the implementer needs c.go", Files: []string{"c.go"},
	}
	if err := s.GrantTaskFiles(ctx, buildEpic, 1, openBuildOf(), grantedBuild(), grant, writtenAt()); err != nil {
		t.Fatalf("GrantTaskFiles: %v", err)
	}

	task := buildTaskOf(ctx, t, s)
	if len(task.Versions) != 1 {
		t.Fatalf("Versions = %+v, want the one version the grant stored", task.Versions)
	}
	got := task.Versions[0]
	if got.Version != 2 || got.Source != store.SourceGrant || got.Decision != "" || got.Step != "step-1" ||
		got.Reason != grant.Reason {
		t.Errorf("version = %+v, want version 2 of a grant with no decision at step-1", got)
	}
	if want := []string{"files"}; !slices.Equal(got.Changed(), want) {
		t.Errorf("Changed() = %v, want %v", got.Changed(), want)
	}
	if !got.Created.Equal(writtenAt()) {
		t.Errorf("Created = %v, want %v", got.Created, writtenAt())
	}
	if n := len(epicOf(ctx, t, s).Decisions); n != decisions {
		t.Errorf("epic has %d decisions after the grant, want %d: a grant mints no decision", n, decisions)
	}
}

func TestTaskVersionReadOverlaysTheCurrentVersion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openBuildStore(ctx, t)
	deliverable := "Each end writes one version"
	testChanges := []string{"ends_test.go"}
	gate := "check changed"
	item := evidence.Item{
		Capture: []string{"go", "test", "./internal/store/"}, Limit: 300, Artifacts: []string{"out/versions.json"},
		Conclusion: "the versions overlay", Judge: "read the output",
	}
	version := store.TaskVersionInput{
		Source: store.SourceOrchestrator, Step: "step-2", Reason: "the spec moved", Deliverable: &deliverable,
		TestChanges: &testChanges, Gate: &gate, Evidence: &item,
	}
	err := s.RerunTask(ctx, buildEpic, 1, store.TaskBuild{}, openBuildOf(), version, requestDecision(),
		writtenAt())
	if err != nil {
		t.Fatalf("RerunTask: %v", err)
	}
	err = s.SettleTaskVersion(ctx, buildEpic, 1, openBuildOf(), openBuildOf(), store.TaskVersionSettle{
		Version: 2, State: store.VersionApplied,
	}, writtenAt())
	if err != nil {
		t.Fatalf("SettleTaskVersion: %v", err)
	}
	// A refused version after the applied one is listed but never overlaid, so the read keeps the applied values.
	if _, err = raw.Exec(`INSERT INTO epic_task_versions
		(epic, task, version, source, decision, step, reason, deliverable, state, refusal, created)
		VALUES (?, 1, 3, 'orchestrator', '', 'step-3', 'refused reason', 'refused deliverable', 'refused',
		'refusal text', ?)`, buildEpic, writtenAt().Unix()); err != nil {
		t.Fatalf("insert refused version: %v", err)
	}

	task := buildTaskOf(ctx, t, s)
	if task.Version != 2 {
		t.Errorf("Version = %d, want 2: the refused version 3 is not current", task.Version)
	}
	if task.Deliverable != deliverable {
		t.Errorf("Deliverable = %q, want the applied version's %q", task.Deliverable, deliverable)
	}
	if !reflect.DeepEqual(task.TestChanges, testChanges) {
		t.Errorf("TestChanges = %v, want the applied version's %v", task.TestChanges, testChanges)
	}
	if task.Gate != gate {
		t.Errorf("Gate = %q, want the applied version's %q", task.Gate, gate)
	}
	if !reflect.DeepEqual(task.Evidence, item) {
		t.Errorf("Evidence = %+v, want the applied version's %+v", task.Evidence, item)
	}
	if len(task.Versions) != 2 || task.Versions[1].State != "refused" || task.Versions[1].Refusal != "refusal text" {
		t.Errorf("Versions = %+v, want the applied version then the refused one listed", task.Versions)
	}
}

func TestTaskVersionsOnlyAppend(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openBuildStore(ctx, t)
	deliverable := "The first spec"
	first := store.TaskVersionInput{
		Source: store.SourceOrchestrator, Step: "step-2", Reason: "the first spec", Deliverable: &deliverable,
	}
	err := s.RerunTask(ctx, buildEpic, 1, store.TaskBuild{}, openBuildOf(), first, requestDecision(),
		writtenAt())
	if err != nil {
		t.Fatalf("RerunTask: %v", err)
	}
	before := rawRows(t, raw, `SELECT * FROM epic_task_versions WHERE epic = ? ORDER BY version`, buildEpic)

	next := openBuildOf()
	next.Rerun++
	grant := store.TaskVersionInput{
		Source: store.SourceGrant, Step: "step-3", Reason: "a later grant", Files: []string{"c.go"},
	}
	later := time.Unix(1700000900, 0)
	if err = s.GrantTaskFiles(ctx, buildEpic, 1, openBuildOf(), next, grant, later); err != nil {
		t.Fatalf("GrantTaskFiles: %v", err)
	}

	after := rawRows(t, raw, `SELECT * FROM epic_task_versions WHERE epic = ? ORDER BY version`, buildEpic)
	if len(after) != 2 || !reflect.DeepEqual(after[0], before[0]) {
		t.Errorf("version rows after the grant = %v, want version 2 unchanged from %v and one row appended",
			after, before)
	}
	task := buildTaskOf(ctx, t, s)
	if len(task.Versions) != 2 || task.Versions[1].Version != 3 || task.Version != 3 {
		t.Errorf("Versions = %+v, want the grant stored as version 3 after version 2", task.Versions)
	}
}

func TestTaskVersionFirstIsThePlannersRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openBuildStore(ctx, t)

	task := buildTaskOf(ctx, t, s)
	if task.Version != 1 || len(task.Versions) != 0 {
		t.Errorf("Version = %d with %d stored versions, want version 1 and none", task.Version, len(task.Versions))
	}
	if want := "Each end is one write"; task.Deliverable != want {
		t.Errorf("Deliverable = %q, want the planner's %q", task.Deliverable, want)
	}
}
