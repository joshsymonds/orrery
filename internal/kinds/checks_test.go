package kinds

import (
	"reflect"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

var (
	_ Kind = Gate{}
	_ Kind = Command{}
	_ Kind = Evidence{}
	_ Kind = Owned{}
	_ Kind = Agent{}
)

// testRunID is the id of the node run the tests decide, and testRev the revision its work ran at.
const (
	testRunID = "run-1"
	testRev   = "rev-1"
)

// decideCase is one decision a kind makes: the node and rows it decides from and the action it must return.
type decideCase struct {
	name string
	node Node
	rows Rows
	want Action
}

// checkDecisions runs each case through kind and fails a case whose action differs from its want.
func checkDecisions(t *testing.T, kind Kind, cases []decideCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := kind.Decide(tc.node, tc.rows); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Decide() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// workID returns the id of part of the work of the test run at attempt 1.
func workID(part string) string {
	return store.NodeWorkID(testRunID, 1, part)
}

// nodeWith returns a node whose run is the test run at attempt 1, with revision, and refsChecked when its refs were
// compared.
func nodeWith(revision string, refsChecked bool) Node {
	return Node{Run: store.NodeRun{
		ID: testRunID, Attempt: 1, State: store.NodeRunning, Revision: revision, RefsChecked: refsChecked,
	}}
}

// checkCases returns the decisions gate and command nodes make: prepare, start, none, refs and the end.
func checkCases() []decideCase {
	ended := store.NodeCheck{ID: workID("check"), State: store.NodeEnded}
	return []decideCase{
		{
			"paused epic without a revision gives none",
			nodeWith("", false),
			Rows{Epic: store.EpicRow{Paused: true}},
			Action{Kind: ActNone},
		},
		{
			"parked epic without a revision gives none",
			nodeWith("", false),
			Rows{Epic: store.EpicRow{Parked: "waiting on the operator"}},
			Action{Kind: ActNone},
		},
		{"no revision gives prepare", nodeWith("", false), Rows{}, Action{Kind: ActPrepare}},
		{
			"revision with no check starts the check",
			nodeWith(testRev, false),
			Rows{},
			Action{Kind: ActStart, IDs: []string{workID("check")}},
		},
		{
			"check of another attempt does not count",
			nodeWith(testRev, false),
			Rows{Checks: []store.NodeCheck{{ID: store.NodeWorkID(testRunID, 2, "check"), State: store.NodeEnded}}},
			Action{Kind: ActStart, IDs: []string{workID("check")}},
		},
		{
			"running check gives none",
			nodeWith(testRev, false),
			Rows{Checks: []store.NodeCheck{{ID: workID("check"), State: store.NodeRunning}}},
			Action{Kind: ActNone},
		},
		{
			"ended check with refs not checked gives refs",
			nodeWith(testRev, false),
			Rows{Checks: []store.NodeCheck{ended}},
			Action{Kind: ActRefs},
		},
		{
			"ended check with refs checked and exit 0 passes with its stdout",
			nodeWith(testRev, true),
			Rows{Checks: []store.NodeCheck{{ID: workID("check"), State: store.NodeEnded, Stdout: "ok\n"}}},
			Action{Kind: ActEnd, Outcome: "passed", Output: "ok\n"},
		},
		{
			"stdout and stderr are both output, one after the other",
			nodeWith(testRev, true),
			Rows{Checks: []store.NodeCheck{{ID: workID("check"), State: store.NodeEnded, Stdout: "a", Stderr: "b"}}},
			Action{Kind: ActEnd, Outcome: "passed", Output: "a\nb"},
		},
		{
			"nonzero exit fails with its stdout",
			nodeWith(testRev, true),
			Rows{Checks: []store.NodeCheck{{ID: workID("check"), State: store.NodeEnded, Exit: 1, Stdout: "FAIL"}}},
			Action{Kind: ActEnd, Outcome: "failed", Output: "FAIL"},
		},
		{
			"timed out fails even at exit 0",
			nodeWith(testRev, true),
			Rows{Checks: []store.NodeCheck{{ID: workID("check"), State: store.NodeEnded, TimedOut: true}}},
			Action{Kind: ActEnd, Outcome: "failed"},
		},
		{
			"check that did not run fails with the reason",
			nodeWith(testRev, true),
			Rows{Checks: []store.NodeCheck{{ID: workID("check"), State: store.NodeEnded, NotRun: "no gate named x"}}},
			Action{Kind: ActEnd, Outcome: "failed", Output: "no gate named x"},
		},
	}
}

func TestGateDecide(t *testing.T) {
	t.Parallel()
	checkDecisions(t, Gate{}, checkCases())
}

func TestCommandDecide(t *testing.T) {
	t.Parallel()
	checkDecisions(t, Command{}, checkCases())
}

// evidenceItem returns an evidence item with a conclusion, so a Requirement that names it has a non-zero Item.
func evidenceItem(conclusion string) evidence.Item {
	return evidence.Item{Capture: []string{"make", "test"}, Limit: 600, Conclusion: conclusion}
}

// evidenceRun returns an ended evidence run of part of the test run at attempt 1 that passed or not.
func evidenceRun(part, phase string, passed bool) store.EvidenceRun {
	return store.EvidenceRun{ID: workID(part), Item: part, Phase: phase, State: "ended", Passed: passed}
}

func TestEvidenceDecide(t *testing.T) {
	t.Parallel()
	r1 := store.EpicRequirement{ID: "R1", Text: "the suite passes", Item: evidenceItem("the suite passes")}
	r2 := store.EpicRequirement{ID: "R2", Text: "lint is clean", Item: evidenceItem("lint is clean")}
	bare := store.EpicRequirement{ID: "R3", Text: "the docs read well"}
	cases := []decideCase{
		{
			"no requirement names an item ends passed with the note",
			nodeWith("", false),
			Rows{Epic: store.EpicRow{Requirements: []store.EpicRequirement{bare}}},
			Action{Kind: ActEnd, Outcome: "passed", Output: "no Requirement names an evidence item"},
		},
		{
			"no requirements end passed with the note",
			nodeWith("", false),
			Rows{},
			Action{Kind: ActEnd, Outcome: "passed", Output: "no Requirement names an evidence item"},
		},
		{
			"paused epic without a revision gives none",
			nodeWith("", false),
			Rows{Epic: store.EpicRow{Paused: true, Requirements: []store.EpicRequirement{r1}}},
			Action{Kind: ActNone},
		},
		{
			"requirement with an item and no revision gives prepare",
			nodeWith("", false),
			Rows{Epic: store.EpicRow{Requirements: []store.EpicRequirement{r1}}},
			Action{Kind: ActPrepare},
		},
		{
			"missing run starts with its id and item",
			nodeWith(testRev, false),
			Rows{Epic: store.EpicRow{Requirements: []store.EpicRequirement{r1}}},
			Action{Kind: ActStart, IDs: []string{workID("R1")}, Items: []string{"R1"}},
		},
		{
			"missing runs start in contract order",
			nodeWith(testRev, false),
			Rows{Epic: store.EpicRow{Requirements: []store.EpicRequirement{r2, bare, r1}}},
			Action{Kind: ActStart, IDs: []string{workID("R2"), workID("R1")}, Items: []string{"R2", "R1"}},
		},
		{
			"only the missing run starts when another exists",
			nodeWith(testRev, false),
			Rows{
				Epic:     store.EpicRow{Requirements: []store.EpicRequirement{r1, r2}},
				Evidence: []store.EvidenceRun{evidenceRun("R1", "red", true)},
			},
			Action{Kind: ActStart, IDs: []string{workID("R2")}, Items: []string{"R2"}},
		},
		{
			"run of another attempt does not count",
			nodeWith(testRev, false),
			Rows{
				Epic: store.EpicRow{Requirements: []store.EpicRequirement{r1}},
				Evidence: []store.EvidenceRun{{
					ID: store.NodeWorkID(testRunID, 2, "R1"), Item: "R1", State: "ended", Passed: true,
				}},
			},
			Action{Kind: ActStart, IDs: []string{workID("R1")}, Items: []string{"R1"}},
		},
		{
			"run not ended gives none",
			nodeWith(testRev, false),
			Rows{
				Epic: store.EpicRow{Requirements: []store.EpicRequirement{r1}},
				Evidence: []store.EvidenceRun{{
					ID: workID("R1"), Item: "R1", State: "capturing",
				}},
			},
			Action{Kind: ActNone},
		},
		{
			"ended runs with refs not checked give refs",
			nodeWith(testRev, false),
			Rows{
				Epic:     store.EpicRow{Requirements: []store.EpicRequirement{r1}},
				Evidence: []store.EvidenceRun{evidenceRun("R1", "red", true)},
			},
			Action{Kind: ActRefs},
		},
		{
			"every run passed ends passed with a line per Requirement",
			nodeWith(testRev, true),
			Rows{
				Epic: store.EpicRow{Requirements: []store.EpicRequirement{r1, bare, r2}},
				Evidence: []store.EvidenceRun{
					evidenceRun("R1", "red", true),
					evidenceRun("R2", "green", true),
				},
			},
			Action{Kind: ActEnd, Outcome: "passed", Output: "R1 red: passed\nR2 green: passed"},
		},
		{
			"a run that did not pass ends failed with its line",
			nodeWith(testRev, true),
			Rows{
				Epic: store.EpicRow{Requirements: []store.EpicRequirement{r1, r2}},
				Evidence: []store.EvidenceRun{
					evidenceRun("R1", "red", true),
					evidenceRun("R2", "green", false),
				},
			},
			Action{Kind: ActEnd, Outcome: "failed", Output: "R1 red: passed\nR2 green: failed"},
		},
	}
	checkDecisions(t, Evidence{}, cases)
}

// ownedNode returns an owned node run whose changed paths are result, owning paths.
func ownedNode(result string, paths ...string) Node {
	return Node{
		Def: workflow.Node{Settings: &workflow.Settings{Paths: paths}},
		Run: store.NodeRun{ID: testRunID, Attempt: 1, Result: result},
	}
}

func TestOwnedDecide(t *testing.T) {
	t.Parallel()
	cases := []decideCase{
		{"no result starts", ownedNode("", "go.mod"), Rows{}, Action{Kind: ActStart}},
		{
			"changes under a directory entry and a file entry pass",
			ownedNode(`["internal/kinds/kinds.go","go.mod"]`, "internal/kinds/", "go.mod"),
			Rows{},
			Action{Kind: ActEnd, Outcome: "passed"},
		},
		{"an empty change list passes", ownedNode("[]", "go.mod"), Rows{}, Action{Kind: ActEnd, Outcome: "passed"}},
		{
			"a changed file outside the paths fails listing it",
			ownedNode(`["internal/kinds/kinds.go","README.md"]`, "internal/kinds/"),
			Rows{},
			Action{Kind: ActEnd, Outcome: "failed", Output: "README.md"},
		},
		{
			"a directory entry does not cover a sibling sharing its prefix",
			ownedNode(`["internal/kindsx/a.go"]`, "internal/kinds/"),
			Rows{},
			Action{Kind: ActEnd, Outcome: "failed", Output: "internal/kindsx/a.go"},
		},
		{
			"a file entry covers only that file",
			ownedNode(`["go.mod.bak","go.mod"]`, "go.mod"),
			Rows{},
			Action{Kind: ActEnd, Outcome: "failed", Output: "go.mod.bak"},
		},
		{
			"every outside path is listed in change order",
			ownedNode(`["b/1","a/2","c/3"]`, "a/"),
			Rows{},
			Action{Kind: ActEnd, Outcome: "failed", Output: "b/1\nc/3"},
		},
	}
	checkDecisions(t, Owned{}, cases)
}

// TestOwnedMalformedResultFails pins that a result which is not a JSON list of paths ends failed, never passed.
func TestOwnedMalformedResultFails(t *testing.T) {
	t.Parallel()
	got := Owned{}.Decide(ownedNode("not json", "go.mod"), Rows{})
	malformed := strings.HasPrefix(got.Output, "owned result is not a JSON list")
	if got.Kind != ActEnd || got.Outcome != "failed" || !malformed {
		t.Errorf("Decide() = %+v, want an end failed on a malformed result", got)
	}
}

func TestAgentDecide(t *testing.T) {
	t.Parallel()
	cases := []decideCase{
		{
			"paused epic without a revision gives none",
			nodeWith("", false),
			Rows{Epic: store.EpicRow{Paused: true}},
			Action{Kind: ActNone},
		},
		{"no revision gives prepare", nodeWith("", false), Rows{}, Action{Kind: ActPrepare}},
		{"no step starts", nodeWith(testRev, false), Rows{}, Action{Kind: ActStart}},
		{
			"step of another attempt does not count",
			nodeWith(testRev, false),
			Rows{Steps: []store.StepRow{{ID: store.NodeWorkID(testRunID, 2, "step"), End: "done"}}},
			Action{Kind: ActStart},
		},
		{
			"running step gives none",
			nodeWith(testRev, false),
			Rows{Steps: []store.StepRow{{ID: workID("step")}}},
			Action{Kind: ActNone},
		},
		{
			"done step ends passed with its result",
			nodeWith(testRev, false),
			Rows{Steps: []store.StepRow{{ID: workID("step"), End: "done", Result: `{"status":"done"}`}}},
			Action{Kind: ActEnd, Outcome: "passed", Output: `done: {"status":"done"}`},
		},
		{
			"step that ended otherwise fails with its reason",
			nodeWith(testRev, false),
			Rows{Steps: []store.StepRow{{ID: workID("step"), End: "returned", Reason: "gate failed"}}},
			Action{Kind: ActEnd, Outcome: "failed", Output: "returned: gate failed"},
		},
	}
	checkDecisions(t, Agent{}, cases)
}
