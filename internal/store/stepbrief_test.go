package store

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// briefTestStep returns a finished step with the schema and result given and no reason or checks.
func briefTestStep(schema, result string) StepRow {
	return StepRow{ID: "s1", Schema: schema, Result: result}
}

// briefTestLines logs the lines got renders, which go test -v shows, and fails the test when got is not want.
func briefTestLines(t *testing.T, got, want []string) {
	t.Helper()
	t.Logf("Lines:\n%s", strings.Join(got, "\n"))
	if !slices.Equal(got, want) {
		t.Fatalf("Lines:\n got  %q\n want %q", got, want)
	}
}

func TestStepEventBriefDeriveReviewerFindingsCount(t *testing.T) {
	t.Parallel()
	schema := `{"type":"object","properties":{"verdict":{"type":"string","enum":["pass","fail"]},` +
		`"findings":{"type":"array","items":{"type":"object",` +
		`"properties":{"title":{"type":"string"}},"required":["title"]}}}}`
	result := `{"verdict":"fail","findings":[{"title":"a"},{"title":"b"},{"title":"c"}]}`
	briefTestLines(t, StepBriefOf(briefTestStep(schema, result)).Lines(), []string{
		"verdict: fail", "findings: 3", "Not shown: none", "More: step_result id s1",
	})
}

func TestStepEventBriefDeriveVerifierResultsTally(t *testing.T) {
	t.Parallel()
	schema := `{"type":"object","properties":{"results":{"type":"array","items":{"type":"object",` +
		`"properties":{"claim":{"type":"string"},"result":{"type":"string","enum":["confirmed","dropped","open"]}},` +
		`"required":["claim","result"]}}}}`
	result := `{"results":[{"claim":"a","result":"open"},{"claim":"b","result":"dropped"},` +
		`{"claim":"c","result":"confirmed"},{"claim":"d","result":"confirmed"}]}`
	briefTestLines(t, StepBriefOf(briefTestStep(schema, result)).Lines(), []string{
		"results: 4 (confirmed 2, dropped 1, open 1)", "Not shown: none", "More: step_result id s1",
	})
}

func TestStepEventBriefDeriveTallyOutsideEnum(t *testing.T) {
	t.Parallel()
	schema := `{"type":"object","properties":{"results":{"type":"array","items":{"type":"object",` +
		`"properties":{"claim":{"type":"string"},"result":{"type":"string","enum":["confirmed","dropped","open"]}},` +
		`"required":["claim","result"]}}}}`
	// The value skip is seen before maybe, and is counted after the enum's values; the item with no result counts
	// toward the array's size but not the tally.
	result := `{"results":[{"claim":"a","result":"skip"},{"claim":"b","result":"maybe"},` +
		`{"claim":"c","result":"confirmed"},{"claim":"d","result":"maybe"},{"claim":"e"}]}`
	briefTestLines(t, StepBriefOf(briefTestStep(schema, result)).Lines(), []string{
		"results: 5 (confirmed 1, skip 1, maybe 2)", "Not shown: none", "More: step_result id s1",
	})
}

func TestStepEventBriefDeriveScalarFields(t *testing.T) {
	t.Parallel()
	schema := `{"type":"object","properties":{"status":{"type":"string","enum":["ok","bad"]},` +
		`"ready":{"type":"boolean"},"score":{"type":"number"}}}`
	result := `{"status":"ok","ready":true,"score":0.75}`
	brief := StepBriefOf(briefTestStep(schema, result))
	wantFields := []BriefField{
		{Name: "status", Value: "ok"}, {Name: "ready", Value: "true"}, {Name: "score", Value: "0.75"},
	}
	if !slices.Equal(brief.Fields, wantFields) {
		t.Fatalf("Fields: got %+v, want %+v", brief.Fields, wantFields)
	}
	briefTestLines(t, brief.Lines(), []string{
		"status: ok", "ready: true", "score: 0.75", "Not shown: none", "More: step_result id s1",
	})
}

func TestStepEventBriefDeriveShortStringsAndNotShown(t *testing.T) {
	t.Parallel()
	schema := `{"type":"object","properties":{"title":{"type":"string"},"headline":{"type":"string"},` +
		`"notes":{"type":"string"},"meta":{"type":"object"},"status":{"type":"string","enum":["ok","bad"]},` +
		`"count":{"type":"integer"},"list":{"type":"array"}}}`
	// title is 120 runes of two bytes each: one line at the cap, shown. headline is 121 runes, so its raw JSON is
	// 123 bytes. notes is multi-line, meta an object, status outside its enum, count not whole, list not an array, and
	// extra is not in the schema.
	title := strings.Repeat("é", 120)
	headline := strings.Repeat("x", 121)
	result := `{"title":"` + title + `","headline":"` + headline + `","notes":"line one\nline two",` +
		`"meta":{"a":1},"status":"weird","count":2.5,"list":"x","extra":1}`
	brief := StepBriefOf(briefTestStep(schema, result))
	wantFields := []BriefField{{Name: "title", Value: title, Short: true}}
	if !slices.Equal(brief.Fields, wantFields) {
		t.Fatalf("Fields: got %+v, want %+v", brief.Fields, wantFields)
	}
	briefTestLines(t, brief.Lines(), []string{
		"title: " + title,
		"Not shown: headline (123 bytes), notes (20 bytes), meta (7 bytes), status (7 bytes), count (3 bytes), " +
			"list (3 bytes), extra (1 bytes)",
		"More: step_result id s1",
	})
}

func TestStepEventBriefDeriveSchemaLessReturn(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("y", 250)
	cases := []struct {
		name   string
		schema string
		result string
		want   []string
	}{
		{
			name:   "empty schema, first line cut",
			result: long + "\nsecond line",
			want: []string{
				"Return: " + strings.Repeat("y", 200) + "...",
				fmt.Sprintf("Not shown: return (%d bytes)", len(long)+len("\nsecond line")),
				"More: step_result id s1",
			},
		},
		{
			name:   "unparseable schema",
			schema: `{"properties":`,
			result: `{"a":1}`,
			want:   []string{`Return: {"a":1}`, "Not shown: none", "More: step_result id s1"},
		},
		{
			name:   "schema without properties",
			schema: `{"type":"object"}`,
			result: `{"a":1}`,
			want:   []string{`Return: {"a":1}`, "Not shown: none", "More: step_result id s1"},
		},
		{
			name:   "return that is not an object, first line cut",
			schema: `{"type":"object","properties":{"answer":{"type":"string"}}}`,
			result: `"` + strings.Repeat("w", 250) + `"`,
			want: []string{
				`Return: "` + strings.Repeat("w", 199) + "...",
				"Not shown: return (252 bytes)",
				"More: step_result id s1",
			},
		},
		{
			name: "empty return is omitted",
			want: []string{"Not shown: none", "More: step_result id s1"},
		},
		{
			name:   "one-line return keeps its spaces",
			result: "  padded answer  ",
			want:   []string{"Return:   padded answer  ", "Not shown: none", "More: step_result id s1"},
		},
		{
			name:   "return followed only by whitespace is cut",
			result: "answer\n   ",
			want:   []string{"Return: answer...", "Not shown: return (10 bytes)", "More: step_result id s1"},
		},
		{
			name:   "return ending in a line break is cut",
			result: "answer\n",
			want:   []string{"Return: answer...", "Not shown: return (7 bytes)", "More: step_result id s1"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			briefTestLines(t, StepBriefOf(briefTestStep(tc.schema, tc.result)).Lines(), tc.want)
		})
	}
}

func TestStepEventBriefDeriveReasonFirstLine(t *testing.T) {
	t.Parallel()
	multi := strings.Repeat("r", 210) + "\nsecond line"
	cases := []struct {
		name   string
		reason string
		want   []string
	}{
		{
			name:   "multi-line reason shows its first line cut",
			reason: multi,
			want: []string{
				fmt.Sprintf("Not shown: reason (%d bytes)", len(multi)),
				"Reason: " + strings.Repeat("r", 200) + "...",
				"More: step_result id s1",
			},
		},
		{
			name:   "one-line reason of 201 runes is cut",
			reason: strings.Repeat("r", 201),
			want: []string{
				"Not shown: reason (201 bytes)",
				"Reason: " + strings.Repeat("r", 200) + "...",
				"More: step_result id s1",
			},
		},
		{
			name:   "one-line reason of 200 runes is whole",
			reason: strings.Repeat("r", 200),
			want: []string{
				"Not shown: none",
				"Reason: " + strings.Repeat("r", 200),
				"More: step_result id s1",
			},
		},
		{
			name:   "reason first line keeps its spaces and is cut before a trailing break and tab",
			reason: " why \n\t",
			want: []string{
				"Not shown: reason (7 bytes)",
				"Reason:  why ...",
				"More: step_result id s1",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			step := StepRow{ID: "s1", Reason: tc.reason}
			briefTestLines(t, StepBriefOf(step).Lines(), tc.want)
		})
	}
}

func TestStepEventBriefDeriveChecksAndMore(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		checks []StepCheck
		want   []string
	}{
		{
			name: "no checks",
			want: []string{"Not shown: none", "More: step_result id s1"},
		},
		{
			name: "two passed and one failed",
			checks: []StepCheck{
				{Round: 1, Name: "fmt", OK: true},
				{Round: 2, Name: "lint", OK: false},
				{Round: 2, Name: "vet", OK: true},
			},
			want: []string{
				"Not shown: none",
				"Checks: 2 passed, 1 failed: lint round 2",
				"More: step_result id s1; step_check id s1 check lint round 2",
			},
		},
		{
			name: "one passed and two failed names the first failed check",
			checks: []StepCheck{
				{Round: 1, Name: "a", OK: false}, {Round: 1, Name: "b", OK: true}, {Round: 2, Name: "c", OK: false},
			},
			want: []string{
				"Not shown: none",
				"Checks: 1 passed, 2 failed: a round 1, c round 2",
				"More: step_result id s1; step_check id s1 check a round 1",
			},
		},
		{
			name:   "all passed names only the result",
			checks: []StepCheck{{Round: 1, Name: "a", OK: true}},
			want:   []string{"Not shown: none", "Checks: 1 passed", "More: step_result id s1"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			step := StepRow{ID: "s1", Checks: tc.checks}
			briefTestLines(t, StepBriefOf(step).Lines(), tc.want)
		})
	}
}
