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
