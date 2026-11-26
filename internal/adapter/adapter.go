// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: The Pumi Authors

// Package adapter is the Pumi harness adapter for the Codex CLI (decisions
// 0158 to 0160 and 0242). The runner starts it as one process per
// invocation, in the invocation's workspace. It reads the invocation as one
// line of protojson from standard input, runs codex exec in the workspace,
// and writes lines of pumi.harness.v1.Line to standard output: the usage
// that codex reports, then the result. Its standard error is the log. When
// standard input closes, it stops codex and writes what the run had.
//
// The same executable is codex's PreToolUse hook (Hook), which refuses the
// tool calls that the capability list leaves out.
package adapter

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	harnessv1 "getpumi.dev/pumi/api/gen/pumi/harness/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// ErrInvocation is returned when the adapter cannot read or cannot run the
// invocation. It then writes no result, and the process exits with a
// non-zero status, which fails the invocation (decision 0158).
var ErrInvocation = errors.New("the invocation cannot be run")

// DefaultStopGrace is how long the adapter waits for codex to stop after it
// interrupts it, before it kills it. It is shorter than the runner's grace
// period of 30 seconds, so that the adapter can still write a result.
const DefaultStopGrace = 20 * time.Second

// Config configures Run.
type Config struct {
	// Codex is the codex program, looked up on the PATH when it has no
	// slash. When it is empty, it is "codex".
	Codex string
	// Self is the adapter's executable, which codex runs as its hook. When
	// it is empty, it is os.Executable.
	Self string
	// StopGrace replaces DefaultStopGrace when it is not zero.
	StopGrace time.Duration
}

// Run runs one invocation. It returns an error, and writes nothing to
// stdout, only when the invocation cannot be read or cannot be run; the
// error then wraps ErrInvocation. Otherwise it writes the usage figures
// that codex reports and then one Result, each as a pumi.harness.v1.Line,
// also when codex fails, and returns nil. stderr is the log.
func Run(ctx context.Context, cfg Config, stdin io.Reader, stdout, stderr io.Writer) error {
	inv, closed, err := readInvocation(stdin)
	if err != nil {
		return err
	}
	if _, err := Args(inv, Files{}, nil); err != nil {
		return err
	}
	result, err := runCodex(ctx, cfg, inv, closed, stdout, stderr)
	if err != nil {
		return err
	}
	data, err := protojson.Marshal(&harnessv1.Line{Kind: &harnessv1.Line_Result{Result: result}})
	if err != nil {
		return err
	}
	_, err = stdout.Write(append(data, '\n'))
	return err
}

// readInvocation reads the invocation line and returns a channel that is
// closed when standard input closes after it, which is the runner's request
// to stop (decision 0158). The line is parsed strictly, so an unknown
// field, such as a capability that this adapter does not know, refuses the
// invocation instead of being ignored.
func readInvocation(stdin io.Reader) (*harnessv1.Invocation, <-chan struct{}, error) {
	r := bufio.NewReader(stdin)
	line, err := r.ReadBytes('\n')
	closed := make(chan struct{})
	switch {
	case errors.Is(err, io.EOF) && len(line) > 0:
		close(closed)
	case err != nil:
		return nil, nil, fmt.Errorf("%w: reading the invocation line: %w", ErrInvocation, err)
	default:
		go func() {
			_, _ = io.Copy(io.Discard, r)
			close(closed)
		}()
	}
	inv := &harnessv1.Invocation{}
	if err := protojson.Unmarshal(line, inv); err != nil {
		return nil, nil, fmt.Errorf("%w: the invocation line is not a pumi.harness.v1.Invocation: %w", ErrInvocation, err)
	}
	return inv, closed, nil
}
