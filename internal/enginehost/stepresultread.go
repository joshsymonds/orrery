package enginehost

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// ErrBadResultRead reports a ReadStepResult request that names no part of the result: a field or item the result does
// not have, an item on a field that is not an array, an offset past the part, or a field on a result that is not a JSON
// object.
var ErrBadResultRead = errors.New("step result read is refused")

// resultPageLimit is the most runes a read's value holds, and the number it holds when its limit is zero.
const resultPageLimit = 4000

// resultMember is one top-level field of a result that is a JSON object, with its value's raw JSON text.
type resultMember struct {
	name string
	raw  json.RawMessage
}

// resultObject is the top-level fields of a JSON object, in the order the object first holds each name, each at its last
// value.
type resultObject []resultMember

// UnmarshalJSON reads data, a JSON object, into o. A name the object holds more than once keeps the position of its first
// occurrence and the value of its last.
func (o *resultObject) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	if _, err := dec.Token(); err != nil {
		return fmt.Errorf("read step result object: %w", err)
	}
	var members resultObject
	position := map[string]int{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("read step result object: %w", err)
		}
		name, ok := tok.(string)
		if !ok {
			return fmt.Errorf("step result object name %v is not a string", tok)
		}
		var raw json.RawMessage
		if err = dec.Decode(&raw); err != nil {
			return fmt.Errorf("read step result object: %w", err)
		}
		if i, seen := position[name]; seen {
			members[i].raw = raw
			continue
		}
		position[name] = len(members)
		members = append(members, resultMember{name: name, raw: raw})
	}
	*o = members
	return nil
}

// lookup returns the raw value of the top-level field name, and false when the object has no such field.
func (o resultObject) lookup(name string) (json.RawMessage, bool) {
	for _, member := range o {
		if member.name == name {
			return member.raw, true
		}
	}
	return nil, false
}

// names returns the top-level field names of the object, comma-separated in order, or none when it has no fields.
func (o resultObject) names() string {
	if len(o) == 0 {
		return "none"
	}
	names := make([]string, 0, len(o))
	for _, member := range o {
		names = append(names, member.name)
	}
	return strings.Join(names, ", ")
}

// decodeResultObject returns the top-level fields of result when result is a JSON object. The bool is false when result is
// not one, and the empty text is not one.
func decodeResultObject(result string) (resultObject, bool, error) {
	if !strings.HasPrefix(strings.TrimLeft(result, " \t\r\n"), "{") || !json.Valid([]byte(result)) {
		return nil, false, nil
	}
	var members resultObject
	if err := json.Unmarshal([]byte(result), &members); err != nil {
		return nil, false, fmt.Errorf("decode step result: %w", err)
	}
	return members, true, nil
}

// readStepResult returns the part of result that field, item, offset and limit name, as ReadStepResult answers it. An
// empty field names no field: it reads the outline of a result that is a JSON object, and the raw text of any other
// result. A field reads that field's value, and a field with an item reads one element of that array field. A value is
// paged over its runes.
func readStepResult(result, field, item string, offset, limit uint32) (*v1.ReadStepResultResponse, error) {
	members, isObject, err := decodeResultObject(result)
	if err != nil {
		return nil, err
	}
	if field == "" {
		if item != "" {
			return nil, fmt.Errorf("%w: item names an element of a field, so it needs a field", ErrBadResultRead)
		}
		if !isObject {
			return pageResultText(result, false, offset, limit)
		}
		return outlineReply(members)
	}
	if !isObject {
		return nil, fmt.Errorf("%w: the result is not a JSON object, so it has no field %q", ErrBadResultRead, field)
	}
	raw, ok := members.lookup(field)
	if !ok {
		return nil, fmt.Errorf("%w: the result has no field %q; its fields are %s",
			ErrBadResultRead, field, members.names())
	}
	if item == "" {
		return pageValue(raw, offset, limit)
	}
	if kind := resultKind(raw); kind != schemaArray {
		return nil, fmt.Errorf("%w: item needs an array field, but field %q is a %s", ErrBadResultRead, field, kind)
	}
	var elements []json.RawMessage
	if err = json.Unmarshal(raw, &elements); err != nil {
		return nil, fmt.Errorf("read step result field %q: %w", field, err)
	}
	element, err := pickElement(field, item, elements)
	if err != nil {
		return nil, err
	}
	return pageValue(element, offset, limit)
}

// outlineReply returns the outline of members, the top-level fields of a result that is a JSON object.
func outlineReply(members resultObject) (*v1.ReadStepResultResponse, error) {
	fields := make([]*v1.StepResultField, 0, len(members))
	for _, member := range members {
		field, err := outlineField(member.name, member.raw)
		if err != nil {
			return nil, err
		}
		fields = append(fields, field)
	}
	return &v1.ReadStepResultResponse{Outline: fields}, nil
}

// outlineField returns the outline entry of the top-level field name, whose value is raw. Its size is the runes of a
// string, the elements of an array, the distinct keys of an object, and the bytes of the JSON text of any other value.
func outlineField(name string, raw json.RawMessage) (*v1.StepResultField, error) {
	kind := resultKind(raw)
	size := len(raw)
	switch kind {
	case schemaString:
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, fmt.Errorf("read step result field %q: %w", name, err)
		}
		size = utf8.RuneCountInString(text)
	case schemaObject:
		var members map[string]json.RawMessage
		if err := json.Unmarshal(raw, &members); err != nil {
			return nil, fmt.Errorf("read step result field %q: %w", name, err)
		}
		size = len(members)
	case schemaArray:
		var elements []json.RawMessage
		if err := json.Unmarshal(raw, &elements); err != nil {
			return nil, fmt.Errorf("read step result field %q: %w", name, err)
		}
		size = len(elements)
	}
	count, err := uint32Count(size)
	if err != nil {
		return nil, err
	}
	return &v1.StepResultField{Name: name, Type: kind, Size: count}, nil
}

// resultKind returns the JSON type of raw, a JSON value without surrounding white space: string, number, boolean, null,
// object or array.
func resultKind(raw json.RawMessage) string {
	switch {
	case bytes.HasPrefix(raw, []byte{'"'}):
		return schemaString
	case bytes.HasPrefix(raw, []byte{'{'}):
		return schemaObject
	case bytes.HasPrefix(raw, []byte{'['}):
		return schemaArray
	case bytes.HasPrefix(raw, []byte("true")), bytes.HasPrefix(raw, []byte("false")):
		return schemaBoolean
	case bytes.HasPrefix(raw, []byte("null")):
		return schemaNull
	default:
		return schemaNumber
	}
}

// pageValue returns the part of raw that offset and limit name, as ReadStepResult reads a value: a string as its decoded
// text, and any other value as compact JSON, paged over its runes.
func pageValue(raw json.RawMessage, offset, limit uint32) (*v1.ReadStepResultResponse, error) {
	if resultKind(raw) == schemaString {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, fmt.Errorf("read step result string: %w", err)
		}
		return pageResultText(text, false, offset, limit)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return nil, fmt.Errorf("compact step result value: %w", err)
	}
	return pageResultText(compact.String(), true, offset, limit)
}

// pickElement returns the element of an array field that item names: the first object whose id property equals item, and
// otherwise, when item is a decimal integer, the element at that 0-based index. A string id is compared as text and a
// number id by its JSON text.
func pickElement(field, item string, elements []json.RawMessage) (json.RawMessage, error) {
	for _, element := range elements {
		if resultKind(element) != schemaObject {
			continue
		}
		var properties map[string]json.RawMessage
		if err := json.Unmarshal(element, &properties); err != nil {
			return nil, fmt.Errorf("read step result element of field %q: %w", field, err)
		}
		id, hasID := properties["id"]
		if !hasID {
			continue
		}
		matched, err := idMatches(id, item)
		if err != nil {
			return nil, err
		}
		if matched {
			return element, nil
		}
	}
	if index, err := strconv.ParseUint(item, 10, 0); err == nil && index < uint64(len(elements)) {
		return elements[index], nil
	}
	return nil, fmt.Errorf("%w: item %q matches no element of field %q, which has %d elements",
		ErrBadResultRead, item, field, len(elements))
}

// idMatches reports whether id, an object's id property, is item: a string id compared as text, or a number id compared
// by its JSON text. An id of any other type matches nothing.
func idMatches(id json.RawMessage, item string) (bool, error) {
	switch resultKind(id) {
	case schemaString:
		var text string
		if err := json.Unmarshal(id, &text); err != nil {
			return false, fmt.Errorf("read step result id: %w", err)
		}
		return text == item, nil
	case schemaNumber:
		return string(id) == item, nil
	default:
		return false, nil
	}
}

// pageResultText returns the part of text that offset and limit name: its runes from offset, at most limit of them, with
// the rune count of the whole text and the offset the rest starts at. asJSON is carried through to the reply.
func pageResultText(text string, asJSON bool, offset, limit uint32) (*v1.ReadStepResultResponse, error) {
	runes := []rune(text)
	total, err := uint32Count(len(runes))
	if err != nil {
		return nil, err
	}
	if offset > total {
		return nil, fmt.Errorf("%w: offset %d is past the part's total of %d runes", ErrBadResultRead, offset, total)
	}
	if limit == 0 || limit > resultPageLimit {
		limit = resultPageLimit
	}
	start := int(offset)
	end := min(start+int(limit), len(runes))
	resp := &v1.ReadStepResultResponse{
		Value:  string(runes[start:end]),
		Json:   asJSON,
		Offset: offset,
		Total:  total,
	}
	if end < len(runes) {
		if resp.NextOffset, err = uint32Count(end); err != nil {
			return nil, err
		}
	}
	return resp, nil
}

// uint32Count returns n as a uint32, or an error when n is outside uint32.
func uint32Count(n int) (uint32, error) {
	if n < 0 || int64(n) > math.MaxUint32 {
		return 0, fmt.Errorf("step result count %d is outside uint32", n)
	}
	return uint32(n), nil
}
