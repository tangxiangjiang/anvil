// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil/internal/index"
	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/runtime"
	"github.com/anvil-dev/anvil/internal/tools"
)

func TestSemanticSearchTool(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "widget.go"), []byte("package main\nfunc UniqueWidgetXYZ() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Build(context.Background(), dir, index.BuildOptions{}); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry(dir, false)
	res := reg.Execute(context.Background(), "semantic_search", mustJSON(map[string]any{
		"query": "UniqueWidgetXYZ",
		"top_k": 5,
	}))
	if res.IsError {
		t.Fatal(res.Content)
	}
	if !strings.Contains(res.Content, "UniqueWidgetXYZ") && !strings.Contains(res.Content, "widget.go") {
		t.Fatalf("unexpected: %s", res.Content)
	}
}

func TestSemanticSearchInLoop(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "core.go"), []byte("package core\nfunc FindMeSymbol() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Build(context.Background(), dir, index.BuildOptions{}); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry(dir, false)
	var sawTool bool
	res := runtime.Loop(context.Background(), &searchScript{}, reg, runtime.Config{
		CWD: dir, Model: "m", MaxSteps: 8, UserMsg: "find FindMeSymbol",
	}, func(ev runtime.Event) {
		if ev.Type == "tool_call" && ev.Name == "semantic_search" {
			sawTool = true
		}
	})
	if res.Status != "finished" {
		t.Fatalf("status=%s err=%s", res.Status, res.Err)
	}
	if !sawTool {
		t.Fatal("expected semantic_search tool call")
	}
	if !strings.Contains(res.Result, "ok") {
		t.Fatalf("result %q", res.Result)
	}
}

type searchScript struct{ n int }

func (s *searchScript) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	_ = ctx
	_ = req
	s.n++
	if s.n == 1 {
		return &llm.Response{Message: llm.Message{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{{
				ID:   "1",
				Name: "semantic_search",
				Args: json.RawMessage(`{"query":"FindMeSymbol"}`),
			}},
		}}, nil
	}
	return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: "ok"}}, nil
}
