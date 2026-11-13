package server

import (
	"testing"

	"google.golang.org/protobuf/proto"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

func TestSessionItem(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		event   store.EngineEvent
		want    *orreryv1.ConversationItem
		wantOK  bool
		wantErr bool
	}{
		{
			name: "prompt is a prompt item with its text",
			event: store.EngineEvent{
				Seq:     1,
				Kind:    store.EventEnginePrompt,
				At:      100,
				Payload: `{"text":"fix the build","promptId":"p1"}`,
			},
			want:   &orreryv1.ConversationItem{Kind: "prompt", Time: 100, Text: "fix the build"},
			wantOK: true,
		},
		{
			name: "assistant text is a reply",
			event: store.EngineEvent{
				Seq:     2,
				Kind:    store.EventEngineBlock,
				At:      101,
				Payload: `{"kind":"text","role":"assistant","text":"Done."}`,
			},
			want:   &orreryv1.ConversationItem{Kind: "reply", Time: 101, Text: "Done."},
			wantOK: true,
		},
		{
			name: "user-role text makes no item",
			event: store.EngineEvent{
				Seq:     3,
				Kind:    store.EventEngineBlock,
				At:      102,
				Payload: `{"kind":"text","role":"user","text":"hi"}`,
			},
			wantOK: false,
		},
		{
			name: "sub-agent text makes no item",
			event: store.EngineEvent{
				Seq:     4,
				Kind:    store.EventEngineBlock,
				At:      103,
				Payload: `{"kind":"text","role":"assistant","parentToolUseId":"toolu_agent","text":"inner"}`,
			},
			wantOK: false,
		},
		{
			name: "sub-agent tool call makes no item",
			event: store.EngineEvent{
				Seq:     5,
				Kind:    store.EventEngineBlock,
				At:      104,
				Payload: `{"kind":"tool_call","role":"assistant","parentToolUseId":"toolu_agent","toolUseId":"toolu_1","name":"Read","input":"{\"file_path\":\"/x\"}"}`,
			},
			wantOK: false,
		},
		{
			name: "thinking makes no item",
			event: store.EngineEvent{
				Seq:     6,
				Kind:    store.EventEngineBlock,
				At:      105,
				Payload: `{"kind":"thinking","role":"assistant","text":"hmm"}`,
			},
			wantOK: false,
		},
		{
			name: "tool call is a tool item whose input is its display text",
			event: store.EngineEvent{
				Seq:     7,
				Kind:    store.EventEngineBlock,
				At:      106,
				Payload: `{"kind":"tool_call","role":"assistant","toolUseId":"toolu_1","name":"Bash","input":"{\"command\":\"go test ./...\"}"}`,
			},
			want: &orreryv1.ConversationItem{
				Kind:  "tool",
				Time:  106,
				Tool:  "Bash",
				Input: "go test ./...",
				Id:    "toolu_1",
			},
			wantOK: true,
		},
		{
			name: "tool result is a result item with its error flag",
			event: store.EngineEvent{
				Seq:     8,
				Kind:    store.EventEngineBlock,
				At:      107,
				Payload: `{"kind":"tool_result","role":"user","toolUseId":"toolu_1","content":"exit 1","isError":true}`,
			},
			want:   &orreryv1.ConversationItem{Kind: "result", Time: 107, Id: "toolu_1", Text: "exit 1", Error: true},
			wantOK: true,
		},
		{
			name: "turn end makes no item",
			event: store.EngineEvent{
				Seq:     9,
				Kind:    store.EventEngineTurnEnded,
				At:      108,
				Payload: `{"subtype":"success","result":"ok"}`,
			},
			wantOK: false,
		},
		{
			name:    "payload that is not JSON is an error",
			event:   store.EngineEvent{Seq: 10, Kind: store.EventEnginePrompt, At: 109, Payload: `not json`},
			wantErr: true,
		},
		{
			name:    "block payload that is not JSON is an error",
			event:   store.EngineEvent{Seq: 11, Kind: store.EventEngineBlock, At: 110, Payload: `not json`},
			wantErr: true,
		},
		{
			name: "engine send-back prompt is a prompt item with its sender and round",
			event: store.EngineEvent{
				Seq:     12,
				Kind:    store.EventEnginePrompt,
				At:      111,
				Payload: `{"text":"fix the build","promptId":"p1","sender":"engine","round":2}`,
			},
			want: &orreryv1.ConversationItem{
				Kind:   "prompt",
				Time:   111,
				Text:   "fix the build",
				Sender: "engine",
				Round:  2,
			},
			wantOK: true,
		},
		{
			name: "brief prompt is a prompt item with its sender and round 0",
			event: store.EngineEvent{
				Seq:     13,
				Kind:    store.EventEnginePrompt,
				At:      112,
				Payload: `{"text":"build the thing","sender":"brief"}`,
			},
			want:   &orreryv1.ConversationItem{Kind: "prompt", Time: 112, Text: "build the thing", Sender: "brief"},
			wantOK: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok, err := sessionItem(tt.event)
			if (err != nil) != tt.wantErr {
				t.Fatalf("sessionItem error = %v, want error %v", err, tt.wantErr)
			}
			if ok != tt.wantOK {
				t.Fatalf("sessionItem ok = %v, want %v", ok, tt.wantOK)
			}
			if tt.want == nil {
				if got != nil {
					t.Fatalf("sessionItem item = %v, want nil", got)
				}
				return
			}
			if !proto.Equal(got, tt.want) {
				t.Fatalf("sessionItem item = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSessionRequest(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		payload string
		want    *orreryv1.OpenRequest
		wantOK  bool
		wantErr bool
	}{
		{
			name:    "permission request offers allow and deny",
			payload: `{"kind":"permission","requestId":"req-1","toolUseId":"toolu_1","toolName":"Bash","input":"{\"command\":\"rm x\"}"}`,
			want: &orreryv1.OpenRequest{
				Id:       "req-1",
				Kind:     "permission",
				Question: "Bash",
				Options:  []string{"allow", "deny"},
			},
			wantOK: true,
		},
		{
			name:    "one single-select question offers its labels in order",
			payload: `{"kind":"question","requestId":"req-2","input":"{\"questions\":[{\"question\":\"Which db?\",\"multiSelect\":false,\"options\":[{\"label\":\"sqlite\"},{\"label\":\"postgres\"}]}]}"}`,
			want: &orreryv1.OpenRequest{
				Id:       "req-2",
				Kind:     "question",
				Question: "Which db?",
				Options:  []string{"sqlite", "postgres"},
			},
			wantOK: true,
		},
		{
			name:    "one multiSelect question shows its text and no options",
			payload: `{"kind":"question","requestId":"req-3","input":"{\"questions\":[{\"question\":\"Which tags?\",\"multiSelect\":true,\"options\":[{\"label\":\"a\"},{\"label\":\"b\"}]}]}"}`,
			want:    &orreryv1.OpenRequest{Id: "req-3", Kind: "question", Question: "Which tags?"},
			wantOK:  true,
		},
		{
			name:    "two questions show the first text and no options",
			payload: `{"kind":"question","requestId":"req-4","input":"{\"questions\":[{\"question\":\"First?\",\"options\":[{\"label\":\"x\"}]},{\"question\":\"Second?\",\"options\":[{\"label\":\"y\"}]}]}"}`,
			want:    &orreryv1.OpenRequest{Id: "req-4", Kind: "question", Question: "First?"},
			wantOK:  true,
		},
		{
			name:    "question input that does not decode shows an empty question and no options",
			payload: `{"kind":"question","requestId":"req-5","input":"not json"}`,
			want:    &orreryv1.OpenRequest{Id: "req-5", Kind: "question"},
			wantOK:  true,
		},
		{
			name:    "refused request makes no open request",
			payload: `{"kind":"permission","requestId":"req-6","toolName":"Bash","refused":true}`,
			wantOK:  false,
		},
		{
			name:    "kind other makes no open request",
			payload: `{"kind":"other","requestId":"req-7","subtype":"hook_callback"}`,
			wantOK:  false,
		},
		{
			name:    "payload that is not JSON is an error",
			payload: `not json`,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok, err := sessionRequest(tt.payload)
			if (err != nil) != tt.wantErr {
				t.Fatalf("sessionRequest error = %v, want error %v", err, tt.wantErr)
			}
			if ok != tt.wantOK {
				t.Fatalf("sessionRequest ok = %v, want %v", ok, tt.wantOK)
			}
			if tt.want == nil {
				if got != nil {
					t.Fatalf("sessionRequest request = %v, want nil", got)
				}
				return
			}
			if !proto.Equal(got, tt.want) {
				t.Fatalf("sessionRequest request = %v, want %v", got, tt.want)
			}
		})
	}
}
