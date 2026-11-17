package enginehost

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/reviewers"
)

// restrictedAgent is the agent a test defines with a tools array that omits StructuredOutput.
const restrictedAgent = "restricted-check"

// returningAgent is the agent a test defines with a tools array that holds StructuredOutput.
const returningAgent = "returning-check"

// agentsJSON returns the JSON object of one agent definition, agent, whose tools array is tools, the text between the
// brackets. It has no spaces, so it stays one field of the fake claude's argument line.
func agentsJSON(agent, tools string) string {
	return `{"` + agent + `":{"description":"checks","prompt":"check","tools":[` + tools + `]}}`
}

// fakeClaudeWithArgs runs the scripted fake claude with args on its command line, in a fresh directory, with input on
// stdin. It returns the lines the fake wrote to stdout.
func fakeClaudeWithArgs(t *testing.T, args []string, input string) []string {
	t.Helper()
	cmd := exec.Command("/bin/sh", append([]string{fakeClaudePath(t)}, args...)...)
	cmd.Dir = t.TempDir()
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run fake claude with %q: %v", args, err)
	}
	return strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
}

// initToolsOf returns the tools list of the init line in lines. It fails the test when no init line is there.
func initToolsOf(t *testing.T, lines []string) []string {
	t.Helper()
	for _, line := range lines {
		var head struct {
			Type    string   `json:"type"`
			Subtype string   `json:"subtype"`
			Tools   []string `json:"tools"`
		}
		if err := json.Unmarshal([]byte(line), &head); err != nil {
			t.Fatalf("stdout line is not JSON: %v\n%s", err, line)
		}
		if head.Type == "system" && head.Subtype == "init" {
			return head.Tools
		}
	}
	t.Fatalf("stdout has no init line:\n%s", strings.Join(lines, "\n"))
	return nil
}

// structuredOutputOf returns the structured_output of the result line in lines, or nil when the result has none.
func structuredOutputOf(t *testing.T, lines []string) json.RawMessage {
	t.Helper()
	for _, line := range lines {
		var head struct {
			Type             string          `json:"type"`
			StructuredOutput json.RawMessage `json:"structured_output"`
		}
		if err := json.Unmarshal([]byte(line), &head); err != nil {
			t.Fatalf("stdout line is not JSON: %v\n%s", err, line)
		}
		if head.Type == "result" {
			return head.StructuredOutput
		}
	}
	t.Fatalf("stdout has no result line:\n%s", strings.Join(lines, "\n"))
	return nil
}

// TestStructuredOutputMissingFromInitEndsStepFailed covers R1: a writing step with a schema whose inline agent's tools
// omit StructuredOutput ends failed when its init event arrives. Its reason names the agent and StructuredOutput, no
// schema round is spent, and no Continue is sent, so the fake claude reads only the step's first prompt.
func TestStructuredOutputMissingFromInitEndsStepFailed(t *testing.T) {
	t.Parallel()
	f := newRefusalFixture(t, 1)
	workspace := stepWorktreeTestAddLinked(t, stepGitTestRepo(t))
	start := stepStartWriting(workspace)
	start.Agent = restrictedAgent
	start.Agents = agentsJSON(restrictedAgent, `"Read","Bash"`)
	id, err := refusalStart(t, f.steps, start)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	stepStartAwait(t, engineOf(f.steps, f.manager), "step "+id+" to end", func() bool {
		row, err := f.steps.Status(t.Context(), id)
		return err == nil && row.End != ""
	})
	row, err := f.steps.Status(t.Context(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if row.End != stepLoopEndFailed {
		t.Fatalf("end = %q, want %q; reason %q", row.End, stepLoopEndFailed, row.Reason)
	}
	for _, want := range []string{restrictedAgent, "StructuredOutput"} {
		if !strings.Contains(row.Reason, want) {
			t.Fatalf("reason = %q, want it to name %s", row.Reason, want)
		}
	}
	if row.LoopRound != 0 || row.LoopContinues != 0 {
		t.Fatalf("round %d continues %d, want no schema round and no Continue", row.LoopRound, row.LoopContinues)
	}
	// A Continue sends a user prompt, so the prompts the fake claude read are the lines that are user messages.
	stdin, err := os.ReadFile(filepath.Join(workspace, "fakeclaude.stdin"))
	if err != nil {
		t.Fatalf("read fake stdin log: %v", err)
	}
	if n := strings.Count(string(stdin), `"type":"user"`); n != 1 {
		t.Fatalf("fake claude read %d prompts, want only the step's first prompt:\n%s", n, stdin)
	}
	requireGone(t, fakePID(t, workspace))
}

// TestStructuredOutputInInitReturnsValueAndEndsDone covers R1 for a step whose inline agent's tools hold
// StructuredOutput: the step runs as before, its return is the value the fake claude returns, and it ends done.
func TestStructuredOutputInInitReturnsValueAndEndsDone(t *testing.T) {
	t.Parallel()
	rig := newStepsRig(t, nil)
	repo := stepGitTestRepo(t)
	revision := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	start := stepStartReadOnly(repo, revision)
	start.Agent = returningAgent
	start.Agents = agentsJSON(returningAgent, `"Read","StructuredOutput"`)
	start.Prompt = "look READDONE"
	id, err := rig.steps.Start(t.Context(), start)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	releaseResumedTurn(t, filepath.Join(rig.worktrees, id))
	waitUntil(t, engineOf(rig.manager, rig.steps), "step ended", func() bool {
		return stepRowOf(t, rig.steps, id).End != ""
	})
	row := stepRowOf(t, rig.steps, id)
	if row.End != stepLoopEndDone {
		t.Fatalf("end = %q, want %q; reason %q", row.End, stepLoopEndDone, row.Reason)
	}
	var value struct {
		Status  string `json:"status"`
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(row.Result), &value); err != nil {
		t.Fatalf("result %q does not decode: %v", row.Result, err)
	}
	if value.Status != "done" || value.Summary != "fake done" {
		t.Fatalf("result = %q, want the fake claude's done return", row.Result)
	}
}

// TestStructuredOutputModelReviewerStepReturnsVerdict covers R1 and R2 for a model reviewer: its step runs the inline
// agent that reviewers.Reviewer.Agents builds for the default conformance reviewer, whose tools hold StructuredOutput,
// so the fake claude returns the verdict and the step ends done.
func TestStructuredOutputModelReviewerStepReturnsVerdict(t *testing.T) {
	t.Parallel()
	reviewer := defaultConformanceReviewer(t)
	agents, err := reviewer.Agents()
	if err != nil {
		t.Fatalf("Agents: %v", err)
	}
	gate := reviewer.Gate()
	gate.Assertions = []gates.Assertion{{Text: "JUDGEHOLDS", Kind: gates.KindMust}}

	rig := newStepsRig(t, nil)
	repo := stepGitTestRepo(t)
	revision := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	start := stepStartReadOnly(repo, revision)
	start.Agent = ""
	start.Schema = ""
	start.Prompt = "review the change"
	start.Agents = agents
	start.AgentGate = &gate
	id, err := rig.steps.Start(t.Context(), start)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	releaseResumedTurn(t, filepath.Join(rig.worktrees, id))
	waitUntil(t, engineOf(rig.manager, rig.steps), "reviewer step ended", func() bool {
		return stepRowOf(t, rig.steps, id).End != ""
	})
	row := stepRowOf(t, rig.steps, id)
	if row.End != stepLoopEndDone {
		t.Fatalf("end = %q, want %q; reason %q", row.End, stepLoopEndDone, row.Reason)
	}
	var verdict struct {
		Status  string `json:"status"`
		Results []struct {
			Assertion string `json:"assertion"`
			Result    string `json:"result"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(row.Result), &verdict); err != nil {
		t.Fatalf("result %q does not decode: %v", row.Result, err)
	}
	if verdict.Status != "done" || len(verdict.Results) != 1 ||
		verdict.Results[0].Assertion != "JUDGEHOLDS" || verdict.Results[0].Result != "holds" {
		t.Fatalf("result = %q, want the holding verdict of the fake claude", row.Result)
	}
}

// defaultConformanceReviewer returns the default conformance reviewer, merged as a project's reviewers are.
func defaultConformanceReviewer(t *testing.T) reviewers.Reviewer {
	t.Helper()
	merged, err := reviewers.Merge(reviewers.Defaults())
	if err != nil {
		t.Fatalf("Merge defaults: %v", err)
	}
	i := slices.IndexFunc(merged, func(r reviewers.Reviewer) bool { return r.Name == "conformance" })
	if i < 0 {
		t.Fatal("the defaults hold no conformance reviewer")
	}
	return merged[i]
}

// TestStructuredOutputFakeInitListsRestrictedAgentTools covers R2: the fake claude's init lists exactly the tools of
// the --agents definition of its --agent, and returns structured output only when StructuredOutput is among them. With
// no --agents, its init lists a full set that holds StructuredOutput.
func TestStructuredOutputFakeInitListsRestrictedAgentTools(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		args  []string
		tools []string
		// returns is whether the fake claude's result carries structured output.
		returns bool
	}{
		{
			name:    "restricted without StructuredOutput",
			args:    []string{"--agents", agentsJSON(restrictedAgent, `"Read","Bash"`), "--agent", restrictedAgent},
			tools:   []string{"Read", "Bash"},
			returns: false,
		},
		{
			name: "restricted with StructuredOutput",
			args: []string{
				"--agents", agentsJSON(returningAgent, `"Read","StructuredOutput"`), "--agent", returningAgent,
			},
			tools:   []string{"Read", "StructuredOutput"},
			returns: true,
		},
		{
			name:    "no --agents",
			args:    nil,
			tools:   nil,
			returns: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			lines := fakeClaudeWithArgs(t, tc.args, userLine("BGDONE"))
			tools := initToolsOf(t, lines)
			if tc.tools != nil && !slices.Equal(tools, tc.tools) {
				t.Fatalf("init tools = %q, want %q", tools, tc.tools)
			}
			if tc.tools == nil && !slices.Contains(tools, "StructuredOutput") {
				t.Fatalf("init tools = %q, want a full set that holds StructuredOutput", tools)
			}
			structured := structuredOutputOf(t, lines)
			if got := structured != nil; got != tc.returns {
				t.Fatalf("structured output = %s, want it returned %v", structured, tc.returns)
			}
		})
	}
}
