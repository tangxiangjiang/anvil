// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package runtime

import (
	"encoding/json"
	"fmt"
	"strings"
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
	// The same two calls, over and over: the second pass learns nothing the
	// first one did not. Results vary so only the cycle detector can fire.
	var last loopAction
	var reason string
	for i := 0; i < 6; i++ {
		tc := a
		if i%2 == 1 {
			tc = b
		}
		last, reason = g.Observe(
			[]llm.ToolCall{tc},
			[]tools.Result{{Content: fmt.Sprintf("result-%d", i)}},
		)
		if last == loopStop {
			break
		}
	}
	if last != loopWarn && last != loopStop {
		t.Fatalf("want cycle warn/stop got %v %q", last, reason)
	}
	if !strings.Contains(reason, "tool cycle [read_file grep]") {
		t.Fatalf("reason names the tools, got %q", reason)
	}
}

// The reported false positive (0.40.x): a model working through a large source
// file alternates read_file and grep, with a different path and pattern every
// time, and the name-only cycle detector stopped the run as
// "[read_file grep] repeated ×3". Different arguments are progress.
func TestLoopGuardAlternatingNamesWithNewArgumentsIsProgress(t *testing.T) {
	g := newLoopGuard()
	for i := 0; i < 12; i++ {
		var tc llm.ToolCall
		if i%2 == 0 {
			tc = llm.ToolCall{Name: "read_file", Args: json.RawMessage(fmt.Sprintf(`{"path":"f%d"}`, i))}
		} else {
			tc = llm.ToolCall{Name: "grep", Args: json.RawMessage(fmt.Sprintf(`{"pattern":"p%d","path":"f%d"}`, i, i-1))}
		}
		res := tools.Result{Content: fmt.Sprintf("body-%d", i)}
		action, reason := g.Observe([]llm.ToolCall{tc}, []tools.Result{res})
		if action != loopOK {
			t.Fatalf("call %d: action=%v reason=%q", i, action, reason)
		}
	}
}

func TestLoopGuardSameToolNameNotCycle(t *testing.T) {
	g := newLoopGuard()
	// Six different read_file calls must not trip the cycle detector: a
	// repeated name is not a repeated call.
	for i := 0; i < 6; i++ {
		tc := llm.ToolCall{
			Name: "read_file",
			Args: json.RawMessage(fmt.Sprintf(`{"path":"f%d"}`, i)),
		}
		res := tools.Result{Content: fmt.Sprintf("body-%d", i)}
		action, reason := g.Observe([]llm.ToolCall{tc}, []tools.Result{res})
		if action != loopOK {
			t.Fatalf("read_file batch %d: action=%v reason=%q", i, action, reason)
		}
	}
}

func TestLoopGuardAllowsRunningTaskPolls(t *testing.T) {
	g := newLoopGuard()
	tc := llm.ToolCall{Name: "task_output", Args: json.RawMessage(`{"task_id":"task-42"}`)}
	res := tools.Result{Content: "task-42 running\n(no output)\nnext offset 0"}
	for i := 0; i < 12; i++ {
		action, reason := g.Observe([]llm.ToolCall{tc}, []tools.Result{res})
		if action != loopOK {
			t.Fatalf("poll %d: action=%v reason=%q", i, action, reason)
		}
	}

	// Completion is progress. Reading that same terminal result forever is
	// still a real loop and must retain the ordinary warn/stop behavior.
	finished := tools.Result{Content: "task-42 finished\ncomplete"}
	if action, reason := g.Observe([]llm.ToolCall{tc}, []tools.Result{finished}); action != loopOK {
		t.Fatalf("first terminal read: action=%v reason=%q", action, reason)
	}
	var action loopAction
	for i := 0; i < 3; i++ {
		action, _ = g.Observe([]llm.ToolCall{tc}, []tools.Result{finished})
	}
	if action != loopStop {
		t.Fatalf("repeated terminal read: want stop, got %v", action)
	}
}

func TestLoopGuardDoesNotExemptInvalidRunningTaskResult(t *testing.T) {
	cases := []struct {
		name    string
		args    json.RawMessage
		content string
	}{
		{name: "mismatched id", args: json.RawMessage(`{"task_id":"task-42"}`), content: "task-99 running"},
		{name: "missing id", args: json.RawMessage(`{}`), content: "task-42 running"},
		{name: "error", args: json.RawMessage(`{"task_id":"task-42"}`), content: "unknown task: task-42"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newLoopGuard()
			call := llm.ToolCall{Name: "task_output", Args: tc.args}
			var action loopAction
			for i := 0; i < 4; i++ {
				action, _ = g.Observe([]llm.ToolCall{call}, []tools.Result{{Content: tc.content}})
			}
			if action != loopStop {
				t.Fatalf("want stop, got %v", action)
			}
		})
	}
}
