package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// StepBrief is what a step's end tells its caller of the step: its return, its reason and its checks. StepBriefOf
// derives it from the stored step. Fields holds the return's fields when its schema shows them; otherwise Return holds
// the return's first line. NotShown names what the brief leaves out, and Failed holds the failing checks in stored
// order.
type StepBrief struct {
	ID       string
	Fields   []BriefField
	Return   string
	NotShown []BriefOmit
	Reason   string
	Passed   int
	Failed   []StepCheck
}

// BriefField is one field of a step's return that its brief shows by name and value. Short marks a string field.
type BriefField struct {
	Name  string
	Value string
	Short bool
}

// BriefOmit is a part of a step that its brief does not show, with the byte length of its raw JSON.
type BriefOmit struct {
	Name  string
	Bytes int
}

// The most runes a string field keeps, and the most runes a return's or a reason's first line keeps.
const (
	briefShortCap = 120
	briefLineCap  = 200
)

// errBriefTrailing reports text after a JSON value.
var errBriefTrailing = errors.New("text after the JSON value")

// StepBriefOf derives the brief of step. Its return is shown field by field when its schema names properties and the
// return is exactly one JSON object; otherwise the return's first line is shown as Return. Its reason is shown by its
// first line, and its checks are counted, with the failing ones kept in stored order.
func StepBriefOf(step StepRow) StepBrief {
	brief := StepBrief{ID: step.ID}
	if fields, omitted, ok := briefFields(step.Schema, step.Result); ok {
		brief.Fields = fields
		brief.NotShown = omitted
	} else {
		text, whole := briefLine(step.Result)
		brief.Return = text
		if !whole {
			brief.NotShown = append(brief.NotShown, BriefOmit{Name: "return", Bytes: len(step.Result)})
		}
	}
	reason, whole := briefLine(step.Reason)
	brief.Reason = reason
	if !whole {
		brief.NotShown = append(brief.NotShown, BriefOmit{Name: "reason", Bytes: len(step.Reason)})
	}
	for _, check := range step.Checks {
		if check.OK {
			brief.Passed++
		} else {
			brief.Failed = append(brief.Failed, check)
		}
	}
	return brief
}

// Lines renders the brief as the lines a step_end block tells: a line per field, or the Return; then what is Not shown;
// the Reason; the Checks when the step has any; and the More line that names the reads which give the rest.
func (b StepBrief) Lines() []string {
	lines := make([]string, 0, len(b.Fields))
	for _, field := range b.Fields {
		lines = append(lines, field.Name+": "+field.Value)
	}
	if b.Return != "" {
		lines = append(lines, "Return: "+b.Return)
	}
	if len(b.NotShown) == 0 {
		lines = append(lines, "Not shown: none")
	} else {
		omitted := make([]string, 0, len(b.NotShown))
		for _, omit := range b.NotShown {
			omitted = append(omitted, fmt.Sprintf("%s (%d bytes)", omit.Name, omit.Bytes))
		}
		lines = append(lines, "Not shown: "+strings.Join(omitted, ", "))
	}
	if b.Reason != "" {
		lines = append(lines, "Reason: "+b.Reason)
	}
	if b.Passed+len(b.Failed) > 0 {
		lines = append(lines, briefChecksLine(b))
	}
	more := "More: step_result id " + b.ID
	if len(b.Failed) > 0 {
		first := b.Failed[0]
		more += fmt.Sprintf("; step_check id %s check %s round %d", b.ID, first.Name, first.Round)
	}
	return append(lines, more)
}

// briefChecksLine returns the Checks line of a brief that has checks: the count passed, and the failing checks by name
// and round when any failed.
func briefChecksLine(brief StepBrief) string {
	line := fmt.Sprintf("Checks: %d passed", brief.Passed)
	if len(brief.Failed) == 0 {
		return line
	}
	failed := make([]string, 0, len(brief.Failed))
	for _, check := range brief.Failed {
		failed = append(failed, fmt.Sprintf("%s round %d", check.Name, check.Round))
	}
	return line + fmt.Sprintf(", %d failed: %s", len(brief.Failed), strings.Join(failed, ", "))
}

// briefFields shows a return field by field, by the properties of its schema. It reports false when the schema has no
// properties or the return is not exactly one JSON object. A field the schema does not show is omitted with its size.
func briefFields(schema, result string) ([]BriefField, []BriefOmit, bool) {
	props, ok := briefSchemaProps(schema)
	if !ok {
		return nil, nil, false
	}
	members, ok := briefMembers(result)
	if !ok {
		return nil, nil, false
	}
	var fields []BriefField
	var omitted []BriefOmit
	for _, member := range members {
		if prop, known := props[member.key].(map[string]any); known {
			if field, shown := briefShow(member.key, prop, member.value); shown {
				fields = append(fields, field)
				continue
			}
		}
		omitted = append(omitted, BriefOmit{Name: member.key, Bytes: len(member.raw)})
	}
	return fields, omitted, true
}

// briefSchemaProps returns the properties of schema when schema is a JSON object with a non-empty properties object.
func briefSchemaProps(schema string) (map[string]any, bool) {
	var top map[string]any
	if err := briefDecode(schema, &top); err != nil {
		return nil, false
	}
	props, ok := top["properties"].(map[string]any)
	return props, ok && len(props) > 0
}

// briefMember is one top-level member of a return: its key, its raw JSON value, and that value decoded.
type briefMember struct {
	key   string
	raw   json.RawMessage
	value any
}

// briefMembers returns the top-level members of text in their own order, when text is exactly one JSON object.
func briefMembers(text string) ([]briefMember, bool) {
	dec := json.NewDecoder(strings.NewReader(text))
	open, err := dec.Token()
	if err != nil || open != json.Delim('{') {
		return nil, false
	}
	var members []briefMember
	for dec.More() {
		var keyToken json.Token
		keyToken, err = dec.Token()
		if err != nil {
			return nil, false
		}
		key, isKey := keyToken.(string)
		if !isKey {
			return nil, false
		}
		var raw json.RawMessage
		if err = dec.Decode(&raw); err != nil {
			return nil, false
		}
		var value any
		if err = briefDecode(string(raw), &value); err != nil {
			return nil, false
		}
		members = append(members, briefMember{key: key, raw: raw, value: value})
	}
	closing, err := dec.Token()
	if err != nil || closing != json.Delim('}') {
		return nil, false
	}
	if _, err = dec.Token(); !errors.Is(err, io.EOF) {
		return nil, false
	}
	return members, true
}

// briefDecode decodes text into v as one JSON value, with numbers kept as json.Number, and no text after it.
func briefDecode(text string, v any) error {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("decode JSON: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errBriefTrailing
	}
	return nil
}

// briefShow returns the field a property shows for value, and whether it shows at all. A property with an enum shows a
// scalar value that is in the enum. Otherwise a property shows a value of its type: a one-line string of at most
// briefShortCap runes, a boolean, a number, an integer, or an array (by its count and tally).
func briefShow(name string, prop map[string]any, value any) (BriefField, bool) {
	if enum, ok := prop["enum"].([]any); ok {
		if !briefScalar(value) || !slices.ContainsFunc(enum, func(e any) bool { return reflect.DeepEqual(e, value) }) {
			return BriefField{}, false
		}
		return BriefField{Name: name, Value: fmt.Sprint(value)}, true
	}
	types := briefTypes(prop["type"])
	switch val := value.(type) {
	case string:
		if slices.Contains(types, "string") && briefOneLine(val) {
			return BriefField{Name: name, Value: val, Short: true}, true
		}
	case bool:
		if slices.Contains(types, "boolean") {
			return BriefField{Name: name, Value: fmt.Sprint(val)}, true
		}
	case json.Number:
		if slices.Contains(types, "number") || (slices.Contains(types, "integer") && briefIntegral(val)) {
			return BriefField{Name: name, Value: val.String()}, true
		}
	case []any:
		if slices.Contains(types, "array") {
			return BriefField{Name: name, Value: briefCount(val, prop)}, true
		}
	}
	return BriefField{}, false
}

// briefTypes returns the type names a schema's type states: one name, or the names in a list.
func briefTypes(t any) []string {
	switch types := t.(type) {
	case string:
		return []string{types}
	case []any:
		names := make([]string, 0, len(types))
		for _, entry := range types {
			if name, ok := entry.(string); ok {
				names = append(names, name)
			}
		}
		return names
	}
	return nil
}

// briefOneLine reports whether s is one line of at most briefShortCap runes.
func briefOneLine(s string) bool {
	return !strings.ContainsAny(s, "\r\n") && utf8.RuneCountInString(s) <= briefShortCap
}

// briefIntegral reports whether n is a whole number.
func briefIntegral(n json.Number) bool {
	f, err := n.Float64()
	return err == nil && f == math.Trunc(f)
}

// briefScalar reports whether value is a string, a boolean or a number.
func briefScalar(value any) bool {
	switch value.(type) {
	case string, bool, json.Number:
		return true
	}
	return false
}

// briefTally is one value of an array's tally and how many items hold it.
type briefTally struct {
	value any
	n     int
}

// briefCount returns an array's item count, and when its items require an enum property, the tally of that property's
// values after the count in parentheses: the enum's values in enum order, then the values outside it in first-seen
// order. A value with no count is omitted.
func briefCount(items []any, prop map[string]any) string {
	count := strconv.Itoa(len(items))
	key, enum, ok := briefTallyKey(prop)
	if !ok {
		return count
	}
	tallies := make([]briefTally, 0, len(enum))
	for _, entry := range enum {
		if briefScalar(entry) {
			tallies = append(tallies, briefTally{value: entry})
		}
	}
	for _, item := range items {
		obj, isObject := item.(map[string]any)
		if !isObject {
			continue
		}
		value := obj[key]
		if !briefScalar(value) {
			continue
		}
		index := slices.IndexFunc(tallies, func(t briefTally) bool { return reflect.DeepEqual(t.value, value) })
		if index < 0 {
			tallies = append(tallies, briefTally{value: value})
			index = len(tallies) - 1
		}
		tallies[index].n++
	}
	parts := make([]string, 0, len(tallies))
	for _, t := range tallies {
		if t.n > 0 {
			parts = append(parts, fmt.Sprintf("%v %d", t.value, t.n))
		}
	}
	if len(parts) == 0 {
		return count
	}
	return fmt.Sprintf("%s (%s)", count, strings.Join(parts, ", "))
}

// briefTallyKey returns the name and enum of the first property an array's items schema requires that has an enum, in
// the order required lists them. It reports false when no required property has an enum.
func briefTallyKey(prop map[string]any) (string, []any, bool) {
	items, isItems := prop["items"].(map[string]any)
	required, isRequired := items["required"].([]any)
	itemProps, isProps := items["properties"].(map[string]any)
	if !isItems || !isRequired || !isProps {
		return "", nil, false
	}
	for _, entry := range required {
		name, isName := entry.(string)
		if !isName {
			continue
		}
		propSchema, isSchema := itemProps[name].(map[string]any)
		if !isSchema {
			continue
		}
		if enum, hasEnum := propSchema["enum"].([]any); hasEnum {
			return name, enum, true
		}
	}
	return "", nil, false
}

// briefLine returns the first line of s cut to briefLineCap runes, and whether that holds all of s. The line is the
// text before the first "\n", never trimmed. It holds all of s only when s has no "\n" and the line has at most
// briefLineCap runes; a line cut short, or followed by more text, ends with "...".
func briefLine(s string) (string, bool) {
	first, _, multi := strings.Cut(s, "\n")
	runes := []rune(first)
	if !multi && len(runes) <= briefLineCap {
		return first, true
	}
	if len(runes) > briefLineCap {
		runes = runes[:briefLineCap]
	}
	return string(runes) + "...", false
}
