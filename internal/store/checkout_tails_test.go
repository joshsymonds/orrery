package store

import (
	"context"
	"io/fs"
	"reflect"
	"testing"

	"github.com/pressly/goose/v3"
)

// TestStepAddStoresCheckoutBeforeState reads back the checkout head and hash a StepAdd carries, on GetStep's row.
func TestStepAddStoresCheckoutBeforeState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)
	add := stepAddOf("s1")
	add.CheckoutHead = "3f9c2e1a0b7d4c5e6f8a9b0c1d2e3f4a5b6c7d8e"
	add.CheckoutHash = "sha256-checkout-tree"
	if err := s.ApplyEngine(ctx, []EngineOp{add}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	got, err := s.Step(ctx, "s1")
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	if got.CheckoutHead != add.CheckoutHead || got.CheckoutHash != add.CheckoutHash {
		t.Fatalf("checkout of step s1 = (%q, %q), want (%q, %q)",
			got.CheckoutHead, got.CheckoutHash, add.CheckoutHead, add.CheckoutHash)
	}
}

// TestStepReturnStoresCheckTails reads back the stdout and stderr tails of a check a return carries, and reads back
// both empty for a check stored without them.
func TestStepReturnStoresCheckTails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)
	if err := s.ApplyEngine(ctx, []EngineOp{stepAddOf("s1")}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine StepAdd: %v", err)
	}
	ret := StepReturn{ID: "s1", Result: "done", Checks: []StepCheck{
		{Round: 0, Name: "lint", OK: false, Detail: "exit 1", Stdout: "vet: ok\n", Stderr: "main.go:3: boom\n"},
		{Round: 0, Name: "plain", OK: true, Detail: "ok"},
	}}
	if err := s.ApplyEngine(ctx, []EngineOp{ret}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine StepReturn: %v", err)
	}

	got, err := s.Step(ctx, "s1")
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	want := []StepCheck{
		{Round: 0, Name: "lint", OK: false, Detail: "exit 1", Stdout: "vet: ok\n", Stderr: "main.go:3: boom\n"},
		{Round: 0, Name: "plain", OK: true, Detail: "ok"},
	}
	if !reflect.DeepEqual(got.Checks, want) {
		t.Fatalf("Checks of step s1 = %+v, want %+v", got.Checks, want)
	}
}

// TestRecordEvidenceCaptureStoresStderrBesideLog reads back the stderr hash and size a capture records, beside the log
// hash and size, on GetEvidenceRun and in the epic's run list.
func TestRecordEvidenceCaptureStoresStderrBesideLog(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = "run-stderr"
	if err := s.AddEvidenceRun(ctx, newRun(id, runCreatedAt())); err != nil {
		t.Fatalf("AddEvidenceRun: %v", err)
	}
	capture := runCapture()
	capture.StderrHash = "sha256-stderr"
	capture.StderrBytes = 96
	if err := s.RecordEvidenceCapture(ctx, id, capture); err != nil {
		t.Fatalf("RecordEvidenceCapture: %v", err)
	}

	type logs struct {
		LogHash     string
		LogBytes    int64
		StderrHash  string
		StderrBytes int64
	}
	want := logs{LogHash: "sha256-log", LogBytes: 2048, StderrHash: "sha256-stderr", StderrBytes: 96}
	got, err := s.EvidenceRun(ctx, id)
	if err != nil {
		t.Fatalf("EvidenceRun: %v", err)
	}
	if read := (logs{got.LogHash, got.LogBytes, got.StderrHash, got.StderrBytes}); read != want {
		t.Fatalf("EvidenceRun logs = %+v, want %+v", read, want)
	}
	runs, err := s.EvidenceRuns(ctx, runEpic)
	if err != nil {
		t.Fatalf("EvidenceRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("EvidenceRuns = %d runs, want 1", len(runs))
	}
	if read := (logs{runs[0].LogHash, runs[0].LogBytes, runs[0].StderrHash, runs[0].StderrBytes}); read != want {
		t.Fatalf("EvidenceRuns logs = %+v, want %+v", read, want)
	}
}

// TestCheckoutTailsMigrationDownAndUp removes the checkout columns of steps and the stderr columns of evidence_runs
// with the Down of 00023_checkout_tails.sql, and Up restores them.
func TestCheckoutTailsMigrationDownAndUp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)
	stepColumns := []string{"checkout_head", "checkout_hash"}
	runColumns := []string{"stderr_hash", "stderr_bytes"}

	migrationsFS, err := fs.Sub(migrationFS, "migrations")
	if err != nil {
		t.Fatalf("sub migrations fs: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, s.db, migrationsFS)
	if err != nil {
		t.Fatalf("create goose provider: %v", err)
	}

	if _, err := provider.DownTo(ctx, 22); err != nil {
		t.Fatalf("DownTo 22: %v", err)
	}
	if hasAny(tableColumns(t, s, "steps"), stepColumns...) {
		t.Fatalf("steps columns after Down = %v, want no checkout columns", tableColumns(t, s, "steps"))
	}
	if hasAny(tableColumns(t, s, "evidence_runs"), runColumns...) {
		t.Fatalf("evidence_runs columns after Down = %v, want no stderr columns", tableColumns(t, s, "evidence_runs"))
	}

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if !hasAll(tableColumns(t, s, "steps"), stepColumns...) {
		t.Fatalf("steps columns after Up = %v, want the checkout columns", tableColumns(t, s, "steps"))
	}
	if !hasAll(tableColumns(t, s, "evidence_runs"), runColumns...) {
		t.Fatalf("evidence_runs columns after Up = %v, want the stderr columns", tableColumns(t, s, "evidence_runs"))
	}
}
