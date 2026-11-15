package store

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
)

// TestEpicFlowReadsEpicRunsAndPendingOfOneState checks that EpicFlow returns the epic Epic returns, the node runs
// NodeRuns returns, and the pending triggers of its epic only, as each trigger is claimed or consumed.
func TestEpicFlowReadsEpicRunsAndPendingOfOneState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = "orrery-1a2b3c4/epic-flow"
	const other = "orrery-1a2b3c4/epic-flow-other"
	createEpic(t, s, fullEpic(id))
	createEpic(t, s, fullEpic(other))

	runs := []NodeRun{{Path: "plan", ID: "id-plan", Node: "plan", Kind: "plan"}}
	first := NodeTrigger{ID: "workflow:id-one", Cause: "workflow node one started"}
	second := NodeTrigger{ID: "workflow:id-two", Cause: "workflow node two started"}
	third := NodeTrigger{ID: "workflow:id-three", Cause: "workflow node three started"}
	if err := s.StartNodes(ctx, id, runs, &first, epicCreated()); err != nil {
		t.Fatalf("StartNodes of first trigger: %v", err)
	}
	if err := s.StartNodes(ctx, id, nil, &second, epicCreated()); err != nil {
		t.Fatalf("StartNodes of second trigger: %v", err)
	}
	if err := s.StartNodes(ctx, other, nil, &third, epicCreated()); err != nil {
		t.Fatalf("StartNodes of other epic's trigger: %v", err)
	}

	checkFlow := func(want int) {
		t.Helper()
		flow, err := s.EpicFlow(ctx, id)
		if err != nil {
			t.Fatalf("EpicFlow: %v", err)
		}
		epic, err := s.Epic(ctx, id)
		if err != nil {
			t.Fatalf("Epic: %v", err)
		}
		nodes, err := s.NodeRuns(ctx, id)
		if err != nil {
			t.Fatalf("NodeRuns: %v", err)
		}
		if len(nodes) != 1 {
			t.Fatalf("NodeRuns = %+v, want the plan run", nodes)
		}
		if !reflect.DeepEqual(flow.Epic, epic) {
			t.Errorf("EpicFlow epic = %+v, want the epic Epic returns %+v", flow.Epic, epic)
		}
		if !reflect.DeepEqual(flow.Runs, nodes) {
			t.Errorf("EpicFlow runs = %+v, want the runs NodeRuns returns %+v", flow.Runs, nodes)
		}
		if flow.Pending != want {
			t.Errorf("EpicFlow pending = %d, want %d", flow.Pending, want)
		}
	}

	// CreateEpic leaves its own trigger pending, so the epic counts it with the two started above, and never the other
	// epic's trigger.
	checkFlow(3)
	if _, err := s.db.ExecContext(
		ctx, `UPDATE epic_triggers SET state = 'claimed', turn = 3 WHERE epic = ? AND id = ?`, id, first.ID,
	); err != nil {
		t.Fatalf("claim first trigger: %v", err)
	}
	checkFlow(2)
	if _, err := s.db.ExecContext(
		ctx, `UPDATE epic_triggers SET state = 'consumed', turn = 3 WHERE epic = ? AND id = ?`, id, second.ID,
	); err != nil {
		t.Fatalf("consume second trigger: %v", err)
	}
	checkFlow(1)
}

// TestEpicFlowOfUnknownEpicIsErrNoEpic checks that EpicFlow of an id no epic has returns an error matching ErrNoEpic.
func TestEpicFlowOfUnknownEpicIsErrNoEpic(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	if _, err := s.EpicFlow(context.Background(), "orrery-1a2b3c4/missing"); !errors.Is(err, ErrNoEpic) {
		t.Fatalf("EpicFlow of unknown id err = %v, want ErrNoEpic", err)
	}
}

// evidenceRunIDs returns the ids of runs in order.
func evidenceRunIDs(runs []EvidenceRun) []string {
	ids := make([]string, 0, len(runs))
	for _, run := range runs {
		ids = append(ids, run.ID)
	}
	return ids
}

// TestEpicFlowKeepsOnlyRunsCurrentWithTheirRequirementItem checks that EpicFlow lists a Requirement run while its kept
// spec equals the item its Requirement stores, drops it once an UPDATE changes that item by SQL, and keeps the run of
// another Requirement. EvidenceRuns still lists the dropped run as history.
func TestEpicFlowKeepsOnlyRunsCurrentWithTheirRequirementItem(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = "orrery-1a2b3c4/epic-flow-runs"
	row := evidenceEpic(id)
	createEpic(t, s, row)
	items := make(map[string]evidence.Item)
	for _, requirement := range row.Requirements {
		items[requirement.ID] = requirement.Item
	}
	for _, requirement := range []struct{ run, item string }{{"run-r1", "R1"}, {"run-r2", "R2"}} {
		if err := s.AddEvidenceRun(ctx, EvidenceRun{
			ID: requirement.run, Epic: id, Item: requirement.item, Revision: "abc1234", Phase: "red",
			Source: "captured", Spec: items[requirement.item], Created: runCreatedAt(),
		}); err != nil {
			t.Fatalf("AddEvidenceRun %s: %v", requirement.run, err)
		}
	}

	checkFlow := func(want ...string) {
		t.Helper()
		flow, err := s.EpicFlow(ctx, id)
		if err != nil {
			t.Fatalf("EpicFlow: %v", err)
		}
		if got := evidenceRunIDs(flow.Evidence); !reflect.DeepEqual(got, want) {
			t.Fatalf("EpicFlow evidence runs = %v, want %v", got, want)
		}
	}
	checkFlow("run-r1", "run-r2")

	changed := items["R1"]
	changed.Conclusion = "The store changed"
	raw, err := encodeItem(changed)
	if err != nil {
		t.Fatalf("encode changed item: %v", err)
	}
	if _, err := s.db.ExecContext(
		ctx, `UPDATE epic_requirements SET item = ? WHERE epic = ? AND id = 'R1'`, raw, id,
	); err != nil {
		t.Fatalf("change item of R1: %v", err)
	}
	checkFlow("run-r2")

	history, err := s.EvidenceRuns(ctx, id)
	if err != nil {
		t.Fatalf("EvidenceRuns: %v", err)
	}
	if got, want := evidenceRunIDs(history), []string{"run-r1", "run-r2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("EvidenceRuns = %v, want %v: history keeps the run whose item changed", got, want)
	}
}

// TestTaskRunReadsAsRedAndGreenOnlyWhileCurrentWithTheTaskEvidence checks that a task's red and green runs read back
// through Epic while their kept spec equals the task's evidence, and read nil once an UPDATE changes that evidence by
// SQL. Build keeps the run ids the build named.
func TestTaskRunReadsAsRedAndGreenOnlyWhileCurrentWithTheTaskEvidence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := plannedBuildEpic(t)
	spec := plannedTurn().Tasks[0].Evidence
	for _, run := range []struct{ id, phase string }{{"red-run", "red"}, {"green-run", "green"}} {
		if err := s.AddEvidenceRun(ctx, EvidenceRun{
			ID: run.id, Epic: buildEpic, Item: "T1", Revision: "abc1234", Phase: run.phase, Source: "captured",
			Spec: spec, Created: runCreatedAt(),
		}); err != nil {
			t.Fatalf("AddEvidenceRun %s: %v", run.id, err)
		}
	}
	from := buildTaskOf(t, s, 1).Build
	to := TaskBuild{Stage: "implement", Base: buildBase, RedRun: "red-run", GreenRun: "green-run"}
	if err := s.SetTaskBuild(ctx, buildEpic, 1, from, to); err != nil {
		t.Fatalf("SetTaskBuild: %v", err)
	}

	task := buildTaskOf(t, s, 1)
	if task.Red == nil || task.Red.ID != "red-run" || task.Green == nil || task.Green.ID != "green-run" {
		t.Fatalf("red = %v, green = %v; want the red-run and green-run runs", task.Red, task.Green)
	}

	changed := spec
	changed.Conclusion = "the store changed"
	raw, err := encodeItem(changed)
	if err != nil {
		t.Fatalf("encode changed evidence: %v", err)
	}
	if _, err := s.db.ExecContext(
		ctx, `UPDATE epic_tasks SET evidence = ? WHERE epic = ? AND seq = 1`, raw, buildEpic,
	); err != nil {
		t.Fatalf("change evidence of task 1: %v", err)
	}
	task = buildTaskOf(t, s, 1)
	if task.Red != nil || task.Green != nil {
		t.Errorf("red = %v, green = %v after the evidence changed; want nil and nil", task.Red, task.Green)
	}
	if task.Build.RedRun != "red-run" || task.Build.GreenRun != "green-run" {
		t.Errorf("build runs = %q and %q; want red-run and green-run kept", task.Build.RedRun, task.Build.GreenRun)
	}
}

// TestEvidenceRunStoredAtVersion33IsCurrentAfterOpenMigrates checks that a run stored at version 33 with its
// Requirement's item still counts in EpicFlow once Open migrates the store: the migration gives the item and the run's
// spec the same kind, so the run stays current.
func TestEvidenceRunStoredAtVersion33IsCurrentAfterOpenMigrates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	const item = `{"capture":["scripts/check"],"limit":60,"artifacts":null,"supplied":null,` +
		`"conclusion":"the capture holds","judge":"a judge"}`

	fixture := openStoreAtVersion(t, path, 33)
	seedEpic(t, fixture)
	seeds := []struct {
		stmt string
		args []any
	}{
		{`INSERT INTO epic_requirements (epic, position, id, text, evidence, kind, item)
			VALUES (?, 1, 'R1', 'Captured', 'store test', 'check', ?)`, []any{seededEpic, item}},
		{`INSERT INTO evidence_runs (id, epic, item, revision, phase, source, state, spec, created, ended)
			VALUES ('run-1', ?, 'R1', 'abc1234', 'red', 'captured', 'ended', ?, 1700000000, 1700000100)`,
			[]any{seededEpic, item}},
	}
	for _, seed := range seeds {
		if _, err := fixture.ExecContext(ctx, seed.stmt, seed.args...); err != nil {
			fixture.Close()
			t.Fatalf("seed fixture: %v", err)
		}
	}
	if err := fixture.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}

	s, err := Open(ctx, path, noSync())
	if err != nil {
		t.Fatalf("Open at version 33: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	flow, err := s.EpicFlow(ctx, seededEpic)
	if err != nil {
		t.Fatalf("EpicFlow: %v", err)
	}
	if got, want := evidenceRunIDs(flow.Evidence), []string{"run-1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("EpicFlow evidence runs = %v, want %v: an old run still counts", got, want)
	}
}
