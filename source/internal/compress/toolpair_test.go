// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package compress_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/anvil-dev/anvil/internal/compress"
	"github.com/anvil-dev/anvil/internal/llm"
)

// toolExchange builds one assistant(tool_calls) + one tool message per call,
// which is the shape a provider requires: N calls, N distinct response ids.
func toolExchange(prefix string, calls int, filler string) []llm.Message {
	asst := llm.Message{Role: llm.RoleAssistant}
	for i := 0; i < calls; i++ {
		asst.ToolCalls = append(asst.ToolCalls, llm.ToolCall{
			ID:   fmt.Sprintf("%s-%d", prefix, i),
			Name: "shell",
			Args: json.RawMessage(`{"command":"ls"}`),
		})
	}
	out := []llm.Message{asst}
	for i := 0; i < calls; i++ {
		out = append(out, llm.Message{
			Role:       llm.RoleTool,
			ToolCallID: fmt.Sprintf("%s-%d", prefix, i),
			Name:       "shell",
			Content:    filler,
		})
	}
	return out
}

// dangling reports assistant(tool_calls) messages whose responses are not the
// immediately following tool messages, one per call id. This is the invariant
// OpenAI-compatible providers enforce with HTTP 400 "An assistant message with
// 'tool_calls' must be followed by tool messages responding to each
// 'tool_call_id'".
func dangling(msgs []llm.Message) []string {
	var bad []string
	for i, m := range msgs {
		if m.Role != llm.RoleAssistant || len(m.ToolCalls) == 0 {
			continue
		}
		var missing []string
		for k, tc := range m.ToolCalls {
			j := i + 1 + k
			if j >= len(msgs) || msgs[j].Role != llm.RoleTool || msgs[j].ToolCallID != tc.ID {
				missing = append(missing, tc.ID)
			}
		}
		if len(missing) > 0 {
			bad = append(bad, missing...)
		}
	}
	return bad
}

// TestCompressDoesNotSplitToolExchange pins the invariant the provider enforces.
// The boundary is a blind slice of the last KeepRecent messages, so a slot count
// that lands on an assistant(tool_calls) strands its tool messages — or keeps the
// assistant and cuts the responses, which is the same 400.
func TestCompressDoesNotSplitToolExchange(t *testing.T) {
	filler := "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
	var msgs []llm.Message
	msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: "start"})
	for i := 0; i < 6; i++ {
		msgs = append(msgs, toolExchange(fmt.Sprintf("call_%d", i), 2, filler)...)
		msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: "next " + filler})
	}
	msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: "do it"})

	if bad := dangling(msgs); len(bad) > 0 {
		t.Fatalf("fixture itself is malformed: %v", bad)
	}

	failed := 0
	for keep := 1; keep <= 20; keep++ {
		res, err := compress.Apply(context.Background(), nil, "m", msgs, compress.Options{
			MaxChars:   10, // always over budget
			KeepRecent: keep,
		})
		if err != nil {
			t.Fatalf("keep=%d: %v", keep, err)
		}
		if !res.DidCompress {
			continue
		}
		if bad := dangling(res.Messages); len(bad) > 0 {
			failed++
			t.Logf("keep=%d: stranded tool_calls %v", keep, bad)
		}
	}
	if failed > 0 {
		t.Fatalf("%d of 20 KeepRecent values produced a history the provider rejects", failed)
	}
}
