package enginehost

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/store"
)

// recorderID is the session id every recorder in these tests carries.
const recorderID = "orrery-session-1"

// step is one event fed to a recorder and the ops it must yield, in order.
type step struct {
	event session.Event
	want  []store.EngineOp
}

// strPtr returns a pointer to a copy of s, for the optional fields of an EngineChanges.
func strPtr(s string) *string {
	return &s
}

func TestRecorderOps(t *testing.T) {
	cases := []struct {
		name  string
		steps []step
	}{
		{
			name: "started sets the claude session only when it changes",
			steps: []step{
				{
					session.Started{SessionID: "claude-1", PermissionMode: "default"},
					[]store.EngineOp{
						store.EngineSet{
							ID:      recorderID,
							Changes: store.EngineChanges{ClaudeSession: strPtr("claude-1")},
						},
					},
				},
				{session.Started{SessionID: "claude-1"}, nil},
				{
					session.Started{SessionID: "claude-2"},
					[]store.EngineOp{
						store.EngineSet{
							ID:      recorderID,
							Changes: store.EngineChanges{ClaudeSession: strPtr("claude-2")},
						},
					},
				},
			},
		},
		{
			name: "text deltas grow one unfinished row with the whole text so far",
			steps: []step{
				{
					session.TextDelta{MessageID: "m1", Index: 0, Text: "Hel"},
					[]store.EngineOp{
						store.EngineText{ID: recorderID, MessageID: "m1", Index: 0, Kind: "text", Text: "Hel"},
					},
				},
				{
					session.TextDelta{MessageID: "m1", Index: 0, Text: "lo"},
					[]store.EngineOp{
						store.EngineText{ID: recorderID, MessageID: "m1", Index: 0, Kind: "text", Text: "Hello"},
					},
				},
			},
		},
		{
			name: "thinking deltas keep the thinking kind",
			steps: []step{
				{
					session.ThinkingDelta{MessageID: "m1", Index: 0, Text: "hmm"},
					[]store.EngineOp{
						store.EngineText{ID: recorderID, MessageID: "m1", Index: 0, Kind: "thinking", Text: "hmm"},
					},
				},
			},
		},
		{
			name: "a finished text block with no streamed text yields only the block",
			steps: []step{
				{
					session.Block{Kind: session.BlockText, Role: "assistant", MessageID: "m1", Text: "hi"},
					[]store.EngineOp{
						store.EngineBlock{ID: recorderID, Kind: "text", Role: "assistant", MessageID: "m1", Text: "hi"},
					},
				},
			},
		},
		{
			name: "a finished block and the removal of its unfinished row come in one batch",
			steps: []step{
				{
					session.TextDelta{MessageID: "m1", Index: 0, Text: "Hel"},
					[]store.EngineOp{
						store.EngineText{ID: recorderID, MessageID: "m1", Index: 0, Kind: "text", Text: "Hel"},
					},
				},
				{
					session.Block{Kind: session.BlockText, Role: "assistant", MessageID: "m1", Text: "Hello"},
					[]store.EngineOp{
						store.EngineBlock{
							ID:        recorderID,
							Kind:      "text",
							Role:      "assistant",
							MessageID: "m1",
							Text:      "Hello",
						},
						store.EngineTextDone{ID: recorderID, MessageID: "m1", Index: 0},
					},
				},
			},
		},
		{
			name: "two interleaved streaming blocks close in index order",
			steps: []step{
				{
					session.TextDelta{MessageID: "m1", Index: 1, Text: "B"},
					[]store.EngineOp{
						store.EngineText{ID: recorderID, MessageID: "m1", Index: 1, Kind: "text", Text: "B"},
					},
				},
				{
					session.TextDelta{MessageID: "m1", Index: 0, Text: "A"},
					[]store.EngineOp{
						store.EngineText{ID: recorderID, MessageID: "m1", Index: 0, Kind: "text", Text: "A"},
					},
				},
				{
					session.TextDelta{MessageID: "m1", Index: 1, Text: "b"},
					[]store.EngineOp{
						store.EngineText{ID: recorderID, MessageID: "m1", Index: 1, Kind: "text", Text: "Bb"},
					},
				},
				{session.Block{Kind: session.BlockText, Role: "assistant", MessageID: "m1", Text: "A!"},
					[]store.EngineOp{
						store.EngineBlock{ID: recorderID, Kind: "text", Role: "assistant", MessageID: "m1", Text: "A!"},
						store.EngineTextDone{ID: recorderID, MessageID: "m1", Index: 0},
					}},
				{
					session.Block{Kind: session.BlockText, Role: "assistant", MessageID: "m1", Text: "Bb!"},
					[]store.EngineOp{
						store.EngineBlock{
							ID:        recorderID,
							Kind:      "text",
							Role:      "assistant",
							MessageID: "m1",
							Text:      "Bb!",
						},
						store.EngineTextDone{ID: recorderID, MessageID: "m1", Index: 1},
					},
				},
				{
					session.Block{Kind: session.BlockText, Role: "assistant", MessageID: "m1", Text: "again"},
					[]store.EngineOp{
						store.EngineBlock{
							ID:        recorderID,
							Kind:      "text",
							Role:      "assistant",
							MessageID: "m1",
							Text:      "again",
						},
					},
				},
			},
		},
		{
			name: "a finished block closes only the open row of its kind, message and parent",
			steps: []step{
				{
					session.ThinkingDelta{MessageID: "m1", Index: 0, Text: "t"},
					[]store.EngineOp{
						store.EngineText{ID: recorderID, MessageID: "m1", Index: 0, Kind: "thinking", Text: "t"},
					},
				},
				{
					session.TextDelta{MessageID: "m1", Index: 1, Text: "x"},
					[]store.EngineOp{
						store.EngineText{ID: recorderID, MessageID: "m1", Index: 1, Kind: "text", Text: "x"},
					},
				},
				{
					session.TextDelta{MessageID: "m1", Index: 0, ParentToolUseID: "tool-9", Text: "s"},
					[]store.EngineOp{
						store.EngineText{
							ID:              recorderID,
							MessageID:       "m1",
							Index:           0,
							ParentToolUseID: "tool-9",
							Kind:            "text",
							Text:            "s",
						},
					},
				},
				{session.Block{Kind: session.BlockText, Role: "assistant", MessageID: "m1", Text: "x!"},
					[]store.EngineOp{
						store.EngineBlock{ID: recorderID, Kind: "text", Role: "assistant", MessageID: "m1", Text: "x!"},
						store.EngineTextDone{ID: recorderID, MessageID: "m1", Index: 1},
					}},
				{
					session.Block{Kind: session.BlockThinking, Role: "assistant", MessageID: "m1", Text: "t!"},
					[]store.EngineOp{
						store.EngineBlock{
							ID:        recorderID,
							Kind:      "thinking",
							Role:      "assistant",
							MessageID: "m1",
							Text:      "t!",
						},
						store.EngineTextDone{ID: recorderID, MessageID: "m1", Index: 0},
					},
				},
				{
					session.Block{
						Kind:            session.BlockText,
						Role:            "assistant",
						MessageID:       "m1",
						ParentToolUseID: "tool-9",
						Text:            "s!",
					},
					[]store.EngineOp{
						store.EngineBlock{
							ID:              recorderID,
							Kind:            "text",
							Role:            "assistant",
							MessageID:       "m1",
							ParentToolUseID: "tool-9",
							Text:            "s!",
						},
						store.EngineTextDone{ID: recorderID, MessageID: "m1", Index: 0, ParentToolUseID: "tool-9"},
					},
				},
			},
		},
		{
			name: "tool call and tool result blocks carry their raw json",
			steps: []step{
				{
					session.Block{
						Kind:      session.BlockToolCall,
						Role:      "assistant",
						MessageID: "m1",
						ToolUseID: "tu1",
						Name:      "Bash",
						Input:     json.RawMessage(`{"command":"ls"}`),
					},
					[]store.EngineOp{
						store.EngineBlock{ID: recorderID, Kind: "tool_call", Role: "assistant", MessageID: "m1",
							ToolUseID: "tu1", Name: "Bash", Input: `{"command":"ls"}`},
					},
				},
				{
					session.Block{Kind: session.BlockToolResult, Role: "user", ToolUseID: "tu1",
						Content: json.RawMessage(`"out"`), IsError: true},
					[]store.EngineOp{
						store.EngineBlock{ID: recorderID, Kind: "tool_result", Role: "user", ToolUseID: "tu1",
							Content: `"out"`, IsError: true},
					},
				},
			},
		},
		{
			name: "a question request waits with its first question as the question",
			steps: []step{
				{
					session.Request{
						Kind:      session.RequestQuestion,
						RequestID: "r1",
						ToolUseID: "tu2",
						ToolName:  "AskUserQuestion",
						Input: json.RawMessage(
							`{"questions":[{"question":"Which branch?"},{"question":"Second?"}]}`,
						),
					},
					[]store.EngineOp{
						store.EngineRequest{
							ID:        recorderID,
							Kind:      "question",
							RequestID: "r1",
							ToolUseID: "tu2",
							ToolName:  "AskUserQuestion",
							Input:     `{"questions":[{"question":"Which branch?"},{"question":"Second?"}]}`,
						},
						store.EngineSet{
							ID:      recorderID,
							Changes: store.EngineChanges{State: strPtr("waiting"), Question: strPtr("Which branch?")},
						},
					},
				},
			},
		},
		{
			name: "a permission request waits with its tool name as the question",
			steps: []step{
				{
					session.Request{
						Kind:      session.RequestPermission,
						RequestID: "r2",
						ToolUseID: "tu3",
						ToolName:  "Bash",
						Subtype:   "can_use_tool",
						Input:     json.RawMessage(`{"command":"rm"}`),
					},
					[]store.EngineOp{
						store.EngineRequest{
							ID:        recorderID,
							Kind:      "permission",
							RequestID: "r2",
							ToolUseID: "tu3",
							ToolName:  "Bash",
							Subtype:   "can_use_tool",
							Input:     `{"command":"rm"}`,
						},
						store.EngineSet{
							ID:      recorderID,
							Changes: store.EngineChanges{State: strPtr("waiting"), Question: strPtr("Bash")},
						},
					},
				},
			},
		},
		{
			name: "an other request waits with an empty question",
			steps: []step{
				{
					session.Request{Kind: session.RequestOther, RequestID: "r3", Subtype: "hook_callback"},
					[]store.EngineOp{
						store.EngineRequest{ID: recorderID, Kind: "other", RequestID: "r3", Subtype: "hook_callback"},
						store.EngineSet{
							ID:      recorderID,
							Changes: store.EngineChanges{State: strPtr("waiting"), Question: strPtr("")},
						},
					},
				},
			},
		},
		{
			name: "a question request with no questions waits with an empty question",
			steps: []step{
				{
					session.Request{
						Kind:      session.RequestQuestion,
						RequestID: "r5",
						Input:     json.RawMessage(`{"questions":[]}`),
					},
					[]store.EngineOp{
						store.EngineRequest{
							ID:        recorderID,
							Kind:      "question",
							RequestID: "r5",
							Input:     `{"questions":[]}`,
						},
						store.EngineSet{
							ID:      recorderID,
							Changes: store.EngineChanges{State: strPtr("waiting"), Question: strPtr("")},
						},
					},
				},
			},
		},
		{
			name: "a refused request is recorded without changing the session state",
			steps: []step{
				{
					session.Request{Kind: session.RequestPermission, RequestID: "r4", ToolName: "Bash", Refused: true},
					[]store.EngineOp{
						store.EngineRequest{
							ID:        recorderID,
							Kind:      "permission",
							RequestID: "r4",
							ToolName:  "Bash",
							Refused:   true,
						},
					},
				},
			},
		},
		{
			name: "a canceled request returns the session to working",
			steps: []step{
				{
					session.RequestCancelled{RequestID: "r1", ToolUseID: "tu2"},
					[]store.EngineOp{
						store.EngineRequestCancelled{ID: recorderID, RequestID: "r1", ToolUseID: "tu2"},
						store.EngineSet{
							ID:      recorderID,
							Changes: store.EngineChanges{State: strPtr("working"), Question: strPtr("")},
						},
					},
				},
			},
		},
		{
			name: "a turn end clears open text, so a later block closes nothing",
			steps: []step{
				{
					session.TextDelta{MessageID: "m1", Index: 0, Text: "partial"},
					[]store.EngineOp{
						store.EngineText{ID: recorderID, MessageID: "m1", Index: 0, Kind: "text", Text: "partial"},
					},
				},
				{
					session.TurnEnded{Subtype: "success", TerminalReason: "completed", Result: "done", Origin: "user"},
					[]store.EngineOp{
						store.EngineTurnEnded{
							ID:             recorderID,
							Subtype:        "success",
							TerminalReason: "completed",
							Result:         "done",
							Origin:         "user",
						},
						store.EngineTextClear{ID: recorderID},
						store.EngineSet{
							ID:      recorderID,
							Changes: store.EngineChanges{State: strPtr("done"), Question: strPtr("")},
						},
					},
				},
				{
					session.Block{Kind: session.BlockText, Role: "assistant", MessageID: "m1", Text: "partial"},
					[]store.EngineOp{
						store.EngineBlock{
							ID:        recorderID,
							Kind:      "text",
							Role:      "assistant",
							MessageID: "m1",
							Text:      "partial",
						},
					},
				},
			},
		},
		{
			name: "a failed turn records its error and ends done",
			steps: []step{
				{
					session.TurnEnded{Subtype: "error_during_execution", IsError: true, TerminalReason: "max_turns"},
					[]store.EngineOp{
						store.EngineTurnEnded{
							ID:             recorderID,
							Subtype:        "error_during_execution",
							IsError:        true,
							TerminalReason: "max_turns",
						},
						store.EngineTextClear{ID: recorderID},
						store.EngineSet{
							ID:      recorderID,
							Changes: store.EngineChanges{State: strPtr("done"), Question: strPtr("")},
						},
					},
				},
			},
		},
		{
			name: "a clean exit stops the session and clears open text",
			steps: []step{
				{
					session.TextDelta{MessageID: "m1", Index: 0, Text: "x"},
					[]store.EngineOp{
						store.EngineText{ID: recorderID, MessageID: "m1", Index: 0, Kind: "text", Text: "x"},
					},
				},
				{session.Exited{},
					[]store.EngineOp{
						store.EngineTextClear{ID: recorderID},
						store.EngineSet{ID: recorderID, Changes: store.EngineChanges{State: strPtr("stopped")}},
					}},
				{
					session.Block{Kind: session.BlockText, Role: "assistant", MessageID: "m1", Text: "x"},
					[]store.EngineOp{
						store.EngineBlock{ID: recorderID, Kind: "text", Role: "assistant", MessageID: "m1", Text: "x"},
					},
				},
			},
		},
		{
			name: "an exit error fails the session with its error and the last stderr line",
			steps: []step{
				{session.Exited{ExitErr: errors.New("exit status 1"), Stderr: "warn\nfatal: bad config\n\n"},
					[]store.EngineOp{
						store.EngineTextClear{ID: recorderID},
						store.EngineSet{ID: recorderID, Changes: store.EngineChanges{State: strPtr("failed"),
							Reason: strPtr("exit status 1: fatal: bad config")}},
					}},
			},
		},
		{
			name: "a stream error fails the session with its error",
			steps: []step{
				{session.Exited{StreamErr: errors.New("read stdout: closed")},
					[]store.EngineOp{
						store.EngineTextClear{ID: recorderID},
						store.EngineSet{ID: recorderID, Changes: store.EngineChanges{State: strPtr("failed"),
							Reason: strPtr("read stdout: closed")}},
					}},
			},
		},
		{
			name: "both exit and stream errors join into the reason",
			steps: []step{
				{session.Exited{ExitErr: errors.New("exit status 1"), StreamErr: errors.New("read stdout: closed")},
					[]store.EngineOp{
						store.EngineTextClear{ID: recorderID},
						store.EngineSet{ID: recorderID, Changes: store.EngineChanges{State: strPtr("failed"),
							Reason: strPtr("exit status 1\nread stdout: closed")}},
					}},
			},
		},
		{
			name: "events the store does not record yield nothing",
			steps: []step{
				{session.ToolCallStarted{MessageID: "m1", Index: 0, ToolUseID: "tu1", Name: "Bash"}, nil},
				{session.Notice{Kind: session.NoticeAPIRetry, Text: "retrying"}, nil},
				{session.BackgroundTasks{Tasks: []session.BackgroundTask{{TaskID: "b1"}}}, nil},
				{session.Unknown{Line: []byte("{"), Err: errors.New("bad json")}, nil},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRecorder(recorderID)
			for i, st := range tc.steps {
				got := r.ops(st.event)
				if !reflect.DeepEqual(got, st.want) {
					t.Errorf("step %d (%T): ops\n got  %#v\n want %#v", i, st.event, got, st.want)
				}
			}
		})
	}
}
