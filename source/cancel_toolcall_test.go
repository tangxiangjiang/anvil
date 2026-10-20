// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/store"
)

// cancelMidToolCompleter emits an assistant turn with tool_calls, then blocks
// forever on the *next* completion — the shape of a run cancelled while the
// model is thinking after tools already ran.
type cancelMidToolCompleter struct{ n int }

func (c *cancelMidToolCompleter) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	c.n++
	if c.n == 1 {
		return &llm.Response{Message: llm.Message{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{{
				ID: "call_1", Name: "read_file", Args: json.RawMessage(`{"path":"a.txt"}`),
			}},
		}}, nil
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestCancelledRunLeavesNoDanglingToolCall is the regression guard for the
// provider 400 "An assistant message with 'tool_calls' must be followed by tool
// messages responding to each 'tool_call_id'". A cancelled or failed run must
// not leave a persisted assistant(tool_calls) whose responses never landed,
// because every later turn in that session resends it and is rejected.
func TestCancelledRunLeavesNoDanglingToolCall(t *testing.T) {
	dir := t.TempDir()
	agent, err := Create(context.Background(), Options{
		APIKey: "k",
		Model:  Model{ID: "m"},
		Local:  LocalOptions{CWD: dir, AllowShell: boolPtr(false)},
	})
	if err != nil {
		t.Fatal(err)
	}
	agent.complete = &cancelMidToolCompleter{}
	id := agent.AgentID()

	ctx, cancel := context.WithCancel(context.Background())
	run, err := agent.Send(ctx, "read a.txt")
	if err != nil {
		t.Fatal(err)
	}
	// Let the tool round finish, then cancel while the next completion blocks.
	for ev := range run.Stream(context.Background()) {
		if ev.Type == EventToolResult {
			cancel()
		}
	}
	cancel()
	_ = agent.Close()

	st, err := store.Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	recs, err := st.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	var msgs []llm.Message
	for _, r := range recs {
		if r.Type != store.TypeMessage {
			continue
		}
		m := llm.Message{Role: llm.Role(r.Role), Content: r.Content, ToolCallID: r.ToolCallID}
		for _, tc := range r.ToolCalls {
			m.ToolCalls = append(m.ToolCalls, llm.ToolCall{ID: tc.ID, Name: tc.Name, Args: tc.Args})
		}
		msgs = append(msgs, m)
	}
	for i, m := range msgs {
		if m.Role != llm.RoleAssistant || len(m.ToolCalls) == 0 {
			continue
		}
		for k, tc := range m.ToolCalls {
			j := i + 1 + k
			if j >= len(msgs) || msgs[j].Role != llm.RoleTool || msgs[j].ToolCallID != tc.ID {
				t.Fatalf("assistant(tool_calls=%s) has no persisted tool response; "+
					"the next turn in this session will be rejected by the provider:\n%+v",
					tc.ID, msgs)
			}
		}
	}
}

func boolPtr(b bool) *bool { return &b }
