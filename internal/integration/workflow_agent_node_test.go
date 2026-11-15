package integration_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// The tests in this file run a pinned workflow's agent node through the real orreryd, with the fake claude in place of
// claude. The node runs one read-only step at the epic's base under its agent, prompt and schema. The READDONE prompt
// waits for fakeclaude.release in the step's worktree and then ends done with no commit, as a read-only step does. A
// plain prompt answers with no structured return, so the step never ends done and its retries run out.

// agentNode is the id of the agent node the tests run. agentNodeAgent is the agent it names, whose file the rig writes
// under the config directory claude reads agents from.
const (
	agentNode      = "review"
	agentNodeAgent = "orrery-reviewer"
)

// agentNodeSchema is the node's schema as the workflow holds it: its compact JSON, with sorted keys.
const agentNodeSchema = `{"properties":{"status":{"type":"string"},"summary":{"type":"string"}},` +
	`"required":["status"],"type":"object"}`

// The prompts the tests give the node. The fake claude answers the first with done and the second with no return.
const (
	agentNodeDonePrompt    = "READDONE review the base read-only"
	agentNodeNotDonePrompt = "review the base read-only"
)

// agentStepPart is the part an agent node's step is stored under, which is NodeWorkID of the node run and its attempt.
const agentStepPart = "step"

// agentNodesText returns a workflow whose one agent node asks prompt of agentNodeAgent at the base, under the node's
// schema.
func agentNodesText(prompt string) string {
	return fmt.Sprintf(`name: %s
nodes:
  - id: %s
    kind: agent
    agent: %s
    prompt: %q
    schema: {type: object, properties: {status: {type: string}, summary: {type: string}}, required: [status]}
    at: base
`, nodesName, agentNode, agentNodeAgent, prompt)
}

// newAgentNodesRig returns the nodes rig with the node's agent written under the rig's config directory, which claude
// reads agents from. The fake claude writes its files into the step's worktree, so the repository's info/exclude
// ignores them, as the build rig's ignore file does. The file is not committed, so the base stays the rig's commit.
func newAgentNodesRig(t *testing.T) *buildRig {
	t.Helper()
	r := newNodesRig(t)
	config := filepath.Join(r.f.root, "home", ".claude")
	stepReadoptWrite(t, filepath.Join(config, "agents", agentNodeAgent+".md"),
		"---\nname: "+agentNodeAgent+"\ndescription: Reviews a base\n---\n")
	r.f.env = append(r.f.env, "CLAUDE_CONFIG_DIR="+config)
	stepReadoptWrite(t, filepath.Join(r.repo, ".git", "info", "exclude"), "fakeclaude.*\n")
	return r
}

// epicSteps returns the labeled steps of the rig's epic, oldest first, as ListSteps reports them.
func (r *buildRig) epicSteps(t *testing.T) []*v1.Step {
	t.Helper()
	resp, err := r.steps().ListSteps(t.Context(), connect.NewRequest(&v1.ListStepsRequest{Epic: buildRigEpic}))
	if err != nil {
		t.Fatalf("list steps of epic %s: %v", buildRigEpic, err)
	}
	return resp.Msg.GetSteps()
}

// awaitEpicStep waits until the rig's epic holds a labeled step, and returns it. An agent node's step is labeled with
// its epic and no task, so the epic holds no other step.
func (r *buildRig) awaitEpicStep(t *testing.T) *v1.Step {
	t.Helper()
	var steps []*v1.Step
	waitUntil(t, func() bool {
		steps = r.epicSteps(t)
		return len(steps) > 0
	})
	if len(steps) != 1 {
		t.Fatalf("labeled steps of epic %s = %d, want the agent node's one step", buildRigEpic, len(steps))
	}
	return steps[0]
}

// requireAgentStep fails unless step, as the store lists it, is the read-only step of run: its id is derived from run,
// and it runs at the epic's base in its own worktree under the node's agent.
func (r *buildRig) requireAgentStep(t *testing.T, step *v1.Step, run sqlcgen.EpicNodeRun) {
	t.Helper()
	if want := store.NodeWorkID(run.ID, run.Attempt, agentStepPart); step.GetId() != want {
		t.Fatalf("step id = %q, want %q derived from node run %s", step.GetId(), want, run.ID)
	}
	if !step.GetReadOnly() || step.GetRevision() != r.base || step.GetAgent() != agentNodeAgent ||
		step.GetEpic() != buildRigEpic || step.GetTask() != "" || step.GetWorktree() == "" {
		t.Fatalf("step %+v, want a read-only step at base %s under agent %s in its own worktree",
			step, r.base, agentNodeAgent)
	}
}

// requireAgentClaude fails unless the fake claude of step was started with the node's agent and schema and read
// prompt. It reads the fake claude's files in the step's worktree, which the step removes when it ends, so the caller
// holds the step open until it returns.
func requireAgentClaude(t *testing.T, step *v1.Step, prompt string) {
	t.Helper()
	dir := step.GetWorktree()
	stdin := filepath.Join(dir, "fakeclaude.stdin")
	waitUntil(t, func() bool { return fileContains(t, stdin, prompt) })
	args := filepath.Join(dir, "fakeclaude.args")
	if !fileContains(t, args, "--agent "+agentNodeAgent) {
		t.Fatalf("fake claude was not started with --agent %s", agentNodeAgent)
	}
	if !fileContains(t, args, "--json-schema "+agentNodeSchema) {
		t.Fatalf("fake claude was not started with the node's schema %s", agentNodeSchema)
	}
}

// TestWorkflowAgentNodeStepDonePassesNode proves that an agent node of a pinned workflow runs one read-only step
// through the real orreryd, and that the node ends passed once that step ends done.
func TestWorkflowAgentNodeStepDonePassesNode(t *testing.T) {
	t.Parallel()
	r := newAgentNodesRig(t)
	seedNodesEpic(t, r, agentNodesText(agentNodeDonePrompt), nil)
	daemon, ack := r.startPassDaemon(t, "first")
	t.Cleanup(func() { clearHolders(t, r.stateDir) })

	step := r.awaitEpicStep(t)
	r.requireAgentStep(t, step, r.nodeRun(t, agentNode))
	requireAgentClaude(t, step, agentNodeDonePrompt)
	stepReadoptTouch(t, filepath.Join(step.GetWorktree(), "fakeclaude.release"))
	if end := r.awaitEnd(t, step.GetId()).GetEnd(); end != "done" {
		t.Fatalf("step %s ended %q, want done", step.GetId(), end)
	}
	signalPass(t, daemon, ack)
	if run := r.nodeRun(t, agentNode); run.State != store.NodeEnded || run.Outcome != "passed" {
		t.Fatalf("agent node %q with outcome %q, want ended passed; output %q", run.State, run.Outcome, run.Output)
	}
	wantExit(t, daemon.stop(t), 0)
}

// TestWorkflowAgentNodeStepNotDoneFailsAndParks proves that an agent node whose step does not end done ends failed, and
// that the failure parks the epic and names the node.
func TestWorkflowAgentNodeStepNotDoneFailsAndParks(t *testing.T) {
	t.Parallel()
	r := newAgentNodesRig(t)
	seedNodesEpic(t, r, agentNodesText(agentNodeNotDonePrompt), nil)
	daemon, ack := r.startPassDaemon(t, "first")
	t.Cleanup(func() { clearHolders(t, r.stateDir) })

	step := r.awaitEpicStep(t)
	// This step ends at once and its worktree goes with it, so only the stored step is checked here; the done test
	// checks what the fake claude was started with.
	r.requireAgentStep(t, step, r.nodeRun(t, agentNode))
	if end := r.awaitEnd(t, step.GetId()).GetEnd(); end == "done" {
		t.Fatalf("step %s ended done, want a step that does not end done", step.GetId())
	}
	signalPass(t, daemon, ack)
	if run := r.nodeRun(t, agentNode); run.State != store.NodeParked || run.Outcome != "failed" {
		t.Fatalf("agent node %q with outcome %q, want parked failed; output %q", run.State, run.Outcome, run.Output)
	}
	if reason := r.epic(t).GetParked(); !strings.Contains(reason, "workflow node "+agentNode+" failed") {
		t.Fatalf("park reason %q does not name node %s", reason, agentNode)
	}
	wantExit(t, daemon.stop(t), 0)
}
