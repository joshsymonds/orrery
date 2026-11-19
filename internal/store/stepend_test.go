package store_test

import (
	"fmt"
	"runtime"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/joshsymonds/orrery/internal/store"
)

// stepEndTestOpen is the opening tag of a step_end block of step s1 by the implementer agent, ended done and passed,
// with no epic or task.
const stepEndTestOpen = `<orrery-event kind="step_end" id="step:s1" agent="implementer" end="done" status="passed">`

// stepEndTestMore is the More line of step s1 when no check failed.
const stepEndTestMore = "More: step_result id s1"

// stepEndTestCheckMore is the More line of step s1 when check-00 round 1 is the first failed check.
const stepEndTestCheckMore = "More: step_result id s1; step_check id s1 check check-00 round 1"

// stepEndTestBlock returns the block of the opening tag open with the body lines, and its closing tag.
func stepEndTestBlock(open string, lines ...string) string {
	return open + "\n" + strings.Join(lines, "\n") + "\n</orrery-event>"
}

// stepEndTestFailed returns n failed checks of round 1, named check-00 to check-(n-1).
func stepEndTestFailed(n int) []store.StepCheck {
	checks := make([]store.StepCheck, 0, n)
	for i := range n {
		checks = append(checks, store.StepCheck{Round: 1, Name: fmt.Sprintf("check-%02d", i)})
	}
	return checks
}

// stepEndTestChecksLine returns the Checks line of passed passed checks and failed failing ones, of which the first
// kept are named, and the rest are a count.
func stepEndTestChecksLine(passed, failed, kept int) string {
	names := make([]string, 0, kept)
	for i := range kept {
		names = append(names, fmt.Sprintf("check-%02d round 1", i))
	}
	head := fmt.Sprintf("Checks: %d passed, %d failed: ", passed, failed)
	switch kept {
	case failed:
		return head + strings.Join(names, ", ")
	case 0:
		return head + fmt.Sprintf("... (%d more)", failed)
	default:
		return head + strings.Join(names, ", ") + fmt.Sprintf(", ... (%d more)", failed-kept)
	}
}

// stepEndTestBody returns the lines between the opening tag and the closing tag of block.
func stepEndTestBody(block string) []string {
	lines := strings.Split(block, "\n")
	return lines[1 : len(lines)-1]
}

// stepEndTestLine returns the first line of body that starts with prefix, or "" when none does.
func stepEndTestLine(body []string, prefix string) string {
	for _, line := range body {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	return ""
}

// TestStepEventBriefTagLabeledBlock: a step with an epic and a task carries both in its tag after agent, end and
// status, and its body is the lines of its brief.
func TestStepEventBriefTagLabeledBlock(t *testing.T) {
	t.Parallel()
	step := store.StepRow{
		ID: "s1", Agent: "implementer", End: "done", Status: "passed", Epic: "epic-1", Task: "task-1",
		Result: "all green", Reason: "gate green",
	}
	want := stepEndTestBlock(
		`<orrery-event kind="step_end" id="step:s1" agent="implementer" end="done" status="passed"`+
			` epic="epic-1" task="task-1">`,
		"Return: all green", "Not shown: none", "Reason: gate green", stepEndTestMore,
	)
	if got := store.StepEndBlock(step); got != want {
		t.Fatalf("StepEndBlock = %q, want %q", got, want)
	}
}

// TestStepEventBriefTagUnlabeledHasNoLabels: a step with no epic and no task has a tag with only agent, end and status.
func TestStepEventBriefTagUnlabeledHasNoLabels(t *testing.T) {
	t.Parallel()
	step := store.StepRow{ID: "s1", Agent: "reviewer", End: "done", Status: "passed", Result: "all green"}
	want := stepEndTestBlock(
		`<orrery-event kind="step_end" id="step:s1" agent="reviewer" end="done" status="passed">`,
		"Return: all green", "Not shown: none", stepEndTestMore,
	)
	if got := store.StepEndBlock(step); got != want {
		t.Fatalf("StepEndBlock = %q, want %q", got, want)
	}
}

// TestStepEventBriefTagLabelLimitCountsEscapedBytes: an epic whose escaped value is over 200 bytes is left off the tag
// and named under Not shown by its raw length, while a task of exactly 200 bytes stays on the tag.
func TestStepEventBriefTagLabelLimitCountsEscapedBytes(t *testing.T) {
	t.Parallel()
	step := store.StepRow{
		ID: "s1", Agent: "implementer", End: "done", Status: "passed",
		Epic: strings.Repeat(`"`, 60), Task: strings.Repeat("t", 200), Result: "all green",
	}
	want := stepEndTestBlock(
		`<orrery-event kind="step_end" id="step:s1" agent="implementer" end="done" status="passed" task="`+
			strings.Repeat("t", 200)+`">`,
		"Return: all green", "Not shown: epic (60 bytes)", stepEndTestMore,
	)
	if got := store.StepEndBlock(step); got != want {
		t.Fatalf("StepEndBlock = %q, want %q", got, want)
	}
}

// TestStepEventBriefTagEscapesEveryValue: & < > " in the agent, epic, task, a short string field, the reason and a
// schema-less return come out escaped, and the block holds exactly one closing tag, at its end.
func TestStepEventBriefTagEscapesEveryValue(t *testing.T) {
	t.Parallel()
	const esc = `&amp;&lt;&gt;&quot;`
	const raw = `&<>"`
	withField := store.StepRow{
		ID: "s1", Agent: "a" + raw, End: "done", Status: "passed", Epic: "e" + raw, Task: "t" + raw,
		Schema: `{"type":"object","properties":{"note":{"type":"string"}}}`, Result: `{"note":"n&<>\""}`,
		Reason: "r" + raw,
	}
	withReturn := store.StepRow{
		ID: "s1", Agent: "a" + raw, End: "done", Status: "passed", Result: "r" + raw, Reason: "gate green",
	}
	for _, tc := range []struct {
		name string
		step store.StepRow
		want string
	}{
		{
			name: "field and reason with labels",
			step: withField,
			want: stepEndTestBlock(
				`<orrery-event kind="step_end" id="step:s1" agent="a`+esc+`" end="done" status="passed" epic="e`+esc+
					`" task="t`+esc+`">`,
				"note: n"+esc, "Not shown: none", "Reason: r"+esc, stepEndTestMore,
			),
		},
		{
			name: "schema-less return",
			step: withReturn,
			want: stepEndTestBlock(
				`<orrery-event kind="step_end" id="step:s1" agent="a`+esc+`" end="done" status="passed">`,
				"Return: r"+esc, "Not shown: none", "Reason: gate green", stepEndTestMore,
			),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := store.StepEndBlock(tc.step)
			if got != tc.want {
				t.Fatalf("StepEndBlock = %q, want %q", got, tc.want)
			}
			if n := strings.Count(got, "</orrery-event>"); n != 1 || !strings.HasSuffix(got, "\n</orrery-event>") {
				t.Fatalf("closing tags = %d, block ends %q; want one, at its end", n, got[max(0, len(got)-20):])
			}
		})
	}
}

// TestStepEventBriefBoundOversizedRows: each oversized step's block is at most StepEndBlockCap bytes, is valid UTF-8,
// begins with its exact opening tag, and ends its body with its exact More line.
func TestStepEventBriefBoundOversizedRows(t *testing.T) {
	t.Parallel()
	passedThenFailed := append([]store.StepCheck{
		{Round: 1, Name: "fmt", OK: true}, {Round: 1, Name: "vet", OK: true},
	}, stepEndTestFailed(50)...)
	// The schema of the omitted rows: two known properties, status and summary, and the rest omitted.
	schemaWithKnown := `{"type":"object","properties":{"status":{"type":"string","enum":["ok","bad"]},` +
		`"summary":{"type":"string"}}}`
	var omitted strings.Builder
	omittedNames := make([]string, 0, 44)
	for i := range 60 {
		fmt.Fprintf(&omitted, `,"k%02d":"%s"`, i, strings.Repeat("x", 15))
		if i < 44 {
			omittedNames = append(omittedNames, fmt.Sprintf("k%02d (17 bytes)", i))
		}
	}
	// The 4 two-byte strings of 120 runes: the first three whole, the last cut to 55 runes.
	twoByte := strings.Repeat("é", 120)
	cases := []struct {
		name string
		step store.StepRow
		open string
		want []string
	}{
		{
			name: "20 KB one-line result and 16 KB multi-line reason",
			step: store.StepRow{
				ID: "s1", Agent: "implementer", End: "done", Status: "passed",
				Result: strings.Repeat("r", 20000), Reason: strings.Repeat("s", 16000) + "\nsecond line",
			},
			open: stepEndTestOpen,
			want: []string{
				"Return: " + strings.Repeat("r", 200) + "...",
				fmt.Sprintf("Not shown: return (20000 bytes), reason (%d bytes)", 16000+len("\nsecond line")),
				"Reason: " + strings.Repeat("s", 200) + "...",
				stepEndTestMore,
			},
		},
		{
			name: "50 failed checks",
			step: store.StepRow{
				ID: "s1", Agent: "implementer", End: "done", Status: "passed", Reason: "gate green",
				Checks: passedThenFailed,
			},
			open: stepEndTestOpen,
			want: []string{
				"Not shown: none", "Reason: gate green", stepEndTestChecksLine(2, 50, 41), stepEndTestCheckMore,
			},
		},
		{
			name: "epic and task of 2 KB each",
			step: store.StepRow{
				ID: "s1", Agent: "implementer", End: "done", Status: "passed",
				Epic: strings.Repeat("e", 2048), Task: strings.Repeat("t", 2048), Result: "all green",
				Reason: "gate green",
			},
			open: stepEndTestOpen,
			want: []string{
				"Return: all green", "Not shown: epic (2048 bytes), task (2048 bytes)", "Reason: gate green",
				stepEndTestMore,
			},
		},
		{
			name: "schema'd return with 60 omitted fields",
			step: store.StepRow{
				ID: "s1", Agent: "implementer", End: "done", Status: "passed", Schema: schemaWithKnown,
				Result: `{"status":"ok","summary":"` + strings.Repeat("s", 120) + `"` + omitted.String() + `}`,
			},
			open: stepEndTestOpen,
			want: []string{
				"status: ok", "summary: " + strings.Repeat("s", 120),
				"Not shown: " + strings.Join(omittedNames, ", ") + ", ...", stepEndTestMore,
			},
		},
		{
			name: "four 120-rune multibyte strings",
			step: store.StepRow{
				ID: "s1", Agent: "implementer", End: "done", Status: "passed",
				Schema: `{"type":"object","properties":{"p1":{"type":"string"},"p2":{"type":"string"},` +
					`"p3":{"type":"string"},"p4":{"type":"string"}}}`,
				Result: `{"p1":"` + twoByte + `","p2":"` + twoByte + `","p3":"` + twoByte + `","p4":"` + twoByte + `"}`,
			},
			open: stepEndTestOpen,
			want: []string{
				"p1: " + twoByte, "p2: " + twoByte, "p3: " + twoByte,
				"p4: " + strings.Repeat("é", 55) + "...", "Not shown: none", stepEndTestMore,
			},
		},
		{
			name: "reason cut once the return and the checks have given way",
			step: store.StepRow{
				ID: "s1", Agent: "implementer", End: "done", Status: "passed",
				Result: strings.Repeat("r", 250), Reason: strings.Repeat("🙂", 200) + "\nsecond line",
				Checks: append([]store.StepCheck{{Round: 1, Name: "fmt", OK: true}}, stepEndTestFailed(50)...),
			},
			open: stepEndTestOpen,
			want: []string{
				"Return: ...", "Not shown: ...", "Reason: " + strings.Repeat("🙂", 186) + "...",
				stepEndTestChecksLine(1, 50, 0), stepEndTestCheckMore,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := store.StepEndBlock(tc.step)
			if len(got) > store.StepEndBlockCap {
				t.Fatalf("block is %d bytes, want at most %d", len(got), store.StepEndBlockCap)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("block is not valid UTF-8: %q", got)
			}
			if !strings.HasPrefix(got, tc.open+"\n") {
				t.Fatalf("block opens %q, want %q", got, tc.open)
			}
			body := stepEndTestBody(got)
			if last := body[len(body)-1]; last != tc.want[len(tc.want)-1] {
				t.Fatalf("last line = %q, want %q", last, tc.want[len(tc.want)-1])
			}
			if want := stepEndTestBlock(tc.open, tc.want...); got != want {
				t.Fatalf("StepEndBlock = %q, want %q", got, want)
			}
		})
	}
}

// TestStepEventBriefBoundStagesGiveWayInOrder: a step with every part, as the reason grows from one rune to 200, gives
// way in the order of the stages. A part is given way only once the parts before it are: the Not shown entries, then
// the short strings, then the failed checks, then the reason. Each stage is reached by some length of the reason.
func TestStepEventBriefBoundStagesGiveWayInOrder(t *testing.T) {
	t.Parallel()
	schema := `{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"string"}}}`
	var members strings.Builder
	for i := range 30 {
		fmt.Fprintf(&members, `,"k%02d":"%s"`, i, strings.Repeat("x", 15))
	}
	result := `{"a":"` + strings.Repeat("x", 100) + `","b":"` + strings.Repeat("y", 100) + `"` + members.String() + `}`
	fullA, fullB := "a: "+strings.Repeat("x", 100), "b: "+strings.Repeat("y", 100)
	fullChecks := stepEndTestChecksLine(1, 10, 10)
	stepChecks := append([]store.StepCheck{{Round: 1, Name: "fmt", OK: true}}, stepEndTestFailed(10)...)
	var reached struct{ notShown, shorts, checks, reason bool }
	for runes := 1; runes <= 200; runes++ {
		reason := strings.Repeat("🙂", runes)
		step := store.StepRow{
			ID: "s1", Agent: "implementer", End: "done", Status: "passed", Schema: schema, Result: result,
			Reason: reason, Checks: stepChecks,
		}
		got := store.StepEndBlock(step)
		if len(got) > store.StepEndBlockCap {
			t.Fatalf("reason of %d runes: block is %d bytes, want at most %d", runes, len(got), store.StepEndBlockCap)
		}
		body := stepEndTestBody(got)
		notShown := stepEndTestLine(body, "Not shown: ")
		a, b := stepEndTestLine(body, "a: "), stepEndTestLine(body, "b: ")
		checks := stepEndTestLine(body, "Checks: ")
		gaveNotShown := notShown == "Not shown: ..."
		shortCut := a != fullA || b != fullB
		shortsGone := a == "a: ..." && b == "b: ..."
		checksCut := checks != fullChecks
		checksGone := checks == stepEndTestChecksLine(1, 10, 0)
		reasonCut := stepEndTestLine(body, "Reason: ") != "Reason: "+reason
		if shortCut && !gaveNotShown {
			t.Fatalf("reason of %d runes: a short string is cut before the Not shown entries are", runes)
		}
		if checksCut && (!gaveNotShown || !shortsGone) {
			t.Fatalf("reason of %d runes: the checks are cut before the Not shown entries and short strings are", runes)
		}
		if reasonCut && !checksGone {
			t.Fatalf("reason of %d runes: the reason is cut before the checks have given way", runes)
		}
		reached.notShown = reached.notShown || gaveNotShown
		reached.shorts = reached.shorts || shortCut
		reached.checks = reached.checks || checksCut
		reached.reason = reached.reason || reasonCut
	}
	if !reached.notShown || !reached.shorts || !reached.checks || !reached.reason {
		t.Fatalf("stages reached = %+v, want every stage reached by some reason length", reached)
	}
}

// stepEndTestRepeated returns a JSON object of n copies of member, n being at least 1.
func stepEndTestRepeated(member string, n int) string {
	return "{" + strings.Repeat(member+",", n-1) + member + "}"
}

// stepEndTestBlockAllocs returns the block of step and the bytes allocated while StepEndBlock made it.
func stepEndTestBlockAllocs(step store.StepRow) (string, uint64) {
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	block := store.StepEndBlock(step)
	runtime.ReadMemStats(&after)
	return block, after.TotalAlloc - before.TotalAlloc
}

// TestStepEventBriefBoundManyFieldsShortStrings: a return of 2000 members, each a short string field, is briefed in at
// most 1 MiB plus 4 KiB per member, and its block is bounded, ends in its More line and opens with a field line.
func TestStepEventBriefBoundManyFieldsShortStrings(t *testing.T) {
	const members = 2000
	step := store.StepRow{
		ID: "s1", Agent: "implementer", End: "done", Status: "passed",
		Schema: `{"type":"object","properties":{"a":{"type":"string"}}}`,
		Result: stepEndTestRepeated(`"a":"`+strings.Repeat("x", 100)+`"`, members),
	}
	block, allocated := stepEndTestBlockAllocs(step)
	if limit := uint64(1<<20 + 4096*members); allocated > limit {
		t.Fatalf("StepEndBlock allocated %d bytes, want at most %d", allocated, limit)
	}
	if len(block) > store.StepEndBlockCap {
		t.Fatalf("block is %d bytes, want at most %d", len(block), store.StepEndBlockCap)
	}
	body := stepEndTestBody(block)
	if last := body[len(body)-1]; last != stepEndTestMore {
		t.Fatalf("last line = %q, want %q", last, stepEndTestMore)
	}
	if !strings.HasPrefix(body[0], "a: ") {
		t.Fatalf("first line = %q, want a field line", body[0])
	}
}

// TestStepEventBriefBoundManyFieldsScalars: a return of 5000 members, each an enum scalar field, is briefed in at most
// 1 MiB plus 4 KiB per member, and its block is bounded, holds a "..." line for the dropped fields and ends in its More
// line.
func TestStepEventBriefBoundManyFieldsScalars(t *testing.T) {
	const members = 5000
	step := store.StepRow{
		ID: "s1", Agent: "implementer", End: "done", Status: "passed",
		Schema: `{"type":"object","properties":{"v":{"enum":["yes","no"]}}}`,
		Result: stepEndTestRepeated(`"v":"yes"`, members),
	}
	block, allocated := stepEndTestBlockAllocs(step)
	if limit := uint64(1<<20 + 4096*members); allocated > limit {
		t.Fatalf("StepEndBlock allocated %d bytes, want at most %d", allocated, limit)
	}
	if len(block) > store.StepEndBlockCap {
		t.Fatalf("block is %d bytes, want at most %d", len(block), store.StepEndBlockCap)
	}
	body := stepEndTestBody(block)
	if last := body[len(body)-1]; last != stepEndTestMore {
		t.Fatalf("last line = %q, want %q", last, stepEndTestMore)
	}
	if !slices.Contains(body, "...") {
		t.Fatalf("body %q has no ... line for the dropped fields", body)
	}
}
