// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package runtime_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/runtime"
	"github.com/anvil-dev/anvil/internal/tools"
)

func TestHealToolPairs(t *testing.T) {
	call := func(id string) llm.ToolCall {
		return llm.ToolCall{ID: id, Name: "shell", Args: json.RawMessage(`{}`)}
	}
	cases := []struct {
		name string
		in   []llm.Message
		want []string // role:toolcallid compacted, for readability
	}{
		{
			name: "healthy history is untouched",
			in: []llm.Message{
				{Role: llm.RoleUser, Content: "hi"},
				{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call("a")}},
				{Role: llm.RoleTool, ToolCallID: "a", Content: "ok"},
			},
			want: []string{"user", "assistant:a", "tool:a"},
		},
		{
			name: "missing response gets a synthetic one",
			in: []llm.Message{
				{Role: llm.RoleUser, Content: "hi"},
				{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call("a")}},
			},
			want: []string{"user", "assistant:a", "tool:a"},
		},
		{
			name: "partially answered turn is completed",
			in: []llm.Message{
				{Role: llm.RoleUser, Content: "hi"},
				{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call("a"), call("b")}},
				{Role: llm.RoleTool, ToolCallID: "a", Content: "ok"},
			},
			want: []string{"user", "assistant:a,b", "tool:a", "tool:b"},
		},
		{
			name: "orphan tool message is dropped",
			in: []llm.Message{
				{Role: llm.RoleUser, Content: "hi"},
				{Role: llm.RoleTool, ToolCallID: "ghost", Content: "who asked?"},
			},
			want: []string{"user"},
		},
		{
			name: "interleaved runs: hole filled, next user turn kept in place",
			in: []llm.Message{
				{Role: llm.RoleUser, Content: "start"},
				{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call("a")}},
				// A second run's user turn landed before run 1's tool result.
				{Role: llm.RoleUser, Content: "why did it stop?"},
				{Role: llm.RoleTool, ToolCallID: "a", Content: "ok"},
			},
			want: []string{"user", "assistant:a", "tool:a", "user", "tool:a"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runtime.HealToolPairs(tc.in)
			if !hasShape(got, tc.want) {
				t.Fatalf("got %v\nwant %v\n%+v", shapeOf(got), tc.want, got)
			}
		})
	}
}

func TestHealToolPairsIsIdempotent(t *testing.T) {
	in := []llm.Message{
		{Role: llm.RoleUser, Content: "hi"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
			{ID: "a", Name: "shell", Args: json.RawMessage(`{}`)},
		}},
	}
	once := runtime.HealToolPairs(in)
	twice := runtime.HealToolPairs(once)
	if !hasShape(twice, shapeOf(once)) {
		t.Fatalf("not idempotent:\nonce=%v\ntwice=%v", shapeOf(once), shapeOf(twice))
	}
}

func shapeOf(msgs []llm.Message) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		ids := ""
		for i, tc := range m.ToolCalls {
			if i > 0 {
				ids += ","
			}
			ids += tc.ID
		}
		switch {
		case len(m.ToolCalls) > 0:
			out = append(out, "assistant:"+ids)
		case m.Role == llm.RoleTool:
			out = append(out, "tool:"+m.ToolCallID)
		default:
			out = append(out, string(m.Role))
		}
	}
	return out
}

func hasShape(msgs []llm.Message, want []string) bool {
	got := shapeOf(msgs)
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// replayCompleter records every request the loop makes, so a test can assert
// what actually went over the wire.
type replayCompleter struct {
	requests []llm.Request
	reply    func(n int) *llm.Response
}

func (c *replayCompleter) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	_ = ctx
	c.requests = append(c.requests, req)
	return c.reply(len(c.requests)), nil
}

// TestLoopHealsDanglingHistoryBeforeSending is the regression guard for the
// reported failure: a history whose assistant(tool_calls) lost its response made
// every later turn fail with the provider's 400. The loop must repair it before
// the first request, not pass it through.
func TestLoopHealsDanglingHistoryBeforeSending(t *testing.T) {
	c := &replayCompleter{reply: func(n int) *llm.Response {
		return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}}
	}}
	reg := tools.NewRegistry(t.TempDir(), false)

	res := runtime.Loop(context.Background(), c, reg, runtime.Config{
		CWD:   t.TempDir(),
		Model: "m",
		History: []llm.Message{
			{Role: llm.RoleUser, Content: "start"},
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
				ID: "call_lost", Name: "shell", Args: json.RawMessage(`{"command":"ls"}`),
			}}},
			// the response never landed
			{Role: llm.RoleTool, ToolCallID: "ghost", Content: "orphan"},
			{Role: llm.RoleAssistant, Content: "earlier answer"},
		},
		UserMsg: "continue",
	}, nil)
	if res.Status != "finished" {
		t.Fatalf("status=%s err=%s", res.Status, res.Err)
	}
	if len(c.requests) == 0 {
		t.Fatal("no request was made")
	}
	sent := c.requests[0].Messages
	for i, m := range sent {
		if m.Role != llm.RoleAssistant || len(m.ToolCalls) == 0 {
			continue
		}
		for k, tc := range m.ToolCalls {
			j := i + 1 + k
			if j >= len(sent) || sent[j].Role != llm.RoleTool || sent[j].ToolCallID != tc.ID {
				t.Fatalf("dangling %s was sent to the provider:\n%v", tc.ID, shapeOf(sent))
			}
		}
	}
	for _, m := range sent {
		if m.Role == llm.RoleTool && m.ToolCallID == "ghost" {
			t.Fatalf("orphan tool message was sent to the provider:\n%v", shapeOf(sent))
		}
	}
}
