// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package runtime

import (
	"encoding/json"
	"testing"

	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/tools"
)

func TestLoopGuardRepeatThenStop(t *testing.T) {
	g := newLoopGuard()
	tc := llm.ToolCall{Name: "read_file", Args: json.RawMessage(`{"path":"a"}`)}
	res := tools.Result{Content: "same"}
	for i := 0; i < 2; i++ {
		action, _ := g.Observe([]llm.ToolCall{tc}, []tools.Result{res})
		if action != loopOK {
			t.Fatalf("early action=%v at %d", action, i)
		}
	}
	action, reason := g.Observe([]llm.ToolCall{tc}, []tools.Result{res})
	if action != loopWarn || reason == "" {
		t.Fatalf("want warn got %v %q", action, reason)
	}
	action, _ = g.Observe([]llm.ToolCall{tc}, []tools.Result{res})
	if action != loopStop {
		t.Fatalf("want stop got %v", action)
	}
}

func TestLoopGuardCycle(t *testing.T) {
	g := newLoopGuard()
	a := llm.ToolCall{Name: "read_file", Args: json.RawMessage(`{"path":"a"}`)}
	b := llm.ToolCall{Name: "grep", Args: json.RawMessage(`{"pattern":"x"}`)}
	// Vary args/results so only cycle detector fires (not repeat/stall).
	contents := []string{"1", "2", "3", "4", "5", "6"}
	calls := []llm.ToolCall{a, b, a, b, a, b}
	var last loopAction
	var reason string
	for i, tc := range calls {
		args := tc.Args
		if tc.Name == "read_file" {
			args = json.RawMessage(`{"path":"p` + contents[i] + `"}`)
		} else {
			args = json.RawMessage(`{"pattern":"p` + contents[i] + `"}`)
		}
		last, reason = g.Observe(
			[]llm.ToolCall{{Name: tc.Name, Args: args}},
			[]tools.Result{{Content: contents[i]}},
		)
		if last == loopStop {
			break
		}
	}
	if last != loopWarn && last != loopStop {
		t.Fatalf("want cycle warn/stop got %v %q", last, reason)
	}
}
