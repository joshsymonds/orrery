package enginehost

import (
	"errors"
	"strings"
	"testing"
)

// stepSchemaWithCommit is an accepted step schema: a string status enum, an optional commit, a nested object and an
// array of objects, with annotations at several depths.
const stepSchemaWithCommit = `{
	"$schema": "http://json-schema.org/draft-07/schema#",
	"title": "step return",
	"description": "what the step returns",
	"type": "object",
	"properties": {
		"status": {"type": "string", "enum": ["done", "blocked"], "description": "outcome", "default": "done"},
		"commit": {"type": ["string", "null"], "examples": ["abc123"]},
		"gate": {
			"type": "object",
			"properties": {"passed": {"type": "boolean"}, "runs": {"type": "integer"}},
			"required": ["passed"],
			"additionalProperties": false
		},
		"notes": {
			"type": "array",
			"items": {"type": "object", "properties": {"text": {"type": "string"}}, "required": ["text"]}
		},
		"extra": {"additionalProperties": {"type": "number"}}
	},
	"required": ["status"],
	"additionalProperties": true
}`

func TestCheckStepSchemaRefusesUnsupportedSchemas(t *testing.T) {
	cases := []struct {
		name   string
		schema string
	}{
		{"not JSON", `{"type": "object"`},
		{"root is an array", `[]`},
		{"root is a string", `"object"`},
		{"root type is not object", `{"type": "array", "properties": {"status": {"type": "string"}}}`},
		{"root type missing", `{"properties": {"status": {"type": "string"}}}`},
		{"root type is a list", `{"type": ["object", "null"], "properties": {"status": {"type": "string"}}}`},
		{"no properties", `{"type": "object"}`},
		{"status missing", `{"type": "object", "properties": {"commit": {"type": "string"}}}`},
		{"status not a string", `{"type": "object", "properties": {"status": {"type": "integer"}}}`},
		{"status type is a list", `{"type": "object", "properties": {"status": {"type": ["string", "null"]}}}`},
		{"status without type", `{"type": "object", "properties": {"status": {"enum": ["done"]}}}`},
		{"status is not a schema", `{"type": "object", "properties": {"status": "string"}}`},
		{"unknown type name", `{"type": "object", "properties": {"status": {"type": "text"}}}`},
		{"empty type list", `{"type": "object", "properties": {"status": {"type": "string"}, "x": {"type": []}}}`},
		{
			"non-string in type list",
			`{"type": "object", "properties": {"status": {"type": "string"}, "x": {"type": [1]}}}`,
		},
		{"properties not an object", `{"type": "object", "properties": ["status"]}`},
		{
			"required not an array",
			`{"type": "object", "properties": {"status": {"type": "string"}}, "required": "status"}`,
		},
		{
			"required with a non-string",
			`{"type": "object", "properties": {"status": {"type": "string"}}, "required": [1]}`,
		},
		{"enum not an array", `{"type": "object", "properties": {"status": {"type": "string", "enum": "done"}}}`},
		{
			"items as a list",
			`{"type": "object", "properties": {"status": {"type": "string"}, "x": {"type": "array", "items": [{"type": "string"}]}}}`,
		},
		{
			"additionalProperties not bool or schema",
			`{"type": "object", "properties": {"status": {"type": "string", "additionalProperties": "no"}}}`,
		},
		{"property schema is a boolean", `{"type": "object", "properties": {"status": {"type": "string"}, "x": true}}`},
		{"pattern at root", `{"type": "object", "pattern": "^a", "properties": {"status": {"type": "string"}}}`},
		{"pattern at depth", `{"type": "object", "properties": {"status": {"type": "string", "pattern": "^d"}}}`},
		{
			"oneOf at depth",
			`{"type": "object", "properties": {"status": {"type": "string"}, "x": {"type": "object", "properties": {"y": {"oneOf": [{"type": "string"}]}}}}}`,
		},
		{
			"$ref at depth",
			`{"type": "object", "properties": {"status": {"type": "string"}, "x": {"items": {"$ref": "#/definitions/a"}, "type": "array"}}}`,
		},
		{
			"unsupported keyword in additionalProperties schema",
			`{"type": "object", "properties": {"status": {"type": "string"}, "x": {"additionalProperties": {"minLength": 1}}}}`,
		},
		{
			"unsupported keyword in a list item",
			`{"type": "object", "properties": {"status": {"type": "string"}, "x": {"type": "array", "items": {"maxItems": 2}}}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkStepSchema(tc.schema)
			if !errors.Is(err, ErrBadSchema) {
				t.Fatalf("checkStepSchema(%s) = %v, want ErrBadSchema", tc.schema, err)
			}
		})
	}
}

func TestCheckStepSchemaAcceptsSupportedSchemas(t *testing.T) {
	cases := []struct {
		name   string
		schema string
	}{
		{"status string with enum and commit, nested object and array", stepSchemaWithCommit},
		{"status only", `{"type": "object", "properties": {"status": {"type": "string"}}}`},
		{
			"annotations on root and status",
			`{"$schema": "x", "title": "t", "description": "d", "type": "object", "properties": {"status": {"type": "string", "title": "s", "description": "d", "default": "done", "examples": ["done"]}}}`,
		},
		{
			"empty subschema for a property",
			`{"type": "object", "properties": {"status": {"type": "string"}, "anything": {}}}`,
		},
		{
			"every supported type name",
			`{"type": "object", "properties": {"status": {"type": "string"}, "t": {"type": ["object", "array", "string", "number", "integer", "boolean", "null"]}}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkStepSchema(tc.schema); err != nil {
				t.Fatalf("checkStepSchema(%s) = %v, want nil", tc.schema, err)
			}
		})
	}
}

func TestValidateReturnPassesConformingValues(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{"status only", `{"status": "done"}`},
		{"status and commit null", `{"status": "blocked", "commit": null}`},
		{
			"full value",
			`{"status": "done", "commit": "abc", "gate": {"passed": true, "runs": 2}, "notes": [{"text": "ok"}], "extra": {"a": 1.5}}`,
		},
		{"empty array", `{"status": "done", "notes": []}`},
		{"integer as a whole number", `{"status": "done", "gate": {"passed": false, "runs": 3}}`},
		{"extra keys allowed at root", `{"status": "done", "other": 1}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateReturn(stepSchemaWithCommit, []byte(tc.value)); err != nil {
				t.Fatalf("validateReturn(%s) = %v, want nil", tc.value, err)
			}
		})
	}
}

func TestValidateReturnFailsWithPath(t *testing.T) {
	cases := []struct {
		name     string
		schema   string
		value    []byte
		wantPath string
	}{
		{"nil value", stepSchemaWithCommit, nil, "$"},
		{"empty value", stepSchemaWithCommit, []byte{}, "$"},
		{"null value", stepSchemaWithCommit, []byte(`null`), "$"},
		{"not JSON", stepSchemaWithCommit, []byte(`{"status":`), "$"},
		{"root is a string", stepSchemaWithCommit, []byte(`"done"`), "$"},
		{"status wrong type", stepSchemaWithCommit, []byte(`{"status": 1}`), "$.status"},
		{"status missing", stepSchemaWithCommit, []byte(`{"commit": "abc"}`), "$.status"},
		{"status null", stepSchemaWithCommit, []byte(`{"status": null}`), "$.status"},
		{"commit wrong type", stepSchemaWithCommit, []byte(`{"status": "done", "commit": 7}`), "$.commit"},
		{"status outside enum", stepSchemaWithCommit, []byte(`{"status": "maybe"}`), "$.status"},
		{
			"nested required field missing",
			stepSchemaWithCommit,
			[]byte(`{"status": "done", "gate": {"runs": 1}}`),
			"$.gate.passed",
		},
		{
			"nested wrong type",
			stepSchemaWithCommit,
			[]byte(`{"status": "done", "gate": {"passed": "yes"}}`),
			"$.gate.passed",
		},
		{
			"extra key under additionalProperties false",
			stepSchemaWithCommit,
			[]byte(`{"status": "done", "gate": {"passed": true, "extra": 1}}`),
			"$.gate.extra",
		},
		{
			"bad array item",
			stepSchemaWithCommit,
			[]byte(`{"status": "done", "notes": [{"text": "ok"}, {"text": 2}]}`),
			"$.notes[1].text",
		},
		{
			"array item missing required field",
			stepSchemaWithCommit,
			[]byte(`{"status": "done", "notes": [{}]}`),
			"$.notes[0].text",
		},
		{"array given an object", stepSchemaWithCommit, []byte(`{"status": "done", "notes": {}}`), "$.notes"},
		{
			"additionalProperties schema rejects a value",
			stepSchemaWithCommit,
			[]byte(`{"status": "done", "extra": {"a": "x"}}`),
			"$.extra.a",
		},
		{
			"1.5 for integer",
			stepSchemaWithCommit,
			[]byte(`{"status": "done", "gate": {"passed": true, "runs": 1.5}}`),
			"$.gate.runs",
		},
		{
			"integer given a string",
			stepSchemaWithCommit,
			[]byte(`{"status": "done", "gate": {"passed": true, "runs": "2"}}`),
			"$.gate.runs",
		},
		{
			"number given a boolean",
			stepSchemaWithCommit,
			[]byte(`{"status": "done", "extra": {"a": true}}`),
			"$.extra.a",
		},
		{
			"boolean given a number",
			stepSchemaWithCommit,
			[]byte(`{"status": "done", "gate": {"passed": 0}}`),
			"$.gate.passed",
		},
		{"string given an array", stepSchemaWithCommit, []byte(`{"status": ["done"]}`), "$.status"},
		{
			"object where string or null is allowed",
			`{"type": "object", "properties": {"status": {"type": "string"}, "c": {"type": ["string", "null"]}}}`,
			[]byte(`{"status": "x", "c": {}}`),
			"$.c",
		},
		{
			"value outside a non-string enum",
			`{"type": "object", "properties": {"status": {"type": "string", "enum": ["done"]}, "n": {"enum": [1, 2]}}}`,
			[]byte(`{"status": "done", "n": 3}`),
			"$.n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateReturn(tc.schema, tc.value)
			if err == nil {
				t.Fatalf("validateReturn(%s) = nil, want an error naming %s", tc.value, tc.wantPath)
			}
			if !strings.Contains(err.Error(), tc.wantPath) {
				t.Fatalf("validateReturn(%s) = %v, want the error to name %s", tc.value, err, tc.wantPath)
			}
		})
	}
}

func TestValidateReturnRejectsUnacceptedSchema(t *testing.T) {
	err := validateReturn(
		`{"type": "object", "properties": {"status": {"type": "string", "pattern": "^d"}}}`,
		[]byte(`{"status": "done"}`),
	)
	if !errors.Is(err, ErrBadSchema) {
		t.Fatalf("validateReturn with an unsupported keyword = %v, want ErrBadSchema", err)
	}
}
