package stepmcp

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/store"
)

// resultSteps is the daemon's steps for the result tool. Its Status answers row, and no other command is reached.
type resultSteps struct {
	enginehost.StepCommands
	row store.StepRow
}

// Status answers the fake's row whatever the id.
func (f resultSteps) Status(context.Context, string) (store.StepRow, error) {
	return f.row, nil
}

// inventedResult is the step's return: a JSON object whose fields cover a string with multi-byte runes, an array of
// objects with id properties, an object whose keys are out of order, a multi-line string, a long string, a number, a
// boolean and a null.
const inventedResult = `{
  "summary": "héllo",
  "verdicts": [{"id": "v1", "ok": true}, {"id": "v2", "ok": false}, {"id": "v3", "ok": true}],
  "meta": {"b": 1, "a": "x"},
  "note": "line one\nline two",
  "long": "abcdefghijklmnopqrstuvwxyz",
  "count": 42,
  "ok": true,
  "gone": null
}`

// TestStepByPartResultTool checks step_result over MCP against a daemon whose step returns inventedResult. The outline
// lists each field with its type and size and no values. A string field reads as raw text and an object field as
// compact JSON. An array element reads by id and by index. A value cut at limit ends in a line naming the offset to
// continue from. An unknown field, an unknown item and a call without id are tool errors.
func TestStepByPartResultTool(t *testing.T) {
	row := store.StepRow{ID: "s1", Agent: "reviewer", Result: inventedResult}
	p := newPeer(t, startDaemon(t, resultSteps{row: row}), nil)

	t.Run("outline lists type and size with no values", func(t *testing.T) {
		outcome := p.tool("step_result", `{"id":"s1"}`)
		if outcome.isError {
			t.Fatalf("step_result reported an error: %s", outcome.text)
		}
		lines := strings.Split(outcome.text, "\n")
		want := []string{
			"summary (string, 5 runes)",
			"verdicts (array, 3 items)",
			"meta (object, 2 keys)",
			"note (string, 17 runes)",
			"long (string, 26 runes)",
			"count (number, 2 bytes)",
			"ok (boolean, 4 bytes)",
			"gone (null, 4 bytes)",
		}
		if len(lines) != len(want)+1 || !slices.Equal(lines[:len(want)], want) {
			t.Fatalf("step_result outline = %q, want the lines %q and a last line", outcome.text, want)
		}
		last := lines[len(lines)-1]
		for _, word := range []string{"field", "item"} {
			if !strings.Contains(last, word) {
				t.Errorf("step_result outline's last line = %q, want it to name %s", last, word)
			}
		}
	})

	for _, tc := range []struct {
		name string
		args string
		want string
	}{
		{name: "string field reads as raw text", args: `{"id":"s1","field":"note"}`, want: "line one\nline two"},
		{name: "object field reads as compact JSON", args: `{"id":"s1","field":"meta"}`, want: `{"b":1,"a":"x"}`},
		{
			name: "array item reads by id",
			args: `{"id":"s1","field":"verdicts","item":"v2"}`,
			want: `{"id":"v2","ok":false}`,
		},
		{
			name: "array item reads by 0-based index",
			args: `{"id":"s1","field":"verdicts","item":"2"}`,
			want: `{"id":"v3","ok":true}`,
		},
		{
			name: "value cut at limit followed by a line naming the offset to continue from",
			args: `{"id":"s1","field":"long","limit":10}`,
			want: "abcdefghij\n[cut: runes 0-10 of 26; continue with offset 10]",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outcome := p.tool("step_result", tc.args)
			if outcome.isError {
				t.Fatalf("step_result %s reported an error: %s", tc.args, outcome.text)
			}
			if outcome.text != tc.want {
				t.Fatalf("step_result %s = %q, want %q", tc.args, outcome.text, tc.want)
			}
		})
	}

	for _, tc := range []struct {
		name string
		args string
		want string
	}{
		{
			name: "unknown field is a tool error naming the fields",
			args: `{"id":"s1","field":"nope"}`,
			want: `its fields are summary, verdicts, meta, note, long, count, ok, gone`,
		},
		{
			name: "unknown item is a tool error naming the element count",
			args: `{"id":"s1","field":"verdicts","item":"9"}`,
			want: `which has 3 elements`,
		},
		{name: "call without id is refused", args: `{}`, want: "id is required"},
		{name: "call with empty id is refused", args: `{"id":""}`, want: "id is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outcome := p.tool("step_result", tc.args)
			if !outcome.isError {
				t.Fatalf("step_result %s = %q, want a tool error", tc.args, outcome.text)
			}
			if !strings.Contains(outcome.text, tc.want) {
				t.Fatalf("step_result %s error = %q, want it to contain %q", tc.args, outcome.text, tc.want)
			}
		})
	}
}
