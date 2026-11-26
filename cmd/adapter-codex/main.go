// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: The Pumi Authors

// Command adapter-codex is the Pumi harness adapter for the Codex CLI
// (decisions 0158 to 0160 and 0242). The runner starts it in the
// invocation's workspace and writes one pumi.harness.v1.Invocation as a line
// of protojson to its standard input. It runs codex exec there and writes
// lines of pumi.harness.v1.Line to its standard output: the usage that codex
// reports, then the result. Its standard error is the log. Closing standard
// input asks it to stop codex; an interrupt or SIGTERM does the same.
//
// Run as "adapter-codex hook <policy>", it is the PreToolUse hook that the
// adapter gives codex: it reads a tool call from standard input and refuses
// it when the capability list in the policy file leaves it out. It also
// refuses the call when it cannot decide, because codex lets a call go on
// when its hook fails, and so it always exits with status 0.
//
// It exits with status 0 when it wrote a result, and with status 1 when it
// cannot read or run the invocation.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"getpumi.dev/adapter-codex/internal/adapter"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	status := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	cancel()
	os.Exit(status)
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 2 && args[0] == adapter.HookCommand {
		adapter.Hook(args[1], stdin, stdout, stderr)
		return 0
	}
	if err := adapter.Run(ctx, adapter.Config{}, stdin, stdout, stderr); err != nil {
		_, _ = fmt.Fprintf(stderr, "adapter-codex: %v\n", err)
		return 1
	}
	return 0
}
