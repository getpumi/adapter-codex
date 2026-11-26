// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: The Pumi Authors

package adapter

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	harnessv1 "getpumi.dev/pumi/api/gen/pumi/harness/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// HookCommand is the first argument of the adapter when codex runs it as
// its PreToolUse hook: "adapter-codex hook <policy file>".
const HookCommand = "hook"

// Policy is what the hook needs of the invocation to judge a tool call: the
// workspace and the capability list (decision 0159), and the file where it
// records each refusal for the result (decision 0249). The adapter writes
// it as JSON to a file outside the workspace.
type Policy struct {
	Workspace  string   `json:"workspace"`
	Read       bool     `json:"read"`
	Edit       bool     `json:"edit"`
	Run        bool     `json:"run"`
	WritePaths []string `json:"write_paths"`
	Commands   []string `json:"commands"`
	Refusals   string   `json:"refusals"`
}

// NewPolicy returns the policy of a capability list for the workspace ws.
func NewPolicy(ws string, caps *harnessv1.Capabilities) *Policy {
	p := &Policy{Workspace: ws, WritePaths: caps.GetWritePaths(), Commands: caps.GetCommands()}
	for _, k := range caps.GetTools() {
		switch k {
		case harnessv1.Capabilities_TOOL_KIND_READ:
			p.Read = true
		case harnessv1.Capabilities_TOOL_KIND_EDIT:
			p.Edit = true
		case harnessv1.Capabilities_TOOL_KIND_RUN_COMMANDS:
			p.Run = true
		case harnessv1.Capabilities_TOOL_KIND_WEB, harnessv1.Capabilities_TOOL_KIND_UNSPECIFIED:
		}
	}
	return p
}

func (p *Policy) write(file string) error {
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return os.WriteFile(file, data, 0o600)
}

func readPolicy(file string) (*Policy, error) {
	data, err := readInDir(file)
	if err != nil {
		return nil, err
	}
	p := &Policy{}
	if err := json.Unmarshal(data, p); err != nil {
		return nil, err
	}
	return p, nil
}

// toolCall is the part of codex's PreToolUse input that the hook reads.
type toolCall struct {
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
}

// verdict is the hook's judgment of a tool call: why it refuses it, or
// nothing, and the refusals that it records.
type verdict struct {
	reason   string
	refusals []*harnessv1.Refusal
}

// Hook is codex's PreToolUse hook. It reads the tool call from stdin and
// refuses it, with a decision on stdout, when the capability list in the
// policy file leaves it out. A call that it allows gets no output. Codex
// lets a call go on when its hook fails, so Hook also refuses a call it
// cannot read or judge, and never fails; stderr is codex's log of the hook.
//
// Codex sends its edits (apply_patch) and its commands (Bash, for
// exec_command and shell) through the hook, and its other local tools by
// their names. The hook allows:
//
//   - an edit when the list has the edit kind and its write paths match
//     every file of the patch, also when the patch comes as an apply_patch
//     command, which codex carries out as its own edit;
//   - a command when the list has the kind that runs commands and one of
//     its entries allows it (decision 0248);
//   - a command that only reads (readOnlyCommand) when the list has the
//     read kind, because codex has no tool of its own that reads a file;
//   - view_image, which reads a file, when the list has the read kind;
//   - codex's tools that touch no file and no command, such as update_plan;
//
// and refuses subagents and MCP tools, which would run outside these rules.
func Hook(policyFile string, stdin io.Reader, stdout, stderr io.Writer) {
	v := judge(policyFile, stdin, stderr)
	if v.reason == "" {
		return
	}
	data, err := json.Marshal(map[string]any{"hookSpecificOutput": map[string]any{
		"hookEventName":            "PreToolUse",
		"permissionDecision":       "deny",
		"permissionDecisionReason": "Pumi: " + v.reason,
	}})
	if err != nil {
		data = []byte(`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny"}}`)
	}
	_, _ = stdout.Write(append(data, '\n'))
}

// judge reads the policy and the tool call and returns the verdict, and
// records its refusals.
func judge(policyFile string, stdin io.Reader, stderr io.Writer) verdict {
	p, err := readPolicy(policyFile)
	if err != nil {
		hookLog(stderr, "cannot read the policy: %v", err)
		return verdict{reason: "the adapter's hook cannot read its policy"}
	}
	var call toolCall
	if err := json.NewDecoder(stdin).Decode(&call); err != nil {
		hookLog(stderr, "cannot read the tool call: %v", err)
		return verdict{reason: "the adapter's hook cannot read the tool call"}
	}
	v := p.judge(call)
	if len(v.refusals) > 0 {
		if err := recordRefusals(p.Refusals, v.refusals); err != nil {
			hookLog(stderr, "cannot record a refusal: %v", err)
		}
	}
	return v
}

// otherTools are codex's own local tools that read or write no file and run
// no command, which the hook allows whatever the capability list holds.
var otherTools = []string{
	"update_plan", "request_user_input", "request_user_input_async", "send_message_to_user_async",
	"get_goal", "create_goal", "update_goal", "sleep", "wait", "clock",
}

// judge judges one tool call.
func (p *Policy) judge(call toolCall) verdict {
	var in struct {
		Command string `json:"command"`
		Path    string `json:"path"`
	}
	if len(call.ToolInput) > 0 && json.Unmarshal(call.ToolInput, &in) != nil {
		return verdict{reason: "the adapter's hook cannot read the input of " + call.ToolName}
	}
	switch {
	case call.ToolName == "apply_patch":
		return p.judgePatch(in.Command)
	case call.ToolName == "Bash":
		if patch, ok := patchCommand(in.Command); ok {
			return p.judgePatch(patch)
		}
		if p.Read && readOnlyCommand(in.Command) {
			return verdict{}
		}
		if !p.Run || !commandAllowed(p.Commands, in.Command) {
			return verdict{
				reason:   "the capability list does not allow the command " + in.Command,
				refusals: []*harnessv1.Refusal{{Tool: harnessv1.Capabilities_TOOL_KIND_RUN_COMMANDS, Asked: &harnessv1.Refusal_Command{Command: strings.TrimSpace(in.Command)}}},
			}
		}
		return verdict{}
	case call.ToolName == "view_image":
		if !p.Read {
			return verdict{
				reason:   "the capability list does not allow reading files",
				refusals: []*harnessv1.Refusal{{Tool: harnessv1.Capabilities_TOOL_KIND_READ, Asked: &harnessv1.Refusal_Path{Path: in.Path}}},
			}
		}
		return verdict{}
	case slices.Contains(otherTools, call.ToolName):
		return verdict{}
	}
	return verdict{reason: "the adapter does not allow codex's tool " + call.ToolName}
}

// judgePatch judges an edit with patch.
func (p *Policy) judgePatch(patch string) verdict {
	paths := patchPaths(patch)
	if len(paths) == 0 {
		return verdict{reason: "the adapter's hook finds no file in the patch"}
	}
	var refused []*harnessv1.Refusal
	for _, path := range paths {
		if !p.Edit || !writable(p.WritePaths, p.Workspace, path) {
			refused = append(refused, &harnessv1.Refusal{Tool: harnessv1.Capabilities_TOOL_KIND_EDIT, Asked: &harnessv1.Refusal_Path{Path: path}})
		}
	}
	if len(refused) == 0 {
		return verdict{}
	}
	if !p.Edit {
		return verdict{reason: "the capability list does not allow editing files", refusals: refused}
	}
	return verdict{reason: "the capability list's write paths do not allow " + refused[0].GetPath(), refusals: refused}
}

// recordRefusals appends refusals to the file, one protojson line each.
func recordRefusals(file string, refusals []*harnessv1.Refusal) error {
	if file == "" {
		return errors.New("the policy names no file for refusals")
	}
	root, err := os.OpenRoot(filepath.Dir(file))
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	f, err := root.OpenFile(filepath.Base(file), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	var b bytes.Buffer
	for _, r := range refusals {
		data, err := protojson.Marshal(r)
		if err != nil {
			_ = f.Close()
			return err
		}
		b.Write(append(data, '\n'))
	}
	_, err = f.Write(b.Bytes())
	return errors.Join(err, f.Close())
}

// readRefusals reads the refusals that the hook recorded, each once.
func readRefusals(file string, log io.Writer) []*harnessv1.Refusal {
	data, err := readInDir(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		logf(log, "cannot read the refusals of the hook: %v", err)
		return nil
	}
	var out []*harnessv1.Refusal
	s := bufio.NewScanner(bytes.NewReader(data))
	s.Buffer(nil, 16<<20)
	for s.Scan() {
		r := &harnessv1.Refusal{}
		if err := protojson.Unmarshal(s.Bytes(), r); err != nil {
			logf(log, "a refusal of the hook does not parse: %v", err)
			continue
		}
		if !slices.ContainsFunc(out, func(o *harnessv1.Refusal) bool { return proto.Equal(o, r) }) {
			out = append(out, r)
		}
	}
	return out
}

func hookLog(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, "adapter-codex hook: "+format+"\n", args...)
}

// readInDir reads a file of the adapter's run directory through an os.Root
// of the directory that holds it, so that a link in the file's place cannot
// lead the read out of it.
func readInDir(file string) ([]byte, error) {
	root, err := os.OpenRoot(filepath.Dir(file))
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return root.ReadFile(filepath.Base(file))
}
