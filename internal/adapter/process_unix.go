// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: The Pumi Authors

//go:build unix

package adapter

import (
	"os/exec"
	"syscall"
)

// newProcessGroup starts codex in a process group of its own, so that
// interrupt and kill reach the processes it starts, such as the commands
// that it runs.
func newProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// interrupt sends SIGINT to codex's process group, which ends codex's
// run.
func interrupt(cmd *exec.Cmd) {
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGINT)
}

// kill kills codex's process group.
func kill(cmd *exec.Cmd) {
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
