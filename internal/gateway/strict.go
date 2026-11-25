package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"

	"github.com/joshsymonds/orrery/internal/llmroutes"
)

// strictToolName is the tool a strict seat marks. Claude Code names its structured-output tool this way.
const strictToolName = "StructuredOutput"

// The budgets a schema must stay within to qualify for strict mode. A schema over either budget is left unmarked, so
// the upstream judges it as the caller sent it.
const (
	maxUnrequiredProperties = 24
	maxArrayTypedNodes      = 16
)

// objectMember is one top-level member of a JSON object: its name and the exact bytes of its value.
type objectMember struct {
	key   string
	value json.RawMessage
}

// applyStrict writes the seat's strict mode into document. A seat that sets it marks each StructuredOutput tool whose
// schema qualifies; every other seat leaves the document as it is.
func applyStrict(document map[string]json.RawMessage, seat llmroutes.Seat) error {
	if !seat.StrictStructuredOutput {
		return nil
	}

	tools, changed, err := strictTools(document[fieldTools])
	if err != nil {
		return err
	}

	if changed {
		document[fieldTools] = tools
	}

	return nil
}

// strictTools returns tools with "strict": true set on every top-level element named StructuredOutput whose
// input_schema qualifies. Every other element keeps its bytes. changed reports whether any element was rewritten; when
// it is false, tools is returned as it arrived. A tools value that is not an array is not the gateway's to rewrite, so
// it is returned unchanged.
func strictTools(tools json.RawMessage) (json.RawMessage, bool, error) {
	if firstToken(tools) != '[' {
		return tools, false, nil
	}

	var elements []json.RawMessage
	if err := json.Unmarshal(tools, &elements); err != nil {
		return nil, false, fmt.Errorf("decode tools: %w", err)
	}

	changed := false
	for i, element := range elements {
		if !strictQualifies(element) {
			continue
		}

		marked, err := markStrict(element)
		if err != nil {
			return nil, false, fmt.Errorf("mark strict on %s: %w", strictToolName, err)
		}

		elements[i] = marked
		changed = true
	}

	if !changed {
		return tools, false, nil
	}

	encoded, err := json.Marshal(elements)
	if err != nil {
		return nil, false, fmt.Errorf("encode tools: %w", err)
	}

	return encoded, true, nil
}

// strictQualifies reports whether element is a StructuredOutput tool object whose input_schema passes the allowlist.
func strictQualifies(element json.RawMessage) bool {
	var fields map[string]json.RawMessage
	if firstToken(element) != '{' || json.Unmarshal(element, &fields) != nil {
		return false
	}

	var name string
	if json.Unmarshal(fields["name"], &name) != nil || name != strictToolName {
		return false
	}

	return strictSchemaAllowed(fields["input_schema"])
}

// markStrict returns element, a StructuredOutput tool object, with its strict member set to true. An existing member
// keeps its position; otherwise the member is added last. The other members keep their original order and value bytes.
func markStrict(element json.RawMessage) (json.RawMessage, error) {
	members, err := objectMembers(element)
	if err != nil {
		return nil, err
	}

	found := false
	for i := range members {
		if members[i].key == "strict" {
			members[i].value = json.RawMessage("true")
			found = true
		}
	}

	if !found {
		members = append(members, objectMember{key: "strict", value: json.RawMessage("true")})
	}

	return encodeMembers(members)
}

// objectMembers returns the top-level members of a JSON object in document order.
func objectMembers(object json.RawMessage) ([]objectMember, error) {
	decoder := json.NewDecoder(bytes.NewReader(object))
	if _, err := decoder.Token(); err != nil {
		return nil, fmt.Errorf("open object: %w", err)
	}

	var members []objectMember
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("read member name: %w", err)
		}

		key, ok := token.(string)
		if !ok {
			return nil, fmt.Errorf("member name %v is not a string", token)
		}

		var value json.RawMessage
		if err = decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("read member %q: %w", key, err)
		}

		members = append(members, objectMember{key: key, value: value})
	}

	return members, nil
}

// encodeMembers writes members back as one JSON object, each value as its original bytes.
func encodeMembers(members []objectMember) (json.RawMessage, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')

	for i, member := range members {
		if i > 0 {
			buf.WriteByte(',')
		}

		key, err := json.Marshal(member.key)
		if err != nil {
			return nil, fmt.Errorf("encode member name %q: %w", member.key, err)
		}

		buf.Write(key)
		buf.WriteByte(':')
		buf.Write(member.value)
	}

	buf.WriteByte('}')

	return buf.Bytes(), nil
}

// strictSchemaAllowed reports whether schema passes the allowlist: every node uses only the allowed keywords with their
// allowed shapes, and the whole schema stays within the unrequired-property and array-type budgets.
func strictSchemaAllowed(schema json.RawMessage) bool {
	root, ok := decodeSchema(schema)
	if !ok {
		return false
	}

	var budget schemaBudget

	return budget.node(root)
}

// decodeSchema parses schema once into generic values, so the walk visits each node a single time instead of
// rescanning every subtree at each level. Numbers stay json.Number, so no literal is range-checked, and trailing data is
// refused as json.Unmarshal refuses it.
func decodeSchema(schema json.RawMessage) (any, bool) {
	decoder := json.NewDecoder(bytes.NewReader(schema))
	decoder.UseNumber()

	var root any
	if decoder.Decode(&root) != nil {
		return nil, false
	}

	_, err := decoder.Token()

	return root, err == io.EOF
}

// schemaBudget counts what the allowlist limits across one whole schema.
type schemaBudget struct {
	unrequired int
	arrayTyped int
}

// node reports whether value, a decoded schema node, passes the allowlist and keeps the budgets.
func (b *schemaBudget) node(value any) bool {
	fields, ok := value.(map[string]any)
	if !ok {
		return false
	}

	for keyword := range fields {
		if !isStrictKeyword(keyword) {
			return false
		}
	}

	objectTyped, ok := b.typeKeyword(fields)
	if !ok {
		return false
	}

	required, ok := requiredNames(fields)
	if !ok {
		return false
	}

	return b.objectKeywords(fields, objectTyped, required) &&
		b.items(fields) &&
		valuesAllowed(fields)
}

// typeKeyword checks a node's type, counts an array type toward its budget, and reports whether the node is an object
// type. A node without a type is not one.
func (b *schemaBudget) typeKeyword(fields map[string]any) (bool, bool) {
	value, present := fields["type"]
	if !present {
		return false, true
	}

	names, isArray, ok := typeNames(value)
	if !ok {
		return false, false
	}

	if isArray {
		b.arrayTyped++
		if b.arrayTyped > maxArrayTypedNodes {
			return false, false
		}
	}

	return slices.Contains(names, "object"), true
}

// objectKeywords requires a node that is an object type, or that has properties, to set additionalProperties to false,
// and checks its properties.
func (b *schemaBudget) objectKeywords(fields map[string]any, objectTyped bool, required map[string]bool) bool {
	properties, hasProperties := fields["properties"]
	if (objectTyped || hasProperties) && !isFalse(fields["additionalProperties"]) {
		return false
	}

	return !hasProperties || b.properties(properties, required)
}

// items checks the items schema when the node has one.
func (b *schemaBudget) items(fields map[string]any) bool {
	value, present := fields["items"]

	return !present || b.node(value)
}

// valuesAllowed checks the keywords whose values are not schemas: a present additionalProperties is the literal false,
// enum and const hold scalars, and title and description are strings.
func valuesAllowed(fields map[string]any) bool {
	if value, present := fields["additionalProperties"]; present && !isFalse(value) {
		return false
	}

	if value, present := fields["enum"]; present && !scalarArray(value) {
		return false
	}

	if value, present := fields["const"]; present && !isScalar(value) {
		return false
	}

	for _, keyword := range []string{"title", "description"} {
		if value, present := fields[keyword]; present {
			if _, isString := value.(string); !isString {
				return false
			}
		}
	}

	return true
}

// isStrictKeyword reports whether a schema node may carry keyword.
func isStrictKeyword(keyword string) bool {
	switch keyword {
	case "type", "properties", "required", "items", "enum", "const",
		"additionalProperties", "title", "description":
		return true
	}

	return false
}

// properties checks a node's properties object: every value is a schema node, and every property name missing from the
// node's required list counts toward the unrequired budget.
func (b *schemaBudget) properties(value any, required map[string]bool) bool {
	named, ok := value.(map[string]any)
	if !ok {
		return false
	}

	for name, schema := range named {
		if !b.node(schema) {
			return false
		}

		if !required[name] {
			b.unrequired++
			if b.unrequired > maxUnrequiredProperties {
				return false
			}
		}
	}

	return true
}

// typeNames reads a node's type, a string or an array of strings. isArray reports the array form, which counts toward
// the array-typed budget.
func typeNames(value any) ([]string, bool, bool) {
	if name, ok := value.(string); ok {
		return []string{name}, false, true
	}

	names, ok := stringArray(value)

	return names, true, ok
}

// requiredNames returns the names in a node's required list. An absent list names none; a present one must be an array
// of strings.
func requiredNames(fields map[string]any) (map[string]bool, bool) {
	names := make(map[string]bool)

	value, present := fields["required"]
	if !present {
		return names, true
	}

	list, ok := stringArray(value)
	if !ok {
		return nil, false
	}

	for _, name := range list {
		names[name] = true
	}

	return names, true
}

// stringArray reads value as an array whose members are all strings.
func stringArray(value any) ([]string, bool) {
	items, ok := value.([]any)
	if !ok {
		return nil, false
	}

	strs := make([]string, 0, len(items))
	for _, item := range items {
		str, isString := item.(string)
		if !isString {
			return nil, false
		}

		strs = append(strs, str)
	}

	return strs, true
}

// scalarArray reports whether value is an array whose members are all scalars.
func scalarArray(value any) bool {
	items, ok := value.([]any)
	if !ok {
		return false
	}

	for _, item := range items {
		if !isScalar(item) {
			return false
		}
	}

	return true
}

// isScalar reports whether value is a string, number, boolean or null.
func isScalar(value any) bool {
	switch value.(type) {
	case map[string]any, []any:
		return false
	}

	return true
}

// isFalse reports whether value is the literal false.
func isFalse(value any) bool {
	flag, ok := value.(bool)

	return ok && !flag
}
