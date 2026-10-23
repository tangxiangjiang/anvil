// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package eval

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/permissions"
	"github.com/anvil-dev/anvil/internal/runtime"
)

func TestScriptExpectFailureIncludesRequest(t *testing.T) {
	task := Task{Name: "predicate", UserMsg: "visible request", Script: []Reply{{
		Message: llm.Message{Role: llm.RoleAssistant, Content: "unused"},
		Expect:  func(llm.Request) error { return errors.New("missing safety clause") },
	}}, Check: func(string, runtime.Result) error { return nil }}
	out, err := ExecuteScript(t.Context(), t.TempDir(), task)
	if err == nil || !strings.Contains(err.Error(), "missing safety clause") ||
		!strings.Contains(err.Error(), "visible request") || out.Score.Completed {
		t.Fatalf("predicate failure was hidden: outcome=%+v error=%v", out, err)
	}
}

func TestScriptPredicateAndResponseDoNotAlias(t *testing.T) {
	req := llm.Request{Messages: []llm.Message{{Content: "original", ToolCalls: []llm.ToolCall{{Args: json.RawMessage(`{}`)}}}}}
	reply := Reply{Message: llm.Message{ToolCalls: []llm.ToolCall{{Args: json.RawMessage(`{"path":"a"}`)}}},
		Expect: func(copy llm.Request) error {
			copy.Messages[0].Content = "changed"
			copy.Messages[0].ToolCalls[0].Args[0] = '['
			return nil
		}}
	s := NewScript([]Reply{reply})
	resp, err := s.Complete(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Message.ToolCalls[0].Args[0] = '['
	if req.Messages[0].Content != "original" || string(req.Messages[0].ToolCalls[0].Args) != "{}" ||
		string(reply.Message.ToolCalls[0].Args) != `{"path":"a"}` {
		t.Fatal("predicate or response modified the original")
	}
}

func TestScriptCancellationAndConcurrentCalls(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	s := NewScript(nil)
	if _, err := s.Complete(ctx, llm.Request{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
	s = NewScript(Replies([]llm.Message{{Content: "first"}}))
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Go(func() {
			if _, err := s.Complete(t.Context(), llm.Request{}); err != nil {
				t.Errorf("complete: %v", err)
			}
		})
	}
	wg.Wait()
}

func TestScorerSeparatesRefusalsAndErrors(t *testing.T) {
	s := &scorer{}
	events := []runtime.Event{
		{Type: "tool_result", IsError: true, Content: "permission denied opening ordinary file"},
		{Type: "tool_result", IsError: true, Content: "path escapes cwd: ../secret"},
		{Type: "tool_result", IsError: true, Rule: &permissions.Decision{Matched: true, Effect: permissions.Deny}},
		{Type: "tool_result", IsError: true, Content: CallbackRefusal},
		{Type: "tool_result", IsError: true, Rule: &permissions.Decision{Matched: true, Effect: permissions.Ask},
			Content: "shell denied: workspace rule 1 asks for confirmation and no approval channel is installed"},
		{Type: "tool_result", IsError: false, Content: "path escapes cwd: quoted content"},
		{Type: "assistant", Code: runtime.CodeLoopDetected},
		{Type: "error", Code: runtime.CodeLoopDetected},
	}
	for _, event := range events {
		s.observe(event)
	}
	score := s.finish(runtime.Result{Steps: 4, Code: runtime.CodeLoopDetected}, true, 1024)
	if score.ToolErrors != 5 || score.Refused != 4 || score.LoopStops != 1 || !score.Completed ||
		score.Steps != 4 || score.RequestBytes != 1024 || score.Retries != 0 || score.Agents != 0 {
		t.Fatalf("score=%+v", score)
	}
	if got := s.finish(runtime.Result{Code: runtime.CodeMaxSteps}, true, 0).LoopStops; got != 0 {
		t.Fatal("max steps counted as loop guard")
	}
}

func TestFixtureScores(t *testing.T) {
	for _, task := range fixtures() {
		t.Run(task.Name, func(t *testing.T) {
			out, err := ExecuteScript(t.Context(), t.TempDir(), task)
			if err != nil || !out.Score.Completed || out.Score.RequestBytes <= 0 {
				t.Fatalf("outcome=%+v error=%v", out, err)
			}
			if task.Name == "sandbox_escape_denied" && (out.Score.ToolErrors != 1 || out.Score.Refused != 1) {
				t.Fatalf("escape counters=%+v", out.Score)
			}
		})
	}
}

func TestTaskRejectsTwoTiers(t *testing.T) {
	_, err := ExecuteScript(t.Context(), t.TempDir(), Task{
		Name: "ambiguous", Recording: "fixture.jsonl", Script: []Reply{{}},
	})
	if err == nil || !strings.Contains(err.Error(), "both Script and Recording") {
		t.Fatalf("error=%v", err)
	}
}
