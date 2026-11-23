package store

import (
	"context"
	"reflect"
	"testing"
)

// captureGoneRunRow returns the stored row of evidence_runs with id, each column's value keyed by the column's name.
func captureGoneRunRow(t *testing.T, s *Store, id string) map[string]any {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(), `SELECT * FROM evidence_runs WHERE id = ?`, id)
	if err != nil {
		t.Fatalf("read evidence run %s: %v", id, err)
	}
	names, err := rows.Columns()
	if err != nil {
		t.Fatalf("columns of evidence run %s: %v", id, err)
	}
	if !rows.Next() {
		t.Fatalf("evidence run %s: no stored row", id)
	}
	values := make([]any, len(names))
	ptrs := make([]any, len(names))
	for i := range values {
		ptrs[i] = &values[i]
	}
	if err = rows.Scan(ptrs...); err != nil {
		t.Fatalf("scan evidence run %s: %v", id, err)
	}
	if err = rowsDone(rows); err != nil {
		t.Fatalf("read evidence run %s: %v", id, err)
	}
	row := make(map[string]any, len(names))
	for i, name := range names {
		row[name] = values[i]
	}
	return row
}

// TestCaptureGoneMigratesCapturingRunToWaitingBrief seeds a store at version 96 with a capturing run, a judging run and an
// ended run, then migrates it up with 00097. The capturing run becomes an open brief: it is waiting, holds no capture,
// gatherer or gatherer count, and keeps every other field. The judging and ended runs are unchanged.
func TestCaptureGoneMigratesCapturingRunToWaitingBrief(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, provider := storeAtVersion(t, 96)
	spec, err := encodeItem(runItem())
	if err != nil {
		t.Fatalf("encode item: %v", err)
	}
	if _, err = s.fixtureExec(ctx, `INSERT INTO evidence_runs
		(id, epic, item, revision, phase, source, state, spec, grant_id, holder, created)
		VALUES ('run-capturing', ?, 'R1', 'abc1234', 'red', 'captured', 'capturing', ?, 'grant-1', 'holder-1', 1700000100)`,
		runEpic, spec); err != nil {
		t.Fatalf("insert capturing run: %v", err)
	}
	if _, err = s.fixtureExec(ctx, `INSERT INTO evidence_runs
		(id, epic, item, revision, phase, source, state, spec, grant_id, holder, exit, log_hash, log_bytes, judge_step,
		judges, capture, gatherer, gatherers, created)
		VALUES ('run-judging', ?, 'R1', 'abc1234', 'green', 'captured', 'judging', ?, 'grant-2', 'holder-2', 0,
		'sha256-log', 17, 'judge-step-1', 1, 'capture-1', 'step-gatherer', 1, 1700000200)`, runEpic, spec); err != nil {
		t.Fatalf("insert judging run: %v", err)
	}
	if _, err = s.fixtureExec(ctx, `INSERT INTO evidence_runs
		(id, epic, item, revision, phase, source, state, spec, grant_id, holder, verdict, judge_reason, passed, ended,
		judges, capture, gatherers, created)
		VALUES ('run-ended', ?, 'R1', 'abc1234', 'red', 'captured', 'ended', ?, '', '', 'holds',
		'the output holds', 1, 1700000300, 1, 'capture-2', 2, 1700000250)`, runEpic, spec); err != nil {
		t.Fatalf("insert ended run: %v", err)
	}

	capturing := captureGoneRunRow(t, s, "run-capturing")
	judging := captureGoneRunRow(t, s, "run-judging")
	ended := captureGoneRunRow(t, s, "run-ended")

	if _, err = provider.UpByOne(ctx); err != nil {
		t.Fatalf("Up of 00097: %v", err)
	}

	want := make(map[string]any, len(capturing))
	for column, value := range capturing {
		want[column] = value
	}
	want["state"] = "waiting"
	if got := captureGoneRunRow(t, s, "run-capturing"); !reflect.DeepEqual(got, want) {
		t.Errorf("capturing run after Up = %v, want %v", got, want)
	}
	if got := captureGoneRunRow(t, s, "run-judging"); !reflect.DeepEqual(got, judging) {
		t.Errorf("judging run after Up = %v, want it unchanged, %v", got, judging)
	}
	if got := captureGoneRunRow(t, s, "run-ended"); !reflect.DeepEqual(got, ended) {
		t.Errorf("ended run after Up = %v, want it unchanged, %v", got, ended)
	}

	open, err := s.OpenBriefs(ctx, runEpic)
	if err != nil {
		t.Fatalf("OpenBriefs: %v", err)
	}
	if len(open) != 1 || open[0].ID != "run-capturing" || open[0].State != RunWaiting || open[0].Source != "captured" {
		t.Errorf("open briefs after Up = %+v, want the capturing run as one waiting captured brief", open)
	}
}
