package stepmcp

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// toolsListFixture is the tools array of the effort base's tools/list reply.
const toolsListFixture = "testdata/tools_list.json"

// listedTool is one tool of a tools/list reply or of the fixture, reduced to the fields the regression compares.
type listedTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// TestToolsListMatchesTheBaseFixture fails when a tool's name, description or input schema differs from the fixture
// captured at the effort base, when a tool is added or missing, when a top-level input schema does not set
// additionalProperties to false, or when a schema at any depth has an array type.
func TestToolsListMatchesTheBaseFixture(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(toolsListFixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fixture []listedTool
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}

	p := newPeerWithEvidence(t, nil, nil, nil)
	reply := p.call("tools/list", map[string]any{})
	var listed struct {
		Tools []listedTool `json:"tools"`
	}
	if err := json.Unmarshal(reply.Result, &listed); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}

	live := make(map[string]listedTool, len(listed.Tools))
	for _, tool := range listed.Tools {
		if _, dup := live[tool.Name]; dup {
			t.Errorf("tools/list lists tool %s more than once", tool.Name)
		}
		live[tool.Name] = tool
		if !closedTopLevel(tool.InputSchema) {
			t.Errorf("tool %s input schema does not set additionalProperties to false", tool.Name)
		}
		if hasArrayType(tool.InputSchema) {
			t.Errorf("tool %s has a schema whose type is an array", tool.Name)
		}
	}

	inFixture := make(map[string]bool, len(fixture))
	for _, want := range fixture {
		inFixture[want.Name] = true
		got, ok := live[want.Name]
		if !ok {
			t.Errorf("tools/list is missing tool %s", want.Name)
			continue
		}
		if got.Description != want.Description {
			t.Errorf("tool %s description differs from the fixture", want.Name)
		}
		if !reflect.DeepEqual(canonicalSchema(got.InputSchema), canonicalSchema(want.InputSchema)) {
			t.Errorf("tool %s input schema differs from the fixture", want.Name)
		}
	}
	for name := range live {
		if !inFixture[name] {
			t.Errorf("tools/list has tool %s, which the fixture does not", name)
		}
	}
}

// TestStrictSchemaRulesFlagOpenAndNullableSchemas checks the two schema rules the regression applies to live tools.
func TestStrictSchemaRulesFlagOpenAndNullableSchemas(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		schema     string
		wantClosed bool
		wantArray  bool
	}{
		{
			name:       "closed object with string properties",
			schema:     `{"type":"object","properties":{"a":{"type":"string"}},"additionalProperties":false}`,
			wantClosed: true,
		},
		{
			name:   "open top level",
			schema: `{"type":"object","additionalProperties":true}`,
		},
		{
			name:   "top level without additionalProperties",
			schema: `{"type":"object"}`,
		},
		{
			name:       "nullable property",
			schema:     `{"type":"object","properties":{"a":{"type":["string","null"]}},"additionalProperties":false}`,
			wantClosed: true,
			wantArray:  true,
		},
		{
			name:       "nullable item of an array property",
			schema:     `{"type":"object","properties":{"a":{"type":"array","items":{"type":["integer","null"]}}},"additionalProperties":false}`,
			wantClosed: true,
			wantArray:  true,
		},
		{
			name:       "nullable branch of anyOf",
			schema:     `{"type":"object","anyOf":[{"type":["string","null"]}],"additionalProperties":false}`,
			wantClosed: true,
			wantArray:  true,
		},
		{
			name:      "nullable schema under additionalProperties",
			schema:    `{"type":"object","additionalProperties":{"type":["string","null"]}}`,
			wantArray: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var schema map[string]any
			if err := json.Unmarshal([]byte(tc.schema), &schema); err != nil {
				t.Fatalf("decode schema: %v", err)
			}
			if got := closedTopLevel(schema); got != tc.wantClosed {
				t.Errorf("closedTopLevel = %t, want %t", got, tc.wantClosed)
			}
			if got := hasArrayType(schema); got != tc.wantArray {
				t.Errorf("hasArrayType = %t, want %t", got, tc.wantArray)
			}
		})
	}
}

// TestCanonicalSchemaTreatsAbsentAndEmptyRequiredAndPropertiesAlike checks that an empty required list and an empty
// properties object compare equal to their absence, and that a non-empty required list is kept.
func TestCanonicalSchemaTreatsAbsentAndEmptyRequiredAndPropertiesAlike(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		schema string
		want   string
	}{
		{
			name:   "empty required and properties are dropped",
			schema: `{"type":"object","properties":{},"required":[],"additionalProperties":false}`,
			want:   `{"type":"object","additionalProperties":false}`,
		},
		{
			name:   "empty ones are dropped at every depth",
			schema: `{"type":"object","properties":{"a":{"type":"object","properties":{},"required":[]}}}`,
			want:   `{"type":"object","properties":{"a":{"type":"object"}}}`,
		},
		{
			name:   "a required list that names a property is kept",
			schema: `{"type":"object","properties":{"a":{"type":"string"}},"required":["a"]}`,
			want:   `{"type":"object","properties":{"a":{"type":"string"}},"required":["a"]}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var schema, want map[string]any
			if err := json.Unmarshal([]byte(tc.schema), &schema); err != nil {
				t.Fatalf("decode schema: %v", err)
			}
			if err := json.Unmarshal([]byte(tc.want), &want); err != nil {
				t.Fatalf("decode expected schema: %v", err)
			}
			if !reflect.DeepEqual(canonicalSchema(schema), want) {
				t.Errorf("canonical form of %s differs from the expected schema", tc.name)
			}
		})
	}
}

// forEachSchema calls visit with node and with every schema reached from it through a keyword that holds subschemas.
// A property name is a key of the properties object, not a keyword, so it is never mistaken for a schema.
func forEachSchema(node any, visit func(schema map[string]any)) {
	schema, ok := node.(map[string]any)
	if !ok {
		return
	}
	visit(schema)
	for keyword, value := range schema {
		switch keyword {
		case "properties", "$defs", "definitions":
			if named, ok := value.(map[string]any); ok {
				for _, sub := range named {
					forEachSchema(sub, visit)
				}
			}
		case "items", "additionalProperties", "not", "anyOf", "oneOf", "allOf", "prefixItems":
			forEachSchema(value, visit)
			if list, ok := value.([]any); ok {
				for _, sub := range list {
					forEachSchema(sub, visit)
				}
			}
		}
	}
}

// canonicalSchema drops an empty required list and an empty properties object from each schema in schema, so that an
// absent one and an empty one compare alike. It edits schema in place and returns it.
func canonicalSchema(schema map[string]any) map[string]any {
	forEachSchema(schema, func(node map[string]any) {
		if required, ok := node["required"].([]any); ok && len(required) == 0 {
			delete(node, "required")
		}
		if properties, ok := node["properties"].(map[string]any); ok && len(properties) == 0 {
			delete(node, "properties")
		}
	})
	return schema
}

// hasArrayType reports whether any schema in schema, at any depth, has a type that is an array.
func hasArrayType(schema map[string]any) bool {
	found := false
	forEachSchema(schema, func(node map[string]any) {
		if _, ok := node["type"].([]any); ok {
			found = true
		}
	})
	return found
}

// closedTopLevel reports whether the top-level schema sets additionalProperties to false.
func closedTopLevel(schema map[string]any) bool {
	return schema["additionalProperties"] == false
}
