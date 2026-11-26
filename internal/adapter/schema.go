// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: The Pumi Authors

package adapter

import (
	"encoding/json"
	"fmt"
)

// codexSchema returns the invocation's JSON Schema in the form that the
// adapter gives codex with --output-schema: the schema as it is, after a
// check that it is a JSON object whose root, if it has a type, is an
// object, because the output is always a protobuf message.
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
	return s, nil
}
