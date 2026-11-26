// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: The Pumi Authors

package adapter

import (
	"bytes"
	"encoding/json"
	"io"
	"sync"

	harnessv1 "getpumi.dev/pumi/api/gen/pumi/harness/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// stream reads the events that codex exec --json writes while it runs, one
// JSON object a line. It passes the usage of each turn, from turn.completed,
// to the runner as a line of the adapter's standard output as soon as codex
// reports it (decisions 0241 and 0252). Codex reports usage once a turn,
// and one codex exec is one turn, so the runner learns the usage when the
// run ends (decision 0241). The stream also keeps the last message of the
// agent, which is the structured output when the invocation has an output
// schema, and what codex reported about errors.
type stream struct {
	mu  sync.Mutex
	out io.Writer
	log io.Writer
	// model is the model that the usage figures name: the model that the
	// adapter asked codex for.
	model string
	// partial is the part of a line not yet ended by a newline.
	partial []byte
	// message is the text of the agent's last message.
	message string
	// completed says that codex reported turn.completed, and failure is
	// the message of turn.failed.
	completed bool
	failure   string
	// errors are the messages of codex's error events and items.
	errors []string
	// tokens is every token of the figures passed.
	tokens int64
	// writeErr is the first error of a write to out.
	writeErr error
}

func newStream(out, log io.Writer, model string) *stream {
	return &stream{out: out, log: log, model: model}
}

// Write takes codex's standard output and handles each line it ends.
func (s *stream) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.partial = append(s.partial, p...)
	for {
		i := bytes.IndexByte(s.partial, '\n')
		if i < 0 {
			return len(p), nil
		}
		s.handle(s.partial[:i])
		s.partial = s.partial[i+1:]
	}
}

// codexEvent is the part of one event of codex exec --json that the stream
// reads.
type codexEvent struct {
	Type    string      `json:"type"`
	Message string      `json:"message"`
	Usage   *codexUsage `json:"usage"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error"`
	Item *struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Message string `json:"message"`
	} `json:"item"`
}

// codexUsage is the usage of turn.completed. The input tokens hold the
// cached input and the cache writes, and the output tokens hold the
// reasoning, as in the Responses API.
type codexUsage struct {
	InputTokens           int64 `json:"input_tokens"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	CacheWriteInputTokens int64 `json:"cache_write_input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
}

// handle handles one line of codex's events.
func (s *stream) handle(line []byte) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return
	}
	var e codexEvent
	if err := json.Unmarshal(line, &e); err != nil {
		logf(s.log, "codex wrote a line that is not JSON: %q", clip(line))
		return
	}
	switch e.Type {
	case "turn.completed":
		s.completed = true
		if e.Usage != nil {
			s.pass(e.Usage)
		}
	case "turn.failed":
		if e.Error != nil {
			s.failure = e.Error.Message
		}
	case "error":
		s.errors = append(s.errors, e.Message)
	case "item.completed":
		switch {
		case e.Item == nil:
		case e.Item.Type == "agent_message":
			s.message = e.Item.Text
		case e.Item.Type == "error":
			s.errors = append(s.errors, e.Item.Message)
		}
	}
}

// pass writes the usage figure of a turn as a line. A cache write that the
// usage does not split by duration is a 5-minute write, as UsageFigure
// says.
func (s *stream) pass(u *codexUsage) {
	f := &harnessv1.UsageFigure{
		Model:                      s.model,
		InputTokens:                max(0, u.InputTokens-u.CachedInputTokens-u.CacheWriteInputTokens),
		FiveMinuteCacheWriteTokens: u.CacheWriteInputTokens,
		CacheReadTokens:            u.CachedInputTokens,
		OutputTokens:               u.OutputTokens,
	}
	s.tokens += f.GetInputTokens() + f.GetFiveMinuteCacheWriteTokens() + f.GetCacheReadTokens() + f.GetOutputTokens()
	if s.writeErr != nil {
		return
	}
	data, err := protojson.Marshal(&harnessv1.Line{Kind: &harnessv1.Line_Usage{Usage: f}})
	if err == nil {
		_, err = s.out.Write(append(data, '\n'))
	}
	s.writeErr = err
}

// finish handles a last line that no newline ended.
func (s *stream) finish() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.partial) > 0 {
		s.handle(s.partial)
		s.partial = nil
	}
}
