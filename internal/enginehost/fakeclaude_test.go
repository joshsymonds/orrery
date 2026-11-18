package enginehost

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/engine/wire"
)

// userLine is one user prompt line as the engine host writes it to claude's stdin.
func userLine(text string) string {
	return `{"type":"user","message":{"role":"user","content":"` + text + `"}}` + "\n"
}

// fakeClaudeRun runs the scripted fake claude in a fresh directory with input on stdin. The fake exits at stdin EOF,
// so the run ends once input is consumed. It returns the directory and the lines the fake wrote to stdout.
func fakeClaudeRun(t *testing.T, input string) (string, []string) {
	t.Helper()
	dir := t.TempDir()
	return dir, fakeClaudeRunIn(t, dir, input)
}

// fakeClaudeRunIn runs the scripted fake claude in dir, which the caller has prepared, with input on stdin. It returns
// the lines the fake wrote to stdout.
func fakeClaudeRunIn(t *testing.T, dir, input string) []string {
	t.Helper()
	cmd := exec.Command("/bin/sh", fakeClaudePath(t))
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run fake claude: %v", err)
	}
	return strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
}

// decodeVariants decodes each stdout line through the wire package, the decoder the engine applies to claude's output,
// and returns the variants in line order.
func decodeVariants(t *testing.T, lines []string) []any {
	t.Helper()
	variants := make([]any, 0, len(lines))
	for _, line := range lines {
		var msg wire.SDKMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("stdout line is not an SDKMessage: %v\n%s", err, line)
		}
		variant, err := msg.Variant()
		if err != nil {
			t.Fatalf("stdout line does not decode: %v\n%s", err, line)
		}
		variants = append(variants, variant)
	}
	return variants
}

// findVariant returns the index of the first variant at or after from that match reports, and fails the test with
// what when no variant there matches.
func findVariant(t *testing.T, variants []any, from int, what string, match func(any) bool) int {
	t.Helper()
	for i := from; i < len(variants); i++ {
		if match(variants[i]) {
			return i
		}
	}
	t.Fatalf("stdout has no %s after line %d", what, from)
	return -1
}

// isRejectedSevenDayReset reports whether variant is a rejected seven_day rate_limit_event that resets at 1900000000.
func isRejectedSevenDayReset(variant any) bool {
	event, ok := variant.(wire.SDKRateLimitEvent)
	if !ok {
		return false
	}
	info := event.RateLimitInfo
	return info.Status == "rejected" && info.RateLimitType != nil && *info.RateLimitType == "seven_day" &&
		info.ResetsAt != nil && *info.ResetsAt == 1900000000
}

// isRateLimitError reports whether variant is an assistant line that carries the rate_limit error.
func isRateLimitError(variant any) bool {
	assistant, ok := variant.(wire.SDKAssistantMessage)
	return ok && assistant.Error != nil && *assistant.Error == "rate_limit"
}

// isLimitResult reports whether variant is an error result whose text is the limit text.
func isLimitResult(variant any) bool {
	result, ok := variant.(wire.SDKResultSuccess)
	return ok && result.IsError && result.Result == "You've hit your limit"
}

// TestFakeClaudeLimitTurn checks that a LIMIT prompt plays the usage-limit turn: a rejected seven_day rate_limit_event,
// then the assistant line that carries the rate_limit error, then the error result, in that order.
func TestFakeClaudeLimitTurn(t *testing.T) {
	t.Parallel()
	_, lines := fakeClaudeRun(t, userLine("LIMIT one"))
	variants := decodeVariants(t, lines)

	event := findVariant(t, variants, 0, "rejected seven_day rate_limit_event resetting at 1900000000",
		isRejectedSevenDayReset)
	assistant := findVariant(t, variants, event+1, "assistant line with error rate_limit", isRateLimitError)
	findVariant(t, variants, assistant+1, "error result carrying the limit text", isLimitResult)

	want := [][]string{{"You've hit your limit"}}
	if got := assistantTexts(t, lines); !reflect.DeepEqual(got, want) {
		t.Fatalf("assistant lines = %q, want %q", got, want)
	}
}

// TestFakeClaudeLimitNoResetTurn checks that a LIMITNORESET prompt plays the usage-limit turn without its
// rate_limit_event, and still ends with the error assistant line and the error result.
func TestFakeClaudeLimitNoResetTurn(t *testing.T) {
	t.Parallel()
	_, lines := fakeClaudeRun(t, userLine("LIMITNORESET one"))
	variants := decodeVariants(t, lines)
	for _, variant := range variants {
		if _, ok := variant.(wire.SDKRateLimitEvent); ok {
			t.Fatalf("LIMITNORESET turn sends a rate_limit_event: %#v", variant)
		}
	}
	assistant := findVariant(t, variants, 0, "assistant line with error rate_limit", isRateLimitError)
	findVariant(t, variants, assistant+1, "error result carrying the limit text", isLimitResult)
}

// TestFakeClaudeNextPromptFromFile checks that a fakeclaude.next file stands in for the text of the next prompt and is
// removed when that prompt arrives, so the prompt after it is answered as an ordinary reply.
func TestFakeClaudeNextPromptFromFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fakeclaude.next"), []byte("LIMIT"), 0o600); err != nil {
		t.Fatalf("write fakeclaude.next: %v", err)
	}
	lines := fakeClaudeRunIn(t, dir, userLine("plain one")+userLine("plain two"))

	want := [][]string{{"You've hit your limit"}, {"fake reply 2"}}
	if got := assistantTexts(t, lines); !reflect.DeepEqual(got, want) {
		t.Fatalf("assistant lines = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "fakeclaude.next")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("fakeclaude.next after its prompt: err = %v, want it removed", err)
	}
}

// assistantTexts returns, for each assistant line in lines, the text of its content blocks in order.
func assistantTexts(t *testing.T, lines []string) [][]string {
	t.Helper()
	var turns [][]string
	for _, line := range lines {
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &head); err != nil {
			t.Fatalf("stdout line is not JSON: %v\n%s", err, line)
		}
		if head.Type != "assistant" {
			continue
		}
		var assistant struct {
			Message struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(line), &assistant); err != nil {
			t.Fatalf("assistant line does not parse: %v\n%s", err, line)
		}
		texts := make([]string, 0, len(assistant.Message.Content))
		for _, block := range assistant.Message.Content {
			texts = append(texts, block.Text)
		}
		turns = append(turns, texts)
	}
	return turns
}

// TestFakeClaudePairLine checks that a PAIR prompt is answered by one assistant line carrying two finished text
// blocks, that the next prompt is still answered as an ordinary reply, and that the fake logs each line it reads.
func TestFakeClaudePairLine(t *testing.T) {
	t.Parallel()
	prompts := userLine("PAIR one") + userLine("plain two")
	dir, lines := fakeClaudeRun(t, prompts)

	want := [][]string{{"fake pair 1.1", "fake pair 1.2"}, {"fake reply 2"}}
	if got := assistantTexts(t, lines); !reflect.DeepEqual(got, want) {
		t.Fatalf("assistant lines = %q, want %q", got, want)
	}
	stdin, err := os.ReadFile(filepath.Join(dir, "fakeclaude.stdin"))
	if err != nil {
		t.Fatalf("read fake stdin log: %v", err)
	}
	if string(stdin) != prompts {
		t.Fatalf("fake stdin log = %q, want %q", stdin, prompts)
	}
}

// TestFakeClaudeStdinLogsAnswers checks that each line a mode reads after its prompt, an answer, a permission
// decision or an interrupt, is logged to the fake stdin log as well, so every line the fake receives is counted once.
func TestFakeClaudeStdinLogsAnswers(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		prompt string
		answer string
	}{
		{"ask", "ASK", `{"type":"control_response","response":{"answers":{"Which color?":"red"}}}` + "\n"},
		{"permit", "PERMIT", `{"type":"control_response","response":{"behavior":"allow"}}` + "\n"},
		{"hang", "HANG", `{"type":"control_request","request":{"subtype":"interrupt"}}` + "\n"},
		{"stall", "STALL", `{"type":"control_response"}` + "\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			input := userLine(tc.prompt) + tc.answer
			dir, _ := fakeClaudeRun(t, input)
			stdin, err := os.ReadFile(filepath.Join(dir, "fakeclaude.stdin"))
			if err != nil {
				t.Fatalf("read fake stdin log: %v", err)
			}
			if string(stdin) != input {
				t.Fatalf("fake stdin log = %q, want %q", stdin, input)
			}
		})
	}
}

// queuedLine is one user prompt line that carries uuid, as the engine host writes a prompt with its write id.
func queuedLine(uuid, text string) string {
	return `{"type":"user","message":{"role":"user","content":"` + text + `"},"uuid":"` + uuid + `"}` + "\n"
}

// cancelRequest is the cancel_async_message control request under request id req for the message under uuid.
func cancelRequest(req, uuid string) string {
	return `{"type":"control_request","request_id":"` + req +
		`","request":{"subtype":"cancel_async_message","message_uuid":"` + uuid + `"}}` + "\n"
}

// interruptRequest is the interrupt control request under request id req, with cancel_queued set when cancelQueued is.
func interruptRequest(req string, cancelQueued bool) string {
	request := `{"subtype":"interrupt"`
	if cancelQueued {
		request += `,"cancel_queued":true`
	}
	return `{"type":"control_request","request_id":"` + req + `","request":` + request + `}}` + "\n"
}

// queueTrace returns one token for each line of the fake's output that the queue tests follow: "<state> <uuid>" for a
// command_lifecycle line, "control <request_id> <response>" for a control_response, "tool_result" for a user line that
// carries a tool result, and "result <subtype> is_error=<is_error>" for a result line. Other lines have no token.
func queueTrace(t *testing.T, lines []string) []string {
	t.Helper()
	var trace []string
	for _, line := range lines {
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &head); err != nil {
			t.Fatalf("stdout line is not JSON: %v\n%s", err, line)
		}
		switch head.Type {
		case "command_lifecycle":
			var lifecycle struct {
				CommandUUID string `json:"command_uuid"`
				State       string `json:"state"`
			}
			if err := json.Unmarshal([]byte(line), &lifecycle); err != nil {
				t.Fatalf("command_lifecycle line does not parse: %v\n%s", err, line)
			}
			trace = append(trace, lifecycle.State+" "+lifecycle.CommandUUID)
		case "control_response":
			var control struct {
				Response struct {
					RequestID string          `json:"request_id"`
					Payload   json.RawMessage `json:"response"`
				} `json:"response"`
			}
			if err := json.Unmarshal([]byte(line), &control); err != nil {
				t.Fatalf("control_response line does not parse: %v\n%s", err, line)
			}
			trace = append(trace, "control "+control.Response.RequestID+" "+string(control.Response.Payload))
		case "user":
			if strings.Contains(line, `"type":"tool_result"`) {
				trace = append(trace, "tool_result")
			}
		case "result":
			var result struct {
				Subtype string `json:"subtype"`
				IsError bool   `json:"is_error"`
			}
			if err := json.Unmarshal([]byte(line), &result); err != nil {
				t.Fatalf("result line does not parse: %v\n%s", err, line)
			}
			trace = append(trace, fmt.Sprintf("result %s is_error=%t", result.Subtype, result.IsError))
		}
	}
	return trace
}

// resultUUIDs returns the user_message_uuids of the result line in lines.
func resultUUIDs(t *testing.T, lines []string) []string {
	t.Helper()
	for _, line := range lines {
		var result struct {
			Type             string   `json:"type"`
			UserMessageUUIDs []string `json:"user_message_uuids"`
		}
		if err := json.Unmarshal([]byte(line), &result); err != nil {
			t.Fatalf("stdout line is not JSON: %v\n%s", err, line)
		}
		if result.Type == "result" {
			return result.UserMessageUUIDs
		}
	}
	t.Fatal("stdout has no result line")
	return nil
}

// TestFakeClaudeQueueLifecycleIdle checks that a prompt read while idle is queued as it is read, started as its turn
// opens and completed just before the turn's result, and that its queued line decodes as a command_lifecycle message.
func TestFakeClaudeQueueLifecycleIdle(t *testing.T) {
	t.Parallel()
	_, lines := fakeClaudeRun(t, queuedLine("queue-a", "plain one"))
	want := []string{"queued queue-a", "started queue-a", "completed queue-a", "result success is_error=false"}
	if got := queueTrace(t, lines); !reflect.DeepEqual(got, want) {
		t.Fatalf("trace = %q, want %q", got, want)
	}
	variants := decodeVariants(t, lines)
	if _, ok := variants[0].(wire.SDKCommandLifecycleMessage); !ok {
		t.Fatalf("first line decodes as %T, want a command_lifecycle message", variants[0])
	}
}

// TestFakeClaudeQueueLifecycleFolds checks that a prompt read while a turn runs stays queued until the turn's next tool
// result takes it in: it is started there, and the turn lists it among the uuids it answers. MIDTURN makes its tool call
// as its turn opens. FOLD waits for fakeclaude.release, which the test creates before the run, and then makes its tool
// call.
func TestFakeClaudeQueueLifecycleFolds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		prompt string
	}{
		{"midturn", "MIDTURN one"},
		{"fold", "FOLD one"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "fakeclaude.release"), nil, 0o600); err != nil {
				t.Fatalf("write fakeclaude.release: %v", err)
			}
			lines := fakeClaudeRunIn(t, dir, queuedLine("queue-a", tc.prompt)+queuedLine("queue-b", "plain two"))
			want := []string{
				"queued queue-a", "started queue-a", "queued queue-b", "tool_result", "started queue-b",
				"completed queue-a", "completed queue-b", "result success is_error=false",
			}
			if got := queueTrace(t, lines); !reflect.DeepEqual(got, want) {
				t.Fatalf("trace = %q, want %q", got, want)
			}
			if got := resultUUIDs(t, lines); !reflect.DeepEqual(got, []string{"queue-a", "queue-b"}) {
				t.Fatalf("result answers %q, want [queue-a queue-b]", got)
			}
		})
	}
}

// TestFakeClaudeQueueLifecycleWaitOpen checks that a WAITOPEN prompt writes its queued line once fakeclaude.open exists,
// then opens its turn as any prompt does. The test creates fakeclaude.open before the run, so the fake does not wait.
func TestFakeClaudeQueueLifecycleWaitOpen(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fakeclaude.open"), nil, 0o600); err != nil {
		t.Fatalf("write fakeclaude.open: %v", err)
	}
	lines := fakeClaudeRunIn(t, dir, queuedLine("queue-a", "WAITOPEN one"))
	want := []string{"queued queue-a", "started queue-a", "completed queue-a", "result success is_error=false"}
	if got := queueTrace(t, lines); !reflect.DeepEqual(got, want) {
		t.Fatalf("trace = %q, want %q", got, want)
	}
}

// TestFakeClaudeQueueLifecycleCancelAsync checks that a cancel_async_message for a queued prompt writes its cancelled
// lifecycle line and answers true, and that the prompt is never started. A cancel for a started prompt answers false
// and writes no lifecycle line.
func TestFakeClaudeQueueLifecycleCancelAsync(t *testing.T) {
	t.Parallel()
	input := queuedLine("queue-a", "HANG one") + queuedLine("queue-b", "plain two") +
		cancelRequest("req-b", "queue-b") + cancelRequest("req-a", "queue-a") + interruptRequest("req-int", false)
	_, lines := fakeClaudeRun(t, input)
	want := []string{
		"queued queue-a", "started queue-a", "queued queue-b",
		"cancelled queue-b", `control req-b {"cancelled":true}`,
		`control req-a {"cancelled":false}`,
		"result error_during_execution is_error=true",
	}
	if got := queueTrace(t, lines); !reflect.DeepEqual(got, want) {
		t.Fatalf("trace = %q, want %q", got, want)
	}
}

// TestFakeClaudeQueueLifecycleCancelQueued checks that an interrupt with cancel_queued cancels each queued prompt in
// the order it was read, answers with that list, ends the turn with its error result, and then cancels the opening
// prompt.
func TestFakeClaudeQueueLifecycleCancelQueued(t *testing.T) {
	t.Parallel()
	input := queuedLine("queue-a", "HANG one") + queuedLine("queue-b", "plain two") +
		queuedLine("queue-c", "plain three") + interruptRequest("req-int", true)
	_, lines := fakeClaudeRun(t, input)
	want := []string{
		"queued queue-a", "started queue-a", "queued queue-b", "queued queue-c",
		"cancelled queue-b", "cancelled queue-c",
		`control req-int {"still_queued":[],"cancelled":["queue-b","queue-c"]}`,
		"result error_during_execution is_error=true",
		"cancelled queue-a",
	}
	if got := queueTrace(t, lines); !reflect.DeepEqual(got, want) {
		t.Fatalf("trace = %q, want %q", got, want)
	}
}

// TestFakeClaudeQueueLifecyclePlainInterrupt checks that an interrupt without cancel_queued writes no command_lifecycle
// line after it: the queued prompt stays queued, and the opening prompt gets no line after the error result.
func TestFakeClaudeQueueLifecyclePlainInterrupt(t *testing.T) {
	t.Parallel()
	input := queuedLine("queue-a", "HANG one") + queuedLine("queue-b", "plain two") + interruptRequest("req-int", false)
	_, lines := fakeClaudeRun(t, input)
	want := []string{
		"queued queue-a", "started queue-a", "queued queue-b", "result error_during_execution is_error=true",
	}
	if got := queueTrace(t, lines); !reflect.DeepEqual(got, want) {
		t.Fatalf("trace = %q, want %q", got, want)
	}
}

// TestFakeClaudeSelfTurnTakesInQueuedLine checks that a BGSELFMID prompt plays the turn claude starts by itself once its
// background task ends: the turn reads the next line from stdin during its Bash call, takes that line in with the call's
// result, and its result lists that line's uuid alone, since no prompt opened the turn.
func TestFakeClaudeSelfTurnTakesInQueuedLine(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fakeclaude.release"), nil, 0o600); err != nil {
		t.Fatalf("write fakeclaude.release: %v", err)
	}
	lines := fakeClaudeRunIn(t, dir, queuedLine("prompt-a", "BGSELFMID")+queuedLine("prompt-b", "owner words"))

	want := []string{
		"queued prompt-a", "started prompt-a", "completed prompt-a", "result success is_error=false",
		"queued prompt-b", "tool_result", "started prompt-b", "completed prompt-b", "result success is_error=false",
	}
	if got := queueTrace(t, lines); !reflect.DeepEqual(got, want) {
		t.Fatalf("trace = %q, want %q", got, want)
	}
	var answered [][]string
	for _, line := range lines {
		var result struct {
			Type             string   `json:"type"`
			UserMessageUUIDs []string `json:"user_message_uuids"`
		}
		if err := json.Unmarshal([]byte(line), &result); err != nil {
			t.Fatalf("decode fake claude line %q: %v", line, err)
		}
		if result.Type == "result" {
			answered = append(answered, result.UserMessageUUIDs)
		}
	}
	if wantAnswered := [][]string{{"prompt-a"}, {"prompt-b"}}; !reflect.DeepEqual(answered, wantAnswered) {
		t.Fatalf("results list answered = %q, want %q", answered, wantAnswered)
	}
}
