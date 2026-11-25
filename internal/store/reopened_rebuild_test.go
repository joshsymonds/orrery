package store

import (
	"context"
	"testing"
)

// The paths of the build child runs the rebuild tests start under the plan at "plan".
const (
	rebuildEndedPath   = "plan/build/T1"
	rebuildRunningPath = "plan/build/T2"
	rebuildParkedPath  = "plan/build/T3"
)

// rebuildChild returns the build child run of task seq at path, at attempt 0, as a plan's fan-out starts it.
func rebuildChild(path string, seq int64) NodeRun {
	return NodeRun{Path: path, ID: "id-" + path, Node: "build", Kind: "build", Task: seq}
}

// TestReopenedTaskRebuildRestartsEndedBuildRun checks that RebuildTask turns the build run at its path, which ended at
// the attempt it names, running at its next attempt with its outcome, output, revision, refs, refs checked, result and
// end cleared, keeping its start. A rebuild at another attempt, of a running run and of a parked run changes nothing.
func TestReopenedTaskRebuildRestartsEndedBuildRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	startIntegrateRuns(t, s, rebuildChild(rebuildEndedPath, 1), rebuildChild(rebuildRunningPath, 2),
		rebuildChild(rebuildParkedPath, 3))
	if err := s.PrepareNode(ctx, integrateEpic, rebuildEndedPath, 0, "rev-1", "refs-1"); err != nil {
		t.Fatalf("PrepareNode: %v", err)
	}
	if err := s.RecordNodeResult(ctx, integrateEpic, rebuildEndedPath, 0, `{"built":true}`); err != nil {
		t.Fatalf("RecordNodeResult: %v", err)
	}
	if err := s.CheckNodeRefs(ctx, integrateEpic, rebuildEndedPath, 0, nil, integrateEndedAt()); err != nil {
		t.Fatalf("CheckNodeRefs: %v", err)
	}
	if err := s.EndNodes(ctx, integrateEpic, []NodeEnd{
		{Path: rebuildEndedPath, Outcome: "passed", Output: "built"},
	}, integrateEndedAt()); err != nil {
		t.Fatalf("EndNodes: %v", err)
	}
	if err := s.ParkNode(ctx, integrateEpic, rebuildParkedPath, 0, "failed", "stuck", ParkClassNode, "stuck",
		integrateParkedAt()); err != nil {
		t.Fatalf("ParkNode: %v", err)
	}
	endedRun := integrateRun(t, s, rebuildEndedPath)
	runningRun := integrateRun(t, s, rebuildRunningPath)
	parkedRun := integrateRun(t, s, rebuildParkedPath)

	if err := s.RebuildTask(ctx, integrateEpic, rebuildEndedPath, 1); err != nil {
		t.Fatalf("RebuildTask at another attempt: %v", err)
	}
	requireIntegrateRun(t, s, rebuildEndedPath, endedRun)

	if err := s.RebuildTask(ctx, integrateEpic, rebuildEndedPath, 0); err != nil {
		t.Fatalf("RebuildTask of an ended build run: %v", err)
	}
	requireIntegrateRun(t, s, rebuildEndedPath, NodeRun{
		Path: rebuildEndedPath, ID: "id-" + rebuildEndedPath, Node: "build", Kind: "build", Task: 1, Attempt: 1,
		State: NodeRunning, Started: integrateStartedAt(),
	})

	for _, run := range []NodeRun{runningRun, parkedRun} {
		if err := s.RebuildTask(ctx, integrateEpic, run.Path, 0); err != nil {
			t.Fatalf("RebuildTask of %s run %s: %v", run.State, run.Path, err)
		}
		requireIntegrateRun(t, s, run.Path, run)
	}
}
