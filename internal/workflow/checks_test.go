package workflow

import (
	"reflect"
	"slices"
	"testing"
)

// checkKindCase is one check kind, the settings lines it is written with, and the Settings those lines read to.
type checkKindCase struct {
	kind     string
	settings []string
	want     Settings
}

// checkKindCases holds every check kind with each of its settings. Each settings line sits under the kind's key.
func checkKindCases() []checkKindCase {
	return []checkKindCase{
		{
			kind:     "gate",
			settings: []string{"gate: lint", "at: base"},
			want:     Settings{At: "base", Gate: "lint"},
		},
		{
			kind:     "evidence",
			settings: []string{"at: head"},
			want:     Settings{At: "head"},
		},
		{
			kind:     "owned",
			settings: []string{"paths:", "  - internal/a/", "  - b.go"},
			want:     Settings{Paths: []string{"internal/a/", "b.go"}},
		},
		{
			kind: "command",
			settings: []string{"argv:", "  - ./bin/check", "  - -v", "limit: 30", "needs:", "  tests: 2",
				"  build: 1", "at: head"},
			want: Settings{At: "head", Argv: []string{"./bin/check", "-v"}, Limit: 30,
				Needs: []Need{{Pool: "build", Count: 1}, {Pool: "tests", Count: 2}}},
		},
		{
			kind: "agent",
			settings: []string{"agent: reviewer", "prompt: Review the change.", "schema:", "  type: object",
				"  required: [verdict]", "  properties:", "    verdict: {type: string}", "at: head"},
			want: Settings{At: "head", Agent: "reviewer", Prompt: "Review the change.",
				Schema: `{"properties":{"verdict":{"type":"string"}},"required":["verdict"],"type":"object"}`},
		},
	}
}

// kindNodeText is one node of each check kind, read from indent: its dash sits at indent and its keys two spaces in.
// Each id is the kind's name behind prefix.
func kindNodeText(prefix, indent string) []string {
	cases := checkKindCases()
	blocks := make([][]string, 0, len(cases))
	for _, c := range cases {
		head := []string{indent + "- id: " + prefix + c.kind, indent + "  kind: " + c.kind}
		blocks = append(blocks, slices.Concat(head, indentAll(indent+"  ", c.settings)))
	}
	return slices.Concat(blocks...)
}

// kindNodes is the Node each kindNodeText with the same prefix reads to, in the same order.
func kindNodes(prefix string) []Node {
	cases := checkKindCases()
	nodes := make([]Node, 0, len(cases))
	for _, c := range cases {
		want := c.want
		nodes = append(nodes, Node{ID: prefix + c.kind, Kind: c.kind, Settings: &want})
	}
	return nodes
}

// indentAll returns each line of text with indent put before it.
func indentAll(indent string, text []string) []string {
	out := make([]string, 0, len(text))
	for _, line := range text {
		out = append(out, indent+line)
	}
	return out
}

// checkFile is a flow whose one top-level node is a check node: id c, of the kind, with settings lines under the kind.
// The node sits on line 3, its kind on line 4 and the first setting on line 5.
func checkFile(kind string, settings ...string) string {
	head := []string{"name: flow", "nodes:", "  - id: c", "    kind: " + kind}
	return lines(slices.Concat(head, indentAll("    ", settings))...)
}

// checkLoopFile is a flow whose loop r holds check node c, of the kind, and stops when c reports outcome. The loop
// sits on line 3, its until on line 6 and the check node on line 8.
func checkLoopFile(c checkKindCase, outcome string) string {
	head := []string{"name: flow", "nodes:", "  - id: r", "    loop:", "      max: 2", "      until: c." + outcome,
		"    body:", "      - id: c", "        kind: " + c.kind}
	return lines(slices.Concat(head, indentAll("        ", c.settings))...)
}

func TestParseReadsCheckSettings(t *testing.T) {
	t.Parallel()
	text := slices.Concat(
		[]string{"name: flow", "nodes:"},
		kindNodeText("", "  "),
		[]string{"  - id: r", "    loop:", "      max: 2", "      until: lgate.passed", "    body:"},
		kindNodeText("l", "      "),
		[]string{"  - id: p", "    kind: plan", "    body:", "      - id: pb", "        kind: build"},
		kindNodeText("p", "      "),
	)
	got, err := Parse(testFile, []byte(lines(text...)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := kindNodes("")
	want = append(want,
		Node{
			ID:   "r",
			Loop: &Loop{Max: 2, Until: Outcome{Node: "lgate", Name: outcomePassed}, Spent: spentPark},
			Body: kindNodes("l"),
		},
		Node{
			ID:   "p",
			Kind: kindPlan,
			Body: append([]Node{{ID: "pb", Kind: kindBuild}}, kindNodes("p")...),
		},
	)
	if !reflect.DeepEqual(stripOrigin(got.Nodes), stripOrigin(want)) {
		t.Fatalf("Parse nodes = %+v, want %+v", got.Nodes, want)
	}
	if err := Check(got); err != nil {
		t.Fatalf("Check: %v", err)
	}
}

func TestParseRefusesCheckSettings(t *testing.T) {
	t.Parallel()
	cases := []refusalCase{
		{
			name: "gate with no gate",
			text: checkFile("gate", "at: base"),
			want: at(3, "node c: key gate: required"),
		},
		{
			name: "gate with no at",
			text: checkFile("gate", "gate: lint"),
			want: at(3, "node c: key at: required"),
		},
		{
			name: "gate name not a gate",
			text: checkFile("gate", "gate: Lint", "at: base"),
			want: at(5, `node c: key gate: "Lint" must match ^[a-z][a-z0-9_]*$`),
		},
		{
			name: "at not base or head",
			text: checkFile("gate", "gate: lint", "at: both"),
			want: at(6, `node c: key at: "both" must be base or head`),
		},
		{
			name: "gate with a limit",
			text: checkFile("gate", "gate: lint", "at: base", "limit: 3"),
			want: at(7, "node c: key limit: unknown key"),
		},
		{
			name: "gate with a body",
			text: checkFile("gate", "gate: lint", "at: base", "body:", "  - id: x", "    kind: build"),
			want: at(7, "node c: key body: unknown key"),
		},
		{
			name: "evidence with no at",
			text: checkFile("evidence"),
			want: at(3, "node c: key at: required"),
		},
		{
			name: "evidence with a gate",
			text: checkFile("evidence", "at: head", "gate: lint"),
			want: at(6, "node c: key gate: unknown key"),
		},
		{
			name: "owned with no paths",
			text: checkFile("owned"),
			want: at(3, "node c: key paths: required"),
		},
		{
			name: "owned paths not a sequence",
			text: checkFile("owned", "paths: a/"),
			want: at(5, "node c: key paths: must be a sequence of paths"),
		},
		{
			name: "owned paths empty",
			text: checkFile("owned", "paths: []"),
			want: at(5, "node c: key paths: must hold at least one path"),
		},
		{
			name: "owned path absolute",
			text: checkFile("owned", "paths:", "  - /abs"),
			want: at(6, `node c: key paths: item 1 "/abs" must be a repository-relative path`),
		},
		{
			name: "owned path with a dot-dot element",
			text: checkFile("owned", "paths:", "  - internal/../x"),
			want: at(6, `node c: key paths: item 1 "internal/../x" must be a repository-relative path`),
		},
		{
			name: "owned path with an empty element",
			text: checkFile("owned", "paths:", "  - a//b"),
			want: at(6, `node c: key paths: item 1 "a//b" must be a repository-relative path`),
		},
		{
			name: "owned with an at",
			text: checkFile("owned", "paths:", "  - a.go", "at: base"),
			want: at(7, "node c: key at: unknown key"),
		},
		{
			name: "owned with a body",
			text: checkFile("owned", "paths:", "  - a.go", "body:", "  - id: x", "    kind: build"),
			want: at(7, "node c: key body: unknown key"),
		},
		{
			name: "command with no argv",
			text: checkFile("command", "limit: 5", "at: base"),
			want: at(3, "node c: key argv: required"),
		},
		{
			name: "command argv not a sequence",
			text: checkFile("command", "argv: ./x", "limit: 5", "at: base"),
			want: at(5, "node c: key argv: must be a sequence of strings"),
		},
		{
			name: "command argv empty",
			text: checkFile("command", "argv: []", "limit: 5", "at: base"),
			want: at(5, "node c: key argv: must hold at least one string"),
		},
		{
			name: "command argv item empty",
			text: checkFile("command", "argv:", `  - ""`, "limit: 5", "at: base"),
			want: at(6, "node c: key argv: item 1 must be a non-empty string"),
		},
		{
			name: "command argv[0] without a slash",
			text: checkFile("command", "argv:", "  - check", "limit: 5", "at: base"),
			want: at(6, `node c: key argv: item 1 "check" must contain a /`),
		},
		{
			name: "command with no limit",
			text: checkFile("command", "argv:", "  - ./x", "at: base"),
			want: at(3, "node c: key limit: required"),
		},
		{
			name: "command limit zero",
			text: checkFile("command", "argv:", "  - ./x", "limit: 0", "at: base"),
			want: at(7, "node c: key limit: must be an integer of 1 or more"),
		},
		{
			name: "command limit not a whole number",
			text: checkFile("command", "argv:", "  - ./x", "limit: 2.5", "at: base"),
			want: at(7, "node c: key limit: must be an integer of 1 or more"),
		},
		{
			name: "command with no at",
			text: checkFile("command", "argv:", "  - ./x", "limit: 5"),
			want: at(3, "node c: key at: required"),
		},
		{
			name: "command needs not a mapping",
			text: checkFile("command", "argv:", "  - ./x", "limit: 5", "needs: [tests]", "at: base"),
			want: at(8, "node c: key needs: must be a mapping"),
		},
		{
			name: "command needs pool not a name",
			text: checkFile("command", "argv:", "  - ./x", "limit: 5", "needs:", "  Tests: 1", "at: base"),
			want: at(9, `node c: key needs.Tests: "Tests" must match ^[a-z][a-z0-9_]*$`),
		},
		{
			name: "command needs count zero",
			text: checkFile("command", "argv:", "  - ./x", "limit: 5", "needs:", "  tests: 0", "at: base"),
			want: at(9, "node c: key needs.tests: must be an integer of 1 or more"),
		},
		{
			name: "command needs pool twice",
			text: checkFile("command", "argv:", "  - ./x", "limit: 5", "needs:", "  tests: 1", "  tests: 2",
				"at: base"),
			want: at(10, "node c: key needs.tests: appears twice"),
		},
		{
			name: "command with an agent",
			text: checkFile("command", "argv:", "  - ./x", "limit: 5", "at: base", "agent: x"),
			want: at(9, "node c: key agent: unknown key"),
		},
		{
			name: "agent with no agent",
			text: checkFile("agent", "prompt: p", "schema: {}", "at: head"),
			want: at(3, "node c: key agent: required"),
		},
		{
			name: "agent blank",
			text: checkFile("agent", `agent: " "`, "prompt: p", "schema: {}", "at: head"),
			want: at(5, "node c: key agent: must not be blank"),
		},
		{
			name: "agent with no prompt",
			text: checkFile("agent", "agent: r", "schema: {}", "at: head"),
			want: at(3, "node c: key prompt: required"),
		},
		{
			name: "agent with no schema",
			text: checkFile("agent", "agent: r", "prompt: p", "at: head"),
			want: at(3, "node c: key schema: required"),
		},
		{
			name: "agent schema not a mapping",
			text: checkFile("agent", "agent: r", "prompt: p", "schema: [object]", "at: head"),
			want: at(7, "node c: key schema: must be a mapping"),
		},
		{
			name: "agent schema holds a value JSON cannot",
			text: checkFile("agent", "agent: r", "prompt: p", "schema:", "  a: .nan", "at: head"),
			want: at(7, "node c: key schema: must be a mapping of JSON values"),
		},
		{
			name: "agent with no at",
			text: checkFile("agent", "agent: r", "prompt: p", "schema: {}"),
			want: at(3, "node c: key at: required"),
		},
	}
	checkRefusals(t, cases)
}

// TestCheckLoopUntilCheckOutcomes checks that a loop whose until names a check node's passed or failed outcome is
// accepted, for every check kind.
func TestCheckLoopUntilCheckOutcomes(t *testing.T) {
	t.Parallel()
	for _, c := range checkKindCases() {
		for _, outcome := range []string{outcomePassed, outcomeFailed} {
			t.Run(c.kind+" "+outcome, func(t *testing.T) {
				t.Parallel()
				parsed, err := Parse(testFile, []byte(checkLoopFile(c, outcome)))
				if err != nil {
					t.Fatalf("Parse: %v", err)
				}
				if err := Check(parsed); err != nil {
					t.Fatalf("Check refused until %s.%s: %v", c.kind, outcome, err)
				}
			})
		}
	}
}

// TestCheckLoopUntilRefusesOtherOutcome checks that a loop whose until names an outcome a check kind does not report
// is refused, for every check kind.
func TestCheckLoopUntilRefusesOtherOutcome(t *testing.T) {
	t.Parallel()
	for _, c := range checkKindCases() {
		t.Run(c.kind, func(t *testing.T) {
			t.Parallel()
			parsed, err := Parse(testFile, []byte(checkLoopFile(c, outcomeComplete)))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			want := at(3, `node r: key loop.until: c has no outcome "complete"`)
			if err := Check(parsed); err == nil || err.Error() != want {
				t.Fatalf("Check = %v, want refusal %q", err, want)
			}
		})
	}
}

// TestLoadCheckKindsCanonicalText loads a repository file holding the five kinds, with their keys, pools and schema
// keys out of canonical order. The canonical text writes the settings in order, needs and schema keys sorted, and
// reparses to the same Settings.
func TestLoadCheckKindsCanonicalText(t *testing.T) {
	t.Parallel()
	flow := lines(
		"name: flow", "nodes:",
		"  - id: g", "    kind: gate", "    gate: lint", "    at: base",
		"  - id: e", "    kind: evidence", "    at: head",
		"  - id: o", "    kind: owned", "    paths:", "      - internal/a/", "      - b.go",
		"  - id: c", "    kind: command", "    needs:", "      tests: 2", "      build: 1", "    limit: 30",
		"    argv:", "      - ./bin/check", "      - -v", "    at: head",
		"  - id: a", "    kind: agent", "    schema:", "      type: object", "      properties:",
		"        verdict: {type: string}", "    prompt: Review the change.", "    agent: reviewer", "    at: head",
	)
	want := lines(
		"name: flow", "nodes:",
		"  - id: g", "    kind: gate", "    at: base", "    gate: lint",
		"  - id: e", "    kind: evidence", "    at: head",
		"  - id: o", "    kind: owned", "    paths:", "      - internal/a/", "      - b.go",
		"  - id: c", "    kind: command", "    at: head", "    argv:", "      - ./bin/check", "      - -v",
		"    limit: 30", "    needs:", "      build: 1", "      tests: 2",
		"  - id: a", "    kind: agent", "    at: head", "    agent: reviewer", "    prompt: Review the change.",
		"    schema:", "      properties:", "        verdict:", "          type: string", "      type: object",
	)
	got := loadFlow(t, flow)
	if got.Text != want {
		t.Fatalf("Text = %q, want %q", got.Text, want)
	}
	checkPin(t, got, repoPath("flow"))
}
