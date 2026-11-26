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
