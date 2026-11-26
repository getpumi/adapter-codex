// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: The Pumi Authors

package adapter

import (
	"slices"
	"testing"
)

func TestPatchPaths(t *testing.T) {
	patch := "*** Begin Patch\n" +
		"*** Add File: src/a.txt\n+a\n" +
		"*** Update File: src/b.txt\n*** Move to: docs/b.txt\n@@\n-x\n+y\n" +
		"*** Delete File: /abs/c.txt\n" +
		"*** End Patch\n"
	got := patchPaths(patch)
	want := []string{"src/a.txt", "src/b.txt", "docs/b.txt", "/abs/c.txt"}
	if !slices.Equal(got, want) {
		t.Errorf("patchPaths = %q, want %q", got, want)
	}
}

func TestWritable(t *testing.T) {
	ws := "/w"
	tests := []struct {
		globs []string
		path  string
		want  bool
	}{
		{[]string{"src/**"}, "src/a.txt", true},
		{[]string{"src/**"}, "src/nested/a.txt", true},
		{[]string{"src/**"}, "/w/src/a.txt", true},
		{[]string{"src/**"}, "./src/a.txt", true},
		{[]string{"src/**"}, "docs/a.txt", false},
		// Anchored to the workspace (regime PEX-007).
		{[]string{"src/**"}, "lib/src/a.txt", false},
		{[]string{"src/**"}, "/elsewhere/src/a.txt", false},
		{[]string{"src/**"}, "src/../docs/a.txt", false},
		{[]string{"src/**"}, "../w/src/a.txt", true},
		{[]string{"**"}, "any/where.txt", true},
		{[]string{"**"}, "/tmp/x", false},
		{[]string{"docs/*.md"}, "docs/a.md", true},
		{[]string{"docs/*.md"}, "docs/a.txt", false},
		{[]string{"docs/*.md"}, "docs/n/a.md", false},
		{[]string{"**/*.go"}, "a.go", true},
		{[]string{"**/*.go"}, "x/y/a.go", true},
		{[]string{"README.md"}, "README.md", true},
		{[]string{"README.md"}, "README.md.bak", false},
		{nil, "src/a.txt", false},
	}
	for _, tt := range tests {
		if got := writable(tt.globs, ws, tt.path); got != tt.want {
			t.Errorf("writable(%q, %q, %q) = %v, want %v", tt.globs, ws, tt.path, got, tt.want)
		}
	}
}

func TestSandboxWritePath(t *testing.T) {
	tests := map[string]string{
		"src/**":     "src/**",
		"**":         ".",
		"README.md":  "README.md",
		"docs/*.md":  "docs/**",
		"**/*.go":    ".",
		"a/b/**/*.c": "a/b/**",
		"a/[bc]/d":   "a/**",
		"./src/**":   "src/**",
	}
	for glob, want := range tests {
		if got := sandboxWritePath(glob); got != want {
			t.Errorf("sandboxWritePath(%q) = %q, want %q", glob, got, want)
		}
	}
}
