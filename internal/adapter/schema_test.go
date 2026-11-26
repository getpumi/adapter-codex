// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: The Pumi Authors

package adapter

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

// implementationOutput is the bundle that the core's make generate writes
// for pumi.workflow.v1.ImplementationOutput, the output of the spike
// workflow's agent state.
const implementationOutput = `{
  "$defs": {
    "pumi.workflow.v1.ImplementationOutput.jsonschema.json": {
      "$schema": "https://json-schema.org/draft/2020-12/schema",
      "additionalProperties": false,
      "description": "ImplementationOutput is the structured output.",
      "properties": {
        "summary": {"default": "", "description": "What the change does.", "type": "string"}
      },
      "title": "Implementation Output",
      "type": "object"
    }
  },
  "$id": "pumi.workflow.v1.ImplementationOutput.jsonschema.bundle.json",
  "$ref": "#/$defs/pumi.workflow.v1.ImplementationOutput.jsonschema.json",
  "$schema": "https://json-schema.org/draft/2020-12/schema"
}`

// TestTheOutputSchemaIsMadeStrict: OpenAI's structured outputs, which
// codex's --output-schema uses, refuse an object whose required does not
// list every property ("Invalid schema for response_format
// 'codex_output_schema': ... required must include declared property
// \"summary\"", answered with 400 on the API key path and on a ChatGPT
// sign-in, getpumi/pumi#125). The adapter lists every property in
// required, lets a property that the schema did not require be null, which
// protojson reads as unset, closes every object and leaves out default.
func TestTheOutputSchemaIsMadeStrict(t *testing.T) {
	got, err := codexSchema(implementationOutput)
	if err != nil {
		t.Fatal(err)
	}
	want := `{
	  "$defs": {
	    "pumi.workflow.v1.ImplementationOutput.jsonschema.json": {
	      "$schema": "https://json-schema.org/draft/2020-12/schema",
	      "additionalProperties": false,
	      "description": "ImplementationOutput is the structured output.",
	      "properties": {
	        "summary": {"description": "What the change does.", "type": ["string", "null"]}
	      },
	      "required": ["summary"],
	      "title": "Implementation Output",
	      "type": "object"
	    }
	  },
	  "$id": "pumi.workflow.v1.ImplementationOutput.jsonschema.bundle.json",
	  "$ref": "#/$defs/pumi.workflow.v1.ImplementationOutput.jsonschema.json",
	  "$schema": "https://json-schema.org/draft/2020-12/schema"
	}`
	sameJSON(t, got, want)
}

func TestAStrictSchemaKeepsWhatWasRequiredAndReachesNestedSchemas(t *testing.T) {
	in := `{"type": "object", "properties": {
	  "id": {"type": "string"},
	  "kind": {"type": "string", "enum": ["a", "b"]},
	  "count": {"anyOf": [{"type": "integer"}, {"type": "string", "pattern": "^[0-9]+$"}]},
	  "child": {"$ref": "#/$defs/child"},
	  "tags": {"type": "array", "items": {"type": "object", "properties": {"name": {"type": "string"}}}},
	  "either": {"type": ["string", "integer"]}
	}, "required": ["id"],
	"$defs": {"child": {"type": "object", "properties": {"x": {"type": "boolean"}}, "additionalProperties": true}}}`
	got, err := codexSchema(in)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"type": "object", "additionalProperties": false, "properties": {
	  "id": {"type": "string"},
	  "kind": {"type": ["string", "null"], "enum": ["a", "b", null]},
	  "count": {"anyOf": [{"anyOf": [{"type": "integer"}, {"type": "string", "pattern": "^[0-9]+$"}]}, {"type": "null"}]},
	  "child": {"anyOf": [{"$ref": "#/$defs/child"}, {"type": "null"}]},
	  "tags": {"type": ["array", "null"], "items": {"type": "object", "additionalProperties": false, "properties": {"name": {"type": ["string", "null"]}}, "required": ["name"]}},
	  "either": {"type": ["string", "integer", "null"]}
	}, "required": ["child", "count", "either", "id", "kind", "tags"],
	"$defs": {"child": {"type": "object", "properties": {"x": {"type": ["boolean", "null"]}}, "required": ["x"], "additionalProperties": false}}}`
	sameJSON(t, got, want)
}

func TestTheOutputSchemaMustBeAnObject(t *testing.T) {
	for _, s := range []string{`not json`, `[1]`, `{"type": "string"}`} {
		if _, err := codexSchema(s); !errors.Is(err, ErrInvocation) {
			t.Errorf("codexSchema(%s) = %v, want ErrInvocation", s, err)
		}
	}
}

// sameJSON fails the test unless got and want are the same JSON value.
func sameJSON(t *testing.T, got, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Fatalf("the schema is not JSON: %v\n%s", err, got)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("the schema is\n%s\nwant\n%s", got, want)
	}
}
