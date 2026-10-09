// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package runtime_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/runtime"
	"github.com/anvil-dev/anvil/internal/tools"
)

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
		CWD:      dir,
		Model:    "m",
		MaxSteps: 8,
		UserMsg:  "read note",
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

func TestProgressEventsAndMaxSteps(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry(dir, false)
	var progress []runtime.Event
	res := runtime.Loop(context.Background(), alwaysToolCompleter{}, reg, runtime.Config{
		CWD:      dir,
		Model:    "m",
		MaxSteps: 3,
		UserMsg:  "loop",
	}, func(ev runtime.Event) {
		if ev.Type == "progress" {
			progress = append(progress, ev)
		}
	})
	if res.Status != "error" {
		t.Fatalf("status=%s", res.Status)
	}
	if !runtime.IsMaxStepsExceeded(res.Err) || res.Code != runtime.CodeMaxSteps {
		t.Fatalf("err %q code %q", res.Err, res.Code)
	}
	if len(progress) != 3 {
		t.Fatalf("progress count %d: %+v", len(progress), progress)
	}
	for i, ev := range progress {
		if ev.Step != i+1 || ev.MaxSteps != 3 {
			t.Fatalf("progress[%d]=%+v", i, ev)
		}
	}
}

func TestDefaultMaxSteps(t *testing.T) {
	if runtime.DefaultMaxSteps != 128 {
		t.Fatalf("default=%d", runtime.DefaultMaxSteps)
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
