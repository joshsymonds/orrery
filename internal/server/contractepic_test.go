package server

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/conversation"
	"github.com/joshsymonds/orrery/internal/store"
)

// TestContractEpicSessionItemNamesTheWrittenContract checks that an engine transcript's tool item of a
// contract_propose or contract_revise call carries the call's contract id, past the input clip, and that another contract
// tool's item carries none.
func TestContractEpicSessionItemNamesTheWrittenContract(t *testing.T) {
	t.Parallel()
	// The contract id comes after more than 200 runes of compact input, so the display text clips it away.
	longInput := `{"path":"` + strings.Repeat("p", 250) + `","id":"demo-0a1b2c3/contract-long"}`
	tests := []struct {
		name  string
		tool  string
		input string
		want  string
	}{
		{
			name:  "mcp contract_propose names its id past the input clip",
			tool:  "mcp__orrery__contract_propose",
			input: longInput,
			want:  "demo-0a1b2c3/contract-long",
		},
		{
			name:  "mcp contract_revise names its id",
			tool:  "mcp__orrery__contract_revise",
			input: `{"id":"demo-0a1b2c3/contract-login","path":"contract.json"}`,
			want:  "demo-0a1b2c3/contract-login",
		},
		{
			name:  "mcp contract_accept names no contract",
			tool:  "mcp__orrery__contract_accept",
			input: `{"id":"demo-0a1b2c3/contract-login"}`,
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			payload, err := json.Marshal(map[string]string{
				"kind":      "tool_call",
				"role":      "assistant",
				"toolUseId": "toolu_1",
				"name":      tt.tool,
				"input":     tt.input,
			})
			if err != nil {
				t.Fatalf("marshal block: %v", err)
			}
			got, ok, err := sessionItem(store.EngineEvent{
				Seq: 1, Kind: store.EventEngineBlock, At: 200, Payload: string(payload),
			})
			if err != nil || !ok {
				t.Fatalf("sessionItem ok = %v, err = %v, want a tool item", ok, err)
			}
			if got.GetKind() != "tool" || got.GetContract() != tt.want {
				t.Fatalf(
					"item kind %q contract %q, want kind %q contract %q",
					got.GetKind(),
					got.GetContract(),
					"tool",
					tt.want,
				)
			}
		})
	}
}

// TestContractEpicSessionItemNamesTheCreatedEpicByID checks that an engine transcript's epic_create item names its epic
// from the call's id input, and, for a call made before epic_create took an id, from its inline contract's id.
func TestContractEpicSessionItemNamesTheCreatedEpicByID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, input string
	}{
		{name: "id input", input: `{"id":"demo-0a1b2c3/epic-login","base":"main"}`},
		{name: "inline contract of an older call", input: `{"contract":{"id":"demo-0a1b2c3/epic-login"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			payload, err := json.Marshal(map[string]string{
				"kind":      "tool_call",
				"role":      "assistant",
				"toolUseId": "toolu_1",
				"name":      "mcp__orrery__epic_create",
				"input":     tt.input,
			})
			if err != nil {
				t.Fatalf("marshal block: %v", err)
			}
			got, ok, err := sessionItem(
				store.EngineEvent{Seq: 1, Kind: store.EventEngineBlock, At: 200, Payload: string(payload)},
			)
			if err != nil || !ok {
				t.Fatalf("sessionItem ok = %v, err = %v, want a tool item", ok, err)
			}
			if got.GetEpic() != "demo-0a1b2c3/epic-login" || got.GetContract() != "" {
				t.Fatalf("item epic %q contract %q, want epic %q and no contract",
					got.GetEpic(), got.GetContract(), "demo-0a1b2c3/epic-login")
			}
		})
	}
}

// TestContractEpicConversationItemsCopiesTheContract checks that a pane transcript's item carries its contract to the
// wire, and that an item with no contract keeps it empty.
func TestContractEpicConversationItemsCopiesTheContract(t *testing.T) {
	t.Parallel()
	items := conversationItems([]conversation.Item{
		{
			Kind:     "tool",
			Tool:     "mcp__orrery__contract_propose",
			Contract: "demo-0a1b2c3/contract-login",
		},
		{Kind: "tool", Tool: "Bash"},
	})
	want := []string{"demo-0a1b2c3/contract-login", ""}
	got := make([]string, 0, len(items))
	for _, item := range items {
		got = append(got, item.GetContract())
	}
	if !slices.Equal(got, want) {
		t.Fatalf("contracts = %q, want %q", got, want)
	}
}
