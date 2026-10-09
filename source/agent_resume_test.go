// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/store"
)

func TestResumeLoadsHistoryAndRejectsMissing(t *testing.T) {
	dir := t.TempDir()
	opts := Options{
		APIKey: "test-key-not-a-secret-for-assert",
		Model:  Model{ID: "test-model"},
		Local:  LocalOptions{CWD: dir},
	}

	a, err := Create(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	id := a.AgentID()

	// Simulate a prior turn with user / assistant(tool_calls) / tool messages.
	_ = a.store.Append(id, store.Record{
		Type: store.TypeMessage, Role: "user", Content: "read a.txt",
	})
	_ = a.store.Append(id, store.Record{
		Type: store.TypeMessage, Role: "assistant",
		ToolCalls: []store.ToolCall{{
			ID: "1", Name: "read_file", Args: json.RawMessage(`{"path":"a.txt"}`),
		}},
	})
	_ = a.store.Append(id, store.Record{
		Type: store.TypeMessage, Role: "tool", ToolCallID: "1", Name: "read_file", Content: "hello",
	})
	_ = a.store.Append(id, store.Record{
		Type: store.TypeMessage, Role: "assistant", Content: "file says hello",
	})
	_ = a.Close()

	raw, err := os.ReadFile(filepath.Join(dir, ".anvil", "agents", id+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), opts.APIKey) {
		t.Fatal("API key leaked into store")
	}

	resumed, err := Resume(context.Background(), id, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()

	if resumed.AgentID() != id {
		t.Fatalf("id=%s", resumed.AgentID())
	}
	if len(resumed.history) != 4 {
		t.Fatalf("history len=%d %+v", len(resumed.history), resumed.history)
	}
	if resumed.history[0].Role != llm.RoleUser || resumed.history[0].Content != "read a.txt" {
		t.Fatalf("%+v", resumed.history[0])
	}
	if len(resumed.history[1].ToolCalls) != 1 || resumed.history[1].ToolCalls[0].Name != "read_file" {
		t.Fatalf("%+v", resumed.history[1])
	}
	if resumed.history[2].Role != llm.RoleTool || resumed.history[2].Content != "hello" {
		t.Fatalf("%+v", resumed.history[2])
	}

	_, err = Resume(context.Background(), "agent-does-not-exist", opts)
	if err == nil {
		t.Fatal("expected missing agent error")
	}
	if ae, ok := err.(*Error); !ok || ae.Retryable {
		t.Fatalf("want non-retryable *Error, got %T %v", err, err)
	}
}
