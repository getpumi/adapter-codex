// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: The Pumi Authors

package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	harnessv1 "getpumi.dev/pumi/api/gen/pumi/harness/v1"
)

// why is why the adapter stopped codex before it ended by itself.
type why int

const (
	notStopped why = iota
	// The runner closed standard input, or the adapter's context ended.
	stopRequested
	// The run reached the time budget.
	timeBudget
)

// versionTimeout is how long codex --version may take.
const versionTimeout = 30 * time.Second

// logVersion writes codex's version to the log, from codex --version with
// the run's environment.
func logVersion(ctx context.Context, path string, env []string, stderr io.Writer) {
	ctx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		logf(stderr, "codex --version failed: %v", err)
		return
	}
	logf(stderr, "codex --version: %s", strings.TrimSpace(string(out)))
}

// runDir is the adapter's directory for one run, outside the workspace:
// the hook's policy and refusals, the output's JSON Schema, the cache of
// codex's commands and, on the API key path, codex's home.
type runDir struct {
	dir                      string
	policy, refusals, schema string
	codexHome                string
	cache                    string
}

// newRunDir makes the run's directory and writes the policy and the schema.
func newRunDir(inv *harnessv1.Invocation) (*runDir, error) {
	dir, err := os.MkdirTemp("", "adapter-codex-")
	if err != nil {
		return nil, err
	}
	d := &runDir{dir: dir, policy: filepath.Join(dir, "policy.json"), refusals: filepath.Join(dir, "refusals.jsonl")}
	p := NewPolicy(inv.GetWorkspace(), inv.GetCapabilities())
	p.Refusals = d.refusals
	if err := p.write(d.policy); err != nil {
		return d, err
	}
	if s := inv.GetOutputSchema().GetJsonSchema(); s != "" {
		schema, err := codexSchema(s)
		if err != nil {
			return d, err
		}
		d.schema = filepath.Join(dir, "schema.json")
		if err := os.WriteFile(d.schema, []byte(schema), 0o600); err != nil {
			return d, err
		}
	}
	d.cache = filepath.Join(dir, "cache")
	if err := os.Mkdir(d.cache, 0o700); err != nil {
		return d, err
	}
	if inv.GetGateway() != nil {
		d.codexHome = filepath.Join(dir, "codex-home")
		if err := os.Mkdir(d.codexHome, 0o700); err != nil {
			return d, err
		}
	}
	return d, nil
}

// hookCommand is the command, for a shell, that runs the adapter at self
// as the hook with the policy file.
func hookCommand(self, policy string) string {
	return shellQuote(self) + " " + HookCommand + " " + shellQuote(policy)
}

// shellQuote quotes s for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// runCodex runs codex and returns the result. It passes the usage that
// codex reports to stdout. It stops codex when closed is closed, when ctx
// ends or when the time budget runs out. It returns an error only when the
// run's files cannot be made.
func runCodex(ctx context.Context, cfg Config, inv *harnessv1.Invocation, closed <-chan struct{}, stdout, stderr io.Writer) (*harnessv1.Result, error) {
	program := cfg.Codex
	if program == "" {
		program = "codex"
	}
	grace := cfg.StopGrace
	if grace == 0 {
		grace = DefaultStopGrace
	}
	self := cfg.Self
	if self == "" {
		var err error
		if self, err = os.Executable(); err != nil {
			return nil, fmt.Errorf("%w: cannot find the adapter's own executable for codex's hook: %w", ErrInvocation, err)
		}
	}
	d, err := newRunDir(inv)
	if d != nil {
		defer func() { _ = os.RemoveAll(d.dir) }()
	}
	if err != nil {
		return nil, fmt.Errorf("%w: cannot write the run's files: %w", ErrInvocation, err)
	}
	path, err := exec.LookPath(program)
	if err != nil {
		logf(stderr, "cannot find codex: %v", err)
		return failed(0), nil
	}
	// codex's standard error and the stream's notes both go to the log,
	// from two goroutines of the command.
	stderr = &syncWriter{w: stderr}
	env := Env(os.Environ(), inv.GetGateway(), d.codexHome, d.cache)
	install, err := InstallDirectory(path)
	if err != nil {
		logf(stderr, "cannot find codex's install: %v", err)
		return failed(0), nil
	}
	args, err := Args(inv, Files{Hook: hookCommand(self, d.policy), Schema: d.schema, Codex: install, Cache: d.cache}, os.Environ())
	if err != nil {
		return nil, err
	}
	logVersion(ctx, path, env, stderr)

	// The arguments come from the invocation, which is what the adapter
	// runs; no shell reads them, and the task goes to standard input.
	// Stopping cancels runCtx, and its Cancel interrupts codex's process
	// group instead of killing codex.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(runCtx, path, args...)
	cmd.Cancel = func() error {
		interrupt(cmd)
		return nil
	}
	cmd.Dir = inv.GetWorkspace()
	cmd.Env = env
	cmd.Stdin = strings.NewReader(inv.GetInput().GetTask())
	s := newStream(stdout, stderr, modelName(inv))
	cmd.Stdout = s
	cmd.Stderr = stderr
	// A process that codex started can hold its standard error open after
	// codex exits; Wait then stops waiting for it.
	cmd.WaitDelay = grace
	newProcessGroup(cmd)

	start := time.Now()
	if err := cmd.Start(); err != nil {
		logf(stderr, "cannot start codex: %v", err)
		return failed(0), nil
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var budget <-chan time.Time
	if sec := inv.GetBudget().GetSeconds(); sec > 0 {
		timer := time.NewTimer(time.Duration(sec) * time.Second)
		defer timer.Stop()
		budget = timer.C
	}
	stopped := notStopped
	select {
	case err = <-done:
	case <-closed:
		stopped = stopRequested
	case <-ctx.Done():
		stopped = stopRequested
	case <-budget:
		stopped = timeBudget
	}
	if stopped != notStopped {
		cancel()
		err = wait(cmd, done, grace)
	}
	elapsed := time.Since(start)
	s.finish()
	res := result(s, err, stopped, inv, elapsed, stderr)
	res.Refusals = readRefusals(d.refusals, stderr)
	if s.writeErr != nil {
		logf(stderr, "cannot write a usage figure: %v", s.writeErr)
	}
	return res, nil
}

// modelName is the model that the usage figures name: the model role
// through the gateway, which maps it, or the role's model on the sign-in
// path.
func modelName(inv *harnessv1.Invocation) string {
	if inv.GetGateway() != nil {
		return inv.GetModelRole()
	}
	return inv.GetModel()
}

// wait waits for codex to exit after it was interrupted, and kills its
// process group when it has not exited after the grace period. It returns
// the error of Wait.
func wait(cmd *exec.Cmd, done <-chan error, grace time.Duration) error {
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		kill(cmd)
		return <-done
	}
}

// limitMarks are the words with which codex reports that a provider's
// usage or rate limit refused a call (decision 0249). Codex CLI 0.172.0
// wrote "Quota exceeded. Check your plan and billing details." in
// turn.failed for a 429 with insufficient_quota, with no error code.
var limitMarks = []string{"quota exceeded", "exceeded your current quota", "rate limit", "usage limit", "too many requests", "429"}

// limited reports whether a provider's usage or rate limit ended the run:
// the turn did not complete, and what codex reported names a limit.
func limited(s *stream) bool {
	if s.completed {
		return false
	}
	for _, m := range slices.Concat([]string{s.failure}, s.errors) {
		m = strings.ToLower(m)
		for _, mark := range limitMarks {
			if strings.Contains(m, mark) {
				return true
			}
		}
	}
	return false
}

// result builds the result from what codex's events said.
func result(s *stream, waitErr error, stopped why, inv *harnessv1.Invocation, elapsed time.Duration, stderr io.Writer) *harnessv1.Result {
	switch {
	case stopped == stopRequested:
		logf(stderr, "stopped codex on request")
	case stopped == timeBudget:
		logf(stderr, "stopped codex at the time budget of %d seconds", inv.GetBudget().GetSeconds())
	case waitErr != nil:
		logf(stderr, "codex exited: %v", waitErr)
	}
	seconds := int64(math.Ceil(elapsed.Seconds()))
	res := &harnessv1.Result{Status: harnessv1.Result_STATUS_FAILED, Usage: &harnessv1.Usage{Tokens: s.tokens, Seconds: seconds}}
	switch {
	case stopped == stopRequested:
		res.Status = harnessv1.Result_STATUS_STOPPED
	case limited(s):
		logf(stderr, "the provider's usage or rate limit refused a call of codex: %s", clip([]byte(s.reason())))
		res.Status = harnessv1.Result_STATUS_LIMITED
	case stopped == timeBudget:
	case !s.completed:
		logf(stderr, "codex did not complete its turn: %s", clip([]byte(s.reason())))
	case waitErr != nil:
	case inv.GetOutputSchema().GetJsonSchema() == "":
		res.Status = harnessv1.Result_STATUS_COMPLETED
	default:
		var output bytes.Buffer
		if json.Compact(&output, []byte(s.message)) != nil || !bytes.HasPrefix(output.Bytes(), []byte("{")) {
			logf(stderr, "codex's last message is not a JSON object: %s", clip([]byte(s.message)))
			break
		}
		res.Status, res.Output = harnessv1.Result_STATUS_COMPLETED, output.String()
	}
	return res
}

// reason is what codex reported about why its turn did not complete.
func (s *stream) reason() string {
	if s.failure != "" {
		return s.failure
	}
	if len(s.errors) > 0 {
		return s.errors[len(s.errors)-1]
	}
	return "codex reported nothing"
}

// failed is a failed result with the wall time only.
func failed(seconds int64) *harnessv1.Result {
	return &harnessv1.Result{Status: harnessv1.Result_STATUS_FAILED, Usage: &harnessv1.Usage{Seconds: seconds}}
}

// syncWriter makes the writes to w one at a time.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// clip shortens codex's output for the log.
func clip(out []byte) string {
	const limit = 500
	if len(out) > limit {
		return string(out[:limit]) + "..."
	}
	return string(out)
}

func logf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, "adapter-codex: "+format+"\n", args...)
}
