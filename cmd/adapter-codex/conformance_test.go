// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: The Pumi Authors

package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"getpumi.dev/adapter-codex/internal/adapter"
	"getpumi.dev/pumi/conformance"
)

// adapterEnv is set when the test binary runs as the adapter under test, or
// as the adapter's hook: TestMain then runs the adapter's main function
// instead of the tests.
const adapterEnv = "PUMI_ADAPTER_CODEX_UNDER_TEST"

// keptEnv are the variables of the test's environment that the conformance
// runs keep. TestMain clears the rest, so that a person's own settings, or
// the settings of a cloud container, do not reach codex, and gives codex a
// HOME and a CODEX_HOME of their own with no sign-in.
var keptEnv = []string{"PATH", "TMPDIR", "LANG", "GOCOVERDIR"}

func TestMain(m *testing.M) {
	if os.Getenv(adapterEnv) != "" {
		os.Exit(run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
	}
	clearEnv()
	os.Exit(m.Run())
}

// installTools is the script of the devcontainer image that pins codex's
// version.
const installTools = "../../.devcontainer/install-tools.sh"

// pinnedCodex returns the version of codex that install-tools.sh pins.
func pinnedCodex(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(installTools)
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^CODEX_VERSION=(\S+)$`).FindSubmatch(data)
	if m == nil {
		t.Fatalf("%s pins no CODEX_VERSION", installTools)
	}
	return string(m[1])
}

// TestCodexVersion checks that the codex on the PATH, which the conformance
// suite runs, is the version that the devcontainer pins.
func TestCodexVersion(t *testing.T) {
	want := pinnedCodex(t)
	out, err := exec.CommandContext(t.Context(), "codex", "--version").Output()
	if err != nil {
		t.Fatalf("codex --version: %v; the conformance suite runs the pinned codex %s, which the devcontainer installs", err, want)
	}
	if got := strings.TrimSpace(string(out)); got != "codex-cli "+want {
		t.Errorf("codex --version = %q, want codex-cli %s, the version that %s pins", out, want, installTools)
	}
}

// TestConformance runs the adapter conformance suite (decision 0244)
// against the adapter and the pinned real codex, which calls the suite's
// fake Responses API provider.
func TestConformance(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	codex, err := exec.LookPath("codex")
	if err != nil {
		t.Fatalf("codex is not on the PATH: %v", err)
	}
	home := t.TempDir()
	codexHome := t.TempDir()
	conformance.Run(t, conformance.Adapter{
		Path:  exe,
		API:   conformance.APIResponses,
		Modes: []conformance.Mode{conformance.ModeAPIKey, conformance.ModeSignIn},
		Turn:  turn,
		// The person's own sign-in, as an API key that codex reads from
		// CODEX_API_KEY, and the provider's address in OPENAI_BASE_URL,
		// which the adapter passes to codex on the sign-in path.
		SignIn: func(baseURL, credential string) []string {
			return []string{"OPENAI_BASE_URL=" + baseURL + "/v1", "CODEX_API_KEY=" + credential}
		},
		Env: []string{
			adapterEnv + "=1",
			"HOME=" + home,
			"CODEX_HOME=" + codexHome,
			"PATH=" + filepath.Dir(codex) + string(os.PathListSeparator) + os.Getenv("PATH"),
		},
		ReportsRefusals: true,
		ReportsLimits:   true,
		Unenforced: map[conformance.Item]string{
			conformance.ItemNetwork: adapter.UnenforcedNetwork,
		},
		Timeout: 3 * time.Minute,
	})
}

// clearEnv clears the environment except keptEnv.
func clearEnv() {
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if !slices.Contains(keptEnv, name) {
			_ = os.Unsetenv(name)
		}
	}
}

// turn turns a step of a conformance scenario into the tool call of Codex
// that a model makes for it. The suite's model names are names that codex
// has no metadata for, and codex then offers the model exec_command and no
// apply_patch tool, so a write is an apply_patch command, which codex runs
// as an edit of its own.
func turn(s conformance.Step) conformance.Turn {
	var cmd string
	switch s.Kind {
	case conformance.StepWrite:
		cmd = "apply_patch <<'PATCH'\n*** Begin Patch\n*** Add File: " + s.Path + "\n" + patchLines(s.Content) + "*** End Patch\nPATCH"
	case conformance.StepRead:
		cmd = "cat " + s.Path
	case conformance.StepRun:
		cmd = s.Command
	case conformance.StepFetch:
		cmd = "curl --fail --silent --show-error " + s.URL
	case conformance.StepOutput:
		return conformance.Turn{Text: s.Output}
	}
	return conformance.Turn{Calls: []conformance.ToolCall{{Name: "exec_command", Input: execInput(cmd)}}}
}

// patchLines returns content as the added lines of a patch.
func patchLines(content string) string {
	var b strings.Builder
	for _, line := range strings.SplitAfter(strings.TrimSuffix(content, "\n"), "\n") {
		b.WriteString("+" + strings.TrimSuffix(line, "\n") + "\n")
	}
	return b.String()
}

// execInput is the input of exec_command for the command cmd.
func execInput(cmd string) string {
	data, err := json.Marshal(map[string]string{"cmd": cmd})
	if err != nil {
		panic(err)
	}
	return string(data)
}
