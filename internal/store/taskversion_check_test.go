package store_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// checkingRedRun is the red run that judges the checking version of the tests, under a synthetic id.
const checkingRedRun = "5a1c0e9f3b7d4c2a8e6f1b0d9c3a7e52"

// checkingEvidence is the evidence item a rerun of the checking tests changes task 1 to: a conclusion the planner's
// item does not hold.
func checkingEvidence() evidence.Item {
	return evidence.Item{
		Capture: []string{"go", "test", "./internal/build/"}, Limit: 300, Artifacts: []string{"out/build.json"},
		Conclusion: "the build ends hold", Judge: "read the output",
	}
}

// openCheckingStore returns a build store whose task 1 has been reran to checkingEvidence: version 2 is stored checking
// and the build is at openBuildOf. It returns the store and a raw handle to its file.
func openCheckingStore(ctx context.Context, t *testing.T) (*store.Store, *sql.DB) {
	t.Helper()
	s, raw := openBuildStore(ctx, t)
	item := checkingEvidence()
	version := store.TaskVersionInput{
		Source: store.SourceOrchestrator, Step: "step-2", Reason: "the evidence moved", Evidence: &item,
	}
	err := s.RerunTask(ctx, buildEpic, 1, store.TaskBuild{}, openBuildOf(), version, nil, requestDecision(),
		writtenAt())
	if err != nil {
		t.Fatalf("RerunTask: %v", err)
	}
	return s, raw
}

func TestTaskVersionEvidenceChangeIsStoredChecking(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openCheckingStore(ctx, t)

	task := buildTaskOf(ctx, t, s)
	if len(task.Versions) != 1 {
		t.Fatalf("Versions = %+v, want the one version the rerun stored", task.Versions)
	}
	got := task.Versions[0]
	if got.State != store.VersionChecking || !got.Settled.IsZero() {
		t.Errorf("version state %q settled %v, want checking and unsettled", got.State, got.Settled)
	}
	if got.Evidence == nil || got.Evidence.Conclusion != checkingEvidence().Conclusion {
		t.Errorf("Evidence = %+v, want the version's %+v", got.Evidence, checkingEvidence())
	}
	if want := buildPlan().Tasks[0].Evidence.Conclusion; task.Evidence.Conclusion != want || task.Version != 1 {
		t.Errorf("task evidence %q at version %d, want the planner's %q at version 1", task.Evidence.Conclusion,
			task.Version, want)
	}
	version, ok := task.Checking()
	if !ok || version.Version != 2 {
		t.Errorf("Checking() = %+v, %t, want version 2", version, ok)
	}
}

func TestTaskVersionSettleAppliesACheckingVersion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openCheckingStore(ctx, t)
	settledAt := time.Unix(1700000900, 0)

	err := s.SettleTaskVersion(ctx, buildEpic, 1, openBuildOf(), openBuildOf(), store.TaskVersionSettle{
		Version: 2, State: store.VersionApplied, RedRun: checkingRedRun,
	}, settledAt)
	if err != nil {
		t.Fatalf("SettleTaskVersion: %v", err)
	}

	task := buildTaskOf(ctx, t, s)
	if task.Version != 2 || task.Evidence.Conclusion != checkingEvidence().Conclusion {
		t.Errorf("task at version %d with evidence %q, want version 2 with the version's evidence", task.Version,
			task.Evidence.Conclusion)
	}
	if len(task.Versions) != 1 {
		t.Fatalf("Versions = %+v, want the one version", task.Versions)
	}
	got := task.Versions[0]
	if got.State != store.VersionApplied || got.RedRun != checkingRedRun || !got.Settled.Equal(settledAt) {
		t.Errorf("version state %q red run %q settled %v, want applied, the red run and %v",
			got.State, got.RedRun, got.Settled, settledAt)
	}
	if _, ok := task.Checking(); ok {
		t.Errorf("Checking() reports a checking version after the settle, want none")
	}
}

func TestTaskVersionSettleRefusesAndKeepsTheCurrentVersion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openCheckingStore(ctx, t)
	const refusal = "red: the conclusion holds at the base"

	err := s.SettleTaskVersion(ctx, buildEpic, 1, openBuildOf(), openBuildOf(), store.TaskVersionSettle{
		Version: 2, State: store.VersionRefused, RedRun: checkingRedRun, Refusal: refusal,
	}, writtenAt())
	if err != nil {
		t.Fatalf("SettleTaskVersion: %v", err)
	}

	task := buildTaskOf(ctx, t, s)
	if want := buildPlan().Tasks[0].Evidence.Conclusion; task.Version != 1 || task.Evidence.Conclusion != want {
		t.Errorf("task at version %d with evidence %q, want the planner's version 1 and evidence %q", task.Version,
			task.Evidence.Conclusion, want)
	}
	if len(task.Versions) != 1 || task.Versions[0].State != store.VersionRefused ||
		task.Versions[0].Refusal != refusal {
		t.Errorf("Versions = %+v, want the version listed as refused for %q", task.Versions, refusal)
	}
	var state, stored string
	err = raw.QueryRow(`SELECT state, refusal FROM epic_task_versions WHERE epic = ? AND version = 2`,
		buildEpic).Scan(&state, &stored)
	if err != nil {
		t.Fatalf("read version row: %v", err)
	}
	if state != store.VersionRefused || stored != refusal {
		t.Errorf("row state %q refusal %q, want refused and %q", state, stored, refusal)
	}
}

func TestTaskVersionSettleOnlyOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openCheckingStore(ctx, t)
	err := s.SettleTaskVersion(ctx, buildEpic, 1, openBuildOf(), openBuildOf(), store.TaskVersionSettle{
		Version: 2, State: store.VersionApplied, RedRun: checkingRedRun,
	}, writtenAt())
	if err != nil {
		t.Fatalf("first SettleTaskVersion: %v", err)
	}
	before := rawRows(t, raw, `SELECT * FROM epic_task_versions WHERE epic = ? ORDER BY version`, buildEpic)

	again := openBuildOf()
	again.Step = "step-9"
	err = s.SettleTaskVersion(ctx, buildEpic, 1, openBuildOf(), again, store.TaskVersionSettle{
		Version: 2, State: store.VersionRefused, RedRun: "other", Refusal: "a second settle",
	}, time.Unix(1700000950, 0))
	if !errors.Is(err, store.ErrVersionNotChecking) {
		t.Fatalf("second SettleTaskVersion = %v, want ErrVersionNotChecking", err)
	}

	after := rawRows(t, raw, `SELECT * FROM epic_task_versions WHERE epic = ? ORDER BY version`, buildEpic)
	if !reflect.DeepEqual(after, before) {
		t.Errorf("version rows after the second settle = %v, want them unchanged from %v", after, before)
	}
	if build := buildTaskOf(ctx, t, s).Build; build.Step != openBuildOf().Step {
		t.Errorf("Build.Step = %q after the second settle, want the build of the first settle %q", build.Step,
			openBuildOf().Step)
	}
}
