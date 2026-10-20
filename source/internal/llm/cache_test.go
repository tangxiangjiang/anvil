// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package llm_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil/internal/llm"
)

func TestOpenAICachedTokensNonStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]any{"role": "assistant", "content": "ok"},
			}},
			"usage": map[string]any{
				"prompt_tokens": 30, "completion_tokens": 4, "total_tokens": 34,
				"prompt_tokens_details": map[string]any{"cached_tokens": 20},
			},
		})
	}))
	defer srv.Close()

	c := llm.NewOpenAI(llm.Config{APIKey: "k", BaseURL: srv.URL, Model: "m"})
	resp, err := c.Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Usage == nil || resp.Usage.CachedTokens != 20 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
	// OpenAI's cached_tokens is a subset of prompt_tokens: the total must not
	// grow.
	if resp.Usage.TotalTokens != 34 || resp.Usage.InputTokens != 30 {
		t.Fatalf("total changed: %+v", resp.Usage)
	}
	if resp.Usage.CacheWriteTokens != 0 {
		t.Fatalf("OpenAI reported a cache write: %+v", resp.Usage)
	}
}

func TestDeepSeekCacheHitNonStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]any{"role": "assistant", "content": "ok"},
			}},
			"usage": map[string]any{
				"prompt_tokens": 50, "completion_tokens": 5, "total_tokens": 55,
				"prompt_cache_hit_tokens": 30,
			},
		})
	}))
	defer srv.Close()

	c := llm.NewOpenAI(llm.Config{APIKey: "k", BaseURL: srv.URL, Model: "m"})
	resp, err := c.Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Usage == nil || resp.Usage.CachedTokens != 30 || resp.Usage.TotalTokens != 55 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

func TestOpenAIStreamRequestsUsageAndParsesCache(t *testing.T) {
	cases := []struct {
		name  string
		chunk string
		want  int
	}{
		{
			name: "openai-details",
			chunk: `{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12,` +
				`"prompt_tokens_details":{"cached_tokens":6}}}`,
			want: 6,
		},
		{
			name:  "deepseek-hit",
			chunk: `{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12,"prompt_cache_hit_tokens":4}}`,
			want:  4,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					StreamOptions *struct {
						IncludeUsage bool `json:"include_usage"`
					} `json:"stream_options"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode request: %v", err)
				} else if body.StreamOptions == nil || !body.StreamOptions.IncludeUsage {
					t.Errorf("stream_options.include_usage missing: %+v", body.StreamOptions)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
				fmt.Fprintf(w, "data: %s\n\n", tc.chunk)
				fmt.Fprintf(w, "data: [DONE]\n\n")
			}))
			defer srv.Close()

			c := llm.NewOpenAI(llm.Config{APIKey: "k", BaseURL: srv.URL, Model: "m"})
			resp, err := c.CompleteStream(context.Background(), llm.Request{
				Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if resp.Usage == nil || resp.Usage.CachedTokens != tc.want || resp.Usage.TotalTokens != 12 {
				t.Fatalf("usage = %+v, want cached %d", resp.Usage, tc.want)
			}
		})
	}
}

func TestAnthropicCacheUsageNonStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []any{map[string]any{"type": "text", "text": "ok"}},
			"usage": map[string]any{
				"input_tokens": 10, "output_tokens": 3,
				"cache_read_input_tokens": 100, "cache_creation_input_tokens": 40,
			},
		})
	}))
	defer srv.Close()

	c := llm.NewAnthropic(llm.Config{APIKey: "k", BaseURL: srv.URL, Model: "claude"})
	resp, err := c.Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	u := resp.Usage
	if u == nil || u.CachedTokens != 100 || u.CacheWriteTokens != 40 {
		t.Fatalf("usage = %+v", u)
	}
	// Anthropic's input_tokens excludes the cached parts, so the total adds
	// them: 10 + 100 + 40 + 3.
	if u.TotalTokens != 153 {
		t.Fatalf("total = %d, want 153", u.TotalTokens)
	}
}

func TestAnthropicStreamCacheUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":1,\"cache_read_input_tokens\":100,\"cache_creation_input_tokens\":40}}}\n\n")
		fmt.Fprintf(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n")
		fmt.Fprintf(w, "data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":3}}\n\n")
		fmt.Fprintf(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srv.Close()

	c := llm.NewAnthropic(llm.Config{APIKey: "k", BaseURL: srv.URL, Model: "claude"})
	resp, err := c.CompleteStream(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	u := resp.Usage
	if u == nil || u.CachedTokens != 100 || u.CacheWriteTokens != 40 {
		t.Fatalf("usage = %+v", u)
	}
	if u.TotalTokens != 153 || u.OutputTokens != 3 {
		t.Fatalf("total/output = %+v", u)
	}
}

// cacheBreakpoints counts the ephemeral markers in a marshaled request body.
// Counting the raw text is the only check that sees a marker wherever it was
// placed — system block and message block are different shapes.
func cacheBreakpoints(t *testing.T, raw []byte) (int, map[string]any) {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return strings.Count(string(raw), `"cache_control"`), body
}

func TestAnthropicPromptCacheBreakpoints(t *testing.T) {
	cases := []struct {
		name  string
		cache bool
		want  int
	}{
		{name: "on", cache: true, want: 2},
		{name: "off", cache: false, want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var raw []byte
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ = io.ReadAll(r.Body)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"content": []any{map[string]any{"type": "text", "text": "ok"}},
					"usage":   map[string]any{"input_tokens": 1, "output_tokens": 1},
				})
			}))
			defer srv.Close()

			c := llm.NewAnthropic(llm.Config{APIKey: "k", BaseURL: srv.URL, Model: "claude"})
			_, err := c.Complete(context.Background(), llm.Request{
				Messages: []llm.Message{
					{Role: llm.RoleSystem, Content: "sys"},
					{Role: llm.RoleUser, Content: "hi"},
					{Role: llm.RoleAssistant, Content: "ok"},
				},
				PromptCache: tc.cache,
			})
			if err != nil {
				t.Fatal(err)
			}
			got, body := cacheBreakpoints(t, raw)
			if got != tc.want {
				t.Fatalf("cache breakpoints = %d, want %d in %s", got, tc.want, raw)
			}
			if !tc.cache {
				if _, isString := body["system"].(string); !isString {
					t.Fatalf("system is not a string when caching is off: %s", raw)
				}
				return
			}
			sys, ok := body["system"].([]any)
			if !ok || len(sys) != 1 {
				t.Fatalf("system = %#v", body["system"])
			}
			block, _ := sys[0].(map[string]any)
			if block["text"] != "sys" {
				t.Fatalf("system block = %#v", block)
			}
			msgs, _ := body["messages"].([]any)
			last, _ := msgs[len(msgs)-1].(map[string]any)
			content, _ := last["content"].([]any)
			lastBlock, _ := content[len(content)-1].(map[string]any)
			if lastBlock["cache_control"] == nil {
				t.Fatalf("last message has no breakpoint: %s", raw)
			}
		})
	}
}

func TestAnthropicStreamPromptCacheBreakpoints(t *testing.T) {
	var raw []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
		fmt.Fprintf(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n")
		fmt.Fprintf(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srv.Close()

	c := llm.NewAnthropic(llm.Config{APIKey: "k", BaseURL: srv.URL, Model: "claude"})
	_, err := c.CompleteStream(context.Background(), llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "sys"},
			{Role: llm.RoleUser, Content: "hi"},
		},
		PromptCache: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := cacheBreakpoints(t, raw); got != 2 {
		t.Fatalf("stream breakpoints = %d in %s", got, raw)
	}
}
