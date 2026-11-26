// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: The Pumi Authors

package adapter

import (
	"bytes"
	"strings"
	"testing"

	harnessv1 "getpumi.dev/pumi/api/gen/pumi/harness/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestStream(t *testing.T) {
	var out, log bytes.Buffer
	s := newStream(&out, &log, "frontier")
	events := strings.Join([]string{
		`{"type":"thread.started","thread_id":"t1"}`,
		`{"type":"turn.started"}`,
		`not json`,
		`{"type":"item.completed","item":{"id":"i0","type":"agent_message","text":"first"}}`,
		`{"type":"item.completed","item":{"id":"i1","type":"error","message":"a tool failed"}}`,
		`{"type":"error","message":"reconnecting"}`,
		`{"type":"item.completed","item":{"id":"i2","type":"agent_message","text":"{\"answer\":42}"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":1000,"cached_input_tokens":600,"cache_write_input_tokens":100,"output_tokens":50,"reasoning_output_tokens":20}}`,
	}, "\n")
	// Write in pieces that split lines, and leave the last line without a
	// newline, as a codex that is killed can.
	for i := 0; i < len(events); i += 7 {
		if _, err := s.Write([]byte(events[i:min(i+7, len(events))])); err != nil {
			t.Fatal(err)
		}
	}
	if out.Len() != 0 {
		t.Fatalf("the stream passed %q before the turn's line ended", out.String())
	}
	s.finish()
	line := &harnessv1.Line{}
	if err := protojson.Unmarshal(bytes.TrimSuffix(out.Bytes(), []byte("\n")), line); err != nil {
		t.Fatalf("output %q is not one line: %v", out.String(), err)
	}
	want := &harnessv1.UsageFigure{Model: "frontier", InputTokens: 300, FiveMinuteCacheWriteTokens: 100, CacheReadTokens: 600, OutputTokens: 50}
	if !proto.Equal(line.GetUsage(), want) {
		t.Errorf("usage = %v, want %v", line.GetUsage(), want)
	}
	if !s.completed || s.failure != "" {
		t.Errorf("completed = %t, failure = %q, want a completed turn", s.completed, s.failure)
	}
	if s.message != `{"answer":42}` {
		t.Errorf("message = %q, want the last agent message", s.message)
	}
	if got := strings.Join(s.errors, "|"); got != "a tool failed|reconnecting" {
		t.Errorf("errors = %q", got)
	}
	if s.tokens != 1050 {
		t.Errorf("tokens = %d, want 1050", s.tokens)
	}
	if !strings.Contains(log.String(), "not JSON") {
		t.Errorf("log %q does not say that a line is not JSON", log.String())
	}
}

func TestStreamFailedTurn(t *testing.T) {
	var out, log bytes.Buffer
	s := newStream(&out, &log, "gpt-6.2-sol")
	if _, err := s.Write([]byte(`{"type":"turn.failed","error":{"message":"Quota exceeded. Check your plan and billing details."}}` + "\n")); err != nil {
		t.Fatal(err)
	}
	s.finish()
	if s.completed || !strings.HasPrefix(s.failure, "Quota exceeded") {
		t.Errorf("completed = %t, failure = %q", s.completed, s.failure)
	}
	if out.Len() != 0 {
		t.Errorf("a failed turn passed usage %q", out.String())
	}
}
