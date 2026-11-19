package build

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/taskspec"
)

// orchestratorVersionedTask returns task seq at status, at version 2: its planner's version 1 deliverable, overlaid by
// applied version 2, which changes the deliverable to deliverable, as the store's read overlays it.
func orchestratorVersionedTask(seq int64, status, deliverable string) store.EpicTask {
	created := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	changed := deliverable
	return store.EpicTask{
		Seq: seq, Title: "Versioned", Deliverable: deliverable, Files: []string{"v.go"}, Status: status,
		Build: store.TaskBuild{Base: "b0se", Stage: StageImplement}, Version: 2,
		Versions: []store.TaskVersion{{
			Version: 2, Source: store.SourceOrchestrator, Decision: "T1-request-1",
			Reason: "the planner named the wrong deliverable", State: store.VersionApplied,
			Deliverable: &changed, Created: created, Settled: created,
		}},
	}
}

func TestTaskVersionOrchestratorTaskLinesRenderTheCurrentVersion(t *testing.T) {
	t.Parallel()
	const deliverable = "read the second spec"
	task := orchestratorVersionedTask(1, taskOpen, deliverable)
	epic := store.EpicRow{Repository: t.TempDir()}
	runner := &Runner{getenv: func(string) string { return "" }}
	got, err := runner.requestTaskLines(context.Background(), epic, task)
	if err != nil {
		t.Fatalf("requestTaskLines: %v", err)
	}
	spec := taskspec.Lines(task)
	if len(spec) == 0 {
		t.Fatalf("taskspec.Lines of a task at version %d is empty, want its version lines", task.Version)
	}
	at := slices.Index(got, "Deliverable: "+deliverable)
	if at < len(spec) {
		t.Fatalf("Task lines = %q, want the version lines %q right before the Deliverable line", got, spec)
	}
	if before := got[at-len(spec) : at]; !slices.Equal(before, spec) {
		t.Errorf("the lines before the Deliverable are %q, want the version lines %q", before, spec)
	}
	if !slices.ContainsFunc(got, func(line string) bool { return strings.Contains(line, "version 2 governs.") }) {
		t.Errorf("Task lines do not say version 2 governs: %q", got)
	}
}

func TestTaskVersionOrchestratorPeerLinesRenderTheCurrentVersion(t *testing.T) {
	t.Parallel()
	task := store.EpicTask{Seq: 1, Title: "Requester", Status: taskOpen, Build: store.TaskBuild{Base: "b0se"}}
	open := orchestratorVersionedTask(2, taskOpen, "open peer deliverable")
	pending := orchestratorVersionedTask(3, taskDone, "pending peer deliverable")
	plain := store.EpicTask{
		Seq: 4, Title: "Plain", Deliverable: "plain deliverable", Status: taskOpen, Version: 1,
		Build: store.TaskBuild{Stage: StageImplement},
	}
	peers := []Peer{
		{Task: task},
		{Task: open, State: Pending},
		{Task: pending, State: Pending, Changed: []string{"p.go"}},
		{Task: plain, State: Pending},
	}
	got := peerLines(context.Background(), "", task, peers)
	for _, peer := range []store.EpicTask{open, pending} {
		var want []string
		for _, line := range taskspec.Lines(peer) {
			want = append(want, "  "+line)
		}
		at := slices.Index(got, "  deliverable: "+peer.Deliverable)
		if at < len(want) {
			t.Fatalf("peer lines = %q, want T%d's version lines %q right before its deliverable line",
				got, peer.Seq, want)
		}
		if before := got[at-len(want) : at]; !slices.Equal(before, want) {
			t.Errorf("T%d's lines before its deliverable are %q, want its version lines %q", peer.Seq, before, want)
		}
	}
	specLines := 0
	for _, line := range got {
		if strings.HasPrefix(line, "  Spec: ") {
			specLines++
		}
	}
	if specLines != 2 {
		t.Errorf("peer lines hold %d Spec lines, want 2: one for each version 2 peer; got %q", specLines, got)
	}
}
