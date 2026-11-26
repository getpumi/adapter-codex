// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: The Pumi Authors

package adapter

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	harnessv1 "getpumi.dev/pumi/api/gen/pumi/harness/v1"
	"google.golang.org/protobuf/proto"
)

// hookCall runs the hook with policy p on the tool call of tool with input,
// and returns whether it refused the call and the refusals it recorded.
func hookCall(t *testing.T, p *Policy, tool string, input map[string]any) (bool, []*harnessv1.Refusal) {
	t.Helper()
	dir := t.TempDir()
	p.Refusals = filepath.Join(dir, "refusals.jsonl")
	file := filepath.Join(dir, "policy.json")
	if err := p.write(file); err != nil {
		t.Fatal(err)
	}
	call, err := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "cwd": p.Workspace, "tool_name": tool, "tool_input": input, "tool_use_id": "call_1"})
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	Hook(file, bytes.NewReader(call), &stdout, &stderr)
	refused := strings.Contains(stdout.String(), `"permissionDecision":"deny"`)
	if !refused && stdout.Len() > 0 {
		t.Errorf("the hook wrote %q for a call it allows, want nothing", stdout.String())
	}
	return refused, readRefusals(p.Refusals, &stderr)
}

func TestHook(t *testing.T) {
	ws := t.TempDir()
	patchText := func(path string) string {
		return "*** Begin Patch\n*** Add File: " + path + "\n+a\n*** End Patch\n"
	}
	patch := func(path string) map[string]any { return map[string]any{"command": patchText(path)} }
	heredoc := func(path string) map[string]any {
		return map[string]any{"command": "apply_patch <<'EOF'\n" + patchText(path) + "EOF"}
	}
	edit, run, read := harnessv1.Capabilities_TOOL_KIND_EDIT, harnessv1.Capabilities_TOOL_KIND_RUN_COMMANDS, harnessv1.Capabilities_TOOL_KIND_READ
	editRefusal := func(p string) *harnessv1.Refusal {
		return &harnessv1.Refusal{Tool: edit, Asked: &harnessv1.Refusal_Path{Path: p}}
	}
	runRefusal := func(c string) *harnessv1.Refusal {
		return &harnessv1.Refusal{Tool: run, Asked: &harnessv1.Refusal_Command{Command: c}}
	}
	tests := []struct {
		name    string
		caps    *harnessv1.Capabilities
		tool    string
		input   map[string]any
		refused bool
		refusal *harnessv1.Refusal
	}{
		{"patch in a write path", caps(edit, "src/**"), "apply_patch", patch("src/a.txt"), false, nil},
		{"patch outside the write paths", caps(edit, "src/**"), "apply_patch", patch("docs/a.txt"), true, editRefusal("docs/a.txt")},
		{"patch without the edit kind", caps(run, "src/**"), "apply_patch", patch("src/a.txt"), true, editRefusal("src/a.txt")},
		{"patch command in a write path", caps(edit, "src/**"), "Bash", heredoc("src/a.txt"), false, nil},
		{"patch command outside", caps(edit, "src/**"), "Bash", heredoc("lib/src/a.txt"), true, editRefusal("lib/src/a.txt")},
		{"listed command", withCommands(caps(run, "out/**"), "touch"), "Bash", map[string]any{"command": "touch out/a"}, false, nil},
		{"unlisted command", withCommands(caps(run, "out/**"), "touch"), "Bash", map[string]any{"command": "mkdir out/a"}, true, runRefusal("mkdir out/a")},
		{"command without the run kind", withCommands(caps(edit, "out/**"), "touch"), "Bash", map[string]any{"command": "touch out/a"}, true, runRefusal("touch out/a")},
		{"read command with the read kind", caps(read), "Bash", map[string]any{"command": "cat .pumi/plan.md"}, false, nil},
		{"read pipeline with the read kind", caps(read), "Bash", map[string]any{"command": "rg -n '^' .pumi/plan.md | head -50"}, false, nil},
		{"read command without the read kind", caps(edit), "Bash", map[string]any{"command": "cat .pumi/plan.md"}, true, runRefusal("cat .pumi/plan.md")},
		{"read command that writes", caps(read), "Bash", map[string]any{"command": "cat a > b"}, true, runRefusal("cat a > b")},
		{"image with the read kind", caps(read), "view_image", map[string]any{"path": "a.png"}, false, nil},
		{"image without the read kind", caps(edit), "view_image", map[string]any{"path": "a.png"}, true, &harnessv1.Refusal{Tool: read, Asked: &harnessv1.Refusal_Path{Path: "a.png"}}},
		{"subagent", caps(read, edit, run), "spawn_agent", map[string]any{"message": "x"}, true, nil},
		{"mcp tool", caps(read, edit, run), "mcp__fs__write", map[string]any{}, true, nil},
		{"plan", caps(), "update_plan", map[string]any{"plan": []any{}}, false, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewPolicy(ws, tt.caps)
			refused, refusals := hookCall(t, p, tt.tool, tt.input)
			if refused != tt.refused {
				t.Errorf("refused = %v, want %v", refused, tt.refused)
			}
			switch {
			case tt.refusal == nil && len(refusals) > 0:
				t.Errorf("refusals = %v, want none", refusals)
			case tt.refusal != nil && (len(refusals) != 1 || !proto.Equal(refusals[0], tt.refusal)):
				t.Errorf("refusals = %v, want %v", refusals, tt.refusal)
			}
		})
	}
}

func TestHookRefusesWhatItCannotRead(t *testing.T) {
	ws := t.TempDir()
	p := NewPolicy(ws, caps(harnessv1.Capabilities_TOOL_KIND_RUN_COMMANDS))
	p.Refusals = filepath.Join(ws, "refusals.jsonl")
	file := filepath.Join(ws, "policy.json")
	if err := p.write(file); err != nil {
		t.Fatal(err)
	}
	for _, in := range []string{"", "not json", `{"tool_name":"Bash","tool_input":{"command":1}}`} {
		var stdout, stderr bytes.Buffer
		Hook(file, strings.NewReader(in), &stdout, &stderr)
		if !strings.Contains(stdout.String(), `"permissionDecision":"deny"`) {
			t.Errorf("Hook(%q) wrote %q, want a refusal", in, stdout.String())
		}
	}
	if err := os.WriteFile(file, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	Hook(file, strings.NewReader(`{"tool_name":"update_plan","tool_input":{}}`), &stdout, &stderr)
	if !strings.Contains(stdout.String(), `"permissionDecision":"deny"`) {
		t.Errorf("Hook with a policy that does not parse wrote %q, want a refusal", stdout.String())
	}
}

func caps(kinds ...any) *harnessv1.Capabilities {
	c := &harnessv1.Capabilities{Network: &harnessv1.Capabilities_Network{Access: harnessv1.Capabilities_Network_ACCESS_GATEWAY}}
	for _, k := range kinds {
		switch k := k.(type) {
		case harnessv1.Capabilities_ToolKind:
			c.Tools = append(c.Tools, k)
		case string:
			c.WritePaths = append(c.WritePaths, k)
		}
	}
	return c
}

func withCommands(c *harnessv1.Capabilities, commands ...string) *harnessv1.Capabilities {
	c.Commands = commands
	return c
}
