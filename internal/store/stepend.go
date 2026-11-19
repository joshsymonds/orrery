package store

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// StepEndBlockCap is the most bytes a step_end block may have: its opening tag, its escaped body and its closing tag.
const StepEndBlockCap = 1000

// stepEndLabelCap is the most bytes an epic or task label may have, escaped, and still be an attribute of the tag.
const stepEndLabelCap = 200

// stepEndShown is one line of a step's return as its brief shows it: a field's name and value, or Return and the
// return's text. Short marks a string, whose value a stage may cut.
type stepEndShown struct {
	name, value string
	short       bool
}

// stepEndState is the parts of a step_end block while the block is fitted to StepEndBlockCap. Each *Kept count is how
// many of its parts are still shown; a list with some parts given way ends with a "..." in place of them.
type stepEndState struct {
	event     string
	attrs     []EventAttr
	shown     []stepEndShown
	shownKept int
	omits     []BriefOmit
	omitKept  int
	reason    string
	passed    int
	failed    []StepCheck
	failKept  int
	more      string
}

// StepEndParts returns the tag attributes of step's end block and the lines of its body, unescaped, fitted so that
// the block is at most StepEndBlockCap bytes. The tag's agent, end and status always stay, and so does the More line.
// An epic or task label over stepEndLabelCap bytes, escaped, is left off the tag and named first under Not shown.
// When the block is still over the cap, the parts give way in this order, each only once the ones before it have:
// the Not shown entries from the end; the short strings, the last first, cut to a prefix and "..."; the failed checks
// past those kept, shown as a count; the reason, cut; and the field or Return lines from the end, with one "..." line
// standing for them. The tag and the More line are never cut. Once every part has given way the block is the tag, the
// More line and a few fixed words, so it can exceed the cap only when the agent, the step id or a failed check's name is
// several hundred bytes, which the store does not limit.
func StepEndParts(step StepRow) ([]EventAttr, []string) {
	attrs := []EventAttr{
		{Name: "agent", Value: step.Agent}, {Name: "end", Value: step.End}, {Name: "status", Value: step.Status},
	}
	brief := StepBriefOf(step)
	var labels []BriefOmit
	for _, label := range []EventAttr{{Name: "epic", Value: step.Epic}, {Name: "task", Value: step.Task}} {
		if label.Value == "" {
			continue
		}
		if len(escapeEvent(label.Value)) <= stepEndLabelCap {
			attrs = append(attrs, label)
		} else {
			labels = append(labels, BriefOmit{Name: label.Name, Bytes: len(label.Value)})
		}
	}
	brief.NotShown = append(labels, brief.NotShown...)
	st := stepEndNew(attrs, brief)
	stepEndFit(st)
	return st.attrs, stepEndLines(st)
}

// StepEndBlock returns the block that tells a caller of step's end: the step_end event of the step, whose tag carries
// the attributes of StepEndParts and whose body is the lines of its brief. The block is at most StepEndBlockCap bytes.
func StepEndBlock(step StepRow) string {
	attrs, lines := StepEndParts(step)
	return CallerEventBlock(CallerKindStepEnd, stepEventPrefix+step.ID, attrs, strings.Join(lines, "\n"))
}

// stepEndNew returns the state of a block with attrs whose body is brief, with every part shown.
func stepEndNew(attrs []EventAttr, brief StepBrief) *stepEndState {
	st := &stepEndState{
		event: stepEventPrefix + brief.ID, attrs: attrs, omits: brief.NotShown, omitKept: len(brief.NotShown),
		reason: brief.Reason, passed: brief.Passed, failed: brief.Failed, failKept: len(brief.Failed),
	}
	for _, field := range brief.Fields {
		st.shown = append(st.shown, stepEndShown{name: field.Name, value: field.Value, short: field.Short})
	}
	if brief.Return != "" {
		st.shown = append(st.shown, stepEndShown{name: "Return", value: brief.Return, short: true})
	}
	st.shownKept = len(st.shown)
	// The last line of the brief is its More line, which the block keeps whole.
	lines := brief.Lines()
	st.more = lines[len(lines)-1]
	return st
}

// stepEndFit gives way the parts of st, one stage at a time, until the block fits. Each stage changes only what the
// stages before it have left, and stops as soon as the block fits.
func stepEndFit(st *stepEndState) {
	st.omitKept = stepEndKeep(len(st.omits), func(k int) bool {
		st.omitKept = k
		return stepEndFits(st)
	})
	for i := len(st.shown) - 1; i >= 0 && !stepEndFits(st); i-- {
		if st.shown[i].short {
			st.shown[i].value = stepEndCut(st.shown[i].value, func(v string) bool {
				st.shown[i].value = v
				return stepEndFits(st)
			})
		}
	}
	if len(st.failed) > 0 {
		st.failKept = stepEndKeep(len(st.failed), func(k int) bool {
			st.failKept = k
			return stepEndFits(st)
		})
	}
	if st.reason != "" && !stepEndFits(st) {
		st.reason = stepEndCut(st.reason, func(v string) bool {
			st.reason = v
			return stepEndFits(st)
		})
	}
	st.shownKept = stepEndKeep(len(st.shown), func(k int) bool {
		st.shownKept = k
		return stepEndFits(st)
	})
}

// stepEndKeep returns how many of n parts a stage keeps: all n when fitsWith holds for n, otherwise the largest count
// below n that fitsWith holds for, or 0 when none does. A shorter list makes a shorter block, so fitsWith holds for the
// counts up to some one, and the largest is found by bisection.
func stepEndKeep(n int, fitsWith func(int) bool) int {
	if fitsWith(n) {
		return n
	}
	// The first count that does not fit comes after the largest that does; with none that fits, that is 0.
	first := sort.Search(n, func(k int) bool { return !fitsWith(k) })
	return max(first-1, 0)
}

// stepEndCut returns text cut to its longest rune prefix followed by "..." that fitsWith holds for, or "..." when even
// the empty prefix does not fit. Text that is empty is returned as it is. fitsWith sets the candidate it is given on
// the state and reports whether the block fits with it, so the caller sets the value returned.
func stepEndCut(text string, fitsWith func(string) bool) string {
	if text == "" {
		return text
	}
	bounds := stepEndRuneBounds(text)
	if !fitsWith("...") {
		return "..."
	}
	// The first prefix that does not fit comes after the longest that does, which is at least the empty one.
	runes := len(bounds) - 1
	first := sort.Search(runes, func(m int) bool { return !fitsWith(text[:bounds[m]] + "...") })
	return text[:bounds[first-1]] + "..."
}

// stepEndRuneBounds returns the byte offset of each rune boundary of text, from 0 to len(text). A byte that is not
// UTF-8 counts as a rune of its own, so a cut keeps the bytes of text.
func stepEndRuneBounds(text string) []int {
	bounds := []int{0}
	for i := 0; i < len(text); {
		_, size := utf8.DecodeRuneInString(text[i:])
		i += size
		bounds = append(bounds, i)
	}
	return bounds
}

// stepEndFits reports whether the block of st is at most StepEndBlockCap bytes.
func stepEndFits(st *stepEndState) bool {
	body := strings.Join(stepEndLines(st), "\n")
	return len(CallerEventBlock(CallerKindStepEnd, st.event, st.attrs, body)) <= StepEndBlockCap
}

// stepEndLines returns the lines of the body of st, unescaped, in the order the brief gives: the fields or the Return,
// then the Not shown line, the Reason, the Checks and the More line.
func stepEndLines(st *stepEndState) []string {
	lines := make([]string, 0, len(st.shown)+4)
	for _, shown := range st.shown[:st.shownKept] {
		lines = append(lines, shown.name+": "+shown.value)
	}
	if st.shownKept < len(st.shown) {
		lines = append(lines, "...")
	}
	lines = append(lines, stepEndNotShown(st))
	if st.reason != "" {
		lines = append(lines, "Reason: "+st.reason)
	}
	if st.passed+len(st.failed) > 0 {
		lines = append(lines, stepEndChecks(st))
	}
	return append(lines, st.more)
}

// stepEndNotShown returns the Not shown line of st: its entries kept, then "..." when some are given way.
func stepEndNotShown(st *stepEndState) string {
	if len(st.omits) == 0 {
		return "Not shown: none"
	}
	parts := make([]string, 0, st.omitKept+1)
	for _, omit := range st.omits[:st.omitKept] {
		parts = append(parts, fmt.Sprintf("%s (%d bytes)", omit.Name, omit.Bytes))
	}
	if st.omitKept < len(st.omits) {
		parts = append(parts, "...")
	}
	return "Not shown: " + strings.Join(parts, ", ")
}

// stepEndChecks returns the Checks line of st. The failing checks past those kept are a count, and the total failed
// stays the count before the colon.
func stepEndChecks(st *stepEndState) string {
	line := fmt.Sprintf("Checks: %d passed", st.passed)
	if len(st.failed) == 0 {
		return line
	}
	names := make([]string, 0, st.failKept)
	for _, check := range st.failed[:st.failKept] {
		names = append(names, fmt.Sprintf("%s round %d", check.Name, check.Round))
	}
	line += fmt.Sprintf(", %d failed: ", len(st.failed))
	switch {
	case st.failKept == len(st.failed):
		return line + strings.Join(names, ", ")
	case len(names) == 0:
		return line + fmt.Sprintf("... (%d more)", len(st.failed))
	default:
		return line + strings.Join(names, ", ") + fmt.Sprintf(", ... (%d more)", len(st.failed)-st.failKept)
	}
}
