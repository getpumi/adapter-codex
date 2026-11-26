// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: The Pumi Authors

package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	harnessv1 "getpumi.dev/pumi/api/gen/pumi/harness/v1"
)

// UnenforcedNetwork says why the conformance suite cannot see codex refuse
// what the network access leaves out (decision 0244): codex refuses it, but
// does not report the refusal.
const UnenforcedNetwork = "Codex CLI 0.172.0 has no tool that fetches a URL, and its sandbox gives commands the network entirely or not at all, so the adapter gives commands no network even when the list names hosts; a refused fetch shows only in the command's output, so the adapter cannot name the host as a refusal"

// GatewayHeaderVariable is the variable from which codex sends the
// invocation token to the model gateway, in the header X-Pumi-Token.
const GatewayHeaderVariable = "PUMI_CODEX_GATEWAY_HEADER"

// gatewayProvider is the name of the entry in codex's model_providers that
// points codex at the gateway.
const gatewayProvider = "pumi-gateway"

// profile is the name of the permission profile in which codex runs.
const profile = "pumi"

// Files are the files outside the workspace that the arguments of codex
// name: the hook's policy, the output's JSON Schema and the hook command.
type Files struct {
	// Hook is the command, for a shell, that runs the adapter as codex's
	// hook with its policy file.
	Hook string
	// Schema is the file of the output's JSON Schema; empty for none.
	Schema string
	// Codex is the directory of codex's install, which codex's sandbox
	// must read (InstallDirectory); empty for none.
	Codex string
	// Cache is the run's own cache directory, which the sandbox lets
	// commands write and Env names in XDG_CACHE_HOME (decision 0269); empty
	// for none.
	Cache string
}

// Sandbox is what codex's sandbox lets commands reach outside the
// workspace.
type Sandbox struct {
	// Codex is the directory of codex's install, which the sandbox must
	// read (InstallDirectory); empty for none.
	Codex string
	// ReadPaths are the invocation's read paths, such as a toolchain,
	// which the sandbox lets commands read (decision 0269).
	ReadPaths []string
	// Cache is the run's own cache directory, which the sandbox lets
	// commands write (decision 0269); empty for none.
	Cache string
}

// Args returns the arguments of codex for an invocation. codex exec reads
// the task from its standard input, which it does when no prompt is given
// as an argument, so a task that starts with "-" is not read as a flag.
//
//   - --json writes codex's events as lines, which the adapter reads for
//     the usage, the agent's last message and the errors.
//   - --ignore-user-config and --ignore-rules leave out the person's
//     config.toml and command rules. A rule that allows a command makes
//     codex run it outside the sandbox, so the adapter writes none.
//   - --ephemeral keeps no session to resume.
//   - --output-schema passes the output's JSON Schema.
//   - -m asks for the invocation's model role by name through the gateway,
//     which maps it to a model (decision 0233), or for the role's model on
//     the sign-in path (decision 0254). Codex has no fallback model of its
//     own, so the role's fallback on the same adapter is not passed.
//   - --dangerously-bypass-hook-trust runs the adapter's own hook, which
//     the configuration names, without a review by a person.
//
// The configuration (-c) translates the capability list (Translate), turns
// off what would reach beyond it, and points codex at the gateway with the
// invocation token in a header, or, on the sign-in path, at the address in
// the person's OPENAI_BASE_URL.
func Args(inv *harnessv1.Invocation, files Files, environ []string) ([]string, error) {
	if inv.GetInput().GetTask() == "" {
		return nil, fmt.Errorf("%w: it has no task", ErrInvocation)
	}
	model, err := modelOf(inv)
	if err != nil {
		return nil, err
	}
	args := []string{
		"exec",
		"--json",
		"--ignore-user-config",
		"--ignore-rules",
		"--ephemeral",
		"--dangerously-bypass-hook-trust",
		"--color", "never",
		"-m", model,
	}
	if files.Schema != "" {
		args = append(args, "--output-schema", files.Schema)
	}
	config := Translate(inv.GetCapabilities(), Sandbox{Codex: files.Codex, ReadPaths: inv.GetReadPaths(), Cache: files.Cache})
	config = append(config, hookConfig(files.Hook), "web_search="+tomlString("disabled"))
	config = append(config, quiet...)
	if gw := inv.GetGateway(); gw != nil {
		config = append(config, gatewayConfig(gw)...)
	} else if base := lookup(environ, "OPENAI_BASE_URL"); base != "" {
		config = append(config, "openai_base_url="+tomlString(base))
	}
	for _, c := range config {
		args = append(args, "-c", c)
	}
	return args, nil
}

// quiet turns off what codex does besides the run: its own approval
// prompts, subagents, analytics, feedback, update checks and history.
var quiet = []string{
	"approval_policy=" + tomlString("never"),
	"agents.enabled=false",
	"features.multi_agent=false",
	"analytics.enabled=false",
	"feedback.enabled=false",
	"check_for_update_on_startup=false",
	"history.persistence=" + tomlString("none"),
}

// modelOf returns the model that codex asks for: the model role through the
// gateway, or the role's model on the sign-in path.
func modelOf(inv *harnessv1.Invocation) (string, error) {
	if gw := inv.GetGateway(); gw != nil {
		if err := checkGateway(gw); err != nil {
			return "", err
		}
		if inv.GetModelRole() == "" {
			return "", fmt.Errorf("%w: it has a model gateway but no model role, which the gateway maps (decision 0233)", ErrInvocation)
		}
		return inv.GetModelRole(), nil
	}
	if inv.GetModel() == "" {
		return "", fmt.Errorf("%w: it has neither a model gateway nor a model, which the runner gives on the sign-in path (decision 0254)", ErrInvocation)
	}
	return inv.GetModel(), nil
}

// checkGateway refuses an invocation whose gateway codex cannot use. Codex
// goes through the gateway only on the API key path, where the gateway
// holds the key; with a sign-in, codex talks to OpenAI directly and the
// invocation has no gateway (decision 0240). The error never holds the
// token.
func checkGateway(gw *harnessv1.Gateway) error {
	u, err := url.Parse(gw.GetBaseUrl())
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%w: the model gateway's base URL %q is not an http or https URL", ErrInvocation, gw.GetBaseUrl())
	}
	if gw.GetKeepsSignIn() {
		return fmt.Errorf("%w: its model gateway expects the harness to keep its own sign-in, and codex goes through the gateway only with the gateway's API key; a sign-in runs with no gateway (decision 0240)", ErrInvocation)
	}
	t := gw.GetToken()
	if t == "" {
		return fmt.Errorf("%w: the model gateway has no token", ErrInvocation)
	}
	for _, c := range []byte(t) {
		// The token goes into an HTTP header: only visible ASCII, with no
		// space, keeps it one header value.
		if c < '!' || c > '~' {
			return fmt.Errorf("%w: the model gateway's token holds a character that is not visible ASCII", ErrInvocation)
		}
	}
	return nil
}

// gatewayConfig points codex at the gateway: a provider of its own in
// model_providers, at the gateway's address, that sends the invocation
// token in the header X-Pumi-Token from GatewayHeaderVariable, so the token
// is not in codex's arguments, and holds no key. Codex sends no call again
// by itself; the gateway retries once (decision 0233), and a limit is not
// waited out (decision 0249).
func gatewayConfig(gw *harnessv1.Gateway) []string {
	base := strings.TrimSuffix(gw.GetBaseUrl(), "/") + "/v1"
	provider := "{name=" + tomlString("Pumi model gateway") +
		",base_url=" + tomlString(base) +
		",wire_api=" + tomlString("responses") +
		",env_http_headers={" + tomlString("X-Pumi-Token") + "=" + tomlString(GatewayHeaderVariable) + "}" +
		",request_max_retries=0,stream_max_retries=0,supports_websockets=false}"
	return []string{
		"model_provider=" + tomlString(gatewayProvider),
		"model_providers." + gatewayProvider + "=" + provider,
	}
}

// hookConfig makes the command the PreToolUse hook of every tool call.
func hookConfig(command string) string {
	return "hooks.PreToolUse=[{matcher=" + tomlString("") + ",hooks=[{type=" + tomlString("command") +
		",command=" + tomlString(command) + ",timeout=60}]}]"
}

// Translate translates a capability list into codex's configuration
// (decision 0159): a permission profile for its sandbox, and whether it
// offers its shell tool. What its sandbox cannot express, the hook
// refuses (Hook), and the runner's confinement check covers the rest.
//
//   - The profile lets codex and its commands read the system's minimal
//     files, codex's own install, the read paths of the invocation, such
//     as a toolchain, and the workspace, and nothing else (decision 0269). Codex starts itself again inside its sandbox from its own
//     path, which failed with "bwrap: execvp .../codex/bin/codex: No such
//     file or directory" for a codex installed in the person's home, where
//     the minimal files do not reach. Codex itself reads the
//     workspace under the profile, such as its AGENTS.md, and stops when
//     it cannot, so the profile cannot leave reading out; codex has no tool
//     that reads a file other than its commands and view_image, which the
//     hook refuses without the read kind.
//   - It lets them write the write paths, as sandboxWritePath gives them,
//     when the list has the edit kind or the kind that runs commands, and
//     the run's own cache, which Env names in XDG_CACHE_HOME, so that a
//     command such as go test can keep a build cache (decision 0269). It
//     lets them write no other temporary directory and no other path
//     outside the workspace.
//   - Commands get no network: the model gateway is codex's, not theirs,
//     and codex cannot limit them to the list's hosts.
//   - Codex's shell tool is offered when the list has the kind that runs
//     commands, or the edit kind, because with a model that codex has no
//     metadata for its edits are apply_patch commands.
func Translate(caps *harnessv1.Capabilities, sandbox Sandbox) []string {
	codexDir := sandbox.Codex
	p := NewPolicy("", caps)
	workspace := map[string]string{".": "read"}
	if p.Edit || p.Run {
		for _, w := range caps.GetWritePaths() {
			workspace[sandboxWritePath(w)] = "write"
		}
	}
	keys := make([]string, 0, len(workspace))
	for k := range workspace {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	entries := make([]string, 0, len(keys))
	for _, k := range keys {
		entries = append(entries, tomlString(k)+"="+tomlString(workspace[k]))
	}
	filesystem := "{" + tomlString(":minimal") + "=" + tomlString("read") + ","
	if codexDir != "" {
		filesystem += tomlString(codexDir) + "=" + tomlString("read") + ","
	}
	for _, r := range sandbox.ReadPaths {
		filesystem += tomlString(r) + "=" + tomlString("read") + ","
	}
	if sandbox.Cache != "" {
		filesystem += tomlString(sandbox.Cache) + "=" + tomlString("write") + ","
	}
	filesystem += tomlString(":workspace_roots") + "={" + strings.Join(entries, ",") + "}}"
	return []string{
		"default_permissions=" + tomlString(profile),
		"permissions." + profile + ".filesystem=" + filesystem,
		"permissions." + profile + ".network.enabled=false",
		fmt.Sprintf("features.shell_tool=%t", p.Run || p.Edit),
	}
}

// tomlString returns s as a TOML basic string. A JSON string without HTML
// escapes is one.
func tomlString(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}

// lookup returns the value of the variable name in environ.
func lookup(environ []string, name string) string {
	for _, kv := range environ {
		if n, v, ok := strings.Cut(kv, "="); ok && n == name {
			return v
		}
	}
	return ""
}

// InstallDirectory returns the directory of codex's install for program,
// the path of the codex that the adapter runs: the directory of the file
// that the path resolves to, or the directory above it when that is a bin
// directory, as in the npm package's native build, whose bundled bwrap is
// in codex-resources beside bin.
func InstallDirectory(program string) (string, error) {
	resolved, err := filepath.EvalSymlinks(program)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(resolved)
	if filepath.Base(dir) == "bin" {
		dir = filepath.Dir(dir)
	}
	return dir, nil
}
