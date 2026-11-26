// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: The Pumi Authors

package adapter

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// codexSchema returns the invocation's JSON Schema in the form that the
// adapter gives codex with --output-schema, after a check that it is a
// JSON object whose root, if it has a type, is an object, because the
// output is always a protobuf message.
//
// Codex asks OpenAI for structured outputs in strict mode, which refuses a
// schema whose objects do not list every property in required ("required
// must include declared property \"summary\"", getpumi/pumi#125). So every
// object of the schema lists all its properties in required, a property
// that it did not require may also be null, which protojson reads as an
// unset field, every object with properties is closed with
// additionalProperties false, and default is left out. A $ref may have no
// sibling keyword ("$ref cannot have sibling keyword \"$id\" in strict
// schemas"), so the definition that the root of a bundle refers to becomes
// the root, a $ref keeps no sibling, and $id, $schema and the
// patternProperties that match the protobuf field names, which protojson
// also reads in their JSON names, are left out.
func codexSchema(s string) (string, error) {
	var root any
	if err := json.Unmarshal([]byte(s), &root); err != nil {
		return "", fmt.Errorf("%w: its output schema is not JSON: %w", ErrInvocation, err)
	}
	m, ok := root.(map[string]any)
	if !ok {
		return "", fmt.Errorf("%w: its output schema is not a JSON object", ErrInvocation)
	}
	if t, ok := m["type"]; ok && t != "object" {
		return "", fmt.Errorf("%w: its output schema has the type %v, not object", ErrInvocation, t)
	}
	m, err := inlineRoot(m)
	if err != nil {
		return "", fmt.Errorf("%w: its output schema: %w", ErrInvocation, err)
	}
	b, err := json.Marshal(strict(m))
	if err != nil {
		return "", fmt.Errorf("%w: its output schema: %w", ErrInvocation, err)
	}
	return string(b), nil
}

// subschemas are the keywords whose value is one schema, and listSchemas
// those whose value is a list of schemas, which strict reaches.
var (
	subschemas  = []string{"items", "not", "additionalProperties", "contains"}
	listSchemas = []string{"anyOf", "oneOf", "allOf", "prefixItems"}
)

// inlineRoot returns the schema m with the definition that its root's $ref
// names in $defs as the root, beside the same $defs, or m when its root
// has no such $ref.
func inlineRoot(m map[string]any) (map[string]any, error) {
	ref, ok := m["$ref"].(string)
	if !ok {
		return m, nil
	}
	defs, _ := m["$defs"].(map[string]any)
	name, ok := strings.CutPrefix(ref, "#/$defs/")
	def, found := defs[name].(map[string]any)
	if !ok || !found {
		return nil, fmt.Errorf("its root refers to %q, which is not one of its $defs", ref)
	}
	b, err := json.Marshal(def)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		return nil, err
	}
	root["$defs"] = defs
	return root, nil
}

// dropped are the keywords that strict leaves out of every schema.
var dropped = []string{"default", "$id", "$schema", "patternProperties"}

// strict returns the schema s in the form of codexSchema. It changes s.
func strict(s any) any {
	m, ok := s.(map[string]any)
	if !ok {
		return s
	}
	if ref, ok := m["$ref"]; ok {
		return map[string]any{"$ref": ref}
	}
	for _, k := range dropped {
		delete(m, k)
	}
	strictNested(m)
	if props, ok := m["properties"].(map[string]any); ok {
		closeObject(m, props)
	}
	return m
}

// strictNested makes the schemas nested in m strict: those of subschemas,
// listSchemas and $defs.
func strictNested(m map[string]any) {
	for _, k := range subschemas {
		if v, ok := m[k]; ok {
			m[k] = strict(v)
		}
	}
	for _, k := range listSchemas {
		if list, ok := m[k].([]any); ok {
			for i, v := range list {
				list[i] = strict(v)
			}
		}
	}
	if defs, ok := m["$defs"].(map[string]any); ok {
		for k, v := range defs {
			defs[k] = strict(v)
		}
	}
}

// closeObject makes the object schema m, whose properties are props, list
// every property in required, lets the properties that it did not require
// be null, and closes it unless additionalProperties is a schema, as for a
// protobuf map.
func closeObject(m, props map[string]any) {
	var required []string
	if list, ok := m["required"].([]any); ok {
		for _, v := range list {
			if name, ok := v.(string); ok {
				required = append(required, name)
			}
		}
	}
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	names := make([]any, 0, len(keys))
	for _, k := range keys {
		p := strict(props[k])
		if !slices.Contains(required, k) {
			p = nullable(p)
		}
		props[k] = p
		names = append(names, k)
	}
	m["required"] = names
	if _, isSchema := m["additionalProperties"].(map[string]any); !isSchema {
		m["additionalProperties"] = false
	}
}

// nullable returns the schema s, which a property did not require, so that
// it also allows null: with null added to its type and enum when it has a
// type, and as an anyOf with null otherwise.
func nullable(s any) any {
	m, ok := s.(map[string]any)
	if !ok {
		return s
	}
	switch t := m["type"].(type) {
	case string:
		if t != "null" {
			m["type"] = []any{t, "null"}
		}
	case []any:
		if !slices.Contains(t, any("null")) {
			m["type"] = append(t, "null")
		}
	default:
		return map[string]any{"anyOf": []any{m, map[string]any{"type": "null"}}}
	}
	if enum, ok := m["enum"].([]any); ok && !slices.Contains(enum, nil) {
		m["enum"] = append(enum, nil)
	}
	return m
}
