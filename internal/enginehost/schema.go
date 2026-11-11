package enginehost

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
)

// ErrBadSchema reports a step schema that is not JSON, or is not a JSON object schema with a string status property
// built from the supported keywords.
var ErrBadSchema = errors.New("step schema is not a supported object schema with a string status")

// The type names a schema's type keyword may name.
const (
	schemaObject  = "object"
	schemaArray   = "array"
	schemaString  = "string"
	schemaNumber  = "number"
	schemaInteger = "integer"
	schemaBoolean = "boolean"
	schemaNull    = "null"
)

// checkStepSchema accepts a step schema only when it is a JSON object schema whose root type is object and whose
// properties include status of type string, and every subschema uses supported keywords. It returns an error wrapping
// ErrBadSchema otherwise.
func checkStepSchema(schema string) error {
	_, err := schemaCompile(schema)
	return err
}

// schemaCompile parses schema, checks it against the step schema rules, and returns its root object.
func schemaCompile(schema string) (map[string]any, error) {
	var parsed any
	if err := json.Unmarshal([]byte(schema), &parsed); err != nil {
		return nil, fmt.Errorf("%w: not JSON: %w", ErrBadSchema, err)
	}
	root, ok := parsed.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: root must be an object", ErrBadSchema)
	}
	if err := schemaCheckNode("$", root); err != nil {
		return nil, err
	}
	if root["type"] != schemaObject {
		return nil, fmt.Errorf(`%w: root type must be "object"`, ErrBadSchema)
	}
	properties, ok := root["properties"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: root must declare properties", ErrBadSchema)
	}
	status, ok := properties["status"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: properties must include status", ErrBadSchema)
	}
	if status["type"] != schemaString {
		return nil, fmt.Errorf(`%w: status type must be "string"`, ErrBadSchema)
	}
	return root, nil
}

// schemaCheckNode checks that node is a schema object built from supported keywords, recursing into its subschemas.
func schemaCheckNode(path string, node any) error {
	object, ok := node.(map[string]any)
	if !ok {
		return fmt.Errorf("%w: %s must be a schema object", ErrBadSchema, path)
	}
	for _, key := range schemaSortedKeys(object) {
		if err := schemaCheckKeyword(path, key, object[key]); err != nil {
			return err
		}
	}
	return nil
}

// schemaCheckKeyword checks one keyword of the schema object at path.
func schemaCheckKeyword(path, key string, value any) error {
	child := path + "." + key
	switch key {
	case "type":
		return schemaCheckType(child, value)
	case "enum":
		if _, isList := value.([]any); !isList {
			return fmt.Errorf("%w: %s must be an array", ErrBadSchema, child)
		}
		return nil
	case "required":
		return schemaCheckStrings(child, value)
	case "properties":
		return schemaCheckProperties(child, value)
	case "items":
		return schemaCheckNode(child, value)
	case "additionalProperties":
		if _, isBool := value.(bool); isBool {
			return nil
		}
		return schemaCheckNode(child, value)
	}
	if schemaIsAnnotation(key) {
		return nil
	}
	return fmt.Errorf("%w: unsupported keyword %s at %s", ErrBadSchema, key, path)
}

// schemaIsAnnotation reports whether key is an annotation the validator ignores.
func schemaIsAnnotation(key string) bool {
	switch key {
	case "title", "description", "$schema", "default", "examples":
		return true
	}
	return false
}

// schemaIsTypeName reports whether name is a type name the type keyword may use.
func schemaIsTypeName(name string) bool {
	switch name {
	case schemaObject, schemaArray, schemaString, schemaNumber, schemaInteger, schemaBoolean, schemaNull:
		return true
	}
	return false
}

// schemaCheckProperties checks a properties keyword: an object whose values are schemas.
func schemaCheckProperties(path string, value any) error {
	properties, isObject := value.(map[string]any)
	if !isObject {
		return fmt.Errorf("%w: %s must be an object", ErrBadSchema, path)
	}
	for _, name := range schemaSortedKeys(properties) {
		if err := schemaCheckNode(path+"."+name, properties[name]); err != nil {
			return err
		}
	}
	return nil
}

// schemaCheckType checks a type keyword: one supported type name, or a non-empty list of them.
func schemaCheckType(path string, value any) error {
	if name, isName := value.(string); isName {
		if !schemaIsTypeName(name) {
			return fmt.Errorf("%w: %s names an unsupported type %q", ErrBadSchema, path, name)
		}
		return nil
	}
	list, isList := value.([]any)
	if !isList || len(list) == 0 {
		return fmt.Errorf("%w: %s must be a type name or a non-empty list of them", ErrBadSchema, path)
	}
	for _, item := range list {
		name, isName := item.(string)
		if !isName || !schemaIsTypeName(name) {
			return fmt.Errorf("%w: %s names an unsupported type %v", ErrBadSchema, path, item)
		}
	}
	return nil
}

// schemaCheckStrings checks that value is an array of strings.
func schemaCheckStrings(path string, value any) error {
	list, isList := value.([]any)
	if !isList {
		return fmt.Errorf("%w: %s must be an array of strings", ErrBadSchema, path)
	}
	for _, item := range list {
		if _, isString := item.(string); !isString {
			return fmt.Errorf("%w: %s must be an array of strings", ErrBadSchema, path)
		}
	}
	return nil
}

// validateReturn checks value against schema, which must be accepted by checkStepSchema. It returns an error naming
// the first failing JSON path, such as $.status, or nil when value conforms.
func validateReturn(schema string, value json.RawMessage) error {
	root, err := schemaCompile(schema)
	if err != nil {
		return err
	}
	if len(value) == 0 {
		return errors.New("$: missing return value")
	}
	var parsed any
	if err = json.Unmarshal(value, &parsed); err != nil {
		return fmt.Errorf("$: return is not JSON: %w", err)
	}
	return schemaValidate("$", root, parsed)
}

// schemaValidate checks value against the schema node at path, applying the keywords that apply to its JSON type.
func schemaValidate(path string, node map[string]any, value any) error {
	if want, present := node["type"]; present && !schemaTypeMatches(want, value) {
		return fmt.Errorf("%s: expected %s, got %s", path, schemaDescribeType(want), schemaJSONType(value))
	}
	if enum, isList := node["enum"].([]any); isList && !schemaInEnum(enum, value) {
		return fmt.Errorf("%s: value %s is not in enum", path, schemaJSON(value))
	}
	switch typed := value.(type) {
	case map[string]any:
		return schemaValidateObject(path, node, typed)
	case []any:
		return schemaValidateArray(path, node, typed)
	}
	return nil
}

// schemaValidateObject checks the object keywords: required, properties and additionalProperties. Keys are visited in
// sorted order so the first failing path is stable.
func schemaValidateObject(path string, node, object map[string]any) error {
	if err := schemaCheckRequired(path, node, object); err != nil {
		return err
	}
	for _, name := range schemaSortedKeys(object) {
		childPath := path + "." + name
		if sub, isSchema := schemaProperty(node, name); isSchema {
			if err := schemaValidate(childPath, sub, object[name]); err != nil {
				return err
			}
			continue
		}
		if err := schemaValidateAdditional(childPath, node["additionalProperties"], object[name]); err != nil {
			return err
		}
	}
	return nil
}

// schemaCheckRequired checks that object has every name the required keyword of node lists.
func schemaCheckRequired(path string, node, object map[string]any) error {
	required, isList := node["required"].([]any)
	if !isList {
		return nil
	}
	for _, item := range required {
		name, isName := item.(string)
		if _, present := object[name]; isName && !present {
			return fmt.Errorf("%s.%s: required property is missing", path, name)
		}
	}
	return nil
}

// schemaProperty returns the subschema that the properties keyword of node gives name, when it gives one.
func schemaProperty(node map[string]any, name string) (map[string]any, bool) {
	properties, isObject := node["properties"].(map[string]any)
	if !isObject {
		return nil, false
	}
	sub, isSchema := properties[name].(map[string]any)
	return sub, isSchema
}

// schemaValidateAdditional checks a property that no properties keyword names: false rejects it, a schema checks it,
// and true or an absent keyword allows it.
func schemaValidateAdditional(path string, additional, value any) error {
	switch rule := additional.(type) {
	case bool:
		if !rule {
			return fmt.Errorf("%s: property is not allowed", path)
		}
	case map[string]any:
		return schemaValidate(path, rule, value)
	}
	return nil
}

// schemaValidateArray checks every item of a list against the items schema, when the node has one.
func schemaValidateArray(path string, node map[string]any, list []any) error {
	items, hasItems := node["items"].(map[string]any)
	if !hasItems {
		return nil
	}
	for i, item := range list {
		if err := schemaValidate(fmt.Sprintf("%s[%d]", path, i), items, item); err != nil {
			return err
		}
	}
	return nil
}

// schemaTypeMatches reports whether value is one of the types in want, a type name or a list of them.
func schemaTypeMatches(want, value any) bool {
	if name, isName := want.(string); isName {
		return schemaTypeIs(name, value)
	}
	list, isList := want.([]any)
	if !isList {
		return false
	}
	for _, item := range list {
		if name, isName := item.(string); isName && schemaTypeIs(name, value) {
			return true
		}
	}
	return false
}

// schemaTypeIs reports whether value has the JSON type named by name. An integer is a number with no fractional part.
func schemaTypeIs(name string, value any) bool {
	switch name {
	case schemaNull:
		return value == nil
	case schemaBoolean:
		_, ok := value.(bool)
		return ok
	case schemaString:
		_, ok := value.(string)
		return ok
	case schemaNumber:
		_, ok := value.(float64)
		return ok
	case schemaInteger:
		number, ok := value.(float64)
		return ok && number == math.Trunc(number)
	case schemaArray:
		_, ok := value.([]any)
		return ok
	case schemaObject:
		_, ok := value.(map[string]any)
		return ok
	}
	return false
}

// schemaInEnum reports whether value equals one of the enum values.
func schemaInEnum(enum []any, value any) bool {
	for _, item := range enum {
		if reflect.DeepEqual(item, value) {
			return true
		}
	}
	return false
}

// schemaDescribeType names the type or type list a schema asks for, for an error message.
func schemaDescribeType(want any) string {
	if name, isName := want.(string); isName {
		return name
	}
	list, isList := want.([]any)
	if !isList {
		return fmt.Sprint(want)
	}
	names := make([]string, 0, len(list))
	for _, item := range list {
		names = append(names, fmt.Sprint(item))
	}
	return strings.Join(names, " or ")
}

// schemaJSONType names the JSON type of a parsed value, for an error message.
func schemaJSONType(value any) string {
	switch typed := value.(type) {
	case nil:
		return schemaNull
	case bool:
		return schemaBoolean
	case string:
		return schemaString
	case float64:
		return schemaNumber
	case []any:
		return schemaArray
	case map[string]any:
		return schemaObject
	default:
		return fmt.Sprintf("%T", typed)
	}
}

// schemaJSON renders a parsed value as JSON, for an error message.
func schemaJSON(value any) string {
	out, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(out)
}

// schemaSortedKeys returns the keys of m in sorted order.
func schemaSortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
