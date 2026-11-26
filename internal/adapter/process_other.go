// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: The Pumi Authors

//go:build !unix

package adapter

import "os/exec"

// newProcessGroup does nothing on systems other than Unix.
func newProcessGroup(*exec.Cmd) {}

// interrupt kills codex on systems other than Unix, where the os package
// cannot send an interrupt (decision 0158).
func interrupt(cmd *exec.Cmd) {
	_ = cmd.Process.Kill()
}

// kill kills codex.
func kill(cmd *exec.Cmd) {
	_ = cmd.Process.Kill()
}
