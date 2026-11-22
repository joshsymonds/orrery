package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// hostedEpic is the epic the host tests create, and hostEpicRow is its row, created at the unix second 1700000000 with the
// host given.
const hostedEpic = "orrery-0668c87/epic-hosted"

func hostEpicRow(host string) EpicRow {
	created := time.Unix(1700000000, 0)
	return EpicRow{
		ID: hostedEpic, Repository: "/work/synthetic-repo", Base: "main", Title: "Hosted epic",
		Approved: "Approved sentence", Intent: "Store the host", Care: "serious",
		Approach: "Read the host back", ReleaseThen: "Then line", Checks: "just check", Phase: "accepted",
		Created: created, Changed: created, Host: host,
	}
}

// TestEngineHostColumns checks that epics and steps store the host their work lives on: a row stored before hosts reads
// the controller name once AdoptHost names it, a new epic keeps its host, and a step takes its epic's host, or keeps its
// own host when it has no epic.
func TestEngineHostColumns(t *testing.T) {
	t.Parallel()
	t.Run("old rows read the controller name", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		path := filepath.Join(t.TempDir(), "orrery.db")

		fixture := openStoreAtVersion(t, path, 94)
		seedEpic(t, fixture)
		if _, err := fixture.ExecContext(ctx, `INSERT INTO steps (id, agent, prompt, output_schema, workspace, revision,
			worktree, done_value, read_only, owned, gate, gate_limit, started, checks, result, status, end_value, reason,
			ended, model_limit, doing, verdicts, epic, task, gate_name, gate_source, gate_needs)
			VALUES ('step-1', 'haiku', 'Implement the thing', '{"type":"object"}', '/work/ws', 'abc1234', '/work/wt',
			'done', 0, '["a.go"]', '["just","check"]', 1800, 1700000000, '[]', '{}', 'passed', '{"ok":true}', '',
			1700000600, 60, 'tests', '[{"ok":true}]', ?, 'T2', 'check', 'argv', '[]')`, seededEpic); err != nil {
			fixture.Close()
			t.Fatalf("seed step: %v", err)
		}
		if err := fixture.Close(); err != nil {
			t.Fatalf("close fixture: %v", err)
		}

		s, err := Open(ctx, path, noSync())
		if err != nil {
			t.Fatalf("Open at version 94: %v", err)
		}
		defer s.Close()
		if err := s.AdoptHost(ctx, "ctl"); err != nil {
			t.Fatalf("AdoptHost: %v", err)
		}
		epic, err := s.Epic(ctx, seededEpic)
		if err != nil {
			t.Fatalf("Epic: %v", err)
		}
		if epic.Host != "ctl" {
			t.Errorf("epic host = %q, want %q", epic.Host, "ctl")
		}
		step, err := s.Step(ctx, "step-1")
		if err != nil {
			t.Fatalf("Step: %v", err)
		}
		if step.Host != "ctl" {
			t.Errorf("step host = %q, want %q", step.Host, "ctl")
		}

		// A row that already names a host keeps it, so a second adoption under another name changes nothing.
		if err := s.AdoptHost(ctx, "other"); err != nil {
			t.Fatalf("second AdoptHost: %v", err)
		}
		if epic, err = s.Epic(ctx, seededEpic); err != nil || epic.Host != "ctl" {
			t.Errorf("epic host after a second AdoptHost = %q, %v; want %q", epic.Host, err, "ctl")
		}
		if step, err = s.Step(ctx, "step-1"); err != nil || step.Host != "ctl" {
			t.Errorf("step host after a second AdoptHost = %q, %v; want %q", step.Host, err, "ctl")
		}
	})

	t.Run("new epic stores its host", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		s := openEpicStore(t)
		createEpic(t, s, hostEpicRow("ctl"))

		epic, err := s.Epic(ctx, hostedEpic)
		if err != nil {
			t.Fatalf("Epic: %v", err)
		}
		if epic.Host != "ctl" {
			t.Errorf("epic host = %q, want %q", epic.Host, "ctl")
		}
		host, err := s.EpicHost(ctx, hostedEpic)
		if err != nil {
			t.Fatalf("EpicHost: %v", err)
		}
		if host != "ctl" {
			t.Errorf("EpicHost = %q, want %q", host, "ctl")
		}
		if _, err := s.EpicHost(ctx, "orrery-0668c87/epic-missing"); !errors.Is(err, ErrNoEpic) {
			t.Errorf("EpicHost of an unknown epic err = %v, want ErrNoEpic", err)
		}
	})

	t.Run("new step copies its epic host", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		s := openEpicStore(t)
		createEpic(t, s, hostEpicRow("ctl"))
		add := StepAdd{ID: "step-hosted", Agent: "implementer", Prompt: "do it", Epic: hostedEpic, Host: "other"}
		if err := s.ApplyEngine(ctx, []EngineOp{add}, time.Unix(1700000600, 0)); err != nil {
			t.Fatalf("ApplyEngine add step: %v", err)
		}

		step, err := s.Step(ctx, "step-hosted")
		if err != nil {
			t.Fatalf("Step: %v", err)
		}
		if step.Host != "ctl" {
			t.Errorf("step host = %q, want its epic's %q", step.Host, "ctl")
		}
	})

	t.Run("step without epic stores its own host", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		s := openEpicStore(t)
		add := StepAdd{ID: "step-loose", Agent: "implementer", Prompt: "do it", Host: "ctl"}
		if err := s.ApplyEngine(ctx, []EngineOp{add}, time.Unix(1700000600, 0)); err != nil {
			t.Fatalf("ApplyEngine add step: %v", err)
		}

		step, err := s.Step(ctx, "step-loose")
		if err != nil {
			t.Fatalf("Step: %v", err)
		}
		if step.Host != "ctl" {
			t.Errorf("step host = %q, want %q", step.Host, "ctl")
		}
	})
}
