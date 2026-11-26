// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: The Pumi Authors

package adapter

import "testing"

func TestCommandAllowed(t *testing.T) {
	tests := []struct {
		entries []string
		command string
		want    bool
	}{
		{[]string{"touch"}, "touch out/listed", true},
		{[]string{"touch"}, "touch", true},
		{[]string{"go test"}, "go test ./...", true},
		{[]string{"go test"}, "go testing", false},
		{[]string{"go test"}, "go vet", false},
		{[]string{"touch"}, "mkdir out/unlisted", false},
		// A command that is more than one simple command is refused,
		// since the shell would run the parts that no entry names.
		{[]string{"touch"}, "touch out/first && mkdir out/chained", false},
		{[]string{"touch"}, "touch a; rm -rf b", false},
		{[]string{"touch"}, "touch a | sh", false},
		{[]string{"touch"}, "touch $(rm b)", false},
		{[]string{"touch"}, "touch `rm b`", false},
		{[]string{"touch"}, "touch a > b", false},
		{[]string{"touch"}, "touch a\nrm b", false},
		{[]string{"touch"}, "touch \"$HOME\"", false},
		{[]string{"echo"}, "echo 'a; b'", true},
		{[]string{"echo"}, `echo "a b" c\ d`, true},
		{[]string{"echo"}, "echo 'unterminated", false},
		// Assignments (decision 0248).
		{[]string{"PUMI_CONFORMANCE=1 touch"}, "PUMI_CONFORMANCE=1 touch out/assigned", true},
		{[]string{"PUMI_CONFORMANCE=1 touch"}, "touch out/bare", false},
		{[]string{"PUMI_CONFORMANCE=1 touch"}, "PUMI_OTHER=1 touch out/other", false},
		{[]string{"PUMI_CONFORMANCE=1 touch"}, "PUMI_CONFORMANCE=1 PUMI_OTHER=1 touch out/extra", false},
		{[]string{"PUMI_CONFORMANCE=1 touch"}, "PUMI_CONFORMANCE=2 touch out/x", false},
		{[]string{"touch"}, "PUMI_CONFORMANCE=1 touch out/assigned", false},
		{[]string{"A=1 B=2 go test"}, "B=2 A=1 go test", true},
		{[]string{"touch"}, "  touch out/listed  ", true},
		{[]string{"touch", "mkdir"}, "mkdir out/x", true},
		{nil, "touch", false},
		{[]string{"touch"}, "", false},
	}
	for _, tt := range tests {
		if got := commandAllowed(tt.entries, tt.command); got != tt.want {
			t.Errorf("commandAllowed(%q, %q) = %v, want %v", tt.entries, tt.command, got, tt.want)
		}
	}
}

func TestPatchCommand(t *testing.T) {
	patch := "*** Begin Patch\n*** Add File: src/a.txt\n+hello\n*** End Patch\n"
	tests := []struct {
		command string
		want    string
		ok      bool
	}{
		{"apply_patch <<'PATCH'\n" + patch + "PATCH", patch, true},
		{"apply_patch <<\"EOF\"\n" + patch + "EOF", patch, true},
		{"apply_patch <<EOF\n" + patch + "EOF\n", patch, true},
		{"apply_patch '" + patch + "'", patch, true},
		{"apply_patch <<'PATCH'\n" + patch + "PATCH\nrm -rf x", "", false},
		{"cd src && apply_patch <<'PATCH'\n" + patch + "PATCH", "", false},
		{"touch a", "", false},
	}
	for _, tt := range tests {
		got, ok := patchCommand(tt.command)
		if ok != tt.ok || got != tt.want {
			t.Errorf("patchCommand(%q) = %q, %v, want %q, %v", tt.command, got, ok, tt.want, tt.ok)
		}
	}
}

// TestReadOnlyCommand: with the read kind, codex, which has no tool that
// reads a file, reads through commands that only read: a fixed list of
// programs, alone or in a pipeline of them, without an option that writes
// or runs another program, a redirect or a substitution (getpumi/pumi#125).
func TestReadOnlyCommand(t *testing.T) {
	for command, want := range map[string]bool{
		"cat .pumi/plan.md":        true,
		"sed -n '1,200p' a.go":     true,
		"rg -n '^' .pumi/plan.md":  true,
		"rg --files | head -50":    true,
		"grep -rn foo src | wc -l": true,
		"ls -la":                   true,
		"find . -name '*.go'":      true,
		"git status --short":       true,
		"git diff HEAD -- a.go":    true,
		"git log --oneline -5":     true,
		"head -20 a.go":            true,
		"tail -n 5 a.go":           true,
		"nl -ba a.go":              true,
		"pwd":                      true,
		"cat a > b":                false,
		"cat a; rm b":              false,
		"cat a && rm b":            false,
		"cat a || rm b":            false,
		"cat $(echo a)":            false,
		"cat a | sh":               false,
		"rm a":                     false,
		"sed -i 's/a/b/' a.go":     false,
		"sed -n 'w out' a.go":      false,
		"sed 's/a/b/' a.go":        false,
		"find . -name a -delete":   false,
		"find . -exec rm {} ;":     false,
		"find . -fprint out":       false,
		"rg --pre sh foo":          false,
		"rg --pre=sh foo":          false,
		"git checkout a.go":        false,
		"git diff --output=out":    false,
		"git -c core.pager=sh log": false,
		"FOO=1 cat a":              false,
		"cat a |":                  false,
		"":                         false,
	} {
		if got := readOnlyCommand(command); got != want {
			t.Errorf("readOnlyCommand(%q) = %v, want %v", command, got, want)
		}
	}
}
