package server_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
)

// writeToolCountsConversation writes a transcript whose edit result carries an invented patch that adds four lines
// and removes two, and returns its path.
func writeToolCountsConversation(t *testing.T) string {
	t.Helper()
	lines := []string{
		`{"type":"user","message":{"role":"user","content":"` + synthOrigin + `"}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_fx","name":"Edit",` +
			`"input":{"file_path":"/synthetic/edit.go"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_fx","content":"edited"}]},` +
			`"toolUseResult":{"type":"update","filePath":"/synthetic/edit.go","structuredPatch":[` +
			`{"oldStart":2,"oldLines":3,"newStart":2,"newLines":5,"lines":[" keep","-old a","-old b",` +
			`"+new a","+new b","+new c","+new d"]}]}}`,
	}
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path
}

func TestServePanesToolLineCountsResultItem(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	pane := conversationPane(t, tmux, liveOptions(writeToolCountsConversation(t)))
	client := panesClient(t, newHandler(t, tmux))

	got, err := client.Conversation(t.Context(), connect.NewRequest(&orreryv1.ConversationRequest{Pane: pane}))
	if err != nil {
		t.Fatalf("Panes.Conversation: %v", err)
	}
	var results []*orreryv1.ConversationItem
	for _, item := range got.Msg.GetItems() {
		if item.GetKind() == "result" {
			results = append(results, item)
		}
	}
	if len(results) != 1 {
		t.Fatalf("result items = %d, want 1 in %v", len(results), got.Msg)
	}
	if added, removed := results[0].GetAdded(), results[0].GetRemoved(); added != 4 || removed != 2 {
		t.Fatalf("result added, removed = %d, %d; want 4, 2", added, removed)
	}
}
