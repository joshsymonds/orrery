package interpret

import (
	"context"
	"reflect"
	"testing"
)

// TestOrchestratorParkPassGivesThePlannerAnEpicParkedAtATopLevelNode checks that a Pass gives the planner's pass an epic a
// planner park parked at a top-level node with no plan run running, so the planner's turn for it starts.
func TestOrchestratorParkPassGivesThePlannerAnEpicParkedAtATopLevelNode(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo, base := nodeRepo(t)
	s, _ := openNodeStore(t)
	createNodeEpic(t, s, repo, base, nodeLintFlow)
	planner := &fakePlanner{known: []string{runEpic}}
	cfg := nodeConfig(s, newNodeFakes(s))
	cfg.Planner = planner
	runner := New(cfg)
	runner.Pass(ctx)
	check := workID(t, s, "lint", "check")

	// The failed check parks the top-level node lint, which is a planner park: the epic has no plan run. The pass that
	// parks it reads the epic before the park, so the next pass is the one that gives the planner the epic. The runner
	// names no role file, so the check's triage records a change at once.
	nodeEndCheck(t, s, check, 1, "lint failed")
	runner.Pass(ctx)
	if got := epicOf(t, s).Parked; got != "workflow node lint failed: "+refusedTriage+"\nlint failed" {
		t.Fatalf("epic Parked = %q, want the lint node's park", got)
	}

	planner.named = nil
	runner.Pass(ctx)
	if want := [][]string{{runEpic}}; !reflect.DeepEqual(planner.named, want) {
		t.Errorf("PassOnly named %v, want %v: the planner park gives its epic the planner's pass", planner.named, want)
	}
}
