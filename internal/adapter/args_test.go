// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: The Pumi Authors

package adapter

import (
	"errors"
	"slices"
	"strings"
	"testing"

	harnessv1 "getpumi.dev/pumi/api/gen/pumi/harness/v1"
)

func invocation(caps *harnessv1.Capabilities) *harnessv1.Invocation {
	return &harnessv1.Invocation{
		Input:        &harnessv1.Input{Task: "say woof"},
		ModelRole:    "frontier",
		Model:        "gpt-6.2-sol",
		Capabilities: caps,
	}
}

func gateway() *harnessv1.Gateway {
	return &harnessv1.Gateway{BaseUrl: "http://127.0.0.1:4100/", Token: "tok-123"}
}

// configOf returns the values of the -c arguments.
func configOf(args []string) []string {
	var out []string
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-c" {
			out = append(out, args[i+1])
		}
	}
	return out
}

func TestArgsSignIn(t *testing.T) {
	inv := invocation(caps(harnessv1.Capabilities_TOOL_KIND_READ))
	args, err := Args(inv, Files{Hook: "/bin/adapter-codex hook /run/policy.json", Schema: "/run/schema.json"}, []string{"OPENAI_BASE_URL=http://proxy/v1"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"exec", "--json", "--ignore-user-config", "--ignore-rules", "--ephemeral", "--dangerously-bypass-hook-trust", "--color", "never", "-m", "gpt-6.2-sol", "--output-schema", "/run/schema.json"}
	if !slices.Equal(args[:len(want)], want) {
		t.Errorf("args start %q, want %q", args[:len(want)], want)
	}
	config := configOf(args)
	for _, c := range []string{
		`openai_base_url="http://proxy/v1"`,
		`web_search="disabled"`,
		`approval_policy="never"`,
		`hooks.PreToolUse=[{matcher="",hooks=[{type="command",command="/bin/adapter-codex hook /run/policy.json",timeout=60}]}]`,
	} {
		if !slices.Contains(config, c) {
			t.Errorf("config %q does not hold %q", config, c)
		}
	}
	for _, c := range config {
		if strings.HasPrefix(c, "model_provider") {
			t.Errorf("the sign-in path sets %q, want codex's own provider", c)
		}
	}
	if slices.Contains(args, "say woof") {
		t.Error("the task is an argument, want it on standard input")
	}
}

func TestArgsGateway(t *testing.T) {
	inv := invocation(caps(harnessv1.Capabilities_TOOL_KIND_READ))
	inv.Gateway = gateway()
	args, err := Args(inv, Files{Hook: "hook"}, []string{"OPENAI_BASE_URL=http://proxy/v1"})
	if err != nil {
		t.Fatal(err)
	}
	if i := slices.Index(args, "-m"); i < 0 || args[i+1] != "frontier" {
		t.Errorf("args %q do not ask for the model role", args)
	}
	if slices.Contains(args, "--output-schema") {
		t.Error("args pass an output schema that the invocation does not have")
	}
	config := configOf(args)
	wantProvider := `model_providers.pumi-gateway={name="Pumi model gateway",base_url="http://127.0.0.1:4100/v1",wire_api="responses",env_http_headers={"X-Pumi-Token"="PUMI_CODEX_GATEWAY_HEADER"},request_max_retries=0,stream_max_retries=0,supports_websockets=false}`
	for _, c := range []string{`model_provider="pumi-gateway"`, wantProvider} {
		if !slices.Contains(config, c) {
			t.Errorf("config %q does not hold %q", config, c)
		}
	}
	for _, a := range args {
		if strings.Contains(a, "tok-123") {
			t.Errorf("argument %q holds the token", a)
		}
		if strings.HasPrefix(a, "openai_base_url") {
			t.Errorf("the gateway path keeps the person's base URL: %q", a)
		}
	}
}

func TestArgsRefusesInvocation(t *testing.T) {
	for name, change := range map[string]func(*harnessv1.Invocation){
		"no task":            func(i *harnessv1.Invocation) { i.Input = nil },
		"no model":           func(i *harnessv1.Invocation) { i.Model = "" },
		"no role":            func(i *harnessv1.Invocation) { i.Gateway, i.ModelRole = gateway(), "" },
		"gateway URL":        func(i *harnessv1.Invocation) { i.Gateway = gateway(); i.Gateway.BaseUrl = "unix:///run/gw" },
		"keeps sign-in":      func(i *harnessv1.Invocation) { i.Gateway = gateway(); i.Gateway.KeepsSignIn = true },
		"no token":           func(i *harnessv1.Invocation) { i.Gateway = gateway(); i.Gateway.Token = "" },
		"token with a space": func(i *harnessv1.Invocation) { i.Gateway = gateway(); i.Gateway.Token = "a b" },
	} {
		t.Run(name, func(t *testing.T) {
			inv := invocation(caps())
			change(inv)
			_, err := Args(inv, Files{}, nil)
			if !errors.Is(err, ErrInvocation) {
				t.Fatalf("Args() error = %v, want ErrInvocation", err)
			}
			if inv.GetGateway().GetToken() != "" && strings.Contains(err.Error(), inv.GetGateway().GetToken()) {
				t.Errorf("error %q holds the token", err)
			}
		})
	}
}

func TestTranslate(t *testing.T) {
	edit, run, read := harnessv1.Capabilities_TOOL_KIND_EDIT, harnessv1.Capabilities_TOOL_KIND_RUN_COMMANDS, harnessv1.Capabilities_TOOL_KIND_READ
	profile := func(workspace string) string {
		return `permissions.pumi.filesystem={":minimal"="read",":workspace_roots"={` + workspace + `}}`
	}
	for name, tc := range map[string]struct {
		caps  *harnessv1.Capabilities
		want  string
		shell bool
	}{
		"read only":      {caps(read, "src/**"), profile(`"."="read"`), false},
		"edit":           {caps(read, edit, "src/**", "docs/*.md", "go.mod"), profile(`"."="write","docs/**"="write","src/**"="write"`), true},
		"run":            {caps(run, "out/**"), profile(`"."="read","out/**"="write"`), true},
		"leading glob":   {caps(edit, "*.go"), profile(`"."="write"`), true},
		"no write paths": {caps(edit), profile(`"."="read"`), true},
	} {
		t.Run(name, func(t *testing.T) {
			got := Translate(tc.caps)
			want := []string{
				`default_permissions="pumi"`,
				tc.want,
				"permissions.pumi.network.enabled=false",
				"features.shell_tool=" + map[bool]string{true: "true", false: "false"}[tc.shell],
			}
			if !slices.Equal(got, want) {
				t.Errorf("Translate() =\n%q\nwant\n%q", got, want)
			}
		})
	}
}
