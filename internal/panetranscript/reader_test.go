package panetranscript

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/conversation"
)

// stamp returns the RFC 3339 timestamp of the unix second s, in UTC.
func stamp(s int) string {
	return time.Unix(int64(s), 0).UTC().Format(time.RFC3339)
}

// promptLine returns a string prompt line at second s.
func promptLine(s int, text string) string {
	return `{"type":"user","timestamp":"` + stamp(s) + `","message":{"role":"user","content":"` + text + `"}}`
}

// resultLine returns a user line at second s that carries the result of the tool call id.
func resultLine(s int, id string) string {
	return `{"type":"user","timestamp":"` + stamp(s) + `","message":{"role":"user","content":[` +
		`{"type":"tool_result","tool_use_id":"` + id + `","content":"ok"}]}}`
}

// assistantLine returns an assistant line at second s whose content blocks are content.
func assistantLine(s int, content string) string {
	return `{"type":"assistant","timestamp":"` + stamp(s) + `","message":{"role":"assistant","content":[` +
		content + `]}}`
}

// usageLine returns an assistant line at second s whose usage object is usage and which has no content.
func usageLine(s int, usage string) string {
	return `{"type":"assistant","timestamp":"` + stamp(s) + `","message":{"role":"assistant","usage":` + usage +
		`,"content":[]}}`
}

// textBlock returns a text content block.
func textBlock(text string) string {
	return `{"type":"text","text":"` + text + `"}`
}

// toolUseBlock returns a tool_use content block whose input is the JSON object input.
func toolUseBlock(id, name, input string) string {
	return `{"type":"tool_use","id":"` + id + `","name":"` + name + `","input":` + input + `}`
}

// flagged returns line with the JSON member flag added first, as in `"isSidechain":true`.
func flagged(line, flag string) string {
	return strings.Replace(line, "{", "{"+flag+",", 1)
}

// writeTranscript writes the lines to a new transcript file and returns its path.
func writeTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path
}

// appendText appends text to the transcript at path.
func appendText(t *testing.T, path, text string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open transcript: %v", err)
	}
	defer file.Close()
	if _, err := file.WriteString(text); err != nil {
		t.Fatalf("append transcript: %v", err)
	}
}

// readTurn calls Read on reader and fails the test on error.
func readTurn(t *testing.T, reader *Reader) Turn {
	t.Helper()
	turn, err := reader.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return turn
}

func TestTranscriptTurnToolRunsUntilResult(t *testing.T) {
	path := writeTranscript(t,
		promptLine(1, "go"),
		assistantLine(2, toolUseBlock("t1", "Bash", `{"command":"make test"}`)),
	)
	reader := NewReader(path)

	turn := readTurn(t, reader)
	want := &Tool{ID: "t1", Name: "Bash", Input: "make test", Start: 2}
	if !reflect.DeepEqual(turn.Tool, want) {
		t.Fatalf("Tool = %+v, want %+v", turn.Tool, want)
	}

	appendText(t, path, resultLine(3, "t1")+"\n")
	if turn = readTurn(t, reader); turn.Tool != nil {
		t.Fatalf("Tool = %+v after its result, want nil", turn.Tool)
	}
}

func TestTranscriptTurnStartIsNewestPrompt(t *testing.T) {
	path := writeTranscript(t,
		promptLine(100, "first"),
		`{"type":"user","timestamp":"`+stamp(200)+`","message":{"role":"user","content":[`+
			textBlock("second")+`]}}`,
		resultLine(300, "t1"),
		flagged(promptLine(400, "meta"), `"isMeta":true`),
		flagged(promptLine(500, "side"), `"isSidechain":true`),
	)

	if turn := readTurn(t, NewReader(path)); turn.TurnStart != 200 {
		t.Fatalf("TurnStart = %d, want 200", turn.TurnStart)
	}
}

func TestTranscriptTurnPendingOldestFirst(t *testing.T) {
	path := writeTranscript(t,
		assistantLine(1, toolUseBlock("old", "Read", `{"file_path":"/a"}`)),
		promptLine(2, "go"),
		assistantLine(3, textBlock("plan")+","+toolUseBlock("a", "Bash", `{"command":"ls"}`)+","+
			toolUseBlock("b", "Grep", `{"pattern":"x"}`)),
		resultLine(4, "b"),
		assistantLine(5, toolUseBlock("c", "Edit", `{"file_path":"/f"}`)),
	)

	turn := readTurn(t, NewReader(path))
	want := []Call{
		{ID: "a", Name: "Bash", Input: json.RawMessage(`{"command":"ls"}`)},
		{ID: "c", Name: "Edit", Input: json.RawMessage(`{"file_path":"/f"}`)},
	}
	if !reflect.DeepEqual(turn.Pending, want) {
		t.Fatalf("Pending = %+v, want %+v", turn.Pending, want)
	}
}

func TestTranscriptTurnLastMessageIsNewestTurnsLastText(t *testing.T) {
	path := writeTranscript(t,
		promptLine(1, "go"),
		assistantLine(2, textBlock("alpha")+","+textBlock("beta")),
		assistantLine(3, textBlock("gamma")+","+toolUseBlock("t1", "Bash", `{"command":"ls"}`)),
	)
	reader := NewReader(path)

	if turn := readTurn(t, reader); turn.LastMessage != "gamma" {
		t.Fatalf("LastMessage = %q, want gamma", turn.LastMessage)
	}

	appendText(t, path, promptLine(4, "again")+"\n"+
		assistantLine(5, toolUseBlock("t2", "Bash", `{"command":"pwd"}`))+"\n")
	if turn := readTurn(t, reader); turn.LastMessage != "" {
		t.Fatalf("LastMessage = %q in a turn with no text, want empty", turn.LastMessage)
	}
}

func TestTranscriptTurnAPIErrorFailsTurn(t *testing.T) {
	path := writeTranscript(t,
		promptLine(1, "go"),
		assistantLine(2, textBlock("fine")),
	)
	reader := NewReader(path)
	if turn := readTurn(t, reader); turn.Failed || turn.APIError != "" {
		t.Fatalf("Failed = %v, APIError = %q before an error line, want false and empty", turn.Failed, turn.APIError)
	}

	appendText(t, path, `{"type":"assistant","timestamp":"`+stamp(3)+`","isApiErrorMessage":true,`+
		`"error":"authentication_failed","message":{"role":"assistant","content":[`+
		textBlock("API Error: 401")+`]}}`+"\n")
	if turn := readTurn(t, reader); !turn.Failed || turn.APIError != "authentication_failed" {
		t.Fatalf("Failed = %v, APIError = %q after an error line, want true and authentication_failed",
			turn.Failed, turn.APIError)
	}

	appendText(t, path, assistantLine(4, textBlock("recovered"))+"\n")
	if turn := readTurn(t, reader); turn.Failed || turn.APIError != "" {
		t.Fatalf("Failed = %v, APIError = %q after a reply, want false and empty", turn.Failed, turn.APIError)
	}
}

func TestTranscriptTurnContextFromUsage(t *testing.T) {
	path := writeTranscript(t,
		promptLine(1, "go"),
		usageLine(2, `{"input_tokens":100,"cache_creation_input_tokens":200,"cache_read_input_tokens":300}`),
	)
	reader := NewReader(path)

	want := conversation.Context{Tokens: 600, Window: 1_000_000}
	if turn := readTurn(t, reader); turn.Context != want {
		t.Fatalf("Context = %+v, want %+v", turn.Context, want)
	}

	appendText(t, path, usageLine(3, `{"input_tokens":1000}`)+"\n")
	want = conversation.Context{Tokens: 1000, Window: 1_000_000}
	if turn := readTurn(t, reader); turn.Context != want {
		t.Fatalf("Context = %+v after a newer usage line, want %+v", turn.Context, want)
	}
}

func TestTranscriptTurnSidechainAndMetaChangeNothing(t *testing.T) {
	path := writeTranscript(t,
		promptLine(1, "go"),
		usageLine(2, `{"input_tokens":100}`),
		assistantLine(3, textBlock("working")+","+toolUseBlock("t1", "Bash", `{"command":"ls"}`)),
	)
	reader := NewReader(path)
	before := readTurn(t, reader)

	appendText(t, path, strings.Join([]string{
		flagged(promptLine(4, "side prompt"), `"isSidechain":true`),
		flagged(promptLine(5, "meta prompt"), `"isMeta":true`),
		flagged(resultLine(6, "t1"), `"isSidechain":true`),
		flagged(assistantLine(7, textBlock("side text")+","+toolUseBlock("t2", "Bash", `{"command":"pwd"}`)),
			`"isSidechain":true`),
		flagged(assistantLine(8, textBlock("meta text")),
			`"isMeta":true,"isApiErrorMessage":true,"error":"rate_limit"`),
		flagged(usageLine(9, `{"input_tokens":999999}`), `"isSidechain":true`),
	}, "\n")+"\n")
	after := readTurn(t, reader)

	if !reflect.DeepEqual(after, before) {
		t.Fatalf("Turn after sidechain and meta lines = %+v, want unchanged %+v", after, before)
	}
}

func TestTranscriptTurnHoldsPartialLine(t *testing.T) {
	path := writeTranscript(t, promptLine(1, "go"))
	line := assistantLine(2, toolUseBlock("t1", "Bash", `{"command":"ls"}`))
	appendText(t, path, line[:40])
	reader := NewReader(path)

	if turn := readTurn(t, reader); turn.Tool != nil || len(turn.Pending) != 0 {
		t.Fatalf("Tool = %+v, Pending = %+v for a line not yet complete, want none", turn.Tool, turn.Pending)
	}

	appendText(t, path, line[40:]+"\n")
	turn := readTurn(t, reader)
	if turn.Tool == nil || turn.Tool.ID != "t1" {
		t.Fatalf("Tool = %+v once the line is complete, want t1", turn.Tool)
	}
	if len(turn.Pending) != 1 {
		t.Fatalf("Pending has %d calls, want the line read once as 1", len(turn.Pending))
	}
}

func TestTranscriptTurnStartsOverWhenFileShrinks(t *testing.T) {
	path := writeTranscript(t,
		promptLine(1, "first prompt that is long enough to make this file longer than the next one"),
		assistantLine(2, toolUseBlock("t1", "Bash", `{"command":"ls"}`)),
	)
	reader := NewReader(path)
	readTurn(t, reader)

	if err := os.WriteFile(path, []byte(promptLine(9, "second")+"\n"), 0o600); err != nil {
		t.Fatalf("rewrite transcript: %v", err)
	}
	turn := readTurn(t, reader)
	if turn.TurnStart != 9 || turn.Tool != nil || len(turn.Pending) != 0 {
		t.Fatalf("Turn = %+v after the file shrank, want only the new prompt at 9", turn)
	}
}

func TestTranscriptTurnMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.jsonl")

	_, err := NewReader(path).Read()
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Read of a missing transcript: err = %v, want one wrapping fs.ErrNotExist", err)
	}
}

// stoppedLine returns an assistant line at second s whose content blocks are content and whose message's stop_reason
// is stop.
func stoppedLine(s int, stop, content string) string {
	return `{"type":"assistant","timestamp":"` + stamp(s) + `","message":{"role":"assistant","stop_reason":"` + stop +
		`","content":[` + content + `]}}`
}

// interruptionLine returns a user line at second s that holds the interruption text text.
func interruptionLine(s int, text string) string {
	return `{"type":"user","timestamp":"` + stamp(s) + `","message":{"role":"user","content":[` + textBlock(text) +
		`]}}`
}

func TestTranscriptTurnEndsAtStopThatIsNotToolUse(t *testing.T) {
	path := writeTranscript(t,
		promptLine(1, "go"),
		stoppedLine(2, "tool_use", toolUseBlock("a1", "Agent", `{"prompt":"search","run_in_background":true}`)),
	)
	reader := NewReader(path)
	if turn := readTurn(t, reader); turn.TurnEnd != 0 {
		t.Fatalf("TurnEnd = %d at a tool_use stop, want 0", turn.TurnEnd)
	}

	appendText(t, path, resultLine(3, "a1")+"\n")
	if turn := readTurn(t, reader); turn.TurnEnd != 0 {
		t.Fatalf("TurnEnd = %d at a tool result, want 0", turn.TurnEnd)
	}

	appendText(t, path, stoppedLine(4, "end_turn", textBlock("The agent runs."))+"\n")
	if turn := readTurn(t, reader); turn.TurnEnd != 4 {
		t.Fatalf("TurnEnd = %d at an end_turn stop, want 4", turn.TurnEnd)
	}

	appendText(t, path, promptLine(9, "<task-notification><task-id>a1</task-id></task-notification>")+"\n")
	if turn := readTurn(t, reader); turn.TurnEnd != 0 || turn.TurnStart != 9 {
		t.Fatalf("TurnEnd, TurnStart = %d, %d at the next prompt, want 0, 9", turn.TurnEnd, turn.TurnStart)
	}

	appendText(t, path, stoppedLine(11, "stop_sequence", textBlock("Done."))+"\n")
	if turn := readTurn(t, reader); turn.TurnEnd != 11 {
		t.Fatalf("TurnEnd = %d at a stop_sequence stop, want 11", turn.TurnEnd)
	}
}

func TestTranscriptTurnRunsAtAssistantLineWithNoStop(t *testing.T) {
	path := writeTranscript(t,
		promptLine(1, "go"),
		stoppedLine(2, "end_turn", textBlock("first")),
		promptLine(3, "again"),
		assistantLine(4, textBlock("cut off")),
	)
	if turn := readTurn(t, NewReader(path)); turn.TurnEnd != 0 {
		t.Fatalf("TurnEnd = %d at an assistant line with no stop_reason, want 0", turn.TurnEnd)
	}
}

func TestTranscriptTurnEndsAtAPIErrorAndInterruption(t *testing.T) {
	for _, tc := range []struct {
		name string
		line string
	}{
		{"api error", `{"type":"assistant","timestamp":"` + stamp(5) + `","isApiErrorMessage":true,` +
			`"error":"rate_limit","message":{"role":"assistant","content":[` + textBlock("API Error") + `]}}`},
		{"interruption", interruptionLine(5, "[Request interrupted by user]")},
		{"tool interruption", interruptionLine(5, "[Request interrupted by user for tool use]")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTranscript(t,
				promptLine(1, "go"),
				stoppedLine(2, "tool_use", toolUseBlock("t1", "Bash", `{"command":"make"}`)),
				tc.line,
			)
			if turn := readTurn(t, NewReader(path)); turn.TurnEnd != 5 {
				t.Fatalf("TurnEnd = %d, want 5", turn.TurnEnd)
			}
		})
	}
}

func TestTranscriptTurnEndIgnoresSidechainStops(t *testing.T) {
	path := writeTranscript(t,
		promptLine(1, "go"),
		stoppedLine(2, "tool_use", toolUseBlock("t1", "Bash", `{"command":"make"}`)),
		flagged(stoppedLine(3, "end_turn", textBlock("subagent done")), `"isSidechain":true`),
	)
	if turn := readTurn(t, NewReader(path)); turn.TurnEnd != 0 {
		t.Fatalf("TurnEnd = %d after a sidechain stop, want 0", turn.TurnEnd)
	}
}
