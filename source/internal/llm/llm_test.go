// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package llm_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anvil-dev/anvil/internal/llm"
)

func TestOpenAIChatToolsRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{
				map[string]any{
					"message": map[string]any{
						"role": "assistant",
						"tool_calls": []any{
							map[string]any{
								"id":   "call_1",
								"type": "function",
								"function": map[string]any{
									"name":      "read_file",
									"arguments": `{"path":"a.go"}`,
								},
							},
						},
					},
				},
			},
			"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 2, "total_tokens": 3},
		})
	}))
	defer srv.Close()

	c := llm.NewOpenAI(llm.Config{APIKey: "k", BaseURL: srv.URL, Model: "m"})
	resp, err := c.Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
		Tools: []llm.ToolDef{{
			Name:       "read_file",
			Parameters: json.RawMessage(`{"type":"object","properties":{}}`),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0].Name != "read_file" {
		t.Fatalf("%+v", resp.Message.ToolCalls)
	}
}

func TestAnthropicChatToolsRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []any{
				map[string]any{"type": "text", "text": "looking"},
				map[string]any{
					"type":  "tool_use",
					"id":    "toolu_1",
					"name":  "read_file",
					"input": map[string]any{"path": "a.go"},
				},
			},
			"usage": map[string]any{"input_tokens": 1, "output_tokens": 2},
		})
	}))
	defer srv.Close()

	c := llm.NewAnthropic(llm.Config{APIKey: "k", BaseURL: srv.URL, Model: "claude"})
	resp, err := c.Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
		Tools: []llm.ToolDef{{
			Name:       "read_file",
			Parameters: json.RawMessage(`{"type":"object","properties":{}}`),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.Content != "looking" {
		t.Fatalf("content %q", resp.Message.Content)
	}
	if len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0].Name != "read_file" {
		t.Fatalf("%+v", resp.Message.ToolCalls)
	}
}
