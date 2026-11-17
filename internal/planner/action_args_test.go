package planner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// actionPlan returns a planner return holding one action of kind with args written as a JSON string.
func actionPlan(kind, args string) json.RawMessage {
	encoded, err := json.Marshal(args)
	if err != nil {
		panic(err)
	}
	return json.RawMessage(`{"status":"done","decisions":[],"tasks":[],"outline":"o","questions":[],` +
		`"actions":[{"kind":"` + kind + `","args":` + string(encoded) + `,"reason":"r"}],"outcome":"wait"}`)
}

func TestActionArgsAreReadIntoTheActionsFields(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind, args string
		want       EngineAction
	}{
		{
			store.ActionRetryNode,
			`{"node":"plan/integrate"}`,
			EngineAction{Kind: store.ActionRetryNode, Node: "plan/integrate"},
		},
		{store.ActionFailTask, `{"task":3}`, EngineAction{Kind: store.ActionFailTask, Task: 3}},
		{store.ActionCloseEpic, `{"close":"superseded","by":"repo/other"}`,
			EngineAction{Kind: store.ActionCloseEpic, Close: "superseded", By: "repo/other"}},
		{
			store.ActionResumeRelease,
			`{"undo_none":[2]}`,
			EngineAction{Kind: store.ActionResumeRelease, UndoNone: []int64{2}},
		},
		{store.ActionRetryEpic, ``, EngineAction{Kind: store.ActionRetryEpic}},
		{store.ActionRetryEpic, `{}`, EngineAction{Kind: store.ActionRetryEpic}},
	}
	for _, tc := range cases {
		ret, err := Parse(actionPlan(tc.kind, tc.args))
		if err != nil {
			t.Fatalf("Parse(%s %s): %v", tc.kind, tc.args, err)
		}
		got := ret.Actions[0]
		got.Reason = ""
		if got.Kind != tc.want.Kind || got.Node != tc.want.Node || got.Task != tc.want.Task ||
			got.Close != tc.want.Close || got.By != tc.want.By || !slices.Equal(got.UndoNone, tc.want.UndoNone) ||
			got.ArgsError != "" || len(got.Unknown) != 0 {
			t.Errorf("Parse(%s %s) action = %+v, want %+v", tc.kind, tc.args, got, tc.want)
		}
	}
}

func TestActionArgsThatDoNotReadAreRefusedByCheck(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind, args, want string
	}{
		{store.ActionRetryNode, `plan/integrate`, `actions[0] "retry_node" args: not a JSON object`},
		{store.ActionFailTask, `{"task":"three"}`, `actions[0] "fail_task" args: `},
		{store.ActionRetryNode, `{"node":"n","nodes":"x"}`, `actions[0] "retry_node" args: no kind uses the key nodes`},
		{store.ActionRetryNode, `{"task":2,"node":"n"}`, `actions[0] "retry_node" task: ` + reasonUnused},
	}
	for _, tc := range cases {
		ret, err := Parse(actionPlan(tc.kind, tc.args))
		if err != nil {
			t.Fatalf("Parse(%s %s): %v, want the bad args left to Check", tc.kind, tc.args, err)
		}
		refusals := checkAction(0, ret.Actions[0])
		if !slices.ContainsFunc(refusals, func(line string) bool { return strings.HasPrefix(line, tc.want) }) {
			t.Errorf("checkAction(%s %s) = %q, want a line starting %q", tc.kind, tc.args, refusals, tc.want)
		}
	}
}

// TestPlannerSchemaIsTheOneTheAPIAccepted pins the planner schema to testdata/schema_accepted.json, a schema measured as
// accepted by the Anthropic API. The API refuses a strict tool whose compiled grammar is too large ("400 The compiled
// grammar is too large ... Simplify your tool schemas"), and Claude Code's structured output is such a tool, so an
// over-large schema fails every planner turn before the model runs. The grammar's size does not follow a simple count of
// properties: on 2026-11-17 a schema whose actions held seven fields was refused with and without its enums, while this
// one, whose actions hold kind, args and reason, was accepted by haiku and opus. Grow the return through string fields the
// engine parses, as args does. To change the schema, first measure it:
// claude -p --model haiku --output-format json --json-schema "$(cat schema.json)" 'Return a minimal valid object.'
// must not report is_error with that 400; then update the testdata file.
func TestPlannerSchemaIsTheOneTheAPIAccepted(t *testing.T) {
	t.Parallel()
	var got, want any
	if err := json.Unmarshal([]byte(Schema), &got); err != nil {
		t.Fatalf("Schema is not JSON: %v", err)
	}
	data, err := os.ReadFile(filepath.Join("testdata", "schema_accepted.json"))
	if err != nil {
		t.Fatalf("read the accepted schema: %v", err)
	}
	if err = json.Unmarshal(data, &want); err != nil {
		t.Fatalf("the accepted schema is not JSON: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("planner Schema differs from testdata/schema_accepted.json; measure the new schema against the API " +
			"(see this test's comment) before updating the file")
	}
}
