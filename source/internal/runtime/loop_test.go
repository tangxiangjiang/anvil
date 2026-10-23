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
	case 2:
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
	if completer.n != 3 || res.Steps != 2 {
		t.Fatalf("calls=%d steps=%d", completer.n, res.Steps)
	}
	if res.Usage == nil || res.Usage.TotalTokens != 14 {
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

func TestLoopErrorsAfterTwoEmptyResponses(t *testing.T) {
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

	if res.Status != "error" || res.Code != runtime.CodeProvider || res.Err != runtime.EmptyResponseError {
		t.Fatalf("status=%s code=%q err=%q", res.Status, res.Code, res.Err)
	}
	if completer.n != 2 || res.Steps != 1 {
		t.Fatalf("calls=%d steps=%d", completer.n, res.Steps)
	}
	if doneEvents != 0 || errorEvent == nil || errorEvent.Text != runtime.EmptyResponseError {
		t.Fatalf("done=%d error=%+v", doneEvents, errorEvent)
	}
	if len(persisted) != 1 || persisted[0].Role != llm.RoleUser {
		t.Fatalf("persisted=%+v", persisted)
	}
	if res.Usage == nil || res.Usage.TotalTokens != 4 {
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
			Role:    llm.RoleAssistant,
			Content: "已完成实际处理。",
		}}, nil
	}
}

func TestLoopContinuesAfterModelOnlyAnnouncesWork(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry(dir, false)
	completer := &deferredThenToolCompleter{}
	var persisted []llm.Message
	res := runtime.Loop(context.Background(), completer, reg, runtime.Config{
		CWD:     dir,
		Model:   "m",
		UserMsg: "处理一下",
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
	retryMessages := completer.requests[1].Messages
	if len(retryMessages) < 2 ||
		!strings.Contains(retryMessages[len(retryMessages)-2].Content, "我先确认") ||
		!strings.Contains(retryMessages[len(retryMessages)-1].Content, "do not stop") {
		t.Fatalf("corrective retry messages=%+v", retryMessages)
	}
	for _, message := range persisted {
		if strings.Contains(message.Content, "我先确认") || strings.Contains(message.Content, "do not stop") {
			t.Fatalf("transient corrective turn was persisted: %+v", message)
		}
	}
}

type alwaysDeferredCompleter struct {
	n int
}

func (c *alwaysDeferredCompleter) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	_ = ctx
	_ = req
	c.n++
	return &llm.Response{Message: llm.Message{
		Role:    llm.RoleAssistant,
		Content: "我会排除路径或文件名含 Streaming 的改动，其余改动作为一次本地快照提交。",
	}}, nil
}

func TestLoopErrorsWhenModelOnlyAnnouncesWorkTwice(t *testing.T) {
	dir := t.TempDir()
	reg := tools.NewRegistry(dir, false)
	completer := &alwaysDeferredCompleter{}
	var persisted []llm.Message
	var doneEvents int
	res := runtime.Loop(context.Background(), completer, reg, runtime.Config{
		CWD:     dir,
		Model:   "m",
		UserMsg: "修复问题",
		Sink: func(message llm.Message) {
			persisted = append(persisted, message)
		},
	}, func(event runtime.Event) {
		if event.Type == "done" {
			doneEvents++
		}
	})

	if res.Status != "error" || res.Code != runtime.CodeProvider || res.Err != runtime.UnproductiveResponseError {
		t.Fatalf("status=%s code=%q err=%q", res.Status, res.Code, res.Err)
	}
	if completer.n != 2 || doneEvents != 0 {
		t.Fatalf("calls=%d done=%d", completer.n, doneEvents)
	}
	if len(persisted) != 1 || persisted[0].Role != llm.RoleUser {
		t.Fatalf("persisted=%+v", persisted)
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
				ID:   "read",
				Name: "read_file",
				Args: json.RawMessage(`{"path":"note.txt"}`),
			}},
		}}, nil
	case 2:
		return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: "已完成修改。"}}, nil
	case 3:
		return &llm.Response{Message: llm.Message{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{{
				ID:   "write",
				Name: "write_file",
				Args: json.RawMessage(`{"path":"note.txt","content":"changed"}`),
			}},
		}}, nil
	default:
		return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: "已完成修改。"}}, nil
	}
}

func TestLoopRequiresMutationEvidenceForAChangeRequest(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry(dir, false)
	completer := &mutationEvidenceCompleter{}
	var persisted []llm.Message
	res := runtime.Loop(context.Background(), completer, reg, runtime.Config{
		CWD:     dir,
		Model:   "m",
		UserMsg: "帮我修改 note.txt",
		Sink: func(message llm.Message) {
			persisted = append(persisted, message)
		},
	}, nil)

	if res.Status != "finished" || completer.n != 4 {
		t.Fatalf("status=%s calls=%d err=%q", res.Status, completer.n, res.Err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "note.txt"))
	if err != nil || string(data) != "changed" {
		t.Fatalf("file=%q err=%v", data, err)
	}
	if !strings.Contains(completer.requests[2].Messages[len(completer.requests[2].Messages)-1].Content, "no successful write") {
		t.Fatalf("corrective request=%+v", completer.requests[2].Messages)
	}
	for _, message := range persisted {
		if strings.Contains(message.Content, "no successful write") {
			t.Fatalf("completion correction was persisted: %+v", message)
		}
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
