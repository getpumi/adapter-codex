// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: The Pumi Authors

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"getpumi.dev/adapter-codex/internal/adapter"
	harnessv1 "getpumi.dev/pumi/api/gen/pumi/harness/v1"
)

// writeIn writes content to the file name, a path relative to dir, through
// an os.Root (gosec G304).
func writeIn(t *testing.T, dir, name, content string) {
	t.Helper()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if err := root.MkdirAll(filepath.Dir(name), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := root.WriteFile(name, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// readIn returns the content of the file name in dir, or "" when it cannot
// be read.
func readIn(t *testing.T, dir, name string) string {
	t.Helper()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	b, err := root.ReadFile(name)
	if err != nil {
		return ""
	}
	return string(b)
}

// codexElsewhere returns the path of a codex at a place that codex's
// sandbox does not let commands read by itself, as for a person who
// installed codex in their home: the pinned codex's executable and its
// bundled bwrap, which codex finds in codex-resources beside bin, linked
// into a new temporary directory. Codex starts itself again inside its
// sandbox from its own path, which failed with "bwrap: execvp
// /home/.../codex/bin/codex: No such file or directory" when the profile
// did not let the sandbox read it (getpumi/pumi#125).
func codexElsewhere(t *testing.T) string {
	t.Helper()
	found, err := exec.LookPath("codex")
	if err != nil {
		t.Fatalf("codex is not on the PATH: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(found)
	if err != nil {
		t.Fatal(err)
	}
	// The npm package's native build keeps the executable in bin.
	install := filepath.Dir(filepath.Dir(resolved))
	elsewhere := filepath.Join(t.TempDir(), "codex")
	for _, f := range []string{"bin/codex", "codex-resources/bwrap"} {
		to := filepath.Join(elsewhere, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(to), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(filepath.Join(install, filepath.FromSlash(f)), to); err != nil {
			t.Fatalf("cannot link %s of codex's install %s into the test's directory: %v", f, install, err)
		}
	}
	return filepath.Join(elsewhere, "bin", "codex")
}

// TestTheSandboxLetsCommandsWriteExactWritePaths runs a command in the
// pinned codex's sandbox with the configuration that Translate gives a
// capability list whose write paths name single files: one that exists,
// one that the command creates and one at the top of the workspace. Codex
// 0.172.0 takes a writable path as a directory: with an existing file it
// failed before the command ran ("bwrap: Can't mkdir parents for
// .../early_test.go/.git: Not a directory"), and it did not let the
// command create a file it did not have, so the adapter must not give it
// the file (getpumi/pumi#125).
func TestTheSandboxLetsCommandsWriteExactWritePaths(t *testing.T) {
	codex := codexElsewhere(t)
	ws := t.TempDir()
	gitInit := exec.CommandContext(t.Context(), "git", "init", "--quiet")
	gitInit.Dir = ws
	if out, err := gitInit.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	writeIn(t, ws, "src/a.txt", "old\n")
	caps := &harnessv1.Capabilities{
		WritePaths: []string{"src/a.txt", "src/new.txt", "TOP.md"},
		Tools:      []harnessv1.Capabilities_ToolKind{harnessv1.Capabilities_TOOL_KIND_EDIT, harnessv1.Capabilities_TOOL_KIND_RUN_COMMANDS},
	}
	// codex sandbox reads the configuration from config.toml in
	// CODEX_HOME, so that its arguments are constants (gosec G204).
	home := t.TempDir()
	dir, err := adapter.InstallDirectory(codex)
	if err != nil {
		t.Fatal(err)
	}
	writeIn(t, home, "config.toml", strings.Join(adapter.Translate(caps, dir), "\n")+"\n")
	t.Setenv("PATH", filepath.Dir(codex)+string(filepath.ListSeparator)+os.Getenv("PATH"))
	cmd := exec.CommandContext(t.Context(), "codex", "sandbox", "--",
		"sh", "-c", "echo new > src/a.txt && echo created > src/new.txt && echo top > TOP.md")
	cmd.Dir = ws
	cmd.Env = append(os.Environ(), "CODEX_HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Errorf("codex sandbox: %v\n%s", err, out)
	}
	for name, want := range map[string]string{"src/a.txt": "new\n", "src/new.txt": "created\n", "TOP.md": "top\n"} {
		if got := readIn(t, ws, name); got != want {
			t.Errorf("%s holds %q after the command, want %q", name, got, want)
		}
	}
}
