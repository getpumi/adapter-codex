// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: The Pumi Authors

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunRefusesInvocation(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if got := run(t.Context(), nil, strings.NewReader("{}\n"), &stdout, &stderr); got != 1 {
		t.Errorf("exit status = %d, want 1", got)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want nothing", stdout.String())
	}
	if !strings.Contains(stderr.String(), "no task") {
		t.Errorf("stderr = %q, want the reason", stderr.String())
	}
}

// TestRunHookRefusesWithoutPolicy checks that the hook refuses the call
// when it cannot read its policy: codex lets a call go on when its hook
// fails, so the hook does not fail.
func TestRunHookRefusesWithoutPolicy(t *testing.T) {
	var stdout, stderr bytes.Buffer
	got := run(t.Context(), []string{"hook", t.TempDir() + "/missing.json"}, strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"ls"}}`), &stdout, &stderr)
	if got != 0 {
		t.Errorf("exit status = %d, want 0", got)
	}
	if !strings.Contains(stdout.String(), `"permissionDecision":"deny"`) {
		t.Errorf("stdout = %q, want a refusal", stdout.String())
	}
}
