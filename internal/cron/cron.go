// Package cron parses five-field cron expressions in Claude Code's dialect and finds the next time one of them matches.
package cron

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// fieldCount is the number of fields in an expression: minute, hour, day of month, month and day of week.
const fieldCount = 5

// searchYears bounds Next: a schedule that matches nothing in the five years after a time matches nothing at all.
const searchYears = 5

// sundayAlias is the day-of-week value that means Sunday as well as 0.
const sundayAlias = 7

// The largest value of each field. The smallest is 0, or 1 for the day of month and the month.
const (
	maxMinute     = 59
	maxHour       = 23
	maxDayOfMonth = 31
	maxMonth      = 12
)

// digits is the set of characters that make up a decimal number.
const digits = "0123456789"

// Schedule is a parsed cron expression. It keeps one bit per value that each field matches. The zero Schedule matches
// nothing.
type Schedule struct {
	minute, hour, dom, month, dow uint64
	// domRestricted and dowRestricted report that the day-of-month or day-of-week field is not a bare "*".
	domRestricted, dowRestricted bool
}

// fieldSpec describes one field: its name in errors and the smallest and largest values it accepts.
type fieldSpec struct {
	name   string
	lo, hi int
}

// Parse reads a cron expression of five whitespace-separated fields: minute (0-59), hour (0-23), day of month (1-31),
// month (1-12) and day of week (0-7, where 0 and 7 are both Sunday). Each field is a comma list of items. An item is
// *, a value, a range a-b, or a step: */n or a-b/n. Names such as MON are not accepted. Parse refuses an expression
// that does not have exactly five fields, a value outside its field's range, a reversed range, a step that is not a
// positive number, a step on a bare value, and an empty list item. Its error names the field and the reason.
func Parse(expr string) (Schedule, error) {
	parts := strings.Fields(expr)
	if len(parts) != fieldCount {
		return Schedule{}, fmt.Errorf(
			"cron needs %d fields (minute, hour, day of month, month, day of week), got %d", fieldCount, len(parts))
	}
	specs := [fieldCount]fieldSpec{
		{name: "minute", lo: 0, hi: maxMinute},
		{name: "hour", lo: 0, hi: maxHour},
		{name: "day of month", lo: 1, hi: maxDayOfMonth},
		{name: "month", lo: 1, hi: maxMonth},
		{name: "day of week", lo: 0, hi: sundayAlias},
	}
	var masks [fieldCount]uint64
	for i, spec := range specs {
		mask, err := parseField(parts[i], spec)
		if err != nil {
			return Schedule{}, fmt.Errorf("%s: %w", spec.name, err)
		}
		masks[i] = mask
	}
	// A 7 in the day-of-week field also sets Sunday's bit, 0.
	if masks[4]&(1<<sundayAlias) != 0 {
		masks[4] |= 1
	}
	return Schedule{
		minute:        masks[0],
		hour:          masks[1],
		dom:           masks[2],
		month:         masks[3],
		dow:           masks[4],
		domRestricted: parts[2] != "*",
		dowRestricted: parts[4] != "*",
	}, nil
}

// parseField returns the mask of the values that the comma list text matches within spec's range.
func parseField(text string, spec fieldSpec) (uint64, error) {
	var mask uint64
	for item := range strings.SplitSeq(text, ",") {
		bits, err := parseItem(item, spec)
		if err != nil {
			return 0, err
		}
		mask |= bits
	}
	return mask, nil
}

// parseItem returns the mask of one list item: *, a value, a range, or a step over * or a range.
func parseItem(item string, spec fieldSpec) (uint64, error) {
	if item == "" {
		return 0, errors.New("empty list item")
	}
	span, stepText, stepped := strings.Cut(item, "/")
	lo, hi, single, err := parseSpan(span, spec)
	if err != nil {
		return 0, err
	}
	step := 1
	if stepped {
		if single {
			return 0, fmt.Errorf("step %q needs * or a range", item)
		}
		step, err = parseNumber(stepText)
		if err != nil {
			return 0, err
		}
		if step < 1 {
			return 0, fmt.Errorf("step must be at least 1, got %d", step)
		}
	}
	// The values are lo, lo+step, ... up to hi. Counting whole steps rather than adding step to a running value keeps a
	// huge step from overflowing; a step wider than the span matches only lo.
	var mask uint64
	for n := 0; n <= (hi-lo)/step; n++ {
		mask |= 1 << (lo + n*step)
	}
	return mask, nil
}

// parseSpan returns the first and last values that span names. A bare value is a span of one value and reports
// single; * is the whole range and a range a-b is the values from a to b.
func parseSpan(span string, spec fieldSpec) (lo, hi int, single bool, err error) {
	if span == "*" {
		return spec.lo, spec.hi, false, nil
	}
	first, last, isRange := strings.Cut(span, "-")
	lo, err = parseValue(first, spec)
	if err != nil {
		return 0, 0, false, err
	}
	if !isRange {
		return lo, lo, true, nil
	}
	hi, err = parseValue(last, spec)
	if err != nil {
		return 0, 0, false, err
	}
	if lo > hi {
		return 0, 0, false, fmt.Errorf("range %d-%d is reversed", lo, hi)
	}
	return lo, hi, false, nil
}

// parseValue reads text as a number within spec's range.
func parseValue(text string, spec fieldSpec) (int, error) {
	n, err := parseNumber(text)
	if err != nil {
		return 0, err
	}
	if n < spec.lo || n > spec.hi {
		return 0, fmt.Errorf("%d is outside %d-%d", n, spec.lo, spec.hi)
	}
	return n, nil
}

// parseNumber reads text as a decimal number. Only digits are accepted, so a sign, a name or a space is refused.
func parseNumber(text string) (int, error) {
	if text == "" || strings.Trim(text, digits) != "" {
		return 0, fmt.Errorf("%q is not a number", text)
	}
	n, err := strconv.Atoi(text)
	if err != nil {
		return 0, fmt.Errorf("%q is too large", text)
	}
	return n, nil
}

// Next returns the first whole minute strictly after after that the schedule matches. The result is in after's
// location, so the schedule's times are wall-clock times there. Next returns the zero time when nothing matches in the
// five years after after.
func (s Schedule) Next(after time.Time) time.Time {
	loc := after.Location()
	at := after.Truncate(time.Minute).Add(time.Minute)
	limit := after.AddDate(searchYears, 0, 0)
	for at.Before(limit) {
		switch {
		case !has(s.month, int(at.Month())):
			at = time.Date(at.Year(), at.Month()+1, 1, 0, 0, 0, 0, loc)
		case !s.dayMatches(at):
			at = time.Date(at.Year(), at.Month(), at.Day()+1, 0, 0, 0, 0, loc)
		case !has(s.hour, at.Hour()):
			at = time.Date(at.Year(), at.Month(), at.Day(), at.Hour()+1, 0, 0, 0, loc)
		case !has(s.minute, at.Minute()):
			at = at.Add(time.Minute)
		default:
			return at
		}
	}
	return time.Time{}
}

// dayMatches reports whether the day of t matches. When both day fields are restricted, a day matches if either one
// does. Otherwise both must match, and an unrestricted field matches every day, so it does not narrow the choice.
func (s Schedule) dayMatches(t time.Time) bool {
	dom := has(s.dom, t.Day())
	dow := has(s.dow, int(t.Weekday()))
	if s.domRestricted && s.dowRestricted {
		return dom || dow
	}
	return dom && dow
}

// has reports whether v is one of the values in mask.
func has(mask uint64, v int) bool {
	return mask&(1<<v) != 0
}
