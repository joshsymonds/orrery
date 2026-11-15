package workflow

import (
	"slices"
	"strings"
	"testing"
)

// topLevelFile is a flow whose one top-level node x is of the kind. Its kind sits on line 4.
func topLevelFile(kind string, extra ...string) string {
	return lines(append([]string{"name: flow", "nodes:", "  - id: x", "    kind: " + kind}, extra...)...)
}

// loopBodyFile is a flow whose loop r holds node x of the kind and stops when x passes. Its kind sits on line 9.
func loopBodyFile(kind string, extra ...string) string {
	head := []string{"name: flow", "nodes:", "  - id: r", "    loop:", "      max: 2", "      until: x.passed",
		"    body:", "      - id: x", "        kind: " + kind}
	return lines(slices.Concat(head, extra)...)
}

// planBodyFile is a flow whose plan p holds node x of the kind. Its kind sits on line 7.
func planBodyFile(kind string) string {
	return lines("name: flow", "nodes:", "  - id: p", "    kind: plan", "    body:", "      - id: x",
		"        kind: "+kind)
}

// kindsOf appends to out each node under nodes, depth first, as id:kind, a loop with an empty kind.
func kindsOf(out []string, nodes []Node) []string {
	for _, node := range nodes {
		out = kindsOf(append(out, node.ID+":"+node.Kind), node.Body)
	}
	return out
}

func TestParseEpicKindsInTheBuiltInShape(t *testing.T) {
	t.Parallel()
	text := lines("name: flow", "nodes:",
		"  - id: red", "    kind: evidence", "    at: base",
		"  - id: plan", "    kind: plan", "    body:", "      - id: build", "        kind: build",
		"  - id: review", "    kind: review",
		"  - id: correct", "    loop:", "      max: 2", "      until: closure.passed", "      spent: continue",
		"    body:",
		"      - id: fix", "        kind: plan", "        body:", "          - id: rebuild", "            kind: build",
		"      - id: closure", "        kind: closure",
		"  - id: conclude", "    kind: conclude",
		"  - id: release", "    kind: release")
	got, err := Parse(testFile, []byte(text))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := Check(got); err != nil {
		t.Fatalf("Check: %v", err)
	}
	want := "red:evidence plan:plan build:build review:review correct: fix:plan rebuild:build closure:closure " +
		"conclude:conclude release:release"
	if names := strings.Join(kindsOf(nil, got.Nodes), " "); names != want {
		t.Fatalf("nodes = %q, want %q", names, want)
	}
}

func TestParseEpicKindsTakeNoSettings(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{kindReview, kindClosure, kindConclude, kindRelease} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			text := topLevelFile(kind)
			if kind == kindClosure {
				text = loopBodyFile(kind)
			}
			got, err := Parse(testFile, []byte(text))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			node := got.Nodes[0]
			if kind == kindClosure {
				node = node.Body[0]
			}
			if node.Kind != kind || node.Settings != nil || node.Body != nil {
				t.Fatalf("node x: kind %q, settings set %t, body set %t; want kind %q and neither set", node.Kind,
					node.Settings != nil, node.Body != nil, kind)
			}
		})
	}
}

func TestParseRefusesEpicKindPlacement(t *testing.T) {
	t.Parallel()
	cases := make([]refusalCase, 0, 11)
	cases = append(cases, []refusalCase{
		{
			name: "closure at the top level",
			text: topLevelFile(kindClosure),
			want: at(4, "node x: key kind: closure cannot sit at the top level"),
		},
		{
			name: "closure in a plan body",
			text: planBodyFile(kindClosure),
			want: at(7, "node x: key kind: closure cannot sit in a plan body"),
		},
		{
			name: "review with a body",
			text: topLevelFile(kindReview, "    body:", "      - id: b", "        kind: build"),
			want: at(5, "node x: key body: unknown key"),
		},
		{
			name: "release with a setting",
			text: topLevelFile(kindRelease, "    at: head"),
			want: at(5, "node x: key at: unknown key"),
		},
		{
			name: "closure with a setting",
			text: loopBodyFile(kindClosure, "        at: head"),
			want: at(10, "node x: key at: unknown key"),
		},
	}...)
	for _, kind := range []string{kindReview, kindConclude, kindRelease} {
		cases = append(cases,
			refusalCase{
				name: kind + " in a loop body",
				text: loopBodyFile(kind),
				want: at(9, "node x: key kind: "+kind+" cannot sit in a loop body"),
			},
			refusalCase{
				name: kind + " in a plan body",
				text: planBodyFile(kind),
				want: at(7, "node x: key kind: "+kind+" cannot sit in a plan body"),
			},
		)
	}
	checkRefusals(t, cases)
}

// TestCheckEpicKindOutcomes builds a loop whose until names a node of each epic kind, so the outcomes each kind
// reports are checked apart from where the kind may sit.
func TestCheckEpicKindOutcomes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind    string
		outcome string
		reports bool
	}{
		{kind: kindReview, outcome: outcomePassed, reports: true},
		{kind: kindReview, outcome: outcomeFailed, reports: false},
		{kind: kindClosure, outcome: outcomePassed, reports: true},
		{kind: kindClosure, outcome: outcomeFailed, reports: true},
		{kind: kindClosure, outcome: outcomeComplete, reports: false},
		{kind: kindConclude, outcome: outcomePassed, reports: true},
		{kind: kindConclude, outcome: outcomeFailed, reports: false},
		{kind: kindRelease, outcome: outcomePassed, reports: true},
		{kind: kindRelease, outcome: outcomeFailed, reports: true},
	}
	for _, tc := range cases {
		t.Run(tc.kind+" "+tc.outcome, func(t *testing.T) {
			t.Parallel()
			loop := Node{
				ID:   "r",
				Loop: &Loop{Max: 2, Until: Outcome{Node: "x", Name: tc.outcome}, Spent: spentPark},
				Body: []Node{{ID: "x", Kind: tc.kind, File: testFile, Line: 4}},
				File: testFile,
				Line: 2,
			}
			err := Check(Workflow{Name: "flow", File: testFile, Nodes: []Node{loop}})
			if tc.reports {
				if err != nil {
					t.Fatalf("Check: %v", err)
				}
				return
			}
			want := at(2, `node r: key loop.until: x has no outcome "`+tc.outcome+`"`)
			if err == nil || err.Error() != want {
				t.Fatalf("Check = %v, want refusal %q", err, want)
			}
		})
	}
}
