// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package runtime_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/runtime"
	"github.com/anvil-dev/anvil/internal/tools"
)

func TestLoopPutsUsageOnTheTerminalEvent(t *testing.T) {
	dir := t.TempDir()
	reg := tools.NewRegistry(dir, false)
	var done *runtime.Event
	res := runtime.Loop(context.Background(), usageCompleter{}, reg, runtime.Config{
		CWD:     dir,
		Model:   "m",
		UserMsg: "hi",
	}, func(ev runtime.Event) {
		if ev.Type == "done" {
			e := ev
			done = &e
		}
	})
	if res.Status != "finished" {
		t.Fatalf("status=%s err=%s", res.Status, res.Err)
	}
	if done == nil || done.Usage == nil {
		t.Fatalf("done event usage=%+v", done)
	}
	if done.Usage.TotalTokens != 7 || done.Usage.InputTokens != 4 || done.Usage.OutputTokens != 3 {
		t.Fatalf("done usage=%+v", done.Usage)
	}
}

type todoCompleter struct {
	n    int
	args string
}

func (c *todoCompleter) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	_ = ctx
	_ = req
	c.n++
	if c.n == 1 {
		return &llm.Response{Message: llm.Message{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{{
				ID:   "1",
				Name: tools.TodoToolName,
				Args: json.RawMessage(c.args),
			}},
		}}, nil
	}
	return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: "planned"}}, nil
}

func TestLoopEmitsTheTodoList(t *testing.T) {
	dir := t.TempDir()
	reg := tools.NewRegistry(dir, false)
	var todos []runtime.Event
	res := runtime.Loop(context.Background(), &todoCompleter{
		args: `{"items":[{"text":"a","status":"pending"},{"text":"b","status":"done"}]}`,
	}, reg, runtime.Config{
		CWD:     dir,
		Model:   "m",
		UserMsg: "plan",
	}, func(ev runtime.Event) {
		if ev.Type == "todo" {
			todos = append(todos, ev)
		}
	})
	if res.Status != "finished" {
		t.Fatalf("status=%s err=%s", res.Status, res.Err)
	}
	if len(todos) != 1 || len(todos[0].Items) != 2 {
		t.Fatalf("todo events %+v", todos)
	}
	if todos[0].Items[0].Text != "a" || todos[0].Items[1].Status != "done" {
		t.Fatalf("items %+v", todos[0].Items)
	}
}

// A refused call must not draw a list: the panel keeps the one it had.
func TestLoopDoesNotEmitTodosForAFailedCall(t *testing.T) {
	dir := t.TempDir()
	reg := tools.NewRegistry(dir, false)
	var todos int
	res := runtime.Loop(context.Background(), &todoCompleter{
		args: `{"items":[{"text":"a","status":"blocked"}]}`,
	}, reg, runtime.Config{
		CWD:     dir,
		Model:   "m",
		UserMsg: "plan",
	}, func(ev runtime.Event) {
		if ev.Type == "todo" {
			todos++
		}
	})
	if res.Status != "finished" {
		t.Fatalf("status=%s err=%s", res.Status, res.Err)
	}
	if todos != 0 {
		t.Fatalf("a failed call emitted %d todo events", todos)
	}
}

type usageCompleter struct{}

func (usageCompleter) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	_ = ctx
	_ = req
	return &llm.Response{
		Message: llm.Message{Role: llm.RoleAssistant, Content: "ok"},
		Usage:   &llm.Usage{InputTokens: 4, OutputTokens: 3, TotalTokens: 7},
	}, nil
}

type backgroundCompositionCompleter struct{ n int }

func (c *backgroundCompositionCompleter) Complete(_ context.Context, _ llm.Request) (*llm.Response, error) {
	c.n++
	switch c.n {
	case 1:
		return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
			ID: "spawn", Name: "spawn_agent",
			Args: json.RawMessage(`{"prompt":"inspect","description":"research","background":true}`),
		}}}}, nil
	case 2:
		return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: "done before join"}}, nil
	case 3:
		return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
			ID: "output", Name: "task_output", Args: json.RawMessage(`{"task_id":"task-123"}`),
		}}}}, nil
	case 4:
		return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: "I will implement it next."}}, nil
	default:
		return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: "Integrated the child result and completed the parent task."}}, nil
	}
}

func TestLoopJoinsAndComposesBackgroundSubagentBeforeFinishing(t *testing.T) {
	dir := t.TempDir()
	reg := tools.NewRegistry(dir, false)
	for name, output := range map[string]string{
		"spawn_agent": "task-123 started in the background: subagent research",
		"task_output": "task-123 finished\nchild result",
	} {
		out := output
		if err := reg.Register(&tools.FuncTool{
			Name: name, Builtin: true,
			Handler: func(context.Context, json.RawMessage) (string, error) { return out, nil },
		}); err != nil {
			t.Fatal(err)
		}
	}
	model := &backgroundCompositionCompleter{}
	res := runtime.Loop(context.Background(), model, reg, runtime.Config{
		CWD: dir, Model: "m", UserMsg: "coordinate the parent and child",
	}, nil)
	if res.Status != "finished" || res.Result != "Integrated the child result and completed the parent task." {
		t.Fatalf("result = %+v", res)
	}
	if model.n != 5 {
		t.Fatalf("model requests = %d, want join plus composition checkpoint", model.n)
	}
}

type scriptCompleter struct {
	n int
}

func (s *scriptCompleter) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	_ = ctx
	_ = req
	s.n++
	if s.n == 1 {
		return &llm.Response{Message: llm.Message{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{{
				ID:   "1",
				Name: "read_file",
				Args: json.RawMessage(`{"path":"note.txt"}`),
			}},
		}}, nil
	}
	return &llm.Response{Message: llm.Message{
		Role:    llm.RoleAssistant,
		Content: "done reading",
	}}, nil
}

func TestLoopToolThenFinish(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry(dir, false)
	var types []string
	res := runtime.Loop(context.Background(), &scriptCompleter{}, reg, runtime.Config{
		CWD:     dir,
		Model:   "m",
		UserMsg: "read note",
	}, func(ev runtime.Event) {
		types = append(types, ev.Type)
	})
	if res.Status != "finished" {
		t.Fatalf("status=%s err=%s", res.Status, res.Err)
	}
	if res.Result != "done reading" {
		t.Fatalf("result %q", res.Result)
	}
	if !containsAll(types, "progress", "tool_call", "tool_result", "assistant", "done") {
		t.Fatalf("events %v", types)
	}
}

type toolEmptyThenFinishCompleter struct {
	n int
}

func (c *toolEmptyThenFinishCompleter) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	_ = ctx
	_ = req
	c.n++
	switch c.n {
	case 1:
		return &llm.Response{
			Message: llm.Message{
				Role: llm.RoleAssistant,
				ToolCalls: []llm.ToolCall{{
					ID:   "1",
					Name: "read_file",
					Args: json.RawMessage(`{"path":"note.txt"}`),
				}},
			},
			Usage: &llm.Usage{InputTokens: 3, OutputTokens: 1, TotalTokens: 4},
		}, nil
	case 2, 3:
		return &llm.Response{
			Message: llm.Message{Role: llm.RoleAssistant, Content: " \n\t"},
			Usage:   &llm.Usage{InputTokens: 4, TotalTokens: 4},
		}, nil
	default:
		return &llm.Response{
			Message: llm.Message{Role: llm.RoleAssistant, Content: "done after retry"},
			Usage:   &llm.Usage{InputTokens: 4, OutputTokens: 2, TotalTokens: 6},
		}, nil
	}
}

func TestLoopRetriesEmptyResponseWithoutPersistingIt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry(dir, false)
	completer := &toolEmptyThenFinishCompleter{}
	var persisted []llm.Message
	res := runtime.Loop(context.Background(), completer, reg, runtime.Config{
		CWD:     dir,
		Model:   "m",
		UserMsg: "read note",
		Sink: func(message llm.Message) {
			persisted = append(persisted, message)
		},
	}, nil)

	if res.Status != "finished" || res.Result != "done after retry" {
		t.Fatalf("status=%s result=%q err=%q", res.Status, res.Result, res.Err)
	}
	if completer.n != 4 || res.Steps != 2 {
		t.Fatalf("calls=%d steps=%d", completer.n, res.Steps)
	}
	if res.Usage == nil || res.Usage.TotalTokens != 18 {
		t.Fatalf("usage=%+v", res.Usage)
	}
	for _, message := range persisted {
		if message.Role == llm.RoleAssistant && len(message.ToolCalls) == 0 && strings.TrimSpace(message.Content) == "" {
			t.Fatalf("empty assistant message was persisted: %+v", message)
		}
	}
}

type emptyCompleter struct {
	n int
}

func (c *emptyCompleter) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	_ = ctx
	_ = req
	c.n++
	return &llm.Response{
		Message: llm.Message{Role: llm.RoleAssistant},
		Usage:   &llm.Usage{InputTokens: 2, TotalTokens: 2},
	}, nil
}

func TestLoopErrorsAfterFiveEmptyResponses(t *testing.T) {
	dir := t.TempDir()
	reg := tools.NewRegistry(dir, false)
	completer := &emptyCompleter{}
	var persisted []llm.Message
	var doneEvents int
	var errorEvent *runtime.Event
	res := runtime.Loop(context.Background(), completer, reg, runtime.Config{
		CWD:     dir,
		Model:   "m",
		UserMsg: "answer",
		Sink: func(message llm.Message) {
			persisted = append(persisted, message)
		},
	}, func(event runtime.Event) {
		switch event.Type {
		case "done":
			doneEvents++
		case "error":
			copy := event
			errorEvent = &copy
		}
	})

	if res.Status != "error" || res.Code != runtime.CodeEmptyResponse || res.Err != runtime.EmptyResponseError {
		t.Fatalf("status=%s code=%q err=%q", res.Status, res.Code, res.Err)
	}
	if completer.n != 5 || res.Steps != 1 {
		t.Fatalf("calls=%d steps=%d", completer.n, res.Steps)
	}
	if doneEvents != 0 || errorEvent == nil || errorEvent.Text != runtime.EmptyResponseError {
		t.Fatalf("done=%d error=%+v", doneEvents, errorEvent)
	}
	if len(persisted) != 1 || persisted[0].Role != llm.RoleUser {
		t.Fatalf("persisted=%+v", persisted)
	}
	if res.Usage == nil || res.Usage.TotalTokens != 10 {
		t.Fatalf("usage=%+v", res.Usage)
	}
}

type deferredThenToolCompleter struct {
	n        int
	requests []llm.Request
}

func (c *deferredThenToolCompleter) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	_ = ctx
	c.n++
	req.Messages = append([]llm.Message(nil), req.Messages...)
	c.requests = append(c.requests, req)
	switch c.n {
	case 1:
		return &llm.Response{Message: llm.Message{
			Role:    llm.RoleAssistant,
			Content: "我先确认当前改动范围，把 Streaming 相关文件排除，只提交其余改动。",
		}}, nil
	case 2:
		return &llm.Response{Message: llm.Message{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{{
				ID:   "1",
				Name: "read_file",
				Args: json.RawMessage(`{"path":"note.txt"}`),
			}},
		}}, nil
	default:
		return &llm.Response{Message: llm.Message{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{{
				ID:   "finish",
				Name: "finish_task",
				Args: json.RawMessage(`{"summary":"已完成实际处理。"}`),
			}},
		}}, nil
	}
}

func TestLoopRequiresFinishTaskAfterModelOnlyAnnouncesWork(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry(dir, false)
	completer := &deferredThenToolCompleter{}
	var persisted []llm.Message
	res := runtime.Loop(context.Background(), completer, reg, runtime.Config{
		CWD:               dir,
		Model:             "m",
		UserMsg:           "处理一下",
		RequireFinishTask: true,
		Sink: func(message llm.Message) {
			persisted = append(persisted, message)
		},
	}, nil)

	if res.Status != "finished" || res.Result != "已完成实际处理。" || completer.n != 3 {
		t.Fatalf("status=%s result=%q calls=%d err=%q", res.Status, res.Result, completer.n, res.Err)
	}
	if len(completer.requests) < 2 {
		t.Fatalf("requests=%d", len(completer.requests))
	}
	first := completer.requests[0]
	hasFinishDef := false
	for _, def := range first.Tools {
		hasFinishDef = hasFinishDef || def.Name == "finish_task"
	}
	if !hasFinishDef || len(first.Messages) == 0 || !strings.Contains(first.Messages[0].Content, "Completion protocol") {
		t.Fatalf("finish protocol was not declared: tools=%+v messages=%+v", first.Tools, first.Messages)
	}
	retryMessages := completer.requests[1].Messages
	if len(retryMessages) < 2 ||
		!strings.Contains(retryMessages[len(retryMessages)-2].Content, "我先确认") ||
		!strings.Contains(retryMessages[len(retryMessages)-1].Content, "finish_task") {
		t.Fatalf("corrective retry messages=%+v", retryMessages)
	}
	for _, message := range persisted {
		if strings.Contains(message.Content, "我先确认") || strings.Contains(message.Content, "ordinary assistant text") {
			t.Fatalf("transient corrective turn was persisted: %+v", message)
		}
		for _, call := range message.ToolCalls {
			if call.Name == "finish_task" {
				t.Fatalf("finish control call was persisted: %+v", message)
			}
		}
	}
}

type alwaysDeferredCompleter struct {
	n        int
	requests []llm.Request
}

func (c *alwaysDeferredCompleter) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	_ = ctx
	c.requests = append(c.requests, req)
	c.n++
	return &llm.Response{Message: llm.Message{
		Role:    llm.RoleAssistant,
		Content: "我继续完成这版实现，先核对已写入的代码和 RHI 接口，再补齐 IO 完成、取消回收与 CPU 回退路径。",
	}}, nil
}

func TestLoopErrorsWhenModelNeverCallsFinishTask(t *testing.T) {
	dir := t.TempDir()
	reg := tools.NewRegistry(dir, false)
	completer := &alwaysDeferredCompleter{}
	var persisted []llm.Message
	var doneEvents int
	res := runtime.Loop(context.Background(), completer, reg, runtime.Config{
		CWD:               dir,
		Model:             "m",
		UserMsg:           "修复问题",
		RequireFinishTask: true,
		Sink: func(message llm.Message) {
			persisted = append(persisted, message)
		},
	}, func(event runtime.Event) {
		if event.Type == "done" {
			doneEvents++
		}
	})

	if res.Status != "error" || res.Code != runtime.CodeFinishProtocol || res.Err != runtime.FinishTaskRequiredError {
		t.Fatalf("status=%s code=%q err=%q", res.Status, res.Code, res.Err)
	}
	if completer.n != 4 || doneEvents != 0 {
		t.Fatalf("calls=%d done=%d", completer.n, doneEvents)
	}
	last := completer.requests[len(completer.requests)-1]
	if len(last.Tools) != 1 || last.Tools[0].Name != "finish_task" {
		t.Fatalf("forced finalization tools=%+v", last.Tools)
	}
	if len(persisted) != 1 || persisted[0].Role != llm.RoleUser {
		t.Fatalf("persisted=%+v", persisted)
	}
}

type finishRecoveryCompleter struct {
	streamCalls   int
	completeCalls int
	last          llm.Request
}

func (c *finishRecoveryCompleter) Complete(
	ctx context.Context,
	req llm.Request,
) (*llm.Response, error) {
	_ = ctx
	c.completeCalls++
	c.last = req
	return &llm.Response{Message: llm.Message{
		Role: llm.RoleAssistant,
		ToolCalls: []llm.ToolCall{{
			ID:   "finish-recovery",
			Name: "finish_task",
			Args: json.RawMessage(`{"summary":"恢复收口成功。"}`),
		}},
	}}, nil
}

func (c *finishRecoveryCompleter) CompleteStream(
	ctx context.Context,
	req llm.Request,
	onDelta func(llm.Delta),
) (*llm.Response, error) {
	_ = ctx
	_ = req
	_ = onDelta
	c.streamCalls++
	return &llm.Response{Message: llm.Message{
		Role:    llm.RoleAssistant,
		Content: "工作已经完成。",
	}}, nil
}

func TestLoopRecoversFinishProtocolWithOnlyFinishToolAndNonStreaming(t *testing.T) {
	reg := tools.NewRegistry(t.TempDir(), false)
	completer := &finishRecoveryCompleter{}
	res := runtime.Loop(context.Background(), completer, reg, runtime.Config{
		CWD:               t.TempDir(),
		Model:             "m",
		UserMsg:           "完成任务",
		RequireFinishTask: true,
	}, nil)

	if res.Status != "finished" || res.Result != "恢复收口成功。" {
		t.Fatalf("status=%s result=%q err=%q", res.Status, res.Result, res.Err)
	}
	if completer.streamCalls != 3 || completer.completeCalls != 1 {
		t.Fatalf("stream=%d complete=%d", completer.streamCalls, completer.completeCalls)
	}
	if len(completer.last.Tools) != 1 || completer.last.Tools[0].Name != "finish_task" {
		t.Fatalf("forced finalization tools=%+v", completer.last.Tools)
	}
}

type mutationEvidenceCompleter struct {
	n        int
	requests []llm.Request
}

func (c *mutationEvidenceCompleter) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	_ = ctx
	req.Messages = append([]llm.Message(nil), req.Messages...)
	c.requests = append(c.requests, req)
	c.n++
	switch c.n {
	case 1:
		return &llm.Response{Message: llm.Message{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{{
				ID:   "write",
				Name: "write_file",
				Args: json.RawMessage(`{"path":"note.txt","content":"changed"}`),
			}},
		}}, nil
	default:
		return &llm.Response{Message: llm.Message{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{{
				ID:   "finish",
				Name: "finish_task",
				Args: json.RawMessage(`{"summary":"已完成修改。"}`),
			}},
		}}, nil
	}
}

func TestLoopFinishesOnlyFromFinishTaskAndPersistsItsSummary(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry(dir, false)
	completer := &mutationEvidenceCompleter{}
	var persisted []llm.Message
	res := runtime.Loop(context.Background(), completer, reg, runtime.Config{
		CWD:               dir,
		Model:             "m",
		UserMsg:           "继续按照架构落地代码，之前中断了",
		RequireFinishTask: true,
		Sink: func(message llm.Message) {
			persisted = append(persisted, message)
		},
	}, nil)

	if res.Status != "finished" || res.Result != "已完成修改。" || completer.n != 2 {
		t.Fatalf("status=%s calls=%d err=%q", res.Status, completer.n, res.Err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "note.txt"))
	if err != nil || string(data) != "changed" {
		t.Fatalf("file=%q err=%v", data, err)
	}
	for _, message := range persisted {
		for _, call := range message.ToolCalls {
			if call.Name == "finish_task" {
				t.Fatalf("finish control call was persisted: %+v", message)
			}
		}
	}
	if persisted[len(persisted)-1].Role != llm.RoleAssistant || persisted[len(persisted)-1].Content != "已完成修改。" {
		t.Fatalf("final persisted message = %+v", persisted[len(persisted)-1])
	}
}

type streamedTextCompleter struct {
	n int
}

func (c *streamedTextCompleter) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	return c.CompleteStream(ctx, req, func(llm.Delta) {})
}

func (c *streamedTextCompleter) CompleteStream(ctx context.Context, req llm.Request, onDelta func(llm.Delta)) (*llm.Response, error) {
	_ = ctx
	_ = req
	c.n++
	onDelta(llm.Delta{Text: "streamed answer"})
	return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant}}, nil
}

func TestLoopUsesStreamedTextWhenFinalMessageIsEmpty(t *testing.T) {
	dir := t.TempDir()
	reg := tools.NewRegistry(dir, false)
	completer := &streamedTextCompleter{}
	var persisted []llm.Message
	res := runtime.Loop(context.Background(), completer, reg, runtime.Config{
		CWD:     dir,
		Model:   "m",
		UserMsg: "answer",
		Sink: func(message llm.Message) {
			persisted = append(persisted, message)
		},
	}, nil)

	if res.Status != "finished" || res.Result != "streamed answer" || completer.n != 1 {
		t.Fatalf("status=%s result=%q calls=%d err=%q", res.Status, res.Result, completer.n, res.Err)
	}
	if len(persisted) != 2 || persisted[1].Content != "streamed answer" {
		t.Fatalf("persisted=%+v", persisted)
	}
}

type emptyStreamFallbackCompleter struct {
	streamCalls   int
	completeCalls int
}

func (c *emptyStreamFallbackCompleter) Complete(context.Context, llm.Request) (*llm.Response, error) {
	c.completeCalls++
	return &llm.Response{
		Message: llm.Message{Role: llm.RoleAssistant, Content: "recovered without streaming"},
	}, nil
}

func (c *emptyStreamFallbackCompleter) CompleteStream(
	context.Context, llm.Request, func(llm.Delta),
) (*llm.Response, error) {
	c.streamCalls++
	return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant}}, nil
}

func TestLoopRetriesAnEmptyStreamThroughComplete(t *testing.T) {
	dir := t.TempDir()
	reg := tools.NewRegistry(dir, false)
	completer := &emptyStreamFallbackCompleter{}

	res := runtime.Loop(context.Background(), completer, reg, runtime.Config{
		CWD: dir, Model: "m", UserMsg: "answer",
	}, nil)

	if res.Status != "finished" || res.Result != "recovered without streaming" {
		t.Fatalf("status=%s result=%q err=%q", res.Status, res.Result, res.Err)
	}
	if completer.streamCalls != 1 || completer.completeCalls != 1 || res.Steps != 1 {
		t.Fatalf("stream=%d complete=%d steps=%d", completer.streamCalls, completer.completeCalls, res.Steps)
	}
}

type alwaysEmptyDualCompleter struct {
	streamCalls   int
	completeCalls int
}

func (c *alwaysEmptyDualCompleter) Complete(context.Context, llm.Request) (*llm.Response, error) {
	c.completeCalls++
	return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant}}, nil
}

func (c *alwaysEmptyDualCompleter) CompleteStream(
	context.Context, llm.Request, func(llm.Delta),
) (*llm.Response, error) {
	c.streamCalls++
	return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant}}, nil
}

func TestLoopAlternatesTransportsDuringEmptyRecovery(t *testing.T) {
	dir := t.TempDir()
	reg := tools.NewRegistry(dir, false)
	completer := &alwaysEmptyDualCompleter{}

	res := runtime.Loop(context.Background(), completer, reg, runtime.Config{
		CWD: dir, Model: "m", UserMsg: "answer",
	}, nil)

	if res.Code != runtime.CodeEmptyResponse {
		t.Fatalf("status=%s code=%q err=%q", res.Status, res.Code, res.Err)
	}
	if completer.streamCalls != 3 || completer.completeCalls != 2 {
		t.Fatalf("stream=%d complete=%d", completer.streamCalls, completer.completeCalls)
	}
}

type alwaysToolCompleter struct{}

func (alwaysToolCompleter) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	_ = ctx
	_ = req
	return &llm.Response{Message: llm.Message{
		Role: llm.RoleAssistant,
		ToolCalls: []llm.ToolCall{{
			ID:   "1",
			Name: "read_file",
			Args: json.RawMessage(`{"path":"note.txt"}`),
		}},
	}}, nil
}

func TestLoopGuardStopsRepeatedTool(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry(dir, false)
	res := runtime.Loop(context.Background(), alwaysToolCompleter{}, reg, runtime.Config{
		CWD:     dir,
		Model:   "m",
		UserMsg: "loop",
	}, nil)
	if res.Status != "error" {
		t.Fatalf("status=%s", res.Status)
	}
	if !runtime.IsLoopDetected(res.Err) || res.Code != runtime.CodeLoopDetected {
		t.Fatalf("err %q code %q", res.Err, res.Code)
	}
}

func TestOptionalMaxStepsStillWorks(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry(dir, false)
	res := runtime.Loop(context.Background(), alwaysToolCompleter{}, reg, runtime.Config{
		CWD:      dir,
		Model:    "m",
		MaxSteps: 2,
		UserMsg:  "loop",
	}, nil)
	// MaxSteps=2 hits before loop guard needs 3 repeats + warn
	if res.Status != "error" || !runtime.IsMaxStepsExceeded(res.Err) {
		t.Fatalf("status=%s err=%q", res.Status, res.Err)
	}
}

func TestDefaultMaxStepsUnlimited(t *testing.T) {
	if runtime.DefaultMaxSteps != 0 {
		t.Fatalf("default=%d want 0 (unlimited)", runtime.DefaultMaxSteps)
	}
}

func containsAll(have []string, want ...string) bool {
	set := map[string]bool{}
	for _, h := range have {
		set[h] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}
