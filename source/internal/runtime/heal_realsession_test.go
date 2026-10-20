// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package runtime_test

import (
	"encoding/json"
	"testing"

	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/runtime"
)

// TestHealSourceOfTheReportedSession reproduces the exact store order of the
// session that failed with "An assistant message with 'tool_calls' must be
// followed by tool messages responding to each 'tool_call_id'".
//
// The store showed a long-lived run (79a5…) and a short run (58a0…) writing to
// one file: a user turn from the short run, its run_end, then the long run's
// tool result, and finally the long run's assistant turn whose response never
// arrived. This is the real record order, not a simplified version of it.
func TestHealSourceOfTheReportedSession(t *testing.T) {
	mustArgs := func(s string) json.RawMessage { return json.RawMessage(s) }
	history := []llm.Message{
		{Role: llm.RoleUser, Content: "任务怎么停了？"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
			ID: "call_00_Q3mDojLIUa3dUhAiUjRi8208", Name: "shell",
			Args: mustArgs(`{"command":"cd /d d:\\uc && python _tmp_batch.py 2>&1"}`),
		}}},
		// run-58a0's tool result landed after the next run's user turn began
		{Role: llm.RoleTool, ToolCallID: "call_00_Q3mDojLIUa3dUhAiUjRi8208",
			Name: "shell", Content: "OK  want=TRACK  got=TRACK  .gitignore"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
			ID: "call_00_2A1a0116", Name: "shell", Args: mustArgs(`{}`),
		}}},
		{Role: llm.RoleTool, ToolCallID: "call_00_2A1a0116", Name: "shell", Content: "wrote _tmp_batch.py"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
			ID: "call_00_Reai1755", Name: "shell", Args: mustArgs(`{}`),
		}}},
		{Role: llm.RoleTool, ToolCallID: "call_00_Reai1755", Name: "shell", Content: "newly staged: 1645"},
		// the run's last assistant turn: its tool call never got an answer
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
			ID: "call_00_Q3mDojLIUa3dUhAiUjRi9999", Name: "shell", Args: mustArgs(`{}`),
		}}},
		// the next run's prompt, written before that answer arrived
		{Role: llm.RoleUser, Content: "git，只需要引擎Runtime部分的管理，其余的都从git中排除"},
	}

	healed := runtime.HealToolPairs(history)
	assertWireValid(t, healed)

	// Every original non-tool message survives, in order: healing must not eat
	// the user's own turns.
	var users []string
	for _, m := range healed {
		if m.Role == llm.RoleUser {
			users = append(users, m.Content)
		}
	}
	if len(users) != 2 {
		t.Fatalf("user turns lost: %v", users)
	}

	// The synthesised result must say what happened rather than claim success.
	var synthetic int
	for _, m := range healed {
		if m.Role == llm.RoleTool && m.Content == runtime.InterruptedToolContent {
			synthetic++
		}
	}
	if synthetic != 1 {
		t.Fatalf("synthetic results = %d, want 1 (the lost call)", synthetic)
	}
}

// assertWireValid enforces the invariant the provider checks.
func assertWireValid(t *testing.T, msgs []llm.Message) {
	t.Helper()
	for i, m := range msgs {
		if m.Role != llm.RoleAssistant || len(m.ToolCalls) == 0 {
			continue
		}
		for k, tc := range m.ToolCalls {
			j := i + 1 + k
			if j >= len(msgs) || msgs[j].Role != llm.RoleTool || msgs[j].ToolCallID != tc.ID {
				t.Fatalf("dangling call %s at %d in %v", tc.ID, i, shapeOf(msgs))
			}
		}
	}
	for _, m := range msgs {
		if m.Role == llm.RoleTool && m.ToolCallID == "" {
			t.Fatalf("tool message without an id in %v", shapeOf(msgs))
		}
	}
}
