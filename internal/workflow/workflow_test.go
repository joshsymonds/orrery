package workflow

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// testFile is the repository path of the workflow the refusal tests parse. Its name is flow.
const testFile = ".orrery/workflows/flow.yaml"

// refusalCase is one file that must be refused, and the exact refusal it must get.
type refusalCase struct {
	name string
	text string
	want string
}

// lines joins text lines into one file, each line ended by a newline.
func lines(text ...string) string {
	return strings.Join(text, "\n") + "\n"
}

// at is a refusal at one line of testFile.
func at(line int, rest string) string {
	return fmt.Sprintf("%s:%d: %s", testFile, line, rest)
}

// loopFile is a flow whose one top-level node is loop r over plan a holding build b. Each loop line is indented under
// the loop key, so the first one sits on line 5.
func loopFile(loop ...string) string {
	text := []string{"name: flow", "nodes:", "  - id: r", "    loop:"}
	for _, line := range loop {
		text = append(text, "      "+line)
	}
	body := []string{
		"    body:",
		"      - id: a",
		"        kind: plan",
		"        body:",
		"          - id: b",
		"            kind: build",
	}
	return lines(append(text, body...)...)
}

// checkRefusals parses each case's text as testFile and fails unless the refusal is exactly the case's want.
func checkRefusals(t *testing.T, cases []refusalCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Parse(testFile, []byte(tc.text))
			if err == nil {
				t.Fatalf("Parse accepted the file; want refusal %q", tc.want)
			}
			if err.Error() != tc.want {
				t.Fatalf("Parse refusal = %q, want %q", err.Error(), tc.want)
			}
		})
	}
}

func TestParseRefusesFile(t *testing.T) {
	t.Parallel()
	cases := []refusalCase{
		{name: "empty file", text: "", want: testFile + ": empty file: a workflow is one YAML document"},
		{
			name: "two documents",
			text: lines("name: flow", "---", "name: flow"),
			want: at(2, "a workflow is one YAML document"),
		},
		{name: "not a mapping", text: lines("- flow"), want: at(1, "a workflow is a mapping of name and nodes")},
		{
			name: "repeated top key",
			text: lines("name: flow", "name: flow", "nodes: []"),
			want: at(2, "key name: appears twice"),
		},
		{
			name: "unknown top key",
			text: lines("name: flow", "nodes:", "  - id: a", "    kind: plan", "    body:", "      - id: b",
				"        kind: build", "extra: 1"),
			want: at(8, "key extra: unknown key"),
		},
		{
			name: "missing name",
			text: lines("nodes:", "  - id: a", "    kind: plan", "    body:", "      - id: b", "        kind: build"),
			want: at(1, "key name: required"),
		},
		{name: "missing nodes", text: lines("name: flow"), want: at(1, "key nodes: required")},
		{
			name: "empty nodes",
			text: lines("name: flow", "nodes: []"),
			want: at(2, "key nodes: must hold at least one node"),
		},
		{
			name: "nodes not a sequence",
			text: lines("name: flow", "nodes: a"),
			want: at(2, "key nodes: must be a sequence of nodes"),
		},
		{
			name: "node item not a mapping",
			text: lines("name: flow", "nodes:", "  - build"),
			want: at(3, "key nodes: item 1 must be a mapping"),
		},
		{
			name: "name differs from the file",
			text: lines("name: other", "nodes:", "  - id: a", "    kind: plan", "    body:", "      - id: b",
				"        kind: build"),
			want: at(1, `key name: "other" differs from the file's name "flow"`),
		},
		{
			name: "bad name pattern",
			text: lines("name: Flow", "nodes:", "  - id: a", "    kind: plan", "    body:", "      - id: b",
				"        kind: build"),
			want: at(1, `key name: "Flow" must match ^[a-z][a-z0-9_-]*$`),
		},
		{
			name: "name not a single value",
			text: lines("name: [flow]", "nodes:", "  - id: a", "    kind: plan", "    body:", "      - id: b",
				"        kind: build"),
			want: at(1, "key name: must be a single value"),
		},
	}
	checkRefusals(t, cases)
}

func TestParseRefusesNode(t *testing.T) {
	t.Parallel()
	cases := []refusalCase{
		{
			name: "unknown kind",
			text: lines("name: flow", "nodes:", "  - id: a", "    kind: review"),
			want: at(4, `node a: key kind: unknown kind "review"`),
		},
		{
			name: "unknown key in a kind node",
			text: lines("name: flow", "nodes:", "  - id: a", "    kind: plan", "    color: red", "    body:",
				"      - id: b", "        kind: build"),
			want: at(5, "node a: key color: unknown key"),
		},
		{
			name: "build at the top level",
			text: lines("name: flow", "nodes:", "  - id: b", "    kind: build"),
			want: at(4, "node b: key kind: build cannot sit at the top level"),
		},
		{
			name: "plan inside a plan body",
			text: lines("name: flow", "nodes:", "  - id: a", "    kind: plan", "    body:", "      - id: inner",
				"        kind: plan", "        body:", "          - id: b", "            kind: build"),
			want: at(7, "node inner: key kind: plan cannot sit in a plan body"),
		},
		{
			name: "include inside a plan body",
			text: lines("name: flow", "nodes:", "  - id: a", "    kind: plan", "    body:", "      - include: other"),
			want: at(6, "node #1: key include: cannot sit in a plan body"),
		},
		{
			name: "kind and loop on one node",
			text: lines("name: flow", "nodes:", "  - id: r", "    kind: plan", "    loop:", "      max: 2",
				"      until: a.complete", "    body:", "      - id: a", "        kind: build"),
			want: at(5, "node r: key loop: cannot sit beside kind"),
		},
		{
			name: "node with no form",
			text: lines("name: flow", "nodes:", "  - id: a"),
			want: at(3, "node a: key kind: one of kind, loop or include is required"),
		},
		{
			name: "node without id",
			text: lines("name: flow", "nodes:", "  - kind: plan", "    body:", "      - id: b", "        kind: build"),
			want: at(3, "node #1: key id: required"),
		},
		{
			name: "bad id pattern",
			text: lines("name: flow", "nodes:", "  - id: Plan", "    kind: plan", "    body:", "      - id: b",
				"        kind: build"),
			want: at(3, `node Plan: key id: "Plan" must match ^[a-z][a-z0-9_-]*$`),
		},
		{
			name: "repeated key in a node",
			text: lines("name: flow", "nodes:", "  - id: a", "    id: b", "    kind: plan"),
			want: at(4, "node a: key id: appears twice"),
		},
		{
			name: "plan body not a sequence",
			text: lines("name: flow", "nodes:", "  - id: a", "    kind: plan", "    body: build"),
			want: at(5, "node a: key body: must be a sequence of nodes"),
		},
		{
			name: "plan body empty",
			text: lines("name: flow", "nodes:", "  - id: a", "    kind: plan", "    body: []"),
			want: at(5, "node a: key body: must hold at least one node"),
		},
		{
			name: "plan body item not a mapping",
			text: lines("name: flow", "nodes:", "  - id: a", "    kind: plan", "    body:", "      - build"),
			want: at(6, "node a: key body: item 1 must be a mapping"),
		},
		{
			name: "plan without body",
			text: lines("name: flow", "nodes:", "  - id: a", "    kind: plan"),
			want: at(3, "node a: key body: required"),
		},
		{
			name: "integrate at the top level",
			text: lines("name: flow", "nodes:", "  - id: i", "    kind: integrate"),
			want: at(4, "node i: key kind: integrate cannot sit at the top level"),
		},
		{
			name: "integrate in a loop body",
			text: lines("name: flow", "nodes:", "  - id: r", "    loop:", "      max: 2", "      until: i.passed",
				"    body:", "      - id: i", "        kind: integrate"),
			want: at(9, "node i: key kind: integrate cannot sit in a loop body"),
		},
		{
			name: "setting on integrate",
			text: lines("name: flow", "nodes:", "  - id: a", "    kind: plan", "    body:", "      - id: b",
				"        kind: build", "      - id: i", "        kind: integrate", "        at: base"),
			want: at(10, "node i: key at: unknown key"),
		},
		{
			name: "body on integrate",
			text: lines("name: flow", "nodes:", "  - id: a", "    kind: plan", "    body:", "      - id: b",
				"        kind: build", "      - id: i", "        kind: integrate", "        body:",
				"          - id: c", "            kind: build"),
			want: at(10, "node i: key body: unknown key"),
		},
	}
	checkRefusals(t, cases)
}

func TestParseRefusesLoop(t *testing.T) {
	t.Parallel()
	cases := []refusalCase{
		{
			name: "loop with no max",
			text: loopFile("until: a.complete"),
			want: at(5, "node r: key loop.max: required"),
		},
		{
			name: "loop with no until",
			text: loopFile("max: 2"),
			want: at(5, "node r: key loop.until: required"),
		},
		{
			name: "loop max zero",
			text: loopFile("max: 0", "until: a.complete"),
			want: at(5, "node r: key loop.max: must be an integer of 1 or more"),
		},
		{
			name: "loop max not an integer",
			text: loopFile("max: many", "until: a.complete"),
			want: at(5, "node r: key loop.max: must be an integer of 1 or more"),
		},
		{
			name: "loop max not a whole number",
			text: loopFile("max: 2.5", "until: a.complete"),
			want: at(5, "node r: key loop.max: must be an integer of 1 or more"),
		},
		{
			name: "unknown key inside loop",
			text: loopFile("max: 2", "until: a.complete", "every: 1"),
			want: at(7, "node r: key loop.every: unknown key"),
		},
		{
			name: "spent other than park or continue",
			text: loopFile("max: 2", "until: a.complete", "spent: stop"),
			want: at(7, "node r: key loop.spent: must be park or continue"),
		},
		{
			name: "until without an outcome",
			text: loopFile("max: 2", "until: complete"),
			want: at(6, "node r: key loop.until: must be <node id>.<outcome>"),
		},
		{
			name: "loop not a mapping",
			text: lines("name: flow", "nodes:", "  - id: r", "    loop: 3"),
			want: at(4, "node r: key loop: must be a mapping"),
		},
		{
			name: "loop body not a sequence",
			text: lines("name: flow", "nodes:", "  - id: r", "    loop:", "      max: 2", "      until: a.complete",
				"    body: a"),
			want: at(7, "node r: key body: must be a sequence of nodes"),
		},
	}
	checkRefusals(t, cases)
}

func TestParseRefusesInclude(t *testing.T) {
	t.Parallel()
	cases := []refusalCase{
		{
			name: "include with an id",
			text: lines("name: flow", "nodes:", "  - id: a", "    include: other"),
			want: at(3, "node a: key id: unknown key"),
		},
		{
			name: "include name bad pattern",
			text: lines("name: flow", "nodes:", "  - include: Other"),
			want: at(3, `node #1: key include: "Other" must match ^[a-z][a-z0-9_-]*$`),
		},
	}
	checkRefusals(t, cases)
}

// checkCases are files Parse accepts and Check refuses, with the refusal Check must give.
func checkCases() []refusalCase {
	return []refusalCase{
		{
			name: "duplicate id",
			text: lines("name: flow", "nodes:", "  - id: p", "    kind: plan", "    body:", "      - id: b",
				"        kind: build", "  - id: p", "    kind: plan", "    body:", "      - id: c",
				"        kind: build"),
			want: at(8, "node p: key id: already used at "+testFile+":3"),
		},
		{
			name: "unresolved include",
			text: lines("name: flow", "nodes:", "  - include: other"),
			want: at(3, `node #1: key include: unresolved include "other"`),
		},
		{
			name: "until names no body node",
			text: loopFile("max: 2", "until: nope.complete"),
			want: at(3, `node r: key loop.until: "nope" is not a node in the loop body`),
		},
		{
			name: "until names an outcome its kind lacks",
			text: loopFile("max: 2", "until: a.done"),
			want: at(3, `node r: key loop.until: a has no outcome "done"`),
		},
	}
}

func TestCheckRefuses(t *testing.T) {
	t.Parallel()
	for _, tc := range checkCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			parsed, err := Parse(testFile, []byte(tc.text))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			err = Check(parsed)
			if err == nil {
				t.Fatalf("Check accepted the workflow; want refusal %q", tc.want)
			}
			if err.Error() != tc.want {
				t.Fatalf("Check refusal = %q, want %q", err.Error(), tc.want)
			}
		})
	}
}

// TestCheckBuildReportsNoOutcome builds a loop whose until names a build. Parse would not let a build sit in a loop
// body, so the workflow is made here: Check still refuses it, because a build reports no outcome.
func TestCheckBuildReportsNoOutcome(t *testing.T) {
	t.Parallel()
	build := Node{ID: "b", Kind: "build", File: testFile, Line: 4}
	loop := Node{
		ID:   "r",
		Loop: &Loop{Max: 2, Until: Outcome{Node: "b", Name: outcomeComplete}, Spent: spentPark},
		Body: []Node{build},
		File: testFile,
		Line: 2,
	}
	err := Check(Workflow{Name: "flow", File: testFile, Nodes: []Node{loop}})
	want := at(2, `node r: key loop.until: b has no outcome "complete"`)
	if err == nil || err.Error() != want {
		t.Fatalf("Check = %v, want refusal %q", err, want)
	}
}

func TestParseEpicShape(t *testing.T) {
	t.Parallel()
	text := lines("name: epic", "nodes:", "  - id: plan", "    kind: plan", "    body:", "      - id: build",
		"        kind: build")
	got, err := Parse("built-in epic", []byte(text))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := Workflow{
		Name: "epic",
		File: "built-in epic",
		Nodes: []Node{{
			ID: "plan", Kind: "plan", File: "built-in epic", Line: 3,
			Body: []Node{{ID: "build", Kind: "build", File: "built-in epic", Line: 6}},
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse = %+v, want %+v", got, want)
	}
	if err := Check(got); err != nil {
		t.Fatalf("Check: %v", err)
	}
}

func TestParseLoopOverPlan(t *testing.T) {
	t.Parallel()
	text := lines("name: flow", "nodes:", "  - id: rounds", "    loop:", "      max: 3", "      until: plan.complete",
		"    body:", "      - id: plan", "        kind: plan", "        body:", "          - id: build",
		"            kind: build")
	got, err := Parse(testFile, []byte(text))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := Workflow{
		Name: "flow",
		File: testFile,
		Nodes: []Node{{
			ID: "rounds", File: testFile, Line: 3,
			Loop: &Loop{Max: 3, Until: Outcome{Node: "plan", Name: "complete"}, Spent: "park"},
			Body: []Node{{
				ID: "plan", Kind: "plan", File: testFile, Line: 8,
				Body: []Node{{ID: "build", Kind: "build", File: testFile, Line: 11}},
			}},
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse = %+v, want %+v", got, want)
	}
	if err := Check(got); err != nil {
		t.Fatalf("Check: %v", err)
	}
}

func TestParsePlanHoldsIntegrate(t *testing.T) {
	t.Parallel()
	text := lines("name: flow", "nodes:", "  - id: a", "    kind: plan", "    body:", "      - id: b",
		"        kind: build", "      - id: i", "        kind: integrate")
	got, err := Parse(testFile, []byte(text))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := Workflow{
		Name: "flow",
		File: testFile,
		Nodes: []Node{{
			ID: "a", Kind: "plan", File: testFile, Line: 3,
			Body: []Node{
				{ID: "b", Kind: "build", File: testFile, Line: 6},
				{ID: "i", Kind: "integrate", File: testFile, Line: 8},
			},
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse = %+v, want %+v", got, want)
	}
	if err := Check(got); err != nil {
		t.Fatalf("Check: %v", err)
	}
}

// TestCheckIntegrateReportsPassedAndFailed builds loops whose until names an integrate. Parse would not let an integrate
// sit in a loop body, so the workflows are made here: Check takes passed and failed and refuses complete.
func TestCheckIntegrateReportsPassedAndFailed(t *testing.T) {
	t.Parallel()
	flow := func(outcome string) Workflow {
		integrate := Node{ID: "i", Kind: kindIntegrate, File: testFile, Line: 4}
		loop := Node{
			ID:   "r",
			Loop: &Loop{Max: 2, Until: Outcome{Node: "i", Name: outcome}, Spent: spentPark},
			Body: []Node{integrate},
			File: testFile,
			Line: 2,
		}
		return Workflow{Name: "flow", File: testFile, Nodes: []Node{loop}}
	}
	for _, outcome := range []string{outcomePassed, outcomeFailed} {
		if err := Check(flow(outcome)); err != nil {
			t.Errorf("Check until i.%s = %v, want nil", outcome, err)
		}
	}
	want := at(2, `node r: key loop.until: i has no outcome "complete"`)
	if err := Check(flow(outcomeComplete)); err == nil || err.Error() != want {
		t.Fatalf("Check until i.complete = %v, want refusal %q", err, want)
	}
}

func TestParseLoopSpentContinue(t *testing.T) {
	t.Parallel()
	got, err := Parse(testFile, []byte(loopFile("max: 2", "until: a.complete", "spent: continue")))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := &Loop{Max: 2, Until: Outcome{Node: "a", Name: "complete"}, Spent: "continue"}
	if !reflect.DeepEqual(got.Nodes[0].Loop, want) {
		t.Fatalf("Loop = %+v, want %+v", got.Nodes[0].Loop, want)
	}
}
