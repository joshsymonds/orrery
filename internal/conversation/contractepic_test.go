package conversation

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestContractEpicReadNamesTheWrittenContract checks that a pane transcript's contract_propose and contract_revise
// items carry their input's id as Contract, past the input clip, and that a contract tool that writes no version
// carries none.
func TestContractEpicReadNamesTheWrittenContract(t *testing.T) {
	longInput := `{"path":"` + strings.Repeat("p", 250) + `","id":"demo-0a1b2c3/contract-long"}`
	path := writeTranscript(t,
		`{"type":"assistant","timestamp":"`+stampAt(1)+`","message":{"content":[`+
			`{"type":"tool_use","id":"toolu_1","name":"mcp__orrery__contract_propose","input":`+longInput+`}]}}`,
		`{"type":"assistant","timestamp":"`+stampAt(2)+`","message":{"content":[`+
			`{"type":"tool_use","id":"toolu_2","name":"mcp__orrery__contract_revise","input":{"id":"demo-0a1b2c3/contract-login"}}]}}`,
		`{"type":"assistant","timestamp":"`+stampAt(3)+`","message":{"content":[`+
			`{"type":"tool_use","id":"toolu_3","name":"mcp__orrery__contract_accept","input":{"id":"demo-0a1b2c3/contract-login"}}]}}`,
	)

	type named struct{ tool, contract string }
	conv := mustRead(t, path, 10)
	got := make([]named, 0, len(conv.Items))
	for _, item := range conv.Items {
		got = append(got, named{item.Tool, item.Contract})
	}
	want := []named{
		{"mcp__orrery__contract_propose", "demo-0a1b2c3/contract-long"},
		{"mcp__orrery__contract_revise", "demo-0a1b2c3/contract-login"},
		{"mcp__orrery__contract_accept", ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tool items = %+v, want %+v", got, want)
	}
}

// TestContractEpicReadNamesTheCreatedEpicFromEitherForm checks that a pane transcript's epic_create item names its epic
// from the call's id input, and, for a call made before epic_create took an id, from its inline contract's id, and that
// neither carries a contract.
func TestContractEpicReadNamesTheCreatedEpicFromEitherForm(t *testing.T) {
	path := writeTranscript(t,
		`{"type":"assistant","timestamp":"`+stampAt(1)+`","message":{"content":[`+
			`{"type":"tool_use","id":"toolu_1","name":"mcp__orrery__epic_create","input":{"id":"demo-0a1b2c3/epic-new","base":"main"}}]}}`,
		`{"type":"assistant","timestamp":"`+stampAt(2)+`","message":{"content":[`+
			`{"type":"tool_use","id":"toolu_2","name":"mcp__orrery__epic_create","input":{"contract":{"id":"demo-0a1b2c3/epic-old"}}}]}}`,
	)

	type named struct{ epic, contract string }
	conv := mustRead(t, path, 10)
	got := make([]named, 0, len(conv.Items))
	for _, item := range conv.Items {
		got = append(got, named{item.Epic, item.Contract})
	}
	want := []named{{"demo-0a1b2c3/epic-new", ""}, {"demo-0a1b2c3/epic-old", ""}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("epic_create items = %+v, want %+v", got, want)
	}
}

// TestContractEpicWrittenContractByToolName checks that only an MCP server's contract_propose or contract_revise call
// names a contract, and that an input naming no id, or one that does not decode, names none.
func TestContractEpicWrittenContractByToolName(t *testing.T) {
	input := json.RawMessage(`{"id":"demo-0a1b2c3/contract-login","path":"contract.json"}`)
	tests := []struct {
		name  string
		tool  string
		input json.RawMessage
		want  string
	}{
		{
			"mcp contract_propose names its id",
			"mcp__orrery__contract_propose",
			input,
			"demo-0a1b2c3/contract-login",
		},
		{
			"mcp contract_revise names its id",
			"mcp__orrery__contract_revise",
			input,
			"demo-0a1b2c3/contract-login",
		},
		{
			"mcp server name with separators names its id",
			"mcp__my__server__contract_revise",
			input,
			"demo-0a1b2c3/contract-login",
		},
		{"bare contract_propose names none", "contract_propose", input, ""},
		{"bare contract_revise names none", "contract_revise", input, ""},
		{"mcp contract_accept names none", "mcp__orrery__contract_accept", input, ""},
		{"mcp contract_get names none", "mcp__orrery__contract_get", input, ""},
		{
			"mcp contract_propose with no id",
			"mcp__orrery__contract_propose",
			json.RawMessage(`{"path":"x"}`),
			"",
		},
		{"mcp contract_propose with input that does not decode", "mcp__orrery__contract_propose",
			json.RawMessage(`not json`), ""},
		{"mcp name with no server", "mcp__contract_propose", input, ""},
		{"another tool with an id", "Bash", input, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := WrittenContract(tt.tool, tt.input); got != tt.want {
				t.Fatalf("WrittenContract(%q) = %q, want %q", tt.tool, got, tt.want)
			}
		})
	}
}

// TestContractEpicCreatedEpicByIDOrInlineContract checks that an epic_create call names its epic from its id input,
// and from its inline contract's id for a call made before epic_create took an id, with the id input first.
func TestContractEpicCreatedEpicByIDOrInlineContract(t *testing.T) {
	tests := []struct {
		name  string
		input json.RawMessage
		want  string
	}{
		{
			"id input",
			json.RawMessage(`{"id":"demo-0a1b2c3/epic-login","base":"main"}`),
			"demo-0a1b2c3/epic-login",
		},
		{
			"inline contract id from a call made before this release",
			json.RawMessage(`{"contract":{"id":"demo-0a1b2c3/epic-login"}}`),
			"demo-0a1b2c3/epic-login",
		},
		{
			"id input takes precedence over an inline contract id",
			json.RawMessage(
				`{"id":"demo-0a1b2c3/epic-new","contract":{"id":"demo-0a1b2c3/epic-old"}}`,
			),
			"demo-0a1b2c3/epic-new",
		},
		{
			"empty id input falls back to the inline contract id",
			json.RawMessage(`{"id":"","contract":{"id":"demo-0a1b2c3/epic-old"}}`),
			"demo-0a1b2c3/epic-old",
		},
		{"id input with no id names no epic", json.RawMessage(`{"base":"main"}`), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreatedEpic("mcp__orrery__epic_create", tt.input); got != tt.want {
				t.Fatalf("CreatedEpic = %q, want %q", got, tt.want)
			}
		})
	}
}
