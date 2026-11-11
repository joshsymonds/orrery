package conversation

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// assistantUsageLine returns a main-chain assistant line at second s with one text block and the given usage
// object. An empty usage omits the usage key.
func assistantUsageLine(s int, usage string) string {
	message := `{"content":[{"type":"text","text":"synthetic reply"}]`
	if usage != "" {
		message += `,"usage":` + usage
	}
	return `{"type":"assistant","timestamp":"` + stampAt(s) + `","message":` + message + `}}`
}

// boundaryWithPost returns a main-chain compact_boundary line at second s with the given pre- and post-compaction
// token counts.
func boundaryWithPost(s int, pre, post string) string {
	return `{"type":"system","subtype":"compact_boundary","timestamp":"` + stampAt(s) +
		`","compactMetadata":{"trigger":"auto","preTokens":` + pre + `,"postTokens":` + post + `}}`
}

func TestReadContextSkipsZeroUsageLine(t *testing.T) {
	tests := []struct {
		name  string
		newer string
	}{
		{"all counts zero", `{"input_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}`},
		{"usage key absent", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTranscript(t,
				assistantUsageLine(0, `{"input_tokens":7}`),
				assistantUsageLine(1, tc.newer),
			)
			if got := mustRead(t, path, 10).Context.Tokens; got != 7 {
				t.Fatalf("Context.Tokens = %d, want 7 from the older line", got)
			}
		})
	}
}

func TestReadContextSumsUsageKeys(t *testing.T) {
	tests := []struct {
		name  string
		usage string
		want  int64
	}{
		{"input and cache read only", `{"input_tokens":5,"cache_read_input_tokens":10}`, 15},
		{
			"input, cache creation and cache read",
			`{"input_tokens":1,"cache_creation_input_tokens":2,"cache_read_input_tokens":4}`,
			7,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTranscript(t, assistantUsageLine(0, tc.usage))
			if got := mustRead(t, path, 10).Context.Tokens; got != tc.want {
				t.Fatalf("Context.Tokens = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestReadContextBoundaryPostTokensWhenNewest(t *testing.T) {
	path := writeTranscript(t,
		assistantUsageLine(0, `{"input_tokens":100}`),
		boundaryWithPost(1, "180000", "42"),
	)
	if got := mustRead(t, path, 10).Context.Tokens; got != 42 {
		t.Fatalf("Context.Tokens = %d, want 42 from the newer boundary", got)
	}
}

func TestReadContextAssistantAfterBoundaryWins(t *testing.T) {
	path := writeTranscript(t,
		boundaryWithPost(0, "180000", "42"),
		assistantUsageLine(1, `{"input_tokens":9}`),
	)
	if got := mustRead(t, path, 10).Context.Tokens; got != 9 {
		t.Fatalf("Context.Tokens = %d, want 9 from the newer assistant line", got)
	}
}

func TestReadContextFoundBeyondItemLimit(t *testing.T) {
	path := writeTranscript(t,
		assistantUsageLine(0, `{"input_tokens":77}`),
		assistantUsageLine(1, `{"input_tokens":0}`),
		assistantUsageLine(2, `{"input_tokens":0}`),
		assistantUsageLine(3, `{"input_tokens":0}`),
	)
	conv := mustRead(t, path, 2)
	if conv.Context.Tokens != 77 {
		t.Fatalf("Context.Tokens = %d, want 77 from a line beyond the item limit", conv.Context.Tokens)
	}
	if len(conv.Items) != 2 {
		t.Fatalf("Items has %d entries, want 2: %+v", len(conv.Items), conv.Items)
	}
}

func TestReadContextIgnoresSidechainAndMetaLines(t *testing.T) {
	path := writeTranscript(t,
		assistantUsageLine(0, `{"input_tokens":30}`),
		`{"type":"assistant","isSidechain":true,"timestamp":"`+stampAt(1)+
			`","message":{"content":[{"type":"text","text":"synthetic"}],"usage":{"input_tokens":500}}}`,
		`{"type":"assistant","isMeta":true,"timestamp":"`+stampAt(2)+
			`","message":{"content":[{"type":"text","text":"synthetic"}],"usage":{"input_tokens":600}}}`,
	)
	if got := mustRead(t, path, 10).Context.Tokens; got != 30 {
		t.Fatalf("Context.Tokens = %d, want 30 from the main-chain line", got)
	}
}

func TestReadContextNoQualifyingLineGivesZeroWithWindow(t *testing.T) {
	path := writeTranscript(t,
		`{"type":"user","timestamp":"`+stampAt(0)+`","message":{"content":"synthetic prompt"}}`,
		assistantUsageLine(1, `{"input_tokens":0}`),
	)
	want := Context{Tokens: 0, Window: 1000000}
	if got := mustRead(t, path, 10).Context; got != want {
		t.Fatalf("Context = %+v, want %+v", got, want)
	}
}

func TestConversationJSONHasContextKeys(t *testing.T) {
	path := writeTranscript(t, assistantUsageLine(0, `{"input_tokens":12}`))
	raw, err := json.Marshal(mustRead(t, path, 10))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var body struct {
		Context map[string]int64 `json:"context"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := map[string]int64{"tokens": 12, "window": 1000000}
	if !reflect.DeepEqual(body.Context, want) {
		t.Fatalf("context = %v, want %v in %s", body.Context, want, strings.TrimSpace(string(raw)))
	}
}
