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
//
// The block is never built to be measured. Its length is frame plus units: frame is its opening tag, the newline after
// it and its closing tag, and units is the sum over its body lines of each line's units (see stepEndLineUnit). units
// starts as the total of the body as stepEndNew leaves it, and each stage moves it by the lines it changes. A stage
// takes the lines it does not change as fixed while it runs, so a probe costs only the units of the line it sets: a
// count probes the prefix sums of its lines' units, built once for the stage (see stepEndPrefix), and a cut costs the
// length of each candidate it tries.
func stepEndFit(st *stepEndState) {
	// CallerEventBlock of an empty body is frame plus the units of one empty line, which is its newline.
	frame := len(CallerEventBlock(CallerKindStepEnd, st.event, st.attrs, "")) - 1
	fits := func(units int) bool { return frame+units <= StepEndBlockCap }
	omitSums := stepEndOmitSums(st.omits)
	failSums := stepEndCheckSums(st.failed)
	// Every part is shown when fitting starts, as stepEndNew leaves it.
	units := stepEndLineUnit(st.more) + stepEndChecksUnit(st, failSums, st.failKept) +
		stepEndNotShownUnit(omitSums, st.omitKept)
	if st.reason != "" {
		units += stepEndReasonUnit(st.reason)
	}
	for _, shown := range st.shown {
		units += stepEndShownUnit(shown.name, shown.value)
	}

	// The Not shown entries, from the end.
	base := units - stepEndNotShownUnit(omitSums, st.omitKept)
	st.omitKept = stepEndKeep(len(st.omits), func(k int) bool {
		return fits(base + stepEndNotShownUnit(omitSums, k))
	})
	units = base + stepEndNotShownUnit(omitSums, st.omitKept)

	// The short strings, the last first.
	for i := len(st.shown) - 1; i >= 0 && !fits(units); i-- {
		shown := &st.shown[i]
		if !shown.short {
			continue
		}
		others := units - stepEndShownUnit(shown.name, shown.value)
		shown.value = stepEndCut(shown.value, func(kept string) bool {
			return fits(others + stepEndShownUnit(shown.name, kept) + len("..."))
		})
		units = others + stepEndShownUnit(shown.name, shown.value)
	}

	// The failing checks past those kept.
	if len(st.failed) > 0 {
		base = units - stepEndChecksUnit(st, failSums, st.failKept)
		st.failKept = stepEndKeep(len(st.failed), func(k int) bool {
			return fits(base + stepEndChecksUnit(st, failSums, k))
		})
		units = base + stepEndChecksUnit(st, failSums, st.failKept)
	}

	// The reason, cut.
	if st.reason != "" && !fits(units) {
		base = units - stepEndReasonUnit(st.reason)
		st.reason = stepEndCut(st.reason, func(kept string) bool {
			return fits(base + stepEndReasonUnit(kept) + len("..."))
		})
		units = base + stepEndReasonUnit(st.reason)
	}

	// The field or Return lines, from the end, with the "..." line that stands for those given way. Their values are
	// final now, so their units are summed afresh.
	sums := stepEndPrefix(len(st.shown), func(i int) int {
		return stepEndShownUnit(st.shown[i].name, st.shown[i].value)
	})
	base = units - sums[len(st.shown)]
	st.shownKept = stepEndKeep(len(st.shown), func(k int) bool {
		return fits(base + stepEndShownPart(sums, k))
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
// the empty prefix does not fit. Text that is empty is returned as it is. fitsWith reports whether the block fits with
// the prefix it is given in place of text, followed by "...", and the caller sets the value returned.
func stepEndCut(text string, fitsWith func(kept string) bool) string {
	if text == "" {
		return text
	}
	if !fitsWith("") {
		return "..."
	}
	// The first prefix that does not fit comes after the longest that does, which is at least the empty one. The
	// prefixes are bisected by their rune count, and each is found by walking to it, so a probe costs the length of text
	// and no probe allocates.
	runes := stepEndRuneCount(text)
	first := sort.Search(runes, func(m int) bool { return !fitsWith(text[:stepEndRuneOffset(text, m)]) })
	return text[:stepEndRuneOffset(text, first-1)] + "..."
}

// stepEndRuneCount returns how many runes text has, where a byte that is not UTF-8 counts as a rune of its own, so a
// cut keeps the bytes of text.
func stepEndRuneCount(text string) int {
	n := 0
	for i := 0; i < len(text); n++ {
		_, size := utf8.DecodeRuneInString(text[i:])
		i += size
	}
	return n
}

// stepEndRuneOffset returns the byte offset just after the first m runes of text, counted as stepEndRuneCount counts
// them. m is at most the rune count of text.
func stepEndRuneOffset(text string, m int) int {
	offset := 0
	for range m {
		_, size := utf8.DecodeRuneInString(text[offset:])
		offset += size
	}
	return offset
}

// stepEndEscapedLen returns the length of s once escapeEvent has escaped it: each & < > " takes the bytes of its
// entity, and every other byte is kept. It counts the bytes escapeEvent would write, without writing them.
func stepEndEscapedLen(s string) int {
	n := len(s)
	for i := range len(s) {
		switch s[i] {
		case '&':
			n += len("&amp;") - 1
		case '<', '>':
			n += len("&lt;") - 1
		case '"':
			n += len("&quot;") - 1
		}
	}
	return n
}

// stepEndLineUnit returns the units of a body line that reads as text: its escaped length and the newline after it.
// The newline after the block's tag is in frame, not here.
func stepEndLineUnit(text string) int {
	return stepEndEscapedLen(text) + 1
}

// stepEndShownUnit returns the units of the line of a field or the Return: its name and value joined by ": ". A value
// cut to a kept prefix and "..." adds len("...") to the units of the kept prefix.
func stepEndShownUnit(name, value string) int {
	return stepEndEscapedLen(name) + len(": ") + stepEndEscapedLen(value) + 1
}

// stepEndReasonUnit returns the units of the Reason line of reason, which the caller leaves off when reason is empty.
// A reason cut to a kept prefix and "..." adds len("...") to the units of the kept prefix.
func stepEndReasonUnit(reason string) int {
	return stepEndEscapedLen("Reason: ") + stepEndEscapedLen(reason) + 1
}

// stepEndNotShownUnit returns the units of the Not shown line when its first k entries are kept. sums holds the prefix
// sums of the entries' escaped lengths (see stepEndPrefix), so it has one more entry than there are entries. The line
// joins the kept entries, then "..." when some are given way, by ", ".
func stepEndNotShownUnit(sums []int, k int) int {
	n := len(sums) - 1
	if n == 0 {
		return stepEndLineUnit("Not shown: none")
	}
	parts, sum := k, sums[k]
	if k < n {
		parts++
		sum += stepEndEscapedLen("...")
	}
	return stepEndEscapedLen("Not shown: ") + stepEndJoined(sum, parts, len(", ")) + 1
}

// stepEndChecksUnit returns the units of the Checks line of st when its first k failing checks are named. sums holds
// the prefix sums of the escaped lengths of the names (see stepEndPrefix). The line is left off when st has no checks.
// The line has the same text as stepEndChecks gives it.
func stepEndChecksUnit(st *stepEndState, sums []int, k int) int {
	failed := len(st.failed)
	if st.passed+failed == 0 {
		return 0
	}
	head := fmt.Sprintf("Checks: %d passed", st.passed)
	if failed > 0 {
		head += fmt.Sprintf(", %d failed: ", failed)
	}
	if k == failed {
		return stepEndEscapedLen(head) + stepEndJoined(sums[failed], failed, len(", ")) + 1
	}
	// The names kept, then the count of those given way, which is the last part of the line.
	tail := fmt.Sprintf("... (%d more)", failed-k)
	return stepEndEscapedLen(head) + stepEndJoined(sums[k]+stepEndEscapedLen(tail), k+1, len(", ")) + 1
}

// stepEndShownPart returns the units of the first k field or Return lines, given the prefix sums of their units (see
// stepEndPrefix), and of the "..." line when some are given way.
func stepEndShownPart(sums []int, k int) int {
	if k < len(sums)-1 {
		return sums[k] + stepEndLineUnit("...")
	}
	return sums[k]
}

// stepEndJoined returns the length of parts pieces totalling sum bytes, joined by a separator sep bytes long.
func stepEndJoined(sum, parts, sep int) int {
	if parts == 0 {
		return 0
	}
	return sum + sep*(parts-1)
}

// stepEndPrefix returns the prefix sums of n lengths: sums[k] is the sum of the first k of them, so sums has n+1 entries.
func stepEndPrefix(n int, length func(i int) int) []int {
	sums := make([]int, n+1)
	for i := range n {
		sums[i+1] = sums[i] + length(i)
	}
	return sums
}

// stepEndOmitSums returns the prefix sums of the escaped lengths of the Not shown entries of omits.
func stepEndOmitSums(omits []BriefOmit) []int {
	return stepEndPrefix(len(omits), func(i int) int { return stepEndEscapedLen(stepEndOmitEntry(omits[i])) })
}

// stepEndCheckSums returns the prefix sums of the escaped lengths of the names of failed checks on the Checks line.
func stepEndCheckSums(failed []StepCheck) []int {
	return stepEndPrefix(len(failed), func(i int) int { return stepEndEscapedLen(stepEndCheckName(failed[i])) })
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

// stepEndOmitEntry returns an entry of the Not shown line: the name of a part and its raw size.
func stepEndOmitEntry(omit BriefOmit) string {
	return fmt.Sprintf("%s (%d bytes)", omit.Name, omit.Bytes)
}

// stepEndCheckName returns a failing check as the Checks line names it: its name and its round.
func stepEndCheckName(check StepCheck) string {
	return fmt.Sprintf("%s round %d", check.Name, check.Round)
}

// stepEndNotShown returns the Not shown line of st: its entries kept, then "..." when some are given way.
func stepEndNotShown(st *stepEndState) string {
	if len(st.omits) == 0 {
		return "Not shown: none"
	}
	parts := make([]string, 0, st.omitKept+1)
	for _, omit := range st.omits[:st.omitKept] {
		parts = append(parts, stepEndOmitEntry(omit))
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
		names = append(names, stepEndCheckName(check))
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
